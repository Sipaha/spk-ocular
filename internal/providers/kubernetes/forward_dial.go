package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	pfconst "k8s.io/apimachinery/pkg/util/portforward"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/websocket"
	"k8s.io/klog/v2"
	streamhttp "k8s.io/streaming/pkg/httpstream"
	streamspdy "k8s.io/streaming/pkg/httpstream/spdy"

	"github.com/spk/spk-ocular/internal/provider"
)

// Port-forward connections: negotiated under a context (client-go's
// dialers take none: the tunnelling one uses context.Background(), the
// SPDY one http.NewRequest), with a deadline that is dropped once the
// connection is up; then one multiplexed SPDY connection (over a
// WebSocket tunnel, or plain) carries a pair of streams per local
// connection, as client-go's handleConnection does — except that a stream
// error is that connection's alone and every wait is bounded.

const (
	pfProtocol = portforward.PortForwardProtocolV1Name
	// negotiateTimeout bounds dial + TLS + upgrade of one attempt.
	negotiateTimeout = 15 * time.Second
	// pfPingPeriod: SPDY pings keep an idle connection observably alive;
	// pfDeadAfter without a byte from the peer (pings answered included)
	// declares it dead — spdystream's own pings only log failures.
	pfPingPeriod = 10 * time.Second
	pfDeadAfter  = 3 * pfPingPeriod
	// maxErrorReport bounds what the error stream may say.
	maxErrorReport = 4 << 10
	// resultWait bounds waiting for the error stream once the data ended.
	resultWait = 5 * time.Second
)

// forwardDialer opens port-forward connections to a pod (tests replace
// the pieces).
type forwardDialer struct {
	c         *conn
	deadAfter time.Duration
	// ws disables the WebSocket attempt when false (tests of the SPDY path).
	ws bool
}

// handshakeCtx is ctx plus a trace hook that closes the connection a
// handshake dials when ctx ends — until release is called (the connection
// is established and belongs to its user; the negotiation deadline must
// not kill it). release reports false when ctx already closed it.
func handshakeCtx(ctx context.Context) (context.Context, func() bool) {
	var mu sync.Mutex
	var stops []func() bool
	released := false
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			c := info.Conn
			mu.Lock()
			defer mu.Unlock()
			if released {
				return
			}
			stops = append(stops, context.AfterFunc(ctx, func() { _ = c.Close() }))
		},
	})
	return ctx, func() bool {
		mu.Lock()
		defer mu.Unlock()
		released = true
		ok := true
		for _, stop := range stops {
			ok = stop() && ok
		}
		return ok
	}
}

// dial negotiates a port-forward connection to pod: the WebSocket tunnel
// first, SPDY when that upgrade is refused before anything was sent
// (kubectl's predicate).
// It says which path was used ("websocket" or "spdy").
func (d *forwardDialer) dial(ctx context.Context, ns, pod string) (streamhttp.Connection, string, error) {
	u, err := d.c.podURL(ns, pod, "portforward", nil)
	if err != nil {
		return nil, "", err
	}
	ctx, cancel := context.WithTimeout(ctx, negotiateTimeout)
	defer cancel()
	if d.ws {
		c, err := d.dialWS(ctx, u)
		if err == nil || !shouldFallback(err) || ctx.Err() != nil {
			return c, "websocket", negotiateError(ctx, err)
		}
	}
	c, err := d.dialSPDY(ctx, u)
	return c, "spdy", negotiateError(ctx, err)
}

func negotiateError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &provider.Error{Class: provider.ClassUnavailable, Message: fmt.Sprintf("no answer from the API server within %s", negotiateTimeout)}
		}
		return ctx.Err()
	}
	return err
}

