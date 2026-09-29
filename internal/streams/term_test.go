package streams

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/provider"
)

const testOrigin = "wails://localhost"

// fakeTerm is a TermSession whose Run is the test's.
type fakeTerm struct {
	run    func(ctx context.Context, t provider.Terminal) (provider.ExitStatus, error)
	closed atomic.Int32
}

func (f *fakeTerm) Run(ctx context.Context, t provider.Terminal) (provider.ExitStatus, error) {
	return f.run(ctx, t)
}
func (f *fakeTerm) Close() { f.closed.Add(1) }

func newTermFixture(t *testing.T, tm termTimings) *fixture {
	t.Helper()
	f := newFixture(t, HandlerOptions{AllowOrigin: testOrigin})
	f.h.term = tm
	return f
}

var fastTimings = termTimings{ping: time.Hour, pingTimeout: time.Hour, ackTimeout: time.Hour, drain: 5 * time.Second, write: 5 * time.Second, hangup: 300 * time.Millisecond}

func (f *fixture) termURL(id string) string {
	return "ws" + strings.TrimPrefix(f.srv.URL, "http") + "/" + f.h.Token() + "/term/" + id
}

func (f *fixture) dial(t *testing.T, id string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, f.termURL(id), &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {testOrigin}}})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	require.NoError(t, err)
	c.SetReadLimit(4 << 20)
	t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

// client reads a terminal socket in the background.
type client struct {
	c      *websocket.Conn
	mu     sync.Mutex
	out    bytes.Buffer
	msgs   []map[string]any
	closed chan struct{}
	err    error
}

func newClient(c *websocket.Conn) *client {
	cl := &client{c: c, closed: make(chan struct{})}
	go func() {
		defer close(cl.closed)
		for {
			typ, data, err := c.Read(context.Background())
			cl.mu.Lock()
			if err != nil {
				cl.err = err
				cl.mu.Unlock()
				return
			}
			if typ == websocket.MessageBinary {
				cl.out.Write(data)
			} else {
				var m map[string]any
				_ = json.Unmarshal(data, &m)
				m["_at"] = cl.out.Len() // output bytes before this control message
				cl.msgs = append(cl.msgs, m)
			}
			cl.mu.Unlock()
		}
	}()
	return cl
}

func (cl *client) outLen() int {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return cl.out.Len()
}

func (cl *client) output() string {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return cl.out.String()
}

func (cl *client) controls(kind string) []map[string]any {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	var out []map[string]any
	for _, m := range cl.msgs {
		if m["k"] == kind {
			out = append(out, m)
		}
	}
	return out
}

func (cl *client) kinds() []string {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	var out []string
	for _, m := range cl.msgs {
		k := m["k"].(string)
		if k == "state" {
			k += ":" + m["state"].(string)
		}
		if k != "iack" {
			out = append(out, k)
		}
	}
	return out
}

func (cl *client) waitClosed(t *testing.T) websocket.StatusCode {
	t.Helper()
	select {
	case <-cl.closed:
	case <-time.After(10 * time.Second):
		t.Fatal("the terminal socket did not close")
	}
	return websocket.CloseStatus(cl.err)
}

func (cl *client) send(t *testing.T, typ websocket.MessageType, data []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, cl.c.Write(ctx, typ, data))
}

func (cl *client) ctl(t *testing.T, m map[string]any) {
	t.Helper()
	b, _ := json.Marshal(m)
	cl.send(t, websocket.MessageText, b)
}

// echoRun echoes input until it sees "exit N".
func echoRun(ctx context.Context, term provider.Terminal) (provider.ExitStatus, error) {
	buf := make([]byte, 1024)
	var line string
	for {
		n, err := term.Stdin.Read(buf)
		if err != nil {
			return provider.ExitStatus{}, ctx.Err()
		}
		if _, err := term.Stdout.Write(buf[:n]); err != nil {
			return provider.ExitStatus{}, err
		}
		line += string(buf[:n])
		if i := strings.Index(line, "exit 3\r"); i >= 0 {
			return provider.ExitStatus{Code: 3, Known: true}, nil
		}
	}
}

