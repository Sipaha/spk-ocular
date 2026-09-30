package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/streams"
)

// echoHandle echoes input until its terminal ends; it records whether it
// was closed and whether its session was closed while it ran.
type echoHandle struct {
	owner  *execSession
	sess   *fakeSession
	closed atomic.Int32
}

func (h *echoHandle) Describe() core.LiveTarget {
	return core.LiveTarget{Provider: "k", Target: h.sess.target, TargetTitle: h.sess.target, ConfigHash: h.sess.hash, Instance: "p", Channel: "app"}
}
func (h *echoHandle) Again() (provider.ExecHandle, error) {
	c := &echoHandle{owner: h.owner, sess: h.sess}
	h.owner.mu.Lock()
	h.owner.runs = append(h.owner.runs, c)
	h.owner.mu.Unlock()
	return c, nil
}
func (h *echoHandle) Close() { h.closed.Add(1) }
func (h *echoHandle) Run(ctx context.Context, t provider.Terminal) (provider.ExitStatus, error) {
	buf := make([]byte, 256)
	for {
		n, err := t.Stdin.Read(buf)
		if err != nil {
			return provider.ExitStatus{}, ctx.Err()
		}
		if _, err := t.Stdout.Write(buf[:n]); err != nil {
			return provider.ExitStatus{}, err
		}
	}
}

type execSession struct {
	*fakeSession
	info     *core.ExecInfo // nil: one pod with one container
	mu       sync.Mutex
	handles  []*echoHandle // prepared (the API's prototypes)
	runs     []*echoHandle // copies that ran or were registered
	prepared []provider.ExecRequest
}

func (e *execSession) ExecInfo(context.Context, core.Ref) (core.ExecInfo, error) {
	if e.info != nil {
		return *e.info, nil
	}
	return core.ExecInfo{Instances: []core.ExecInstance{{ID: "uid", Title: "p", Ready: true,
		Channels: []core.ExecChannel{{ID: "app", Title: "app", Running: true}}, DefaultChannel: "app"}}, DefaultInstance: "uid"}, nil
}

func (e *execSession) PrepareExec(_ context.Context, _ core.Ref, req provider.ExecRequest) (provider.ExecHandle, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	h := &echoHandle{owner: e, sess: e.fakeSession}
	e.handles = append(e.handles, h)
	e.prepared = append(e.prepared, req)
	return h, nil
}

type execOpenable struct {
	*openable
	mu       sync.Mutex
	sessions []*execSession
}

func (o *execOpenable) Open(ctx context.Context, target string) (provider.Session, error) {
	s, _ := o.openable.Open(ctx, target)
	es := &execSession{fakeSession: s.(*fakeSession)}
	o.mu.Lock()
	o.sessions = append(o.sessions, es)
	o.mu.Unlock()
	return es, nil
}

func newExecService(t *testing.T) (*Service, *execOpenable, string) {
	t.Helper()
	k := &execOpenable{openable: newOpenable("a", "b")}
	s, _ := newService(t, k)
	h := streams.NewHandler(s.Streams(), streams.HandlerOptions{AllowOrigin: "wails://localhost", Classify: StreamErrorClass})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return s, k, "ws" + strings.TrimPrefix(srv.URL, "http") + "/" + h.Token()
}

func dialTerm(t *testing.T, base, id string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, base+"/term/"+id, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"wails://localhost"}}})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

// echo sends text and reads until it comes back (skipping control frames).
func echo(t *testing.T, c *websocket.Conn, text string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, c.Write(ctx, websocket.MessageBinary, []byte(text)))
	var got string
	for got != text {
		typ, data, err := c.Read(ctx)
		require.NoError(t, err)
		if typ == websocket.MessageBinary {
			got += string(data)
		}
	}
}

var termReq = TerminalRequest{Ref: podRef, Channel: "app", Cols: 80, Rows: 24}

