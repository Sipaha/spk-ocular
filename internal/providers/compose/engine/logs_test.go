package engine_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/compose/engine"
	"github.com/spk/spk-ocular/internal/providers/compose/enginefake"
)

// chunks is a reader that returns exactly the given pieces, one per Read
// (how the network may cut a stream).
type chunks struct{ parts [][]byte }

func (c *chunks) Read(p []byte) (int, error) {
	for len(c.parts) > 0 && len(c.parts[0]) == 0 {
		c.parts = c.parts[1:]
	}
	if len(c.parts) == 0 {
		return 0, io.EOF
	}
	n := copy(p, c.parts[0])
	c.parts[0] = c.parts[0][n:]
	return n, nil
}

func split(b []byte, at ...int) [][]byte {
	var out [][]byte
	prev := 0
	for _, i := range at {
		out = append(out, b[prev:i])
		prev = i
	}
	return append(out, b[prev:])
}

type rec struct {
	Stream    engine.Stream
	Line      string
	Truncated bool
	Partial   bool
	Gap       bool
}

// readAll returns the records (simplified) and the final error.
func readAll(r *engine.LogReader) ([]rec, error) {
	var out []rec
	for {
		x, err := r.Next()
		if err != nil {
			return out, err
		}
		out = append(out, rec{Stream: x.Stream, Line: string(x.Line), Truncated: x.Truncated, Partial: x.Partial, Gap: x.Gap != ""})
	}
}

func reader(parts [][]byte, tty, ts bool, lim engine.Limits) *engine.LogReader {
	return engine.NewLogReader(&chunks{parts: parts}, tty, ts, lim)
}

func frame(stream byte, s string) []byte { return enginefake.Frame(stream, []byte(s)) }

func cat(bs ...[]byte) []byte { return bytes.Join(bs, nil) }

func TestStdcopyHeaderSplitAcrossReads(t *testing.T) {
	data := cat(frame(1, "hello\n"), frame(2, "oops\n"))
	for _, at := range [][]int{{1}, {3, 7}, {8}, {9}, {14, 17}} {
		got, err := readAll(reader(split(data, at...), false, false, engine.Limits{}))
		assert.ErrorIs(t, err, io.EOF)
		assert.Equal(t, []rec{{Stream: engine.Stdout, Line: "hello"}, {Stream: engine.Stderr, Line: "oops"}}, got, at)
	}
}

func TestStdcopySeveralFramesInOneRead(t *testing.T) {
	data := cat(frame(1, "a\n"), frame(2, "b\n"), frame(1, "c\nd\n"), frame(0, "e\n"))
	got, err := readAll(reader([][]byte{data}, false, false, engine.Limits{}))
	assert.ErrorIs(t, err, io.EOF)
	assert.Equal(t, []rec{
		{Stream: engine.Stdout, Line: "a"}, {Stream: engine.Stderr, Line: "b"},
		{Stream: engine.Stdout, Line: "c"}, {Stream: engine.Stdout, Line: "d"},
		{Stream: engine.Stdout, Line: "e"}, // stream 0 (stdin) is written as stdout, like moby
	}, got)
}

func TestStdcopyAFrameSplitMidLineAndLinesAcrossFrames(t *testing.T) {
	// a line continues across frames; the other stream interleaves
	data := cat(frame(1, "hel"), frame(2, "err "), frame(1, "lo wor"), frame(2, "line\n"), frame(1, "ld\nnext\n"))
	got, err := readAll(reader(split(data, 5, 12, 20), false, false, engine.Limits{}))
	assert.ErrorIs(t, err, io.EOF)
	assert.Equal(t, []rec{
		{Stream: engine.Stderr, Line: "err line"},
		{Stream: engine.Stdout, Line: "hello world"},
		{Stream: engine.Stdout, Line: "next"},
	}, got)
}

func TestStdcopyAHugeDeclaredLengthIsNeverAllocated(t *testing.T) {
	lim := engine.Limits{LogFrameBytes: 1024}
	// over the frame limit: the stream is corrupt, an error before reading it
	huge := []byte{1, 0, 0, 0, 0xff, 0xff, 0xff, 0xff}
	got, err := readAll(reader([][]byte{cat(frame(1, "before\n"), huge, []byte("tail"))}, false, false, lim))
	assert.Equal(t, []rec{{Stream: engine.Stdout, Line: "before"}}, got)
	assert.Equal(t, provider.ClassUnavailable, engine.ClassOf(err))
	assert.Contains(t, err.Error(), "4294967295")

	// within the frame limit but cut short: streamed, not preallocated
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	declared := []byte{1, 0, 0, 0, 0x00, 0x7f, 0xff, 0xff} // ~8 MiB
	got, err = readAll(reader([][]byte{cat(declared, []byte("only a little\n"))}, false, false, engine.Limits{}))
	runtime.ReadMemStats(&after)
	assert.ErrorIs(t, err, io.EOF)
	require.Len(t, got, 2)
	assert.Equal(t, rec{Stream: engine.Stdout, Line: "only a little"}, got[0])
	assert.True(t, got[1].Gap, "the frame cut by the end of the stream is a visible gap")
	assert.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(1<<20), "the declared length was allocated")
}

