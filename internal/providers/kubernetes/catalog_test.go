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

func (a *scriptedAPI) get(ctx context.Context, path, _ string) ([]byte, string, error) {
	a.calls.Add(1)
	a.mu.Lock()
	gate := a.gate
	a.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, "", ctx.Err()
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	doc, ok := a.docs[path]
	if !ok {
		return nil, "", &discoveryError{code: 503, path: path, body: "unavailable"}
	}
	return doc, v2ContentType, nil
}

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
