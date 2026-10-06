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
	"github.com/spk/spk-ocular/internal/events"
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
	assert.Equal(t, "pods", kinds.Kinds[0].ID)

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

// visit selects target and opens its session (the page's first call).
func visit(t *testing.T, s *Service, target string) {
	t.Helper()
	require.NoError(t, s.SelectTarget(context.Background(), "k", target))
	_, err := s.ListKinds(context.Background(), "k", target)
	require.NoError(t, err)
}

// The two targets left last stay open (switching back is instant); one
// left before them closes when another is selected.
func TestLeftTargetsStayOpenTwoOfThem(t *testing.T) {
	k := newOpenable("a", "b", "c", "d")
	s, _ := newService(t, k)
	visit(t, s, "a")
	visit(t, s, "b")
	visit(t, s, "c")
	for i := range 3 {
		assert.False(t, k.opened[i].isClosed(), "c current; a, b left last")
	}
	visit(t, s, "d")
	assert.True(t, k.opened[0].isClosed(), "a was left before b and c")
	assert.False(t, k.opened[1].isClosed())
	assert.False(t, k.opened[2].isClosed())

	visit(t, s, "b") // back: the same session, not another Open
	assert.Len(t, k.opened, 4)
	assert.False(t, k.opened[1].isClosed())
	visit(t, s, "a") // left: d, b's before it is c — c goes
	assert.True(t, k.opened[2].isClosed(), "c was left before d and b")
	assert.False(t, k.opened[3].isClosed())
}

// A left target's session idles for recentIdle; the others for sessionIdle.
func TestALeftTargetIdlesLonger(t *testing.T) {
	k := newOpenable("a", "b")
	s, _ := newService(t, k)
	now := time.Unix(1000, 0)
	s.now = func() time.Time { return now }
	visit(t, s, "a")
	visit(t, s, "b")
	now = now.Add(sessionIdle + time.Second)
	s.reapIdleSessions()
	assert.False(t, k.opened[0].isClosed(), "a, left: kept")
	assert.True(t, k.opened[1].isClosed(), "b, current without views: as before")
	now = now.Add(recentIdle)
	s.reapIdleSessions()
	assert.True(t, k.opened[0].isClosed(), "a, left over recentIdle ago")
}

// recentIdle counts from leaving, not from the session's last recorded use.
func TestARecentTargetIdlesFromLeavingIt(t *testing.T) {
	ctx := context.Background()
	k := newOpenable("a", "b")
	s, _ := newService(t, k)
	now := time.Unix(1000, 0)
	s.now = func() time.Time { return now }
	require.NoError(t, s.SelectTarget(ctx, "k", "a"))
	info, err := s.OpenView(ctx, OpenViewRequest{Provider: "k", Target: "a", Query: provider.Query{Kind: "pods", Scope: allScopes}})
	require.NoError(t, err)
	s.reapIdleSessions() // in use now
	now = now.Add(50 * time.Second)
	require.NoError(t, s.CloseView(ctx, info.ViewID)) // the page goes
	visit(t, s, "b")
	now = now.Add(recentIdle - 5*time.Second)
	s.reapIdleSessions()
	assert.False(t, k.opened[0].isClosed(), "left less than recentIdle ago")
}

// Back to a left target: it is current again, not recent — current idle
// rules, and it takes no recent place.
func TestATargetSelectedAgainIsNotRecent(t *testing.T) {
	k := newOpenable("a", "b")
	s, _ := newService(t, k)
	now := time.Unix(1000, 0)
	s.now = func() time.Time { return now }
	visit(t, s, "a")
	visit(t, s, "b")
	visit(t, s, "a")
	now = now.Add(sessionIdle + time.Second)
	s.reapIdleSessions()
	assert.True(t, k.opened[0].isClosed(), "a, current without views: as before")
	assert.False(t, k.opened[1].isClosed(), "b, left")
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

// bgSession records what it was told about the background.
type bgSession struct {
	*fakeSession
	mu    sync.Mutex
	calls []bool
	lost  func()
}

func (b *bgSession) SetBackground(on bool, lost func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, on)
	b.lost = lost
}

