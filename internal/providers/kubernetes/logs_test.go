package kubernetes

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/provider"
)

var t0 = time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)

func at(sec float64) time.Time { return t0.Add(time.Duration(sec * float64(time.Second))) }

func recs(from, n int, sec func(i int) time.Time) []fakeRec {
	out := make([]fakeRec, n)
	for i := range n {
		out[i] = fakeRec{ts: sec(from + i), text: fmt.Sprintf("line %d", from+i)}
	}
	return out
}

func lineNames(from, to int) []string {
	var out []string
	for i := from; i < to; i++ {
		out = append(out, fmt.Sprintf("line %d", i))
	}
	return out
}

func runningObs(uid, id string) podObs {
	return podObs{exists: true, uid: uid, phase: "Running", restartPolicy: "Always",
		ctrs: map[string]ctrObs{"app": {id: id, running: true, startedAt: t0}}}
}

func exited(o podObs, code int64) podObs {
	c := o.ctrs["app"]
	c.running, c.exited, c.exitCode = false, true, code
	o.ctrs = map[string]ctrObs{"app": c}
	return o
}

type sourceRun struct {
	sink   *logRecorder
	src    *podSource
	cancel context.CancelFunc
	done   chan error
}

func startSource(t *testing.T, k *fakeKubelet, box *obsBox, q provider.LogQuery) *sourceRun {
	t.Helper()
	sink := newLogRecorder()
	src := newPodSource(1, "ns", "p", "uid-1", "app", k.fetcher(t), box, sink, q)
	src.retryFirst, src.retryCap = 10*time.Millisecond, 40*time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	r := &sourceRun{sink: sink, src: src, cancel: cancel, done: make(chan error, 1)}
	go func() { r.done <- src.run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-r.done
	})
	return r
}

func (r *sourceRun) finished(t *testing.T) error {
	t.Helper()
	select {
	case err := <-r.done:
		r.done <- err // for Cleanup
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("the source did not finish")
		return nil
	}
}

func syncedBox(o podObs) *obsBox {
	b := newObsBox()
	b.set(o)
	b.setSynced()
	return b
}

var follow100 = provider.LogQuery{Follow: true, TailLines: 100}

func TestArchiveReadsBeyondTheUITailWithoutKeepingAResumeCursor(t *testing.T) {
	k := newFakeKubelet(t)
	k.start("p", "app", "c1")
	k.log("p", "app", recs(0, allTail+1, func(i int) time.Time { return at(float64(i)) })...)
	r := startSource(t, k, nil, provider.LogQuery{Archive: true, TailLines: provider.TailAll})
	require.NoError(t, r.finished(t))
	require.Len(t, r.sink.texts(), allTail+1)
	assert.Equal(t, "line 0", r.sink.texts()[0])
	assert.Empty(t, k.requestLog()[0].Get("tailLines"))
	assert.Nil(t, r.src.cur.ring, "finite archive reads do not accumulate restart history")
}

func TestLogTailThenFollow(t *testing.T) {
	k := newFakeKubelet(t)
	k.start("p", "app", "c1")
	k.log("p", "app", recs(0, 5, func(i int) time.Time { return at(float64(i)) })...)
	r := startSource(t, k, syncedBox(runningObs("uid-1", "c1")), provider.LogQuery{Follow: true, TailLines: 3})
	r.sink.await(t, "the tail", func() bool { return len(r.sink.texts()) == 3 })
	assert.Equal(t, lineNames(2, 5), r.sink.texts())
	k.log("p", "app", recs(5, 2, func(i int) time.Time { return at(float64(i)) })...)
	r.sink.await(t, "live lines", func() bool { return len(r.sink.texts()) == 5 })
	assert.Equal(t, lineNames(2, 7), r.sink.texts())
	assert.Equal(t, provider.LogStreaming, r.sink.lastState().State)
	q := k.requestLog()[0]
	assert.Equal(t, "true", q.Get("timestamps"))
	assert.Equal(t, "3", q.Get("tailLines"))
	assert.Equal(t, "true", q.Get("follow"))
}

