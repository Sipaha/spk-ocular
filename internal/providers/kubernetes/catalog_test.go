package kubernetes

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

const v2ContentType = "application/json;g=apidiscovery.k8s.io;v=v2;as=APIGroupDiscoveryList"

// scriptedAPI serves discovery documents that tests change between runs;
// a path without a document fails.
type scriptedAPI struct {
	mu    sync.Mutex
	docs  map[string][]byte
	calls atomic.Int32
	// gate, if set, holds every request until it is closed or the
	// request's context ends.
	gate chan struct{}
}

func (a *scriptedAPI) set(path string, doc []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.docs == nil {
		a.docs = map[string][]byte{}
	}
	if doc == nil {
		delete(a.docs, path)
		return
	}
	a.docs[path] = doc
}

// get answers with the document as it is when asked (a held request
// answers what it read before a later change).
func (a *scriptedAPI) get(ctx context.Context, path, _ string) ([]byte, string, error) {
	a.calls.Add(1)
	a.mu.Lock()
	gate := a.gate
	doc, ok := a.docs[path]
	a.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, "", ctx.Err()
		}
	}
	if !ok {
		return nil, "", &discoveryError{code: 503, path: path, body: "unavailable"}
	}
	return doc, v2ContentType, nil
}

func (a *scriptedAPI) setGate(g chan struct{}) { a.mu.Lock(); a.gate = g; a.mu.Unlock() }

var (
	coreDoc = v2doc(map[string][]v2ver{"": {{version: "v1", res: []v2res{
		{name: "pods", kind: "Pod", singular: "pod", scope: "Namespaced", verbs: lw, short: []string{"po"}},
		{name: "persistentvolumeclaims", kind: "PersistentVolumeClaim", singular: "persistentvolumeclaim", scope: "Namespaced", verbs: lw, short: []string{"pvc"}},
		{name: "bindings", kind: "Binding", scope: "Namespaced", verbs: []string{"create"}},
	}}}}, "")
	jobsV1  = v2ver{version: "v1", res: []v2res{{name: "jobs", kind: "Job", singular: "job", scope: "Namespaced", verbs: lw}}}
	appsV1  = v2ver{version: "v1", res: []v2res{{name: "deployments", kind: "Deployment", singular: "deployment", scope: "Namespaced", verbs: lw, short: []string{"deploy"}}}}
	widgets = v2ver{version: "v1", res: []v2res{
		{name: "widgets", kind: "Widget", singular: "widget", scope: "Namespaced", verbs: lw, short: []string{"wd"}},
		{name: "gadgets", kind: "Gadget", singular: "gadget", scope: "Cluster", verbs: []string{"get", "list", "watch"}},
	}}
	// metrics.k8s.io style: pods and nodes of another group, list without watch
	metricsV1 = v2ver{version: "v1beta1", res: []v2res{{name: "pods", kind: "PodMetrics", singular: "pod", scope: "Namespaced", verbs: []string{"get", "list"}}}}
	// a watchable resource of another group whose singular is a static kind's alias
	lookalike = v2ver{version: "v1", res: []v2res{{name: "pods", kind: "Pod", singular: "pod", scope: "Namespaced", verbs: lw, short: []string{"po", "xp"}}}}
)

func apisDoc(groups map[string][]v2ver, order ...string) []byte { return v2doc(groups, order...) }

func kindIDs(r *kindRegistry) map[string]*kindDef { return r.byID }

