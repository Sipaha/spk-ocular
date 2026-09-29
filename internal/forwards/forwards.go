// Package forwards runs port-forward tunnels: local loopback listeners whose
// connections are carried to a provider's upstream (k8s: a pod's port over
// one multiplexed connection). It knows nothing about pods: a provider
// hands out a ForwardHandle, the manager owns it from then on.
//
// Tunnels belong to the app, not to a session: they outlive another target
// being selected and end on Stop or when the app closes.
package forwards

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// Limits bound what tunnels can hold.
type Limits struct {
	Tunnels       int // open tunnels (starting ones included)
	ConnsInTunnel int // live connections of one tunnel
	ConnsInApp    int // live connections of all tunnels
}

var DefaultLimits = Limits{Tunnels: 32, ConnsInTunnel: 64, ConnsInApp: 256}

const (
	copyBuffer = 32 << 10
	// connectTimeout and openTimeout cap a provider's Connect and Open
	// (they have their own, shorter, deadlines for the network parts).
	connectTimeout = 30 * time.Second
	openTimeout    = 30 * time.Second
	// failHold: right after a failed Connect, new connections fail with its
	// error instead of connecting again (a client retrying in a loop must
	// not hammer the cluster).
	failHold = time.Second
)

type Options struct {
	// OnChange is called when the list changed: at once for a state
	// change, at most once per EventInterval for counters. Never under a
	// lock; from any goroutine.
	OnChange      func()
	EventInterval time.Duration // default 1 s
	Limits        Limits        // zero = DefaultLimits
	// Listen binds a listener (tests inject failures); default net.Listen.
	Listen func(network, addr string) (net.Listener, error)
	Now    func() time.Time
}

var (
	// ErrLimit: too many tunnels.
	ErrLimit = errors.New("too many tunnels; stop one first")
	// ErrClosed: the app is closing.
	ErrClosed = errors.New("the app is closing")
	// ErrStopped: the tunnel was stopped meanwhile.
	ErrStopped = errors.New("the tunnel was stopped")
	// ErrNotFound: no such tunnel.
	ErrNotFound = errors.New("no such tunnel")
)

// Tunnel states.
const (
	StateConnecting = "connecting" // choosing an instance and connecting
	StateReady      = "ready"      // connected
	StateIdle       = "idle"       // not connected; the next connection connects
	StateError      = "error"      // the last connect failed; the next connection retries
)

// IPv6 listener statuses (the same port on ::1, best effort).
const (
	IPv6OK          = "ok"
	IPv6Busy        = "busy"        // another program listens there
	IPv6Unavailable = "unavailable" // no IPv6 loopback
)

// Problem is a classified error with its time.
type Problem struct {
	Class   string    `json:"class"`
	Message string    `json:"message"`
	At      time.Time `json:"at"`
}

// Info is a tunnel as the UI shows it.
type Info struct {
	ID     string          `json:"id"`
	Target core.LiveTarget `json:"target"`
	// LocalPort and Addresses: where it listens, actual addresses only
	// ("localhost" may resolve to another program's ::1).
	LocalPort  int      `json:"localPort"`
	Addresses  []string `json:"addresses"`
	IPv6       string   `json:"ipv6"`
	IPv6Detail string   `json:"ipv6Detail,omitempty"`
	// Scheme: "http" / "https" when the UI may offer "Open".
	Scheme string `json:"scheme,omitempty"`
	State  string `json:"state"`
	// Upstream: the current (or last) instance and port.
	Upstream string `json:"upstream,omitempty"`
	Conns    int    `json:"conns"`
	Served   int64  `json:"served"`
	Rejected int64  `json:"rejected"` // over the connection limits
	Failed   int64  `json:"failed"`
	BytesIn  int64  `json:"bytesIn"` // remote → local
	BytesOut int64  `json:"bytesOut"`
	// LastError: the last connect or connection error.
	LastError *Problem  `json:"lastError,omitempty"`
	Started   time.Time `json:"started"`
}

// StartRequest: LocalPort 0 = the remote port when it is ≥ 1024 and free,
// otherwise any free port; an explicit port that is taken is a conflict.
type StartRequest struct {
	LocalPort int    `json:"localPort"`
	Scheme    string `json:"scheme,omitempty"`
}