// A dropped connection while the container runs: resume from the cursor,
// skip the replay by count — equal timestamps and identical texts included,
// nanoseconds inside one millisecond too — nothing lost, nothing repeated.
func TestLogResumeSkipsTheReplayExactly(t *testing.T) {
	k := newFakeKubelet(t)
	k.start("p", "app", "c1")
	same := at(1.5)
	k.log("p", "app",
		fakeRec{ts: at(0.2), text: "a"},
		fakeRec{ts: same, text: "dup"}, fakeRec{ts: same, text: "dup"}, fakeRec{ts: same, text: "dup"},
		fakeRec{ts: same.Add(1), text: "ns+1"}, fakeRec{ts: same.Add(2), text: "ns+2"},
	)
	r := startSource(t, k, syncedBox(runningObs("uid-1", "c1")), follow100)
	r.sink.await(t, "backlog", func() bool { return len(r.sink.texts()) == 6 })
	k.breakAll()
	k.log("p", "app", fakeRec{ts: same.Add(3), text: "dup"}, fakeRec{ts: at(2.1), text: "after"})
	r.sink.await(t, "lines after the reconnect", func() bool { return len(r.sink.texts()) == 8 })
	time.Sleep(50 * time.Millisecond) // nothing more may come
	assert.Equal(t, []string{"a", "dup", "dup", "dup", "ns+1", "ns+2", "dup", "after"}, r.sink.texts())
	assert.False(t, r.sink.hasState(provider.LogGap))
	reqs := k.requestLog()
	require.GreaterOrEqual(t, len(reqs), 2)
	assert.Equal(t, t0.Add(time.Second).Format(time.RFC3339), reqs[1].Get("sinceTime"), "second precision, floored")
	assert.False(t, reqs[1].Has("tailLines"), "a resume does not reapply the initial tail")
}

func TestLogResumeWithOutOfOrderTimestamps(t *testing.T) {
	k := newFakeKubelet(t)
	k.start("p", "app", "c1")
	k.log("p", "app", fakeRec{ts: at(5.5), text: "late clock"}, fakeRec{ts: at(5.1), text: "earlier clock"}, fakeRec{ts: at(4.9), text: "before the second"})
	r := startSource(t, k, syncedBox(runningObs("uid-1", "c1")), follow100)
	r.sink.await(t, "backlog", func() bool { return len(r.sink.texts()) == 3 })
	k.breakAll()
	k.log("p", "app", fakeRec{ts: at(5.2), text: "new"})
	r.sink.await(t, "the new line", func() bool { return len(r.sink.texts()) == 4 })
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, []string{"late clock", "earlier clock", "before the second", "new"}, r.sink.texts())
	assert.False(t, r.sink.hasState(provider.LogGap))
}

// Rotation while disconnected: the replay no longer starts with what was
// delivered — show everything from there and say so.
func TestLogRotationDuringDisconnectIsAGap(t *testing.T) {
	k := newFakeKubelet(t)
	k.start("p", "app", "c1")
	k.log("p", "app", recs(0, 4, func(i int) time.Time { return at(10 + float64(i)/10) })...)
	r := startSource(t, k, syncedBox(runningObs("uid-1", "c1")), follow100)
	r.sink.await(t, "backlog", func() bool { return len(r.sink.texts()) == 4 })
	k.breakAll()
	k.rotate("p", "app", 3) // line 0..2 gone
	k.log("p", "app", recs(4, 1, func(i int) time.Time { return at(10 + float64(i)/10) })...)
	r.sink.await(t, "a gap", func() bool { return r.sink.hasState(provider.LogGap) })
	r.sink.await(t, "the new line", func() bool {
		return strings.Join(r.sink.texts(), ",") != "" && r.sink.texts()[len(r.sink.texts())-1] == "line 4"
	})
}

func TestLogRestartAfterEOFWaitsThenReadsTheNewInstanceFromItsStart(t *testing.T) {
	k := newFakeKubelet(t)
	k.start("p", "app", "c1")
	k.log("p", "app", fakeRec{ts: at(1), text: "boot 1"})
	box := syncedBox(runningObs("uid-1", "c1"))
	r := startSource(t, k, box, follow100)
	r.sink.await(t, "boot 1", func() bool { return len(r.sink.texts()) == 1 })

	k.end("p", "app")
	box.set(exited(runningObs("uid-1", "c1"), 1))
	r.sink.await(t, "waiting", func() bool { return r.sink.lastState().State == provider.LogWaiting })
	assert.Contains(t, r.sink.lastState().Message, "exited (code 1)")
	n := len(k.requestLog())
	time.Sleep(100 * time.Millisecond)
	assert.Len(t, k.requestLog(), n, "waiting for a restart makes no requests (no polling)")

	k.start("p", "app", "c2")
	k.log("p", "app", fakeRec{ts: at(30), text: "boot 2"})
	o := runningObs("uid-1", "c2")
	o.ctrs["app"] = ctrObs{id: "c2", running: true, startedAt: at(29), restarts: 3} // restartCount jumped by 3
	box.set(o)
	r.sink.await(t, "boot 2", func() bool { return len(r.sink.texts()) == 2 })
	assert.Equal(t, []string{"boot 1", "boot 2"}, r.sink.texts())
	last := k.requestLog()[len(k.requestLog())-1]
	assert.Equal(t, fmt.Sprint(startupTail), last.Get("tailLines"), "a new instance from its start (bounded)")
	assert.Empty(t, last.Get("sinceTime"))
}

