package forwards

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/provider"
)

func newManager(t *testing.T, o Options) *Manager {
	t.Helper()
	m := NewManager(o)
	t.Cleanup(m.Close)
	return m
}

// freePort returns a port that was free a moment ago.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	p := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	return p
}

func dial(t *testing.T, in Info) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp4", in.Addresses[0], 5*time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// roundTrip writes msg and reads it back (echo backend).
func roundTrip(c net.Conn, msg string) error {
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write([]byte(msg)); err != nil {
		return err
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(c, buf); err != nil {
		return err
	}
	if string(buf) != msg {
		return fmt.Errorf("got %q, want %q", buf, msg)
	}
	return nil
}

// settled: no goroutines are left over from the test's work.
func settled(t *testing.T, before int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<20)
			t.Fatalf("goroutines: %d, before: %d\n%s", runtime.NumGoroutine(), before, buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func start(t *testing.T, m *Manager, h *fakeHandle, local int) Info {
	t.Helper()
	in, err := m.Start(context.Background(), h, StartRequest{LocalPort: local})
	require.NoError(t, err)
	return in
}

func TestTunnelCarriesConnectionsOverOneUpstream(t *testing.T) {
	be := newBackend(t, false)
	m := newManager(t, Options{})
	h := &fakeHandle{be: be, remote: 80}
	in := start(t, m, h, 0)
	assert.Equal(t, StateReady, in.State)
	assert.Equal(t, "pod-1:80", in.Upstream)
	assert.Equal(t, "127.0.0.1:"+strconv.Itoa(in.LocalPort), in.Addresses[0])
	for i := range 3 {
		require.NoError(t, roundTrip(dial(t, in), fmt.Sprintf("hello %d", i)))
	}
	assert.EqualValues(t, 1, h.connects.Load())
}

func TestHalfCloseSendsTheRequestAndReadsTheAnswer(t *testing.T) {
	be := newBackend(t, true)
	m := newManager(t, Options{})
	in := start(t, m, &fakeHandle{be: be, remote: 80}, 0)
	c := dial(t, in)
	_, err := c.Write([]byte("GET / HTTP/1.0\r\n\r\n"))
	require.NoError(t, err)
	require.NoError(t, c.(*net.TCPConn).CloseWrite())
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	got, err := io.ReadAll(c)
	require.NoError(t, err)
	assert.Equal(t, "got 18", string(got))
	require.Eventually(t, func() bool { l := m.List(); return l[0].BytesIn == 6 && l[0].BytesOut == 18 }, 3*time.Second, 10*time.Millisecond)
}

func TestExplicitPortInUseIsAConflict(t *testing.T) {
	busy, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port
	m := newManager(t, Options{})
	h := &fakeHandle{be: newBackend(t, false), remote: port}
	_, err = m.Start(context.Background(), h, StartRequest{LocalPort: port})
	var pe *provider.Error
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, provider.ClassConflict, pe.Class)
	assert.EqualValues(t, 1, h.closed.Load(), "the handle is closed")
	assert.EqualValues(t, 0, h.connects.Load(), "nothing connects")
	assert.Equal(t, 0, m.Len())
}

func TestAutoPortTakesTheRemoteOneOrAnyFree(t *testing.T) {
	be := newBackend(t, false)
	m := newManager(t, Options{})

	free := freePort(t)
	in := start(t, m, &fakeHandle{be: be, remote: free}, 0)
	assert.Equal(t, free, in.LocalPort, "the remote port when it is free")

	busy, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	defer busy.Close()
	taken := busy.Addr().(*net.TCPAddr).Port
	in = start(t, m, &fakeHandle{be: be, remote: taken}, 0)
	assert.NotEqual(t, taken, in.LocalPort, "another port when it is taken")
	require.NoError(t, roundTrip(dial(t, in), "x"))

	in = start(t, m, &fakeHandle{be: be, remote: 80}, 0)
	assert.NotEqual(t, 80, in.LocalPort, "a privileged remote port is not tried")
}

func TestIPv6TakenByAnotherProgramIsAStatus(t *testing.T) {
	foreign, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skip("no IPv6 loopback:", err)
	}
	defer foreign.Close()
	port := foreign.Addr().(*net.TCPAddr).Port
	m := newManager(t, Options{})
	in := start(t, m, &fakeHandle{be: newBackend(t, false), remote: 80}, port)
	assert.Equal(t, IPv6Busy, in.IPv6)
	assert.Contains(t, in.IPv6Detail, "another program")
	assert.Equal(t, []string{"127.0.0.1:" + strconv.Itoa(port)}, in.Addresses, "only our own address is shown")

	in = start(t, m, &fakeHandle{be: newBackend(t, false), remote: 80}, 0)
	assert.Equal(t, IPv6OK, in.IPv6)
	require.Len(t, in.Addresses, 2)
	c, err := net.DialTimeout("tcp6", in.Addresses[1], 5*time.Second)
	require.NoError(t, err)
	defer c.Close()
	require.NoError(t, roundTrip(c, "over ::1"))
}

