package synthetic

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

var (
	_ provider.Execer        = (*session)(nil)
	_ provider.PortForwarder = (*session)(nil)
)

// live counts what terminals and tunnels hold, for leak checks
// (/api/_test/stats).
type live struct {
	execs     atomic.Int64 // commands running
	handles   atomic.Int64 // exec and forward handles not closed
	upstreams atomic.Int64
	streams   atomic.Int64

	srvMu   sync.Mutex
	servers map[string]net.Listener // "object:port" → its HTTP server
}

// LiveStats: running commands, open handles, upstreams and streams.
func (p *Provider) LiveStats() map[string]int {
	return map[string]int{
		"syn_execs":     int(p.live.execs.Load()),
		"syn_handles":   int(p.live.handles.Load()),
		"syn_upstreams": int(p.live.upstreams.Load()),
		"syn_streams":   int(p.live.streams.Load()),
	}
}

// Instances (pods) of each object; each has one channel, "main".
func instances(object string) []string {
	var out []string
	for _, src := range objects[object] {
		out = append(out, strings.TrimSuffix(src, "/main"))
	}
	return out
}

func (s *session) ExecInfo(_ context.Context, ref core.Ref) (core.ExecInfo, error) {
	insts := instances(ref.Name)
	if len(insts) == 0 {
		return core.ExecInfo{}, &provider.Error{Class: provider.ClassNotFound, Message: ref.Name}
	}
	info := core.ExecInfo{DefaultInstance: insts[0]}
	for _, id := range insts {
		info.Instances = append(info.Instances, core.ExecInstance{
			ID: id, Title: id, Ready: true, DefaultChannel: "main",
			Channels: []core.ExecChannel{{ID: "main", Title: "main", Running: true}},
		})
	}
	return info, nil
}

func (s *session) PrepareExec(_ context.Context, ref core.Ref, req provider.ExecRequest) (provider.ExecHandle, error) {
	insts := instances(ref.Name)
	if len(insts) == 0 {
		return nil, &provider.Error{Class: provider.ClassNotFound, Message: ref.Name}
	}
	inst := req.Instance
	if inst == "" {
		inst = insts[0]
	}
	found := false
	for _, id := range insts {
		found = found || id == inst
	}
	if !found {
		return nil, &provider.Error{Class: provider.ClassNotFound, Message: "instance " + inst}
	}
	if req.Channel != "" && req.Channel != "main" {
		return nil, &provider.Error{Class: provider.ClassNotFound, Message: "channel " + req.Channel}
	}
	return s.p.newExecHandle(s.ref(ref.Name), inst, req.Command), nil
}

func (p *Provider) liveTarget(ref core.Ref) core.LiveTarget {
	return core.LiveTarget{Provider: ID, Target: Target, TargetTitle: Target, Endpoint: "synthetic.local", ConfigHash: "1", Ref: ref}
}

// execHandle runs the echo terminal (see run).
type execHandle struct {
	p      *Provider
	ref    core.Ref
	inst   string
	argv   []string
	closed atomic.Bool
}

func (p *Provider) newExecHandle(ref core.Ref, inst string, argv []string) *execHandle {
	p.live.handles.Add(1)
	return &execHandle{p: p, ref: ref, inst: inst, argv: argv}
}

func (h *execHandle) Describe() core.LiveTarget {
	t := h.p.liveTarget(h.ref)
	t.Instance, t.Channel, t.Command = h.inst, "main", h.argv
	return t
}

func (h *execHandle) Again() (provider.ExecHandle, error) {
	return h.p.newExecHandle(h.ref, h.inst, h.argv), nil
}

func (h *execHandle) Close() {
	if h.closed.CompareAndSwap(false, true) {
		h.p.live.handles.Add(-1)
	}
}

