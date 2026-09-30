package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/spk/spk-ocular/internal/provider"
)

// Changes. A change goes on the wire at most once. Its failure is
// classified by whether the request may have reached the daemon: before
// the request headers were written (dial, TLS, a dead idle connection) the
// usual classes; after — no answer, the client's wait passed, the caller
// gave up, or a 5xx — ClassUnknown: the daemon may have done it.
// "Written" is conservative (httptrace WroteHeaders: handed to the
// connection, not proven received).

// DefaultStopTimeout is the daemon's stop timeout of a container without
// its own StopTimeout.
const DefaultStopTimeout = 10

// changeBodyBytes bounds a change's JSON body.
const changeBodyBytes = 64 << 10

// sentFlag records that the request's headers were written.
type sentFlag struct{ v atomic.Bool }

func (s *sentFlag) trace(ctx context.Context) context.Context {
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{WroteHeaders: func() { s.v.Store(true) }})
}

// newChange builds a change request; body nil: none. POST and DELETE are
// never replayed by net/http, so a bodyless change is sent with no body
// at all (the GET trick of newRequest would send an empty chunked body,
// which the daemon refuses for a container start).
func (c *Client) newChange(ctx context.Context, method, u string, body any) (*http.Request, error) {
	req, err := c.newRequest(ctx, method, u)
	if err != nil {
		return nil, err
	}
	if body == nil {
		req.Body, req.ContentLength = http.NoBody, 0
		return req, nil
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, &Error{Class: provider.ClassInternal, Message: err.Error(), Err: err}
	}
	if len(b) > changeBodyBytes {
		return nil, &Error{Class: provider.ClassInvalid, Message: fmt.Sprintf("the request body is larger than %d bytes", changeBodyBytes)}
	}
	req.Body = io.NopCloser(bytes.NewReader(b))
	req.GetBody = nil
	req.ContentLength = int64(len(b))
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// unknownError: a change got no usable answer after it may have been sent.
func unknownError(what string, cause error, detail string) *Error {
	return &Error{Class: provider.ClassUnknown, Message: fmt.Sprintf("%s: %s; the Docker Engine may have done it", what, detail), Err: cause}
}

// changeFailed classifies a failed round trip or body read of a change
// made on derived (from parent, bounded by wait).
func changeFailed(parent, derived context.Context, what string, sent bool, err error, wait time.Duration) error {
	if !sent {
		return transportError(parent, derived, err, wait)
	}
	switch {
	case parent.Err() != nil:
		return unknownError(what, parent.Err(), "canceled after the request was sent")
	case derived.Err() != nil || errors.Is(err, context.DeadlineExceeded):
		return unknownError(what, context.DeadlineExceeded, fmt.Sprintf("no answer within %s", wait))
	}
	return unknownError(what, err, "the connection failed after the request was sent: "+unwrapURLError(err).Error())
}

// changeStatus is the error of a non-2xx, non-304 answer to a change: the
// daemon's refusal with its class, or unknown for a 5xx (it may have
// acted before failing).
func changeStatus(what string, code int, status string, body []byte) error {
	e := statusError(code, status, body)
	if code >= 500 {
		e.Class = provider.ClassUnknown
		e.Message = fmt.Sprintf("%s: %s; the Docker Engine may have done it", what, e.Message)
	}
	return e
}

// change sends one change and reads its bounded answer. wait bounds the
// whole exchange. A 304 is notModified, not an error.
func (c *Client) change(parent context.Context, method, path string, q url.Values, body any, wait time.Duration, limit int64, into any) (notModified bool, err error) {
	what := method + " " + path
	ver, err := c.apiVersion(parent)
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(parent, wait)
	defer cancel()
	var sent sentFlag
	req, err := c.newChange(sent.trace(ctx), method, c.endpoint(ver, path, q), body)
	if err != nil {
		return false, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return false, changeFailed(parent, ctx, what, sent.v.Load(), err, wait)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotModified {
		return true, nil
	}
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, c.lim.ErrorBytes))
		return false, changeStatus(what, resp.StatusCode, resp.Status, b)
	}
	if into == nil {
		return false, nil
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return false, changeFailed(parent, ctx, what, true, err, wait)
	}
	if int64(len(b)) > limit {
		return false, unknownError(what, nil, fmt.Sprintf("the answer is larger than %d bytes", limit))
	}
	if err := json.Unmarshal(b, into); err != nil {
		return false, unknownError(what, err, "cannot decode the answer: "+err.Error())
	}
	return false, nil
}

// StartContainer: notModified — it was running already (304).
func (c *Client) StartContainer(ctx context.Context, id string) (notModified bool, err error) {
	return c.change(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/start", nil, nil, c.cfg.RequestTimeout, 0, nil)
}

// StopContainer stops with the container's stop signal, killing after
// timeout seconds (0: at once; −1: never — the daemon waits for the
// process). The client waits timeout + RequestTimeout (for −1 at least
// a minute); past that the stop is unknown (the daemon goes on).
// notModified — it was stopped already (304).
func (c *Client) StopContainer(ctx context.Context, id string, timeout int) (notModified bool, err error) {
	q := url.Values{"t": {strconv.Itoa(timeout)}}
	return c.change(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/stop", q, nil, c.StopWait(timeout), 0, nil)
}

// RestartContainer stops (as StopContainer) and starts the container; a
// stopped one is just started.
func (c *Client) RestartContainer(ctx context.Context, id string, timeout int) error {
	q := url.Values{"t": {strconv.Itoa(timeout)}}
	_, err := c.change(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/restart", q, nil, c.StopWait(timeout), 0, nil)
	return err
}

// RemoveContainer removes a stopped container: never forced, its anonymous
// volumes kept (a running one is refused with a conflict).
func (c *Client) RemoveContainer(ctx context.Context, id string) error {
	_, err := c.change(ctx, http.MethodDelete, "/containers/"+url.PathEscape(id), nil, nil, c.cfg.RequestTimeout, 0, nil)
	return err
}

// StopWait is how long a stop/restart with timeout seconds is waited for.
func (c *Client) StopWait(timeout int) time.Duration {
	if timeout < 0 {
		return max(time.Minute, c.cfg.RequestTimeout)
	}
	return time.Duration(timeout)*time.Second + c.cfg.RequestTimeout
}
