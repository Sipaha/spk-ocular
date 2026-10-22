package api

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/streams"
	"github.com/stretchr/testify/require"
)

type compositionSession struct {
	*fakeSession
	mu               sync.Mutex
	calls, reads     int
	refs             []core.Ref
	query            provider.LogQuery
	partial, replace bool
}

func (s *compositionSession) CheckComposedLogRoots(_ context.Context, refs []core.Ref) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads += len(refs)
	if s.replace && s.calls > 0 {
		return &provider.Error{Class: provider.ClassGone, Message: "root replaced"}
	}
	return nil
}
func (s *compositionSession) Get(context.Context, core.Ref) (*core.Resource, error) {
	return nil, fmt.Errorf("unexpected inspector read")
}
func (s *compositionSession) StreamComposedLogs(_ context.Context, refs []core.Ref, q provider.LogQuery, sink provider.LogSink) error {
	s.mu.Lock()
	s.calls++
	s.refs = append([]core.Ref(nil), refs...)
	s.query = q
	partial := s.partial
	s.mu.Unlock()
	for i, r := range refs {
		if err := sink.Source(i+1, "root/"+r.UID, r.Name, ""); err != nil {
			return err
		}
		if err := sink.State(i+1, provider.LogState{State: provider.LogStreaming}); err != nil {
			return err
		}
	}
	if err := sink.Source(3, "ns/uid/app", "ns/pod/app", "app"); err != nil {
		return err
	}
	if err := sink.Lines(3, []provider.LogLine{{Text: "retained"}}); err != nil {
		return err
	}
	if partial {
		return sink.State(1, provider.LogState{State: provider.LogError, Class: provider.ClassForbidden, Message: "denied"})
	}
	return sink.Ready()
}

type compositionOpenable struct {
	*openable
	sess *compositionSession
}