// waitRev waits for the catalog to reach at least rev.
func waitRev(t *testing.T, c *catalog, rev uint64) *catalogSnap {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s := c.snap(); s.rev >= rev {
			return s
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("catalog stayed at rev %d (%s)", c.snap().rev, c.snap().state)
	return nil
}

// The described kinds are there at once, before and without any I/O.
func TestCatalogStartsWithTheDescribedKindsWithoutIO(t *testing.T) {
	api := &scriptedAPI{}
	c := newCatalog(context.Background(), allKinds, api.get)
	s := c.snap()
	assert.Equal(t, catalogDiscovering, s.state)
	assert.Len(t, s.reg.list, len(allKinds.list))
	assert.Zero(t, api.calls.Load())

	offline := newCatalog(context.Background(), allKinds, nil)
	offline.refresh()
	assert.Equal(t, catalogReady, offline.snap().state, "no server, nothing to discover")
}

func TestCatalogAddsServedResourcesTheDescribedKindsDoNotCover(t *testing.T) {
	api := &scriptedAPI{}
	api.set("/api", coreDoc)
	api.set("/apis", apisDoc(map[string][]v2ver{
		"apps": {appsV1}, "batch": {jobsV1}, "ocular.dev": {widgets}, "metrics.k8s.io": {metricsV1}, "other.example": {lookalike},
	}, "apps", "batch", "ocular.dev", "metrics.k8s.io", "other.example"))
	c := newCatalog(context.Background(), allKinds, api.get)
	var revs []uint64
	c.setOnChange(func(rev uint64) { revs = append(revs, rev) })
	c.refresh()
	s := waitRev(t, c, 2)
	c.wait()

	ids := kindIDs(s.reg)
	assert.Equal(t, catalogReady, s.state)
	for _, id := range []string{"persistentvolumeclaims", "batch/jobs", "ocular.dev/widgets", "ocular.dev/gadgets", "other.example/pods"} {
		require.NotNil(t, ids[id], id)
		assert.True(t, ids[id].discovered, id)
	}
	for _, id := range []string{"bindings", "metrics.k8s.io/pods"} {
		assert.Nil(t, ids[id], "%s: no list+watch", id)
	}
	assert.False(t, ids["pods"].discovered, "the described kind stays")
	assert.False(t, ids["apps/deployments"].discovered, "the described kind stays")
	assert.Len(t, s.reg.list, len(allKinds.list)+5, "no second kind for a described resource")
	assert.Equal(t, []uint64{2}, revs)

	byID := map[string]core.KindDescriptor{}
	for _, d := range s.reg.descriptors() {
		byID[d.ID] = d
	}
	w := byID["ocular.dev/widgets"]
	assert.Equal(t, "Widgets", w.Title)
	assert.Equal(t, "Widget", w.Singular)
	assert.Equal(t, discoveredGroup, w.Group)
	assert.Equal(t, "ocular.dev", w.Subgroup)
	assert.True(t, w.Scoped)
	assert.ElementsMatch(t, []string{"wd", "widget", "widgets"}, w.Aliases)
	assert.Equal(t, []core.ActionDescriptor{actDelete}, w.Actions)
	assert.Equal(t, "events", w.EventsKind)
	assert.Equal(t, "core", byID["persistentvolumeclaims"].Subgroup)
	assert.Equal(t, "PersistentVolumeClaims", byID["persistentvolumeclaims"].Title)
	assert.Empty(t, byID["ocular.dev/gadgets"].Actions, "no delete verb")
	assert.False(t, byID["ocular.dev/gadgets"].Scoped)
	// The described kinds keep their own names; a lookalike does not take them.
	assert.ElementsMatch(t, []string{"po", "pod"}, byID["pods"].Aliases)
	assert.Equal(t, "Pod", byID["pods"].Singular)
	assert.ElementsMatch(t, []string{"xp"}, byID["other.example/pods"].Aliases)
	seen := map[string]string{}
	for _, d := range s.reg.descriptors() {
		for _, a := range d.Aliases {
			assert.Empty(t, seen[a], "alias %q of %s is taken by %s", a, d.ID, seen[a])
			seen[a] = d.ID
		}
	}
}

// A failed or stale group keeps its kinds (unconfirmed); only a successful
// answer without a resource removes its kind; an equal answer is no change.
func TestCatalogKeepsUnconfirmedGroupsAndRemovesConfirmedAbsence(t *testing.T) {
	api := &scriptedAPI{}
	api.set("/api", coreDoc)
	all := apisDoc(map[string][]v2ver{"batch": {jobsV1}, "ocular.dev": {widgets}}, "batch", "ocular.dev")
	api.set("/apis", all)
	c := newCatalog(context.Background(), allKinds, api.get)
	var changes atomic.Int32
	c.setOnChange(func(uint64) { changes.Add(1) })
	c.refresh()
	waitRev(t, c, 2)
	c.wait()

	c.refresh() // the same answer
	c.wait()
	assert.Equal(t, uint64(2), c.snap().rev, "same kinds, same state: same revision")

	// /apis fails: every named group unconfirmed, their kinds kept
	api.set("/apis", nil)
	c.refresh()
	c.wait()
	s := c.snap()
	assert.Equal(t, uint64(3), s.rev)
	assert.Equal(t, catalogPartial, s.state)
	assert.Equal(t, []string{"*"}, s.unconfirmed)
	assert.NotNil(t, s.reg.byID["ocular.dev/widgets"])
	assert.NotNil(t, s.reg.byID["batch/jobs"])

	// ocular.dev answers Stale: kept, batch confirmed
	api.set("/apis", apisDoc(map[string][]v2ver{"batch": {jobsV1}, "ocular.dev": {{version: "v1", stale: true}}}, "batch", "ocular.dev"))
	c.refresh()
	c.wait()
	s = c.snap()
	assert.Equal(t, []string{"ocular.dev"}, s.unconfirmed)
	assert.NotNil(t, s.reg.byID["ocular.dev/widgets"])

	// ocular.dev is really gone
	api.set("/apis", apisDoc(map[string][]v2ver{"batch": {jobsV1}}, "batch"))
	c.refresh()
	c.wait()
	s = c.snap()
	assert.Equal(t, catalogReady, s.state)
	assert.Empty(t, s.unconfirmed)
	assert.Nil(t, s.reg.byID["ocular.dev/widgets"])
	assert.NotNil(t, s.reg.byID["batch/jobs"])
	assert.Equal(t, int32(s.rev-1), changes.Load())
}

// The first discovery failing leaves the described kinds and says so.
func TestCatalogFirstDiscoveryFailing(t *testing.T) {
	api := &scriptedAPI{}
	c := newCatalog(context.Background(), allKinds, api.get)
	c.refresh()
	c.wait()
	s := c.snap()
	assert.Equal(t, catalogFailed, s.state)
	assert.Len(t, s.reg.list, len(allKinds.list))
	assert.Equal(t, uint64(2), s.rev)
}

// One discovery at a time; refreshes asked meanwhile run it once more.
func TestCatalogRefreshIsSingleFlight(t *testing.T) {
	api := &scriptedAPI{gate: make(chan struct{})}
	api.set("/api", coreDoc)
	api.set("/apis", apisDoc(map[string][]v2ver{"batch": {jobsV1}}, "batch"))
	c := newCatalog(context.Background(), allKinds, api.get)
	c.refresh()
	require.Eventually(t, func() bool { return api.calls.Load() >= 1 }, 5*time.Second, 5*time.Millisecond)
	for range 5 {
		c.refresh()
	}
	close(api.gate)
	c.wait()
	assert.Equal(t, int32(4), api.calls.Load(), "two discoveries of /api and /apis")
	assert.NotNil(t, c.snap().reg.byID["batch/jobs"])
}

// Close ends a hanging discovery and waits for it.
func TestSessionCloseEndsDiscovery(t *testing.T) {
	api := &scriptedAPI{gate: make(chan struct{})}
	s := newSession("ctx", "h", fakeClient(), false)
	s.startCatalog(api.get)
	require.Eventually(t, func() bool { return api.calls.Load() >= 1 }, 5*time.Second, 5*time.Millisecond)
	done := make(chan struct{})
	go func() { s.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not end the discovery")
	}
	s.cat.refresh() // after Close: nothing starts
	s.cat.wait()
	assert.Equal(t, catalogDiscovering, s.cat.snap().state)
}

// The session serves what its catalog knows: kinds, actions of a discovered kind.
func TestSessionServesDiscoveredKinds(t *testing.T) {
	api := &scriptedAPI{}
	api.set("/api", coreDoc)
	api.set("/apis", apisDoc(map[string][]v2ver{"ocular.dev": {widgets}}, "ocular.dev"))
	s := newSession("ctx", "h", fakeClient(), false)
	defer s.Close()
	assert.Len(t, s.Kinds(), len(allKinds.list), "before discovery: the described kinds")
	s.startCatalog(api.get)
	waitRev(t, s.cat, 2)

	var ids []string
	for _, d := range s.Kinds() {
		ids = append(ids, d.ID)
	}
	assert.Contains(t, ids, "ocular.dev/widgets")
	assert.Contains(t, ids, "persistentvolumeclaims")

	ref := core.Ref{Kind: "ocular.dev/widgets", Scope: "ns", Name: "alpha"}
	def, a, err := s.actionTarget(ref, "delete", core.ActionParams{}, false)
	require.NoError(t, err)
	assert.Equal(t, "widgets", def.gvr.Resource)
	assert.Equal(t, actDelete, a)
	_, _, err = s.actionTarget(ref, "scale", core.ActionParams{}, false)
	var perr *provider.Error
	require.True(t, errors.As(err, &perr))
	assert.Equal(t, provider.ClassUnsupported, perr.Class)
	_, _, err = s.actionTarget(core.Ref{Kind: "ocular.dev/gadgets", Name: "g"}, "delete", core.ActionParams{}, false)
	require.True(t, errors.As(err, &perr), "no delete verb, no delete")
}

// Review 2026-09-30 (Codex, P2): an answer read before a refresh asked
// meanwhile is superseded — publishing it would drop a kind the newer
// discovery sees (and end its views removed).
func TestCatalogDoesNotPublishASupersededAnswer(t *testing.T) {
	api := &scriptedAPI{}
	api.set("/api", coreDoc)
	good := apisDoc(map[string][]v2ver{"ocular.dev": {widgets}}, "ocular.dev")
	api.set("/apis", good)
	c := newCatalog(context.Background(), allKinds, api.get)
	var mu sync.Mutex
	var seen []bool // widgets in each told revision
	c.setOnChange(func(uint64) {
		mu.Lock()
		seen = append(seen, c.snap().reg.byID["ocular.dev/widgets"] != nil)
		mu.Unlock()
	})
	c.refresh()
	c.wait()
	require.NotNil(t, c.snap().reg.byID["ocular.dev/widgets"])

	gate := make(chan struct{})
	api.setGate(gate)
	api.set("/apis", apisDoc(nil)) // widgets gone for a moment
	before := api.calls.Load()
	c.refresh()
	require.Eventually(t, func() bool { return api.calls.Load() >= before+2 }, 5*time.Second, 5*time.Millisecond)
	api.set("/apis", good) // back; a refresh asked about it
	c.refresh()
	api.setGate(nil)
	close(gate)
	c.wait()
	assert.NotNil(t, c.snap().reg.byID["ocular.dev/widgets"])
	assert.Equal(t, uint64(2), c.snap().rev, "nothing changed in the end")
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []bool{true}, seen, "the superseded absence was never told")
}

