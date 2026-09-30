package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/spk/spk-ocular/internal/core"
)

// nodePod is a running pod on node in ns; opts change it.
func nodePod(ns, name, uid, node string, opts ...func(o map[string]any)) *unstructured.Unstructured {
	o := map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": name, "namespace": ns, "uid": uid, "resourceVersion": "1", "labels": map[string]any{"app": name}},
		"spec":     map[string]any{"nodeName": node},
		"status":   map[string]any{"phase": "Running"},
	}
	for _, f := range opts {
		f(o)
	}
	return &unstructured.Unstructured{Object: o}
}

func ownedBy(kind, name, uid string) func(o map[string]any) {
	return func(o map[string]any) {
		o["metadata"].(map[string]any)["ownerReferences"] = []any{map[string]any{"apiVersion": "apps/v1", "kind": kind, "name": name, "uid": uid, "controller": true}}
	}
}

func withEmptyDir(vol string) func(o map[string]any) {
	return func(o map[string]any) {
		o["spec"].(map[string]any)["volumes"] = []any{map[string]any{"name": vol, "emptyDir": map[string]any{}}}
	}
}

func phase(p string) func(o map[string]any) {
	return func(o map[string]any) { o["status"].(map[string]any)["phase"] = p }
}

func mirror(o map[string]any) {
	o["metadata"].(map[string]any)["annotations"] = map[string]any{mirrorAnnotation: "x"}
}

func deleting(o map[string]any) {
	o["metadata"].(map[string]any)["deletionTimestamp"] = "2026-09-30T00:00:00Z"
}

func pdb(ns, name string, match map[string]any, allowed int64) *unstructured.Unstructured {
	spec := map[string]any{}
	if match != nil {
		spec["selector"] = map[string]any{"matchLabels": match}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "policy/v1", "kind": "PodDisruptionBudget",
		"metadata": map[string]any{"name": name, "namespace": ns, "uid": "uid-" + name},
		"spec":     spec, "status": map[string]any{"disruptionsAllowed": allowed},
	}}
}

func drainSession(t *testing.T, objs ...kruntime.Object) (*session, *dynamicfake.FakeDynamicClient) {
	t.Helper()
	c := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(kruntime.NewScheme(), map[schema.GroupVersionResource]string{
		podGVR: "PodList", nodeGVR: "NodeList", pdbGVR: "PodDisruptionBudgetList",
	}, objs...)
	s := newSession("ctx", "h", c, false)
	t.Cleanup(s.Close)
	return s, c
}

// itemsOf: a list's items as "name · note".
func itemsOf(l core.ActionList) []string {
	var out []string
	for _, it := range l.Items {
		s := it.Name
		if it.Note != nil {
			s += " · " + it.Note.Text
			for k, v := range it.Note.Params {
				s = strings.ReplaceAll(s, "{"+k+"}", v)
			}
		}
		out = append(out, s)
	}
	return out
}

func listTitled(t *testing.T, plan core.ActionPlan, key string) core.ActionList {
	t.Helper()
	for _, l := range plan.Lists {
		if l.Title.Key == "kubernetes."+key {
			return l
		}
	}
	require.Failf(t, "no list", "%s in %v", key, plan.Lists)
	return core.ActionList{}
}

func workerPods() []kruntime.Object {
	return []kruntime.Object{
		node("w1", "uid-w1", "5", false),
		nodePod("a", "web-1", "uid-web-1", "w1", ownedBy("ReplicaSet", "web-rs", "uid-rs"), withEmptyDir("cache")),
		nodePod("a", "api-1", "uid-api-1", "w1", ownedBy("StatefulSet", "api", "uid-api")),
		nodePod("a", "bare", "uid-bare", "w1"),
		nodePod("kube-system", "proxy-x", "uid-proxy", "w1", ownedBy("DaemonSet", "kube-proxy", "uid-ds")),
		nodePod("kube-system", "etcd-w1", "uid-etcd", "w1", mirror),
		nodePod("a", "job-1", "uid-job", "w1", ownedBy("Job", "once", "uid-jobc"), phase("Succeeded")),
		nodePod("a", "old-1", "uid-old", "w1", ownedBy("ReplicaSet", "web-rs", "uid-rs"), deleting),
		nodePod("a", "elsewhere", "uid-else", "w2", ownedBy("ReplicaSet", "web-rs", "uid-rs")),
	}
}