// A provider's nil lists go out as empty lists, never null.
func TestExecInfoListsAreNeverNull(t *testing.T) {
	s, k, _ := newExecService(t)
	ctx := context.Background()
	ref := core.Ref{Provider: "k", Target: "a", Kind: "pods", Name: "p"}
	_, err := s.ExecInfo(ctx, ref)
	require.NoError(t, err)
	es := k.sessions[0]
	es.info = &core.ExecInfo{Instances: []core.ExecInstance{{ID: "c1", Title: "c1"}}}
	info, err := s.ExecInfo(ctx, ref)
	require.NoError(t, err)
	b, _ := json.Marshal(info)
	assert.Contains(t, string(b), `"channels":[]`)
	es.info = &core.ExecInfo{}
	info, err = s.ExecInfo(ctx, ref)
	require.NoError(t, err)
	b, _ = json.Marshal(info)
	assert.Contains(t, string(b), `"instances":[]`)
}

func TestTerminalsOutliveTheirSession(t *testing.T) {
	ctx := context.Background()
	s, k, base := newExecService(t)
	now := time.Unix(1000, 0)
	s.now = func() time.Time { return now }

	live, err := s.OpenTerminal(ctx, termReq)
	require.NoError(t, err)
	revH1 := targetRev(t, s, "a")
	assert.Equal(t, revH1, live.Target.ConfigRev, "the target's revision when opened")
	assert.Equal(t, "a", live.Target.Target)
	pending, err := s.OpenTerminal(ctx, termReq)
	require.NoError(t, err)
	c := dialTerm(t, base, live.StreamID)
	echo(t, c, "before")

	// Another target is selected: the session of "a" closes...
	require.NoError(t, s.SelectTarget(ctx, "k", "b"))
	require.True(t, k.sessions[0].isClosed())
	// ...the terminal goes on, and a pending one can still connect.
	echo(t, c, "after switch")
	c2 := dialTerm(t, base, pending.StreamID)
	echo(t, c2, "late")

	// A configuration change re-creates sessions: terminals stay.
	k.setHash("a", "h2")
	s.revalidateSessions(ctx, "k")
	now = now.Add(sessionIdle + time.Second)
	s.reapIdleSessions()
	echo(t, c, "after reconfigure and reap")
	run := k.sessions[0].runs[0]
	assert.Equal(t, int32(0), run.closed.Load())

	_ = c.Close(websocket.StatusNormalClosure, "")
	require.Eventually(t, func() bool { return run.closed.Load() == 1 }, 5*time.Second, 10*time.Millisecond)

	// Reconnect runs the same prepared command again, from the old session's
	// snapshot (no new PrepareExec on today's session).
	again, err := s.ReopenTerminal(ctx, ReopenTerminalRequest{TerminalID: live.TerminalID, Cols: 100, Rows: 30})
	require.NoError(t, err)
	assert.Equal(t, live.TerminalID, again.TerminalID)
	assert.NotEqual(t, live.StreamID, again.StreamID)
	assert.Equal(t, "h1", again.Target.ConfigHash, "the snapshot it was opened with")
	assert.Equal(t, revH1, again.Target.ConfigRev)
	assert.NotEqual(t, targetRev(t, s, "a"), again.Target.ConfigRev, "the UI sees the target reconfigured since")
	b, _ := json.Marshal(again)
	assert.NotContains(t, string(b), `"h1"`, "hashes cover credentials: only revisions reach the UI")
	echo(t, dialTerm(t, base, again.StreamID), "reconnected")
	total := 0
	for _, es := range k.sessions {
		total += len(es.prepared)
	}
	assert.Equal(t, 2, total, "reconnecting prepares nothing new")

	require.NoError(t, s.ForgetTerminal(ctx, live.TerminalID))
	assert.Equal(t, int32(1), k.sessions[0].handles[0].closed.Load(), "a forgotten prototype is released")
	_, err = s.ReopenTerminal(ctx, ReopenTerminalRequest{TerminalID: live.TerminalID, Cols: 100, Rows: 30})
	assert.True(t, IsCoded(err, CodeGone), "%v", err)
}

