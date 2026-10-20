package kubernetes

import (
	"context"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

var actNodeFiles = func() core.ActionDescriptor {
	d := actNodeShell
	d.ID, d.Title, d.Terminal, d.Files = "node-files", "Files", false, true
	return d
}()

// One reviewed Files consumer retains a shared helper across its commands.
type nodeFilesHandle struct{ lease *nodeHelperLease }

func (s *session) PrepareFilesAction(ctx context.Context, run provider.ActionRun) (provider.FilesHandle, error) {
	if run.Action != actNodeFiles.ID {
		return nil, invalid("Files requires a file review")
	}
	h, err := s.prepareNodeHelper(ctx, run)
	if err != nil {
		return nil, err
	}
	lease, err := s.conn.helpers.acquire(ctx, h)
	if err != nil {
		return nil, err
	}
	return &nodeFilesHandle{lease: lease}, nil
}
func (h *nodeFilesHandle) SetHelperJournal(j provider.HelperJournal) { h.lease.setJournal(j) }
func (h *nodeFilesHandle) Describe() core.LiveTarget                 { return h.lease.describe() }
func (h *nodeFilesHandle) PrepareCommand(_ context.Context, argv []string) (provider.ExecHandle, error) {
	return &nodeFilesCommand{owner: h, argv: append([]string(nil), argv...)}, nil
}
func (h *nodeFilesHandle) Close() error {
	h.lease.close()
	select {
	case <-h.lease.entry.done:
		return h.lease.entry.owner.state.cleanupError
	default:
		return nil
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
	return h.owner.lease.command(ctx, h.argv, t)
}