func TestADrainPlanSortsTheNodesPods(t *testing.T) {
	s, _ := drainSession(t, workerPods()...)
	plan := prepare(t, s, nodeRef("w1", ""), "drain", core.ActionParams{})
	assert.True(t, plan.Destructive)
	assert.Nil(t, plan.Unavailable)
	assert.Contains(t, text(plan), "no new pods are scheduled on node w1")
	assert.Contains(t, text(plan), "eviction is requested for 2 pods")
	assert.Contains(t, text(plan), "1 pod has no controller")

	assert.Equal(t, []string{"a/api-1 · StatefulSet api", "a/web-1 · ReplicaSet web-rs"}, itemsOf(listTitled(t, plan, "drain.list.evict")))
	assert.True(t, listTitled(t, plan, "drain.list.evict").Destructive)
	assert.Equal(t, []string{"a/web-1 · volumes cache"}, itemsOf(listTitled(t, plan, "drain.list.emptyDir")))
	assert.Equal(t, []string{"a/bare"}, itemsOf(listTitled(t, plan, "drain.list.bare")))
	left := listTitled(t, plan, "drain.list.left")
	assert.True(t, left.Collapsed)
	assert.Equal(t, []string{
		"a/job-1 · finished", "a/old-1 · being deleted",
		"kube-system/etcd-w1 · a static pod (the API cannot evict it)",
		"kube-system/proxy-x · a DaemonSet's (it would come back at once)",
	}, itemsOf(left))
	for _, l := range plan.Lists {
		assert.NotContains(t, itemsOf(l), "a/elsewhere", "a pod of another node (a server ignoring the field selector)")
		// Each item names its pod: an agent's run is checked against the
		// scopes it was granted (P14).
		for _, it := range l.Items {
			if assert.NotNil(t, it.Ref, it.Name) {
				assert.Equal(t, it.Name, it.Ref.Scope+"/"+it.Ref.Name)
				assert.Equal(t, core.Ref{Provider: ProviderID, Target: s.target, Scope: it.Ref.Scope, Kind: podsKind.desc.ID, Name: it.Ref.Name, UID: it.Ref.UID}, *it.Ref)
				assert.NotEmpty(t, it.Ref.UID, it.Name)
			}
		}
	}
}

// The plan's promise covers each pod's identity, class, controller and
// emptyDir volumes: another controller behind the same UIDs, a pod turning
// finished, a node cordoned meanwhile — another Expect. Status churn is not.
func TestADrainsExpectBindsWhatItPromises(t *testing.T) {
	s, c := drainSession(t, workerPods()...)
	ref := nodeRef("w1", "")
	was := prepare(t, s, ref, "drain", core.ActionParams{}).Expect
	update := func(obj *unstructured.Unstructured) {
		gvr := podGVR
		if obj.GetKind() == "Node" {
			gvr = nodeGVR
		}
		require.NoError(t, c.Tracker().Update(gvr, obj, obj.GetNamespace()))
	}

	update(nodePod("a", "web-1", "uid-web-1", "w1", ownedBy("ReplicaSet", "web-rs", "uid-rs"), withEmptyDir("cache"), func(o map[string]any) {
		o["metadata"].(map[string]any)["resourceVersion"] = "9"
		o["status"].(map[string]any)["conditions"] = []any{map[string]any{"type": "Ready", "status": "False"}}
	}))
	assert.Equal(t, was, prepare(t, s, ref, "drain", core.ActionParams{}).Expect, "status churn")

	update(nodePod("a", "web-1", "uid-web-1", "w1", ownedBy("ReplicaSet", "other-rs", "uid-rs2"), withEmptyDir("cache")))
	now := prepare(t, s, ref, "drain", core.ActionParams{}).Expect
	assert.NotEqual(t, was, now, "another controller, the same UIDs")

	update(nodePod("a", "api-1", "uid-api-1", "w1", ownedBy("StatefulSet", "api", "uid-api"), phase("Failed")))
	then := prepare(t, s, ref, "drain", core.ActionParams{}).Expect
	assert.NotEqual(t, now, then, "a pod left alone now")

	update(nodePod("a", "bare", "uid-bare", "w1", withEmptyDir("tmp")))
	again := prepare(t, s, ref, "drain", core.ActionParams{}).Expect
	assert.NotEqual(t, then, again, "a named pod's volumes")

	update(node("w1", "uid-w1", "6", true))
	assert.NotEqual(t, again, prepare(t, s, ref, "drain", core.ActionParams{}).Expect, "cordoned meanwhile")
}

