package api

import (
	"bufio"
	"context"
	"encoding/json"
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

type historySession struct {
	*fakeSession
	query        chan provider.LogQuery
	refs         chan core.Ref
	wait         bool
	cancelled    chan struct{}
	readComplete bool
	mu           sync.Mutex
}

func (s *historySession) LogInfo(context.Context, core.Ref) (core.LogInfo, error) {
	return core.LogInfo{}, nil
}
func (s *historySession) StreamLogs(ctx context.Context, ref core.Ref, q provider.LogQuery, sink provider.LogSink) error {
	s.query <- q
	s.refs <- ref
	if err := sink.Source(1, "uid/app", "pod/app", "app"); err != nil {
		return err
	}
	if s.wait {
		<-ctx.Done()
		close(s.cancelled)
		return ctx.Err()
	}
	rows := make([]provider.LogLine, 2000)
	for i := range rows {
		rows[i] = provider.LogLine{Text: fmt.Sprintf("history %d", i)}
	}
	if !q.Archive && q.TailLines > 0 {
		rows = rows[len(rows)-q.TailLines:]
	}
	if err := sink.Lines(1, rows); err != nil {
		return err
	}
	return sink.Ready()
}

type historyOpenable struct {
	*openable
	query     chan provider.LogQuery
	refs      chan core.Ref
	wait      bool
	cancelled chan struct{}
}

func (o *historyOpenable) Open(ctx context.Context, target string) (provider.Session, error) {
	base, _ := o.openable.Open(ctx, target)
	return &historySession{fakeSession: base.(*fakeSession), query: o.query, refs: o.refs, wait: o.wait, cancelled: o.cancelled}, nil
}
func historyService(t *testing.T, wait bool) (*Service, *historyOpenable, *httptest.Server, *streams.Handler) {
	t.Helper()
	o := &historyOpenable{openable: newOpenable("a", "b"), query: make(chan provider.LogQuery, 4), refs: make(chan core.Ref, 4), wait: wait, cancelled: make(chan struct{})}
	s, _ := newService(t, o)
	h := streams.NewHandler(s.Streams(), streams.HandlerOptions{Classify: StreamErrorClass})
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	_, err := s.ConnectTarget(UIContext(t.Context()), "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	return s, o, server, h
}
func TestLogHistoryReadsAllRetainedRowsAsFiniteArchive(t *testing.T) {
	s, o, server, h := historyService(t, false)
	opened, err := s.OpenLogHistory(UIContext(t.Context()), LogHistoryRequest{Ref: podRef, Channel: "chosen", Previous: true})
	require.NoError(t, err)
	response, err := http.Get(server.URL + "/" + h.Token() + "/logs/" + opened.StreamID)
	require.NoError(t, err)
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	lines := 0
	end := ""
	for scanner.Scan() {
		var f struct {
			K      string  `json:"k"`
			L      [][]any `json:"l"`
			Reason string  `json:"reason"`
		}
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &f))
		lines += len(f.L)
		if f.K == "end" {
			end = f.Reason
		}
	}
	require.NoError(t, scanner.Err())
	require.Equal(t, 2000, lines)
	require.Equal(t, "done", end)
	q := <-o.query
	require.True(t, q.Archive)
	require.False(t, q.Follow)
	require.Equal(t, provider.TailAll, q.TailLines)
	require.Equal(t, "chosen", q.Channel)
	require.True(t, q.Previous)
	require.True(t, q.SinceTime.IsZero())
	require.Equal(t, podRef, <-o.refs)
}
func TestLogHistoryRejectsUnpinnedOrDisconnectedRequests(t *testing.T) {
	s, _, _, _ := historyService(t, false)
	r := podRef
	r.UID = ""
	_, err := s.OpenLogHistory(UIContext(t.Context()), LogHistoryRequest{Ref: r})
	require.True(t, IsCoded(err, CodeBadRequest))
	r = podRef
	r.Target = "b"
	_, err = s.OpenLogHistory(UIContext(t.Context()), LogHistoryRequest{Ref: r})
	require.True(t, IsCoded(err, CodeGone))
}
func TestLogHistoryStopsWhenItsSessionCloses(t *testing.T) {
	s, o, server, h := historyService(t, true)
	opened, err := s.OpenLogHistory(UIContext(t.Context()), LogHistoryRequest{Ref: podRef})
	require.NoError(t, err)
	response, err := http.Get(server.URL + "/" + h.Token() + "/logs/" + opened.StreamID)
	require.NoError(t, err)
	defer response.Body.Close()
	<-o.query
	require.NoError(t, s.SelectTarget(UIContext(t.Context()), "k", "b"))
	require.NoError(t, s.CloseTarget(UIContext(t.Context()), "k", "a"))
	<-o.cancelled
	scanner := bufio.NewScanner(response.Body)
	last := ""
	for scanner.Scan() {
		last = scanner.Text()
	}
	require.NoError(t, scanner.Err())
	require.Contains(t, last, `"reason":"gone"`)
}

