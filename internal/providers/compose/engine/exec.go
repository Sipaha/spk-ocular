package engine

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/provider"
)

// ConsoleSize of a TTY.
type ConsoleSize struct{ Rows, Cols uint16 }

func (s ConsoleSize) zero() bool { return s.Rows == 0 || s.Cols == 0 }

// ExecConfig of a new exec: stdin, stdout and stderr are always attached.
type ExecConfig struct {
	Cmd []string
	Tty bool
	// Size: the initial TTY size (sent on create where the API has it,
	// and set by StartExec after the attach for every API).
	Size ConsoleSize
}

// ExecInspect is an exec's state. ExitCode is nil while it has none (not
// started, running, or the daemon did not record it) — never a false 0.
// A command that could not be started ends with 126/127 and Pid 0.
type ExecInspect struct {
	ID          string `json:"ID"`
	ContainerID string `json:"ContainerID"`
	Running     bool   `json:"Running"`
	ExitCode    *int   `json:"ExitCode"`
	Pid         int    `json:"Pid"`
}

// consoleSizeVersion is the first API version whose exec create/start take
// ConsoleSize (older daemons ignore it).
var consoleSizeVersion = version{1, 42}

func (c *Client) speaks(ctx context.Context, v version) (bool, error) {
	ver, err := c.apiVersion(ctx)
	if err != nil {
		return false, err
	}
	have, _ := parseVersion(ver)
	return !have.less(v), nil
}

// CreateExec creates (does not start) an exec in a running container and
// returns its id. A paused or stopped container is refused (conflict).
func (c *Client) CreateExec(ctx context.Context, containerID string, cfg ExecConfig) (string, error) {
	body := map[string]any{"AttachStdin": true, "AttachStdout": true, "AttachStderr": true, "Tty": cfg.Tty, "Cmd": cfg.Cmd}
	if ok, err := c.speaks(ctx, consoleSizeVersion); err != nil {
		return "", err
	} else if ok && !cfg.Size.zero() {
		body["ConsoleSize"] = []uint16{cfg.Size.Rows, cfg.Size.Cols}
	}
	var v struct {
		ID string `json:"Id"`
	}
	if _, err := c.change(ctx, http.MethodPost, "/containers/"+url.PathEscape(containerID)+"/exec", nil, body, c.cfg.RequestTimeout, c.lim.ErrorBytes, &v); err != nil {
		return "", err
	}
	if v.ID == "" {
		return "", &Error{Class: provider.ClassUnknown, Message: "the Docker Engine created an exec without an id"}
	}
	return v.ID, nil
}

func (c *Client) InspectExec(ctx context.Context, execID string) (ExecInspect, error) {
	var v ExecInspect
	err := c.getJSON(ctx, "/exec/"+url.PathEscape(execID)+"/json", nil, c.lim.InspectBytes, &v)
	return v, err
}

// ResizeExec sets a running exec's TTY size. Setting a size is idempotent.
func (c *Client) ResizeExec(ctx context.Context, execID string, cols, rows uint16) error {
	q := url.Values{"w": {strconv.Itoa(int(cols))}, "h": {strconv.Itoa(int(rows))}}
	_, err := c.change(ctx, http.MethodPost, "/exec/"+url.PathEscape(execID)+"/resize", q, nil, c.cfg.RequestTimeout, 0, nil)
	return err
}

// ExecConn is a started exec's hijacked stream: stdin written, the output
// read (raw with a TTY; stdcopy frames without). It lives until Close or
// the end of the context StartExec was given; Client.Close does not end it.
type ExecConn struct {
	rwc   io.ReadWriteCloser
	stop  func() bool // unregisters the lifetime callback
	close func() error
}

func (x *ExecConn) Read(p []byte) (int, error)  { return x.rwc.Read(p) }
func (x *ExecConn) Write(p []byte) (int, error) { return x.rwc.Write(p) }

// Close closes the connection (unblocking Read and Write). The process in
// the container is not killed by it: a TTY shell gets its hangup, others
// may go on — the Engine has no way to stop an exec.
func (x *ExecConn) Close() error {
	x.stop()
	return x.close()
}

