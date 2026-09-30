package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

var debugRef = core.Ref{Provider: ProviderID, Target: "ctx", Scope: "ns", Kind: "pods", Name: "web-1", UID: "uid-p"}

// debugPod: web-1, running, containers app (running) and side
// (crash-looping), with spec changes merged.
func debugPod(spec map[string]any, meta map[string]any) *unstructured.Unstructured {
	s := map[string]any{"containers": []any{
		map[string]any{"name": "app", "image": "distroless/app:1"},
		map[string]any{"name": "side", "image": "sidecar:2"},
	}}
	for k, v := range spec {
		s[k] = v
	}
	md := map[string]any{"name": "web-1", "namespace": "ns", "uid": "uid-p", "resourceVersion": "7"}
	for k, v := range meta {
		md[k] = v
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Pod", "metadata": md, "spec": s,
		"status": map[string]any{"phase": "Running", "containerStatuses": []any{
			map[string]any{"name": "app", "state": map[string]any{"running": map[string]any{}}},
			map[string]any{"name": "side", "state": map[string]any{"waiting": map[string]any{"reason": "CrashLoopBackOff"}}},
		}},
	}}
}

func nsObject(labels map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": "ns", "labels": labels}}}
}

func debugSession(t *testing.T, objs ...kruntime.Object) (*session, *dynamicfake.FakeDynamicClient) {
	t.Helper()
	c := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(kruntime.NewScheme(), map[schema.GroupVersionResource]string{
		podGVR: "PodList", namespaceGV: "NamespaceList",
	}, objs...)
	s := newSession("ctx", "h", c, false)
	t.Cleanup(s.Close)
	return s, c
}

func strp(v string) *string { return &v }

func prepareDebugPlan(t *testing.T, s *session, image, target string) core.ActionPlan {
	t.Helper()
	p := core.ActionParams{}
	if image != "" {
		p.Text = strp(image)
	}
	if target != "" {
		p.Choice = strp(target)
	}
	return prepare(t, s, debugRef, "debug", p)
}

func runDebugPlan(s *session, plan core.ActionPlan) (core.ActionResult, error) {
	return s.RunAction(context.Background(), provider.ActionRun{Ref: plan.Where.Ref, Action: "debug", Params: plan.Params, Expect: plan.Expect})
}

func TestDebugPlanOffersTheTargetsWithTheDefaultChosen(t *testing.T) {
	s, _ := debugSession(t, debugPod(nil, nil), nsObject(nil))
	plan := prepareDebugPlan(t, s, "", "")
	require.Nil(t, plan.Unavailable)
	assert.Equal(t, "busybox:1.36", *plan.Params.Text, "the default image")
	assert.Equal(t, "app", *plan.Params.Choice, "the first container")
	require.Len(t, plan.Choices, 2)
	assert.Equal(t, "side", plan.Choices[1].Value)
	assert.Nil(t, plan.Choices[1].Unavailable, "a crash-looping target is the usual reason to debug")
	assert.Contains(t, core.Texts(plan.Choices[1].Details), "State: waiting: CrashLoopBackOff")
	assert.False(t, plan.Destructive)
	require.Len(t, plan.Effects, 4)
	assert.Regexp(t, `^Debug container debugger-[a-z0-9]{5} with image busybox:1.36 is added to pod web-1\.$`, plan.Effects[0].Text)
	assert.Equal(t, ProviderID+".debug.sees", plan.Effects[1].Key)
	assert.Equal(t, []string{ProviderID + ".debug.registry"}, msgKeys(plan.Warnings), "no restricted warning")

	annotated := debugPod(nil, map[string]any{"annotations": map[string]any{defaultContainerAnnotation: "side"}})
	s, _ = debugSession(t, annotated, nsObject(nil))
	assert.Equal(t, "side", *prepareDebugPlan(t, s, "", "").Params.Choice, "the default-container annotation")

	shared := debugPod(map[string]any{"shareProcessNamespace": true}, nil)
	s, _ = debugSession(t, shared, nsObject(nil))
	plan = prepareDebugPlan(t, s, "nicolaka/netshoot", noTarget)
	require.Nil(t, plan.Unavailable)
	assert.Equal(t, noTarget, plan.Choices[2].Value, "no target: the pod shares its process namespace")
	assert.Equal(t, ProviderID+".debug.seesAll", plan.Effects[1].Key)
}

