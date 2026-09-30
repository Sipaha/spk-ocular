package api

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// openable is a provider whose targets have a mutable config hash.
type openable struct {
	fakeProvider
	mu     sync.Mutex
	hashes map[string]string
	opened []*fakeSession
}

func (o *openable) Discover(ctx context.Context) (provider.Discovery, error) {
	d, err := o.fakeProvider.Discover(ctx)
	o.mu.Lock()
	defer o.mu.Unlock()
	for i := range d.Targets {
		d.Targets[i].ConfigHash = o.hashes[d.Targets[i].ID]
	}
	return d, err
}

func (o *openable) Open(_ context.Context, target string) (provider.Session, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	s := &fakeSession{target: target, hash: o.hashes[target]}
	o.opened = append(o.opened, s)
	return s, nil
}

func (o *openable) setHash(target, h string) { o.mu.Lock(); o.hashes[target] = h; o.mu.Unlock() }

type fakeSession struct {
	target    string
	hash      string
	mu        sync.Mutex
	closed    bool
	scopesErr error
	noScopes  bool
}

func (f *fakeSession) ConfigHash() string { return f.hash }
func (f *fakeSession) ScopeKind() string  { return "" }
func (f *fakeSession) Kinds() []core.KindDescriptor {
	return []core.KindDescriptor{{ID: "pods", Title: "Pods", Scoped: true}}
}
func (f *fakeSession) Scopes(context.Context) ([]core.Scope, error) {
	if f.scopesErr != nil {
		return nil, f.scopesErr
	}
	if f.noScopes {
		return nil, nil
	}
	return []core.Scope{{Name: "default"}}, nil
}
func (f *fakeSession) Watch(_ provider.Query, sink provider.Sink) (func(), error) {
	sink.Apply(provider.Delta{Upserts: []core.Row{{ID: "1", Ref: core.Ref{Name: "p"}}}, Status: &provider.ViewStatus{State: provider.StatusReady}})
	return func() {}, nil
}
func (f *fakeSession) Get(context.Context, core.Ref) (*core.Resource, error) { return nil, nil }
func (f *fakeSession) Close()                                                { f.mu.Lock(); f.closed = true; f.mu.Unlock() }
func (f *fakeSession) isClosed() bool                                        { f.mu.Lock(); defer f.mu.Unlock(); return f.closed }

func newOpenable(targets ...string) *openable {
	o := &openable{fakeProvider: fakeProvider{id: "k", targets: targets, changed: make(chan struct{})}, hashes: map[string]string{}}
	for _, t := range targets {
		o.hashes[t] = "h1"
	}
	return o
}

var allScopes = core.ScopeSel{Mode: core.ScopeAll}

func TestViewLifecycleThroughTheService(t *testing.T) {
	ctx := context.Background()
	k := newOpenable("a")
	s, _ := newService(t, k)

	kinds, err := s.ListKinds(ctx, "k", "a")
	require.NoError(t, err)
	assert.Equal(t, "pods", kinds[0].ID)

	info, err := s.OpenView(ctx, OpenViewRequest{Provider: "k", Target: "a", Query: provider.Query{Kind: "pods", Scope: allScopes}})
	require.NoError(t, err)
	assert.Equal(t, "Pods", info.Kind.Title)
	p, err := s.GetRows(ctx, info.ViewID, 0)
	require.NoError(t, err)
	assert.Len(t, p.Upserts, 1)
	assert.Equal(t, provider.StatusReady, p.Status.State)
	assert.Len(t, k.opened, 1, "one session for the target")

	require.NoError(t, s.CloseView(ctx, info.ViewID))
	_, err = s.GetRows(ctx, info.ViewID, 0)
	assert.True(t, IsCoded(err, CodeGone))
	gone, _ := s.TouchViews(ctx, []string{info.ViewID})
	assert.Equal(t, []string{info.ViewID}, gone)
}