// Review 2026-09-30 (Codex, P3): revisions are told one at a time, in order,
// even when the listener asks for a refresh.
func TestCatalogTellsRevisionsOneAtATimeInOrder(t *testing.T) {
	api := &scriptedAPI{}
	api.set("/api", coreDoc)
	api.set("/apis", apisDoc(map[string][]v2ver{"batch": {jobsV1}}, "batch"))
	c := newCatalog(context.Background(), allKinds, api.get)
	var mu sync.Mutex
	var told []uint64
	var inside atomic.Int32
	overlap := false
	hold := make(chan struct{})
	c.setOnChange(func(rev uint64) {
		if inside.Add(1) > 1 {
			mu.Lock()
			overlap = true
			mu.Unlock()
		}
		defer inside.Add(-1)
		if rev == 2 {
			api.set("/apis", apisDoc(map[string][]v2ver{"batch": {jobsV1}, "ocular.dev": {widgets}}, "batch", "ocular.dev"))
			c.refresh()
			<-hold
		}
		mu.Lock()
		told = append(told, rev)
		mu.Unlock()
	})
	c.refresh()
	require.Eventually(t, func() bool { return c.snap().rev == 2 }, 5*time.Second, 5*time.Millisecond)
	time.Sleep(50 * time.Millisecond) // a concurrent runner would publish rev 3 now
	assert.Equal(t, uint64(2), c.snap().rev, "no next round while a revision is being told")
	close(hold)
	c.wait()
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []uint64{2, 3}, told)
	assert.False(t, overlap)
}

