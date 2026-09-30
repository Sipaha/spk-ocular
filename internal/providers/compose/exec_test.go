package compose

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/compose/engine"
	"github.com/spk/spk-ocular/internal/providers/compose/enginefake"
)

// replica is a service's container with its replica number.
func replica(id, project, service, number, state string) engine.ContainerInspect {
	c := composeContainer(id, project, service, state)
	c.Config.Labels[LabelNumber] = number
	c.Name = "/" + project + "-" + service + "-" + number
	return c
}

func svcRef(project, service string) core.Ref {
	return core.Ref{Provider: ProviderID, Target: "context:test", Scope: project, Kind: KindServices, Name: project + "/" + service}
}

func ctrRef(c engine.ContainerInspect) core.Ref {
	return core.Ref{Provider: ProviderID, Target: "context:test", Scope: projectOf(&c), Kind: KindContainers, Name: c.ID, UID: c.ID}
}

// sizes is a TermSizes fed by the test; Next blocks until a size or close.
type sizes struct{ ch chan *provider.TermSize }

func newSizes(first provider.TermSize) *sizes {
	s := &sizes{ch: make(chan *provider.TermSize, 8)}
	s.ch <- &first
	return s
}

func (s *sizes) Next() *provider.TermSize { return <-s.ch }

// syncBuf is a goroutine-safe output.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func run(t *testing.T, h provider.ExecHandle, input string, sz *sizes) (provider.ExitStatus, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var out syncBuf
	if sz == nil {
		sz = newSizes(provider.TermSize{Cols: 80, Rows: 24})
	}
	st, err := h.Run(ctx, provider.Terminal{Stdin: strings.NewReader(input), Stdout: &out, Sizes: sz})
	return st, out.String(), err
}

func TestExecInfoOfAContainer(t *testing.T) {
	e := newTestEnv(t)
	up := replica("aaaa1111", "p", "web", "1", "running")
	down := replica("bbbb2222", "p", "web", "2", "exited")
	e.fe.PutContainer(up)
	e.fe.PutContainer(down)
	info, err := e.s.ExecInfo(t.Context(), ctrRef(up))
	require.NoError(t, err)
	require.Len(t, info.Instances, 1)
	assert.Equal(t, up.ID, info.Instances[0].ID)
	assert.Equal(t, "p-web-1", info.Instances[0].Title)
	assert.Equal(t, up.ID, info.DefaultInstance)
	assert.Empty(t, info.Instances[0].Channels, "no channel level")
	require.NotNil(t, info.InstanceLabel)
	assert.Equal(t, "compose.level.container", info.InstanceLabel.Key)

	info, err = e.s.ExecInfo(t.Context(), ctrRef(down))
	require.NoError(t, err)
	assert.Empty(t, info.Instances)
	require.NotNil(t, info.NoInstances)
	assert.Equal(t, "The container is exited", info.NoInstances.Text)
}

// A service offers its runnable containers (not one-off, not paused) in
// replica order; the default is the first ready one.
func TestExecInfoOfAService(t *testing.T) {
	e := newTestEnv(t)
	one := replica("aaaa1111", "p", "web", "1", "running")
	one.State.Health = &engine.Health{Status: "unhealthy"}
	two := replica("bbbb2222", "p", "web", "2", "running")
	three := replica("cccc3333", "p", "web", "3", "exited")
	four := replica("dddd4444", "p", "web", "4", "running")
	four.State.Paused = true
	oneoff := replica("eeee5555", "p", "web", "1", "running")
	oneoff.Config.Labels[LabelOneoff] = "True"
	other := replica("ffff6666", "p", "db", "1", "running")
	for _, c := range []engine.ContainerInspect{four, three, two, one, oneoff, other} {
		e.fe.PutContainer(c)
	}
	info, err := e.s.ExecInfo(t.Context(), svcRef("p", "web"))
	require.NoError(t, err)
	var ids []string
	for _, i := range info.Instances {
		ids = append(ids, i.ID)
	}
	assert.Equal(t, []string{one.ID, two.ID}, ids)
	assert.False(t, info.Instances[0].Ready, "unhealthy")
	assert.Equal(t, two.ID, info.DefaultInstance, "the first ready one")

	info, err = e.s.ExecInfo(t.Context(), svcRef("p", "nothing"))
	require.NoError(t, err)
	assert.Empty(t, info.Instances)
	require.NotNil(t, info.NoInstances)
	assert.Equal(t, "compose.exec.noRunning", info.NoInstances.Key)
}