func TestStdcopyIncompleteHeaderAtEOFIsAGap(t *testing.T) {
	r := reader([][]byte{cat(frame(1, "last line\n"), []byte{2, 0, 0})}, false, false, engine.Limits{})
	var got []engine.LogRecord
	var err error
	for {
		var x engine.LogRecord
		if x, err = r.Next(); err != nil {
			break
		}
		got = append(got, x)
	}
	assert.ErrorIs(t, err, io.EOF)
	require.Len(t, got, 2)
	assert.Equal(t, "last line", string(got[0].Line))
	assert.Contains(t, got[1].Gap, "frame header (3 of 8 bytes)")
}

func TestStdcopyAFrameCutAtEOFKeepsItsBytesAndSaysSo(t *testing.T) {
	cut := frame(2, "a partial line that never ends")[:8+9]
	got, err := readAll(reader([][]byte{cut}, false, false, engine.Limits{}))
	assert.ErrorIs(t, err, io.EOF)
	assert.Equal(t, []rec{{Stream: engine.Stderr, Line: "a partial", Partial: true}, {Gap: true}}, got)
}

func TestStdcopyStreamThreeIsTheDaemonsError(t *testing.T) {
	data := cat(frame(1, "ok\n"), frame(1, "open"), frame(3, "error from daemon in stream: log file rotated\n"), frame(1, "never\n"))
	got, err := readAll(reader([][]byte{data}, false, false, engine.Limits{}))
	assert.Equal(t, []rec{{Stream: engine.Stdout, Line: "ok"}, {Stream: engine.Stdout, Line: "open", Partial: true}}, got)
	assert.Equal(t, provider.ClassUnavailable, engine.ClassOf(err))
	assert.Contains(t, err.Error(), "log file rotated")
}

func TestAnUnknownStreamIsCorruption(t *testing.T) {
	_, err := readAll(reader([][]byte{frame(7, "x\n")}, false, false, engine.Limits{}))
	assert.Contains(t, err.Error(), "corrupt")
}

func TestALineLongerThanTheLimitIsTruncatedAndSaysSo(t *testing.T) {
	lim := engine.Limits{LogLineBytes: 10}
	long := strings.Repeat("x", 25)
	data := cat(frame(1, long[:12]), frame(1, long[12:]+"\nshort\n"), frame(2, "0123456789\n"))
	got, err := readAll(reader(split(data, 3), false, false, lim))
	assert.ErrorIs(t, err, io.EOF)
	assert.Equal(t, []rec{
		{Stream: engine.Stdout, Line: strings.Repeat("x", 10), Truncated: true},
		{Stream: engine.Stdout, Line: "short"},
		{Stream: engine.Stderr, Line: "0123456789"}, // exactly the limit
	}, got)
}

func TestTTYIsOneRawStream(t *testing.T) {
	// what looks like a stdcopy header is just output
	data := []byte("\x01\x00\x00\x00\x00\x00\x00\x03abc\r\nline two\r\nno newline")
	got, err := readAll(reader(split(data, 2, 13), true, false, engine.Limits{}))
	assert.ErrorIs(t, err, io.EOF)
	assert.Equal(t, []rec{
		{Stream: engine.Stdout, Line: "\x01\x00\x00\x00\x00\x00\x00\x03abc"},
		{Stream: engine.Stdout, Line: "line two"},
		{Stream: engine.Stdout, Line: "no newline", Partial: true},
	}, got)
}