// A sidecar (a restartable init container) is a target whatever its state
// — crash-looping is the usual reason to debug it; plain init and debug
// containers are not.
func TestDebugOffersACrashLoopingSidecar(t *testing.T) {
	p := debugPod(map[string]any{
		"initContainers": []any{
			map[string]any{"name": "setup", "image": "setup:1"},
			map[string]any{"name": "mesh", "image": "mesh:3", "restartPolicy": "Always"},
		},
		"ephemeralContainers": []any{map[string]any{"name": "debugger-old", "image": "busybox:1.36"}},
	}, nil)
	p.Object["status"].(map[string]any)["initContainerStatuses"] = []any{
		map[string]any{"name": "setup", "state": map[string]any{"terminated": map[string]any{"reason": "Completed"}}},
		map[string]any{"name": "mesh", "state": map[string]any{"waiting": map[string]any{"reason": "CrashLoopBackOff"}}},
	}
	s, _ := debugSession(t, p, nsObject(nil))
	plan := prepareDebugPlan(t, s, "", "mesh")
	require.Nil(t, plan.Unavailable)
	var values []string
	for _, c := range plan.Choices {
		values = append(values, c.Value)
	}
	assert.Equal(t, []string{"app", "side", "mesh"}, values)
	assert.Equal(t, []string{"Image: mesh:3", "State: waiting: CrashLoopBackOff"}, core.Texts(plan.Choices[2].Details))
	assert.Equal(t, "app", *prepareDebugPlan(t, s, "", "").Params.Choice, "the default stays a regular container")
}

func msgKeys(ms []core.Message) []string {
	out := []string{}
	for _, m := range ms {
		out = append(out, m.Key)
	}
	return out
}

func TestDebugIsUnavailable(t *testing.T) {
	for name, c := range map[string]struct {
		pod    *unstructured.Unstructured
		image  string
		target string
		key    string
	}{
		"pending": {func() *unstructured.Unstructured {
			p := debugPod(nil, nil)
			p.Object["status"].(map[string]any)["phase"] = "Pending"
			return p
		}(), "", "", "debug.notRunning"},
		"static":                    {debugPod(nil, map[string]any{"annotations": map[string]any{mirrorKey: "x"}}), "", "", "debug.mirror"},
		"bad image":                 {debugPod(nil, nil), "busybox 1.36", "", "debug.badImage"},
		"no such":                   {debugPod(nil, nil), "", "db", "debug.noSuchTarget"},
		"no target without sharing": {debugPod(nil, nil), "", noTarget, "debug.noSuchTarget"},
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := debugSession(t, c.pod, nsObject(nil))
			plan := prepareDebugPlan(t, s, c.image, c.target)
			require.NotNil(t, plan.Unavailable)
			assert.Equal(t, ProviderID+"."+c.key, plan.Unavailable.Key)
		})
	}
}

// Only an enforced restricted level rejects a debugger without a
// securityContext; a label that cannot be read changes nothing.
func TestDebugWarnsOfTheRestrictedLevelOnly(t *testing.T) {
	for name, c := range map[string]struct {
		ns   *unstructured.Unstructured
		want bool
	}{
		"restricted":   {nsObject(map[string]any{enforceKey: "restricted"}), true},
		"baseline":     {nsObject(map[string]any{enforceKey: "baseline"}), false},
		"warn only":    {nsObject(map[string]any{"pod-security.kubernetes.io/warn": "restricted"}), false},
		"not readable": {nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			objs := []kruntime.Object{debugPod(nil, nil)}
			if c.ns != nil {
				objs = append(objs, c.ns)
			}
			s, _ := debugSession(t, objs...)
			plan := prepareDebugPlan(t, s, "", "")
			require.Nil(t, plan.Unavailable)
			assert.Equal(t, c.want, strings.Contains(strings.Join(msgKeys(plan.Warnings), ","), "debug.restricted"))
		})
	}
}

type patchBody struct {
	Metadata struct{ UID, ResourceVersion string } `json:"metadata"`
	Spec     struct {
		EphemeralContainers []map[string]any `json:"ephemeralContainers"`
	} `json:"spec"`
}

func debugPatches(t *testing.T, c *dynamicfake.FakeDynamicClient) []patchBody {
	t.Helper()
	var out []patchBody
	for _, w := range writes(c) {
		p := w.(k8stesting.PatchAction)
		require.Equal(t, types.StrategicMergePatchType, p.GetPatchType())
		require.Equal(t, "ephemeralcontainers", p.GetSubresource())
		var b patchBody
		require.NoError(t, json.Unmarshal(p.GetPatch(), &b))
		out = append(out, b)
	}
	return out
}