var (
	widgetsGVR = schema.GroupVersionResource{Group: "ocular.dev", Version: "v1", Resource: "widgets"}
	crdGVR     = schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	crdsV1     = v2ver{version: "v1", res: []v2res{{name: "customresourcedefinitions", kind: "CustomResourceDefinition", singular: "customresourcedefinition", scope: "Cluster", verbs: lw, short: []string{"crd"}}}}
)

func catalogClient(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		podGVR: "PodList", namespacesGVR: "NamespaceList", widgetsGVR: "WidgetList", crdGVR: "CustomResourceDefinitionList",
	}, objs...)
}

// A view of a kind that stops being served ends removed (final); opening
// it again says removed, not unknown.
func TestViewOfAKindNoLongerServedEndsRemoved(t *testing.T) {
	api := &scriptedAPI{}
	api.set("/api", coreDoc)
	api.set("/apis", apisDoc(map[string][]v2ver{"ocular.dev": {widgets}}, "ocular.dev"))
	w := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "ocular.dev/v1", "kind": "Widget",
		"metadata": map[string]any{"name": "alpha", "namespace": "ns", "uid": "w1", "creationTimestamp": "2026-09-30T10:00:00Z"}}}
	s := newSession("ctx", "h", catalogClient(w), false)
	defer s.Close()
	var told atomic.Int32
	s.OnKindsChanged(func(uint64) { told.Add(1) })
	s.startCatalog(api.get)
	waitRev(t, s.cat, 2)
	s.cat.wait()
	assert.Equal(t, int32(1), told.Load())
	assert.Equal(t, uint64(2), s.Catalog().Rev)

	sink := &recordingStatusSink{}
	stop, err := s.Watch(provider.Query{Kind: "ocular.dev/widgets", Scope: core.ScopeSel{Mode: core.ScopeAll}}, sink)
	require.NoError(t, err)
	defer stop()
	require.Eventually(t, func() bool { return sink.last().State == provider.StatusReady }, 5*time.Second, 5*time.Millisecond)

	api.set("/apis", apisDoc(nil))
	s.RefreshKinds()
	s.cat.wait()
	require.Eventually(t, func() bool { return sink.last().Class == provider.ClassRemoved }, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, provider.StatusError, sink.last().State)
	assert.True(t, s.KindRemoved("ocular.dev/widgets"))
	assert.False(t, s.KindRemoved("pods"))
	assert.Equal(t, int32(2), told.Load())

	_, err = s.Watch(provider.Query{Kind: "ocular.dev/widgets", Scope: core.ScopeSel{Mode: core.ScopeAll}}, &recordingStatusSink{})
	var perr *provider.Error
	require.True(t, errors.As(err, &perr))
	assert.Equal(t, provider.ClassRemoved, perr.Class)
	_, err = s.Watch(provider.Query{Kind: "never.example/things", Scope: core.ScopeSel{Mode: core.ScopeAll}}, &recordingStatusSink{})
	require.True(t, errors.As(err, &perr))
	assert.Equal(t, provider.ClassUnsupported, perr.Class)
}

