package api

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/spk/spk-ocular/internal/forwards"
	"github.com/spk/spk-ocular/internal/provider"
)

// dialHandle forwards to a local echo server; each stream is a TCP
// connection to it.
type dialHandle struct {
	addr   string
	target core.LiveTarget
	closed atomic.Int32
}

func (h *dialHandle) Describe() core.LiveTarget { return h.target }
func (h *dialHandle) Close()                    { h.closed.Add(1) }
func (h *dialHandle) Connect(context.Context) (provider.Upstream, error) {
	return &dialUpstream{addr: h.addr, done: make(chan struct{})}, nil
}

type dialUpstream struct {
	addr string
	done chan struct{}
	once sync.Once
}

func (u *dialUpstream) Label() string         { return "p:8080" }
func (u *dialUpstream) Done() <-chan struct{} { return u.done }
func (u *dialUpstream) Err() error            { return nil }
func (u *dialUpstream) Close()                { u.once.Do(func() { close(u.done) }) }
func (u *dialUpstream) Open(ctx context.Context) (provider.Stream, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp4", u.addr)
	if err != nil {
		return nil, err
	}
	return dialStream{c.(*net.TCPConn)}, nil
}

type dialStream struct{ *net.TCPConn }

func (dialStream) Result() error { return nil }

type fwdSession struct {
	*fakeSession
	addr    string
	mu      sync.Mutex
	handles []*dialHandle
}

func (f *fwdSession) ForwardInfo(context.Context, core.Ref) (core.ForwardInfo, error) {
	return core.ForwardInfo{Ports: []core.ForwardPort{{Port: 8080, Name: "http", Protocol: "TCP", Scheme: "http", Supported: true}}}, nil
}

func (f *fwdSession) PrepareForward(_ context.Context, ref core.Ref, req provider.ForwardRequest) (provider.ForwardHandle, error) {
	h := &dialHandle{addr: f.addr, target: core.LiveTarget{Provider: "k", Target: f.target, TargetTitle: f.target, ConfigHash: f.hash, Ref: ref, Port: req.Port}}
	f.mu.Lock()
	f.handles = append(f.handles, h)
	f.mu.Unlock()
	return h, nil
}

type fwdOpenable struct {
	*openable
	addr     string
	mu       sync.Mutex
	sessions []*fwdSession
}

func (o *fwdOpenable) Open(ctx context.Context, target string) (provider.Session, error) {
	s, _ := o.openable.Open(ctx, target)
	fs := &fwdSession{fakeSession: s.(*fakeSession), addr: o.addr}
	o.mu.Lock()
	o.sessions = append(o.sessions, fs)
	o.mu.Unlock()
	return fs, nil
}

func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	return ln.Addr().String()
}

func echoThrough(t *testing.T, addr, msg string) {
	t.Helper()
	c, err := net.DialTimeout("tcp4", addr, 5*time.Second)
	require.NoError(t, err)
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_, err = c.Write([]byte(msg))
	require.NoError(t, err)
	buf := make([]byte, len(msg))
	_, err = io.ReadFull(c, buf)
	require.NoError(t, err)
	assert.Equal(t, msg, string(buf))
}

