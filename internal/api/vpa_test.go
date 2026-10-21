package api

import (
	"context"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
)

type vpaTestProvider struct {
	*argoTestProvider
	onRead func(context.Context)
}

func (p *vpaTestProvider) ID() string { return "kubernetes" }
func (p *vpaTestProvider) Open(ctx context.Context, target string) (provider.Session, error) {
	s, e := p.argoTestProvider.Open(ctx, target)
	if e != nil {
		return nil, e
	}
	return &vpaTestSession{s.(*argoTestSession), p.onRead}, nil
}

type vpaTestSession struct {
	*argoTestSession
	onRead func(context.Context)
}

func (s *vpaTestSession) ResourceVPA(ctx context.Context, r core.Ref) (core.VPAInspection, error) {
	s.owner.calls.Add(1)
	if s.onRead != nil {
		s.onRead(ctx)
	}
	return core.VPAInspection{Ref: r}, nil
}
func (s *vpaTestSession) Get(_ context.Context, r core.Ref) (*core.Resource, error) {
	return &core.Resource{Ref: r, VPAAvailable: true}, nil
}
func TestVPARequiresUIAdmittedSessionAndFencesConnection(t *testing.T) {
	p := &vpaTestProvider{argoTestProvider: &argoTestProvider{openable: newOpenable("a")}}
	s, _ := newService(t, p)
	r := core.Ref{Provider: "kubernetes", Target: "a", Kind: "apps/deployments", Scope: "blue", Name: "web", UID: "current-controller"}
	_, e := s.ResourceVPA(t.Context(), r)
	require.ErrorContains(t, e, "only to the application UI")
	_, e = s.ResourceVPA(UIContext(t.Context()), r)
	require.Error(t, e)
	require.Empty(t, p.opened)
	_, e = s.ConnectTarget(UIContext(t.Context()), "kubernetes", "a")
	require.NoError(t, e)
	require.Eventually(t, func() bool {
		s.sessMu.Lock()
		defer s.sessMu.Unlock()
		a := s.connections[ownerKey("kubernetes", "a")]
		return a != nil && a.status.State == "connected"
	}, time.Second, time.Millisecond)
	_, e = s.ResourceVPA(UIContext(t.Context()), r)
	require.NoError(t, e)
	require.EqualValues(t, 1, p.calls.Load())
	ui, e := s.GetResource(UIContext(t.Context()), r)
	require.NoError(t, e)
	require.True(t, ui.VPAAvailable)
	plain, e := s.GetResource(t.Context(), r)
	require.NoError(t, e)
	require.False(t, plain.VPAAvailable)
	require.True(t, ui.VPAAvailable)
	bad := r
	bad.Scope = ""
	_, e = s.ResourceVPA(UIContext(t.Context()), bad)
	require.Error(t, e)
	require.EqualValues(t, 1, p.calls.Load())
	for _, mode := range []string{"cancel", "disconnect"} {
		ctx, cancel := context.WithCancel(UIContext(t.Context()))
		p.onRead = nil
		// Opened session captures its callback; update that exact admitted fixture.
		s.sessMu.Lock()
		fixture := s.sessions[ownerKey("kubernetes", "a")].sess.(*vpaTestSession)
		s.sessMu.Unlock()
		fixture.onRead = func(c context.Context) {
			deadline, ok := c.Deadline()
			require.True(t, ok)
			require.LessOrEqual(t, time.Until(deadline), 20*time.Second)
			if mode == "cancel" {
				cancel()
			} else {
				require.NoError(t, s.CloseTarget(UIContext(t.Context()), "kubernetes", "a"))
			}
		}
		_, e = s.ResourceVPA(ctx, r)
		require.Error(t, e)
		cancel()
	}
}
