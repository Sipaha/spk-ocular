package streams

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"
)

const (
	// flushDelay coalesces frames: a burst becomes one network write.
	flushDelay = 50 * time.Millisecond
	// flushSize flushes a burst early; the buffer never grows much past it
	// (writes block while the page is not reading: backpressure reaches the
	// producer instead of piling up in memory).
	flushSize = 32 << 10
	// nudgeDelay: WebKitGTK sometimes withholds the tail of a burst from
	// the page until more bytes arrive (docs/spikes/2026-09-29-log-stream-
	// desktop.md). A tiny frame this long after a flush that went quiet
	// releases it.
	nudgeDelay = 100 * time.Millisecond
	// heartbeat keeps a quiet stream observably alive (and detects a dead
	// connection on our side).
	heartbeat = 20 * time.Second
	// writeTimeout bounds one network write: a page that stops reading is
	// disconnected rather than blocking its producers forever.
	writeTimeout = 60 * time.Second
)

var errWriterClosed = errors.New("stream writer closed")

// pingFrame is the nudge / heartbeat.
var pingFrame = []byte(`{"k":"ping"}` + "\n")

// Writer writes NDJSON frames (one JSON object per line) with coalesced
// flushing. Frames are never split across flushes. Safe for concurrent use.
type Writer struct {
	out         io.Writer
	flush       func() error
	setDeadline func(time.Time) error

	nudge, beat, timeout time.Duration

	mu     sync.Mutex
	buf    bytes.Buffer
	timer  *time.Timer // pending coalesced flush
	idle   *time.Timer // nudge, then heartbeats
	err    error
	closed bool
}

func newWriter(out io.Writer, flush func() error, setDeadline func(time.Time) error) *Writer {
	if setDeadline == nil {
		setDeadline = func(time.Time) error { return nil }
	}
	return &Writer{out: out, flush: flush, setDeadline: setDeadline, nudge: nudgeDelay, beat: heartbeat, timeout: writeTimeout}
}

// Frame writes v as one line. It blocks while the page is not reading and a
// buffer's worth is pending; it fails once the connection is gone.
func (w *Writer) Frame(v any) error {
	// No HTML escaping: the page parses JSON, and logs are full of <>&.
	var fb bytes.Buffer
	enc := json.NewEncoder(&fb)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil { // Encode appends the '\n'
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return errWriterClosed
	}
	if w.err != nil {
		return w.err
	}
	w.buf.Write(fb.Bytes())
	if w.buf.Len() >= flushSize {
		return w.flushLocked()
	}
	if w.timer == nil {
		w.timer = time.AfterFunc(flushDelay, w.timerFlush)
	}
	return nil
}

// Flush sends everything buffered now.
func (w *Writer) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return errWriterClosed
	}
	return w.flushLocked()
}

func (w *Writer) timerFlush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.timer = nil
	if !w.closed {
		_ = w.flushLocked()
	}
}

// idleFire sends the nudge (first) or a heartbeat (then) on a quiet stream.
func (w *Writer) idleFire() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.err != nil {
		return
	}
	w.err = w.writeLocked(pingFrame)
	if w.err == nil {
		w.idle = time.AfterFunc(w.beat, w.idleFire)
	}
}

func (w *Writer) flushLocked() error {
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	if w.err != nil {
		return w.err
	}
	if w.buf.Len() == 0 {
		return nil
	}
	w.err = w.writeLocked(w.buf.Bytes())
	w.buf.Reset()
	if w.err == nil {
		if w.idle != nil {
			w.idle.Stop()
		}
		w.idle = time.AfterFunc(w.nudge, w.idleFire)
	}
	return w.err
}

func (w *Writer) writeLocked(b []byte) error {
	_ = w.setDeadline(time.Now().Add(w.timeout))
	if _, err := w.out.Write(b); err != nil {
		return err
	}
	return w.flush()
}

// close flushes what is left and stops the writer: after it returns nothing
// touches out again (the HTTP handler may return).
func (w *Writer) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	_ = w.flushLocked()
	w.closed = true
	if w.idle != nil {
		w.idle.Stop()
	}
}