func TestTunnelsOutliveTheirSessionAndEndWithTheApp(t *testing.T) {
	ctx := context.Background()
	k := &fwdOpenable{openable: newOpenable("a", "b"), addr: echoServer(t)}
	s, em := newService(t, k)
	sub, unsub := em.Subscribe()
	defer unsub()
	now := time.Unix(1000, 0)
	s.now = func() time.Time { return now }

	info, err := s.ForwardInfo(ctx, podRef)
	require.NoError(t, err)
	assert.Equal(t, "http", info.Ports[0].Scheme)

	f, err := s.StartForward(ctx, StartForwardRequest{Ref: podRef, Port: 8080, Scheme: "http"})
	require.NoError(t, err)
	assert.Equal(t, forwards.StateReady, f.State)
	assert.Equal(t, 8080, f.Target.Port)
	assert.Equal(t, "http", f.Scheme)
	echoThrough(t, f.Addresses[0], "hello")
	select {
	case <-sub.Wake():
		evs := sub.Drain()
		assert.Contains(t, eventTypes(evs), EventForwardsChanged)
	case <-time.After(3 * time.Second):
		t.Fatal("no forwards_changed")
	}

	// another target, a reconfiguration and the reaper: the tunnel stays
	require.NoError(t, s.SelectTarget(ctx, "k", "b"))
	require.True(t, k.sessions[0].isClosed())
	k.setHash("a", "h2")
	s.revalidateSessions(ctx, "k")
	now = now.Add(sessionIdle + time.Second)
	s.reapIdleSessions()
	echoThrough(t, f.Addresses[0], "after switch")
	list, err := s.ListForwards(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "h1", list[0].Target.ConfigHash, "the snapshot it was opened with")
	assert.EqualValues(t, 2, list[0].Served)
	h := k.sessions[0].handles[0]
	assert.EqualValues(t, 0, h.closed.Load())

	s.Close()
	assert.EqualValues(t, 1, h.closed.Load())
	ln, err := net.Listen("tcp4", f.Addresses[0])
	require.NoError(t, err, "the port is free after exit")
	_ = ln.Close()

	// a late start after the app closed releases its handle and is gone
	_, err = s.StartForward(ctx, StartForwardRequest{Ref: podRef, Port: 8080})
	require.Error(t, err)
}

func eventTypes(evs []events.Event) []string {
	var out []string
	for _, e := range evs {
		out = append(out, e.Type)
	}
	return out
}

func TestStopForwardAndErrors(t *testing.T) {
	ctx := context.Background()
	k := &fwdOpenable{openable: newOpenable("a"), addr: echoServer(t)}
	s, _ := newService(t, k)
	_, err := s.StartForward(ctx, StartForwardRequest{Ref: podRef, Port: 0})
	assert.True(t, IsCoded(err, CodeBadRequest), "%v", err)
	_, err = s.StartForward(ctx, StartForwardRequest{Ref: podRef, Port: 8080, Scheme: "ftp"})
	assert.True(t, IsCoded(err, CodeBadRequest), "%v", err)
	_, err = s.StartForward(ctx, StartForwardRequest{Ref: podRef, Port: 8080, LocalPort: 70000})
	assert.True(t, IsCoded(err, CodeBadRequest), "%v", err)
	assert.Empty(t, k.sessions, "bad requests prepare nothing")

	f, err := s.StartForward(ctx, StartForwardRequest{Ref: podRef, Port: 8080})
	require.NoError(t, err)
	_, err = s.StartForward(ctx, StartForwardRequest{Ref: podRef, Port: 8080, LocalPort: f.LocalPort})
	assert.True(t, IsCoded(err, string(provider.ClassConflict)), "%v", err)
	for _, h := range k.sessions[0].handles[1:] {
		assert.EqualValues(t, 1, h.closed.Load(), "refused handles are released")
	}

	require.NoError(t, s.StopForward(ctx, f.ID))
	assert.True(t, IsCoded(s.StopForward(ctx, f.ID), CodeNotFound))
	list, _ := s.ListForwards(ctx)
	assert.Empty(t, list)

	for range forwards.DefaultLimits.Tunnels {
		_, err := s.StartForward(ctx, StartForwardRequest{Ref: podRef, Port: 8080})
		require.NoError(t, err)
	}
	_, err = s.StartForward(ctx, StartForwardRequest{Ref: podRef, Port: 8080})
	assert.True(t, IsCoded(err, CodeLimit), "%v", err)
}

func TestTunnelsNeedAPortForwarder(t *testing.T) {
	s, _ := newService(t, newOpenable("a"))
	_, err := s.StartForward(context.Background(), StartForwardRequest{Ref: podRef, Port: 80})
	assert.True(t, IsCoded(err, CodeUnsupported), "%v", err)
	_, err = s.ForwardInfo(context.Background(), podRef)
	assert.True(t, IsCoded(err, CodeUnsupported), "%v", err)
}
