package api

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// agentProvider opens agentSessions: scriptable kinds, watches, logs,
// metrics and identity.
type agentProvider struct {
	openable
	mu2      sync.Mutex
	sessions []*agentSession
	script   func(s *agentSession)
	identity string
}

func newAgentProvider(targets ...string) *agentProvider {
	p := &agentProvider{openable: *newOpenable(targets...), identity: "https://a | ca:1 | user:u"}
	return p
}

func (p *agentProvider) Discover(ctx context.Context) (provider.Discovery, error) {
	d, err := p.openable.Discover(ctx)
	for i := range d.Targets {
		d.Targets[i].Identity = p.identity
		d.Targets[i].Title = "Title " + d.Targets[i].ID
	}
	return d, err
}

func (p *agentProvider) Open(ctx context.Context, target string) (provider.Session, error) {
	fs, _ := p.openable.Open(ctx, target)
	s := &agentSession{fakeSession: fs.(*fakeSession), watches: map[string]int{}}
	s.watch = func(_ provider.Query, sink provider.Sink) (func(), error) {
		sink.Apply(provider.Delta{Reset: true, Upserts: []core.Row{row("b", "web-2"), row("a", "web-1")}, Status: &provider.ViewStatus{State: provider.StatusReady}})
		return func() {}, nil
	}
	if p.script != nil {
		p.script(s)
	}
	p.mu2.Lock()
	p.sessions = append(p.sessions, s)
	p.mu2.Unlock()
	return s, nil
}

func row(scope, name string) core.Row {
	return core.Row{ID: "uid-" + name, Ref: core.Ref{Provider: "k", Target: "a", Scope: scope, Kind: "pods", Name: name, UID: "uid-" + name}}
}

type agentSession struct {
	*fakeSession
	mu       sync.Mutex
	watch    func(q provider.Query, sink provider.Sink) (func(), error)
	watches  map[string]int
	queries  []provider.Query
	stops    atomic.Int32
	describe func(q provider.Query) (core.KindDescriptor, provider.Query, error)
	removed  map[string]bool
	logs     func(q provider.LogQuery, sink provider.LogSink) error
	metrics  func(q provider.Query, ids []string) (provider.Metrics, error)
	daemon   func() (string, error)
}

func (s *agentSession) Kinds() []core.KindDescriptor {
	return []core.KindDescriptor{
		{ID: "pods", Title: "Pods", Scoped: true, Columns: []core.Column{{ID: "name", Title: "Name"}}},
		{ID: "nodes", Title: "Nodes"},
		{ID: "example.com/widgets", Title: "Widgets", Scoped: true},
	}
}

func (s *agentSession) Watch(q provider.Query, sink provider.Sink) (func(), error) {
	s.mu.Lock()
	s.watches[q.Kind]++
	s.queries = append(s.queries, q)
	s.mu.Unlock()
	stop, err := s.watch(q, sink)
	if err != nil {
		return nil, err
	}
	return func() { s.stops.Add(1); stop() }, nil
}

// describedSession adds a ViewDescriber and a Cataloger's KindRemoved.
type describedSession struct{ *agentSession }

func (s describedSession) DescribeView(_ context.Context, q provider.Query) (core.KindDescriptor, provider.Query, error) {
	return s.describe(q)
}
func (s describedSession) Catalog() core.KindCatalog {
	return core.KindCatalog{Kinds: s.Kinds(), Rev: 1, State: core.CatalogReady}
}
func (s describedSession) OnKindsChanged(func(uint64)) {}
func (s describedSession) RefreshKinds()               {}
func (s describedSession) KindRemoved(id string) bool  { return s.removed[id] }

type loggingSession struct{ *agentSession }

func (s loggingSession) LogInfo(context.Context, core.Ref) (core.LogInfo, error) {
	return core.LogInfo{}, nil
}
func (s loggingSession) StreamLogs(_ context.Context, _ core.Ref, q provider.LogQuery, sink provider.LogSink) error {
	return s.logs(q, sink)
}

type meteredSession struct{ *agentSession }

func (s meteredSession) Metrics(_ context.Context, q provider.Query, ids []string) (provider.Metrics, error) {
	return s.metrics(q, ids)
}

type identifiedSession struct{ *agentSession }

func (s identifiedSession) Identity(context.Context) (string, error) { return s.daemon() }

// agentWrapped opens sessions of the wrapper type w builds.
type agentWrapped struct {
	*agentProvider
	wrap func(*agentSession) provider.Session
}