func TestTermGuardsDoNotConsumeTheID(t *testing.T) {
	f := newTermFixture(t, fastTimings)
	ft := &fakeTerm{run: echoRun}
	id, err := f.reg.AddTerm("term", ft, provider.TermSize{Cols: 80, Rows: 24})
	require.NoError(t, err)
	logID, err := f.reg.Add("o", lines(1))
	require.NoError(t, err)
	up := map[string]string{"Connection": "Upgrade", "Upgrade": "websocket", "Sec-WebSocket-Version": "13", "Sec-WebSocket-Key": "dGhlIHNhbXBsZSBub25jZQ=="}
	with := func(extra map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range up {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	base := "/" + f.h.Token()
	assert.Equal(t, http.StatusForbidden, f.get(t, base+"/term/"+id, up).StatusCode, "no Origin")
	assert.Equal(t, http.StatusForbidden, f.get(t, base+"/term/"+id, with(map[string]string{"Origin": "http://evil.example"})).StatusCode)
	assert.Equal(t, http.StatusNotFound, f.get(t, "/nope/term/"+id, with(map[string]string{"Origin": testOrigin})).StatusCode)
	assert.Equal(t, http.StatusBadRequest, f.get(t, base+"/term/"+id, map[string]string{"Origin": testOrigin}).StatusCode, "not an upgrade")
	assert.Equal(t, http.StatusBadRequest, f.get(t, base+"/term/"+id, with(map[string]string{"Origin": testOrigin, "Sec-WebSocket-Key": "short"})).StatusCode)
	assert.Equal(t, http.StatusGone, f.get(t, base+"/logs/"+id, map[string]string{"Origin": testOrigin}).StatusCode, "a terminal id is not a log stream")
	assert.Equal(t, http.StatusGone, f.get(t, base+"/term/"+logID, with(map[string]string{"Origin": testOrigin})).StatusCode, "a log id is not a terminal")
	assert.Equal(t, int32(0), ft.closed.Load())

	cl := newClient(f.dial(t, id))
	cl.send(t, websocket.MessageBinary, []byte("hi"))
	require.Eventually(t, func() bool { return cl.output() == "hi" }, 5*time.Second, 10*time.Millisecond)
	// The log stream is still usable too.
	assert.Equal(t, http.StatusOK, f.get(t, base+"/logs/"+logID, map[string]string{"Origin": testOrigin}).StatusCode)
}

func TestTermEchoesThenExitsInOrder(t *testing.T) {
	f := newTermFixture(t, fastTimings)
	ft := &fakeTerm{run: echoRun}
	id, err := f.reg.AddTerm("term", ft, provider.TermSize{Cols: 80, Rows: 24})
	require.NoError(t, err)
	cl := newClient(f.dial(t, id))
	cl.send(t, websocket.MessageBinary, []byte("hello "))
	cl.send(t, websocket.MessageBinary, []byte("exit 3\r"))
	assert.Equal(t, websocket.StatusNormalClosure, cl.waitClosed(t))
	assert.Equal(t, "hello exit 3\r", cl.output())
	assert.Equal(t, []string{"state:connecting", "state:running", "exit", "end"}, cl.kinds())
	exit := cl.controls("exit")[0]
	assert.InDelta(t, 3, exit["code"], 0)
	assert.InDelta(t, len("hello exit 3\r"), exit["_at"], 0, "exit comes after all output")
	assert.Equal(t, "done", cl.controls("end")[0]["reason"])
	iacks := cl.controls("iack")
	require.NotEmpty(t, iacks)
	assert.InDelta(t, len("hello exit 3\r"), iacks[len(iacks)-1]["n"], 0)
	require.Eventually(t, func() bool { return ft.closed.Load() == 1 }, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, 0, f.reg.Count(KindTerm))
}

func TestTermResizeIsLatestWinsAndNeverBlocksTheReader(t *testing.T) {
	f := newTermFixture(t, fastTimings)
	proceed := make(chan struct{})
	sizes := make(chan provider.TermSize, 4)
	ft := &fakeTerm{run: func(ctx context.Context, term provider.Terminal) (provider.ExitStatus, error) {
		sizes <- *term.Sizes.Next() // the initial size
		go func() { _, _ = io.Copy(io.Discard, term.Stdin) }()
		<-proceed
		sizes <- *term.Sizes.Next()
		<-ctx.Done()
		assert.Nil(t, term.Sizes.Next(), "nil once the terminal is gone")
		return provider.ExitStatus{}, ctx.Err()
	}}
	id, err := f.reg.AddTerm("term", ft, provider.TermSize{Cols: 80, Rows: 24})
	require.NoError(t, err)
	cl := newClient(f.dial(t, id))
	assert.Equal(t, provider.TermSize{Cols: 80, Rows: 24}, <-sizes)
	for _, s := range []int{10, 20, 30} {
		cl.ctl(t, map[string]any{"k": "resize", "cols": s, "rows": s + 1})
	}
	// The reader is free: input still flows (and is confirmed).
	cl.send(t, websocket.MessageBinary, []byte("x"))
	require.Eventually(t, func() bool { return len(cl.controls("iack")) == 1 }, 5*time.Second, 10*time.Millisecond)
	close(proceed)
	assert.Equal(t, provider.TermSize{Cols: 30, Rows: 31}, <-sizes)
	_ = cl.c.Close(websocket.StatusNormalClosure, "")
	cl.waitClosed(t)
	require.Eventually(t, func() bool { return ft.closed.Load() == 1 }, 5*time.Second, 10*time.Millisecond)
}

// flooder writes n bytes of output, counting what the command got out.
func flooder(n int, written *atomic.Int64) func(ctx context.Context, term provider.Terminal) (provider.ExitStatus, error) {
	return func(_ context.Context, term provider.Terminal) (provider.ExitStatus, error) {
		chunk := bytes.Repeat([]byte("y"), 32<<10)
		for sent := 0; sent < n; sent += len(chunk) {
			if _, err := term.Stdout.Write(chunk); err != nil {
				return provider.ExitStatus{}, err
			}
			written.Add(int64(len(chunk)))
		}
		return provider.ExitStatus{Code: 0, Known: true}, nil
	}
}

func TestTermOutputStopsAtTheWindowUntilAcked(t *testing.T) {
	f := newTermFixture(t, fastTimings)
	var written atomic.Int64
	ft := &fakeTerm{run: flooder(3<<20, &written)}
	id, err := f.reg.AddTerm("term", ft, provider.TermSize{Cols: 80, Rows: 24})
	require.NoError(t, err)
	cl := newClient(f.dial(t, id))
	require.Eventually(t, func() bool { return cl.outLen() == termOutWindow }, 5*time.Second, 10*time.Millisecond)
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, termOutWindow, cl.outLen(), "nothing beyond the window without acks")
	assert.LessOrEqual(t, written.Load(), int64(termOutWindow+termReadChunk), "the command is held back, not buffered")
	cl.ctl(t, map[string]any{"k": "ack", "n": termOutWindow})
	cl.ctl(t, map[string]any{"k": "ack", "n": termOutWindow}) // a repeat is harmless
	require.Eventually(t, func() bool { return cl.outLen() == 2*termOutWindow }, 5*time.Second, 10*time.Millisecond)
	cl.ctl(t, map[string]any{"k": "ack", "n": 2 * termOutWindow})
	assert.Equal(t, websocket.StatusNormalClosure, cl.waitClosed(t))
	assert.Equal(t, 3<<20, cl.outLen())
	assert.Equal(t, []string{"state:connecting", "state:running", "exit", "end"}, cl.kinds())
}

func TestTermImpossibleAcksAreProtocolErrors(t *testing.T) {
	for name, ack := range map[string]any{
		"beyond sent": termOutWindow + 1,
		"fraction":    0.5,
		"negative":    -1,
		"missing":     nil,
	} {
		t.Run(name, func(t *testing.T) {
			f := newTermFixture(t, fastTimings)
			var written atomic.Int64
			ft := &fakeTerm{run: flooder(2<<20, &written)}
			id, err := f.reg.AddTerm("term", ft, provider.TermSize{Cols: 80, Rows: 24})
			require.NoError(t, err)
			cl := newClient(f.dial(t, id))
			require.Eventually(t, func() bool { return cl.outLen() == termOutWindow }, 5*time.Second, 10*time.Millisecond)
			cl.ctl(t, map[string]any{"k": "ack", "n": ack})
			assert.Equal(t, statusProtocol, cl.waitClosed(t))
		})
	}
	t.Run("lower than before", func(t *testing.T) {
		f := newTermFixture(t, fastTimings)
		var written atomic.Int64
		ft := &fakeTerm{run: flooder(3<<20, &written)}
		id, err := f.reg.AddTerm("term", ft, provider.TermSize{Cols: 80, Rows: 24})
		require.NoError(t, err)
		cl := newClient(f.dial(t, id))
		require.Eventually(t, func() bool { return cl.outLen() == termOutWindow }, 5*time.Second, 10*time.Millisecond)
		cl.ctl(t, map[string]any{"k": "ack", "n": 1000})
		cl.ctl(t, map[string]any{"k": "ack", "n": 999})
		assert.Equal(t, statusProtocol, cl.waitClosed(t), "%v", cl.err)
	})
}

func TestTermCommandNotReadingStdinKeepsControlAlive(t *testing.T) {
	f := newTermFixture(t, fastTimings)
	resized := make(chan provider.TermSize, 8)
	ft := &fakeTerm{run: func(ctx context.Context, term provider.Terminal) (provider.ExitStatus, error) {
		// Never reads stdin; writes output and follows sizes.
		go func() {
			for s := term.Sizes.Next(); s != nil; s = term.Sizes.Next() {
				resized <- *s
			}
		}()
		chunk := bytes.Repeat([]byte("z"), 32<<10)
		for range (3 << 20) / len(chunk) {
			if _, err := term.Stdout.Write(chunk); err != nil {
				return provider.ExitStatus{}, err
			}
		}
		<-ctx.Done()
		return provider.ExitStatus{}, ctx.Err()
	}}
	id, err := f.reg.AddTerm("term", ft, provider.TermSize{Cols: 80, Rows: 24})
	require.NoError(t, err)
	cl := newClient(f.dial(t, id))
	<-resized
	// A big paste the command does not read fills the input window...
	for range 8 {
		cl.send(t, websocket.MessageBinary, bytes.Repeat([]byte("p"), 32<<10))
	}
	// ...while acks and resizes are still handled.
	require.Eventually(t, func() bool { return cl.outLen() == termOutWindow }, 5*time.Second, 10*time.Millisecond)
	cl.ctl(t, map[string]any{"k": "ack", "n": termOutWindow})
	require.Eventually(t, func() bool { return cl.outLen() == 2*termOutWindow }, 5*time.Second, 10*time.Millisecond)
	cl.ctl(t, map[string]any{"k": "resize", "cols": 100, "rows": 40})
	assert.Equal(t, provider.TermSize{Cols: 100, Rows: 40}, <-resized)
	assert.Empty(t, cl.controls("iack"))
	// One byte more than the window is a broken page.
	cl.send(t, websocket.MessageBinary, []byte("!"))
	assert.Equal(t, statusProtocol, cl.waitClosed(t))
	require.Eventually(t, func() bool { return ft.closed.Load() == 1 }, 5*time.Second, 10*time.Millisecond)
}

func TestTermPageThatStopsAckingIsDisconnected(t *testing.T) {
	tm := fastTimings
	tm.ackTimeout = 200 * time.Millisecond
	f := newTermFixture(t, tm)
	ended := make(chan error, 1)
	ft := &fakeTerm{run: func(_ context.Context, term provider.Terminal) (provider.ExitStatus, error) {
		chunk := bytes.Repeat([]byte("y"), 32<<10)
		for {
			if _, err := term.Stdout.Write(chunk); err != nil {
				ended <- err
				return provider.ExitStatus{}, err
			}
		}
	}}
	id, err := f.reg.AddTerm("term", ft, provider.TermSize{Cols: 80, Rows: 24})
	require.NoError(t, err)
	cl := newClient(f.dial(t, id))
	assert.Equal(t, statusNotReading, cl.waitClosed(t))
	assert.Error(t, <-ended, "the command's output is cut off")
}

func TestTermUndeliveredOutputAfterExitIsReported(t *testing.T) {
	tm := fastTimings
	tm.drain = 200 * time.Millisecond
	f := newTermFixture(t, tm)
	ft := &fakeTerm{run: func(_ context.Context, term provider.Terminal) (provider.ExitStatus, error) {
		// Output beyond the window, then exit (a real pipe would let the
		// command finish; here the tail is still unread).
		go func() { _, _ = term.Stdout.Write(bytes.Repeat([]byte("q"), 2<<20)) }()
		time.Sleep(100 * time.Millisecond)
		return provider.ExitStatus{Code: 0, Known: true}, nil
	}}
	id, err := f.reg.AddTerm("term", ft, provider.TermSize{Cols: 80, Rows: 24})
	require.NoError(t, err)
	cl := newClient(f.dial(t, id))
	assert.Equal(t, statusNotReading, cl.waitClosed(t))
	ends := cl.controls("end")
	require.Len(t, ends, 1)
	assert.Equal(t, "error", ends[0]["reason"])
	assert.Empty(t, cl.controls("exit"), "no exit before the output it follows")
}

func TestTermRunErrorsEndWithoutExit(t *testing.T) {
	for name, tc := range map[string]struct {
		st     provider.ExitStatus
		err    error
		reason string
	}{
		"failed":  {err: errors.New("pods/exec is forbidden"), reason: "error"},
		"unknown": {st: provider.ExitStatus{Known: false}, reason: "done"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newTermFixture(t, fastTimings)
			ft := &fakeTerm{run: func(context.Context, provider.Terminal) (provider.ExitStatus, error) { return tc.st, tc.err }}
			id, err := f.reg.AddTerm("term", ft, provider.TermSize{Cols: 80, Rows: 24})
			require.NoError(t, err)
			cl := newClient(f.dial(t, id))
			assert.Equal(t, websocket.StatusNormalClosure, cl.waitClosed(t))
			assert.Empty(t, cl.controls("exit"))
			ends := cl.controls("end")
			require.Len(t, ends, 1)
			assert.Equal(t, tc.reason, ends[0]["reason"])
			if tc.err != nil {
				assert.Contains(t, ends[0]["message"], "forbidden")
			}
		})
	}
}

