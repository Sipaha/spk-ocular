package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spk/spk-ocular/internal/provider"
)

// Stream of a log record.
type Stream uint8

const (
	Stdout Stream = 1
	Stderr Stream = 2
)

func (s Stream) String() string {
	switch s {
	case Stdout:
		return "stdout"
	case Stderr:
		return "stderr"
	}
	return "stream" + strconv.Itoa(int(s))
}

// LogsOptions of GET /containers/{id}/logs.
type LogsOptions struct {
	Follow     bool
	Timestamps bool      // each line starts with RFC3339Nano and a space; parsed into Time
	Since      time.Time // zero: from the start of the journal
	// Tail: N > 0 — the last N lines; 0 or negative — all.
	Tail int
	// Stdout, Stderr: which streams; neither set means both.
	Stdout, Stderr bool
}

// LogRecord is one line of a container's log, or (Gap != "") a marker
// that bytes were lost here.
type LogRecord struct {
	Stream Stream
	// Time: the line's timestamp (zero without Timestamps, or when the
	// line did not start with one).
	Time time.Time
	// Line without its newline (and a TTY's trailing \r). Owned by the
	// record.
	Line []byte
	// Truncated: the line was longer than the limit; Line is its beginning.
	Truncated bool
	// Partial: the stream ended before this line's newline.
	Partial bool
	// Gap: not a line — why bytes are missing here (a frame cut at the end
	// of the stream).
	Gap string
}

// ContainerLogs opens a container's logs. tty is the container's
// Config.Tty: a TTY container's logs are one raw stream, otherwise stdcopy
// frames (stdout and stderr multiplexed). The deadline covers only the
// response headers; with Follow the stream lives until ctx ends or Close.
func (c *Client) ContainerLogs(ctx context.Context, id string, o LogsOptions, tty bool) (*LogReader, error) {
	q := url.Values{}
	if !o.Stdout && !o.Stderr {
		o.Stdout, o.Stderr = true, true
	}
	if o.Stdout {
		q.Set("stdout", "1")
	}
	if o.Stderr {
		q.Set("stderr", "1")
	}
	if o.Follow {
		q.Set("follow", "1")
	}
	if o.Timestamps {
		q.Set("timestamps", "1")
	}
	if !o.Since.IsZero() {
		q.Set("since", unixNano(o.Since))
	}
	if o.Tail > 0 {
		q.Set("tail", strconv.Itoa(o.Tail))
	} else {
		q.Set("tail", "all")
	}
	resp, cancel, err := c.openStream(ctx, "/containers/"+url.PathEscape(id)+"/logs", q)
	if err != nil {
		return nil, err
	}
	r := NewLogReader(resp.Body, tty, o.Timestamps, c.lim)
	r.ctx, r.cancel = ctx, cancel
	return r, nil
}

// LogReader turns a logs body into records. Memory is bounded by the
// limits (a line per stream, a read buffer), never by what the container
// printed or what a frame header declares.
type LogReader struct {
	ctx        context.Context
	cancel     context.CancelFunc
	body       io.Reader
	br         *bufio.Reader
	tty, stamp bool
	maxLine    int
	maxFrame   int
	maxErr     int
	lines      [3]lineAsm // by Stream
	queue      []LogRecord
	err        error // after the queue: io.EOF or the failure
	damaged    bool  // the stream ended inside a frame (see EndedCleanly)
	scratch    []byte
}

type lineAsm struct {
	buf []byte
	cut bool
}

func (a *lineAsm) open() bool { return len(a.buf) > 0 || a.cut }

// NewLogReader reads a logs body (stdcopy frames unless tty). Only the
// Log*/ErrorBytes fields of lim are used; zero ones take the defaults.
func NewLogReader(body io.Reader, tty, timestamps bool, lim Limits) *LogReader {
	lim = lim.withDefaults()
	return &LogReader{
		ctx: context.Background(), cancel: func() {},
		body: body, br: bufio.NewReaderSize(body, 32<<10),
		tty: tty, stamp: timestamps,
		maxLine: lim.LogLineBytes, maxFrame: lim.LogFrameBytes, maxErr: int(lim.ErrorBytes),
		scratch: make([]byte, 32<<10),
	}
}

// Next returns the next record; io.EOF after the last one of a stream
// that ended cleanly (gaps included). Other errors: the stream broke or
// the daemon reported an error in it (*Error).
func (r *LogReader) Next() (LogRecord, error) {
	for len(r.queue) == 0 && r.err == nil {
		if r.tty {
			r.fillRaw()
		} else {
			r.fillFrame()
		}
	}
	if len(r.queue) > 0 {
		rec := r.queue[0]
		r.queue[0] = LogRecord{}
		r.queue = r.queue[1:]
		return rec, nil
	}
	return LogRecord{}, r.err
}

// EndedCleanly: the stream is over and ended with a clean EOF (a Partial
// record then is the log's real last line, not a cut one). A stream cut
// inside a frame is not clean: its Partial is a damaged prefix.
func (r *LogReader) EndedCleanly() bool { return errors.Is(r.err, io.EOF) && !r.damaged }

// Pending is how many decoded records the next Next calls return without
// reading: at 0 the next Next may wait for the daemon (buffered bytes of
// an incomplete frame or line do not make a record).
func (r *LogReader) Pending() int { return len(r.queue) }