func (w agentWrapped) Open(ctx context.Context, target string) (provider.Session, error) {
	s, err := w.agentProvider.Open(ctx, target)
	if err != nil {
		return nil, err
	}
	return w.wrap(s.(*agentSession)), nil
}

func (p *agentProvider) last() *agentSession {
	p.mu2.Lock()
	defer p.mu2.Unlock()
	return p.sessions[len(p.sessions)-1]
}

func call(t *testing.T, s *Service, target string) *AgentCall {
	t.Helper()
	c, err := s.AgentCall(context.Background(), "k", target)
	require.NoError(t, err)
	t.Cleanup(c.Done)
	return c
}

// An agent's call keeps its session through the UI selecting another
// target, and for sessionIdle after it ends; then the reaper closes it.
func TestAgentCallsSpareTheirSession(t *testing.T) {
	ctx := context.Background()
	k := newAgentProvider("a", "b")
	s, _ := newService(t, k)
	now := time.Unix(1000, 0)
	s.now = func() time.Time { return now }

	c := call(t, s, "a")
	require.NoError(t, s.SelectTarget(ctx, "k", "b"))
	assert.False(t, k.opened[0].isClosed(), "a call in progress")
	c.Done()
	now = now.Add(30 * time.Second)
	require.NoError(t, s.SelectTarget(ctx, "k", "b"))
	assert.False(t, k.opened[0].isClosed(), "within a minute of the last call")
	now = now.Add(sessionIdle)
	s.reapIdleSessions()
	assert.True(t, k.opened[0].isClosed(), "idle since")
}

// A long call: its session is idle from the call's end, not its start.
func TestAgentCallsIdleFromTheirEnd(t *testing.T) {
	k := newAgentProvider("a")
	s, _ := newService(t, k)
	now := time.Unix(1000, 0)
	s.now = func() time.Time { return now }
	c := call(t, s, "a")
	now = now.Add(2 * sessionIdle)
	s.reapIdleSessions()
	assert.False(t, k.opened[0].isClosed(), "in progress")
	c.Done()
	now = now.Add(sessionIdle / 2)
	s.reapIdleSessions()
	assert.False(t, k.opened[0].isClosed(), "ended half a minute ago")
	now = now.Add(sessionIdle)
	s.reapIdleSessions()
	assert.True(t, k.opened[0].isClosed())

	// No reaping during the call this time: its end counts all the same.
	c = call(t, s, "a")
	now = now.Add(2 * sessionIdle)
	c.Done()
	now = now.Add(sessionIdle / 2)
	s.reapIdleSessions()
	assert.False(t, k.opened[1].isClosed(), "ended half a minute ago")
}

func TestSelectingATargetDuringATailKeepsIt(t *testing.T) {
	ctx := context.Background()
	k := newAgentProvider("a", "b")
	started, release := make(chan struct{}), make(chan struct{})
	p := agentWrapped{k, func(a *agentSession) provider.Session {
		a.logs = func(_ provider.LogQuery, sink provider.LogSink) error {
			close(started)
			<-release
			_ = sink.Source(1, "k1", "web-1", "main")
			_ = sink.Lines(1, []provider.LogLine{{TS: "t1", Text: "hello"}})
			return sink.Ready()
		}
		return loggingSession{a}
	}}
	s, _ := newService(t, p)
	c := call(t, s, "a")
	done := make(chan Tail, 1)
	go func() {
		tl, err := c.TailLogs(TailRequest{Ref: row("a", "web-1").Ref, TailLines: 10})
		assert.NoError(t, err)
		done <- tl
	}()
	<-started
	require.NoError(t, s.SelectTarget(ctx, "k", "b"))
	close(release)
	tl := <-done
	require.Len(t, tl.Lines, 1)
	assert.False(t, k.opened[0].isClosed())
}

// A configuration change closes the session: the call's work ends, gone.
func TestAConfigChangeEndsAnAgentsCall(t *testing.T) {
	k := newAgentProvider("a")
	k.script = func(a *agentSession) {
		a.watch = func(provider.Query, provider.Sink) (func(), error) { return func() {}, nil } // never ready
	}
	s, _ := newService(t, k)
	c := call(t, s, "a")
	errc := make(chan error, 1)
	go func() {
		_, err := c.Snapshot(SnapshotRequest{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "a"}})
		errc <- err
	}()
	require.Eventually(t, func() bool { k.last().mu.Lock(); defer k.last().mu.Unlock(); return k.last().watches["pods"] == 1 }, 2*time.Second, 5*time.Millisecond)
	k.setHash("a", "h2")
	s.revalidateSessions(context.Background(), "k")
	select {
	case err := <-errc:
		assert.True(t, IsCoded(err, CodeGone), "%v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("the snapshot outlived its session")
	}
	assert.Equal(t, int32(1), k.last().stops.Load(), "its watch stopped")
}