type recordingStatusSink struct {
	mu sync.Mutex
	st provider.ViewStatus
}

func (r *recordingStatusSink) Apply(d provider.Delta) {
	if d.Status != nil {
		r.mu.Lock()
		r.st = *d.Status
		r.mu.Unlock()
	}
}

func (r *recordingStatusSink) last() provider.ViewStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.st
}

func crd(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "apiextensions.k8s.io/v1", "kind": "CustomResourceDefinition",
		"metadata": map[string]any{"name": name}}}
}

// A CRD created is the trigger of the next discovery (no timer).
func TestACRDChangeTriggersDiscovery(t *testing.T) {
	old := crdDebounce
	crdDebounce = 10 * time.Millisecond
	t.Cleanup(func() { crdDebounce = old })
	api := &scriptedAPI{}
	api.set("/api", coreDoc)
	api.set("/apis", apisDoc(map[string][]v2ver{"apiextensions.k8s.io": {crdsV1}}, "apiextensions.k8s.io"))
	client := catalogClient()
	s := newSession("ctx", "h", client, false)
	defer s.Close()
	s.startCatalog(api.get)
	waitRev(t, s.cat, 2)
	require.Eventually(t, func() bool {
		for _, a := range client.Actions() {
			if a.GetVerb() == "watch" && a.GetResource() == crdGVR {
				return true
			}
		}
		return false
	}, 5*time.Second, 5*time.Millisecond)

	api.set("/apis", apisDoc(map[string][]v2ver{"apiextensions.k8s.io": {crdsV1}, "ocular.dev": {widgets}}, "apiextensions.k8s.io", "ocular.dev"))
	_, err := client.Resource(crdGVR).Create(context.Background(), crd("widgets.ocular.dev"), metav1.CreateOptions{})
	require.NoError(t, err)
	snap := waitRev(t, s.cat, 3)
	assert.NotNil(t, snap.reg.byID["ocular.dev/widgets"])
}