// The cache may report the new instance before the old stream's EOF: the
// source must not then wait for another event that never comes.
func TestLogRestartSeenBeforeEOFOpensAtOnce(t *testing.T) {
	k := newFakeKubelet(t)
	k.start("p", "app", "c1")
	k.log("p", "app", fakeRec{ts: at(1), text: "boot 1"})
	box := syncedBox(runningObs("uid-1", "c1"))
	r := startSource(t, k, box, follow100)
	r.sink.await(t, "boot 1", func() bool { return len(r.sink.texts()) == 1 })

	o := runningObs("uid-1", "c2")
	o.ctrs["app"] = ctrObs{id: "c2", running: true, startedAt: at(20)}
	box.set(o) // seen first
	k.start("p", "app", "c2")
	k.log("p", "app", fakeRec{ts: at(21), text: "boot 2"})
	k.mu.Lock()
	k.ctrs["p/app"].insts[0].ended = true // the old stream ends only now
	k.bumpLocked()
	k.mu.Unlock()
	r.sink.await(t, "boot 2", func() bool { return len(r.sink.texts()) == 2 })
	assert.False(t, r.sink.hasState(provider.LogWaiting))
}

func TestLogFinishedContainersEnd(t *testing.T) {
	for name, c := range map[string]struct {
		obs  func() podObs
		want string
	}{
		"restartPolicy Never": {func() podObs {
			o := exited(runningObs("uid-1", "c1"), 0)
			o.restartPolicy = "Never"
			return o
		}, "finished (exit code 0)"},
		"OnFailure and success": {func() podObs {
			o := exited(runningObs("uid-1", "c1"), 0)
			o.restartPolicy = "OnFailure"
			return o
		}, "finished"},
		"completed init container": {func() podObs {
			o := exited(runningObs("uid-1", "c1"), 0)
			c := o.ctrs["app"]
			c.kind = ctrInit
			o.ctrs["app"] = c
			return o
		}, "finished"},
		"ephemeral": {func() podObs {
			o := exited(runningObs("uid-1", "c1"), 137)
			c := o.ctrs["app"]
			c.kind = ctrEphemeral
			o.ctrs["app"] = c
			return o
		}, "finished (exit code 137)"},
		"pod deleted":  {func() podObs { return podObs{} }, "deleted"},
		"pod replaced": {func() podObs { return runningObs("uid-2", "c9") }, "replaced"},
	} {
		t.Run(name, func(t *testing.T) {
			k := newFakeKubelet(t)
			k.start("p", "app", "c1")
			k.log("p", "app", fakeRec{ts: at(1), text: "x"})
			box := syncedBox(runningObs("uid-1", "c1"))
			r := startSource(t, k, box, follow100)
			r.sink.await(t, "x", func() bool { return len(r.sink.texts()) == 1 })
			box.set(c.obs())
			k.end("p", "app")
			require.NoError(t, r.finished(t))
			assert.Equal(t, provider.LogEnded, r.sink.lastState().State)
			assert.Contains(t, r.sink.lastState().Message, c.want)
		})
	}
}

func TestLogSidecarInitContainerWaitsForRestart(t *testing.T) {
	k := newFakeKubelet(t)
	k.start("p", "app", "c1")
	o := runningObs("uid-1", "c1")
	c := o.ctrs["app"]
	c.kind = ctrSidecar
	o.ctrs["app"] = c
	box := syncedBox(o)
	r := startSource(t, k, box, follow100)
	r.sink.await(t, "streaming", func() bool { return r.sink.lastState().State == provider.LogStreaming })
	box.set(exited(o, 0))
	k.end("p", "app")
	r.sink.await(t, "waiting", func() bool { return r.sink.lastState().State == provider.LogWaiting })
}