func TestADrainThatCannotKnowAllPodsIsUnavailable(t *testing.T) {
	t.Run("too many", func(t *testing.T) {
		objs := []kruntime.Object{node("w1", "uid-w1", "5", false)}
		for i := range maxDrainPods + 1 {
			objs = append(objs, nodePod("a", fmt.Sprintf("p-%d", i), fmt.Sprintf("uid-%d", i), "w1", ownedBy("ReplicaSet", "rs", "uid-rs")))
		}
		s, _ := drainSession(t, objs...)
		plan := prepare(t, s, nodeRef("w1", ""), "drain", core.ActionParams{})
		require.NotNil(t, plan.Unavailable)
		assert.Equal(t, "kubernetes.drain.tooMany", plan.Unavailable.Key)
		assert.Empty(t, plan.Lists)
	})
	t.Run("list refused", func(t *testing.T) {
		s, c := drainSession(t, workerPods()...)
		c.PrependReactor("list", "pods", func(k8stesting.Action) (bool, kruntime.Object, error) {
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", errors.New("rbac"))
		})
		plan := prepare(t, s, nodeRef("w1", ""), "drain", core.ActionParams{})
		require.NotNil(t, plan.Unavailable)
		assert.Equal(t, "kubernetes.drain.podsUnreadable", plan.Unavailable.Key)
	})
	t.Run("list cut short", func(t *testing.T) {
		s, c := drainSession(t, workerPods()...)
		c.PrependReactor("list", "pods", func(k8stesting.Action) (bool, kruntime.Object, error) {
			l := &unstructured.UnstructuredList{Object: map[string]any{"apiVersion": "v1", "kind": "PodList"}}
			l.SetContinue("more")
			return true, l, nil
		})
		plan := prepare(t, s, nodeRef("w1", ""), "drain", core.ActionParams{})
		require.NotNil(t, plan.Unavailable)
		assert.Equal(t, "kubernetes.drain.tooMany", plan.Unavailable.Key)
	})
}

func TestADrainWithNothingToEvict(t *testing.T) {
	t.Run("open node: only the cordon", func(t *testing.T) {
		s, _ := drainSession(t, node("w1", "uid-w1", "5", false), nodePod("a", "bare", "uid-bare", "w1"))
		plan := prepare(t, s, nodeRef("w1", ""), "drain", core.ActionParams{})
		assert.Nil(t, plan.Unavailable)
		assert.Contains(t, text(plan), "only the cordon is written")
	})
	t.Run("cordoned node: nothing to do, the pods left are named", func(t *testing.T) {
		s, _ := drainSession(t, node("w1", "uid-w1", "5", true), nodePod("a", "bare", "uid-bare", "w1"))
		plan := prepare(t, s, nodeRef("w1", ""), "drain", core.ActionParams{})
		require.NotNil(t, plan.Unavailable)
		assert.Equal(t, "kubernetes.drain.nothing", plan.Unavailable.Key)
		assert.Equal(t, []string{"a/bare"}, itemsOf(listTitled(t, plan, "drain.list.bare")))
	})
}

// ssarRules answers SelfSubjectAccessReviews by rule and records them.
type ssarRules struct {
	mu    sync.Mutex
	asked []map[string]any
	delay time.Duration
}

// concurrency counts SelfSubjectAccessReviews in flight.
type concurrency struct {
	inflight, max atomic.Int32
	delay         time.Duration
}

type countingDyn struct {
	dynamic.Interface
	gate *concurrency
}

func (d countingDyn) Resource(gvr schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	r := d.Interface.Resource(gvr)
	if gvr != ssarGVR {
		return r
	}
	return countingRes{r, d.gate}
}

type countingRes struct {
	dynamic.NamespaceableResourceInterface
	gate *concurrency
}

func (r countingRes) Create(ctx context.Context, u *unstructured.Unstructured, o metav1.CreateOptions, sub ...string) (*unstructured.Unstructured, error) {
	n := r.gate.inflight.Add(1)
	defer r.gate.inflight.Add(-1)
	for {
		m := r.gate.max.Load()
		if n <= m || r.gate.max.CompareAndSwap(m, n) {
			break
		}
	}
	time.Sleep(r.gate.delay)
	return r.NamespaceableResourceInterface.Create(ctx, u, o, sub...)
}