// Forgetting a terminal (its tab closed) ends its runs: the connected one
// and one registered but not connected yet; other terminals go on.
func TestForgettingATerminalEndsItsRuns(t *testing.T) {
	ctx := context.Background()
	s, k, base := newExecService(t)
	a, err := s.OpenTerminal(ctx, termReq)
	require.NoError(t, err)
	b, err := s.OpenTerminal(ctx, termReq)
	require.NoError(t, err)
	ca, cb := dialTerm(t, base, a.StreamID), dialTerm(t, base, b.StreamID)
	echo(t, ca, "a")
	echo(t, cb, "b")
	pending, err := s.ReopenTerminal(ctx, ReopenTerminalRequest{TerminalID: a.TerminalID, Cols: 80, Rows: 24})
	require.NoError(t, err)
	require.Equal(t, 2, s.Streams().Owners()[termOwner(a.TerminalID)])

	require.NoError(t, s.ForgetTerminal(ctx, a.TerminalID))
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		if _, _, err := ca.Read(rctx); err != nil {
			assert.Equal(t, websocket.StatusGoingAway, websocket.CloseStatus(err), "%v", err) // revoked: "gone"
			break
		}
	}
	require.Eventually(t, func() bool { return s.Streams().Owners()[termOwner(a.TerminalID)] == 0 }, 5*time.Second, 10*time.Millisecond)
	es := k.sessions[0]
	require.Eventually(t, func() bool {
		es.mu.Lock()
		defer es.mu.Unlock()
		return es.runs[0].closed.Load() == 1 && es.runs[2].closed.Load() == 1
	}, 5*time.Second, 10*time.Millisecond, "the run and the pending one are released")
	dctx, dcancel := context.WithTimeout(ctx, 5*time.Second)
	defer dcancel()
	_, resp, err := websocket.Dial(dctx, base+"/term/"+pending.StreamID, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"wails://localhost"}}})
	require.Error(t, err, "the pending run can no longer connect")
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusGone, resp.StatusCode)
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
	echo(t, cb, "b goes on")
}

// A reconnect racing the tab's closing never leaves a run behind.
func TestAReopenRacingForgetLeavesNoRun(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newExecService(t)
	for range 200 {
		live, err := s.OpenTerminal(ctx, termReq)
		require.NoError(t, err)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = s.ReopenTerminal(ctx, ReopenTerminalRequest{TerminalID: live.TerminalID, Cols: 80, Rows: 24})
		}()
		go func() {
			defer wg.Done()
			_ = s.ForgetTerminal(ctx, live.TerminalID)
		}()
		wg.Wait()
		require.Zero(t, s.Streams().Owners()[termOwner(live.TerminalID)])
	}
}

func TestOpenTerminalValidatesBeforePreparing(t *testing.T) {
	ctx := context.Background()
	s, k, _ := newExecService(t)
	for name, req := range map[string]TerminalRequest{
		"no size":      {Ref: podRef},
		"too wide":     {Ref: podRef, Cols: 1001, Rows: 24},
		"empty argv0":  {Ref: podRef, Cols: 80, Rows: 24, Command: []string{"", "x"}},
		"huge command": {Ref: podRef, Cols: 80, Rows: 24, Command: []string{strings.Repeat("x", 17<<10)}},
	} {
		_, err := s.OpenTerminal(ctx, req)
		assert.True(t, IsCoded(err, CodeBadRequest), "%s: %v", name, err)
	}
	for _, es := range k.sessions {
		assert.Empty(t, es.prepared)
	}
	req := termReq
	req.Command = []string{"ls", "-la", ""}
	_, err := s.OpenTerminal(ctx, req)
	require.NoError(t, err, "empty later arguments are fine")
	assert.Equal(t, []string{"ls", "-la", ""}, k.sessions[0].prepared[0].Command)
}