// Close ends the stream.
func (r *LogReader) Close() error {
	r.cancel()
	if c, ok := r.body.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

func (r *LogReader) fillRaw() {
	n, err := r.br.Read(r.scratch)
	if n > 0 {
		r.feed(Stdout, r.scratch[:n], false)
	}
	if err != nil {
		r.end(err, "")
	}
}

// stdcopy: an 8-byte header [stream, 0, 0, 0, size (big-endian uint32)],
// then size bytes. 0 (stdin) is written as stdout like moby's stdcopy; 3
// is an error the daemon hit while streaming (the payload is its text).
func (r *LogReader) fillFrame() {
	var hdr [8]byte
	n, err := io.ReadFull(r.br, hdr[:])
	switch {
	case errors.Is(err, io.ErrUnexpectedEOF):
		r.end(io.EOF, fmt.Sprintf("the log stream ended inside a frame header (%d of 8 bytes)", n))
		return
	case err != nil:
		r.end(err, "")
		return
	}
	size := int64(binary.BigEndian.Uint32(hdr[4:]))
	var s Stream
	switch hdr[0] {
	case 0, 1:
		s = Stdout
	case 2:
		s = Stderr
	case 3:
		msg := make([]byte, min(size, int64(r.maxErr)))
		k, _ := io.ReadFull(r.br, msg)
		r.flush()
		r.err = &Error{Class: provider.ClassUnavailable, Message: "the Docker Engine reported an error in the log stream: " + strings.TrimSpace(string(msg[:k]))}
		return
	default:
		r.flush()
		r.err = &Error{Class: provider.ClassUnavailable, Message: fmt.Sprintf("the log stream is corrupt (a frame of unknown stream %d)", hdr[0])}
		return
	}
	if size > int64(r.maxFrame) {
		// never read into memory: a header like this is corruption, and
		// skipping gigabytes would hang the reader
		r.flush()
		r.err = &Error{Class: provider.ClassUnavailable, Message: fmt.Sprintf("the log stream is corrupt (a frame of %d bytes; the limit is %d)", size, r.maxFrame)}
		return
	}
	left := size
	first := true
	for left > 0 {
		k, err := io.ReadFull(r.br, r.scratch[:min(left, int64(len(r.scratch)))])
		if k > 0 {
			r.feed(s, r.scratch[:k], first)
			first = false
			left -= int64(k)
		}
		if err != nil {
			gap := ""
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				err = io.EOF
				gap = fmt.Sprintf("the log stream ended inside a frame (%d of %d bytes)", size-left, size)
			}
			r.end(err, gap)
			return
		}
	}
}

// end finishes the stream: open lines become Partial records, then the
// gap marker (if any); err is returned after them.
func (r *LogReader) end(err error, gap string) {
	r.damaged = gap != ""
	r.flush()
	if gap != "" {
		r.queue = append(r.queue, LogRecord{Gap: gap})
	}
	if errors.Is(err, io.EOF) {
		r.err = io.EOF
		return
	}
	r.err = streamError(r.ctx, err)
}

func (r *LogReader) flush() {
	for s := Stdout; s <= Stderr; s++ {
		if r.lines[s].open() {
			r.emit(s, true)
		}
	}
}

// feed adds a frame's (or a TTY read's) bytes to s's open line. The
// Engine timestamps every message it writes, and a line longer than the
// driver's message size arrives as several messages — each with its own
// timestamp; so a frame continuing an open line drops its leading
// timestamp (moby daemon/logs: partial messages carry no newline).
func (r *LogReader) feed(s Stream, p []byte, frameStart bool) {
	a := &r.lines[s]
	if frameStart && r.stamp && a.open() {
		if _, rest, ok := splitTimestamp(p); ok {
			p = rest
		}
	}
	for len(p) > 0 {
		part := p
		nl := bytes.IndexByte(p, '\n')
		if nl >= 0 {
			part, p = p[:nl], p[nl+1:]
		} else {
			p = nil
		}
		if room := r.maxLine - len(a.buf); len(part) > room {
			a.buf = append(a.buf, part[:room]...)
			a.cut = true
		} else {
			a.buf = append(a.buf, part...)
		}
		if nl >= 0 {
			r.emit(s, false)
		}
	}
}

func (r *LogReader) emit(s Stream, partial bool) {
	a := &r.lines[s]
	line := bytes.TrimSuffix(a.buf, []byte("\r"))
	rec := LogRecord{Stream: s, Truncated: a.cut, Partial: partial}
	if r.stamp {
		if t, rest, ok := splitTimestamp(line); ok {
			rec.Time, line = t, rest
		}
	}
	rec.Line = bytes.Clone(line)
	if rec.Line == nil {
		rec.Line = []byte{}
	}
	r.queue = append(r.queue, rec)
	a.buf, a.cut = a.buf[:0], false
}

// splitTimestamp splits "<RFC3339Nano> <rest>".
func splitTimestamp(b []byte) (time.Time, []byte, bool) {
	i := bytes.IndexByte(b[:min(len(b), 41)], ' ')
	if i <= 0 {
		return time.Time{}, b, false
	}
	t, err := time.Parse(time.RFC3339Nano, string(b[:i]))
	if err != nil {
		return time.Time{}, b, false
	}
	return t, b[i+1:], true
}
