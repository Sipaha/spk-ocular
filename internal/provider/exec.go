package provider

import (
	"context"
	"io"

	"github.com/spk/spk-ocular/internal/core"
)

// Execer is implemented by sessions that can run an interactive command in
// an object (k8s: a container of a pod).
type Execer interface {
	// ExecInfo: the instances (pods) and channels (containers) of ref.
	ExecInfo(ctx context.Context, ref core.Ref) (core.ExecInfo, error)
	// PrepareExec resolves and pins what will run where, and captures the
	// connection. The handle does not depend on the session: closing the
	// session (another target selected, its configuration changed) leaves
	// the handle working.
	PrepareExec(ctx context.Context, ref core.Ref, req ExecRequest) (ExecHandle, error)
}

type ExecRequest struct {
	// Instance: ExecInfo's instance id ("" = the default one).
	Instance string `json:"instance,omitempty"`
	// Channel: ExecInfo's channel id ("" = the default one).
	Channel string `json:"channel,omitempty"`
	// Command is argv (no shell interpretation); empty = the provider's
	// interactive shell.
	Command []string `json:"command,omitempty"`
}

// ExecHandle is a prepared, pinned command. It owns its connection and is
// closed by its owner (the app), not by the session it came from.
type ExecHandle interface {
	Describe() core.LiveTarget
	// Run starts the command once and blocks until it ends or ctx is done
	// (then it returns promptly; the remote process ending is best effort).
	// The instance is re-validated before starting: a replaced one is an
	// error, never silently another. Stdin is first read once the remote
	// side is attached.
	Run(ctx context.Context, t Terminal) (ExitStatus, error)
	// Again is a fresh handle for the same connection snapshot and target
	// ("reconnect"); the API keeps the prepared handle as a prototype and
	// runs copies.
	Again() (ExecHandle, error)
	Close()
}

// Terminal is the page side of a running command. Stdout blocks while the
// page has not acknowledged enough output (backpressure reaches the
// command).
type Terminal struct {
	Stdin  io.Reader
	Stdout io.Writer
	Sizes  TermSizes
	// Notice tells the page something about the terminal itself (not the
	// command's output), e.g. that its size could not be set; nil: nobody
	// listens. It does not block for long.
	Notice func(core.Message)
}

type TermSize struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// TermSizes yields terminal sizes: the initial one first, then only the
// latest after each change; nil once the terminal is gone.
type TermSizes interface {
	Next() *TermSize
}

// ExitStatus: Known is false when the command's end was not observed (the
// connection broke): it may still be running.
type ExitStatus struct {
	Code  int
	Known bool
}