func TestTermPageClosingCancelsTheCommand(t *testing.T) {
	f := newTermFixture(t, fastTimings)
	cancelled := make(chan struct{})
	ft := &fakeTerm{run: func(ctx context.Context, _ provider.Terminal) (provider.ExitStatus, error) {
		<-ctx.Done()
		close(cancelled)
		return provider.ExitStatus{}, ctx.Err()
	}}
	id, err := f.reg.AddTerm("term", ft, provider.TermSize{Cols: 80, Rows: 24})
	require.NoError(t, err)
	c := f.dial(t, id)
	_ = c.Close(websocket.StatusNormalClosure, "tab closed")
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("closing the page did not cancel the command")
	}
	require.Eventually(t, func() bool { return ft.closed.Load() == 1 && f.reg.Count(KindTerm) == 0 }, 5*time.Second, 10*time.Millisecond)
}

func TestTermRegistryCloseEndsTerminalsGoneAndJoins(t *testing.T) {
	before := runtime.NumGoroutine()
	reg := NewRegistry()
	h := NewHandler(reg, HandlerOptions{AllowOrigin: testOrigin})
	h.term = fastTimings
	srv := newTestServer(t, h)
	f := &fixture{reg: reg, h: h, srv: srv}
	fts := make([]*fakeTerm, 3)
	cls := make([]*client, 3)
	for i := range fts {
		fts[i] = &fakeTerm{run: func(ctx context.Context, term provider.Terminal) (provider.ExitStatus, error) {
			go func() { _, _ = io.Copy(io.Discard, term.Stdin) }()
			<-ctx.Done()
			return provider.ExitStatus{}, ctx.Err()
		}}
		id, err := reg.AddTerm("term", fts[i], provider.TermSize{Cols: 80, Rows: 24})
		require.NoError(t, err)
		cls[i] = newClient(f.dial(t, id))
		cls[i].send(t, websocket.MessageBinary, []byte("x"))
	}
	pending := &fakeTerm{run: echoRun}
	_, err := reg.AddTerm("term", pending, provider.TermSize{Cols: 80, Rows: 24})
	require.NoError(t, err)
	for _, cl := range cls {
		require.Eventually(t, func() bool { return len(cl.controls("iack")) == 1 }, 5*time.Second, 10*time.Millisecond)
	}

	reg.Close() // the app exits
	for i, cl := range cls {
		assert.Equal(t, websocket.StatusGoingAway, cl.waitClosed(t))
		ends := cl.controls("end")
		require.Len(t, ends, 1)
		assert.Equal(t, "gone", ends[0]["reason"])
		assert.Equal(t, int32(1), fts[i].closed.Load(), "Close waited for the terminal")
	}
	assert.Equal(t, int32(1), pending.closed.Load(), "a pending terminal is released")
	_, err = reg.AddTerm("term", &fakeTerm{}, provider.TermSize{Cols: 1, Rows: 1})
	assert.ErrorIs(t, err, ErrGone)
	srv.Close()
	require.Eventually(t, func() bool { return runtime.NumGoroutine() <= before+2 }, 5*time.Second, 20*time.Millisecond,
		"goroutines left: %d (before %d)", runtime.NumGoroutine(), before)
}

