package compose

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/compose/engine"
)

var _ provider.Execer = (*session)(nil)

// Commands run in a running container: the container itself, or one of a
// service's. There is no channel level (a container is one process tree).

// execShell finds a shell without relying on /bin/sh's path: bash, then
// ash, then sh (exec replaces the probing sh; the same as for pods).
var execShell = []string{"sh", "-c", "if command -v bash >/dev/null 2>&1; then exec bash; elif command -v ash >/dev/null 2>&1; then exec ash; else exec sh; fi"}

// Exec timing (vars for tests): how long the end of a command is looked
// for in exec inspect after its stream closed.
var (
	execEndWait = 2 * time.Second
	execEndPoll = 20 * time.Millisecond
)

// execRunnable: a command can start in c now (the daemon refuses paused,
// restarting and stopped containers).
func execRunnable(c *engine.ContainerInspect) bool {
	return c.State.Running && !c.State.Paused && !c.State.Restarting
}

// execReady: running and not known to be unhealthy or still starting.
func execReady(c *engine.ContainerInspect) bool {
	return execRunnable(c) && (c.State.Health == nil || c.State.Health.Status == "healthy")
}

func execState(c *engine.ContainerInspect) string {
	switch {
	case c.State.Paused:
		return "paused"
	case c.State.Restarting:
		return "restarting"
	}
	return c.State.Status
}

// execCandidates are ref's containers a command may run in: the container
// itself (read fresh) or the service's members (from the feed), runnable
// or not.
func (s *session) execCandidates(ctx context.Context, ref core.Ref) ([]*engine.ContainerInspect, error) {
	switch ref.Kind {
	case KindContainers:
		c, err := s.container(ctx, ref)
		if err != nil {
			return nil, err
		}
		return []*engine.ContainerInspect{c}, nil
	case KindServices:
		ms, err := s.serviceMembers(ctx, ref)
		var pe *provider.Error
		if errors.As(err, &pe) && pe.Class == provider.ClassNotFound {
			return nil, nil // no containers: nowhere to run
		}
		return ms, err
	}
	return nil, &provider.Error{Class: provider.ClassUnsupported, Message: fmt.Sprintf("commands cannot run in %s", ref.Kind)}
}

// ExecInfo: the runnable containers of ref as instances, in replica order;
// the default is the first ready one.
func (s *session) ExecInfo(ctx context.Context, ref core.Ref) (core.ExecInfo, error) {
	cs, err := s.execCandidates(ctx, ref)
	if err != nil {
		return core.ExecInfo{}, err
	}
	label := msg("level.container")
	info := core.ExecInfo{InstanceLabel: &label}
	for _, c := range cs {
		if !execRunnable(c) {
			continue
		}
		info.Instances = append(info.Instances, core.ExecInstance{ID: c.ID, Title: containerName(c), Ready: execReady(c)})
		if info.DefaultInstance == "" && execReady(c) {
			info.DefaultInstance = c.ID
		}
	}
	if len(info.Instances) > 0 && info.DefaultInstance == "" {
		info.DefaultInstance = info.Instances[0].ID
	}
	if len(info.Instances) == 0 {
		none := msg("exec.noRunning")
		if ref.Kind == KindContainers && len(cs) == 1 {
			none = msg("exec.notRunning", "state", execState(cs[0]))
		}
		info.NoInstances = &none
	}
	return info, nil
}

// PrepareExec pins the container (its full id) and argv, and gives the
// handle its own Engine client (the session's may be closed meanwhile).
func (s *session) PrepareExec(ctx context.Context, ref core.Ref, req provider.ExecRequest) (provider.ExecHandle, error) {
	if req.Channel != "" {
		return nil, &provider.Error{Class: provider.ClassInvalid, Message: "containers have no channels to choose"}
	}
	c, err := s.execInstance(ctx, ref, req.Instance)
	if err != nil {
		return nil, err
	}
	cl, err := engine.New(s.cl.Config())
	if err != nil {
		return nil, providerError(err)
	}
	argv := req.Command
	if len(argv) == 0 {
		argv = execShell
	}
	conn := &execConn{cl: cl}
	conn.refs.Store(1)
	return &execHandle{
		conn: conn, ref: ref, target: s.target, title: s.title, hash: s.hash,
		id: c.ID, name: containerName(c), argv: argv, shell: len(req.Command) == 0,
	}, nil
}