type Manager struct {
	opts   Options
	notify *notifier

	mu       sync.Mutex
	tunnels  map[string]*tunnel
	starting int
	seq      uint64
	closed   bool

	conns atomic.Int64 // live connections of all tunnels
	// late: Connects in flight, which Stop cancels but does not wait for.
	late sync.WaitGroup
}

// lateWait bounds how long Close waits for cancelled Connects to return.
const lateWait = 2 * time.Second

func NewManager(o Options) *Manager {
	if o.Limits == (Limits{}) {
		o.Limits = DefaultLimits
	}
	if o.EventInterval <= 0 {
		o.EventInterval = time.Second
	}
	if o.Listen == nil {
		o.Listen = net.Listen
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Manager{opts: o, tunnels: map[string]*tunnel{}, notify: newNotifier(o.OnChange, o.EventInterval, o.Now)}
}

// Start binds the local listeners, connects once and starts serving. It
// owns h from the call on: every failure closes it. A failed first
// connect rolls the tunnel back (the error is the caller's to show).
func (m *Manager) Start(ctx context.Context, h provider.ForwardHandle, req StartRequest) (Info, error) {
	if req.LocalPort < 0 || req.LocalPort > 65535 {
		h.Close()
		return Info{}, &provider.Error{Class: provider.ClassInvalid, Message: "the local port must be 1..65535"}
	}
	switch req.Scheme {
	case "", "http", "https":
	default:
		h.Close()
		return Info{}, &provider.Error{Class: provider.ClassInvalid, Message: "the scheme must be http or https"}
	}
	m.mu.Lock()
	switch {
	case m.closed:
		m.mu.Unlock()
		h.Close()
		return Info{}, ErrClosed
	case len(m.tunnels)+m.starting >= m.opts.Limits.Tunnels:
		m.mu.Unlock()
		h.Close()
		return Info{}, ErrLimit
	}
	m.starting++
	m.mu.Unlock()

	target := h.Describe()
	b, err := m.bind(target.Port, req.LocalPort)
	m.mu.Lock()
	m.starting--
	if err == nil && m.closed {
		err = ErrClosed
	}
	if err != nil {
		m.mu.Unlock()
		b.close()
		h.Close()
		return Info{}, err
	}
	m.seq++
	t := newTunnel(m, "f"+strconv.FormatUint(m.seq, 10), h, target, b, req.Scheme)
	m.tunnels[t.id] = t
	m.mu.Unlock()

	t.serve()
	m.notify.changed(true)
	if _, err := t.acquire(ctx); err != nil {
		m.remove(t)
		if errors.Is(err, ErrStopped) {
			m.mu.Lock()
			closed := m.closed
			m.mu.Unlock()
			if closed {
				err = ErrClosed
			}
		}
		return Info{}, err
	}
	return t.info(), nil
}

// Stop ends a tunnel: its listeners, connections and upstream.
func (m *Manager) Stop(id string) error {
	m.mu.Lock()
	t := m.tunnels[id]
	m.mu.Unlock()
	if t == nil {
		return ErrNotFound
	}
	m.remove(t)
	return nil
}

func (m *Manager) remove(t *tunnel) {
	m.mu.Lock()
	if m.tunnels[t.id] == t {
		delete(m.tunnels, t.id)
	}
	m.mu.Unlock()
	t.stop()
	m.notify.changed(true)
}

// List: the tunnels in the order they were started.
func (m *Manager) List() []Info {
	m.mu.Lock()
	ts := make([]*tunnel, 0, len(m.tunnels))
	for _, t := range m.tunnels {
		ts = append(ts, t)
	}
	m.mu.Unlock()
	sort.Slice(ts, func(i, j int) bool { return ts[i].seq < ts[j].seq })
	out := make([]Info, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.info())
	}
	return out
}

// Len is the number of tunnels; Conns the live connections (tests, stats).
func (m *Manager) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.tunnels)
}

func (m *Manager) Conns() int { return int(m.conns.Load()) }

// Close stops every tunnel and waits for them; later Starts fail with
// ErrClosed (and close their handle).
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	ts := make([]*tunnel, 0, len(m.tunnels))
	for id, t := range m.tunnels {
		ts = append(ts, t)
		delete(m.tunnels, id)
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, t := range ts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t.stop()
		}()
	}
	wg.Wait()
	done := make(chan struct{})
	go func() { m.late.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(lateWait):
	}
	m.notify.close()
}