func TestFailedFirstConnectRollsBack(t *testing.T) {
	m := newManager(t, Options{})
	port := freePort(t)
	h := &fakeHandle{be: newBackend(t, false), remote: 80, connect: func(context.Context, int) error {
		return &provider.Error{Class: provider.ClassForbidden, Message: "needs pods/portforward"}
	}}
	_, err := m.Start(context.Background(), h, StartRequest{LocalPort: port})
	var pe *provider.Error
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, provider.ClassForbidden, pe.Class)
	assert.Equal(t, 0, m.Len())
	assert.EqualValues(t, 1, h.closed.Load())
	ln, err := net.Listen("tcp4", addr4(port))
	require.NoError(t, err, "the port is free again")
	_ = ln.Close()
}

func TestSixtyFourParallelConnections(t *testing.T) {
	be := newBackend(t, false)
	m := newManager(t, Options{})
	h := &fakeHandle{be: be, remote: 80}
	in := start(t, m, h, 0)
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	gate := make(chan struct{})
	for i := range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := net.DialTimeout("tcp4", in.Addresses[0], 5*time.Second)
			if err != nil {
				errs <- err
				return
			}
			defer c.Close()
			<-gate // all 64 are open at once
			errs <- roundTrip(c, strconv.Itoa(i))
		}()
	}
	require.Eventually(t, func() bool { return m.Conns() == 64 }, 5*time.Second, 5*time.Millisecond)
	close(gate)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	assert.EqualValues(t, 0, m.List()[0].Rejected)
	assert.EqualValues(t, 1, h.connects.Load())
}

func TestThousandsOfSequentialConnectionsLeaveNothingBehind(t *testing.T) {
	be := newBackend(t, false)
	m := newManager(t, Options{})
	h := &fakeHandle{be: be, remote: 80}
	in := start(t, m, h, 0)
	before := runtime.NumGoroutine()
	for i := range 2000 {
		// a closed connection is released once its remote side ends:
		// keep a fast loop under the tunnel's limit
		for m.Conns() >= 32 {
			time.Sleep(time.Millisecond)
		}
		c, err := net.DialTimeout("tcp4", in.Addresses[0], 5*time.Second)
		require.NoError(t, err)
		require.NoError(t, roundTrip(c, "x"), "connection %d", i)
		require.NoError(t, c.Close())
	}
	require.Eventually(t, func() bool { return m.Conns() == 0 && h.live.Load() == 0 }, 5*time.Second, 10*time.Millisecond,
		"conns %d, streams %d", m.Conns(), h.live.Load())
	settled(t, before)
	assert.EqualValues(t, 2000, m.List()[0].Served)
}

func TestLimits(t *testing.T) {
	be := newBackend(t, false)
	m := newManager(t, Options{Limits: Limits{Tunnels: 2, ConnsInTunnel: 2, ConnsInApp: 3}})
	a := start(t, m, &fakeHandle{be: be, remote: 80}, 0)
	b := start(t, m, &fakeHandle{be: be, remote: 80}, 0)
	h := &fakeHandle{be: be, remote: 80}
	_, err := m.Start(context.Background(), h, StartRequest{})
	require.ErrorIs(t, err, ErrLimit)
	assert.EqualValues(t, 1, h.closed.Load())

	// two in a, a third one there is closed at once
	a1, a2 := dial(t, a), dial(t, a)
	require.NoError(t, roundTrip(a1, "1"))
	require.NoError(t, roundTrip(a2, "2"))
	assert.Error(t, roundTrip(dial(t, a), "3"), "over the tunnel's limit")
	// one in b fits the app's limit of 3, the next does not
	b1 := dial(t, b)
	require.NoError(t, roundTrip(b1, "4"))
	assert.Error(t, roundTrip(dial(t, b), "5"), "over the app's limit")
	l := m.List()
	assert.EqualValues(t, 1, l[0].Rejected)
	assert.EqualValues(t, 1, l[1].Rejected)
	// closing one frees a slot
	require.NoError(t, a1.Close())
	require.Eventually(t, func() bool { return m.Conns() == 2 }, 3*time.Second, 5*time.Millisecond)
	require.NoError(t, roundTrip(dial(t, b), "6"))
}