// A tail whose session closed meanwhile (a configuration change) is gone,
// not a complete answer, though the provider's backlog ended without error.
func TestATailOfAClosedSessionIsGone(t *testing.T) {
	k := newAgentProvider("a")
	started, release := make(chan struct{}), make(chan struct{})
	p := agentWrapped{k, func(a *agentSession) provider.Session {
		a.logs = func(_ provider.LogQuery, sink provider.LogSink) error {
			close(started)
			<-release
			_ = sink.Source(1, "k1", "web-1", "main")
			_ = sink.Lines(1, []provider.LogLine{{TS: "t1", Text: "part"}})
			return nil
		}
		return loggingSession{a}
	}}
	s, _ := newService(t, p)
	c := call(t, s, "a")
	errc := make(chan error, 1)
	go func() {
		_, err := c.TailLogs(TailRequest{Ref: row("a", "web-1").Ref, TailLines: 10})
		errc <- err
	}()
	<-started
	k.setHash("a", "h2")
	s.revalidateSessions(context.Background(), "k")
	close(release)
	assert.True(t, IsCoded(<-errc, CodeGone))
}

func TestSnapshotAppliesDeltasByContract(t *testing.T) {
	k := newAgentProvider("a")
	k.script = func(a *agentSession) {
		a.watch = func(_ provider.Query, sink provider.Sink) (func(), error) {
			sink.Apply(provider.Delta{Upserts: []core.Row{row("a", "old")}})
			go func() {
				sink.Apply(provider.Delta{Reset: true, Upserts: []core.Row{row("a", "x"), row("a", "gone"), row("b", "y")}, Deletes: []string{"uid-gone"}})
				sink.Apply(provider.Delta{Upserts: []core.Row{row("a", "z")}, Status: &provider.ViewStatus{State: provider.StatusReady}})
				sink.Apply(provider.Delta{Upserts: []core.Row{row("a", "late")}})
			}()
			return func() {}, nil
		}
	}
	s, em := newService(t, k)
	sub, unsub := em.Subscribe()
	defer unsub()
	c := call(t, s, "a")
	snap, err := c.Snapshot(SnapshotRequest{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeAll}})
	require.NoError(t, err)
	var names []string
	for _, r := range snap.Rows {
		names = append(names, r.Ref.Scope+"/"+r.Ref.Name)
	}
	assert.Equal(t, []string{"a/x", "a/z", "b/y"}, names, "reset drops old, deletes after upserts, sorted by scope and name")
	assert.Equal(t, provider.StatusReady, snap.Status.State)
	assert.Equal(t, "Pods", snap.Kind.Title)
	assert.Equal(t, int32(1), k.last().stops.Load())
	var heard []string
	for _, ev := range sub.Drain() {
		heard = append(heard, ev.Type)
	}
	// Only that the target's session opened (its "open" dot, P18).
	assert.Equal(t, []string{EventTargetsChanged}, heard, "the UI hears nothing of an agent's snapshot")
}