func (r *ssarRules) install(c *dynamicfake.FakeDynamicClient, decide func(attrs map[string]any) bool) {
	c.PrependReactor("create", "selfsubjectaccessreviews", func(a k8stesting.Action) (bool, kruntime.Object, error) {
		u := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).DeepCopy()
		attrs, _, _ := unstructured.NestedMap(u.Object, "spec", "resourceAttributes")
		r.mu.Lock()
		r.asked = append(r.asked, attrs)
		r.mu.Unlock()
		if r.delay > 0 {
			time.Sleep(r.delay)
		}
		u.Object["status"] = map[string]any{"allowed": decide(attrs)}
		return true, u, nil
	})
}

func (r *ssarRules) askedAbout(resource string) []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []map[string]any
	for _, a := range r.asked {
		if a["resource"] == resource {
			out = append(out, a)
		}
	}
	return out
}

func TestDrainRightsAreOnlyThoseOfItsWrites(t *testing.T) {
	cordoned := func() []kruntime.Object {
		objs := workerPods()
		objs[0] = node("w1", "uid-w1", "5", true)
		return objs
	}
	t.Run("a cordoned node: no node patch asked, the evictions allowed", func(t *testing.T) {
		s, c := drainSession(t, cordoned()...)
		var r ssarRules
		r.install(c, func(a map[string]any) bool { return a["resource"] == "pods" })
		plan := prepare(t, s, nodeRef("w1", ""), "drain", core.ActionParams{})
		assert.Equal(t, core.RightsAllowed, plan.Rights.State)
		assert.Empty(t, r.askedAbout("nodes"), "no cordon in the plan: its right is not needed")
	})
	t.Run("evictions allowed for the named pods only", func(t *testing.T) {
		s, c := drainSession(t, cordoned()...)
		var r ssarRules
		r.install(c, func(a map[string]any) bool {
			return a["subresource"] == "eviction" && a["verb"] == "create" && a["group"] == "" && (a["name"] == "web-1" || a["name"] == "api-1")
		})
		plan := prepare(t, s, nodeRef("w1", ""), "drain", core.ActionParams{})
		assert.Equal(t, core.RightsAllowed, plan.Rights.State, "a namespace-wide refusal proves nothing")
		assert.Len(t, r.askedAbout("pods"), 3, "the namespace, then each pod by name")
	})
	t.Run("one pod denied: the plan is denied and names it", func(t *testing.T) {
		s, c := drainSession(t, workerPods()...)
		var r ssarRules
		r.install(c, func(a map[string]any) bool { return a["resource"] == "nodes" || a["name"] == "web-1" })
		plan := prepare(t, s, nodeRef("w1", ""), "drain", core.ActionParams{})
		assert.Equal(t, core.RightsDenied, plan.Rights.State)
		assert.Contains(t, plan.Rights.Reason, "you may not evict 1 of the pods")
		denied := listTitled(t, plan, "drain.list.denied")
		assert.Equal(t, []string{"a/api-1"}, itemsOf(denied))
		assert.Equal(t, &core.Ref{Provider: ProviderID, Target: s.target, Scope: "a", Kind: "pods", Name: "api-1", UID: "uid-api-1"}, denied.Items[0].Ref)
		assert.Len(t, r.askedAbout("nodes"), 1)
		assert.Equal(t, "w1", r.askedAbout("nodes")[0]["name"])
	})
	t.Run("the node patch denied", func(t *testing.T) {
		s, c := drainSession(t, workerPods()...)
		var r ssarRules
		r.install(c, func(a map[string]any) bool { return a["resource"] == "pods" })
		plan := prepare(t, s, nodeRef("w1", ""), "drain", core.ActionParams{})
		assert.Equal(t, core.RightsDenied, plan.Rights.State)
		assert.Contains(t, plan.Rights.Reason, "you may not patch nodes")
	})
	t.Run("at most 8 questions at a time", func(t *testing.T) {
		objs := []kruntime.Object{node("w1", "uid-w1", "5", true)}
		for i := range 40 {
			objs = append(objs, nodePod(fmt.Sprintf("ns-%d", i%4), fmt.Sprintf("p-%d", i), fmt.Sprintf("uid-%d", i), "w1", ownedBy("ReplicaSet", "rs", "uid-rs")))
		}
		_, c := drainSession(t, objs...)
		var r ssarRules
		r.install(c, func(a map[string]any) bool { return a["name"] != nil })
		// Counted outside the fake client (its reactors run under one lock).
		gate := &concurrency{delay: 5 * time.Millisecond}
		s := newSession("ctx", "h", countingDyn{c, gate}, false)
		t.Cleanup(s.Close)
		plan := prepare(t, s, nodeRef("w1", ""), "drain", core.ActionParams{})
		assert.Equal(t, core.RightsAllowed, plan.Rights.State)
		assert.Greater(t, gate.max.Load(), int32(1), "asked in parallel")
		assert.LessOrEqual(t, gate.max.Load(), int32(drainParallel))
		assert.Len(t, r.askedAbout("pods"), 44, "4 namespaces, then 40 pods")
	})
	t.Run("not answered in time: unknown", func(t *testing.T) {
		old := prepareExtrasTimeout
		prepareExtrasTimeout = 50 * time.Millisecond
		t.Cleanup(func() { prepareExtrasTimeout = old })
		s, c := drainSession(t, workerPods()...)
		r := ssarRules{delay: 200 * time.Millisecond}
		r.install(c, func(map[string]any) bool { return true })
		plan := prepare(t, s, nodeRef("w1", ""), "drain", core.ActionParams{})
		assert.Equal(t, core.RightsUnknown, plan.Rights.State)
	})
}