func TestConfigChangeClosesSessionAndTellsViews(t *testing.T) {
	ctx := context.Background()
	k := newOpenable("a")
	s, em := newService(t, k)
	sub, unsub := em.Subscribe()
	defer unsub()
	s.Start(ctx)
	info, err := s.OpenView(ctx, OpenViewRequest{Provider: "k", Target: "a", Query: provider.Query{Kind: "pods", Scope: allScopes}})
	require.NoError(t, err)

	k.setHash("a", "h2") // kubeconfig edited: same context, another server/user
	k.changed <- struct{}{}
	require.Eventually(t, func() bool {
		select {
		case <-sub.Wake():
			for _, ev := range sub.Drain() {
				if ev.Type == EventViewChanged && ev.Payload["gone"] == true && ev.Payload["viewId"] == info.ViewID {
					return true
				}
			}
		default:
		}
		return false
	}, 2*time.Second, 10*time.Millisecond)
	assert.True(t, k.opened[0].isClosed())

	_, err = s.OpenView(ctx, OpenViewRequest{Provider: "k", Target: "a", Query: provider.Query{Kind: "pods", Scope: allScopes}})
	require.NoError(t, err)
	require.Len(t, k.opened, 2)
	assert.Equal(t, "h2", k.opened[1].ConfigHash())
}

func TestSelectingAnotherTargetClosesOtherSessions(t *testing.T) {
	ctx := context.Background()
	k := newOpenable("a", "b")
	s, _ := newService(t, k)
	_, err := s.ListKinds(ctx, "k", "a")
	require.NoError(t, err)
	require.NoError(t, s.SelectTarget(ctx, "k", "b"))
	assert.True(t, k.opened[0].isClosed())
}

func TestScopesForbiddenIsDataNotFailure(t *testing.T) {
	ctx := context.Background()
	k := newOpenable("a")
	s, _ := newService(t, k)
	_, err := s.ListKinds(ctx, "k", "a")
	require.NoError(t, err)
	k.opened[0].scopesErr = &provider.Error{Class: provider.ClassForbidden, Message: "namespaces is forbidden"}
	v, err := s.ListScopes(ctx, "k", "a")
	require.NoError(t, err)
	require.NotNil(t, v.Error)
	assert.Equal(t, "forbidden", v.Error.Code)
	assert.NotNil(t, v.Scopes)
}

func TestNoScopesIsAnEmptyListNotNull(t *testing.T) {
	ctx := context.Background()
	k := newOpenable("a")
	s, _ := newService(t, k)
	_, err := s.ListKinds(ctx, "k", "a")
	require.NoError(t, err)
	k.opened[0].noScopes = true // a provider without scopes answers nil
	v, err := s.ListScopes(ctx, "k", "a")
	require.NoError(t, err)
	assert.NotNil(t, v.Scopes, "an empty list, not null, for the UI")
}

func TestOpenViewValidation(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t, newOpenable("a"))
	_, err := s.OpenView(ctx, OpenViewRequest{Provider: "k", Target: "a", Query: provider.Query{Kind: "pods"}})
	assert.True(t, IsCoded(err, CodeBadRequest), "scope selector is required")
	_, err = s.OpenView(ctx, OpenViewRequest{Provider: "k", Target: "a", Query: provider.Query{Kind: "nope", Scope: allScopes}})
	assert.True(t, IsCoded(err, CodeUnsupported))
	_, err = s.OpenView(ctx, OpenViewRequest{Provider: "k", Target: "zzz", Query: provider.Query{Kind: "pods", Scope: allScopes}})
	assert.True(t, IsCoded(err, CodeNotFound))
	s2, _ := newService(t, &fakeProvider{id: "plain", targets: []string{"x"}})
	_, err = s2.ListKinds(ctx, "plain", "x")
	assert.True(t, IsCoded(err, CodeUnsupported), "providers without Open")
}

type metricSession struct {
	*fakeSession
	m   provider.Metrics
	err error
	// rows: the view's rows are r0…r<rows-1> (0: fakeSession's one row)
	rows int
	// block: Metrics waits for its ctx to end
	block bool
	// started/ended (optional): each call's start and, when blocking, its
	// context's end
	started chan []string
	ended   chan error

	mu     sync.Mutex
	asked  [][]string
	ctxErr error
}