func TestTermLimitsCountPendingAndReleaseExpired(t *testing.T) {
	reg := NewRegistry()
	now := time.Now()
	reg.now = func() time.Time { return now }
	var fts []*fakeTerm
	for range MaxTerms {
		ft := &fakeTerm{}
		fts = append(fts, ft)
		_, err := reg.AddTerm("term", ft, provider.TermSize{Cols: 80, Rows: 24})
		require.NoError(t, err)
	}
	over := &fakeTerm{}
	_, err := reg.AddTerm("term", over, provider.TermSize{Cols: 80, Rows: 24})
	assert.ErrorIs(t, err, ErrLimit)
	assert.Equal(t, int32(1), over.closed.Load(), "a refused terminal is released")
	_, err = reg.Add("o", nil)
	assert.NoError(t, err, "log streams have their own limit")
	assert.Equal(t, map[string]int{"term": MaxTerms, "o": 1}, reg.Owners())

	now = now.Add(connectTTL + time.Second)
	assert.Equal(t, 0, reg.Count(KindTerm))
	for _, ft := range fts {
		assert.Equal(t, int32(1), ft.closed.Load(), "an expired terminal is released")
	}
	reg.Close()
}

func newTestServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s
}

func TestTermDeadPageIsDetectedByPing(t *testing.T) {
	tm := fastTimings
	tm.ping, tm.pingTimeout = 100*time.Millisecond, 100*time.Millisecond
	f := newTermFixture(t, tm)
	cancelled := make(chan struct{})
	ft := &fakeTerm{run: func(ctx context.Context, _ provider.Terminal) (provider.ExitStatus, error) {
		<-ctx.Done()
		close(cancelled)
		return provider.ExitStatus{}, ctx.Err()
	}}
	id, err := f.reg.AddTerm("term", ft, provider.TermSize{Cols: 80, Rows: 24})
	require.NoError(t, err)
	_ = f.dial(t, id) // never reads: pings go unanswered
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("a page that does not answer pings was not dropped")
	}
	require.Eventually(t, func() bool { return ft.closed.Load() == 1 }, 5*time.Second, 10*time.Millisecond)
}