func TestTerminalLimitReleasesTheRefusedHandle(t *testing.T) {
	ctx := context.Background()
	s, k, _ := newExecService(t)
	for range streams.MaxTerms {
		_, err := s.OpenTerminal(ctx, termReq)
		require.NoError(t, err)
	}
	_, err := s.OpenTerminal(ctx, termReq)
	assert.True(t, IsCoded(err, CodeLimit), "%v", err)
	es := k.sessions[0]
	assert.Equal(t, int32(1), es.runs[len(es.runs)-1].closed.Load(), "the refused run")
	assert.Equal(t, int32(1), es.handles[len(es.handles)-1].closed.Load(), "the refused prototype")

	s.Close() // the app exits: pending terminals and prototypes are released
	for _, h := range append(es.handles, es.runs...) {
		assert.Equal(t, int32(1), h.closed.Load())
	}
	assert.Equal(t, 0, s.Streams().Count(streams.KindTerm))
}

func TestTerminalsNeedAnExecer(t *testing.T) {
	s, _ := newService(t, newOpenable("a"))
	_, err := s.OpenTerminal(context.Background(), termReq)
	assert.True(t, IsCoded(err, CodeUnsupported), "%v", err)
	_, err = s.ExecInfo(context.Background(), podRef)
	assert.True(t, IsCoded(err, CodeUnsupported), "%v", err)
}

// targetRev is the ConfigRev ListTargets shows for target id of provider "k".
func targetRev(t *testing.T, s *Service, id string) string {
	t.Helper()
	v, err := s.ListTargets(context.Background())
	require.NoError(t, err)
	for _, g := range v.Groups {
		for _, tg := range g.Targets {
			if tg.Provider == "k" && tg.ID == id {
				require.NotEmpty(t, tg.ConfigRev)
				return tg.ConfigRev
			}
		}
	}
	t.Fatalf("no target %s", id)
	return ""
}

// lockProbe is a handle whose Close records whether the prototypes' lock
// was held (TryLock: the test cannot hang on it).
type lockProbe struct {
	p          *termProtos
	underLock  *atomic.Bool
	closedRuns *atomic.Int32
}

func (h *lockProbe) Describe() core.LiveTarget { return core.LiveTarget{} }
func (h *lockProbe) Run(context.Context, provider.Terminal) (provider.ExitStatus, error) {
	return provider.ExitStatus{}, nil
}
func (h *lockProbe) Again() (provider.ExecHandle, error) {
	return &lockProbe{p: h.p, underLock: h.underLock, closedRuns: h.closedRuns}, nil
}
func (h *lockProbe) Close() {
	if h.p.mu.TryLock() {
		h.p.mu.Unlock()
	} else {
		h.underLock.Store(true)
	}
	h.closedRuns.Add(1)
}

// A run the registry refuses (the terminal limit) is closed after the
// prototypes' lock is released: provider cleanup may block or call back.
func TestARefusedRunIsClosedOutsideThePrototypesLock(t *testing.T) {
	s := &Service{streams: streams.NewRegistry()}
	defer s.streams.Close()
	var underLock atomic.Bool
	var closed atomic.Int32
	id := s.terms.add(&lockProbe{p: &s.terms, underLock: &underLock, closedRuns: &closed})
	for range streams.MaxTerms {
		_, err := s.streams.AddTerm("other", &lockProbe{p: &s.terms, underLock: &underLock, closedRuns: &closed}, provider.TermSize{Cols: 80, Rows: 24})
		require.NoError(t, err)
	}
	_, err := s.startTerminal(id, 80, 24)
	assert.True(t, IsCoded(err, CodeLimit), "%v", err)
	assert.EqualValues(t, 1, closed.Load(), "the refused run is released")
	assert.False(t, underLock.Load(), "and not under the prototypes' lock")
}