func TestSnapshotStates(t *testing.T) {
	old := snapshotWait
	snapshotWait = 50 * time.Millisecond
	t.Cleanup(func() { snapshotWait = old })
	one := core.ScopeSel{Mode: core.ScopeOne, Name: "a"}

	k := newAgentProvider("a")
	k.script = func(a *agentSession) {
		a.watch = func(q provider.Query, sink provider.Sink) (func(), error) {
			switch q.Scope.Name {
			case "slow":
				sink.Apply(provider.Delta{Upserts: []core.Row{row("slow", "p")}})
			case "denied":
				sink.Apply(provider.Delta{Status: &provider.ViewStatus{State: provider.StatusError, Class: provider.ClassForbidden, Message: "pods is forbidden"}})
			case "broken":
				return nil, &provider.Error{Class: provider.ClassUnavailable, Message: "no server"}
			default:
				sink.Apply(provider.Delta{Status: &provider.ViewStatus{State: provider.StatusStale, Class: provider.ClassUnavailable}})
			}
			return func() {}, nil
		}
	}
	s, _ := newService(t, k)
	c := call(t, s, "a")
	snap, err := c.Snapshot(SnapshotRequest{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "slow"}})
	require.NoError(t, err)
	assert.Equal(t, provider.StatusLoading, snap.Status.State, "not ready in time: what came, said so")
	assert.Len(t, snap.Rows, 1)
	snap, err = c.Snapshot(SnapshotRequest{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "denied"}})
	require.NoError(t, err)
	assert.Equal(t, provider.ClassForbidden, snap.Status.Class)
	_, err = c.Snapshot(SnapshotRequest{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "broken"}})
	assert.True(t, IsCoded(err, string(provider.ClassUnavailable)))
	snap, err = c.Snapshot(SnapshotRequest{Kind: "pods", Scope: one})
	require.NoError(t, err)
	assert.Equal(t, provider.StatusStale, snap.Status.State)
	_, err = c.Snapshot(SnapshotRequest{Kind: "nope", Scope: one})
	assert.True(t, IsCoded(err, CodeUnsupported))
	_, err = c.Snapshot(SnapshotRequest{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeOne}})
	assert.True(t, IsCoded(err, CodeBadRequest))
}

// A described kind (server-side columns) is opened as OpenView opens it;
// a schema change while snapshotting is retried once.
func TestSnapshotOfADescribedKind(t *testing.T) {
	k := newAgentProvider("a")
	var describes atomic.Int32
	failFirst := atomic.Int32{}
	failFirst.Store(1)
	p := agentWrapped{k, func(a *agentSession) provider.Session {
		a.removed = map[string]bool{"example.com/gadgets": true}
		a.describe = func(q provider.Query) (core.KindDescriptor, provider.Query, error) {
			n := describes.Add(1)
			q.Schema = uint64(n)
			return core.KindDescriptor{ID: q.Kind, Title: "Widgets", Scoped: true, Columns: []core.Column{{ID: "name", Title: "Name"}, {ID: fmt.Sprint("col", n), Title: "Server"}}}, q, nil
		}
		a.watch = func(_ provider.Query, sink provider.Sink) (func(), error) {
			if failFirst.Add(-1) >= 0 {
				return nil, &provider.Error{Class: provider.ClassSchemaChanged, Message: "the columns changed"}
			}
			sink.Apply(provider.Delta{Reset: true, Upserts: []core.Row{row("a", "w")}, Status: &provider.ViewStatus{State: provider.StatusReady}})
			return func() {}, nil
		}
		return describedSession{a}
	}}
	s, _ := newService(t, p)
	c := call(t, s, "a")
	snap, err := c.Snapshot(SnapshotRequest{Kind: "example.com/widgets", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "a"}})
	require.NoError(t, err)
	assert.Equal(t, "col2", snap.Kind.Columns[1].ID, "the columns of the view that answered")
	qs := k.last().queries
	require.Len(t, qs, 2)
	assert.Equal(t, uint64(2), qs[1].Schema, "bound to its descriptor")

	failFirst.Store(2)
	_, err = c.Snapshot(SnapshotRequest{Kind: "example.com/widgets", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "a"}})
	assert.True(t, IsCoded(err, string(provider.ClassSchemaChanged)), "changing twice: said")

	_, err = c.Snapshot(SnapshotRequest{Kind: "example.com/gadgets", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "a"}})
	assert.True(t, IsCoded(err, CodeRemoved))
}

// At most maxAgentWatches watches of agents at once; a call waits within
// its own time, then limit.
func TestAgentWatchesAreBounded(t *testing.T) {
	k := newAgentProvider("a")
	release := make(chan struct{})
	var started atomic.Int32
	k.script = func(a *agentSession) {
		a.watch = func(_ provider.Query, sink provider.Sink) (func(), error) {
			started.Add(1)
			go func() {
				<-release
				sink.Apply(provider.Delta{Status: &provider.ViewStatus{State: provider.StatusReady}})
			}()
			return func() {}, nil
		}
	}
	s, _ := newService(t, k)
	c := call(t, s, "a")
	var wg sync.WaitGroup
	for range maxAgentWatches {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.Snapshot(SnapshotRequest{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeAll}})
			assert.NoError(t, err)
		}()
	}
	require.Eventually(t, func() bool { return started.Load() == maxAgentWatches }, 2*time.Second, 5*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	c2, err := s.AgentCall(ctx, "k", "a")
	require.NoError(t, err)
	_, err = c2.Snapshot(SnapshotRequest{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeAll}})
	c2.Done()
	assert.True(t, IsCoded(err, CodeLimit), "%v", err)
	assert.Equal(t, int32(maxAgentWatches), started.Load())
	close(release)
	wg.Wait()
}

