package api

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/synthetic"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestSecurityIsUIOnlyAndNeverImplicitlyConnects(t *testing.T) {
	s, _ := newService(t, synthetic.New())
	req := SecurityRequest{Provider: synthetic.ID, Target: synthetic.Target, Scope: core.ScopeSel{Mode: core.ScopeAll}}
	_, err := s.ClusterSecurity(context.Background(), req)
	require.Error(t, err)
	_, err = s.ClusterSecurity(UIContext(context.Background()), req)
	require.Error(t, err)
	_, err = s.ListTargets(context.Background())
	require.NoError(t, err)
	_, err = s.ConnectTarget(UIContext(context.Background()), req.Provider, req.Target)
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	security, err := s.ClusterSecurity(UIContext(context.Background()), req)
	require.NoError(t, err)
	require.NotEmpty(t, security.Findings)
	require.Positive(t, security.Resources)
	ui, err := s.ListKinds(UIContext(context.Background()), req.Provider, req.Target)
	require.NoError(t, err)
	found := false
	for _, kind := range ui.Kinds {
		if kind.Workspace == "security" {
			found = true
		}
	}
	require.True(t, found)
	agent, err := s.ListKinds(context.Background(), req.Provider, req.Target)
	require.NoError(t, err)
	for _, kind := range agent.Kinds {
		require.NotEqual(t, "security", kind.Workspace)
	}
	for _, scope := range []core.ScopeSel{{Mode: core.ScopeNone}, {Mode: core.ScopeOne}, {Mode: core.ScopeAll, Name: "blue"}} {
		req.Scope = scope
		_, err = s.ClusterSecurity(UIContext(context.Background()), req)
		require.Error(t, err)
	}
}

type securityFenceProvider struct {
	*argoTestProvider
	onRead func(context.Context, core.SecurityOptions)
}

func (p *securityFenceProvider) ID() string { return "kubernetes" }
func (p *securityFenceProvider) Open(ctx context.Context, target string) (provider.Session, error) {
	session, err := p.argoTestProvider.Open(ctx, target)
	if err != nil {
		return nil, err
	}
	return &securityFenceSession{session.(*argoTestSession), p.onRead}, nil
}

type securityFenceSession struct {
	*argoTestSession
	onRead func(context.Context, core.SecurityOptions)
}

func (s *securityFenceSession) Security(ctx context.Context, _ core.ScopeSel, options core.SecurityOptions) (core.SecuritySnapshot, error) {
	s.onRead(ctx, options)
	return core.SecuritySnapshot{Findings: []core.SecurityFinding{{ID: "obsolete"}}}, nil
}
func TestSecurityRejectsCancelledAndReplacedSessionEvidence(t *testing.T) {
	for _, mode := range []string{"cancel", "disconnect"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(UIContext(t.Context()))
			defer cancel()
			p := &securityFenceProvider{argoTestProvider: &argoTestProvider{openable: newOpenable("a")}}
			s, _ := newService(t, p)
			p.onRead = func(c context.Context, _ core.SecurityOptions) {
				deadline, ok := c.Deadline()
				require.True(t, ok)
				require.LessOrEqual(t, time.Until(deadline), 75*time.Second)
				if mode == "cancel" {
					cancel()
				} else {
					require.NoError(t, s.CloseTarget(UIContext(t.Context()), "kubernetes", "a"))
				}
			}
			_, err := s.ConnectTarget(UIContext(t.Context()), "kubernetes", "a")
			require.NoError(t, err)
			awaitConnection(t, s, "connected")
			out, err := s.ClusterSecurity(ctx, SecurityRequest{Provider: "kubernetes", Target: "a", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}})
			require.Error(t, err)
			require.Empty(t, out.Findings)
		})
	}
}

func TestSecurityClusterReportOptionIsExplicitAndUIOnly(t *testing.T) {
	seen := []core.SecurityOptions{}
	p := &securityFenceProvider{argoTestProvider: &argoTestProvider{openable: newOpenable("a")}, onRead: func(_ context.Context, options core.SecurityOptions) { seen = append(seen, options) }}
	s, _ := newService(t, p)
	_, err := s.ConnectTarget(UIContext(t.Context()), "kubernetes", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	req := SecurityRequest{Provider: "kubernetes", Target: "a", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}, IncludeClusterReports: true}
	_, err = s.ClusterSecurity(t.Context(), req)
	require.Error(t, err)
	require.Empty(t, seen)
	req.IncludeClusterReports = false
	_, err = s.ClusterSecurity(UIContext(t.Context()), req)
	require.NoError(t, err)
	req.IncludeClusterReports = true
	_, err = s.ClusterSecurity(UIContext(t.Context()), req)
	require.NoError(t, err)
	require.Equal(t, []core.SecurityOptions{{}, {IncludeClusterReports: true}}, seen)
}
