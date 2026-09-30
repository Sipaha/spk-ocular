package engine_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/compose/engine"
	"github.com/spk/spk-ocular/internal/providers/compose/enginefake"
)

func runningContainer(f *enginefake.Engine, id, name string) {
	f.PutContainer(engine.ContainerInspect{
		ID: id, Name: "/" + name, Created: engine.TimeOf(time.Now()),
		State:  engine.ContainerState{Status: "running", Running: true, StartedAt: engine.TimeOf(time.Now())},
		Config: engine.ContainerConfig{Image: "busybox", Labels: map[string]string{"com.docker.compose.project": "p"}},
	})
}

func writeClient(t *testing.T, f *enginefake.Engine, cfg engine.Config) *engine.Client {
	t.Helper()
	cfg.Host = f.Host()
	c, err := engine.New(cfg)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return c
}

// A command runs on the hijacked stream: stdin in, output out, the exit
// code from exec inspect once it ended.
func TestExecRoundTrip(t *testing.T) {
	f := enginefake.New(t)
	runningContainer(f, "c1", "web-1")
	c := writeClient(t, f, engine.Config{})
	ctx := t.Context()
	id, err := c.CreateExec(ctx, "c1", engine.ExecConfig{Cmd: []string{"sh"}, Tty: true, Size: engine.ConsoleSize{Rows: 24, Cols: 80}})
	require.NoError(t, err)
	conn, err := c.StartExec(ctx, id, true, engine.ConsoleSize{Rows: 24, Cols: 80})
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, c.SetInitialSize(ctx, id, engine.ConsoleSize{Rows: 24, Cols: 80}))
	_, err = io.WriteString(conn, "hello\nexit 3\n")
	require.NoError(t, err)
	out, err := io.ReadAll(conn)
	require.NoError(t, err)
	assert.Contains(t, string(out), "hello")
	var st engine.ExecInspect
	require.Eventually(t, func() bool {
		st, err = c.InspectExec(ctx, id)
		return err == nil && !st.Running
	}, 2*time.Second, 5*time.Millisecond)
	require.NotNil(t, st.ExitCode)
	assert.Equal(t, 3, *st.ExitCode)
	assert.NotZero(t, st.Pid)
	assert.Equal(t, []uint16{24, 80}, f.Execs()[0].ConsoleSize)
	require.NoError(t, c.ResizeExec(ctx, id, 100, 30))
	assert.Equal(t, [][2]uint16{{80, 24}, {100, 30}}, f.Execs()[0].Sizes, "the first size set after the attach, then the resize")
}

// hijack answers the path's request itself on the raw connection.
func hijack(path string, fn func(conn io.ReadWriteCloser, br *bufio.Reader)) enginefake.Hook {
	return func(w http.ResponseWriter, _ *http.Request, p string) bool {
		if p != path {
			return false
		}
		conn, brw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return true
		}
		fn(conn, brw.Reader)
		return true
	}
}

// Bytes the daemon wrote in the same packet as the 101 are not lost.
func TestExecBytesWithTheSwitchAreKept(t *testing.T) {
	f := enginefake.New(t)
	f.AddHook(hijack("/exec/e1/start", func(conn io.ReadWriteCloser, _ *bufio.Reader) {
		_, _ = io.WriteString(conn, "HTTP/1.1 101 UPGRADED\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\nfirst bytes")
		_ = conn.Close()
	}))
	c := writeClient(t, f, engine.Config{})
	conn, err := c.StartExec(t.Context(), "e1", true, engine.ConsoleSize{})
	require.NoError(t, err)
	out, _ := io.ReadAll(conn)
	assert.Equal(t, "first bytes", string(out))
}

// A daemon that never switches: the start fails at the header deadline —
// unknown (the request was sent: the command may have started).
func TestExecStartWithoutASwitchIsUnknownAtTheDeadline(t *testing.T) {
	f := enginefake.New(t)
	release := make(chan struct{})
	defer close(release)
	f.AddHook(hijack("/exec/e1/start", func(conn io.ReadWriteCloser, _ *bufio.Reader) {
		<-release
		_ = conn.Close()
	}))
	c := writeClient(t, f, engine.Config{HeaderTimeout: 200 * time.Millisecond})
	t0 := time.Now()
	_, err := c.StartExec(t.Context(), "e1", true, engine.ConsoleSize{})
	require.Error(t, err)
	assert.Equal(t, provider.ClassUnknown, engine.ClassOf(err))
	assert.Less(t, time.Since(t0), 2*time.Second)
}