func TestTailLogs(t *testing.T) {
	var asked provider.LogQuery
	k := newAgentProvider("a")
	p := agentWrapped{k, func(a *agentSession) provider.Session {
		a.logs = func(q provider.LogQuery, sink provider.LogSink) error {
			asked = q
			// As a pod's own stream does: Ready first (its backlog is the
			// start of its stream), then the lines; without Follow the
			// stream ends by itself.
			_ = sink.Source(1, "k1", "web-1", "main")
			_ = sink.Source(2, "k2", "web-2", "main")
			if err := sink.Ready(); err != nil {
				return err
			}
			_ = sink.Lines(1, []provider.LogLine{{TS: "1970-01-01T00:01:40Z", Text: "one"}, {TS: "1970-01-01T00:01:41Z", Text: strings.Repeat("x", 100), Flags: provider.LineCut}})
			_ = sink.State(2, provider.LogState{State: provider.LogWaiting, Message: "not started"})
			_ = sink.Lines(2, []provider.LogLine{{TS: "1970-01-01T00:01:42Z", Text: "two"}})
			return nil
		}
		return loggingSession{a}
	}}
	s, _ := newService(t, p)
	c := call(t, s, "a")
	since := time.Unix(100, 0)
	tl, err := c.TailLogs(TailRequest{Ref: row("a", "web-1").Ref, Channel: "main", TailLines: 50, SinceTime: since, Limit: 50})
	require.NoError(t, err)
	assert.Equal(t, provider.LogQuery{Channel: "main", TailLines: 50, SinceTime: since}, asked, "never followed")
	require.Len(t, tl.Sources, 2)
	assert.Equal(t, "web-2", tl.Sources[1].Label)
	assert.Equal(t, provider.LogWaiting, tl.Sources[1].State.State)
	require.Len(t, tl.Lines, 3)
	assert.Equal(t, TailLine{Source: 1, TS: "1970-01-01T00:01:41Z", Text: strings.Repeat("x", 100), Cut: true}, tl.Lines[1])
	assert.False(t, tl.Truncated)

	old := maxTailBytes
	maxTailBytes = 50
	t.Cleanup(func() { maxTailBytes = old })
	tl, err = c.TailLogs(TailRequest{Ref: row("a", "web-1").Ref, TailLines: 50})
	require.NoError(t, err)
	assert.True(t, tl.Truncated, "older lines left out")
	require.Len(t, tl.Lines, 1)
	assert.Equal(t, "two", tl.Lines[0].Text, "the newest lines are kept")

	for _, n := range []int{0, -2, maxTailLines + 1} {
		_, err = c.TailLogs(TailRequest{Ref: row("a", "web-1").Ref, TailLines: n})
		assert.True(t, IsCoded(err, CodeBadRequest), "%d", n)
	}
}

// The whole stream's state (id 0: a group showing only some of its pods)
// reaches the agent, and a limited group is a truncated tail.
func TestATailSaysTheGroupsState(t *testing.T) {
	k := newAgentProvider("a")
	p := agentWrapped{k, func(a *agentSession) provider.Session {
		a.logs = func(_ provider.LogQuery, sink provider.LogSink) error {
			_ = sink.State(0, provider.LogState{State: provider.LogLimited, Message: "showing 20 of 25 streams"})
			_ = sink.Source(1, "k1", "web-1", "main")
			_ = sink.Lines(1, []provider.LogLine{{TS: "t1", Text: "one"}})
			return nil
		}
		return loggingSession{a}
	}}
	s, _ := newService(t, p)
	tl, err := call(t, s, "a").TailLogs(TailRequest{Ref: row("a", "web-1").Ref, TailLines: 10})
	require.NoError(t, err)
	require.NotNil(t, tl.State)
	assert.Equal(t, provider.LogState{State: provider.LogLimited, Message: "showing 20 of 25 streams"}, *tl.State)
	assert.True(t, tl.Truncated)
	require.Len(t, tl.Lines, 1)
}