func TestTimestampsAreParsed(t *testing.T) {
	ts1, ts2, ts3 := "2026-09-30T10:00:00.123456789Z", "2026-09-30T10:00:01.5Z", "2026-09-30T10:00:02Z"
	data := cat(
		frame(1, ts1+" hello\n"),
		// a long line in two messages: the Engine timestamps each
		frame(2, ts2+" first half "), frame(2, ts3+" second half\n"),
		frame(1, "no timestamp here\n"),
		frame(1, ts3+" \n"), // an empty line
	)
	r := reader([][]byte{data}, false, true, engine.Limits{})
	var got []engine.LogRecord
	for {
		x, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		got = append(got, x)
	}
	require.Len(t, got, 4)
	assert.Equal(t, "hello", string(got[0].Line))
	assert.Equal(t, time.Date(2026, 9, 30, 10, 0, 0, 123456789, time.UTC), got[0].Time)
	assert.Equal(t, "first half second half", string(got[1].Line))
	assert.Equal(t, engine.Stderr, got[1].Stream)
	// a line of several messages: Time is its start, End its last message's
	assert.Equal(t, time.Date(2026, 9, 30, 10, 0, 1, 5e8, time.UTC), got[1].Time)
	assert.Equal(t, time.Date(2026, 9, 30, 10, 0, 2, 0, time.UTC), got[1].End)
	assert.Equal(t, got[0].Time, got[0].End)
	assert.Equal(t, time.Date(2026, 9, 30, 10, 0, 1, 5e8, time.UTC), got[1].Time)
	assert.Equal(t, "no timestamp here", string(got[2].Line))
	assert.True(t, got[2].Time.IsZero())
	assert.Equal(t, "", string(got[3].Line))
	assert.NotNil(t, got[3].Line)
	assert.Equal(t, time.Date(2026, 9, 30, 10, 0, 2, 0, time.UTC), got[3].Time)

	// TTY: the timestamp starts each line too
	got2, err := readAll(reader([][]byte{[]byte(ts1 + " tty line\r\n")}, true, true, engine.Limits{}))
	assert.ErrorIs(t, err, io.EOF)
	assert.Equal(t, []rec{{Stream: engine.Stdout, Line: "tty line"}}, got2)
}

func TestContainerLogsOverTheWire(t *testing.T) {
	f := enginefake.New(t)
	f.PutContainer(engine.ContainerInspect{ID: "c1", Name: "/web"})
	f.SetLogs("c1", cat(frame(1, "one\n"), frame(2, "two\n")))
	c := newClient(t, engine.Config{Host: f.Host(), RequestTimeout: 100 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	since := time.Unix(1790000000, 250000000)
	r, err := c.ContainerLogs(ctx, "c1", engine.LogsOptions{Follow: true, Timestamps: true, Since: since, Tail: 100}, false)
	require.NoError(t, err)
	defer r.Close()
	reqs := f.Requests()
	q := reqs[len(reqs)-1].Query
	assert.Equal(t, "/containers/c1/logs", reqs[len(reqs)-1].Path)
	for k, v := range map[string]string{"stdout": "1", "stderr": "1", "follow": "1", "timestamps": "1", "tail": "100", "since": "1790000000.250000000"} {
		assert.Equal(t, v, q.Get(k), k)
	}
	for _, want := range []string{"one", "two"} {
		x, err := r.Next()
		require.NoError(t, err)
		assert.Equal(t, want, string(x.Line))
	}

	// follow outlives the request deadline; new output arrives
	time.Sleep(300 * time.Millisecond)
	f.AppendLogs("c1", frame(1, "three\n"))
	x, err := r.Next()
	require.NoError(t, err)
	assert.Equal(t, "three", string(x.Line))

	// cancel ends it
	done := make(chan error, 1)
	go func() { _, err := r.Next(); done <- err }()
	cancel()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not end the logs")
	}

	// without follow the answer ends; defaults ask for all of both streams
	r2, err := c.ContainerLogs(context.Background(), "c1", engine.LogsOptions{}, true)
	require.NoError(t, err)
	defer r2.Close()
	reqs = f.Requests()
	q = reqs[len(reqs)-1].Query
	assert.Equal(t, "all", q.Get("tail"))
	assert.Equal(t, "1", q.Get("stdout"))
	assert.Equal(t, "1", q.Get("stderr"))
	assert.Empty(t, q.Get("follow"))
	assert.Empty(t, q.Get("since"))
	_, err = readAll(r2)
	assert.ErrorIs(t, err, io.EOF)

	// the daemon's end of a follow is EOF
	r3, err := c.ContainerLogs(context.Background(), "c1", engine.LogsOptions{Follow: true}, false)
	require.NoError(t, err)
	defer r3.Close()
	waitFor(t, func() bool { return f.LogFollowers("c1") == 1 })
	f.EndLogs("c1")
	got, err := readAll(r3)
	assert.ErrorIs(t, err, io.EOF)
	assert.Len(t, got, 3)

	_, err = c.ContainerLogs(context.Background(), "gone", engine.LogsOptions{}, false)
	assert.True(t, engine.IsNotFound(err))
}