func (d *forwardDialer) dialWS(ctx context.Context, u *url.URL) (streamhttp.Connection, error) {
	rt, holder, err := websocket.RoundTripperFor(d.c.cfg)
	if err != nil {
		return nil, err
	}
	hctx, release := handshakeCtx(ctx)
	req, err := http.NewRequestWithContext(hctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	// The tunnel carries SPDY: no WebSocket port-forward channel prefixes.
	ws, err := websocket.Negotiate(rt, holder, req, pfconst.WebsocketsSPDYTunnelingPortForwardV1)
	if !release() {
		if ws != nil {
			_ = ws.Close()
		}
		return nil, fmt.Errorf("upgrade cancelled: %w", context.Cause(ctx))
	}
	if err != nil {
		return nil, err
	}
	if p := ws.Subprotocol(); p != pfconst.WebsocketsSPDYTunnelingPortForwardV1 {
		_ = ws.Close()
		return nil, &streamhttp.UpgradeFailureError{Cause: fmt.Errorf("the server chose protocol %q", p)}
	}
	tc := portforward.NewTunnelingConnectionWithLogger(klog.Background(), ws)
	return streamspdy.NewClientConnectionWithPings(watchdog(tc, d.deadAfter), pfPingPeriod)
}

func (d *forwardDialer) dialSPDY(ctx context.Context, u *url.URL) (streamhttp.Connection, error) {
	rt, up, err := spdyUpgrade(d.c.cfg)
	if err != nil {
		return nil, err
	}
	up.wrap = func(c net.Conn) net.Conn { return watchdog(c, d.deadAfter) }
	up.pingPeriod = pfPingPeriod
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add(streamhttp.HeaderProtocolVersion, pfProtocol)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	c, err := up.NewConnection(resp)
	if err != nil {
		return nil, err
	}
	if p := resp.Header.Get(streamhttp.HeaderProtocolVersion); p != pfProtocol {
		_ = c.Close()
		return nil, fmt.Errorf("unable to negotiate port-forward: the server answered protocol %q", p)
	}
	return c, nil
}

// watchdog closes c when nothing has been read from it for deadAfter:
// the peer answers pings every ping period, so silence means it is gone
// (a half-open TCP connection would otherwise look alive for minutes).
func watchdog(c net.Conn, deadAfter time.Duration) net.Conn {
	return &watchedConn{Conn: c, deadAfter: deadAfter}
}

type watchedConn struct {
	net.Conn
	deadAfter time.Duration
}

func (w *watchedConn) Read(p []byte) (int, error) {
	if err := w.SetReadDeadline(time.Now().Add(w.deadAfter)); err != nil {
		return 0, err
	}
	n, err := w.Conn.Read(p)
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		_ = w.Close()
		return n, fmt.Errorf("no answer from the pod's connection for %s: %w", w.deadAfter, err)
	}
	return n, err
}

// pfUpstream is one port-forward connection to a pod.
type pfUpstream struct {
	conn  streamhttp.Connection
	label string
	port  int
	reqID atomic.Int64
	done  chan struct{}
	// via: "websocket" or "spdy" (tests, diagnostics).
	via string
	// alive, after a connection failed, checks the pod is still the one
	// this upstream serves: kubelet keeps the connection of a deleted pod
	// open and fails every stream, so without it a Service tunnel would
	// never move on to another pod. One GET per failure, never polling.
	alive func(ctx context.Context) error

	mu     sync.Mutex
	err    error
	closed bool
}

func newPFUpstream(c streamhttp.Connection, label string, port int) *pfUpstream {
	u := &pfUpstream{conn: c, label: label, port: port, done: make(chan struct{})}
	go func() {
		<-c.CloseChan()
		u.mu.Lock()
		if !u.closed {
			u.err = &provider.Error{Class: provider.ClassUnavailable, Message: "the connection to " + label + " was lost"}
		}
		u.mu.Unlock()
		close(u.done)
	}()
	return u
}

func (u *pfUpstream) Label() string         { return u.label }
func (u *pfUpstream) Done() <-chan struct{} { return u.done }
func (u *pfUpstream) Err() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.err
}

func (u *pfUpstream) Close() {
	u.mu.Lock()
	if u.closed {
		u.mu.Unlock()
		return
	}
	u.closed = true
	u.mu.Unlock()
	_ = u.conn.Close()
}

// retire ends the upstream because its pod is gone (Err says so).
func (u *pfUpstream) retire(why error) {
	u.mu.Lock()
	if u.closed {
		u.mu.Unlock()
		return
	}
	u.closed, u.err = true, why
	u.mu.Unlock()
	_ = u.conn.Close()
}

