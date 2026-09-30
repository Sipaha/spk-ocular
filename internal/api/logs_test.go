package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/streams"
)

// logSession streams until its context ends.
type logSession struct {
	*fakeSession
	started chan provider.LogQuery
}

func (l *logSession) LogInfo(context.Context, core.Ref) (core.LogInfo, error) {
	return core.LogInfo{Channels: []core.LogChannel{{ID: "app", Title: "app"}}, DefaultChannel: "app", Previous: true}, nil
}

func (l *logSession) StreamLogs(ctx context.Context, _ core.Ref, q provider.LogQuery, sink provider.LogSink) error {
	if err := sink.Source(1, "uid/app", "p/app", "app"); err != nil {
		return err
	}
	if err := sink.Ready(); err != nil {
		return err
	}
	l.started <- q
	<-ctx.Done()
	return ctx.Err()
}

type logOpenable struct {
	*openable
	started chan provider.LogQuery
}

func (o *logOpenable) Open(ctx context.Context, target string) (provider.Session, error) {
	s, _ := o.openable.Open(ctx, target)
	return &logSession{fakeSession: s.(*fakeSession), started: o.started}, nil
}

func newLogService(t *testing.T) (*Service, *logOpenable, *httptest.Server, *streams.Handler) {
	t.Helper()
	k := &logOpenable{openable: newOpenable("a", "b", "c", "d"), started: make(chan provider.LogQuery, 4)}
	s, _ := newService(t, k)
	h := streams.NewHandler(s.Streams(), streams.HandlerOptions{Classify: StreamErrorClass})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	s.SetStreamBase(func() (string, error) { return srv.URL + "/" + h.Token(), nil })
	return s, k, srv, h
}

var podRef = core.Ref{Provider: "k", Target: "a", Scope: "ns", Kind: "pods", Name: "p", UID: "uid"}

func connect(t *testing.T, s *Service, id string) *bufio.Scanner {
	t.Helper()
	base, err := s.StreamBase(context.Background())
	require.NoError(t, err)
	resp, err := http.Get(base + "/logs/" + id)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return bufio.NewScanner(resp.Body)
}

func lastFrame(t *testing.T, sc *bufio.Scanner) map[string]any {
	t.Helper()
	var m map[string]any
	for sc.Scan() {
		m = nil
		require.NoError(t, json.Unmarshal(sc.Bytes(), &m))
	}
	return m
}

func TestOpenLogStreamValidates(t *testing.T) {
	ctx := context.Background()
	s, _, _, _ := newLogService(t)
	for name, q := range map[string]provider.LogQuery{
		"tail 0 (kube: no lines)": {TailLines: 0},
		"tail below -1":           {TailLines: -2},
		"previous + follow":       {TailLines: 10, Previous: true, Follow: true},
	} {
		_, err := s.OpenLogStream(ctx, LogStreamRequest{Ref: podRef, Query: q})
		assert.True(t, IsCoded(err, CodeBadRequest), name)
	}
	info, err := s.LogInfo(ctx, podRef)
	require.NoError(t, err)
	assert.Equal(t, "app", info.DefaultChannel)

	plain, _ := newService(t, newOpenable("a"))
	_, err = plain.LogInfo(ctx, podRef)
	assert.True(t, IsCoded(err, CodeUnsupported), "a provider without LogSource")
	_, err = plain.StreamBase(ctx)
	assert.True(t, IsCoded(err, CodeUnsupported), "no stream server in this mode")
}

func TestSessionClosingEndsItsLogStreamsGone(t *testing.T) {
	ctx := context.Background()
	s, k, _, _ := newLogService(t)
	info, err := s.OpenLogStream(ctx, LogStreamRequest{Ref: podRef, Query: provider.LogQuery{TailLines: 100, Follow: true}})
	require.NoError(t, err)
	sc := connect(t, s, info.StreamID)
	q := <-k.started
	assert.Equal(t, provider.LogQuery{TailLines: 100, Follow: true}, q)

	k.setHash("a", "h2") // the kubeconfig changed under it: the session goes
	s.revalidateSessions(ctx, "k")
	assert.Equal(t, map[string]any{"k": "end", "reason": "gone"}, lastFrame(t, sc))
	require.Eventually(t, func() bool { return s.Streams().Len() == 0 }, 5*time.Second, 10*time.Millisecond)
}

// A log tab of another target keeps going: switching never closes a
// session with a stream, however many targets were left since.
func TestSwitchingKeepsALogTabOfAnotherTarget(t *testing.T) {
	ctx := context.Background()
	s, k, _, _ := newLogService(t)
	info, err := s.OpenLogStream(ctx, LogStreamRequest{Ref: podRef, Query: provider.LogQuery{TailLines: 100, Follow: true}})
	require.NoError(t, err)
	connect(t, s, info.StreamID)
	<-k.started
	for _, next := range []string{"b", "c", "d", "b", "c"} {
		require.NoError(t, s.SelectTarget(ctx, "k", next))
	}
	assert.False(t, k.opened[0].isClosed(), "a was left long ago, but its log tab is open")
	assert.Equal(t, 1, s.Streams().Len())
}

func TestSessionWithOnlyALogTabIsNotReaped(t *testing.T) {
	ctx := context.Background()
	s, k, _, _ := newLogService(t)
	now := time.Unix(1000, 0)
	s.now = func() time.Time { return now }
	info, err := s.OpenLogStream(ctx, LogStreamRequest{Ref: podRef, Query: provider.LogQuery{TailLines: 100, Follow: true}})
	require.NoError(t, err)
	sc := connect(t, s, info.StreamID)
	<-k.started

	now = now.Add(3 * sessionIdle)
	s.reapIdleSessions()
	assert.False(t, k.opened[0].isClosed(), "a streaming log tab keeps its session")

	s.Close()
	assert.Equal(t, "end", lastFrame(t, sc)["k"])
}

func TestLogStreamOfAClosedIncarnationIsGone(t *testing.T) {
	ctx := context.Background()
	s, _, _, _ := newLogService(t)
	old, _, err := s.logSource(ctx, podRef)
	require.NoError(t, err)
	s.sessMu.Lock()
	s.closeSessionLocked(ownerKey("k", "a")) // closed between lookup and registration
	s.sessMu.Unlock()
	_, err = s.registerStream(old, func(context.Context, *streams.Writer) error { return nil })
	assert.True(t, IsCoded(err, CodeGone))
	assert.Equal(t, 0, s.Streams().Len())

	_, err = s.OpenLogStream(ctx, LogStreamRequest{Ref: podRef, Query: provider.LogQuery{TailLines: 1}})
	require.NoError(t, err, "a new incarnation serves a new request")
}

func TestLogStreamLimitIsCoded(t *testing.T) {
	ctx := context.Background()
	s, _, _, _ := newLogService(t)
	for range streams.MaxStreams {
		_, err := s.OpenLogStream(ctx, LogStreamRequest{Ref: podRef, Query: provider.LogQuery{TailLines: 1}})
		require.NoError(t, err)
	}
	_, err := s.OpenLogStream(ctx, LogStreamRequest{Ref: podRef, Query: provider.LogQuery{TailLines: 1}})
	assert.True(t, IsCoded(err, CodeLimit))
}
