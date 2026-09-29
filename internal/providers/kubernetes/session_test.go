package kubernetes

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/views"
)

var podGVR = schema.GroupVersionResource{Version: "v1", Resource: "pods"}

func pod(ns, name, uid string, mutate ...func(map[string]any)) *unstructured.Unstructured {
	o := map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{
			"name": name, "namespace": ns, "uid": uid,
			"creationTimestamp": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
			"annotations":       map[string]any{"kubectl.kubernetes.io/last-applied-configuration": "{huge}"},
			"managedFields":     []any{map[string]any{"manager": "kubectl"}},
		},
		"spec": map[string]any{"nodeName": "node-1", "containers": []any{map[string]any{"name": "app", "image": "nginx", "env": []any{map[string]any{"name": "PASSWORD", "value": "s3cret"}}}}},
		"status": map[string]any{
			"phase":             "Running",
			"conditions":        []any{map[string]any{"type": "Ready", "status": "True"}},
			"containerStatuses": []any{map[string]any{"name": "app", "ready": true, "restartCount": int64(0), "state": map[string]any{"running": map[string]any{}}}},
		},
	}
	for _, m := range mutate {
		m(o)
	}
	return &unstructured.Unstructured{Object: o}
}

func fakeClient(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{podGVR: "PodList", namespacesGVR: "NamespaceList"}, objs...)
}

type harness struct {
	t    *testing.T
	sess *session
	m    *views.Manager
}

func newHarness(t *testing.T, client *dynamicfake.FakeDynamicClient) *harness {
	t.Helper()
	s := newSession("ctx", "h", client, false)
	m := views.NewManager(events.NewEmitter())
	t.Cleanup(func() { m.CloseAll(); s.Close() })
	return &harness{t: t, sess: s, m: m}
}

func (h *harness) open(kind string, scope core.ScopeSel) string {
	h.t.Helper()
	id, err := h.m.Open("s", h.sess, provider.Query{Kind: kind, Scope: scope})
	require.NoError(h.t, err)
	return id
}

// until pulls the view until cond holds on a full snapshot.
func (h *harness) until(id string, cond func(views.Page) bool) views.Page {
	h.t.Helper()
	var last views.Page
	require.Eventually(h.t, func() bool {
		p, err := h.m.Get(id, 0)
		require.NoError(h.t, err)
		last = p
		return cond(p)
	}, 8*time.Second, 10*time.Millisecond, "last page: %+v", last)
	return last
}

func names(p views.Page) []string {
	out := []string{}
	for _, r := range p.Upserts {
		out = append(out, r.Ref.Name)
	}
	return out
}

var all = core.ScopeSel{Mode: core.ScopeAll}

func isReady(p views.Page) bool { return p.Status.State == provider.StatusReady }

func TestWatchDeliversSnapshotThenReadyThenChanges(t *testing.T) {
	client := fakeClient(pod("web", "a", "uid-a"), pod("web", "b", "uid-b"))
	h := newHarness(t, client)
	id := h.open("pods", all)
	p := h.until(id, isReady)
	assert.ElementsMatch(t, []string{"a", "b"}, names(p), "ready only after the initial rows are in")

	_, err := client.Resource(podGVR).Namespace("web").Create(context.Background(), pod("web", "c", "uid-c"), metav1.CreateOptions{})
	require.NoError(t, err)
	require.NoError(t, client.Resource(podGVR).Namespace("web").Delete(context.Background(), "a", metav1.DeleteOptions{}))
	p = h.until(id, func(p views.Page) bool { return len(p.Upserts) == 2 && names(p)[0] != "a" })
	assert.ElementsMatch(t, []string{"b", "c"}, names(p))
	assert.Equal(t, "uid-c", findRow(p, "c").ID, "row id is the UID")
}

func findRow(p views.Page, name string) core.Row {
	for _, r := range p.Upserts {
		if r.Ref.Name == name {
			return r
		}
	}
	return core.Row{}
}

func TestEmptyListStillBecomesReady(t *testing.T) {
	h := newHarness(t, fakeClient())
	p := h.until(h.open("pods", all), isReady)
	assert.Empty(t, p.Upserts)
}

func TestForbiddenIsAnErrorNotAnEmptyTable(t *testing.T) {
	client := fakeClient()
	client.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", assert.AnError)
	})
	h := newHarness(t, client)
	p := h.until(h.open("pods", all), func(p views.Page) bool { return p.Status.State == provider.StatusError })
	assert.Equal(t, provider.ClassForbidden, p.Status.Class)
	assert.Contains(t, p.Status.Message, "forbidden")
}

func TestOneScopeUsesANamespacedCache(t *testing.T) {
	client := fakeClient(pod("web", "a", "uid-a"), pod("db", "x", "uid-x"))
	h := newHarness(t, client)
	p := h.until(h.open("pods", core.ScopeSel{Mode: core.ScopeOne, Name: "db"}), isReady)
	assert.Equal(t, []string{"x"}, names(p))
}

func TestSecondViewOnWarmCacheGetsSnapshot(t *testing.T) {
	client := fakeClient(pod("web", "a", "uid-a"))
	h := newHarness(t, client)
	h.until(h.open("pods", all), isReady)
	active, _ := h.sess.caches.stats()
	require.Equal(t, 1, active)
	p := h.until(h.open("pods", all), isReady)
	assert.Equal(t, []string{"a"}, names(p))
	active, _ = h.sess.caches.stats()
	assert.Equal(t, 1, active, "one shared informer")
}