// A provider ends a tail at the deadline with what came (the others of a
// group) and no error: still truncated.
func TestATailEndedByItsDeadlineIsTruncated(t *testing.T) {
	old := tailWait
	tailWait = 50 * time.Millisecond
	t.Cleanup(func() { tailWait = old })
	k := newAgentProvider("a")
	p := agentWrapped{k, func(a *agentSession) provider.Session {
		a.logs = func(_ provider.LogQuery, sink provider.LogSink) error {
			_ = sink.Source(1, "k1", "web-1", "main")
			time.Sleep(200 * time.Millisecond) // past the deadline
			_ = sink.Lines(1, []provider.LogLine{{TS: "t1", Text: "came"}})
			return nil
		}
		return loggingSession{a}
	}}
	s, _ := newService(t, p)
	tl, err := call(t, s, "a").TailLogs(TailRequest{Ref: row("a", "web-1").Ref, TailLines: 10})
	require.NoError(t, err)
	require.Len(t, tl.Lines, 1)
	assert.True(t, tl.Truncated)
}

func TestTailLogsWithoutLogs(t *testing.T) {
	s, _ := newService(t, newAgentProvider("a"))
	c := call(t, s, "a")
	_, err := c.TailLogs(TailRequest{Ref: row("a", "web-1").Ref, TailLines: 10})
	assert.True(t, IsCoded(err, CodeUnsupported))
}

func TestAgentMetricsOfASnapshotsRows(t *testing.T) {
	k := newAgentProvider("a")
	var gotQ provider.Query
	var gotIDs []string
	p := agentWrapped{k, func(a *agentSession) provider.Session {
		a.metrics = func(q provider.Query, ids []string) (provider.Metrics, error) {
			gotQ, gotIDs = q, ids
			cpu := 0.5
			return provider.Metrics{Window: "30s", Values: map[string]provider.Usage{"uid-web-1": {CPU: &cpu}, "uid-other": {CPU: &cpu}}}, nil
		}
		return meteredSession{a}
	}}
	s, _ := newService(t, p)
	c := call(t, s, "a")
	snap, err := c.Snapshot(SnapshotRequest{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "a"}})
	require.NoError(t, err)
	mv, err := c.Metrics(snap, []string{"uid-web-1", "uid-web-2"})
	require.NoError(t, err)
	assert.Equal(t, "ok", mv.Status)
	assert.Equal(t, "pods", gotQ.Kind)
	assert.Equal(t, []string{"uid-web-1", "uid-web-2"}, gotIDs)
	assert.Len(t, mv.Values, 1, "only the rows asked")

	s2, _ := newService(t, newAgentProvider("a"))
	c2 := call(t, s2, "a")
	snap, _ = c2.Snapshot(SnapshotRequest{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "a"}})
	mv, err = c2.Metrics(snap, []string{"uid-web-1"})
	require.NoError(t, err)
	assert.Equal(t, CodeUnsupported, mv.Status)
}

func TestAgentCallIdentity(t *testing.T) {
	k := newAgentProvider("a")
	s, _ := newService(t, k)
	c := call(t, s, "a")
	id, err := c.Identity()
	require.NoError(t, err)
	assert.Equal(t, "https://a | ca:1 | user:u", id)
	assert.Equal(t, "Title a", c.Title())

	daemon := "daemon:X"
	var derr error
	p := agentWrapped{newAgentProvider("a"), func(a *agentSession) provider.Session {
		a.daemon = func() (string, error) { return daemon, derr }
		return identifiedSession{a}
	}}
	s2, _ := newService(t, p)
	c2 := call(t, s2, "a")
	id, err = c2.Identity()
	require.NoError(t, err)
	assert.Equal(t, "https://a | ca:1 | user:u | daemon:X", id)
	derr = &provider.Error{Class: provider.ClassUnavailable, Message: "no daemon"}
	_, err = c2.Identity()
	assert.True(t, IsCoded(err, string(provider.ClassUnavailable)))
}

// Without the agent socket the UI's agent methods say so.
func TestAgentAccessWithoutTheSocket(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t, newAgentProvider("a"))
	_, err := s.AgentAccessStatus(ctx)
	assert.True(t, IsCoded(err, CodeUnsupported))
	_, err = s.ListAgentGrants(ctx)
	assert.True(t, IsCoded(err, CodeUnsupported))
	assert.True(t, IsCoded(s.DecideAgentPending(ctx, DecideAgentPendingRequest{ID: "x", Approve: true}), CodeUnsupported))
}