// The same instance still runs after an EOF: a broken connection — retry
// with the cursor.
func TestLogCleanEOFWhileRunningRetries(t *testing.T) {
	k := newFakeKubelet(t)
	k.start("p", "app", "c1")
	k.log("p", "app", fakeRec{ts: at(1), text: "one"})
	r := startSource(t, k, syncedBox(runningObs("uid-1", "c1")), follow100)
	r.sink.await(t, "one", func() bool { return len(r.sink.texts()) == 1 })
	k.endAll() // the response ends cleanly, the container still runs
	k.log("p", "app", fakeRec{ts: at(2), text: "two"})
	r.sink.await(t, "two", func() bool { return len(r.sink.texts()) == 2 })
	assert.Equal(t, []string{"one", "two"}, r.sink.texts())
}

// No permission to watch pods (pods/log allowed): stream what there is,
// then say restarts cannot be followed.
func TestLogWithoutPodTrackingEndsHonestly(t *testing.T) {
	k := newFakeKubelet(t)
	k.start("p", "app", "c1")
	k.log("p", "app", fakeRec{ts: at(1), text: "one"})
	box := newObsBox()
	box.setBlind("forbidden: pods is forbidden")
	r := startSource(t, k, box, follow100)
	r.sink.await(t, "one", func() bool { return len(r.sink.texts()) == 1 })
	k.end("p", "app")
	require.NoError(t, r.finished(t))
	assert.Equal(t, provider.LogEnded, r.sink.lastState().State)
	assert.Contains(t, r.sink.lastState().Message, "restart tracking is unavailable")
	assert.NotEqual(t, provider.ClassForbidden, r.sink.lastState().Class, "an inventory denial is not a pods/log denial")
}

func TestLogWaitingToStartThenStarts(t *testing.T) {
	k := newFakeKubelet(t)
	k.mu.Lock()
	k.ctrs["p/app"] = &fakeCtr{} // no instance yet
	k.mu.Unlock()
	o := runningObs("uid-1", "")
	o.ctrs["app"] = ctrObs{} // spec only, no status yet
	box := syncedBox(o)
	r := startSource(t, k, box, follow100)
	r.sink.await(t, "waiting", func() bool { return r.sink.lastState().State == provider.LogWaiting })
	assert.Empty(t, k.requestLog(), "a container that never started is not asked for logs")

	k.start("p", "app", "c1")
	k.log("p", "app", fakeRec{ts: at(3), text: "hello"})
	box.set(runningObs("uid-1", "c1"))
	r.sink.await(t, "hello", func() bool { return len(r.sink.texts()) == 1 })
}

func TestLogRequestErrors(t *testing.T) {
	k := newFakeKubelet(t)
	k.start("p", "app", "c1")

	r := startSource(t, k, nil, provider.LogQuery{Previous: true, TailLines: 10})
	require.NoError(t, r.finished(t))
	assert.Equal(t, provider.LogState{State: provider.LogEnded, Class: provider.ClassNotFound, Message: "there is no previous instance of this container"}, r.sink.lastState())

	k.mu.Lock()
	k.forbidden = true
	k.mu.Unlock()
	r = startSource(t, k, syncedBox(runningObs("uid-1", "c1")), follow100)
	require.NoError(t, r.finished(t))
	assert.Equal(t, provider.LogError, r.sink.lastState().State)
	assert.Equal(t, provider.ClassForbidden, r.sink.lastState().Class)
	assert.Contains(t, r.sink.lastState().Message, "pods/log")
}

func TestLogPreviousInstance(t *testing.T) {
	k := newFakeKubelet(t)
	k.start("p", "app", "c1")
	k.log("p", "app", fakeRec{ts: at(1), text: "old run"})
	k.end("p", "app")
	k.start("p", "app", "c2")
	k.log("p", "app", fakeRec{ts: at(5), text: "new run"})
	r := startSource(t, k, nil, provider.LogQuery{Previous: true, TailLines: provider.TailAll})
	require.NoError(t, r.finished(t))
	assert.Equal(t, []string{"old run"}, r.sink.texts())
	assert.Equal(t, fmt.Sprint(allTail), k.requestLog()[0].Get("tailLines"), "all = the UI's buffer size")
}

// CRI partial records form one logical line with one timestamp; a final
// fragment without a newline is a line at a clean end.
func TestLogPartialRecordsAndUnterminatedEnd(t *testing.T) {
	k := newFakeKubelet(t)
	k.start("p", "app", "c1")
	k.log("p", "app",
		fakeRec{ts: at(1), text: "part one ", partial: true},
		fakeRec{ts: at(1.1), text: "2026-09-29T10:00:00Z looks like a timestamp ", partial: true},
		fakeRec{ts: at(1.2), text: "end"},
		fakeRec{ts: at(2), text: "no newline at the end", partial: true},
	)
	k.end("p", "app")
	r := startSource(t, k, nil, provider.LogQuery{TailLines: provider.TailAll})
	require.NoError(t, r.finished(t))
	require.Len(t, r.sink.lines, 2)
	assert.Equal(t, "part one 2026-09-29T10:00:00Z looks like a timestamp end", r.sink.lines[0].Text)
	assert.Equal(t, at(1).Format(time.RFC3339Nano), r.sink.lines[0].TS)
	assert.Equal(t, "no newline at the end", r.sink.lines[1].Text)
}

