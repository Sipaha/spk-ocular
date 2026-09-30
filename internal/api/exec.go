package api

import (
	"context"
	"errors"
	"fmt"
	"sync"

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
	// Attach to the container's own process (a debug container), not a
	// new command; a reopen attaches again.
	Attach bool `json:"attach,omitempty"`
	// Cols, Rows: the terminal's size when it opens (1..1000).
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

type TerminalInfo struct {
	// TerminalID names the terminal across reconnects (ReopenTerminal);
	// ForgetTerminal when its tab closes.
	TerminalID string `json:"terminalId"`
	// StreamID: open a WebSocket to <StreamBase>/term/<StreamID> once,
	// within 30 s (web/src/term/protocol.ts).
	StreamID string          `json:"streamId"`
	Target   core.LiveTarget `json:"target"`
}

type ReopenTerminalRequest struct {
	TerminalID string `json:"terminalId"`
	Cols       int    `json:"cols"`
	Rows       int    `json:"rows"`
}

// maxTermProtos bounds remembered terminals (a tab that never said
// ForgetTerminal); the oldest is forgotten first (its run goes on, only
// reconnecting it is no longer possible).
const maxTermProtos = 64

// termProtos remembers each terminal's prepared handle: a reconnect runs
// again with the same connection snapshot, pod and container (never
// silently a new target configuration). Handles are closed outside the
// lock.
type termProtos struct {
	mu     sync.Mutex
	seq    uint64
	order  []string
	byID   map[string]provider.ExecHandle
	closed bool
}

// add remembers h under a new terminal id; "" when the app is closing (h
// is closed then).
func (p *termProtos) add(h provider.ExecHandle) string {
	var evicted []provider.ExecHandle
	defer func() { closeHandles(evicted) }()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed { // the app is closing: nothing will reopen it
		evicted = append(evicted, h)
		return ""
	}
	if p.byID == nil {
		p.byID = map[string]provider.ExecHandle{}
	}
	p.seq++
	id := fmt.Sprintf("t%d", p.seq)
	p.byID[id] = h
	p.order = append(p.order, id)
	for len(p.order) > maxTermProtos {
		old := p.order[0]
		p.order = p.order[1:]
		if h := p.byID[old]; h != nil {
			delete(p.byID, old)
			evicted = append(evicted, h)
		}
	}
	return id
}

// run starts a run of terminal id under the lock forget takes: a run
// either exists before forget (which then ends it) or is refused. start's
// cleanup (closing handles) runs after the lock is released.
func (p *termProtos) run(id string, start func(proto provider.ExecHandle) (TerminalInfo, func(), error)) (TerminalInfo, error) {
	info, cleanup, err := p.runLocked(id, start)
	if cleanup != nil {
		cleanup()
	}
	return info, err
}

func (p *termProtos) runLocked(id string, start func(proto provider.ExecHandle) (TerminalInfo, func(), error)) (TerminalInfo, func(), error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	proto := p.byID[id]
	if proto == nil {
		return TerminalInfo{}, nil, coded(CodeGone, errors.New("this terminal is no longer known; open a new one"))
	}
	return start(proto)
}

// forget drops terminal id; the caller ends its runs.
func (p *termProtos) forget(id string) {
	p.mu.Lock()
	h := p.byID[id]
	delete(p.byID, id)
	for i, o := range p.order {
		if o == id {
			p.order = append(p.order[:i], p.order[i+1:]...)
			break
		}
	}
	p.mu.Unlock()
	if h != nil {
		h.Close()
	}
}

func closeHandles(hs []provider.ExecHandle) {
	for _, h := range hs {
		h.Close()
	}
}

func (p *termProtos) closeAll() {
	p.mu.Lock()
	hs := p.byID
	p.byID, p.order, p.closed = nil, nil, true
	p.mu.Unlock()
	for _, h := range hs {
		h.Close()
	}
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
	// Lists, never null on the wire (a provider without a level leaves it
	// nil).
	if info.Instances == nil {
		info.Instances = []core.ExecInstance{}
	}
	for i := range info.Instances {
		if info.Instances[i].Channels == nil {
			info.Instances[i].Channels = []core.ExecChannel{}
		}
	}
	return info, nil
}

func validSize(cols, rows int) error {
	if cols < 1 || cols > 1000 || rows < 1 || rows > 1000 {
		return errors.New("terminal size must be 1..1000 columns and rows")
	}
	return nil
}

func validTerminal(req TerminalRequest) error {
	if err := validSize(req.Cols, req.Rows); err != nil {
		return err
	}
	if req.Attach && len(req.Command) > 0 {
		return errors.New("an attach runs no command")
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
	proto, err := ex.PrepareExec(ctx, req.Ref, provider.ExecRequest{Instance: req.Instance, Channel: req.Channel, Command: req.Command, Attach: req.Attach})
	if err != nil {
		return TerminalInfo{}, fromProvider(err)
	}
	id := s.terms.add(proto)
	if id == "" {
		return TerminalInfo{}, coded(CodeGone, errors.New("the app is closing"))
	}
	info, err := s.startTerminal(id, req.Cols, req.Rows)
	if err != nil {
		s.terms.forget(id)
		return TerminalInfo{}, err
	}
	return info, nil
}

// ReopenTerminal runs a terminal's command again ("reconnect") with the
// connection snapshot and pinned pod/container it was opened with.
func (s *Service) ReopenTerminal(_ context.Context, req ReopenTerminalRequest) (TerminalInfo, error) {
	if err := validSize(req.Cols, req.Rows); err != nil {
		return TerminalInfo{}, coded(CodeBadRequest, err)
	}
	return s.startTerminal(req.TerminalID, req.Cols, req.Rows)
}

// ForgetTerminal: the terminal's tab is gone; it cannot be reopened, and
// its run (connected or not yet) ends.
func (s *Service) ForgetTerminal(_ context.Context, terminalID string) error {
	s.terms.forget(terminalID)
	s.streams.CloseOwner(termOwner(terminalID))
	return nil
}

// termOwner names a terminal's runs in the stream registry.
func termOwner(terminalID string) string { return "term:" + terminalID }

// startTerminal registers a fresh run of terminal id with the stream
// registry (which closes that run's handle on any failure, including the
// app closing meanwhile). A terminal has one run: the previous one —
// connected or pending — ends first, under the lock runs take (two
// attaches to a debugger would share its stdin).
func (s *Service) startTerminal(id string, cols, rows int) (TerminalInfo, error) {
	return s.terms.run(id, func(proto provider.ExecHandle) (TerminalInfo, func(), error) {
		s.streams.CloseOwner(termOwner(id))
		h, err := proto.Again()
		if err != nil {
			return TerminalInfo{}, nil, fromProvider(err)
		}
		sid, release, err := s.streams.RegisterTerm(termOwner(id), h, provider.TermSize{Cols: uint16(cols), Rows: uint16(rows)})
		switch {
		case errors.Is(err, streams.ErrLimit):
			return TerminalInfo{}, release, &CodedError{Code: CodeLimit, Detail: err.Error()}
		case err != nil:
			return TerminalInfo{}, release, coded(CodeGone, err)
		}
		return TerminalInfo{TerminalID: id, StreamID: sid, Target: s.live(proto.Describe())}, release, nil
	})
}