// checkAfterFailure retires the upstream if its pod is no longer usable.
func (u *pfUpstream) checkAfterFailure() {
	if u.alive == nil || isClosed(u.done) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), getTimeout)
	defer cancel()
	if err := u.alive(ctx); err != nil {
		var pe *provider.Error
		if errors.As(err, &pe) && pe.Class == provider.ClassGone { // not a transient API error
			u.retire(err)
		}
	}
}

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// explain shortens kubelet's usual refusal (a netns path and sandbox id
// in a line of 300 characters) to what the user needs.
func (u *pfUpstream) explain(msg string) string {
	if strings.Contains(msg, "connect: connection refused") {
		return fmt.Sprintf("nothing listens on port %d in %s (connection refused)", u.port, u.pod())
	}
	return msg
}

func (u *pfUpstream) pod() string {
	name, _, _ := strings.Cut(u.label, ":")
	return name
}

// createStream is CreateStream under ctx (spdystream waits up to 30 s for
// the reply and takes no context); a stream that arrives late is reset.
func (u *pfUpstream) createStream(ctx context.Context, h http.Header) (streamhttp.Stream, error) {
	type res struct {
		s   streamhttp.Stream
		err error
	}
	ch := make(chan res, 1)
	go func() {
		s, err := u.conn.CreateStream(h)
		ch <- res{s, err}
	}()
	select {
	case r := <-ch:
		return r.s, r.err
	case <-ctx.Done():
		go func() {
			if r := <-ch; r.s != nil {
				_ = r.s.Reset()
				u.conn.RemoveStreams(r.s)
			}
		}()
		return nil, ctx.Err()
	case <-u.done:
		return nil, errors.New("the connection to the pod is closed")
	}
}

// Open creates the error stream (half-closed: we never write to it) and
// the data stream of one forwarded connection, with a fresh request id.
func (u *pfUpstream) Open(ctx context.Context) (provider.Stream, error) {
	h := http.Header{}
	h.Set(corev1.StreamType, corev1.StreamTypeError)
	h.Set(corev1.PortHeader, strconv.Itoa(u.port))
	h.Set(corev1.PortForwardRequestIDHeader, strconv.FormatInt(u.reqID.Add(1)-1, 10))
	es, err := u.createStream(ctx, h)
	if err != nil {
		return nil, fmt.Errorf("cannot open a stream to %s: %w", u.label, err)
	}
	_ = es.Close()
	s := &pfStream{u: u, errs: es, report: make(chan error, 1)}
	go s.readReport()
	h.Set(corev1.StreamType, corev1.StreamTypeData)
	ds, err := u.createStream(ctx, h)
	if err != nil {
		s.reset()
		if ctx.Err() == nil {
			u.checkAfterFailure()
		}
		return nil, fmt.Errorf("cannot open a stream to %s: %w", u.label, err)
	}
	s.data = ds
	return s, nil
}

type pfStream struct {
	u      *pfUpstream
	errs   streamhttp.Stream
	data   streamhttp.Stream
	report chan error
	once   sync.Once
}

// readReport reads what the error stream says (≤ maxErrorReport).
func (s *pfStream) readReport() {
	msg, err := io.ReadAll(io.LimitReader(s.errs, maxErrorReport))
	switch {
	case len(msg) > 0:
		s.report <- &provider.Error{Class: provider.ClassUnavailable, Message: s.u.explain(strings.TrimSpace(string(msg)))}
	case err != nil:
		s.report <- fmt.Errorf("the error stream of %s failed: %w", s.u.label, err)
	default:
		s.report <- nil
	}
}

func (s *pfStream) Read(p []byte) (int, error)  { return s.data.Read(p) }
func (s *pfStream) Write(p []byte) (int, error) { return s.data.Write(p) }
func (s *pfStream) CloseWrite() error           { return s.data.Close() }

// Result: the data is reset first (unsent data must not hold the error
// stream back, as client-go notes), then the report is awaited briefly.
func (s *pfStream) Result() error {
	_ = s.data.Reset()
	t := time.NewTimer(resultWait)
	defer t.Stop()
	var err error
	select {
	case err = <-s.report:
	case <-s.u.done:
	case <-t.C:
	}
	if err != nil {
		s.u.checkAfterFailure()
	}
	return err
}

func (s *pfStream) Close() error {
	s.reset()
	return nil
}

func (s *pfStream) reset() {
	s.once.Do(func() {
		_ = s.errs.Reset()
		if s.data != nil {
			_ = s.data.Reset()
			s.u.conn.RemoveStreams(s.errs, s.data)
		} else {
			s.u.conn.RemoveStreams(s.errs)
		}
	})
}