// ttyShell behaves like a shell on a TTY: it echoes input and ends with 130
// on ^C followed by ^D. It reports the input it saw and whether it was
// cancelled rather than hung up.
func ttyShell(seen chan<- []byte, cancelled *atomic.Bool) func(ctx context.Context, term provider.Terminal) (provider.ExitStatus, error) {
	return func(ctx context.Context, term provider.Terminal) (provider.ExitStatus, error) {
		var all []byte
		buf := make([]byte, 64)
		for {
			n, err := term.Stdin.Read(buf)
			if err != nil {
				cancelled.Store(ctx.Err() != nil)
				seen <- all
				return provider.ExitStatus{}, ctx.Err()
			}
			all = append(all, buf[:n]...)
			_, _ = term.Stdout.Write(buf[:n])
			if bytes.HasSuffix(all, []byte{3, 4}) {
				cancelled.Store(ctx.Err() != nil)
				seen <- all
				return provider.ExitStatus{Code: 130, Known: true}, nil
			}
		}
	}
}

func TestTermClosingThePageHangsUpInBand(t *testing.T) {
	f := newTermFixture(t, fastTimings)
	seen := make(chan []byte, 1)
	var cancelled atomic.Bool
	ft := &fakeTerm{run: ttyShell(seen, &cancelled)}
	id, err := f.reg.AddTerm("term", ft, provider.TermSize{Cols: 80, Rows: 24})
	require.NoError(t, err)
	cl := newClient(f.dial(t, id))
	cl.send(t, websocket.MessageBinary, []byte("sleep 99\r"))
	require.Eventually(t, func() bool { return cl.output() == "sleep 99\r" }, 5*time.Second, 10*time.Millisecond)
	_ = cl.c.Close(websocket.StatusNormalClosure, "tab closed")
	select {
	case got := <-seen:
		assert.Equal(t, []byte("sleep 99\r\x03\x04"), got)
	case <-time.After(5 * time.Second):
		t.Fatal("the command was not hung up")
	}
	assert.False(t, cancelled.Load(), "ended by ^C ^D, not by cancellation")
	require.Eventually(t, func() bool { return ft.closed.Load() == 1 }, 5*time.Second, 10*time.Millisecond)
}

