package provider

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type viewRecorder struct {
	mu       sync.Mutex
	rows     []LogPreviewLine
	previews int
	ready    bool
	states   []LogState
}

func (r *viewRecorder) Source(int, string, string, string) error { return nil }
func (r *viewRecorder) State(_ int, s LogState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states = append(r.states, s)
	return nil
}
func (r *viewRecorder) Lines(id int, lines []LogLine) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, line := range lines {
		r.rows = append(r.rows, LogPreviewLine{id, line})
	}
	return nil
}
func (r *viewRecorder) Ready() error { r.mu.Lock(); defer r.mu.Unlock(); r.ready = true; return nil }
func (r *viewRecorder) Preview(rows []LogPreviewLine, _ bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = append([]LogPreviewLine(nil), rows...)
	r.previews++
	return nil
}
func (r *viewRecorder) texts() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, row := range r.rows {
		out = append(out, row.Line.Text)
	}
	return out
}

func TestLogViewCapturesLiveDuringHistoryAndReplacesOverlap(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	historyGate := make(chan struct{})
	historyEntered := make(chan struct{})
	liveCaptured := make(chan struct{})
	finishLive := make(chan struct{})
	r := &viewRecorder{}
	done := make(chan error, 1)
	old := LogLine{TS: "2026-10-19T00:00:00Z", Text: "ERROR old"}
	overlap := LogLine{TS: "2026-10-19T00:00:01Z", Text: "ERROR overlap"}
	fresh := LogLine{TS: "2026-10-19T00:00:02Z", Text: "ERROR live during history"}
	run := func(ctx context.Context, q LogQuery, s LogSink) error {
		if q.LiveOnly {
			if err := s.Source(1, "uid/app", "pod/app", "app"); err != nil {
				return err
			}
			if err := s.Ready(); err != nil {
				return err
			}
			select {
			case <-historyEntered:
			case <-ctx.Done():
				return ctx.Err()
			}
			if err := s.Lines(1, []LogLine{overlap, fresh}); err != nil {
				return err
			}
			close(liveCaptured)
			select {
			case <-finishLive:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if err := s.Source(9, "uid/app", "pod/app", "app"); err != nil {
			return err
		}
		close(historyEntered)
		if err := s.(LogPreviewSink).Preview([]LogPreviewLine{{9, old}}, false); err != nil {
			return err
		}
		select {
		case <-historyGate:
		case <-ctx.Done():
			return ctx.Err()
		}
		if err := s.(LogPreviewSink).Preview([]LogPreviewLine{{9, old}, {9, overlap}}, true); err != nil {
			return err
		}
		return s.Ready()
	}
	go func() {
		done <- StreamFilteredLogView(ctx, LogQuery{Follow: true, TailLines: 500, Filter: "error"}, run, r)
	}()
	select {
	case <-liveCaptured:
	case <-time.After(3 * time.Second):
		t.Fatal("live capture did not proceed during blocked history")
	}
	require.Eventually(t, func() bool {
		for _, text := range r.texts() {
			if text == fresh.Text {
				return true
			}
		}
		return false
	}, 3*time.Second, time.Millisecond)
	r.mu.Lock()
	require.False(t, r.ready)
	r.mu.Unlock()
	close(historyGate)
	require.Eventually(t, func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.ready }, 3*time.Second, time.Millisecond)
	require.Equal(t, []string{"ERROR old", "ERROR overlap", "ERROR live during history"}, r.texts())
	close(finishLive)
	require.NoError(t, <-done)
}

func TestLogViewCancellationJoinsHistoryAndLive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	liveClosed := make(chan struct{})
	done := make(chan error, 1)
	run := func(ctx context.Context, q LogQuery, s LogSink) error {
		if q.LiveOnly {
			defer close(liveClosed)
			if err := s.Ready(); err != nil {
				return err
			}
		} else {
			close(entered)
		}
		<-ctx.Done()
		return ctx.Err()
	}
	go func() {
		done <- StreamFilteredLogView(ctx, LogQuery{Follow: true, TailLines: 500, Filter: "error"}, run, &viewRecorder{})
	}()
	<-entered
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	select {
	case <-liveClosed:
	default:
		t.Fatal("live capture leaked after cancellation")
	}
}

func TestLogViewEndedLiveStillLoadsHistory(t *testing.T) {
	r := &viewRecorder{}
	history := false
	run := func(_ context.Context, q LogQuery, s LogSink) error {
		if q.LiveOnly {
			return nil
		}
		history = true
		require.NoError(t, s.Source(1, "x", "x", "app"))
		require.NoError(t, s.Lines(1, []LogLine{{Text: "ERROR retained"}}))
		return s.Ready()
	}
	require.NoError(t, StreamFilteredLogView(t.Context(), LogQuery{Follow: true, TailLines: 500, Filter: "error"}, run, r))
	require.True(t, history)
	require.Equal(t, []string{"ERROR retained"}, r.texts())
}

