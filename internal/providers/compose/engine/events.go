package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"time"

	"github.com/spk/spk-ocular/internal/provider"
)

// ErrEventTooLarge: one event of the stream was longer than
// Limits.EventLineBytes. The stream is over after it (skipping would hide a
// change from the observer; a new subscription and a resync are the fix).
var ErrEventTooLarge = errors.New("an event is larger than the limit")

// EventStream is an open GET /events. Next blocks until an event, the end
// of the stream or the caller's context; Close ends it.
type EventStream struct {
	ctx    context.Context
	body   io.ReadCloser
	cancel context.CancelFunc
	br     *bufio.Reader
	max    int
}

// Events subscribes to the events stream from since (zero: from now) with
// filters. The deadline covers only the response headers; the stream then
// lives until ctx ends or Close.
func (c *Client) Events(ctx context.Context, since time.Time, f Filters) (*EventStream, error) {
	q := url.Values{}
	if !since.IsZero() {
		q.Set("since", unixNano(since))
	}
	if err := setFilters(q, f); err != nil {
		return nil, err
	}
	resp, cancel, err := c.openStream(ctx, "/events", q)
	if err != nil {
		return nil, err
	}
	return &EventStream{
		ctx: ctx, body: resp.Body, cancel: cancel,
		br:  bufio.NewReaderSize(resp.Body, 16<<10),
		max: c.lim.EventLineBytes,
	}, nil
}

// unixNano formats t as the Engine's "seconds.nanoseconds".
func unixNano(t time.Time) string {
	return strconv.FormatInt(t.Unix(), 10) + "." + fmt.Sprintf("%09d", t.Nanosecond())
}

// Next returns the next event. io.EOF: the daemon ended the stream.
func (s *EventStream) Next() (Event, error) {
	for {
		line, err := s.line()
		if err != nil {
			return Event{}, err
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			return Event{}, &Error{Class: provider.ClassUnavailable, Message: fmt.Sprintf("an undecodable event: %v", err), Err: err}
		}
		return ev, nil
	}
}

// line reads one newline-terminated line of at most s.max bytes; its
// memory is bounded by the limit whatever the daemon sends.
func (s *EventStream) line() ([]byte, error) {
	var buf []byte
	for {
		chunk, err := s.br.ReadSlice('\n')
		if len(buf)+len(chunk) > s.max {
			return nil, &Error{Class: provider.ClassUnavailable, Message: fmt.Sprintf("an event is larger than %d bytes", s.max), Err: ErrEventTooLarge}
		}
		buf = append(buf, chunk...)
		switch {
		case err == nil:
			return buf, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if len(bytes.TrimSpace(buf)) != 0 {
				return nil, &Error{Class: provider.ClassUnavailable, Message: "the event stream ended in the middle of an event", Err: io.ErrUnexpectedEOF}
			}
			return nil, io.EOF
		default:
			return nil, streamError(s.ctx, err)
		}
	}
}

// Close ends the stream (safe to call more than once).
func (s *EventStream) Close() error {
	s.cancel()
	return s.body.Close()
}

// streamError classifies a failed read of a stream body.
func streamError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctxError(ctx)
	}
	return &Error{Class: provider.ClassUnavailable, Message: "the Docker Engine stream broke: " + err.Error(), Err: err}
}