func TestTermCommandIgnoringTheHangupIsCancelled(t *testing.T) {
	f := newTermFixture(t, fastTimings)
	var cancelled atomic.Bool
	ended := make(chan time.Time, 1)
	ft := &fakeTerm{run: func(ctx context.Context, term provider.Terminal) (provider.ExitStatus, error) {
		_, _ = term.Stdout.Write([]byte("vim"))
		_, _ = io.Copy(io.Discard, term.Stdin) // ignores ^C ^D
		cancelled.Store(ctx.Err() != nil)
		ended <- time.Now()
		return provider.ExitStatus{}, ctx.Err()
	}}
	id, err := f.reg.AddTerm("term", ft, provider.TermSize{Cols: 80, Rows: 24})
	require.NoError(t, err)
	cl := newClient(f.dial(t, id))
	require.Eventually(t, func() bool { return cl.output() == "vim" }, 5*time.Second, 10*time.Millisecond)
	closed := time.Now()
	_ = cl.c.Close(websocket.StatusNormalClosure, "tab closed")
	at := <-ended
	assert.True(t, cancelled.Load())
	assert.GreaterOrEqual(t, at.Sub(closed), fastTimings.hangup-50*time.Millisecond, "cancelled only after the hang-up time")
}

func TestSizeBoxSkipsTheSizeTheCommandHas(t *testing.T) {
	done := make(chan struct{})
	b := newSizeBox(provider.TermSize{Cols: 80, Rows: 24})
	b.done = done
	assert.Equal(t, provider.TermSize{Cols: 80, Rows: 24}, *b.Next())
	b.set(provider.TermSize{Cols: 80, Rows: 24}) // the page repeating its size
	b.set(provider.TermSize{Cols: 90, Rows: 24})
	b.set(provider.TermSize{Cols: 100, Rows: 30})
	assert.Equal(t, provider.TermSize{Cols: 100, Rows: 30}, *b.Next(), "latest wins")
	b.set(provider.TermSize{Cols: 100, Rows: 30})
	close(done)
	assert.Nil(t, b.Next(), "no repeated size before the end")
}

