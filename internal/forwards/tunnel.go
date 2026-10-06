package forwards

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// tunnel is one forward. Lock order: Manager.mu before tunnel.mu; nothing
// that waits (network, Close of a provider object, wg) runs under either.
type tunnel struct {
	m      *Manager
	id     string
	seq    uint64
	h      provider.ForwardHandle
	target core.LiveTarget
	b      bound
	scheme string
	start  time.Time

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	closed  bool
	state   string
	label   string
	lastErr *Problem
	up      *generation // the live upstream, nil when none
	call    *connectCall
	gen     uint64
	failAt  time.Time
	failErr error
	conns   map[net.Conn]struct{}
	// closeHandle: Stop found a Connect in flight and left closing the
	// handle to it (a provider that ignores cancellation must not wedge
	// Stop or the app's exit).
	closeHandle bool

	served, rejected, failed atomic.Int64
	bytesIn, bytesOut        atomic.Int64
}

// generation is one established upstream; a connection pins the
// generation it opened its stream on, and retiring an old generation never
// touches a newer one.
type generation struct {
	u   provider.Upstream
	gen uint64
}

// connectCall is the single Connect in flight; connections wait for it
// with their own contexts.
type connectCall struct {
	gen  uint64
	done chan struct{}
	g    *generation
	err  error
}

func newTunnel(m *Manager, id string, h provider.ForwardHandle, target core.LiveTarget, b bound, scheme string) *tunnel {
	ctx, cancel := context.WithCancel(context.Background())
	return &tunnel{m: m, id: id, seq: m.seq, h: h, target: target, b: b, scheme: scheme, start: m.opts.Now(),
		ctx: ctx, cancel: cancel, state: StateIdle, conns: map[net.Conn]struct{}{}}
}

func (t *tunnel) info() Info {
	t.mu.Lock()
	defer t.mu.Unlock()
	in := Info{ID: t.id, Target: t.target, LocalPort: t.b.port, Addresses: []string{addr4(t.b.port)},
		IPv6: t.b.ipv6, IPv6Detail: t.b.ipv6Detail, Scheme: t.scheme, State: t.state, Upstream: t.label,
		Conns: len(t.conns), Served: t.served.Load(), Rejected: t.rejected.Load(), Failed: t.failed.Load(),
		BytesIn: t.bytesIn.Load(), BytesOut: t.bytesOut.Load(), Started: t.start}
	if t.b.ipv6 == IPv6OK {
		in.Addresses = append(in.Addresses, addr6(t.b.port))
	}
	if t.lastErr != nil {
		p := *t.lastErr
		in.LastError = &p
	}
	return in
}

func (t *tunnel) serve() {
	for _, ln := range t.b.lns {
		t.wg.Add(1)
		go t.accept(ln)
	}
}

// accept never waits for the upstream: each connection waits in its own
// goroutine. Over a limit a connection is accepted and closed at once.
func (t *tunnel) accept(ln net.Listener) {
	defer t.wg.Done()
	var delay time.Duration
	for {
		c, err := ln.Accept()
		if err != nil {
			if t.ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			// out of descriptors and the like: back off like net/http
			delay = min(max(2*delay, 5*time.Millisecond), time.Second)
			select {
			case <-time.After(delay):
			case <-t.ctx.Done():
				return
			}
			continue
		}
		delay = 0
		if !t.admit(c) {
			_ = c.Close()
			continue
		}
		t.wg.Add(1)
		go t.handle(c)
	}
}

func (t *tunnel) admit(c net.Conn) bool {
	t.mu.Lock()
	switch {
	case t.closed:
		t.mu.Unlock()
		return false
	case len(t.conns) >= t.m.opts.Limits.ConnsInTunnel || !t.m.takeConn():
		t.mu.Unlock()
		t.rejected.Add(1)
		t.m.notify.changed(false)
		return false
	}
	t.conns[c] = struct{}{}
	t.mu.Unlock()
	t.served.Add(1)
	t.m.notify.changed(false)
	return true
}

func (t *tunnel) release(c net.Conn) {
	_ = c.Close()
	t.mu.Lock()
	_, ok := t.conns[c]
	delete(t.conns, c)
	t.mu.Unlock()
	if ok {
		t.m.conns.Add(-1)
	}
	t.m.notify.changed(false)
}