func TestUpstreamDeathWithNewConnectionsConnectsOnce(t *testing.T) {
	be := newBackend(t, false)
	m := newManager(t, Options{})
	release := make(chan struct{})
	h := &fakeHandle{be: be, remote: 80}
	h.connect = func(ctx context.Context, n int) error {
		if n == 2 {
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	in := start(t, m, h, 0)
	old := dial(t, in)
	require.NoError(t, roundTrip(old, "old"))
	h.upstream(0).kill(errors.New("connection reset by peer"))

	// the old connection ends with its upstream: it is not replayed
	_ = old.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err := old.Read(make([]byte, 1))
	require.Error(t, err)

	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := net.DialTimeout("tcp4", in.Addresses[0], 5*time.Second)
			if err != nil {
				errs <- err
				return
			}
			defer c.Close()
			errs <- roundTrip(c, "new")
		}()
	}
	require.Eventually(t, func() bool { return m.Conns() == 10 }, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, StateConnecting, m.List()[0].State)
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	assert.EqualValues(t, 2, h.connects.Load(), "one connect for all of them")
	l := m.List()[0]
	assert.Equal(t, StateReady, l.State)
	assert.Equal(t, "pod-2:80", l.Upstream)
	require.NotNil(t, l.LastError)
	assert.Contains(t, l.LastError.Message, "reset by peer")
}

func TestOldGenerationNeverTouchesTheNewOne(t *testing.T) {
	be := newBackend(t, false)
	m := newManager(t, Options{})
	h := &fakeHandle{be: be, remote: 80}
	in := start(t, m, h, 0)
	g1 := h.upstream(0)
	g1.kill(errors.New("gone"))
	c := dial(t, in)
	require.NoError(t, roundTrip(c, "on gen 2"))
	g2 := h.upstream(1)
	require.NotNil(t, g2)

	// late cleanup of generation 1 (its watcher, a second retire)
	tn := m.tunnels[in.ID]
	tn.retire(&generation{u: g1, gen: 1})
	assert.Equal(t, StateReady, m.List()[0].State)
	assert.Equal(t, 0, g2.closeCount())
	require.NoError(t, roundTrip(c, "still on gen 2"))
	require.NoError(t, roundTrip(dial(t, in), "a new one on gen 2"))
	assert.EqualValues(t, 2, h.connects.Load())
}

func TestSetupIsRetriedOnceOnANewUpstream(t *testing.T) {
	be := newBackend(t, false)
	m := newManager(t, Options{})
	h := &fakeHandle{be: be, remote: 80}
	h.open = func(_ context.Context, u *fakeUpstream, n int) error {
		if n == 1 { // the upstream breaks while the first stream opens
			go u.kill(errors.New("broken"))
			<-u.done
			return errors.New("stream creation failed")
		}
		return nil
	}
	in := start(t, m, h, 0)
	require.NoError(t, roundTrip(dial(t, in), "retried"))
	assert.EqualValues(t, 2, h.connects.Load())
	assert.EqualValues(t, 0, m.List()[0].Failed)
}

func TestSetupFailingTwiceFailsTheConnectionOnly(t *testing.T) {
	be := newBackend(t, false)
	m := newManager(t, Options{})
	h := &fakeHandle{be: be, remote: 80}
	var fails atomic.Int32
	fails.Store(2)
	h.open = func(context.Context, *fakeUpstream, int) error {
		if fails.Add(-1) >= 0 {
			return errors.New("stream creation timed out")
		}
		return nil
	}
	in := start(t, m, h, 0)
	c := dial(t, in)
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err := c.Read(make([]byte, 1))
	require.Error(t, err, "closed")
	require.Eventually(t, func() bool { return m.List()[0].Failed == 1 }, 3*time.Second, 5*time.Millisecond)
	assert.Contains(t, m.List()[0].LastError.Message, "timed out")
	require.NoError(t, roundTrip(dial(t, in), "the next one works"))
	assert.EqualValues(t, 1, h.connects.Load(), "the live upstream is kept")
}