// blockedReader is a command that prints "ready" and reads nothing until
// the page's resize to 100x30 has been applied (so every input message the
// page sent before it is queued in the bridge); then until until(ctx)
// returns it reads its stdin, which it reports.
func blockedReader(seen chan<- []byte, applied chan<- struct{}, until func(sizes provider.TermSizes), done func(all []byte) bool) func(ctx context.Context, term provider.Terminal) (provider.ExitStatus, error) {
	return func(_ context.Context, term provider.Terminal) (provider.ExitStatus, error) {
		_, _ = term.Stdout.Write([]byte("ready"))
		s := term.Sizes.Next()
		for s != nil && s.Cols != 100 {
			s = term.Sizes.Next()
		}
		close(applied)
		until(term.Sizes)
		var all []byte
		buf := make([]byte, 64)
		for {
			n, err := term.Stdin.Read(buf)
			all = append(all, buf[:n]...)
			if err != nil || done(all) {
				seen <- all
				return provider.ExitStatus{Known: err == nil}, nil
			}
		}
	}
}

func TestTermClosingDropsQueuedInputAndHangsUpAfterIt(t *testing.T) {
	f := newTermFixture(t, fastTimings)
	seen, applied := make(chan []byte, 1), make(chan struct{})
	ft := &fakeTerm{run: blockedReader(seen, applied,
		func(sizes provider.TermSizes) { // the terminal is gone before the command reads
			for sizes.Next() != nil { //nolint:revive // drain until the terminal is gone
			}
		},
		func(all []byte) bool { return bytes.HasSuffix(all, []byte{3, 4}) })}
	id, err := f.reg.AddTerm("term", ft, provider.TermSize{Cols: 80, Rows: 24})
	require.NoError(t, err)
	cl := newClient(f.dial(t, id))
	require.Eventually(t, func() bool { return cl.output() == "ready" }, 5*time.Second, 10*time.Millisecond)
	for _, s := range []string{"one\r", "two\r", "three\r"} {
		cl.send(t, websocket.MessageBinary, []byte(s))
	}
	cl.send(t, websocket.MessageText, []byte(`{"k":"resize","cols":100,"rows":30}`))
	<-applied // everything sent before the resize is queued
	_ = cl.c.Close(websocket.StatusNormalClosure, "tab closed")
	select {
	case got := <-seen:
		// "one\r" may already be on its way (a write cannot be recalled);
		// the queued rest is dropped, and the hang-up keys come last.
		assert.Contains(t, []string{"one\r\x03\x04", "\x03\x04"}, string(got))
	case <-time.After(5 * time.Second):
		t.Fatal("the command was not hung up")
	}
}