// acceptEphemeral applies a debug patch to the stored pod (the fake
// tracker does not know the subresource), bumping its version.
func acceptEphemeral(t *testing.T, c *dynamicfake.FakeDynamicClient) {
	c.PrependReactor("patch", "pods", func(a k8stesting.Action) (bool, kruntime.Object, error) {
		var b patchBody
		require.NoError(t, json.Unmarshal(a.(k8stesting.PatchAction).GetPatch(), &b))
		o, err := c.Tracker().Get(podGVR, "ns", "web-1")
		require.NoError(t, err)
		u := o.(*unstructured.Unstructured).DeepCopy()
		list, _ := u.Object["spec"].(map[string]any)["ephemeralContainers"].([]any)
		for _, e := range b.Spec.EphemeralContainers {
			list = append(list, e)
		}
		u.Object["spec"].(map[string]any)["ephemeralContainers"] = list
		u.SetResourceVersion("99")
		require.NoError(t, c.Tracker().Update(podGVR, u, "ns"))
		return true, u, nil
	})
}

func TestDebugAddsOneEphemeralContainerAndOpensItsTerminal(t *testing.T) {
	s, c := debugSession(t, debugPod(nil, nil), nsObject(nil))
	acceptEphemeral(t, c)
	plan := prepareDebugPlan(t, s, "busybox:1.36", "side")
	name := strings.Fields(plan.Effects[0].Text)[2]
	res, err := runDebugPlan(s, plan)
	require.NoError(t, err)
	assertDone(t, res, "done.debug", map[string]string{"kind": "pod", "name": "web-1", "container": name})
	assert.Equal(t, &core.TerminalOpen{Ref: debugRef, Channel: name, Attach: true}, res.Terminal)
	bodies := debugPatches(t, c)
	require.Len(t, bodies, 1)
	assert.Equal(t, "uid-p", bodies[0].Metadata.UID)
	assert.Equal(t, "7", bodies[0].Metadata.ResourceVersion)
	assert.Equal(t, []map[string]any{{
		"name": name, "image": "busybox:1.36", "targetContainerName": "side",
		"stdin": true, "tty": true, "imagePullPolicy": "IfNotPresent",
	}}, bodies[0].Spec.EphemeralContainers)

	// One review, one container: the same plan again is refused.
	_, err = runDebugPlan(s, plan)
	assertSaid(t, err, provider.ClassConflict, "run.spent")
	_, err = runDebugPlan(s, prepareDebugPlan(t, s, "busybox:1.36", "side"))
	require.NoError(t, err, "a new review names another")
	assert.Len(t, debugPatches(t, c), 2)
}

func TestDebugRefusesAForgedOrStaleGrant(t *testing.T) {
	s, c := debugSession(t, debugPod(nil, nil), nsObject(nil))
	plan := prepareDebugPlan(t, s, "busybox:1.36", "app")
	other := plan
	other.Params = core.ActionParams{Text: strp("evil:latest"), Choice: strp("app")}
	_, err := runDebugPlan(s, other)
	assertSaid(t, err, provider.ClassInvalid, "run.invalid")
	tampered := plan
	tampered.Expect = plan.Expect[:len(plan.Expect)-2] + "00"
	_, err = runDebugPlan(s, tampered)
	assertSaid(t, err, provider.ClassInvalid, "run.invalid")
	s.now = func() time.Time { return time.Now().Add(runGrantTTL + time.Second) }
	_, err = runDebugPlan(s, plan)
	assertSaid(t, err, provider.ClassConflict, "run.expired")
	assert.Empty(t, writes(c))
}

func TestDebugRefusesAPodChangedSinceThePlan(t *testing.T) {
	s, c := debugSession(t, debugPod(nil, nil), nsObject(nil))
	plan := prepareDebugPlan(t, s, "", "")
	bumpVersion(t, c, podGVR, "web-1", "8", func(u *unstructured.Unstructured) {
		u.Object["spec"].(map[string]any)["ephemeralContainers"] = []any{map[string]any{"name": "debugger-other", "image": "x"}}
	})()
	_, err := runDebugPlan(s, plan)
	assertSaid(t, err, provider.ClassConflict, "error.changed")
	assert.Empty(t, writes(c))
}