// Run is an echo terminal with a tiny line discipline: typed bytes are
// echoed, Enter runs the line, ^C drops the line (or stops flood), ^D on an
// empty line exits 0. Commands: "flood N" prints N numbered lines,
// "exit N" ends with code N, anything else is answered "you said: …". Each
// size change prints "size CxR". A non-empty argv runs as one line and the
// command then ends (0 unless it was an exit).
func (h *execHandle) Run(ctx context.Context, t provider.Terminal) (provider.ExitStatus, error) {
	h.p.live.execs.Add(1)
	defer h.p.live.execs.Add(-1)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := &lockedWriter{w: t.Stdout}

	go func() {
		for sz := t.Sizes.Next(); sz != nil && ctx.Err() == nil; sz = t.Sizes.Next() {
			if _, err := fmt.Fprintf(out, "\r\nsize %dx%d\r\n", sz.Cols, sz.Rows); err != nil {
				return
			}
		}
	}()
	input := make(chan byte, 4096)
	go func() {
		defer close(input)
		buf := make([]byte, 1024)
		for {
			n, err := t.Stdin.Read(buf)
			for _, c := range buf[:n] {
				select {
				case input <- c:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	sh := &echoShell{out: out, input: input}
	if len(h.argv) > 0 {
		code, exited, err := sh.exec(ctx, strings.Join(h.argv, " "))
		if err != nil {
			return provider.ExitStatus{}, err
		}
		if !exited {
			code = 0
		}
		return provider.ExitStatus{Code: code, Known: true}, nil
	}
	if _, err := io.WriteString(out, "synthetic terminal on "+h.inst+"\r\n$ "); err != nil {
		return provider.ExitStatus{}, err
	}
	var line []byte
	for {
		var c byte
		var ok bool
		select {
		case <-ctx.Done():
			return provider.ExitStatus{}, ctx.Err()
		case c, ok = <-input:
		}
		if !ok { // stdin ended without an exit: the connection is gone
			return provider.ExitStatus{}, errors.New("the terminal's input ended")
		}
		var echo string
		switch c {
		case 0x03:
			line, echo = line[:0], "^C\r\n$ "
		case 0x04:
			if len(line) == 0 {
				_, _ = io.WriteString(out, "exit\r\n")
				return provider.ExitStatus{Code: 0, Known: true}, nil
			}
		case 0x7f, 0x08:
			if len(line) > 0 {
				line, echo = line[:len(line)-1], "\b \b"
			}
		case '\r', '\n':
			if _, err := io.WriteString(out, "\r\n"); err != nil {
				return provider.ExitStatus{}, err
			}
			code, exited, err := sh.exec(ctx, string(line))
			if err != nil {
				return provider.ExitStatus{}, err
			}
			if exited {
				return provider.ExitStatus{Code: code, Known: true}, nil
			}
			line, echo = line[:0], "$ "
		default:
			if c >= 0x20 || c == '\t' {
				line = append(line, c)
				echo = string([]byte{c})
			}
		}
		if echo != "" {
			if _, err := io.WriteString(out, echo); err != nil {
				return provider.ExitStatus{}, err
			}
		}
	}
}

type echoShell struct {
	out   io.Writer
	input <-chan byte
}

// exec runs one line; exited reports an "exit".
func (sh *echoShell) exec(ctx context.Context, line string) (code int, exited bool, err error) {
	f := strings.Fields(line)
	switch {
	case len(f) == 0:
		return 0, false, nil
	case f[0] == "exit":
		if len(f) > 1 {
			code, _ = strconv.Atoi(f[1])
		}
		return code, true, nil
	case f[0] == "flood" && len(f) > 1:
		n, _ := strconv.Atoi(f[1])
		return 0, false, sh.flood(ctx, n)
	default:
		_, err = io.WriteString(sh.out, "you said: "+line+"\r\n")
		return 0, false, err
	}
}

// flood prints n lines in batches, stopping at ^C (other input typed
// meanwhile is dropped, like a busy shell's would be for this test).
func (sh *echoShell) flood(ctx context.Context, n int) error {
	var b strings.Builder
	for i := 1; i <= n; {
		for {
			select {
			case c, ok := <-sh.input:
				if !ok {
					return errors.New("the terminal's input ended")
				}
				if c == 0x03 {
					_, err := io.WriteString(sh.out, "^C\r\n")
					return err
				}
				continue
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			break
		}
		b.Reset()
		for j := 0; j < 64 && i <= n; j++ {
			fmt.Fprintf(&b, "flood line %d of %d\r\n", i, n)
			i++
		}
		if _, err := io.WriteString(sh.out, b.String()); err != nil {
			return err
		}
	}
	_, err := io.WriteString(sh.out, "flood done\r\n")
	return err
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// Ports of each object; each TCP port is served by an in-process HTTP
// server answering "hello from <object>:<port> <path>".
var ports = map[string][]core.ForwardPort{
	"api": {
		{Port: 80, Name: "http", Protocol: "TCP", Note: "→ 8080", Scheme: "http", Supported: true},
		{Port: 53, Name: "dns", Protocol: "UDP", Reason: "only TCP ports can be forwarded"},
	},
	"workers": {
		{Port: 9090, Name: "metrics", Protocol: "TCP", Supported: true},
	},
}

func (s *session) ForwardInfo(_ context.Context, ref core.Ref) (core.ForwardInfo, error) {
	ps, ok := ports[ref.Name]
	if !ok {
		return core.ForwardInfo{}, &provider.Error{Class: provider.ClassNotFound, Message: ref.Name}
	}
	return core.ForwardInfo{Ports: append([]core.ForwardPort(nil), ps...)}, nil
}

func (s *session) PrepareForward(_ context.Context, ref core.Ref, req provider.ForwardRequest) (provider.ForwardHandle, error) {
	ps, ok := ports[ref.Name]
	if !ok {
		return nil, &provider.Error{Class: provider.ClassNotFound, Message: ref.Name}
	}
	for _, p := range ps {
		if p.Port == req.Port && p.Supported {
			s.p.live.handles.Add(1)
			return &fwdHandle{p: s.p, ref: s.ref(ref.Name), port: req.Port}, nil
		}
	}
	return nil, &provider.Error{Class: provider.ClassNotFound, Message: fmt.Sprintf("%s has no TCP port %d", ref.Name, req.Port)}
}

type fwdHandle struct {
	p      *Provider
	ref    core.Ref
	port   int
	closed atomic.Bool
}

func (h *fwdHandle) Describe() core.LiveTarget {
	t := h.p.liveTarget(h.ref)
	t.Port = h.port
	return t
}

func (h *fwdHandle) Close() {
	if h.closed.CompareAndSwap(false, true) {
		h.p.live.handles.Add(-1)
	}
}

func (h *fwdHandle) Connect(ctx context.Context) (provider.Upstream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	addr, err := h.p.server(h.ref.Name, h.port)
	if err != nil {
		return nil, &provider.Error{Class: provider.ClassInternal, Message: err.Error()}
	}
	inst := instances(h.ref.Name)[0]
	h.p.live.upstreams.Add(1)
	uctx, cancel := context.WithCancel(context.Background())
	return &upstream{p: h.p, addr: addr, label: fmt.Sprintf("%s:%d", inst, h.port), ctx: uctx, cancel: cancel}, nil
}

// server starts (once) the HTTP server behind object:port.
func (p *Provider) server(object string, port int) (string, error) {
	key := fmt.Sprintf("%s:%d", object, port)
	p.live.srvMu.Lock()
	defer p.live.srvMu.Unlock()
	if ln, ok := p.live.servers[key]; ok {
		return ln.Addr().String(), nil
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	if p.live.servers == nil {
		p.live.servers = map[string]net.Listener{}
	}
	p.live.servers[key] = ln
	srv := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = fmt.Fprintf(w, "hello from %s %s\n", key, r.URL.Path)
		}),
	}
	go func() { _ = srv.Serve(ln) }()
	return ln.Addr().String(), nil
}

type upstream struct {
	p     *Provider
	addr  string
	label string
	once  sync.Once
	// ctx is cancelled by Close.
	ctx    context.Context
	cancel context.CancelFunc
}

func (u *upstream) Label() string         { return u.label }
func (u *upstream) Done() <-chan struct{} { return u.ctx.Done() }
func (u *upstream) Err() error            { return nil }
func (u *upstream) Close() {
	u.once.Do(func() {
		u.cancel()
		u.p.live.upstreams.Add(-1)
	})
}

func (u *upstream) Open(ctx context.Context) (provider.Stream, error) {
	if u.ctx.Err() != nil {
		return nil, errors.New("the upstream is closed")
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp4", u.addr)
	if err != nil {
		return nil, err
	}
	u.p.live.streams.Add(1)
	s := &stream{TCPConn: c.(*net.TCPConn), p: u.p}
	// A closed upstream resets its streams, like a dropped connection.
	s.mu.Lock()
	s.stop = context.AfterFunc(u.ctx, func() { _ = s.Close() })
	s.mu.Unlock()
	return s, nil
}

type stream struct {
	*net.TCPConn
	p    *Provider
	once sync.Once
	mu   sync.Mutex
	stop func() bool
}

func (s *stream) Close() error {
	var err error
	s.once.Do(func() {
		s.mu.Lock()
		stop := s.stop
		s.mu.Unlock()
		if stop != nil {
			stop()
		}
		s.p.live.streams.Add(-1)
		err = s.TCPConn.Close()
	})
	return err
}

func (s *stream) Result() error { return nil }