// Without the right to list CRDs there is no trigger, and no retrying.
func TestNoCRDTriggerWithoutTheRight(t *testing.T) {
	api := &scriptedAPI{}
	api.set("/api", coreDoc)
	api.set("/apis", apisDoc(map[string][]v2ver{"apiextensions.k8s.io": {crdsV1}}, "apiextensions.k8s.io"))
	client := catalogClient()
	var lists atomic.Int32
	client.PrependReactor("list", "customresourcedefinitions", func(k8stesting.Action) (bool, runtime.Object, error) {
		lists.Add(1)
		return true, nil, apierrors.NewForbidden(crdGVR.GroupResource(), "", errors.New("rbac"))
	})
	s := newSession("ctx", "h", client, false)
	defer s.Close()
	s.startCatalog(api.get)
	waitRev(t, s.cat, 2)
	require.Eventually(t, func() bool { return lists.Load() == 1 }, 5*time.Second, 5*time.Millisecond)
	time.Sleep(1500 * time.Millisecond) // past the first backoff
	assert.Equal(t, int32(1), lists.Load())
}

// Review 2026-09-30 (Codex, be73e99 P2-1): however many answers in a row are
// superseded, none of them removes a kind.
func TestCatalogSupersededAnswersNeverRemoveAKind(t *testing.T) {
	api := &scriptedAPI{}
	api.set("/api", coreDoc)
	good := apisDoc(map[string][]v2ver{"ocular.dev": {widgets}}, "ocular.dev")
	api.set("/apis", good)
	c := newCatalog(context.Background(), allKinds, api.get)
	c.refresh()
	c.wait()
	require.NotNil(t, c.snap().reg.byID["ocular.dev/widgets"])

	api.set("/apis", apisDoc(map[string][]v2ver{"batch": {jobsV1}}, "batch")) // widgets gone, jobs new
	gate := make(chan struct{})
	api.setGate(gate)
	calls := api.calls.Load()
	c.refresh()
	for range 7 {
		require.Eventually(t, func() bool { return api.calls.Load() >= calls+2 }, 5*time.Second, time.Millisecond)
		calls += 2
		next := make(chan struct{})
		api.setGate(next)
		c.refresh() // supersedes the held round
		close(gate)
		gate = next
		require.Eventually(t, func() bool { return api.calls.Load() >= calls+2 }, 5*time.Second, time.Millisecond)
		s := c.snap()
		assert.NotNil(t, s.reg.byID["ocular.dev/widgets"], "a superseded answer removed a kind")
		assert.Empty(t, s.removed)
		assert.NotNil(t, s.reg.byID["batch/jobs"], "a superseded answer still adds")
	}
	api.setGate(nil)
	close(gate) // the last round is not superseded: its absence is confirmed
	c.wait()
	assert.Nil(t, c.snap().reg.byID["ocular.dev/widgets"])
	assert.True(t, c.snap().removed["ocular.dev/widgets"])
}

