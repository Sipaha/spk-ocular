package forwards

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// backend is the "pod": a loopback TCP server. Echo mode echoes until the
// client half-closes; answer mode reads the whole request (until
// half-close) and then answers "got N".
type backend struct {
	ln     net.Listener
	answer bool
	wg     sync.WaitGroup
}

func newBackend(t *testing.T, answer bool) *backend {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b := &backend{ln: ln, answer: answer}
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			b.wg.Add(1)
			go func() {
				defer b.wg.Done()
				defer c.Close()
				if b.answer {
					n, _ := io.Copy(io.Discard, c)
					_, _ = fmt.Fprintf(c, "got %d", n)
					return
				}
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	t.Cleanup(func() { _ = ln.Close(); b.wg.Wait() })
	return b
}

// fakeHandle is a provider's ForwardHandle over a backend; hooks shape
// Connect and Open.
type fakeHandle struct {
	be     *backend
	remote int

	// connect runs inside Connect (may block, may ignore ctx: a late
	// success); an error fails the connect.
	connect func(ctx context.Context, n int) error
	// open runs inside Open for stream n (1-based per handle); returning
	// errPortRefused makes that stream a port error.
	open func(ctx context.Context, u *fakeUpstream, n int) error

	// resultDelay: the error stream reports this long after the data
	// ended (a slow kubelet); 0 = at once.
	resultDelay time.Duration

	connects atomic.Int32
	streamN  atomic.Int32
	live     atomic.Int32 // open streams
	closed   atomic.Int32

	mu  sync.Mutex
	ups []*fakeUpstream
}

var errPortRefused = &provider.Error{Class: provider.ClassUnavailable, Message: "connection refused on port"}

func (h *fakeHandle) Describe() core.LiveTarget {
	return core.LiveTarget{Provider: "fake", Target: "t", TargetTitle: "t", Port: h.remote, Ref: core.Ref{Provider: "fake", Target: "t", Kind: "pods", Name: "web"}}
}

func (h *fakeHandle) Connect(ctx context.Context) (provider.Upstream, error) {
	n := int(h.connects.Add(1))
	if h.connect != nil {
		if err := h.connect(ctx, n); err != nil {
			return nil, err
		}
	}
	u := &fakeUpstream{h: h, n: n, done: make(chan struct{}), streams: map[*fakeStream]struct{}{}}
	h.mu.Lock()
	h.ups = append(h.ups, u)
	h.mu.Unlock()
	return u, nil
}

func (h *fakeHandle) Close() { h.closed.Add(1) }

func (h *fakeHandle) upstream(i int) *fakeUpstream {
	h.mu.Lock()
	defer h.mu.Unlock()
	if i >= len(h.ups) {
		return nil
	}
	return h.ups[i]
}

type fakeUpstream struct {
	h    *fakeHandle
	n    int
	done chan struct{}

	mu      sync.Mutex
	err     error
	dead    bool
	closes  int
	streams map[*fakeStream]struct{}
}

func (u *fakeUpstream) Label() string         { return fmt.Sprintf("pod-%d:%d", u.n, u.h.remote) }
func (u *fakeUpstream) Done() <-chan struct{} { return u.done }
func (u *fakeUpstream) Err() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.err
}

// kill ends the upstream as a broken connection would: its streams fail.
func (u *fakeUpstream) kill(err error) {
	u.mu.Lock()
	if u.dead {
		u.mu.Unlock()
		return
	}
	u.dead, u.err = true, err
	ss := u.streams
	u.streams = map[*fakeStream]struct{}{}
	u.mu.Unlock()
	close(u.done)
	for s := range ss {
		s.reset()
	}
}

func (u *fakeUpstream) Close() {
	u.mu.Lock()
	u.closes++
	u.mu.Unlock()
	u.kill(nil)
}

func (u *fakeUpstream) closeCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.closes
}

func (u *fakeUpstream) Open(ctx context.Context) (provider.Stream, error) {
	if isDone(u.done) {
		return nil, errors.New("upstream is closed")
	}
	n := int(u.h.streamN.Add(1))
	s := &fakeStream{u: u, result: make(chan error, 1), gone: make(chan struct{})}
	if u.h.open != nil {
		switch err := u.h.open(ctx, u, n); {
		case errors.Is(err, errPortRefused):
			s.refused = true
			s.result <- err
		case err != nil:
			return nil, err
		}
	}
	if !s.refused {
		var d net.Dialer
		c, err := d.DialContext(ctx, "tcp4", u.h.be.ln.Addr().String())
		if err != nil {
			return nil, err
		}
		s.c = c.(*net.TCPConn)
	}
	u.mu.Lock()
	if u.dead {
		u.mu.Unlock()
		u.h.live.Add(1) // reset takes it back
		s.reset()
		return nil, errors.New("upstream is closed")
	}
	u.streams[s] = struct{}{}
	u.mu.Unlock()
	u.h.live.Add(1)
	return s, nil
}

// fakeStream carries one connection to the backend; a refused one reads
// EOF at once and reports its error through Result.
type fakeStream struct {
	u       *fakeUpstream
	c       *net.TCPConn
	refused bool
	result  chan error
	gone    chan struct{}
	once    sync.Once
}

func (s *fakeStream) Read(p []byte) (int, error) {
	if s.refused {
		return 0, io.EOF
	}
	return s.c.Read(p)
}

func (s *fakeStream) Write(p []byte) (int, error) {
	if s.refused {
		return len(p), nil
	}
	return s.c.Write(p)
}

func (s *fakeStream) CloseWrite() error {
	if s.refused {
		return nil
	}
	return s.c.CloseWrite()
}

func (s *fakeStream) reset() {
	s.once.Do(func() {
		close(s.gone)
		if s.c != nil {
			_ = s.c.Close()
		}
		s.u.h.live.Add(-1)
	})
}

// drop: the remote side resets this stream only.
func (s *fakeStream) drop() { s.reset() }

func (s *fakeStream) Close() error {
	s.u.mu.Lock()
	delete(s.u.streams, s)
	s.u.mu.Unlock()
	s.reset()
	return nil
}

// Result: a clean stream's error stream closes empty when the data ends.
func (s *fakeStream) Result() error {
	if !s.refused && s.u.h.resultDelay == 0 {
		return nil
	}
	if !s.refused {
		s.result <- nil
	}
	delay := time.After(s.u.h.resultDelay)
	select {
	case <-delay:
	case <-s.gone:
		return nil
	case <-s.u.done:
		return nil
	}
	select {
	case err := <-s.result:
		return err
	case <-s.gone:
		return nil
	case <-s.u.done:
		return nil
	case <-time.After(time.Second):
		return nil
	}
}

func (u *fakeUpstream) anyStream() *fakeStream {
	u.mu.Lock()
	defer u.mu.Unlock()
	for s := range u.streams {
		return s
	}
	return nil
}