func (b *bgSession) told() []bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]bool(nil), b.calls...)
}

type bgOpenable struct {
	*openable
	mu       sync.Mutex
	sessions map[string][]*bgSession
}

func (o *bgOpenable) Open(ctx context.Context, target string) (provider.Session, error) {
	s, _ := o.openable.Open(ctx, target)
	b := &bgSession{fakeSession: s.(*fakeSession)}
	o.mu.Lock()
	o.sessions[target] = append(o.sessions[target], b)
	o.mu.Unlock()
	return b, nil
}

func (o *bgOpenable) of(target string, n int) *bgSession {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.sessions[target][n]
}

func newBgOpenable(targets ...string) *bgOpenable {
	return &bgOpenable{openable: newOpenable(targets...), sessions: map[string][]*bgSession{}}
}

// A session of a target that is not the selected one is in the
// background — left by the user, or opened for an agent; selecting its
// target brings it back.
func TestSessionsOfTargetsNotSelectedAreInTheBackground(t *testing.T) {
	ctx := context.Background()
	k := newBgOpenable("a", "b", "c")
	s, _ := newService(t, k)
	visit(t, s, "a")
	assert.Empty(t, k.of("a", 0).told(), "the selected one: as it opened")
	visit(t, s, "b")
	assert.Equal(t, []bool{true}, k.of("a", 0).told())
	assert.Empty(t, k.of("b", 0).told())
	visit(t, s, "a")
	assert.Equal(t, []bool{true, false}, k.of("a", 0).told())
	assert.Equal(t, []bool{true}, k.of("b", 0).told())
	visit(t, s, "a") // again: nothing changes
	assert.Equal(t, []bool{true, false}, k.of("a", 0).told())

	_, err := s.ListKinds(ctx, "k", "c") // an agent's, say
	require.NoError(t, err)
	assert.Equal(t, []bool{true}, k.of("c", 0).told(), "opened for a target not selected")
}

// The selection remembered from the last run is the selected target before
// anything is selected in this one.
func TestTheRememberedSelectionIsNotInTheBackground(t *testing.T) {
	ctx := context.Background()
	k := newBgOpenable("a", "b")
	s, _ := newService(t, k)
	require.NoError(t, s.SelectTarget(ctx, "k", "b"))
	s2 := newServiceOn(t, s, k) // the next run, same store
	_, err := s2.ListKinds(ctx, "k", "b")
	require.NoError(t, err)
	assert.Empty(t, k.of("b", 0).told())
}

// lost: a background session that cannot go on without a person is closed;
// its log streams end saying a login is needed. Once; never another
// incarnation; never the selected target's.
func TestALostBackgroundSessionIsClosed(t *testing.T) {
	k := newBgOpenable("a", "b")
	s, em := newService(t, k)
	sub, unsub := em.Subscribe()
	defer unsub()
	visit(t, s, "a")
	visit(t, s, "b")
	a := k.of("a", 0)
	sub.Drain() // the openings' events (Emit delivers at once)
	a.lost()
	assert.True(t, a.isClosed())
	a.lost() // again: nothing
	require.Eventually(t, func() bool {
		select {
		case <-sub.Wake():
			for _, ev := range sub.Drain() {
				if ev.Type == EventTargetsChanged {
					return true
				}
			}
		default:
		}
		return false
	}, 2*time.Second, 10*time.Millisecond, "the target list learns it is closed")

	visit(t, s, "a") // a new incarnation, selected
	a.lost()         // the old one's late word
	assert.False(t, k.of("a", 1).isClosed())
	visit(t, s, "b")
	visit(t, s, "a")
	k.of("a", 1).lost() // selected meanwhile: the person is here
	assert.False(t, k.of("a", 1).isClosed())
}

// newServiceOn is another run of s's app: the same store, new sessions.
func newServiceOn(t *testing.T, s *Service, ps ...provider.Provider) *Service {
	t.Helper()
	reg, err := provider.NewRegistry(ps...)
	require.NoError(t, err)
	s2 := NewService(reg, s.store, events.NewEmitter(), s.opts)
	t.Cleanup(s2.Close)
	return s2
}