// Review 2026-09-30 (Codex, be73e99 P2-3): a view ended removed gets
// nothing more, and its observation ends.
func TestARemovedViewGetsNoMoreRowsAndReleasesItsCache(t *testing.T) {
	api := &scriptedAPI{}
	api.set("/api", coreDoc)
	api.set("/apis", apisDoc(map[string][]v2ver{"ocular.dev": {widgets}}, "ocular.dev"))
	w := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "ocular.dev/v1", "kind": "Widget",
		"metadata": map[string]any{"name": "alpha", "namespace": "ns", "uid": "w1", "creationTimestamp": "2026-09-30T10:00:00Z"}}}
	client := catalogClient(w)
	s := newSession("ctx", "h", client, false)
	defer s.Close()
	s.startCatalog(api.get)
	waitRev(t, s.cat, 2)
	sink := &deltaSink{}
	stop, err := s.Watch(provider.Query{Kind: "ocular.dev/widgets", Scope: core.ScopeSel{Mode: core.ScopeAll}}, sink)
	require.NoError(t, err)
	defer stop()
	require.Eventually(t, func() bool { st := sink.status(); return st != nil && st.State == provider.StatusReady }, 5*time.Second, 5*time.Millisecond)

	api.set("/apis", apisDoc(nil))
	s.RefreshKinds()
	require.Eventually(t, func() bool { st := sink.status(); return st != nil && st.Class == provider.ClassRemoved }, 5*time.Second, 5*time.Millisecond)
	n := sink.count()
	w2 := w.DeepCopy()
	w2.SetLabels(map[string]string{"late": "1"})
	_, err = client.Resource(widgetsGVR).Namespace("ns").Update(context.Background(), w2, metav1.UpdateOptions{})
	require.NoError(t, err)
	time.Sleep(200 * time.Millisecond) // a live handler would deliver the update now
	assert.Equal(t, n, sink.count(), "nothing after the final removed status")
	active, _ := s.caches.stats()
	assert.Zero(t, active, "the removed view leases no cache")
}

// deltaSink records deliveries.
type deltaSink struct {
	mu sync.Mutex
	d  []provider.Delta
}

func (r *deltaSink) Apply(d provider.Delta) { r.mu.Lock(); r.d = append(r.d, d); r.mu.Unlock() }
func (r *deltaSink) count() int             { r.mu.Lock(); defer r.mu.Unlock(); return len(r.d) }
func (r *deltaSink) status() *provider.ViewStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.d) - 1; i >= 0; i-- {
		if r.d[i].Status != nil {
			return r.d[i].Status
		}
	}
	return nil
}

func fastCRDTrigger(t *testing.T, debounce, maxDelay time.Duration) {
	d, m := crdDebounce, crdMaxDelay
	crdDebounce, crdMaxDelay = debounce, maxDelay
	t.Cleanup(func() { crdDebounce, crdMaxDelay = d, m })
}