func (m *metricSession) Metrics(ctx context.Context, _ provider.Query, rowIDs []string) (provider.Metrics, error) {
	m.mu.Lock()
	m.asked = append(m.asked, rowIDs)
	m.mu.Unlock()
	if m.started != nil {
		m.started <- rowIDs
	}
	if m.block {
		<-ctx.Done()
		m.mu.Lock()
		m.ctxErr = ctx.Err()
		m.mu.Unlock()
		if m.ended != nil {
			m.ended <- ctx.Err()
		}
		return provider.Metrics{}, ctx.Err()
	}
	return m.m, m.err
}

func (m *metricSession) Watch(q provider.Query, sink provider.Sink) (func(), error) {
	if m.rows == 0 {
		return m.fakeSession.Watch(q, sink)
	}
	var rows []core.Row
	for i := range m.rows {
		rows = append(rows, core.Row{ID: fmt.Sprintf("r%d", i), Ref: core.Ref{Name: fmt.Sprintf("p%d", i)}})
	}
	sink.Apply(provider.Delta{Upserts: rows, Status: &provider.ViewStatus{State: provider.StatusReady}})
	return func() {}, nil
}

func (m *metricSession) lastAsked() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.asked) == 0 {
		return nil
	}
	return m.asked[len(m.asked)-1]
}

type metricOpenable struct {
	*openable
	ms *metricSession
}

func (o *metricOpenable) Open(ctx context.Context, target string) (provider.Session, error) {
	s, _ := o.openable.Open(ctx, target)
	o.ms.fakeSession = s.(*fakeSession)
	return o.ms, nil
}

func TestGetMetricsJoinsCurrentRowsAndReportsStatus(t *testing.T) {
	ctx := context.Background()
	ms := &metricSession{m: provider.Metrics{Values: map[string]provider.Usage{
		"1":       {CPU: provider.Num(0.1), Memory: provider.Num(1024)}, // fakeSession's row id
		"deleted": {CPU: provider.Num(9)},
	}}}
	k := &metricOpenable{openable: newOpenable("a"), ms: ms}
	s, _ := newService(t, k)
	info, err := s.OpenView(ctx, OpenViewRequest{Provider: "k", Target: "a", Query: provider.Query{Kind: "pods", Scope: allScopes}})
	require.NoError(t, err)

	mv, err := s.GetMetrics(ctx, MetricsRequest{ViewID: info.ViewID, RowIDs: []string{"deleted", "1", "1"}})
	require.NoError(t, err)
	assert.Equal(t, "ok", mv.Status)
	assert.Equal(t, map[string]provider.Usage{"1": {CPU: provider.Num(0.1), Memory: provider.Num(1024)}}, mv.Values, "keyed by row id; objects not in the view get nothing")
	assert.Equal(t, []string{"1"}, ms.lastAsked(), "only the view's rows are asked for, once")

	ms.err = &provider.Error{Class: provider.ClassUnsupported, Message: "metrics.k8s.io is not installed"}
	mv, err = s.GetMetrics(ctx, MetricsRequest{ViewID: info.ViewID, RowIDs: []string{"1"}})
	require.NoError(t, err, "a missing metrics API is a status, not a failed call")
	assert.Equal(t, "unsupported", mv.Status)
	assert.Empty(t, mv.Values)

	_, err = s.GetMetrics(ctx, MetricsRequest{ViewID: "v-unknown", RowIDs: []string{"1"}})
	assert.True(t, IsCoded(err, CodeGone))
}