// lateDyn holds the SelfSubjectAccessReviews late() picks until the
// caller's context ends, then answers a little later still (a cancelled
// request unwinding) — outside the fake client's lock.
type lateDyn struct {
	dynamic.Interface
	late func(attrs map[string]any) bool
}

func (d lateDyn) Resource(gvr schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	r := d.Interface.Resource(gvr)
	if gvr != ssarGVR {
		return r
	}
	return lateRes{r, d.late}
}

type lateRes struct {
	dynamic.NamespaceableResourceInterface
	late func(attrs map[string]any) bool
}

func (r lateRes) Create(ctx context.Context, u *unstructured.Unstructured, o metav1.CreateOptions, sub ...string) (*unstructured.Unstructured, error) {
	attrs, _, _ := unstructured.NestedMap(u.Object, "spec", "resourceAttributes")
	if r.late(attrs) {
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond)
		return nil, ctx.Err()
	}
	return r.NamespaceableResourceInterface.Create(ctx, u, o, sub...)
}

func TestADrainsKnownDenialOutlivesALateCheck(t *testing.T) {
	old := prepareExtrasTimeout
	prepareExtrasTimeout = 80 * time.Millisecond
	t.Cleanup(func() { prepareExtrasTimeout = old })
	lateSession := func(t *testing.T, decide, late func(map[string]any) bool) *session {
		_, c := drainSession(t, workerPods()...)
		var r ssarRules
		r.install(c, decide)
		s := newSession("ctx", "h", lateDyn{c, late}, false)
		t.Cleanup(s.Close)
		return s
	}
	t.Run("the node patch denied, the evictions not answered in time", func(t *testing.T) {
		s := lateSession(t,
			func(a map[string]any) bool { return a["resource"] != "nodes" },
			func(a map[string]any) bool { return a["subresource"] == "eviction" })
		plan := prepare(t, s, nodeRef("w1", ""), "drain", core.ActionParams{})
		assert.Equal(t, core.RightsDenied, plan.Rights.State, "a known refusal is not made unknown by a late neighbour")
		assert.Contains(t, plan.Rights.Reason, "you may not patch nodes")
		assert.Contains(t, plan.Rights.Reason, "not all checked in time")
	})
	t.Run("one pod denied by name, another not answered in time", func(t *testing.T) {
		s := lateSession(t,
			func(a map[string]any) bool { return a["resource"] == "nodes" },
			func(a map[string]any) bool { return a["name"] == "web-1" })
		plan := prepare(t, s, nodeRef("w1", ""), "drain", core.ActionParams{})
		assert.Equal(t, core.RightsDenied, plan.Rights.State)
		assert.Contains(t, plan.Rights.Reason, "you may not evict 1 of the pods")
		assert.Equal(t, []string{"a/api-1"}, itemsOf(listTitled(t, plan, "drain.list.denied")), "the denied pod is named")
	})
	t.Run("nothing denied, one check late: unknown", func(t *testing.T) {
		s := lateSession(t,
			func(map[string]any) bool { return true },
			func(a map[string]any) bool { return a["subresource"] == "eviction" })
		plan := prepare(t, s, nodeRef("w1", ""), "drain", core.ActionParams{})
		assert.Equal(t, core.RightsUnknown, plan.Rights.State)
	})
}