func (o *compositionOpenable) Open(ctx context.Context, target string) (provider.Session, error) {
	base, err := o.openable.Open(ctx, target)
	if err != nil {
		return nil, err
	}
	o.sess = &compositionSession{fakeSession: base.(*fakeSession)}
	return o.sess, nil
}
func compositionService(t *testing.T) (*Service, *compositionOpenable, LogComposition) {
	o := &compositionOpenable{openable: newOpenable("a")}
	o.id = "kubernetes"
	s, _ := newService(t, o)
	ctx := UIContext(t.Context())
	_, err := s.ListTargets(ctx)
	require.NoError(t, err)
	_, err = s.ConnectTarget(ctx, "kubernetes", "a")
	require.NoError(t, err)
	conn := awaitConnection(t, s, "connected")
	view, err := s.ListTargets(ctx)
	require.NoError(t, err)
	return s, o, LogComposition{ConfigRev: view.Groups[0].Targets[0].ConfigRev, ConnectionID: conn.ID, Refs: []core.Ref{{Provider: "kubernetes", Target: "a", Kind: "pods", Scope: "ns", Name: "one", UID: "one"}, {Provider: "kubernetes", Target: "a", Kind: "apps/deployments", Scope: "ns", Name: "two", UID: "two"}}}
}
func TestComposedLogsAdmissionIsUIOnlyAndBoundToExactConnection(t *testing.T) {
	s, o, c := compositionService(t)
	q := provider.LogQuery{Channel: provider.ChannelAll, TailLines: 500, Follow: true}
	for _, history := range []bool{false, true} {
		open := func(ctx context.Context, c LogComposition) error {
			if history {
				_, e := s.OpenComposedLogHistory(ctx, c)
				return e
			}
			_, e := s.OpenComposedLogStream(ctx, ComposedLogStreamRequest{Composition: c, Query: q})
			return e
		}
		require.True(t, IsCoded(open(t.Context(), c), "forbidden"))
		stale := c
		stale.ConnectionID++
		require.True(t, IsCoded(open(UIContext(t.Context()), stale), CodeConflict))
		stale = c
		stale.ConfigRev = "new"
		require.True(t, IsCoded(open(UIContext(t.Context()), stale), CodeConflict))
		require.Zero(t, s.Streams().Len())
	}
	require.Zero(t, o.sess.calls)
	opened, err := s.OpenComposedLogStream(UIContext(t.Context()), ComposedLogStreamRequest{Composition: c, Query: q})
	require.NoError(t, err)
	require.Equal(t, 1, s.Streams().Len())
	require.Zero(t, o.sess.calls, "registration is lazy")
	o.setHash("a", "h2")
	s.revalidateSessions(t.Context(), "kubernetes")
	require.Zero(t, s.Streams().Len(), "session replacement invalidates unused composed IDs")
	require.NotEmpty(t, opened.StreamID)
}
func TestComposedLogsRejectMalformedSetsBeforeRegistration(t *testing.T) {
	s, _, c := compositionService(t)
	variants := []LogComposition{{}, c, c, c, c}
	variants[1].Refs = append([]core.Ref(nil), c.Refs[:1]...)
	variants[2].Refs = []core.Ref{c.Refs[0], c.Refs[0]}
	variants[3].Refs = append([]core.Ref(nil), c.Refs...)
	variants[3].Refs[1].Target = "other"
	variants[4].Refs = append([]core.Ref(nil), c.Refs...)
	variants[4].Refs[1].Kind = "services"
	for i, v := range variants {
		_, err := s.OpenComposedLogStream(UIContext(t.Context()), ComposedLogStreamRequest{Composition: v, Query: provider.LogQuery{Channel: "*", TailLines: 100}})
		require.True(t, IsCoded(err, CodeBadRequest), fmt.Sprint(i, err))
	}
	require.Zero(t, s.Streams().Len())
}
func TestComposedHistoryUsesOneFiniteArchiveAndRejectsPartialOrReplacedRoots(t *testing.T) {
	for _, mode := range []string{"complete", "partial", "replaced"} {
		t.Run(mode, func(t *testing.T) {
			s, o, c := compositionService(t)
			o.sess.partial = mode == "partial"
			o.sess.replace = mode == "replaced"
			h := streams.NewHandler(s.Streams(), streams.HandlerOptions{Classify: StreamErrorClass})
			srv := httptest.NewServer(h)
			defer srv.Close()
			opened, err := s.OpenComposedLogHistory(UIContext(t.Context()), c)
			require.NoError(t, err)
			response, err := http.Get(srv.URL + "/" + h.Token() + "/logs/" + opened.StreamID)
			require.NoError(t, err)
			defer response.Body.Close()
			// Reuse the NDJSON decoder helper for the terminal completion frame.
			sc := bufio.NewScanner(response.Body)
			end := lastFrame(t, sc)
			if mode == "complete" {
				require.Equal(t, "done", end["reason"])
			} else {
				require.NotEqual(t, "done", end["reason"])
			}
			o.sess.mu.Lock()
			defer o.sess.mu.Unlock()
			require.Equal(t, 1, o.sess.calls)
			require.Equal(t, c.Refs, o.sess.refs)
			require.True(t, o.sess.query.Archive)
			require.False(t, o.sess.query.Follow)
			require.Equal(t, "*", o.sess.query.Channel)
			if mode == "complete" {
				require.Equal(t, 4, o.sess.reads)
			}
		})
	}
}

func TestComposedStreamCopiesWholeSetAndRunsOneLazyProviderUnion(t *testing.T) {
	s, o, c := compositionService(t)
	h := streams.NewHandler(s.Streams(), streams.HandlerOptions{Classify: StreamErrorClass})
	server := httptest.NewServer(h)
	defer server.Close()
	opened, err := s.OpenComposedLogStream(UIContext(t.Context()), ComposedLogStreamRequest{Composition: c, Query: provider.LogQuery{Channel: "*", TailLines: 500}})
	require.NoError(t, err)
	c.Refs[0].Name = "changed-after-registration"
	response, err := http.Get(server.URL + "/" + h.Token() + "/logs/" + opened.StreamID)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, "done", lastFrame(t, bufio.NewScanner(response.Body))["reason"])
	o.sess.mu.Lock()
	defer o.sess.mu.Unlock()
	require.Equal(t, 1, o.sess.calls)
	require.Equal(t, "one", o.sess.refs[0].Name)
	require.Len(t, o.sess.refs, 2)
	second, err := http.Get(server.URL + "/" + h.Token() + "/logs/" + opened.StreamID)
	require.NoError(t, err)
	defer second.Body.Close()
	require.NotEqual(t, http.StatusOK, second.StatusCode)
}