// The page asks for the rows it shows: at most MaxMetricRows of the
// view's, in its order; more are cut and said; none — no provider call.
func TestGetMetricsAsksForTheShownRowsOnly(t *testing.T) {
	ctx := context.Background()
	ms := &metricSession{rows: 150, m: provider.Metrics{Values: map[string]provider.Usage{"r120": {Memory: provider.Num(1)}}}}
	k := &metricOpenable{openable: newOpenable("a"), ms: ms}
	s, _ := newService(t, k)
	info, err := s.OpenView(ctx, OpenViewRequest{Provider: "k", Target: "a", Query: provider.Query{Kind: "pods", Scope: allScopes}})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		p, err := s.GetRows(ctx, info.ViewID, 0)
		return err == nil && len(p.Upserts) == 150
	}, 5*time.Second, 5*time.Millisecond)

	var ask []string
	for i := 149; i >= 0; i-- {
		ask = append(ask, fmt.Sprintf("r%d", i))
	}
	mv, err := s.GetMetrics(ctx, MetricsRequest{ViewID: info.ViewID, RowIDs: append([]string{"foreign"}, ask...)})
	require.NoError(t, err)
	assert.Equal(t, ask[:MaxMetricRows], ms.lastAsked())
	assert.Equal(t, MaxMetricRows, mv.Limit, "the cut is said")
	assert.Equal(t, map[string]provider.Usage{"r120": {Memory: provider.Num(1)}}, mv.Values, "the memory known, the CPU unknown")

	n := len(ms.asked)
	mv, err = s.GetMetrics(ctx, MetricsRequest{ViewID: info.ViewID, RowIDs: []string{"foreign"}})
	require.NoError(t, err)
	assert.Equal(t, "ok", mv.Status)
	assert.Empty(t, mv.Values)
	assert.Len(t, ms.asked, n, "nothing to ask for")
}

// The provider's wait ends with the page's request, or at the deadline.
func TestGetMetricsEndsWithTheCaller(t *testing.T) {
	ms := &metricSession{block: true}
	k := &metricOpenable{openable: newOpenable("a"), ms: ms}
	s, _ := newService(t, k)
	info, err := s.OpenView(context.Background(), OpenViewRequest{Provider: "k", Target: "a", Query: provider.Query{Kind: "pods", Scope: allScopes}})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	t0 := time.Now()
	_, _ = s.GetMetrics(ctx, MetricsRequest{ViewID: info.ViewID, RowIDs: []string{"1"}})
	assert.Less(t, time.Since(t0), 2*time.Second)
	ms.mu.Lock()
	assert.ErrorIs(t, ms.ctxErr, context.Canceled)
	ms.mu.Unlock()

	old := metricsTimeout
	metricsTimeout = 50 * time.Millisecond
	defer func() { metricsTimeout = old }()
	mv, err := s.GetMetrics(context.Background(), MetricsRequest{ViewID: info.ViewID, RowIDs: []string{"1"}})
	require.NoError(t, err)
	assert.Equal(t, "unavailable", mv.Status, "the deadline is the request's")
}

// One metrics request per view runs: a newer one (by the page's seq), a
// CancelMetrics or closing the view ends the one in flight, and a request
// the page gave up before it started (a cancel that overtook it, or a
// newer request that did) never reaches the provider — whatever order the
// transport delivers them in.
func TestGetMetricsOneRequestPerView(t *testing.T) {
	ms := &metricSession{block: true, started: make(chan []string, 8), ended: make(chan error, 8)}
	k := &metricOpenable{openable: newOpenable("a"), ms: ms}
	s, _ := newService(t, k)
	ctx := context.Background()
	info, err := s.OpenView(ctx, OpenViewRequest{Provider: "k", Target: "a", Query: provider.Query{Kind: "pods", Scope: allScopes}})
	require.NoError(t, err)
	ask := func(seq uint64) chan MetricsView {
		out := make(chan MetricsView, 1)
		go func() {
			mv, err := s.GetMetrics(ctx, MetricsRequest{ViewID: info.ViewID, RowIDs: []string{"1"}, Seq: seq})
			assert.NoError(t, err)
			out <- mv
		}()
		return out
	}
	wait := func(what string, ch any) {
		t.Helper()
		switch c := ch.(type) {
		case chan []string:
			select {
			case <-c:
			case <-time.After(5 * time.Second):
				t.Fatal(what)
			}
		case chan error:
			select {
			case err := <-c:
				assert.ErrorIs(t, err, context.Canceled, what)
			case <-time.After(5 * time.Second):
				t.Fatal(what)
			}
		case chan MetricsView:
			select {
			case mv := <-c:
				assert.Equal(t, "unavailable", mv.Status, what)
			case <-time.After(5 * time.Second):
				t.Fatal(what)
			}
		}
	}

	first := ask(1)
	wait("seq 1 starts", ms.started)
	second := ask(2)
	wait("seq 2 starts", ms.started)
	wait("seq 1 ends when seq 2 starts", ms.ended)
	wait("seq 1 answers", first)

	require.NoError(t, s.CancelMetrics(ctx, info.ViewID, 2))
	wait("CancelMetrics ends seq 2", ms.ended)
	wait("seq 2 answers", second)

	// Given up before it started: a cancel that overtook the call, and a
	// call a newer one overtook.
	require.NoError(t, s.CancelMetrics(ctx, info.ViewID, 3))
	wait("seq 3 is refused at once", ask(3))
	fifth := ask(5)
	wait("seq 5 starts", ms.started)
	wait("seq 4 is refused at once", ask(4))
	ms.mu.Lock()
	assert.Len(t, ms.asked, 3, "seq 3 and 4 never reached the provider")
	ms.mu.Unlock()

	require.NoError(t, s.CloseView(ctx, info.ViewID))
	wait("closing the view ends seq 5", ms.ended)
	wait("seq 5 answers", fifth)
	s.metricsMu.Lock()
	assert.Empty(t, s.metricGates, "nothing kept for a closed view")
	s.metricsMu.Unlock()
}