// Review 2026-09-30 (Codex, be73e99 P2-2): a CRD created between the
// discovery and the watch's baseline has no event; the baseline is
// followed by a discovery.
func TestACRDCreatedBeforeTheWatchBaselineIsFound(t *testing.T) {
	fastCRDTrigger(t, 10*time.Millisecond, 50*time.Millisecond)
	api := &scriptedAPI{}
	api.set("/api", coreDoc)
	api.set("/apis", apisDoc(map[string][]v2ver{"apiextensions.k8s.io": {crdsV1}}, "apiextensions.k8s.io"))
	client := catalogClient()
	client.PrependReactor("list", "customresourcedefinitions", func(k8stesting.Action) (bool, runtime.Object, error) {
		// created just now: after the discovery, before the baseline
		api.set("/apis", apisDoc(map[string][]v2ver{"apiextensions.k8s.io": {crdsV1}, "ocular.dev": {widgets}}, "apiextensions.k8s.io", "ocular.dev"))
		l := &unstructured.UnstructuredList{Object: map[string]any{"apiVersion": "apiextensions.k8s.io/v1", "kind": "CustomResourceDefinitionList"}}
		l.SetResourceVersion("100")
		return true, l, nil
	})
	s := newSession("ctx", "h", client, false)
	defer s.Close()
	s.startCatalog(api.get)
	require.Eventually(t, func() bool { return s.cat.snap().reg.byID["ocular.dev/widgets"] != nil }, 5*time.Second, 5*time.Millisecond)
}

// Review 2026-09-30 (Codex, be73e99 P2-4): CRD changes that never pause
// still refresh the catalog, within the bound.
func TestContinuousCRDChangesStillRefreshWithinTheBound(t *testing.T) {
	fastCRDTrigger(t, 200*time.Millisecond, 400*time.Millisecond)
	api := &scriptedAPI{}
	api.set("/api", coreDoc)
	api.set("/apis", apisDoc(map[string][]v2ver{"apiextensions.k8s.io": {crdsV1}}, "apiextensions.k8s.io"))
	client := catalogClient()
	fw := watch.NewFake()
	client.PrependWatchReactor("customresourcedefinitions", func(k8stesting.Action) (bool, watch.Interface, error) { return true, fw, nil })
	s := newSession("ctx", "h", client, false)
	defer s.Close()
	s.startCatalog(api.get)
	waitRev(t, s.cat, 2)
	require.Eventually(t, func() bool {
		for _, a := range client.Actions() {
			if a.GetVerb() == "watch" {
				return true
			}
		}
		return false
	}, 5*time.Second, 5*time.Millisecond)
	time.Sleep(300 * time.Millisecond) // the baseline's own discovery is done
	api.set("/apis", apisDoc(map[string][]v2ver{"apiextensions.k8s.io": {crdsV1}, "ocular.dev": {widgets}}, "apiextensions.k8s.io", "ocular.dev"))
	start := time.Now()
	found := false
	for time.Since(start) < 1500*time.Millisecond && !found {
		fw.Modify(crd("other.example.com")) // a status change every 40 ms
		time.Sleep(40 * time.Millisecond)
		found = s.cat.snap().reg.byID["ocular.dev/widgets"] != nil
	}
	assert.True(t, found, "continuous changes postponed every refresh")
}

// Review 2026-09-30 (Codex, be73e99 P3): a denial inside the stream stops
// the trigger like a denied request does.
func TestADeniedCRDStreamStopsTheTrigger(t *testing.T) {
	api := &scriptedAPI{}
	api.set("/api", coreDoc)
	api.set("/apis", apisDoc(map[string][]v2ver{"apiextensions.k8s.io": {crdsV1}}, "apiextensions.k8s.io"))
	client := catalogClient()
	var watches atomic.Int32
	client.PrependWatchReactor("customresourcedefinitions", func(k8stesting.Action) (bool, watch.Interface, error) {
		watches.Add(1)
		fw := watch.NewFakeWithChanSize(1, false)
		fw.Error(&metav1.Status{Status: metav1.StatusFailure, Code: 403, Reason: metav1.StatusReasonForbidden, Message: "rbac"})
		return true, fw, nil
	})
	s := newSession("ctx", "h", client, false)
	defer s.Close()
	s.startCatalog(api.get)
	require.Eventually(t, func() bool { return watches.Load() == 1 }, 5*time.Second, 5*time.Millisecond)
	time.Sleep(1500 * time.Millisecond) // past the first backoff
	assert.Equal(t, int32(1), watches.Load())
}