// A chosen instance must be a runnable member of the service, read fresh.
func TestExecExplicitInstanceIsChecked(t *testing.T) {
	e := newTestEnv(t)
	web := replica("aaaa1111", "p", "web", "1", "running")
	db := replica("bbbb2222", "p", "db", "1", "running")
	oneoff := replica("cccc3333", "p", "web", "1", "running")
	oneoff.Config.Labels[LabelOneoff] = "True"
	stopped := replica("dddd4444", "p", "web", "2", "exited")
	for _, c := range []engine.ContainerInspect{web, db, oneoff, stopped} {
		e.fe.PutContainer(c)
	}
	ctx := t.Context()
	for _, c := range []struct {
		instance string
		class    provider.ErrorClass
	}{
		{db.ID, provider.ClassGone},
		{oneoff.ID, provider.ClassGone},
		{"aaaa", provider.ClassGone}, // a prefix is not the pinned full id
		{"nope", provider.ClassGone},
		{stopped.ID, provider.ClassInvalid},
	} {
		_, err := e.s.PrepareExec(ctx, svcRef("p", "web"), provider.ExecRequest{Instance: c.instance})
		assert.Equal(t, c.class, errClass(err), "%s: %v", c.instance, err)
	}
	_, err := e.s.PrepareExec(ctx, svcRef("p", "web"), provider.ExecRequest{Instance: web.ID, Channel: "x"})
	assert.Equal(t, provider.ClassInvalid, errClass(err))
	_, err = e.s.PrepareExec(ctx, ctrRef(web), provider.ExecRequest{Instance: db.ID})
	assert.Equal(t, provider.ClassInvalid, errClass(err))
	_, err = e.s.PrepareExec(ctx, svcRef("p", "none"), provider.ExecRequest{})
	assert.Equal(t, provider.ClassUnavailable, errClass(err))
	h, err := e.s.PrepareExec(ctx, svcRef("p", "web"), provider.ExecRequest{Instance: web.ID})
	require.NoError(t, err)
	h.Close()
}

// The default shell runs in the pinned container with the terminal's
// first size; output, resizes and the exit code come through.
func TestExecRunsTheShell(t *testing.T) {
	e := newTestEnv(t)
	web := replica("aaaa1111", "p", "web", "1", "running")
	e.fe.PutContainer(web)
	h, err := e.s.PrepareExec(t.Context(), svcRef("p", "web"), provider.ExecRequest{})
	require.NoError(t, err)
	defer h.Close()
	d := h.Describe()
	assert.Equal(t, "p-web-1", d.Instance)
	assert.Empty(t, d.Channel)
	assert.Empty(t, d.Command, "the shell is not a command")
	assert.NotEmpty(t, d.Endpoint)

	sz := newSizes(provider.TermSize{Cols: 100, Rows: 30})
	r, w := io.Pipe()
	var out syncBuf
	done := make(chan struct{})
	var st provider.ExitStatus
	go func() {
		defer close(done)
		st, err = h.Run(t.Context(), provider.Terminal{Stdin: r, Stdout: &out, Sizes: sz})
	}()
	_, _ = io.WriteString(w, "hi\n")
	require.Eventually(t, func() bool { return strings.Contains(out.String(), "hi") }, 5*time.Second, 5*time.Millisecond)
	sz.ch <- &provider.TermSize{Cols: 120, Rows: 40}
	require.Eventually(t, func() bool { return len(e.fe.Execs()) == 1 && len(e.fe.Execs()[0].Sizes) == 2 }, 5*time.Second, 5*time.Millisecond)
	_, _ = io.WriteString(w, "exit 3\n")
	<-done
	require.NoError(t, err)
	assert.Equal(t, provider.ExitStatus{Code: 3, Known: true}, st)
	x := e.fe.Execs()[0]
	assert.Equal(t, web.ID, x.Container)
	assert.Equal(t, execShell, x.Cmd)
	assert.True(t, x.Tty)
	assert.Equal(t, [][2]uint16{{100, 30}, {120, 40}}, x.Sizes)
}