func TestPortErrorFailsOneConnectionAndTheNeighbourLives(t *testing.T) {
	be := newBackend(t, false)
	m := newManager(t, Options{})
	h := &fakeHandle{be: be, remote: 80}
	h.open = func(_ context.Context, _ *fakeUpstream, n int) error {
		if n == 2 {
			return errPortRefused
		}
		return nil
	}
	in := start(t, m, h, 0)
	first := dial(t, in)
	require.NoError(t, roundTrip(first, "first"))
	second := dial(t, in)
	_ = second.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err := second.Read(make([]byte, 1))
	require.Error(t, err)
	require.Eventually(t, func() bool { return m.List()[0].Failed == 1 }, 3*time.Second, 5*time.Millisecond)
	l := m.List()[0]
	assert.Equal(t, "connection refused on port", l.LastError.Message)
	assert.Equal(t, StateReady, l.State)
	require.NoError(t, roundTrip(first, "first again"))
	assert.EqualValues(t, 1, h.connects.Load())
}

func TestADroppedStreamEndsOnlyItsConnection(t *testing.T) {
	be := newBackend(t, false)
	m := newManager(t, Options{})
	h := &fakeHandle{be: be, remote: 80}
	in := start(t, m, h, 0)
	first := dial(t, in)
	require.NoError(t, roundTrip(first, "first"))
	s1 := h.upstream(0).anyStream()
	require.NotNil(t, s1)
	second := dial(t, in)
	require.NoError(t, roundTrip(second, "second"))
	s1.drop()
	_ = first.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err := first.Read(make([]byte, 1))
	require.Error(t, err)
	require.NoError(t, roundTrip(second, "second lives"))
}

func TestStopDuringConnect(t *testing.T) {
	be := newBackend(t, false)
	m := newManager(t, Options{})
	before := runtime.NumGoroutine()
	entered := make(chan struct{})
	h := &fakeHandle{be: be, remote: 80, connect: func(ctx context.Context, _ int) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}}
	port := freePort(t)
	res := make(chan error, 1)
	go func() {
		_, err := m.Start(context.Background(), h, StartRequest{LocalPort: port})
		res <- err
	}()
	<-entered
	l := m.List()
	require.Len(t, l, 1)
	assert.Equal(t, StateConnecting, l[0].State)
	require.NoError(t, m.Stop(l[0].ID))
	require.ErrorIs(t, <-res, ErrStopped)
	require.Eventually(t, func() bool { return h.closed.Load() == 1 }, 3*time.Second, 5*time.Millisecond, "the cancelled connect closes the handle")
	ln, err := net.Listen("tcp4", addr4(port))
	require.NoError(t, err, "the port is free")
	_ = ln.Close()
	settled(t, before)
}

func TestLateSuccessfulConnectAfterStopIsClosed(t *testing.T) {
	be := newBackend(t, false)
	m := newManager(t, Options{})
	entered, release := make(chan struct{}), make(chan struct{})
	h := &fakeHandle{be: be, remote: 80, connect: func(context.Context, int) error {
		close(entered)
		<-release // ignores cancellation: a late success
		return nil
	}}
	res := make(chan error, 1)
	go func() {
		_, err := m.Start(context.Background(), h, StartRequest{})
		res <- err
	}()
	<-entered
	stopped := make(chan struct{})
	go func() { _ = m.Stop(m.List()[0].ID); close(stopped) }()
	require.ErrorIs(t, <-res, ErrStopped)
	<-stopped // Stop does not wait for a connect that ignores cancellation
	assert.EqualValues(t, 0, h.closed.Load(), "the handle is still in use by the connect")
	close(release)
	require.Eventually(t, func() bool { u := h.upstream(0); return u != nil && u.closeCount() == 1 && h.closed.Load() == 1 },
		3*time.Second, 5*time.Millisecond, "the late upstream and the handle are closed")
}

func TestStopDuringOpen(t *testing.T) {
	be := newBackend(t, false)
	m := newManager(t, Options{})
	opening := make(chan struct{}, 1)
	h := &fakeHandle{be: be, remote: 80, open: func(ctx context.Context, _ *fakeUpstream, _ int) error {
		opening <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}}
	in := start(t, m, h, 0)
	before := runtime.NumGoroutine()
	c := dial(t, in)
	<-opening
	require.NoError(t, m.Stop(in.ID))
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err := c.Read(make([]byte, 1))
	require.Error(t, err)
	assert.EqualValues(t, 0, m.Conns())
	assert.EqualValues(t, 0, h.live.Load())
	settled(t, before)
}

