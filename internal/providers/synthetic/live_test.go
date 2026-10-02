package synthetic

import (
	"bufio"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

type sizes struct {
	ch   chan provider.TermSize
	done chan struct{}
}

func (s *sizes) Next() *provider.TermSize {
	select {
	case v := <-s.ch:
		return &v
	case <-s.done:
		return nil
	}
}

// term runs h in a fake terminal: what it prints collects in out.
type term struct {
	in    *io.PipeWriter
	sizes *sizes
	mu    sync.Mutex
	out   strings.Builder
	res   chan result
}

type result struct {
	st  provider.ExitStatus
	err error
}

func (t *term) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.out.Write(p)
}

func (t *term) text() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.out.String()
}

func (t *term) waitFor(tb testing.TB, s string) {
	tb.Helper()
	require.Eventually(tb, func() bool { return strings.Contains(t.text(), s) }, 5*time.Second, 5*time.Millisecond, "waiting for %q in %q", s, t.text())
}

func (t *term) wait(tb testing.TB) result {
	tb.Helper()
	select {
	case r := <-t.res:
		return r
	case <-time.After(5 * time.Second):
		tb.Fatal("the command did not end")
		return result{}
	}
}

func start(tb testing.TB, argv ...string) (*Provider, *term) {
	tb.Helper()
	p := New()
	s, err := p.Open(context.Background(), Target)
	require.NoError(tb, err)
	h, err := s.(provider.Execer).PrepareExec(context.Background(), core.Ref{Provider: ID, Target: Target, Kind: Kind, Name: "workers"}, provider.ExecRequest{Instance: "worker-2", Command: argv})
	require.NoError(tb, err)
	assert.Equal(tb, "worker-2", h.Describe().Instance)
	return p, runTerm(h)
}

// runTerm runs h in a fake terminal 80x24.
func runTerm(h provider.ExecHandle) *term {
	inR, inW := io.Pipe()
	t := &term{in: inW, sizes: &sizes{ch: make(chan provider.TermSize, 4), done: make(chan struct{})}, res: make(chan result, 1)}
	t.sizes.ch <- provider.TermSize{Cols: 80, Rows: 24}
	go func() {
		st, err := h.Run(context.Background(), provider.Terminal{Stdin: inR, Stdout: t, Sizes: t.sizes})
		_ = inR.Close()
		close(t.sizes.done)
		h.Close()
		t.res <- result{st, err} // completion includes releasing the handle
	}()
	return t
}

func TestEchoTerminal(t *testing.T) {
	p, tm := start(t)
	tm.waitFor(t, "size 80x24")
	tm.waitFor(t, "synthetic terminal on worker-2\r\n$ ")
	_, _ = tm.in.Write([]byte("hello\r"))
	tm.waitFor(t, "hello\r\nyou said: hello\r\n$ ")
	tm.sizes.ch <- provider.TermSize{Cols: 100, Rows: 30}
	tm.waitFor(t, "size 100x30")
	assert.Equal(t, 1, p.LiveStats()["syn_execs"])
	_, _ = tm.in.Write([]byte("exit 3\r"))
	r := tm.wait(t)
	require.NoError(t, r.err)
	assert.Equal(t, provider.ExitStatus{Code: 3, Known: true}, r.st)
	assert.Equal(t, map[string]int{"syn_execs": 0, "syn_handles": 0, "syn_upstreams": 0, "syn_streams": 0}, p.LiveStats())
}

func TestEchoTerminalFloodStopsAtCtrlC(t *testing.T) {
	_, tm := start(t)
	tm.waitFor(t, "$ ")
	_, _ = tm.in.Write([]byte("flood 100000000\r"))
	tm.waitFor(t, "flood line 1000 of")
	_, _ = tm.in.Write([]byte{0x03})
	tm.waitFor(t, "^C\r\n$ ")
	assert.NotContains(t, tm.text(), "flood done")
	// ^D on an empty line: exit 0, like a shell
	_, _ = tm.in.Write([]byte{0x04})
	r := tm.wait(t)
	require.NoError(t, r.err)
	assert.Equal(t, provider.ExitStatus{Code: 0, Known: true}, r.st)
}

func TestEchoTerminalRunsACommandOnce(t *testing.T) {
	_, tm := start(t, "flood", "3")
	r := tm.wait(t)
	require.NoError(t, r.err)
	assert.Equal(t, provider.ExitStatus{Code: 0, Known: true}, r.st)
	assert.Contains(t, tm.text(), "flood line 3 of 3\r\nflood done\r\n")
	_, tm = start(t, "exit", "4")
	assert.Equal(t, 4, tm.wait(t).st.Code)
}

func TestForwardServesHTTPAndClosingTheUpstreamResetsItsStreams(t *testing.T) {
	p := New()
	s, err := p.Open(context.Background(), Target)
	require.NoError(t, err)
	pf := s.(provider.PortForwarder)
	ref := core.Ref{Provider: ID, Target: Target, Kind: Kind, Name: "api"}
	info, err := pf.ForwardInfo(context.Background(), ref)
	require.NoError(t, err)
	require.Len(t, info.Ports, 2)
	_, err = pf.PrepareForward(context.Background(), ref, provider.ForwardRequest{Port: 53})
	assert.Error(t, err, "a UDP port cannot be forwarded")

	h, err := pf.PrepareForward(context.Background(), ref, provider.ForwardRequest{Port: 80})
	require.NoError(t, err)
	up, err := h.Connect(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "api:80", up.Label())

	st, err := up.Open(context.Background())
	require.NoError(t, err)
	_, err = io.WriteString(st, "GET /x HTTP/1.0\r\nHost: a\r\n\r\n")
	require.NoError(t, err)
	require.NoError(t, st.CloseWrite())
	body, err := io.ReadAll(st)
	require.NoError(t, err)
	assert.Contains(t, string(body), "hello from api:80 /x")
	assert.NoError(t, st.Result())
	require.NoError(t, st.Close())

	// an open stream is reset when its upstream closes
	st2, err := up.Open(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, p.LiveStats()["syn_streams"])
	up.Close()
	_, err = bufio.NewReader(st2).ReadByte()
	assert.Error(t, err)
	_, err = up.Open(context.Background())
	assert.Error(t, err)
	h.Close()
	h.Close()
	assert.Equal(t, map[string]int{"syn_execs": 0, "syn_handles": 0, "syn_upstreams": 0, "syn_streams": 0}, p.LiveStats())
}