// takeConn reserves an app-wide connection slot.
func (m *Manager) takeConn() bool {
	for {
		n := m.conns.Load()
		if n >= int64(m.opts.Limits.ConnsInApp) {
			return false
		}
		if m.conns.CompareAndSwap(n, n+1) {
			return true
		}
	}
}

// problem classifies err for Info.LastError.
func (m *Manager) problem(err error) *Problem {
	p := &Problem{Class: string(provider.ClassUnavailable), Message: err.Error(), At: m.opts.Now()}
	var pe *provider.Error
	switch {
	case errors.As(err, &pe):
		p.Class, p.Message = string(pe.Class), pe.Message
	case errors.Is(err, context.DeadlineExceeded):
		p.Message = "timed out"
	}
	return p
}

// notifier calls fn at once for state changes and at most once per
// interval for counter changes (the latest state, not a timer reset per
// byte).
type notifier struct {
	fn       func()
	interval time.Duration
	now      func() time.Time

	mu     sync.Mutex
	last   time.Time
	timer  *time.Timer
	closed bool
}

func newNotifier(fn func(), interval time.Duration, now func() time.Time) *notifier {
	return &notifier{fn: fn, interval: interval, now: now}
}

func (n *notifier) changed(immediate bool) {
	if n.fn == nil {
		return
	}
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return
	}
	now := n.now()
	if !immediate {
		if n.timer != nil {
			n.mu.Unlock()
			return
		}
		if wait := n.last.Add(n.interval).Sub(now); wait > 0 {
			n.timer = time.AfterFunc(wait, n.fire)
			n.mu.Unlock()
			return
		}
	} else if n.timer != nil {
		n.timer.Stop()
		n.timer = nil
	}
	n.last = now
	n.mu.Unlock()
	n.fn()
}

func (n *notifier) fire() {
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return
	}
	n.timer = nil
	n.last = n.now()
	n.mu.Unlock()
	n.fn()
}

func (n *notifier) close() {
	n.mu.Lock()
	n.closed = true
	if n.timer != nil {
		n.timer.Stop()
		n.timer = nil
	}
	n.mu.Unlock()
}

func addr4(port int) string { return net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) }
func addr6(port int) string { return net.JoinHostPort("::1", strconv.Itoa(port)) }

// bound is a tunnel's listeners.
type bound struct {
	lns        []net.Listener
	port       int
	ipv6       string
	ipv6Detail string
}

func (b bound) close() {
	for _, ln := range b.lns {
		_ = ln.Close()
	}
}

// bind listens on 127.0.0.1 (atomically: bind, never check-then-bind) and
// then on ::1 with the same port, best effort.
func (m *Manager) bind(remote, local int) (bound, error) {
	var b bound
	listen := m.opts.Listen
	var ln net.Listener
	var err error
	switch {
	case local > 0:
		ln, err = listen("tcp4", addr4(local))
		if err != nil {
			return b, bindError(local, err)
		}
	default:
		if remote >= 1024 && remote <= 65535 {
			ln, err = listen("tcp4", addr4(remote))
			if err != nil && !inUse(err) && !denied(err) {
				return b, bindError(remote, err)
			}
		}
		if ln == nil {
			if ln, err = listen("tcp4", addr4(0)); err != nil {
				return b, bindError(0, err)
			}
		}
	}
	b.lns = append(b.lns, ln)
	b.port = ln.Addr().(*net.TCPAddr).Port
	ln6, err := listen("tcp6", addr6(b.port))
	switch {
	case err == nil:
		b.lns = append(b.lns, ln6)
		b.ipv6 = IPv6OK
	case inUse(err):
		b.ipv6 = IPv6Busy
		b.ipv6Detail = fmt.Sprintf("another program listens on [::1]:%d; use 127.0.0.1", b.port)
	default:
		b.ipv6 = IPv6Unavailable
		b.ipv6Detail = err.Error()
	}
	return b, nil
}

func bindError(port int, err error) error {
	switch {
	case inUse(err):
		return &provider.Error{Class: provider.ClassConflict, Message: fmt.Sprintf("local port %d is in use", port)}
	case denied(err):
		return &provider.Error{Class: provider.ClassForbidden, Message: fmt.Sprintf("local port %d needs privileges; choose one ≥ 1024", port)}
	}
	return &provider.Error{Class: provider.ClassInternal, Message: fmt.Sprintf("cannot listen on local port %d: %v", port, err)}
}