// execInstance is the container a command will run in: the default one of
// ExecInfo, or the chosen one read fresh and checked to be ref's (the
// container itself, or a member of the service — not a one-off) and
// runnable. A stale choice is refused, never replaced by another.
func (s *session) execInstance(ctx context.Context, ref core.Ref, instance string) (*engine.ContainerInspect, error) {
	if instance == "" {
		info, err := s.ExecInfo(ctx, ref)
		if err != nil {
			return nil, err
		}
		if info.DefaultInstance == "" {
			m := info.NoInstances
			return nil, &provider.Error{Class: provider.ClassUnavailable, Message: m.Text}
		}
		instance = info.DefaultInstance
	}
	var c *engine.ContainerInspect
	switch ref.Kind {
	case KindContainers:
		if instance != ref.Name && instance != ref.UID {
			return nil, &provider.Error{Class: provider.ClassInvalid, Message: fmt.Sprintf("instance %q is not the container %s", instance, shown(ref))}
		}
		var err error
		if c, err = s.container(ctx, ref); err != nil {
			return nil, err
		}
	case KindServices:
		project, service, ok := strings.Cut(ref.Name, "/")
		if !ok {
			return nil, &provider.Error{Class: provider.ClassInvalid, Message: fmt.Sprintf("%q is not a service key (project/service)", ref.Name)}
		}
		got, err := s.cl.InspectContainer(ctx, instance)
		switch {
		case engine.IsNotFound(err):
			return nil, &provider.Error{Class: provider.ClassGone, Message: "the chosen container no longer exists"}
		case err != nil:
			return nil, providerError(err)
		}
		l := got.Config.Labels
		if got.ID != instance || l[LabelProject] != project || l[LabelService] != service || l[LabelOneoff] == "True" {
			return nil, &provider.Error{Class: provider.ClassGone, Message: fmt.Sprintf("container %s is not (any more) a container of the service %s", containerName(&got), service)}
		}
		c = &got
	default:
		return nil, &provider.Error{Class: provider.ClassUnsupported, Message: fmt.Sprintf("commands cannot run in %s", ref.Kind)}
	}
	if !execRunnable(c) {
		return nil, &provider.Error{Class: provider.ClassInvalid, Message: fmt.Sprintf("container %s is %s", containerName(c), execState(c))}
	}
	return c, nil
}

// execConn is the Engine client of a terminal's handles (the prepared one
// and its runs): closed with the last of them.
type execConn struct {
	cl   *engine.Client
	refs atomic.Int32
}

func (c *execConn) release() {
	if c.refs.Add(-1) == 0 {
		c.cl.Close()
	}
}

// execHandle runs one command in a pinned container.
type execHandle struct {
	conn          *execConn
	ref           core.Ref
	target, title string
	hash          string
	id, name      string // the container's full id and name
	argv          []string
	shell         bool // argv is execShell
	closeOnce     sync.Once
}

func (h *execHandle) Describe() core.LiveTarget {
	t := core.LiveTarget{
		Provider: ProviderID, Target: h.target, TargetTitle: h.title, Endpoint: h.conn.cl.Endpoint(),
		ConfigHash: h.hash, Ref: h.ref, Instance: h.name,
	}
	if !h.shell {
		t.Command = h.argv
	}
	return t
}

func (h *execHandle) Again() (provider.ExecHandle, error) {
	h.conn.refs.Add(1)
	return &execHandle{
		conn: h.conn, ref: h.ref, target: h.target, title: h.title, hash: h.hash,
		id: h.id, name: h.name, argv: h.argv, shell: h.shell,
	}, nil
}

func (h *execHandle) Close() { h.closeOnce.Do(h.conn.release) }

