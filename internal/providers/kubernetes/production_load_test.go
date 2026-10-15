package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/views"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// A wire-level collection fixture: unlike fake-client reactors, this observes
// actual HTTP limit/continue/watch options and generates only the requested
// page. It never preloads the test collection into the client's heap.
type loadPageKey struct{ start, limit int }
type loadReply struct {
	data  []byte
	items int
}

type loadAPI struct {
	prepared      map[loadPageKey]loadReply
	total         int
	omitRemaining bool
	lists         atomic.Int64
	watches       atomic.Int64
	items         atomic.Int64
	bytes         atomic.Int64
}

func (a *loadAPI) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Query().Get("watch") == "true" {
		a.watches.Add(1)
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		return
	}
	a.lists.Add(1)
	start, _ := strconv.Atoi(r.URL.Query().Get("continue"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = a.total
	}
	reply, ok := a.prepared[loadPageKey{start: start, limit: limit}]
	if !ok {
		reply = a.reply(start, limit)
	}
	a.items.Add(int64(reply.items))
	a.bytes.Add(int64(len(reply.data)))
	_, _ = w.Write(reply.data)
}

func (a *loadAPI) reply(start, limit int) loadReply {
	end := min(start+limit, a.total)
	cont := ""
	if end < a.total {
		cont = strconv.Itoa(end)
	}
	objects := make([]any, 0, end-start)
	for i := start; i < end; i++ {
		pod := realisticPod(i)
		pod.SetNamespace("load-scope")
		objects = append(objects, pod.Object)
	}
	metadata := map[string]any{"resourceVersion": "123456", "continue": cont}
	if !a.omitRemaining {
		metadata["remainingItemCount"] = a.total - end
	}
	data, err := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "PodList", "metadata": metadata, "items": objects})
	if err != nil {
		panic(err)
	}
	return loadReply{data: data, items: len(objects)}
}

func loadSession(t *testing.T, total int) (*session, *loadAPI) {
	t.Helper()
	a := &loadAPI{total: total}
	server := httptest.NewServer(http.HandlerFunc(a.serve))
	t.Cleanup(server.Close)
	dyn, err := dynamic.NewForConfig(budgetConfig(&rest.Config{Host: server.URL, QPS: 50, Burst: 100}))
	require.NoError(t, err)
	s := newSession("load-fixture", "hash", dyn, false)
	t.Cleanup(s.Close)
	return s, a
}

// Complete selected-namespace inventory under the real session read budget.
// Historical pre-change measurements remain in scratch; this tests the new contract.
func TestProductionLoadCompleteSelectedNamespace(t *testing.T) {
	if os.Getenv("OCULAR_LOAD_AUDIT") == "" {
		t.Skip("OCULAR_LOAD_AUDIT=1 for wire-level 10k selected-namespace inventory")
	}
	s, a := loadSession(t, 10000)
	m := views.NewManager(events.NewEmitter())
	t.Cleanup(m.CloseAll)
	started := time.Now()
	observed := &loadObservation{session: s}
	id, err := m.Open("fixture", observed, provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "load-scope"}})
	require.NoError(t, err)
	require.Eventually(t, observed.ready.Load, 30*time.Second, 20*time.Millisecond, "complete observation must be ready within the unchanged budget")
	p, err := m.Get(id, 0)
	require.NoError(t, err)
	require.Len(t, p.Upserts, 10000)
	require.EqualValues(t, 10000, a.items.Load())
	t.Logf("complete compact inventory: rows=%d LISTs=%d objects=%d bytes=%d watchStarts=%d elapsed=%s", len(p.Upserts), a.lists.Load(), a.items.Load(), a.bytes.Load(), a.watches.Load(), time.Since(started))
}