// Canceling the context after the switch ends the stream promptly.
func TestExecCancelAfterTheSwitchEndsTheStream(t *testing.T) {
	f := enginefake.New(t)
	runningContainer(f, "c1", "web-1")
	c := writeClient(t, f, engine.Config{})
	ctx, cancel := context.WithCancel(t.Context())
	id, err := c.CreateExec(ctx, "c1", engine.ExecConfig{Cmd: []string{"sh"}, Tty: true})
	require.NoError(t, err)
	conn, err := c.StartExec(ctx, id, true, engine.ConsoleSize{})
	require.NoError(t, err)
	defer conn.Close()
	done := make(chan error, 1)
	go func() { _, err := io.ReadAll(conn); done <- err }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the stream outlived its context")
	}
}

// A start refused before the switch is the daemon's error with its class.
func TestExecRefusedByTheDaemon(t *testing.T) {
	f := enginefake.New(t)
	f.PutContainer(engine.ContainerInspect{ID: "c1", Name: "/web-1", State: engine.ContainerState{Status: "paused", Running: true, Paused: true}})
	c := writeClient(t, f, engine.Config{})
	_, err := c.CreateExec(t.Context(), "c1", engine.ExecConfig{Cmd: []string{"sh"}, Tty: true})
	require.Error(t, err)
	assert.Equal(t, provider.ClassConflict, engine.ClassOf(err))
	assert.Contains(t, err.Error(), "paused")
}

// A change whose request was written but got no answer is unknown, never
// retried; one that could not be sent is not.
func TestAWriteCutAfterItWasSentIsUnknown(t *testing.T) {
	f := enginefake.New(t)
	runningContainer(f, "c1", "web-1")
	f.AddHook(hijack("/containers/c1/stop", func(conn io.ReadWriteCloser, _ *bufio.Reader) {
		_ = conn.Close() // the request was read: a cut before any answer
	}))
	c := writeClient(t, f, engine.Config{})
	_, err := c.StopContainer(t.Context(), "c1", 1)
	require.Error(t, err)
	assert.Equal(t, provider.ClassUnknown, engine.ClassOf(err))
	assert.Equal(t, 1, f.Count("/containers/c1/stop"), "never retried")

	dead, err := engine.New(engine.Config{Host: "tcp://127.0.0.1:1"})
	require.NoError(t, err)
	t.Cleanup(dead.Close)
	_, err = dead.StopContainer(t.Context(), "c1", 1)
	require.Error(t, err)
	assert.Equal(t, provider.ClassUnavailable, engine.ClassOf(err))
}

