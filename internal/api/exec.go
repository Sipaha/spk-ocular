package api

import (
	"context"
	"errors"
	"fmt"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/streams"
)

type TerminalRequest struct {
	Ref      core.Ref `json:"ref"`
	Instance string   `json:"instance,omitempty"`
	Channel  string   `json:"channel,omitempty"`
	// Command is argv; empty = the provider's interactive shell.
	Command []string `json:"command,omitempty"`
	// Cols, Rows: the terminal's size when it opens (1..1000).
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

type TerminalInfo struct {
	// StreamID: open a WebSocket to <StreamBase>/term/<StreamID> once,
	// within 30 s (web/src/term/protocol.ts).
	StreamID string          `json:"streamId"`
	Target   core.LiveTarget `json:"target"`
}

const (
	maxArgs     = 64
	maxArgBytes = 16 << 10 // the whole argv
)

func (s *Service) execer(ctx context.Context, ref core.Ref) (provider.Execer, error) {
	e, err := s.sessionFor(ctx, ref.Provider, ref.Target)
	if err != nil {
		return nil, err
	}
	ex, ok := e.sess.(provider.Execer)
	if !ok {
		return nil, coded(CodeUnsupported, errors.New("commands cannot run on this target"))
	}
	return ex, nil
}

func (s *Service) ExecInfo(ctx context.Context, ref core.Ref) (core.ExecInfo, error) {
	ex, err := s.execer(ctx, ref)
	if err != nil {
		return core.ExecInfo{}, err
	}
	info, err := ex.ExecInfo(ctx, ref)
	if err != nil {
		return core.ExecInfo{}, fromProvider(err)
	}
	return info, nil
}

func validTerminal(req TerminalRequest) error {
	if req.Cols < 1 || req.Cols > 1000 || req.Rows < 1 || req.Rows > 1000 {
		return errors.New("terminal size must be 1..1000 columns and rows")
	}
	if len(req.Command) > maxArgs {
		return fmt.Errorf("at most %d arguments", maxArgs)
	}
	total := 0
	for _, a := range req.Command {
		total += len(a)
	}
	if total > maxArgBytes {
		return fmt.Errorf("the command is longer than %d bytes", maxArgBytes)
	}
	if len(req.Command) > 0 && req.Command[0] == "" {
		return errors.New("the command's executable is empty")
	}
	return nil
}

// OpenTerminal prepares a command and registers its terminal; the command
// starts when the page connects. The terminal belongs to the app, not to
// the session: selecting another target or a configuration change does
// not end it (the handle carries its own connection snapshot).
func (s *Service) OpenTerminal(ctx context.Context, req TerminalRequest) (TerminalInfo, error) {
	if err := validTerminal(req); err != nil {
		return TerminalInfo{}, coded(CodeBadRequest, err)
	}
	ex, err := s.execer(ctx, req.Ref)
	if err != nil {
		return TerminalInfo{}, err
	}
	h, err := ex.PrepareExec(ctx, req.Ref, provider.ExecRequest{Instance: req.Instance, Channel: req.Channel, Command: req.Command})
	if err != nil {
		return TerminalInfo{}, fromProvider(err)
	}
	return s.registerTerminal(h, provider.TermSize{Cols: uint16(req.Cols), Rows: uint16(req.Rows)})
}

// registerTerminal hands h to the stream registry (which closes it on any
// failure, including the app closing meanwhile).
func (s *Service) registerTerminal(h provider.ExecHandle, size provider.TermSize) (TerminalInfo, error) {
	target := h.Describe()
	id, err := s.streams.AddTerm(h, size)
	switch {
	case errors.Is(err, streams.ErrLimit):
		return TerminalInfo{}, &CodedError{Code: CodeLimit, Detail: err.Error()}
	case err != nil:
		return TerminalInfo{}, coded(CodeGone, err)
	}
	return TerminalInfo{StreamID: id, Target: target}, nil
}