type historyRecorder struct {
	lines    int
	maxBatch int
}

func (*historyRecorder) Source(int, string, string, string) error { return nil }
func (r *historyRecorder) Lines(_ int, lines []provider.LogLine) error {
	r.lines += len(lines)
	r.maxBatch = max(r.maxBatch, len(lines))
	return nil
}
func (*historyRecorder) State(int, provider.LogState) error { return nil }
func (*historyRecorder) Ready() error                       { return nil }
func TestLogHistorySinkBudgetsWarningsAndConcurrentSources(t *testing.T) {
	newSink := func(maxBytes int64, maxLines int) (*historyLogSink, *historyRecorder) {
		r := &historyRecorder{}
		return &historyLogSink{ctx: t.Context(), out: r, labels: map[int]string{}, maxBytes: maxBytes, maxLines: maxLines}, r
	}
	for _, test := range []struct {
		name  string
		bytes int64
		lines int
		flag  int
	}{{"bytes", 8, 100, 0}, {"lines", 1024, 1, 0}, {"cut", 1024, 100, provider.LineCut}} {
		t.Run(test.name, func(t *testing.T) {
			s, r := newSink(test.bytes, test.lines)
			require.NoError(t, s.Source(1, "uid", "pod", "app"))
			err := s.Lines(1, []provider.LogLine{{Text: "first", Flags: test.flag}, {Text: "second"}})
			require.Error(t, err)
			require.Zero(t, r.lines)
		})
	}
	for _, state := range []string{provider.LogGap, provider.LogTruncated, provider.LogLimited, provider.LogError, provider.LogWaiting, "unknown"} {
		s, _ := newSink(1024, 100)
		require.Error(t, s.State(1, provider.LogState{State: state}))
	}
	s0, _ := newSink(1024, 100)
	require.Error(t, s0.State(1, provider.LogState{State: provider.LogEnded, Class: provider.ClassNotFound, Message: "pod deleted"}))
	require.NoError(t, s0.State(1, provider.LogState{State: provider.LogEnded}))
	s, r := newSink(1<<20, 1000)
	var group sync.WaitGroup
	for id := 1; id <= 20; id++ {
		group.Go(func() {
			require.NoError(t, s.Source(id, fmt.Sprint(id), "pod", "app"))
			require.NoError(t, s.Lines(id, []provider.LogLine{{Text: "one"}, {Text: "two"}}))
		})
	}
	group.Wait()
	require.Equal(t, 40, r.lines)
	require.NoError(t, s.Lines(1, make([]provider.LogLine, 600)))
	require.LessOrEqual(t, r.maxBatch, 500)
}

type replacedHistorySession struct{ *historySession }

func (s *replacedHistorySession) LogInfo(ctx context.Context, ref core.Ref) (core.LogInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readComplete {
		return core.LogInfo{}, &provider.Error{Class: provider.ClassNotFound, Message: "UID was replaced"}
	}
	return s.historySession.LogInfo(ctx, ref)
}
func (s *replacedHistorySession) StreamLogs(ctx context.Context, ref core.Ref, q provider.LogQuery, sink provider.LogSink) error {
	err := s.historySession.StreamLogs(ctx, ref, q, sink)
	s.mu.Lock()
	s.readComplete = true
	s.mu.Unlock()
	return err
}

type replacedHistoryOpenable struct{ *historyOpenable }

func (o *replacedHistoryOpenable) Open(ctx context.Context, target string) (provider.Session, error) {
	s, err := o.historyOpenable.Open(ctx, target)
	if err != nil {
		return nil, err
	}
	return &replacedHistorySession{historySession: s.(*historySession)}, nil
}
func TestLogHistoryDoesNotCompleteWhenTheUIDChangesDuringTheRead(t *testing.T) {
	base := &historyOpenable{openable: newOpenable("a"), query: make(chan provider.LogQuery, 2), refs: make(chan core.Ref, 2), cancelled: make(chan struct{})}
	s, _ := newService(t, &replacedHistoryOpenable{base})
	_, err := s.ConnectTarget(UIContext(t.Context()), "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	h := streams.NewHandler(s.Streams(), streams.HandlerOptions{Classify: StreamErrorClass})
	server := httptest.NewServer(h)
	defer server.Close()
	opened, err := s.OpenLogHistory(UIContext(t.Context()), LogHistoryRequest{Ref: podRef})
	require.NoError(t, err)
	response, err := http.Get(server.URL + "/" + h.Token() + "/logs/" + opened.StreamID)
	require.NoError(t, err)
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	last := ""
	for scanner.Scan() {
		last = scanner.Text()
	}
	require.NoError(t, scanner.Err())
	require.Contains(t, last, `"reason":"error"`)
	require.Contains(t, last, "UID was replaced")
}