// acquire returns the live upstream or waits for the one Connect in
// flight, starting it if there is none.
func (t *tunnel) acquire(ctx context.Context) (*generation, error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, ErrStopped
	}
	if g := t.up; g != nil && !isDone(g.u.Done()) {
		t.mu.Unlock()
		return g, nil
	}
	started := false
	if t.call == nil {
		if t.failErr != nil && t.m.opts.Now().Sub(t.failAt) < failHold {
			err := t.failErr
			t.mu.Unlock()
			return nil, err
		}
		t.gen++
		t.call = &connectCall{gen: t.gen, done: make(chan struct{})}
		t.state = StateConnecting
		t.m.late.Add(1)
		go t.connect(t.call)
		started = true
	}
	c := t.call
	t.mu.Unlock()
	if started {
		t.m.notify.changed(true)
	}
	select {
	case <-c.done:
		return c.g, c.err
	case <-t.ctx.Done():
		return nil, ErrStopped
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// connect is not waited for by Stop (Stop cancels it); the manager's
// Close waits for it a bounded time.
func (t *tunnel) connect(c *connectCall) {
	defer t.m.late.Done()
	ctx, cancel := context.WithTimeout(t.ctx, connectTimeout)
	u, err := t.h.Connect(ctx)
	cancel()
	t.mu.Lock()
	if t.closed {
		closeHandle := t.closeHandle
		t.mu.Unlock()
		if err == nil {
			u.Close() // a late success after Stop
		}
		if closeHandle {
			t.h.Close()
		}
		c.err = ErrStopped
		close(c.done)
		return
	}
	t.call = nil
	if err != nil {
		t.state = StateError
		t.lastErr = t.m.problem(err)
		t.failAt, t.failErr = t.m.opts.Now(), err
		c.err = err
	} else {
		g := &generation{u: u, gen: c.gen}
		t.up, c.g = g, g
		t.state, t.label, t.failErr = StateReady, u.Label(), nil
		t.wg.Add(1)
		go t.watch(g)
	}
	t.mu.Unlock()
	close(c.done)
	t.m.notify.changed(true)
}

// watch retires a generation when its upstream dies: only new connections
// connect again.
func (t *tunnel) watch(g *generation) {
	defer t.wg.Done()
	select {
	case <-g.u.Done():
		t.retire(g)
	case <-t.ctx.Done():
	}
}

func (t *tunnel) retire(g *generation) {
	t.mu.Lock()
	current := t.up == g
	if current {
		t.up = nil
		t.state = StateIdle
		if err := g.u.Err(); err != nil {
			t.lastErr = t.m.problem(err)
		}
	}
	t.mu.Unlock()
	g.u.Close()
	if current {
		t.m.notify.changed(true)
	}
}

// open gets a stream for a new connection. Only this setup step is
// retried (once): nothing has been read from the local connection yet.
func (t *tunnel) open(ctx context.Context) (*generation, provider.Stream, error) {
	var err error
	for range 2 {
		var g *generation
		if g, err = t.acquire(ctx); err != nil {
			return nil, nil, err
		}
		octx, cancel := context.WithTimeout(ctx, openTimeout)
		var s provider.Stream
		s, err = g.u.Open(octx)
		cancel()
		if err == nil {
			return g, s, nil
		}
		if ctx.Err() != nil {
			return nil, nil, err
		}
		if isDone(g.u.Done()) {
			t.retire(g) // so the retry connects anew
		}
	}
	return nil, nil, err
}

func (t *tunnel) handle(c net.Conn) {
	defer t.wg.Done()
	defer t.release(c)
	ctx, cancel := context.WithCancel(t.ctx)
	defer cancel()
	g, s, err := t.open(ctx)
	if err != nil {
		if !errors.Is(err, ErrStopped) && t.ctx.Err() == nil {
			t.fail(err)
		}
		return
	}
	up := make(chan struct{})
	down := make(chan struct{})
	var ending atomic.Bool // the handler closed c itself: not the client's reset
	go func() {            // local → remote
		defer close(up)
		switch err := t.copy(s, c, &t.bytesOut); {
		case err == nil:
			_ = s.CloseWrite() // the request is sent; the answer is still read
		case !ending.Load():
			_ = s.Close() // the client went away: do not wait for the remote
		}
	}()
	go func() { // remote → local
		defer close(down)
		_ = t.copy(c, s, &t.bytesIn)
	}()
	upstreamDied := false
	select {
	case <-down:
	case <-g.u.Done():
		upstreamDied = true
	case <-t.ctx.Done():
	}
	// Like kubectl: when the remote side is done, so is the connection.
	ending.Store(true)
	_ = c.Close()
	var res error
	if !upstreamDied && t.ctx.Err() == nil {
		res = s.Result()
	}
	_ = s.Close()
	<-up
	<-down
	if res != nil {
		t.fail(res)
	}
}

// fail records a connection's error; the tunnel and its other
// connections carry on.
func (t *tunnel) fail(err error) {
	p := t.m.problem(err)
	t.mu.Lock()
	// Publish the count and its error as one snapshot for info().
	t.failed.Add(1)
	t.lastErr = p
	t.mu.Unlock()
	t.m.notify.changed(true)
}

var bufs = sync.Pool{New: func() any { b := make([]byte, copyBuffer); return &b }}

// copy moves bytes and counts them; nil on a clean EOF from src.
func (t *tunnel) copy(dst io.Writer, src io.Reader, n *atomic.Int64) error {
	bp := bufs.Get().(*[]byte)
	defer bufs.Put(bp)
	buf := *bp
	for {
		k, rerr := src.Read(buf)
		if k > 0 {
			if _, err := dst.Write(buf[:k]); err != nil {
				return err
			}
			n.Add(int64(k))
			t.m.notify.changed(false)
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}

// stop: closed first, then listeners, the pending Connect, local
// connections and the upstream; then wait for every goroutine.
func (t *tunnel) stop() {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		t.wg.Wait()
		return
	}
	t.closed = true
	t.closeHandle = t.call != nil
	closeHandle := !t.closeHandle
	g := t.up
	t.up = nil
	conns := make([]net.Conn, 0, len(t.conns))
	for c := range t.conns {
		conns = append(conns, c)
	}
	t.mu.Unlock()
	t.b.close()
	t.cancel()
	for _, c := range conns {
		_ = c.Close()
	}
	if g != nil {
		g.u.Close()
	}
	t.wg.Wait()
	if closeHandle {
		t.h.Close()
	}
}

func isDone(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func inUse(err error) bool  { return errors.Is(err, syscall.EADDRINUSE) }
func denied(err error) bool { return errors.Is(err, syscall.EACCES) }