func TestProductionLoadScopesAndPickerShareNamespaceInventory(t *testing.T) {
	var lists atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("watch") == "true" {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		lists.Add(1)
		_, _ = fmt.Fprint(w, `{"kind":"NamespaceList","apiVersion":"v1","metadata":{"resourceVersion":"10"},"items":[{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"team-a","uid":"ns-a","resourceVersion":"10"},"status":{"phase":"Active"}}]}`)
	}))
	defer srv.Close()
	dyn, err := dynamic.NewForConfig(&rest.Config{Host: srv.URL})
	require.NoError(t, err)
	s := newSession("scopes-fixture", "hash", dyn, false)
	defer s.Close()
	scopes, err := s.Scopes(context.Background())
	require.NoError(t, err)
	require.Equal(t, []core.Scope{{Name: "team-a"}}, scopes)
	m := views.NewManager(events.NewEmitter())
	defer m.CloseAll()
	id, err := m.Open("scope-owner", s, provider.Query{Kind: "namespaces", Scope: core.ScopeSel{Mode: core.ScopeNone}})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		p, e := m.Get(id, 0)
		return e == nil && p.Status.State == provider.StatusReady && len(p.Upserts) == 1
	}, 5*time.Second, 10*time.Millisecond)
	_, err = s.Scopes(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 1, lists.Load(), "one inventory serves initial scopes, live picker, and repeated scope reads")
}

func TestProductionLoadCanceledScopesDoesNotStartInventory(t *testing.T) {
	s, a := loadSession(t, 10000)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.Scopes(ctx)
	var pe *provider.Error
	require.ErrorAs(t, err, &pe)
	require.Equal(t, provider.ClassUnavailable, pe.Class)
	active, idle := s.caches.stats()
	require.Zero(t, active)
	require.Zero(t, idle)
	require.Zero(t, a.lists.Load())
}

func TestProductionLoadCachedScopesExposeKnownPermissionFailure(t *testing.T) {
	s := newSession("scope-permission-fixture", "hash", fakeClient(), false)
	defer s.Close()
	_, err := s.Scopes(context.Background())
	require.NoError(t, err)
	s.caches.mu.Lock()
	c := s.caches.caches[cacheKey{gvr: namespacesGVR}]
	s.caches.mu.Unlock()
	require.NotNil(t, c)
	c.setTransport(apierrors.NewForbidden(schema.GroupResource{Resource: "namespaces"}, "", fmt.Errorf("scope observation forbidden")))
	_, err = s.Scopes(context.Background())
	var pe *provider.Error
	require.ErrorAs(t, err, &pe)
	require.Equal(t, provider.ClassForbidden, pe.Class)
}