// Each run is a new exec; a copy (Again) keeps working after the session
// and the prototype are closed.
func TestExecHandleOutlivesTheSession(t *testing.T) {
	e := newTestEnv(t)
	web := replica("aaaa1111", "p", "web", "1", "running")
	e.fe.PutContainer(web)
	proto, err := e.s.PrepareExec(t.Context(), ctrRef(web), provider.ExecRequest{Command: []string{"sh"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"sh"}, proto.Describe().Command)
	h1, err := proto.Again()
	require.NoError(t, err)
	st, _, err := run(t, h1, "exit 1\n", nil)
	require.NoError(t, err)
	assert.Equal(t, 1, st.Code)
	h1.Close()
	e.s.Close()
	h2, err := proto.Again()
	require.NoError(t, err)
	proto.Close()
	st, _, err = run(t, h2, "exit 2\n", nil)
	require.NoError(t, err)
	assert.Equal(t, provider.ExitStatus{Code: 2, Known: true}, st)
	h2.Close()
	h2.Close() // harmless
	assert.Len(t, e.fe.Execs(), 2)
}

// The pinned container is re-read on every run: removed → gone; paused →
// refused; never another container.
func TestExecRunChecksThePinnedContainer(t *testing.T) {
	e := newTestEnv(t)
	web := replica("aaaa1111", "p", "web", "1", "running")
	e.fe.PutContainer(web)
	h, err := e.s.PrepareExec(t.Context(), svcRef("p", "web"), provider.ExecRequest{})
	require.NoError(t, err)
	defer h.Close()
	paused := web
	paused.State.Paused = true
	e.fe.PutContainer(paused)
	_, _, err = run(t, h, "", nil)
	assert.Equal(t, provider.ClassInvalid, errClass(err))
	assert.Contains(t, err.Error(), "paused")
	e.fe.RemoveContainer(web.ID)
	e.fe.PutContainer(replica("ffff9999", "p", "web", "1", "running")) // its replacement
	_, _, err = run(t, h, "", nil)
	assert.Equal(t, provider.ClassGone, errClass(err))
	assert.Empty(t, e.fe.Execs(), "nothing ran anywhere")
}

// No exit code from the daemon: ended, the code unknown — not 0.
func TestExecUnknownExitCode(t *testing.T) {
	e := newTestEnv(t)
	web := replica("aaaa1111", "p", "web", "1", "running")
	e.fe.PutContainer(web)
	e.fe.AddHook(func(w http.ResponseWriter, _ *http.Request, p string) bool {
		if !strings.HasPrefix(p, "/exec/") || !strings.HasSuffix(p, "/json") {
			return false
		}
		_, _ = io.WriteString(w, `{"Running":false,"ExitCode":null,"Pid":42}`)
		return true
	})
	h, err := e.s.PrepareExec(t.Context(), ctrRef(web), provider.ExecRequest{})
	require.NoError(t, err)
	defer h.Close()
	st, _, err := run(t, h, "exit 0\n", nil)
	require.NoError(t, err)
	assert.False(t, st.Known)
}

// A command that never started (no pid, 126/127) is refused with the
// daemon's text shown; a real exit 127 is just a code.
func TestExecCommandThatNeverStarted(t *testing.T) {
	e := newTestEnv(t)
	web := replica("aaaa1111", "p", "web", "1", "running")
	e.fe.PutContainer(web)
	h, err := e.s.PrepareExec(t.Context(), ctrRef(web), provider.ExecRequest{Command: []string{"/nonexistent/tool"}})
	require.NoError(t, err)
	defer h.Close()
	_, out, err := run(t, h, "", nil)
	assert.Equal(t, provider.ClassInvalid, errClass(err))
	assert.Contains(t, err.Error(), "/nonexistent/tool")
	assert.Contains(t, out, "no such file or directory")

	e.fe.SetExec(func(_ []string, _ bool, rw io.ReadWriter, _ <-chan [2]uint16) int {
		_, _ = io.WriteString(rw, "exec: \"sh\": executable file not found in $PATH\r\n")
		return enginefake.NotStarted(126)
	})
	sh, err := e.s.PrepareExec(t.Context(), ctrRef(web), provider.ExecRequest{})
	require.NoError(t, err)
	defer sh.Close()
	_, _, err = run(t, sh, "", nil)
	assert.Equal(t, provider.ClassInvalid, errClass(err))
	assert.Contains(t, err.Error(), "no sh")

	e.fe.SetExec(func(_ []string, _ bool, _ io.ReadWriter, _ <-chan [2]uint16) int { return 127 })
	st, _, err := run(t, sh, "", nil)
	require.NoError(t, err)
	assert.Equal(t, provider.ExitStatus{Code: 127, Known: true}, st)
}

// A start cut after it was sent is unknown and never repeated (no second
// exec, no second start).
func TestExecAmbiguousStartIsNotRepeated(t *testing.T) {
	e := newTestEnv(t)
	web := replica("aaaa1111", "p", "web", "1", "running")
	e.fe.PutContainer(web)
	e.fe.AddHook(func(w http.ResponseWriter, _ *http.Request, p string) bool {
		if !strings.HasSuffix(p, "/start") || !strings.HasPrefix(p, "/exec/") {
			return false
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
		return true
	})
	h, err := e.s.PrepareExec(t.Context(), ctrRef(web), provider.ExecRequest{})
	require.NoError(t, err)
	defer h.Close()
	_, _, err = run(t, h, "", nil)
	assert.Equal(t, provider.ClassUnknown, errClass(err))
	assert.Contains(t, err.Error(), "may be running")
	assert.Len(t, e.fe.Execs(), 1)
	assert.Equal(t, 1, e.fe.Count("/exec/exec1/start"))
}

// A terminal closed during the handshake: Run returns promptly.
func TestExecCanceledDuringTheHandshake(t *testing.T) {
	e := newTestEnv(t)
	web := replica("aaaa1111", "p", "web", "1", "running")
	e.fe.PutContainer(web)
	release := make(chan struct{})
	defer close(release)
	e.fe.AddHook(func(_ http.ResponseWriter, _ *http.Request, p string) bool {
		if !strings.HasSuffix(p, "/start") || !strings.HasPrefix(p, "/exec/") {
			return false
		}
		<-release
		return true
	})
	h, err := e.s.PrepareExec(t.Context(), ctrRef(web), provider.ExecRequest{})
	require.NoError(t, err)
	defer h.Close()
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(100*time.Millisecond, cancel)
	t0 := time.Now()
	_, err = h.Run(ctx, provider.Terminal{Stdin: strings.NewReader(""), Stdout: io.Discard, Sizes: newSizes(provider.TermSize{Cols: 80, Rows: 24})})
	require.Error(t, err)
	assert.Less(t, time.Since(t0), 2*time.Second)
}

// Closing the terminal mid-command ends Run promptly.
func TestExecCanceledWhileRunning(t *testing.T) {
	e := newTestEnv(t)
	web := replica("aaaa1111", "p", "web", "1", "running")
	e.fe.PutContainer(web)
	h, err := e.s.PrepareExec(t.Context(), ctrRef(web), provider.ExecRequest{})
	require.NoError(t, err)
	defer h.Close()
	ctx, cancel := context.WithCancel(t.Context())
	r, w := io.Pipe()
	defer w.Close()
	var out syncBuf
	done := make(chan error, 1)
	go func() {
		_, err := h.Run(ctx, provider.Terminal{Stdin: r, Stdout: &out, Sizes: newSizes(provider.TermSize{Cols: 80, Rows: 24})})
		done <- err
	}()
	require.Eventually(t, func() bool { return strings.Contains(out.String(), "$ ") }, 5*time.Second, 5*time.Millisecond)
	cancel()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("Run outlived its context")
	}
}