// Run starts a new exec in the pinned container (re-read first: removed
// → gone, not runnable → refused), streams it, and reads its exit code
// once the stream ended. A start that may have reached the daemon is never
// repeated (unknown: the command may be running).
func (h *execHandle) Run(ctx context.Context, t provider.Terminal) (provider.ExitStatus, error) {
	cl := h.conn.cl
	c, err := cl.InspectContainer(ctx, h.id)
	switch {
	case engine.IsNotFound(err):
		return provider.ExitStatus{}, &provider.Error{Class: provider.ClassGone, Message: fmt.Sprintf("container %s no longer exists", h.name)}
	case err != nil:
		return provider.ExitStatus{}, providerError(err)
	case !execRunnable(&c):
		return provider.ExitStatus{}, &provider.Error{Class: provider.ClassInvalid, Message: fmt.Sprintf("container %s is %s", h.name, execState(&c))}
	}
	var size engine.ConsoleSize
	if t.Sizes != nil {
		sz := t.Sizes.Next()
		if sz == nil {
			return provider.ExitStatus{}, context.Canceled // the terminal is gone
		}
		size = engine.ConsoleSize{Rows: sz.Rows, Cols: sz.Cols}
	}
	xid, err := cl.CreateExec(ctx, h.id, engine.ExecConfig{Cmd: h.argv, Tty: true, Size: size})
	if err != nil {
		return provider.ExitStatus{}, h.startError(err)
	}
	xc, err := cl.StartExec(ctx, xid, true, size)
	if err != nil {
		return provider.ExitStatus{}, h.startError(err)
	}
	defer func() { _ = xc.Close() }()

	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	if t.Sizes != nil {
		// Sizes in order, off the stream's path: the first (older APIs
		// ignore the one sent with the start; failing, the TTY keeps the
		// daemon's until the next), then each change.
		go func() {
			_ = cl.SetInitialSize(runCtx, xid, size)
			for {
				sz := t.Sizes.Next()
				if sz == nil || runCtx.Err() != nil {
					return
				}
				_ = cl.ResizeExec(runCtx, xid, sz.Cols, sz.Rows) // a late one (the command ended) is moot
			}
		}()
	}
	if t.Stdin != nil {
		go func() { _, _ = io.Copy(xc, t.Stdin) }()
	}
	_, copyErr := io.Copy(t.Stdout, xc)
	if ctx.Err() != nil {
		return provider.ExitStatus{}, ctx.Err()
	}
	_ = xc.Close()
	st, ok := h.endOf(ctx, xid)
	if !ok {
		if copyErr != nil {
			return provider.ExitStatus{}, &provider.Error{Class: provider.ClassUnavailable, Message: "the connection to the Docker Engine broke: " + copyErr.Error()}
		}
		return provider.ExitStatus{}, nil // ended, the code not observed
	}
	if st.Pid == 0 && (*st.ExitCode == 126 || *st.ExitCode == 127) {
		// never started (runc said why in the stream); a process that ran
		// and exited 127 has a pid
		if h.shell {
			return provider.ExitStatus{}, &provider.Error{Class: provider.ClassInvalid, Message: "the container has no sh: run a command instead"}
		}
		return provider.ExitStatus{}, &provider.Error{Class: provider.ClassInvalid, Message: fmt.Sprintf("%q could not be started in the container", h.argv[0])}
	}
	return provider.ExitStatus{Code: *st.ExitCode, Known: true}, nil
}

// endOf waits briefly for exec inspect to report the command ended with a
// code; false: not observed (still running, no code, or no answer).
func (h *execHandle) endOf(ctx context.Context, xid string) (engine.ExecInspect, bool) {
	deadline := time.Now().Add(execEndWait)
	for {
		st, err := h.conn.cl.InspectExec(ctx, xid)
		if err == nil && !st.Running {
			return st, st.ExitCode != nil
		}
		if err != nil && engine.ClassOf(err) != provider.ClassUnavailable || time.Now().After(deadline) {
			return engine.ExecInspect{}, false
		}
		select {
		case <-ctx.Done():
			return engine.ExecInspect{}, false
		case <-time.After(execEndPoll):
		}
	}
}

// startError explains a refused or ambiguous start.
func (h *execHandle) startError(err error) error {
	switch engine.ClassOf(err) {
	case provider.ClassConflict: // paused, not running (the daemon's text)
		var ee *engine.Error
		if errors.As(err, &ee) {
			return &provider.Error{Class: provider.ClassInvalid, Message: ee.Message}
		}
	case provider.ClassUnknown:
		var ee *engine.Error
		if errors.As(err, &ee) {
			return &provider.Error{Class: provider.ClassUnknown, Message: ee.Message + " (the command may be running; it is not started again)"}
		}
	}
	return providerError(err)
}