// A 5xx to a change is unknown (the daemon may have acted).
func TestAServerErrorToAChangeIsUnknown(t *testing.T) {
	f := enginefake.New(t)
	runningContainer(f, "c1", "web-1")
	f.AddHook(func(w http.ResponseWriter, _ *http.Request, p string) bool {
		if p != "/containers/c1/restart" {
			return false
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"message":"tried to kill container, but did not receive an exit event"}`)
		return true
	})
	c := writeClient(t, f, engine.Config{})
	err := c.RestartContainer(t.Context(), "c1", 1)
	require.Error(t, err)
	assert.Equal(t, provider.ClassUnknown, engine.ClassOf(err))
	assert.Contains(t, err.Error(), "did not receive an exit event")
}

// Start/stop that change nothing answer 304: not an error, said.
func TestStartStopNotModified(t *testing.T) {
	f := enginefake.New(t)
	runningContainer(f, "c1", "web-1")
	c := writeClient(t, f, engine.Config{})
	ctx := t.Context()
	nm, err := c.StartContainer(ctx, "c1")
	require.NoError(t, err)
	assert.True(t, nm)
	nm, err = c.StopContainer(ctx, "c1", 2)
	require.NoError(t, err)
	assert.False(t, nm)
	assert.Equal(t, "2", f.Requests()[len(f.Requests())-1].Query.Get("t"))
	nm, err = c.StopContainer(ctx, "c1", 2)
	require.NoError(t, err)
	assert.True(t, nm)
	err = c.RemoveContainer(ctx, "c1")
	require.NoError(t, err)
	_, err = c.InspectContainer(ctx, "c1")
	assert.True(t, engine.IsNotFound(err))
}

func TestRemoveRunningIsRefused(t *testing.T) {
	f := enginefake.New(t)
	runningContainer(f, "c1", "web-1")
	c := writeClient(t, f, engine.Config{})
	err := c.RemoveContainer(t.Context(), "c1")
	require.Error(t, err)
	assert.Equal(t, provider.ClassConflict, engine.ClassOf(err))
	assert.Contains(t, err.Error(), "container is running")
	q := f.Requests()[len(f.Requests())-1].Query
	assert.Equal(t, "", q.Get("force")+q.Get("v"), "neither force nor volumes")
}

func TestContainerStatsDecode(t *testing.T) {
	f := enginefake.New(t)
	runningContainer(f, "c1", "web-1")
	c := writeClient(t, f, engine.Config{})
	ctx := t.Context()
	// the first two-point sample is complete by itself (the daemon takes both)
	two, err := c.ContainerStats(ctx, "c1", false)
	require.NoError(t, err)
	assert.Equal(t, int64(5e8), two.PreCPUStats.CPUUsage.TotalUsage)
	assert.Equal(t, int64(10e8), two.CPUStats.CPUUsage.TotalUsage)
	assert.Equal(t, time.Second, two.Read.Sub(two.PreRead))
	q := f.Requests()[len(f.Requests())-1].Query
	assert.Equal(t, "false", q.Get("stream"))
	assert.Empty(t, q.Get("one-shot"))
	one, err := c.ContainerStats(ctx, "c1", true)
	require.NoError(t, err)
	assert.Equal(t, int64(15e8), one.CPUStats.CPUUsage.TotalUsage)
	assert.Zero(t, one.PreCPUStats.SystemCPUUsage)
	assert.True(t, one.PreRead.IsZero())
	assert.Equal(t, 2, one.CPUStats.OnlineCPUs)
	assert.Equal(t, int64(64<<20), one.MemoryStats.Usage)
	assert.Equal(t, int64(16<<20), one.MemoryStats.Stats["inactive_file"])
	assert.False(t, one.Read.IsZero())
	assert.Equal(t, "true", f.Requests()[len(f.Requests())-1].Query.Get("one-shot"))
}

// errors.Is works through the classified error.
func TestUnknownKeepsItsCause(t *testing.T) {
	f := enginefake.New(t)
	runningContainer(f, "c1", "web-1")
	f.AddHook(hijack("/containers/c1/start", func(conn io.ReadWriteCloser, _ *bufio.Reader) {
		_ = conn.Close() // the request was read: a cut before any answer
	}))
	c := writeClient(t, f, engine.Config{})
	_, err := c.StartContainer(t.Context(), "c1")
	var ee *engine.Error
	require.True(t, errors.As(err, &ee))
	assert.True(t, strings.Contains(ee.Message, "may"), ee.Message)
}

// An exit code the daemon does not have is nil, not a false 0; a real 0
// is 0. A command that never started is 127 with Pid 0.
func TestExecExitCodeIsNullable(t *testing.T) {
	f := enginefake.New(t)
	runningContainer(f, "c1", "web-1")
	f.AddHook(func(w http.ResponseWriter, _ *http.Request, p string) bool {
		if p != "/exec/nocode/json" {
			return false
		}
		_, _ = io.WriteString(w, `{"ID":"nocode","Running":false,"ExitCode":null,"Pid":0}`)
		return true
	})
	c := writeClient(t, f, engine.Config{})
	ctx := t.Context()
	st, err := c.InspectExec(ctx, "nocode")
	require.NoError(t, err)
	assert.Nil(t, st.ExitCode)

	run := func(cmd []string, input string) engine.ExecInspect {
		id, err := c.CreateExec(ctx, "c1", engine.ExecConfig{Cmd: cmd, Tty: true})
		require.NoError(t, err)
		st, err := c.InspectExec(ctx, id)
		require.NoError(t, err)
		assert.Nil(t, st.ExitCode, "no code before the start")
		conn, err := c.StartExec(ctx, id, true, engine.ConsoleSize{})
		require.NoError(t, err)
		defer conn.Close()
		_, _ = io.WriteString(conn, input)
		_, _ = io.ReadAll(conn)
		require.Eventually(t, func() bool {
			st, err = c.InspectExec(ctx, id)
			return err == nil && !st.Running
		}, 2*time.Second, 5*time.Millisecond)
		return st
	}
	st = run([]string{"sh"}, "exit 0\n")
	require.NotNil(t, st.ExitCode)
	assert.Equal(t, 0, *st.ExitCode)
	st = run([]string{"sh"}, "exit 127\n")
	require.NotNil(t, st.ExitCode)
	assert.Equal(t, 127, *st.ExitCode)
	assert.NotZero(t, st.Pid, "a process that ran and exited 127")
	st = run([]string{"/nonexistent/sh"}, "")
	require.NotNil(t, st.ExitCode)
	assert.Equal(t, 127, *st.ExitCode)
	assert.Zero(t, st.Pid, "never started")
}

// A live stream outlives the header deadline: only its context or Close
// end it.
func TestExecStreamOutlivesTheHeaderDeadline(t *testing.T) {
	f := enginefake.New(t)
	runningContainer(f, "c1", "web-1")
	c := writeClient(t, f, engine.Config{HeaderTimeout: 100 * time.Millisecond})
	ctx := t.Context()
	id, err := c.CreateExec(ctx, "c1", engine.ExecConfig{Cmd: []string{"sh"}, Tty: true})
	require.NoError(t, err)
	conn, err := c.StartExec(ctx, id, true, engine.ConsoleSize{})
	require.NoError(t, err)
	defer conn.Close()
	time.Sleep(300 * time.Millisecond)
	_, err = io.WriteString(conn, "still here\n")
	require.NoError(t, err)
	br := bufio.NewReader(conn)
	seen := ""
	for !strings.Contains(seen, "still here") {
		line, err := br.ReadString('\n')
		require.NoError(t, err)
		seen += line
	}
}

// Close of the handle ends a blocked read (Client.Close does not: it only
// drops idle connections).
func TestExecCloseEndsABlockedRead(t *testing.T) {
	f := enginefake.New(t)
	runningContainer(f, "c1", "web-1")
	c := writeClient(t, f, engine.Config{})
	ctx := t.Context()
	id, err := c.CreateExec(ctx, "c1", engine.ExecConfig{Cmd: []string{"sh"}, Tty: true})
	require.NoError(t, err)
	conn, err := c.StartExec(ctx, id, true, engine.ConsoleSize{})
	require.NoError(t, err)
	c.Close()
	done := make(chan struct{})
	go func() { _, _ = io.ReadAll(conn); close(done) }()
	select {
	case <-done:
		t.Fatal("Client.Close ended a live exec")
	case <-time.After(100 * time.Millisecond):
	}
	require.NoError(t, conn.Close())
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not end the read")
	}
	assert.NoError(t, conn.Close(), "a second Close is harmless")
}

// The caller giving up while the daemon holds an accepted start: a fast
// local return, unknown (the command may run), no second start.
func TestExecCanceledBeforeTheSwitchIsUnknownAndFast(t *testing.T) {
	f := enginefake.New(t)
	release := make(chan struct{})
	defer close(release)
	f.AddHook(hijack("/exec/e1/start", func(conn io.ReadWriteCloser, _ *bufio.Reader) {
		<-release // accepted, never switched
		_ = conn.Close()
	}))
	c := writeClient(t, f, engine.Config{})
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(100*time.Millisecond, cancel)
	t0 := time.Now()
	_, err := c.StartExec(ctx, "e1", true, engine.ConsoleSize{})
	require.Error(t, err)
	assert.Equal(t, provider.ClassUnknown, engine.ClassOf(err))
	assert.Less(t, time.Since(t0), time.Second)
	assert.Equal(t, 1, f.Count("/exec/e1/start"))
}

// API 1.41 has no ConsoleSize on create/start: the first size comes by
// SetInitialSize right after the attach, with no resize from the terminal.
func TestExecFirstSizeOnAnOldAPI(t *testing.T) {
	f := enginefake.New(t, enginefake.WithAPIVersion("1.41"))
	runningContainer(f, "c1", "web-1")
	c := writeClient(t, f, engine.Config{})
	ctx := t.Context()
	size := engine.ConsoleSize{Rows: 40, Cols: 132}
	id, err := c.CreateExec(ctx, "c1", engine.ExecConfig{Cmd: []string{"sh"}, Tty: true, Size: size})
	require.NoError(t, err)
	conn, err := c.StartExec(ctx, id, true, size)
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, c.SetInitialSize(ctx, id, size))
	x := f.Execs()[0]
	assert.Nil(t, x.ConsoleSize, "not sent to 1.41")
	assert.Equal(t, [][2]uint16{{132, 40}}, x.Sizes)
}

// A stop whose answer does not come within its wait is unknown (the
// daemon goes on stopping); the wait is the stop timeout plus the request
// timeout, and a minute for "never kill".
func TestStopPastItsWaitIsUnknown(t *testing.T) {
	f := enginefake.New(t)
	runningContainer(f, "c1", "web-1")
	release := make(chan struct{})
	defer close(release)
	f.AddHook(func(_ http.ResponseWriter, r *http.Request, p string) bool {
		if p != "/containers/c1/stop" {
			return false
		}
		assert.Equal(t, "0", r.URL.Query().Get("t"))
		<-release
		return true
	})
	c := writeClient(t, f, engine.Config{RequestTimeout: 200 * time.Millisecond})
	t0 := time.Now()
	_, err := c.StopContainer(t.Context(), "c1", 0)
	require.Error(t, err)
	assert.Equal(t, provider.ClassUnknown, engine.ClassOf(err))
	assert.Less(t, time.Since(t0), 2*time.Second)
	assert.Equal(t, 5*time.Second+200*time.Millisecond, c.StopWait(5))
	assert.Equal(t, time.Minute, c.StopWait(-1))
	_, err = c.StopContainer(t.Context(), "gone", -1)
	assert.True(t, engine.IsNotFound(err))
	assert.Equal(t, "-1", f.Requests()[len(f.Requests())-1].Query.Get("t"))
}

// The daemon checks the container again at the start: paused between
// create and start → refused, nothing ran.
func TestExecStartRechecksTheContainer(t *testing.T) {
	f := enginefake.New(t)
	runningContainer(f, "c1", "web-1")
	c := writeClient(t, f, engine.Config{})
	ctx := t.Context()
	id, err := c.CreateExec(ctx, "c1", engine.ExecConfig{Cmd: []string{"sh"}, Tty: true})
	require.NoError(t, err)
	f.PutContainer(engine.ContainerInspect{ID: "c1", Name: "/web-1", State: engine.ContainerState{Status: "paused", Running: true, Paused: true}})
	_, err = c.StartExec(ctx, id, true, engine.ConsoleSize{})
	assert.Equal(t, provider.ClassConflict, engine.ClassOf(err))
	assert.False(t, f.Execs()[0].Started)
}

// A reset right after the start was sent is unknown with its own cause —
// not a made-up timeout.
func TestExecResetAfterSendKeepsItsCause(t *testing.T) {
	f := enginefake.New(t)
	f.AddHook(hijack("/exec/e1/start", func(conn io.ReadWriteCloser, _ *bufio.Reader) { _ = conn.Close() }))
	c := writeClient(t, f, engine.Config{})
	_, err := c.Ping(t.Context())
	require.NoError(t, err)
	_, err = c.StartExec(t.Context(), "e1", true, engine.ConsoleSize{})
	require.Error(t, err)
	assert.Equal(t, provider.ClassUnknown, engine.ClassOf(err))
	assert.NotErrorIs(t, err, context.DeadlineExceeded)
	assert.NotContains(t, err.Error(), "no answer within")
}

// The first size has one small budget as a whole; a final refusal is not
// tried again, and the error says the size was not set.
func TestSetInitialSizeIsBounded(t *testing.T) {
	f := enginefake.New(t)
	release := make(chan struct{})
	defer close(release)
	f.AddHook(func(w http.ResponseWriter, _ *http.Request, p string) bool {
		switch p {
		case "/exec/hang/resize":
			<-release
			return true
		case "/exec/denied/resize":
			w.WriteHeader(http.StatusForbidden)
			return true
		case "/exec/busy/resize":
			w.WriteHeader(http.StatusConflict)
			return true
		}
		return false
	})
	c := writeClient(t, f, engine.Config{})
	ctx := t.Context()
	size := engine.ConsoleSize{Rows: 24, Cols: 80}
	t0 := time.Now()
	require.Error(t, c.SetInitialSize(ctx, "hang", size))
	assert.Less(t, time.Since(t0), engine.InitialSizeBudget+time.Second)
	require.Error(t, c.SetInitialSize(ctx, "denied", size))
	assert.Equal(t, 1, f.Count("/exec/denied/resize"))
	require.Error(t, c.SetInitialSize(ctx, "busy", size))
	assert.Equal(t, 5, f.Count("/exec/busy/resize"))
}
