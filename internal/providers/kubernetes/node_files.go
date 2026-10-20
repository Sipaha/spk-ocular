package kubernetes

import (
	"context"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

var actNodeFiles = func() core.ActionDescriptor {
	d := actNodeShell
	d.ID, d.Title, d.Terminal, d.Files = "node-files", "Files", false, true
	return d
}()

// nodeFilesHandle owns one reviewed helper, shared by file commands. Its
// lifetime is independent of an individual HTTP request but bounded to an hour.
type nodeFilesHandle struct {
	shell  *nodeShellHandle
	mu     sync.Mutex
	ready  chan struct{}
	done   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
	exec   *execHandle
	err    error
	closed bool
}

func (s *session) PrepareFilesAction(ctx context.Context, run provider.ActionRun) (provider.FilesHandle, error) {
	if run.Action != actNodeFiles.ID {
		return nil, invalid("Files requires a file review")
	}
	h, err := s.prepareNodeHelper(ctx, run)
	if err != nil {
		return nil, err
	}
	return &nodeFilesHandle{shell: h}, nil
}
func (h *nodeFilesHandle) SetHelperJournal(j provider.HelperJournal) { h.shell.SetHelperJournal(j) }
func (h *nodeFilesHandle) Describe() core.LiveTarget                 { return h.shell.Describe() }
func (h *nodeFilesHandle) PrepareCommand(_ context.Context, argv []string) (provider.ExecHandle, error) {
	return &nodeFilesCommand{owner: h, argv: append([]string(nil), argv...)}, nil
}
func (h *nodeFilesHandle) start() (<-chan struct{}, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, provider.Said(provider.ClassGone, msg("nodeShell.ended"))
	}
	if h.ready != nil {
		return h.ready, nil
	}
	h.ready, h.done = make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	h.ctx, h.cancel = ctx, cancel
	go func() {
		defer cancel()
		_, err := h.shell.run(ctx, provider.Terminal{}, func(ctx context.Context, e *execHandle) (provider.ExitStatus, error) {
			h.mu.Lock()
			h.exec = e
			h.ctx = ctx
			close(h.ready)
			h.mu.Unlock()
			<-ctx.Done()
			return provider.ExitStatus{Known: true}, nil
		})
		h.mu.Lock()
		h.err = err
		if h.exec == nil {
			close(h.ready)
		}
		h.mu.Unlock()
		close(h.done)
	}()
	return h.ready, nil
}
func (h *nodeFilesHandle) Close() error {
	h.mu.Lock()
	h.closed = true
	cancel, done := h.cancel, h.done
	h.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	// The helper has an independent ten-second cleanup budget.
	ctx, stop := context.WithTimeout(context.Background(), 12*time.Second)
	defer stop()
	select {
	case <-done:
		return h.shell.WaitCleanup(ctx)
	case <-ctx.Done():
		return h.shell.WaitCleanup(ctx)
	}
}

type nodeFilesCommand struct {
	owner *nodeFilesHandle
	argv  []string
}

func (h *nodeFilesCommand) Describe() core.LiveTarget { return h.owner.Describe() }
func (h *nodeFilesCommand) Close()                    {}
func (h *nodeFilesCommand) Again() (provider.ExecHandle, error) {
	return &nodeFilesCommand{owner: h.owner, argv: append([]string(nil), h.argv...)}, nil
}
func (h *nodeFilesCommand) Run(ctx context.Context, t provider.Terminal) (provider.ExitStatus, error) {
	if err := ctx.Err(); err != nil {
		return provider.ExitStatus{}, err
	}
	ready, err := h.owner.start()
	if err != nil {
		return provider.ExitStatus{}, err
	}
	select {
	case <-ready:
	case <-ctx.Done():
		return provider.ExitStatus{}, ctx.Err()
	}
	h.owner.mu.Lock()
	e, fail, closed := h.owner.exec, h.owner.err, h.owner.closed
	done, lifetime := h.owner.done, h.owner.ctx
	h.owner.mu.Unlock()
	if fail != nil {
		return provider.ExitStatus{}, fail
	}
	if e == nil || closed {
		return provider.ExitStatus{}, provider.Said(provider.ClassGone, msg("nodeShell.ended"))
	}
	select {
	case <-done:
		return provider.ExitStatus{}, provider.Said(provider.ClassGone, msg("nodeShell.ended"))
	default:
	}
	if err = validateNodeShell(ctx, e.conn, h.owner.shell.g); err != nil {
		return provider.ExitStatus{}, err
	}
	command := *e
	// All scripts and path operands come from the existing UI-only Files API.
	// nsenter switches both namespaces and root before running the host's shell.
	argv := append([]string(nil), h.argv...)
	if len(argv) > 0 && argv[0] == "sh" {
		argv[0] = "/bin/sh"
	}
	command.argv = append([]string{"nsenter", "-t", "1", "-m", "-u", "-i", "-n", "-p", "-r", "-w", "--"}, argv...)
	runctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(lifetime, cancel)
	defer stop()
	return command.Run(runctx, t)
}