func TestLineReaderBounds(t *testing.T) {
	long := strings.Repeat("x", maxLineBytes+5000)
	in := "2026-09-29T10:00:00.000000001Z " + long + "\n" +
		"2026-09-29T10:00:00.000000002Z next\r\n" +
		"garbage without time\n" +
		"2026-09-29T10:00:00.000000003Z ünïcödé \x1b[31mred\x1b[0m\n" +
		"2026-09-29T10:00:00.000000004Z tail"
	lr := newLineReader(iotest.OneByteReader(strings.NewReader(in)))
	var got []provider.LogLine
	for {
		l, complete, err := lr.next()
		if complete {
			got = append(got, l)
		}
		if err != nil {
			assert.ErrorIs(t, err, io.EOF)
			assert.False(t, complete)
			assert.Equal(t, "tail", l.Text, "the unterminated fragment is handed back for the caller to judge")
			break
		}
	}
	require.Len(t, got, 4)
	assert.Equal(t, provider.LineCut, got[0].Flags)
	assert.Len(t, got[0].Text, maxLineBytes-len("2026-09-29T10:00:00.000000001Z "))
	assert.Equal(t, provider.LogLine{TS: "2026-09-29T10:00:00.000000002Z", Text: "next"}, got[1], "the cut line's rest was discarded, CRLF trimmed")
	assert.Equal(t, provider.LogLine{Text: "garbage without time", Flags: provider.LineNoTime}, got[2])
	assert.Equal(t, "ünïcödé \x1b[31mred\x1b[0m", got[3].Text, "bytes kept as they are")
}

func TestCursorReplayPlan(t *testing.T) {
	var c cursor
	for _, s := range []float64{1.2, 2.5, 2.7, 3.1} {
		c.add(at(s).Format(time.RFC3339Nano))
	}
	thr, expect, covered := c.replay(time.Time{})
	assert.Equal(t, at(3), thr)
	assert.Equal(t, []string{at(3.1).Format(time.RFC3339Nano)}, expect)
	assert.True(t, covered)

	thr, _, _ = c.replay(at(10))
	assert.Equal(t, at(10), thr, "the user's cutoff wins")

	c.reset()
	for i := range cursorRing + 5 {
		c.add(at(100 + float64(i)/1e6).Format(time.RFC3339Nano)) // all in one second
	}
	_, expect, covered = c.replay(time.Time{})
	assert.Len(t, expect, cursorRing)
	assert.False(t, covered, "lines of that second fell out of the ring")
}

// The first request went out before the pod was observed (the instance is
// unknown): its end must not make the same file look like a new instance
// and be read again (found on kind).
func TestLogFirstRequestBeforeTheCacheSynced(t *testing.T) {
	old := firstSyncWait
	firstSyncWait = 10 * time.Millisecond
	t.Cleanup(func() { firstSyncWait = old })
	k := newFakeKubelet(t)
	k.start("p", "app", "c1")
	k.log("p", "app", fakeRec{ts: at(1), text: "run 1"}, fakeRec{ts: at(1.5), text: "tick"})
	box := newObsBox() // not synced yet
	r := startSource(t, k, box, follow100)
	r.sink.await(t, "run 1", func() bool { return len(r.sink.texts()) == 2 })

	box.set(runningObs("uid-1", "c1"))
	box.setSynced()
	k.end("p", "app")
	box.set(exited(runningObs("uid-1", "c1"), 1))
	r.sink.await(t, "waiting", func() bool { return r.sink.lastState().State == provider.LogWaiting })
	k.start("p", "app", "c2")
	k.log("p", "app", fakeRec{ts: at(9), text: "run 2"})
	o := runningObs("uid-1", "c2")
	o.ctrs["app"] = ctrObs{id: "c2", running: true, startedAt: at(8)}
	box.set(o)
	r.sink.await(t, "run 2", func() bool { return len(r.sink.texts()) == 3 })
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, []string{"run 1", "tick", "run 2"}, r.sink.texts())
	assert.False(t, r.sink.hasState(provider.LogGap), "the new instance was recognised, not guessed")
}