func TestProductionLoadScopeBootstrapRequestsOnlyOneObject(t *testing.T) {
	var requested atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		requested.Store(int64(limit))
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"kind":"NamespaceList","apiVersion":"v1","metadata":{"resourceVersion":"10","continue":"more-namespaces"},"items":[]}`)
	}))
	defer srv.Close()
	dyn, err := dynamic.NewForConfig(&rest.Config{Host: srv.URL})
	require.NoError(t, err)
	s := newSession("scope-bootstrap", "hash", dyn, false)
	defer s.Close()
	require.NoError(t, s.CheckScopes(context.Background()))
	require.EqualValues(t, 1, requested.Load())
	active, idle := s.caches.stats()
	require.Zero(t, active)
	require.Zero(t, idle)
}

func TestProductionLoadAllNamespacesStopsWithoutPartialSuccess(t *testing.T) {
	s, a := loadSession(t, 10000)
	m := views.NewManager(events.NewEmitter())
	defer m.CloseAll()
	id, err := m.Open("fixture", s, provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeAll}})
	require.NoError(t, err)
	require.Eventually(t, func() bool { p, e := m.Get(id, 0); return e == nil && p.Status.Class == provider.ClassScopeLimit }, 5*time.Second, 10*time.Millisecond)
	p, _ := m.Get(id, 0)
	require.Equal(t, provider.StatusError, p.Status.State)
	require.Empty(t, p.Upserts)
	require.LessOrEqual(t, a.items.Load(), int64(2100))
	require.Zero(t, a.watches.Load())
	warm, err := m.Open("fixture", s, provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeAll}})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		p, e := m.Get(warm, 0)
		return e == nil && p.Status.Class == provider.ClassScopeLimit && p.Status.State == provider.StatusError && len(p.Upserts) == 0
	}, time.Second, 10*time.Millisecond)
	requests := a.lists.Load()
	time.Sleep(150 * time.Millisecond)
	require.Equal(t, requests, a.lists.Load(), "blocked broad cache must not retry")
	// A selected namespace creates its own full inventory, unaffected by the broad failure.
	observed := &loadObservation{session: s}
	selected, err := m.Open("fixture", observed, provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "load-scope"}})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return observed.ready.Load()
	}, 30*time.Second, 20*time.Millisecond)
	p, err = m.Get(selected, 0)
	require.NoError(t, err)
	require.Len(t, p.Upserts, 10000)
}

// Observe readiness without repeatedly copying and sorting an increasingly
// large snapshot inside the benchmark's 20ms polling loop.
type loadObservation struct {
	*session
	ready atomic.Bool
}
type loadObservationSink struct {
	owner *loadObservation
	sink  provider.Sink
}

func (s *loadObservation) Watch(q provider.Query, sink provider.Sink) (func(), error) {
	return s.session.Watch(q, &loadObservationSink{owner: s, sink: sink})
}
func (s *loadObservationSink) Apply(d provider.Delta) {
	s.sink.Apply(d)
	if d.Status != nil {
		s.owner.ready.Store(d.Status.State == provider.StatusReady)
	}
}

func TestProductionLoadClosingUnfinishedViewCancelsItsHTTPRead(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(canceled) }))
	defer srv.Close()
	dyn, err := dynamic.NewForConfig(budgetConfig(&rest.Config{Host: srv.URL}))
	require.NoError(t, err)
	s := newSession("cancel-fixture", "hash", dyn, false)
	defer s.Close()
	m := views.NewManager(events.NewEmitter())
	defer m.CloseAll()
	id, err := m.Open("owner", s, provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "explicit"}})
	require.NoError(t, err)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("fixture did not start its LIST")
	}
	m.Close(id)
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("unused initial inventory kept reading")
	}
	active, idle := s.caches.stats()
	require.Zero(t, active)
	require.Zero(t, idle)
}

func TestProductionLoadAllNamespacesWithoutEstimateStopsWithinBudget(t *testing.T) {
	s, a := loadSession(t, 10000)
	a.omitRemaining = true
	// This is a client admission/cancellation deadline, not a benchmark of
	// constructing and marshaling synthetic API-server objects under -race.
	// Prepare identical response bytes before the timed observation. All other
	// load measurements still generate each requested page dynamically.
	a.prepared = map[loadPageKey]loadReply{}
	for start := 0; start < allNamespaceObjects; start += int(collectionPageSize) {
		key := loadPageKey{start: start, limit: int(collectionPageSize)}
		a.prepared[key] = a.reply(key.start, key.limit)
	}
	probe := loadPageKey{start: allNamespaceObjects, limit: 1}
	a.prepared[probe] = a.reply(probe.start, probe.limit)
	m := views.NewManager(events.NewEmitter())
	defer m.CloseAll()
	id, err := m.Open("fixture", s, provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeAll}})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		p, e := m.Get(id, 0)
		return e == nil && p.Status.State == provider.StatusError && p.Status.Class == provider.ClassScopeLimit
	}, 5*time.Second, 10*time.Millisecond)
	p, err := m.Get(id, 0)
	require.NoError(t, err)
	require.Empty(t, p.Upserts)
	require.EqualValues(t, 2001, a.items.Load())
	require.EqualValues(t, 21, a.lists.Load())
	require.Zero(t, a.watches.Load())
}