func TestCloseEndsEverythingAndRefusesLateStarts(t *testing.T) {
	be := newBackend(t, false)
	m := NewManager(Options{})
	before := runtime.NumGoroutine()
	h := &fakeHandle{be: be, remote: 80}
	in := start(t, m, h, 0)
	c := dial(t, in)
	require.NoError(t, roundTrip(c, "live"))
	m.Close()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err := c.Read(make([]byte, 1))
	require.Error(t, err)
	assert.EqualValues(t, 1, h.closed.Load())
	assert.Equal(t, 1, h.upstream(0).closeCount())
	assert.EqualValues(t, 0, h.live.Load())
	ln, err := net.Listen("tcp4", in.Addresses[0])
	require.NoError(t, err, "the port is free")
	_ = ln.Close()
	settled(t, before)

	late := &fakeHandle{be: be, remote: 80}
	_, err = m.Start(context.Background(), late, StartRequest{})
	require.ErrorIs(t, err, ErrClosed)
	assert.EqualValues(t, 1, late.closed.Load())
}

func TestCountersAreThrottledStateChangesAreNot(t *testing.T) {
	be := newBackend(t, false)
	var events atomic.Int32
	m := newManager(t, Options{EventInterval: 100 * time.Millisecond, OnChange: func() { events.Add(1) }})
	in := start(t, m, &fakeHandle{be: be, remote: 80}, 0)
	c := dial(t, in)
	events.Store(0)
	deadline := time.Now().Add(500 * time.Millisecond)
	n := 0
	for time.Now().Before(deadline) {
		require.NoError(t, roundTrip(c, "tick"))
		n++
	}
	time.Sleep(150 * time.Millisecond) // the trailing counter event
	got := events.Load()
	assert.Greater(t, n, 50, "enough traffic to tell")
	assert.LessOrEqual(t, got, int32(7), "at most one per interval")
	assert.GreaterOrEqual(t, got, int32(4), "counters still get through")
	last := m.List()[0]
	assert.EqualValues(t, 4*n, last.BytesIn)

	events.Store(0)
	require.NoError(t, m.Stop(in.ID))
	assert.GreaterOrEqual(t, events.Load(), int32(1), "a state change at once")
}

func TestFailedConnectIsHeldBriefly(t *testing.T) {
	be := newBackend(t, false)
	now := time.Now()
	var mu sync.Mutex
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	m := newManager(t, Options{Now: clock})
	var fail atomic.Bool
	h := &fakeHandle{be: be, remote: 80, connect: func(context.Context, int) error {
		if fail.Load() {
			return &provider.Error{Class: provider.ClassGone, Message: "pod replaced"}
		}
		return nil
	}}
	in := start(t, m, h, 0)
	fail.Store(true)
	h.upstream(0).kill(errors.New("gone"))
	require.Eventually(t, func() bool { return m.List()[0].State == StateIdle }, 3*time.Second, 5*time.Millisecond)
	for range 5 {
		c := dial(t, in)
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, err := c.Read(make([]byte, 1))
		require.Error(t, err)
	}
	assert.EqualValues(t, 2, h.connects.Load(), "one failed connect for five connections")
	assert.Equal(t, StateError, m.List()[0].State)
	mu.Lock()
	now = now.Add(2 * failHold)
	mu.Unlock()
	fail.Store(false)
	require.NoError(t, roundTrip(dial(t, in), "after the hold"))
	assert.EqualValues(t, 3, h.connects.Load())
}

func TestSlowErrorReportDoesNotHoldStop(t *testing.T) {
	be := newBackend(t, false)
	m := newManager(t, Options{})
	h := &fakeHandle{be: be, remote: 80, resultDelay: time.Minute}
	h.open = func(_ context.Context, _ *fakeUpstream, n int) error {
		if n == 1 {
			return errPortRefused
		}
		return nil
	}
	in := start(t, m, h, 0)
	before := runtime.NumGoroutine()
	refused := dial(t, in)
	require.Eventually(t, func() bool { return h.live.Load() == 1 }, 3*time.Second, 5*time.Millisecond)
	require.NoError(t, roundTrip(dial(t, in), "a neighbour is not held up"))
	begin := time.Now()
	require.NoError(t, m.Stop(in.ID))
	assert.Less(t, time.Since(begin), 2*time.Second)
	_ = refused.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err := refused.Read(make([]byte, 1))
	require.Error(t, err)
	settled(t, before)
}