// A view closed after a request read it but before the request took its
// place never reaches the provider, and leaves nothing behind.
func TestGetMetricsOfAViewClosedMeanwhile(t *testing.T) {
	ms := &metricSession{m: provider.Metrics{Values: map[string]provider.Usage{"1": {CPU: provider.Num(1)}}}}
	k := &metricOpenable{openable: newOpenable("a"), ms: ms}
	s, _ := newService(t, k)
	ctx := context.Background()
	info, err := s.OpenView(ctx, OpenViewRequest{Provider: "k", Target: "a", Query: provider.Query{Kind: "pods", Scope: allScopes}})
	require.NoError(t, err)
	_, err = s.OpenView(ctx, OpenViewRequest{Provider: "k", Target: "a", Query: provider.Query{Kind: "pods", Scope: allScopes}}) // the session lives on
	require.NoError(t, err)
	s.beforeMetricsGate = func() { require.NoError(t, s.CloseView(ctx, info.ViewID)) }
	_, err = s.GetMetrics(ctx, MetricsRequest{ViewID: info.ViewID, RowIDs: []string{"1"}, Seq: 1})
	assert.True(t, IsCoded(err, CodeGone), "%v", err)
	assert.Empty(t, ms.asked, "the provider was not asked")
	s.metricsMu.Lock()
	assert.Empty(t, s.metricGates, "no gate brought back")
	s.metricsMu.Unlock()
}

func TestIdleSessionsAreReaped(t *testing.T) {
	ctx := context.Background()
	k := newOpenable("a", "b")
	s, _ := newService(t, k)
	now := time.Unix(1000, 0)
	s.now = func() time.Time { return now }
	infoA, err := s.OpenView(ctx, OpenViewRequest{Provider: "k", Target: "a", Query: provider.Query{Kind: "pods", Scope: allScopes}})
	require.NoError(t, err)
	_, err = s.ListKinds(ctx, "k", "b") // a session nobody opens views on
	require.NoError(t, err)

	now = now.Add(sessionIdle + time.Second)
	s.reapIdleSessions()
	assert.False(t, k.opened[0].isClosed(), "a session with a view stays")
	assert.True(t, k.opened[1].isClosed(), "an unused session goes")

	require.NoError(t, s.CloseView(ctx, infoA.ViewID))
	s.reapIdleSessions() // was in use a moment ago: kept
	assert.False(t, k.opened[0].isClosed())
	now = now.Add(sessionIdle + time.Second)
	s.reapIdleSessions()
	assert.True(t, k.opened[0].isClosed())
}