func TestLogViewKeepsNewRecordsArrivingBeforeCaptureReady(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &viewRecorder{}
	done := make(chan error, 1)
	run := func(ctx context.Context, q LogQuery, s LogSink) error {
		if q.LiveOnly {
			s = FilterLogSink(q, s)
			if err := s.Source(1, "uid", "app", "app"); err != nil {
				return err
			}
			if err := s.Lines(1, []LogLine{{TS: "2026-10-19T00:00:01Z", Text: "ERROR first new"}, {TS: "2026-10-19T00:00:02Z", Text: "ERROR second new"}}); err != nil {
				return err
			}
			if err := s.Ready(); err != nil {
				return err
			}
			<-ctx.Done()
			return ctx.Err()
		}
		if err := s.Source(9, "uid", "app", "app"); err != nil {
			return err
		}
		if err := s.Lines(9, []LogLine{{TS: "2026-10-19T00:00:00Z", Text: "ERROR history"}}); err != nil {
			return err
		}
		return s.Ready()
	}
	go func() {
		done <- StreamFilteredLogView(ctx, LogQuery{Follow: true, TailLines: 1, Filter: "error"}, run, r)
	}()
	require.Eventually(t, func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.ready }, 3*time.Second, time.Millisecond)
	require.Equal(t, []string{"ERROR history", "ERROR first new", "ERROR second new"}, r.texts())
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}

func TestLiveCaptureOverflowIsBoundedAndVisibleBeforeHistoryCompletes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &viewRecorder{}
	v := &logViewMerge{ctx: ctx, sink: r, liveIDs: map[int]int{1: 1}, duplicates: map[logRecordKey]int{}, closedSources: map[int]bool{}}
	defer v.stopPreview()
	input := &logViewInput{view: v, live: true}
	text := "ERROR " + strings.Repeat("x", 128<<10)
	lines := make([]LogLine, 140)
	for i := range lines {
		lines[i] = LogLine{TS: fmt.Sprint(i), Text: text}
	}
	require.NoError(t, input.Lines(1, lines))
	v.mu.Lock()
	require.LessOrEqual(t, v.liveBytes, 16<<20)
	require.True(t, v.liveCut)
	v.mu.Unlock()
	r.mu.Lock()
	require.False(t, r.ready)
	require.NotEmpty(t, r.states)
	require.Equal(t, LogGap, r.states[0].State)
	r.mu.Unlock()
	require.NoError(t, v.finish())
}

func TestLiveReadPacerWaitCanBeCancelledWithoutReadingAnUnboundedBlock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := NewLogReadPacer(1)
	raw := strings.NewReader(strings.Repeat("x", 1<<20))
	entered := make(chan struct{})
	body := p.Wrap(ctx, &readStartedBody{ReadCloser: io.NopCloser(raw), entered: entered})
	p.mu.Lock()
	p.next = time.Now().Add(time.Hour)
	p.mu.Unlock()
	done := make(chan error, 1)
	go func() { _, err := body.Read(make([]byte, 1<<20)); done <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("paced read did not begin")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("paced wait did not cancel")
	}
	require.Equal(t, 32<<10, (1<<20)-raw.Len())
}

func TestLiveReadPacerActuallyLimitsSteadyRawDecoding(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	body := NewLogReadPacer(1<<20).Wrap(ctx, io.NopCloser(strings.NewReader(strings.Repeat("x", 512<<10))))
	started := time.Now()
	n, err := io.Copy(io.Discard, body)
	require.NoError(t, err)
	require.Equal(t, int64(512<<10), n)
	require.GreaterOrEqual(t, time.Since(started), 400*time.Millisecond)
}

func TestUntimedHistoryLiveOverlapIsNotSilentlyDropped(t *testing.T) {
	r := &viewRecorder{}
	v := &logViewMerge{ctx: t.Context(), sink: r, history: []LogPreviewLine{{1, LogLine{Text: "same text"}}}, live: []LogPreviewLine{{1, LogLine{Text: "same text"}}}}
	require.NoError(t, v.finish())
	require.Equal(t, []string{"same text", "same text"}, r.texts())
	r.mu.Lock()
	defer r.mu.Unlock()
	require.NotEmpty(t, r.states)
	require.Equal(t, LogGap, r.states[0].State)
}

type readStartedBody struct {
	io.ReadCloser
	entered chan struct{}
	once    sync.Once
}

func (r *readStartedBody) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	r.once.Do(func() { close(r.entered) })
	return n, err
}