// A failed version precondition (a pod's status moves often) is retried;
// our container found at the re-read is done; another's of that name is a
// conflict; a replaced pod is said as such; a lost answer is unknown.
func TestDebugFailedWritesAreClassifiedByLookingAgain(t *testing.T) {
	conflictPod := apierrors.NewConflict(schema.GroupResource{Resource: "pods"}, "web-1", errors.New("the object has been modified"))
	for name, c := range map[string]struct {
		fail   error
		mutate func(t *testing.T, c *dynamicfake.FakeDynamicClient, container string) func()
		calls  int
		class  provider.ErrorClass
		key    string
	}{
		"status churn": {conflictPod, func(t *testing.T, c *dynamicfake.FakeDynamicClient, _ string) func() {
			return bumpVersion(t, c, podGVR, "web-1", "8", nil)
		}, 2, "", ""},
		"ours landed": {conflictPod, func(t *testing.T, c *dynamicfake.FakeDynamicClient, n string) func() {
			return bumpVersion(t, c, podGVR, "web-1", "8", func(u *unstructured.Unstructured) {
				u.Object["spec"].(map[string]any)["ephemeralContainers"] = []any{map[string]any{"name": n, "image": "busybox:1.36", "targetContainerName": "app", "stdin": true, "tty": true}}
			})
		}, 1, "", ""},
		"another of that name": {conflictPod, func(t *testing.T, c *dynamicfake.FakeDynamicClient, n string) func() {
			return bumpVersion(t, c, podGVR, "web-1", "8", func(u *unstructured.Unstructured) {
				u.Object["spec"].(map[string]any)["ephemeralContainers"] = []any{map[string]any{"name": n, "image": "other", "targetContainerName": "app", "stdin": true, "tty": true}} // the image alone differs
			})
		}, 1, provider.ClassConflict, "debug.nameTaken"},
		"replaced": {invalid422, func(t *testing.T, c *dynamicfake.FakeDynamicClient, _ string) func() {
			return func() {
				require.NoError(t, c.Tracker().Delete(podGVR, "ns", "web-1"))
				p := debugPod(nil, nil)
				p.SetUID("uid-new")
				require.NoError(t, c.Tracker().Create(podGVR, p, "ns"))
			}
		}, 1, provider.ClassGone, "error.replacedMeanwhile"},
		"refused":   {invalid422, nil, 1, provider.ClassInvalid, ""},
		"no answer": {errors.New("read tcp: connection reset by peer"), nil, 1, provider.ClassUnknown, "debug.unknown"},
	} {
		t.Run(name, func(t *testing.T) {
			s, cl := debugSession(t, debugPod(nil, nil), nsObject(nil))
			plan := prepareDebugPlan(t, s, "busybox:1.36", "app")
			container := strings.Fields(plan.Effects[0].Text)[2]
			var mutate func()
			if c.mutate != nil {
				mutate = c.mutate(t, cl, container)
			}
			acceptEphemeral(t, cl)                             // a write that passes lands
			calls := failWrite(cl, "patch", 1, mutate, c.fail) // the first fails
			res, err := runDebugPlan(s, plan)
			if c.class == "" {
				require.NoError(t, err)
				assert.Equal(t, container, res.Terminal.Channel)
			} else {
				assertSaid(t, err, c.class, c.key)
			}
			assert.Equal(t, c.calls, *calls, "writes sent")
		})
	}
}

// Debug asks for the write and for the attach its terminal needs.
func TestDebugRightsAskForTheAttachToo(t *testing.T) {
	s, c := debugSession(t, debugPod(nil, nil), nsObject(nil))
	var asked []map[string]any
	c.PrependReactor("create", "selfsubjectaccessreviews", func(a k8stesting.Action) (bool, kruntime.Object, error) {
		u := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).DeepCopy()
		ra, _, _ := unstructured.NestedMap(u.Object, "spec", "resourceAttributes")
		asked = append(asked, ra)
		u.Object["status"] = map[string]any{"allowed": ra["subresource"] == "ephemeralcontainers"}
		return true, u, nil
	})
	plan := prepareDebugPlan(t, s, "", "")
	assert.Equal(t, core.RightsDenied, plan.Rights.State)
	assert.Contains(t, plan.Rights.Reason, "create pods/attach in ns")
	require.Len(t, asked, 2)
	assert.Equal(t, "patch", asked[0]["verb"])
	assert.Equal(t, "ephemeralcontainers", asked[0]["subresource"])
}

// The terminal waits for the debugger's start by watching its pod: a user
// who may patch and attach but not watch is told so in the plan.
func TestDebugRightsAskForTheWatchOfThePod(t *testing.T) {
	s, c := debugSession(t, debugPod(nil, nil), nsObject(nil))
	var asked []map[string]any
	c.PrependReactor("create", "selfsubjectaccessreviews", func(a k8stesting.Action) (bool, kruntime.Object, error) {
		u := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).DeepCopy()
		ra, _, _ := unstructured.NestedMap(u.Object, "spec", "resourceAttributes")
		asked = append(asked, ra)
		u.Object["status"] = map[string]any{"allowed": ra["verb"] != "watch"}
		return true, u, nil
	})
	plan := prepareDebugPlan(t, s, "", "")
	assert.Equal(t, core.RightsDenied, plan.Rights.State)
	assert.Contains(t, plan.Rights.Reason, "watch pods")
	require.Len(t, asked, 3)
	assert.Equal(t, map[string]any{"verb": "watch", "group": "", "resource": "pods", "namespace": "ns", "name": "web-1"}, asked[2])
}