func TestViewsBelongToOneSessionIncarnation(t *testing.T) {
	ctx := context.Background()
	ms := &metricSession{m: provider.Metrics{Values: map[string]provider.Usage{"1": {CPU: provider.Num(1)}}}}
	k := &metricOpenable{openable: newOpenable("a"), ms: ms}
	s, _ := newService(t, k)
	old, err := s.OpenView(ctx, OpenViewRequest{Provider: "k", Target: "a", Query: provider.Query{Kind: "pods", Scope: allScopes}})
	require.NoError(t, err)
	oldOwner := s.sessions[ownerKey("k", "a")].owner

	k.setHash("a", "h2")
	s.revalidateSessions(ctx, "k")
	fresh, err := s.OpenView(ctx, OpenViewRequest{Provider: "k", Target: "a", Query: provider.Query{Kind: "pods", Scope: allScopes}})
	require.NoError(t, err)
	assert.NotEqual(t, oldOwner, s.sessions[ownerKey("k", "a")].owner, "a new incarnation, a new owner")

	_, err = s.GetMetrics(ctx, MetricsRequest{ViewID: old.ViewID, RowIDs: []string{"1"}})
	assert.True(t, IsCoded(err, CodeGone), "the old view went with its session")
	mv, err := s.GetMetrics(ctx, MetricsRequest{ViewID: fresh.ViewID, RowIDs: []string{"1"}})
	require.NoError(t, err)
	assert.Equal(t, "ok", mv.Status)
}

// resyncSession records the queries Resync was asked for, per incarnation.
type resyncSession struct {
	*fakeSession
	mu  sync.Mutex
	got []provider.Query
	err error
}

func (r *resyncSession) Resync(q provider.Query) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, q)
	return r.err
}

type resyncOpenable struct {
	*openable
	sessions []*resyncSession
}

func (o *resyncOpenable) Open(ctx context.Context, target string) (provider.Session, error) {
	s, _ := o.openable.Open(ctx, target)
	rs := &resyncSession{fakeSession: s.(*fakeSession)}
	o.sessions = append(o.sessions, rs)
	return rs, nil
}

func TestResyncGoesToTheViewsOwnSession(t *testing.T) {
	ctx := context.Background()
	k := &resyncOpenable{openable: newOpenable("a")}
	s, _ := newService(t, k)
	q := provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "demo"}}
	old, err := s.OpenView(ctx, OpenViewRequest{Provider: "k", Target: "a", Query: q})
	require.NoError(t, err)
	assert.True(t, old.Resync, "the session can read again: the UI offers it")

	require.NoError(t, s.ResyncView(ctx, old.ViewID))
	require.Len(t, k.sessions, 1)
	assert.Equal(t, []provider.Query{q}, k.sessions[0].got, "the view's own query")

	// A new incarnation: the old view is gone and never reaches the new session.
	k.setHash("a", "h2")
	s.revalidateSessions(ctx, "k")
	fresh, err := s.OpenView(ctx, OpenViewRequest{Provider: "k", Target: "a", Query: q})
	require.NoError(t, err)
	require.Len(t, k.sessions, 2)
	err = s.ResyncView(ctx, old.ViewID)
	assert.True(t, IsCoded(err, CodeGone), "%v", err)
	assert.Empty(t, k.sessions[1].got)

	k.sessions[1].err = &provider.Error{Class: provider.ClassUnavailable, Message: "daemon down"}
	err = s.ResyncView(ctx, fresh.ViewID)
	assert.True(t, IsCoded(err, "unavailable"), "%v", err)

	require.NoError(t, s.CloseView(ctx, fresh.ViewID))
	assert.True(t, IsCoded(s.ResyncView(ctx, fresh.ViewID), CodeGone))
}

func TestResyncIsUnsupportedWithoutAResyncer(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t, newOpenable("a"))
	info, err := s.OpenView(ctx, OpenViewRequest{Provider: "k", Target: "a", Query: provider.Query{Kind: "pods", Scope: allScopes}})
	require.NoError(t, err)
	assert.False(t, info.Resync)
	assert.True(t, IsCoded(s.ResyncView(ctx, info.ViewID), CodeUnsupported))
}