func TestTermInterruptDropsQueuedInputAndGoesFirst(t *testing.T) {
	f := newTermFixture(t, fastTimings)
	seen, applied := make(chan []byte, 1), make(chan struct{})
	ft := &fakeTerm{run: blockedReader(seen, applied, func(provider.TermSizes) {},
		func(all []byte) bool { return bytes.HasSuffix(all, []byte("four\r")) })}
	id, err := f.reg.AddTerm("term", ft, provider.TermSize{Cols: 80, Rows: 24})
	require.NoError(t, err)
	cl := newClient(f.dial(t, id))
	require.Eventually(t, func() bool { return cl.output() == "ready" }, 5*time.Second, 10*time.Millisecond)
	for _, s := range []string{"one\r", "two\r", "three\r"} {
		cl.send(t, websocket.MessageBinary, []byte(s))
	}
	cl.send(t, websocket.MessageText, []byte(`{"k":"intr"}`))
	cl.send(t, websocket.MessageText, []byte(`{"k":"intr"}`)) // repeated: still one ^C
	cl.send(t, websocket.MessageText, []byte(`{"k":"resize","cols":100,"rows":30}`))
	// the dropped input frees the page's window: iack covers all 14 bytes
	require.Eventually(t, func() bool {
		for _, m := range cl.controls("iack") {
			if m["n"] == float64(14) {
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond)
	cl.send(t, websocket.MessageBinary, []byte("four\r"))
	select {
	case got := <-seen:
		assert.Contains(t, []string{"one\r\x03four\r", "\x03four\r"}, string(got), "queued input dropped, ^C first")
	case <-time.After(5 * time.Second):
		t.Fatal("the command did not get the interrupt")
	}
}

// An interrupt drops only what is still queued: the chunk being written
// stays counted until its write ends, so the input window never goes
// negative (or lets the page past it), however often Ctrl+C comes.
func TestTermInterruptKeepsTheChunkBeingWrittenCounted(t *testing.T) {
	b := newTermBridge(nil, fastTimings, nil, provider.TermSize{})
	ctx, cancel := context.WithCancelCause(context.Background())
	b.ctx, b.cancel = ctx, cancel
	runCtx, stop := context.WithCancel(context.Background())
	defer stop()
	r, w := io.Pipe()
	defer r.Close()
	done := make(chan struct{})
	counts := func() (int, int64) {
		b.mu.Lock()
		defer b.mu.Unlock()
		assert.GreaterOrEqual(t, b.inBytes, 0)
		assert.LessOrEqual(t, b.inBytes, termInWindow)
		return b.inBytes, b.inDone
	}
	require.True(t, b.pushInput([]byte("AAAA")))
	require.True(t, b.pushInput([]byte("BBBB")))
	go func() {
		b.stdinLoop(runCtx, w)
		close(done)
	}()
	require.Eventually(t, func() bool { // AAAA is being written (the pipe blocks it)
		b.mu.Lock()
		defer b.mu.Unlock()
		return len(b.inQ) == 1
	}, 5*time.Second, time.Millisecond)

	b.interrupt()
	b.interrupt() // repeated
	n, d := counts()
	assert.Equal(t, 4, n, "AAAA is still in flight")
	assert.EqualValues(t, 4, d, "BBBB dropped counts as done")
	require.True(t, b.pushInput([]byte("CC")), "input after the interrupt")

	buf := make([]byte, 4)
	_, err := io.ReadFull(r, buf)
	require.NoError(t, err)
	assert.Equal(t, "AAAA", string(buf))
	one := make([]byte, 1)
	_, err = io.ReadFull(r, one)
	require.NoError(t, err)
	assert.Equal(t, []byte{3}, one, "^C next, before later input")
	_, err = io.ReadFull(r, buf[:2])
	require.NoError(t, err)
	assert.Equal(t, "CC", string(buf[:2]))
	require.Eventually(t, func() bool {
		n, d := counts()
		return n == 0 && d == 10
	}, 5*time.Second, time.Millisecond)

	cancel(errPageGone)
	_ = w.Close()
	<-done
}