func TestIdleCachesAreEvictedAfterGraceAndBeyondLimit(t *testing.T) {
	h := newHarness(t, fakeClient(pod("a", "p", "u1")))
	h.sess.caches.grace = 150 * time.Millisecond
	h.sess.caches.maxIdle = 1
	v1 := h.open("pods", core.ScopeSel{Mode: core.ScopeOne, Name: "a"})
	v2 := h.open("pods", core.ScopeSel{Mode: core.ScopeOne, Name: "b"})
	h.until(v1, isReady)
	h.until(v2, isReady)
	h.m.Close(v1)
	h.m.Close(v2)
	active, idle := h.sess.caches.stats()
	assert.Equal(t, 0, active)
	assert.Equal(t, 1, idle, "only maxIdle idle caches survive")
	require.Eventually(t, func() bool { _, idle := h.sess.caches.stats(); return idle == 0 }, 2*time.Second, 10*time.Millisecond)

	// Reopening after eviction starts a fresh informer (stopped ones cannot restart).
	p := h.until(h.open("pods", core.ScopeSel{Mode: core.ScopeOne, Name: "a"}), isReady)
	assert.Equal(t, []string{"p"}, names(p))
}

func TestTimeBasedHealthChangesWithoutAPIEvents(t *testing.T) {
	// Kubernetes timestamps have second precision: leave a margin above that.
	created := time.Now().Add(-podPendingWarnAfter + 2*time.Second)
	pending := pod("web", "p", "uid-p", func(o map[string]any) {
		o["metadata"].(map[string]any)["creationTimestamp"] = created.UTC().Format(time.RFC3339Nano)
		o["status"] = map[string]any{"phase": "Pending"}
	})
	h := newHarness(t, fakeClient(pending))
	id := h.open("pods", all)
	p := h.until(id, isReady)
	assert.Equal(t, core.HealthProgressing, p.Upserts[0].Health.State)
	p = h.until(id, func(p views.Page) bool { return len(p.Upserts) == 1 && p.Upserts[0].Health.State == core.HealthWarning })
	assert.Equal(t, "Pending", p.Upserts[0].Health.Reason)
}

func TestReplacementUnderSameNameDropsOldRow(t *testing.T) {
	rec := &recordingSink{}
	w := &viewWatch{def: podsKind, sink: rec, target: "t", now: time.Now, done: make(chan struct{}), deadlines: map[string]deadline{}}
	w.handlers().UpdateFunc(pod("web", "web-0", "old"), pod("web", "web-0", "new"))
	require.Len(t, rec.d, 1)
	assert.Equal(t, []string{"old"}, rec.d[0].Deletes)
	assert.Equal(t, "new", rec.d[0].Upserts[0].ID)
}

type recordingSink struct{ d []provider.Delta }

func (r *recordingSink) Apply(d provider.Delta) { r.d = append(r.d, d) }

func TestTrimDropsNoiseAndSecrets(t *testing.T) {
	u := trim(pod("web", "a", "uid-a"), podsKind.keep)
	_, hasMF, _ := unstructured.NestedFieldNoCopy(u.Object, "metadata", "managedFields")
	_, hasAnn, _ := unstructured.NestedFieldNoCopy(u.Object, "metadata", "annotations")
	assert.False(t, hasMF)
	assert.False(t, hasAnn)
	c := slice(u.Object, "spec", "containers")[0]
	assert.Equal(t, "app", c["name"])
	_, hasEnv := c["env"]
	assert.False(t, hasEnv, "env values (may be secrets) are not cached")
	assert.Equal(t, u.Object, trim(u, podsKind.keep).Object, "idempotent")
}

func TestScopesForbiddenIsClassified(t *testing.T) {
	client := fakeClient()
	client.PrependReactor("list", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "namespaces"}, "", assert.AnError)
	})
	h := newHarness(t, client)
	_, err := h.sess.Scopes(context.Background())
	var pe *provider.Error
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, provider.ClassForbidden, pe.Class)
}

// Stats count what a soak must see: list and watch requests (a relist
// shows as another list or initial sync), watchers and pending deadlines.
func TestStatsCountRequestsWatchersAndDeadlines(t *testing.T) {
	client := fakeClient(pod("web", "p", "uid-p", func(o map[string]any) {
		o["status"] = map[string]any{"phase": "Pending"}
		o["metadata"].(map[string]any)["creationTimestamp"] = time.Now().UTC().Format(time.RFC3339)
	}))
	h := newHarness(t, client)
	id := h.open("pods", all)
	h.until(id, isReady)
	require.Eventually(t, func() bool { return h.sess.Stats()["cache_watch_starts"] >= 1 }, 5*time.Second, 10*time.Millisecond)
	st := h.sess.Stats()
	assert.GreaterOrEqual(t, st["cache_lists"]+st["cache_initial_syncs"], 1)
	assert.Equal(t, 1, st["watchers"])
	assert.Equal(t, 1, st["deadlines"], "a fresh Pending pod turns into a warning later")
}
