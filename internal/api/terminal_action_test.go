package api

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
)

type terminalActionProvider struct {
	*openable
	prepared   atomic.Int32
	closed     atomic.Int32
	cleanupErr error
}

func (p *terminalActionProvider) Open(ctx context.Context, id string) (provider.Session, error) {
	s, err := p.openable.Open(ctx, id)
	if err != nil {
		return nil, err
	}
	return &terminalActionSession{fakeSession: s.(*fakeSession), owner: p}, nil
}

type terminalActionSession struct {
	*fakeSession
	owner *terminalActionProvider
}

func (s *terminalActionSession) Kinds() []core.KindDescriptor {
	return []core.KindDescriptor{{ID: "nodes", Title: "Nodes", Actions: []core.ActionDescriptor{{ID: "shell", Title: "Node Shell", NoAgents: true, Single: true, Terminal: true}}}}
}
func (s *terminalActionSession) PrepareAction(_ context.Context, r core.Ref, _ string, p core.ActionParams) (core.ActionPlan, error) {
	return core.ActionPlan{Where: core.LiveTarget{Ref: r, ConfigHash: s.hash}, Params: p, Expect: "review"}, nil
}
func (s *terminalActionSession) RunAction(context.Context, provider.ActionRun) (core.ActionResult, error) {
	panic("terminal actions must never call RunAction")
}
func (s *terminalActionSession) PrepareTerminalAction(context.Context, provider.ActionRun) (provider.ExecHandle, error) {
	s.owner.prepared.Add(1)
	return &terminalActionHandle{owner: s.owner}, nil
}

type terminalActionHandle struct{ owner *terminalActionProvider }

func (h *terminalActionHandle) Describe() core.LiveTarget {
	return core.LiveTarget{Provider: "k", Target: "a"}
}
func (h *terminalActionHandle) Again() (provider.ExecHandle, error) {
	return &terminalActionHandle{owner: h.owner}, nil
}
func (h *terminalActionHandle) Close() { h.owner.closed.Add(1) }
func (h *terminalActionHandle) Run(context.Context, provider.Terminal) (provider.ExitStatus, error) {
	return provider.ExitStatus{Known: true}, nil
}

func TestReviewedTerminalActionRequiresUIAndPinsConfiguration(t *testing.T) {
	p := &terminalActionProvider{openable: newOpenable("a")}
	s, _ := newService(t, p)
	ctx := UIContext(t.Context())
	_, err := s.ListTargets(ctx)
	require.NoError(t, err)
	_, err = s.ConnectTarget(ctx, "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	ref := core.Ref{Provider: "k", Target: "a", Kind: "nodes", Name: "worker", UID: "node-uid"}
	plan, err := s.PrepareAction(ctx, ActionRequest{Ref: ref, Action: "shell"})
	require.NoError(t, err)
	req := runFor(plan, nil)
	_, err = s.RunAction(t.Context(), req)
	require.True(t, IsCoded(err, "forbidden"))
	require.Zero(t, p.prepared.Load())
	bad := req
	bad.ConfigRev = "old"
	_, err = s.RunAction(ctx, bad)
	require.True(t, IsCoded(err, CodeConflict))
	require.Zero(t, p.prepared.Load())
	res, err := s.RunAction(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, res.Terminal)
	require.NotEmpty(t, res.Terminal.PreparedID)
	require.EqualValues(t, 1, p.prepared.Load())
	require.Empty(t, s.Streams().Owners(), "no stream or remote command before UI adoption")
	info, err := s.ReopenTerminal(ctx, ReopenTerminalRequest{TerminalID: res.Terminal.PreparedID, Cols: 80, Rows: 24})
	require.NoError(t, err)
	require.Equal(t, res.Terminal.PreparedID, info.TerminalID)
	require.NoError(t, s.ForgetTerminal(ctx, info.TerminalID))
	require.Positive(t, p.closed.Load())
}

func (h *terminalActionHandle) WaitCleanup(context.Context) error { return h.owner.cleanupErr }

func TestForgettingAReviewedTerminalReportsCleanupFailure(t *testing.T) {
	p := &terminalActionProvider{openable: newOpenable("a")}
	s, _ := newService(t, p)
	ctx := UIContext(t.Context())
	_, err := s.ListTargets(ctx)
	require.NoError(t, err)
	_, err = s.ConnectTarget(ctx, "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	plan, err := s.PrepareAction(ctx, ActionRequest{Ref: core.Ref{Provider: "k", Target: "a", Kind: "nodes", Name: "worker", UID: "u"}, Action: "shell"})
	require.NoError(t, err)
	res, err := s.RunAction(ctx, runFor(plan, nil))
	require.NoError(t, err)
	p.cleanupErr = &provider.Error{Class: provider.ClassUnknown, Message: "helper tools/ocular-node-shell-1 remains"}
	err = s.ForgetTerminal(ctx, res.Terminal.PreparedID)
	require.True(t, IsCoded(err, "unknown"))
	require.Contains(t, err.Error(), "tools/ocular-node-shell-1")
}