// latePDBDyn holds PodDisruptionBudget lists until the caller's context
// ends — outside the fake client's lock.
type latePDBDyn struct{ dynamic.Interface }

func (d latePDBDyn) Resource(gvr schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	r := d.Interface.Resource(gvr)
	if gvr != pdbGVR {
		return r
	}
	return latePDBRes{r}
}

type latePDBRes struct {
	dynamic.NamespaceableResourceInterface
}

func (r latePDBRes) Namespace(string) dynamic.ResourceInterface { return r }

func (r latePDBRes) List(ctx context.Context, _ metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func cpuTime(t *testing.T) time.Duration {
	var u syscall.Rusage
	require.NoError(t, syscall.Getrusage(syscall.RUSAGE_SELF, &u))
	return time.Duration(u.Utime.Nano() + u.Stime.Nano())
}

// Rights answered, budgets late: the plan waits for the budgets without
// spinning (a closed channel is received once).
func TestADrainWaitingForItsBudgetsDoesNotSpin(t *testing.T) {
	old := prepareExtrasTimeout
	prepareExtrasTimeout = 400 * time.Millisecond
	t.Cleanup(func() { prepareExtrasTimeout = old })
	_, c := drainSession(t, workerPods()...)
	var r ssarRules
	r.install(c, func(map[string]any) bool { return true })
	s := newSession("ctx", "h", latePDBDyn{c}, false)
	t.Cleanup(s.Close)
	before := cpuTime(t)
	plan := prepare(t, s, nodeRef("w1", ""), "drain", core.ActionParams{})
	used := cpuTime(t) - before
	assert.Equal(t, core.RightsAllowed, plan.Rights.State)
	assert.Contains(t, fmt.Sprint(plan.Warnings), "drain.pdbUnchecked")
	assert.Less(t, used, 150*time.Millisecond, "waited %v of CPU for a 400 ms wait", used)
}

func TestDrainForecastsPodDisruptionBudgets(t *testing.T) {
	t.Run("none allowed now, two over one pod", func(t *testing.T) {
		objs := append(workerPods(),
			pdb("a", "web-pdb", map[string]any{"app": "web-1"}, 0),
			pdb("a", "api-pdb", map[string]any{"app": "api-1"}, 1),
			pdb("a", "api-too", map[string]any{"app": "api-1"}, 3),
			pdb("a", "none", nil, 0), // a null selector selects no pod
		)
		s, _ := drainSession(t, objs...)
		plan := prepare(t, s, nodeRef("w1", ""), "drain", core.ActionParams{})
		warn := strings.ToLower(strings.Join(core.Texts(plan.Warnings), "\n"))
		assert.Contains(t, warn, "poddisruptionbudget a/web-pdb allows no disruption now: the eviction of 1 pod may be refused")
		var blocking []string
		for _, w := range plan.Warnings {
			if w.Key == "kubernetes.drain.pdbBlocksOne" {
				blocking = append(blocking, w.Params["pdb"])
			}
			if w.Key == "kubernetes.drain.pdbMany" {
				assert.Equal(t, "a/api-1", w.Params["pod"])
			}
		}
		assert.Equal(t, []string{"a/web-pdb"}, blocking)
		assert.Contains(t, warn, "more than one poddisruptionbudget")
		assert.NotContains(t, warn, "not checked")
	})
	t.Run("unreadable: unchecked, never none", func(t *testing.T) {
		s, c := drainSession(t, workerPods()...)
		c.PrependReactor("list", "poddisruptionbudgets", func(k8stesting.Action) (bool, kruntime.Object, error) {
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "policy", Resource: "poddisruptionbudgets"}, "", errors.New("rbac"))
		})
		plan := prepare(t, s, nodeRef("w1", ""), "drain", core.ActionParams{})
		assert.Contains(t, text(plan), "poddisruptionbudgets were not checked")
	})
}