func openOf(t *testing.T, s *Service) map[string]bool {
	t.Helper()
	v, err := s.ListTargets(context.Background())
	require.NoError(t, err)
	out := map[string]bool{}
	for _, g := range v.Groups {
		for _, tg := range g.Targets {
			out[tg.ID] = tg.Open
		}
	}
	return out
}

// Targets say whether their session is open; the user closes one left
// behind (its watches, its log tabs), never the selected one.
func TestCloseTargetEndsALeftTargetsSession(t *testing.T) {
	ctx := context.Background()
	k := newOpenable("a", "b", "c")
	s, _ := newService(t, k)
	visit(t, s, "a")
	visit(t, s, "b")
	assert.Equal(t, map[string]bool{"a": true, "b": true, "c": false}, openOf(t, s))
	require.NoError(t, s.CloseTarget(ctx, "k", "a"))
	assert.True(t, k.opened[0].isClosed())
	assert.Equal(t, map[string]bool{"a": false, "b": true, "c": false}, openOf(t, s))
	require.NoError(t, s.CloseTarget(ctx, "k", "b"), "the selected target can disconnect")
	assert.True(t, k.opened[1].isClosed())
	require.NoError(t, s.CloseTarget(ctx, "k", "c"), "nothing open: nothing to do")
	assert.True(t, IsCoded(s.CloseTarget(ctx, "k", "zz"), CodeNotFound))

	// Closed, it is not recent any more: opened for an agent, it idles as
	// any other.
	now := time.Unix(1000, 0)
	s.now = func() time.Time { return now }
	_, err := s.ListKinds(ctx, "k", "a")
	require.NoError(t, err)
	now = now.Add(sessionIdle + time.Second)
	s.reapIdleSessions()
	assert.True(t, k.opened[2].isClosed())
}

// An agent's call in flight is not cut: the session closes as soon as the
// last call ends (not sessionIdle later).
func TestCloseTargetWaitsForAgentsCalls(t *testing.T) {
	ctx := context.Background()
	k := newOpenable("a", "b")
	s, _ := newService(t, k)
	visit(t, s, "a")
	visit(t, s, "b")
	c, err := s.AgentCall(ctx, "k", "a")
	require.NoError(t, err)
	require.NoError(t, s.CloseTarget(ctx, "k", "a"))
	assert.False(t, k.opened[0].isClosed())
	assert.NoError(t, c.Context().Err())
	c.Done()
	assert.True(t, k.opened[0].isClosed())
}

// identBg: a background-aware session whose target's identity needs a login.
type identBg struct{ *bgSession }

func (identBg) Identity(context.Context) (string, error) {
	return "", &provider.Error{Class: provider.ClassUnauthorized, Message: "the kubeconfig credential plugin failed or did not answer"}
}

type identBgOpenable struct{ *bgOpenable }

func (o identBgOpenable) Open(ctx context.Context, target string) (provider.Session, error) {
	s, _ := o.bgOpenable.Open(ctx, target)
	return identBg{s.(*bgSession)}, nil
}

// An agent's call on a target not selected that needs a login is told a
// person does it (the plugin ran headless); on the selected one, as before.
func TestAnAgentsCallNeedingALoginInTheBackgroundIsAPersons(t *testing.T) {
	ctx := context.Background()
	k := identBgOpenable{newBgOpenable("a", "b")}
	s, _ := newService(t, k)
	visit(t, s, "a")
	c, err := s.AgentCall(ctx, "k", "b")
	require.NoError(t, err)
	defer c.Done()
	_, err = c.Identity()
	var ce *CodedError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, "unauthorized", ce.Code)
	require.NotNil(t, ce.Why)
	assert.Equal(t, "api.loginByPerson", ce.Why.Key)

	ca, err := s.AgentCall(ctx, "k", "a")
	require.NoError(t, err)
	defer ca.Done()
	_, err = ca.Identity()
	require.ErrorAs(t, err, &ce)
	assert.Nil(t, ce.Why, "the selected target: the provider's own words")
}