// closer closes rwc and ends the request's context once, however often
// and from wherever it is called (the lifetime callback may run before
// StartExec returns).
func closer(rwc io.Closer, cancel context.CancelFunc) func() error {
	var once sync.Once
	var err error
	return func() error {
		once.Do(func() {
			err = rwc.Close()
			cancel()
		})
		return err
	}
}

// testHookSwitched runs after the switch, before the lifetime callback is
// registered (tests end the context there).
var testHookSwitched func()

// StartExec starts an exec attached: POST /exec/{id}/start upgraded to a
// raw stream (101). ctx is the stream's lifetime — its end closes the
// stream at any step; the switch is waited for HeaderTimeout. Once the
// request may have been sent, a failure is unknown (the command may be
// running: the daemon runs it on its own after accepting the start) — never
// retried. The size goes with the start where the API has it (1.42+);
// SetInitialSize sets it for every API once the stream is taken.
func (c *Client) StartExec(ctx context.Context, execID string, tty bool, size ConsoleSize) (*ExecConn, error) {
	const what = "starting the exec"
	ver, err := c.apiVersion(ctx)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"Detach": false, "Tty": tty}
	if have, _ := parseVersion(ver); !have.less(consoleSizeVersion) && !size.zero() {
		body["ConsoleSize"] = []uint16{size.Rows, size.Cols}
	}
	rctx, cancel := context.WithCancel(ctx)
	hdr := time.AfterFunc(c.cfg.HeaderTimeout, cancel)
	var sent sentFlag
	req, err := c.newChange(sent.trace(rctx), http.MethodPost, c.endpoint(ver, "/exec/"+url.PathEscape(execID)+"/start", nil), body)
	if err != nil {
		hdr.Stop()
		cancel()
		return nil, err
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "tcp")
	resp, err := c.hc.Do(req)
	timedOut := !hdr.Stop()
	if err != nil {
		// classified before the cleanup cancel: rctx ended only if the
		// header timer fired or ctx ended
		e := changeFailed(ctx, rctx, what, sent.v.Load(), err, c.cfg.HeaderTimeout)
		cancel()
		return nil, e
	}
	fail := func(e error) (*ExecConn, error) {
		_ = resp.Body.Close()
		cancel()
		return nil, e
	}
	if timedOut || ctx.Err() != nil {
		if ctx.Err() != nil {
			return fail(unknownError(what, ctx.Err(), "canceled after the request was sent"))
		}
		return fail(unknownError(what, context.DeadlineExceeded, "no answer within "+c.cfg.HeaderTimeout.String()))
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		if resp.StatusCode/100 == 2 {
			return fail(unknownError(what, nil, "the Docker Engine answered "+resp.Status+" without switching to a stream"))
		}
		t := time.AfterFunc(c.cfg.HeaderTimeout, cancel)
		b, _ := io.ReadAll(io.LimitReader(resp.Body, c.lim.ErrorBytes))
		t.Stop()
		return fail(changeStatus(what, resp.StatusCode, resp.Status, b))
	}
	rwc, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		return fail(unknownError(what, nil, "the switched stream is not writable"))
	}
	cl := closer(rwc, cancel)
	if testHookSwitched != nil {
		testHookSwitched()
	}
	return &ExecConn{rwc: rwc, close: cl, stop: context.AfterFunc(ctx, func() { _ = cl() })}, nil
}

// InitialSizeBudget bounds SetInitialSize as a whole.
const InitialSizeBudget = 2 * time.Second

// SetInitialSize sets a started exec's first size (API < 1.42 ignores the
// size given with the start). The process may not have its TTY right
// after the switch: a transient refusal is tried again a few times within
// InitialSizeBudget, like the docker CLI does — a resize of the same exec
// to the same size is idempotent, the one exception to "nothing is
// retried". Other refusals are final. The error says the size was not set
// (the TTY keeps the daemon's until the next resize).
func (c *Client) SetInitialSize(ctx context.Context, execID string, size ConsoleSize) error {
	ctx, cancel := context.WithTimeout(ctx, InitialSizeBudget)
	defer cancel()
	var err error
	for i := range 5 {
		if err = c.ResizeExec(ctx, execID, size.Cols, size.Rows); err == nil {
			return nil
		}
		switch ClassOf(err) {
		case provider.ClassConflict, provider.ClassUnavailable, provider.ClassUnknown:
		default:
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(time.Duration(i+1) * 10 * time.Millisecond):
		}
	}
	return err
}
