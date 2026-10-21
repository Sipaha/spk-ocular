package api

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
)

type argoTestProvider struct {
	*openable
	offered       atomic.Bool
	calls         atomic.Int64
	settingsCalls atomic.Int64
}
type argoTestSession struct {
	provider.Session
	owner *argoTestProvider
}

func (p *argoTestProvider) Open(ctx context.Context, target string) (provider.Session, error) {
	s, err := p.openable.Open(ctx, target)
	return &argoTestSession{Session: s, owner: p}, err
}
func (s *argoTestSession) Kinds() []core.KindDescriptor {
	out := s.Session.Kinds()
	if s.owner.offered.Load() {
		out = append(out, core.KindDescriptor{ID: "argoproj.io/applications", Title: "Applications", Scoped: true})
	}
	return out
}
func (s *argoTestSession) ArgoCD(ctx context.Context, _ core.ScopeSel) (core.ArgoSnapshot, error) {
	s.owner.calls.Add(1)
	return core.ArgoSnapshot{Objects: []core.ArgoObject{}, Events: []core.ArgoEvent{}, Coverage: []core.ArgoCoverage{}, Discovery: "ready"}, ctx.Err()
}
func TestArgoCDIsUIOnlyExplicitAndScopeValidated(t *testing.T) {
	p := &argoTestProvider{openable: newOpenable("a")}
	p.offered.Store(true)
	s, _ := newService(t, p)
	req := ArgoRequest{Provider: "k", Target: "a", Scope: core.ScopeSel{Mode: core.ScopeAll}}
	_, err := s.ArgoCD(t.Context(), req)
	require.ErrorContains(t, err, "only to the application UI")
	_, err = s.ArgoCD(UIContext(t.Context()), req)
	require.Error(t, err)
	require.Zero(t, p.calls.Load())
	require.Empty(t, p.opened)
	_, err = s.ConnectTarget(UIContext(t.Context()), "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	got, err := s.ArgoCD(UIContext(t.Context()), req)
	require.NoError(t, err)
	require.Equal(t, "ready", got.Discovery)
	require.EqualValues(t, 1, p.calls.Load())
	for _, scope := range []core.ScopeSel{{Mode: core.ScopeNone}, {Mode: core.ScopeOne}, {Mode: core.ScopeAll, Name: "outside"}} {
		req.Scope = scope
		_, err = s.ArgoCD(UIContext(t.Context()), req)
		require.Error(t, err)
	}
	require.EqualValues(t, 1, p.calls.Load())
	req.Scope = core.ScopeSel{Mode: core.ScopeSome, Names: []string{}}
	_, err = s.ArgoCD(UIContext(t.Context()), req)
	require.NoError(t, err)
}
func TestArgoNavigationRequiresServedApplicationAndNeverAppearsForAgent(t *testing.T) {
	p := &argoTestProvider{openable: newOpenable("a")}
	p.offered.Store(true)
	s, _ := newService(t, p)
	ctx := UIContext(t.Context())
	_, err := s.ConnectTarget(ctx, "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	kinds, err := s.ListKinds(ctx, "k", "a")
	require.NoError(t, err)
	found := false
	count := 0
	for _, k := range kinds.Kinds {
		if strings.HasPrefix(k.Workspace, "argocd") {
			count++
			require.Equal(t, "Argo CD", k.Group)
		}
		if k.Workspace == "argocd" {
			found = true
		}
	}
	require.True(t, found)
	require.Equal(t, 6, count)
	agent, err := s.ListKinds(t.Context(), "k", "a")
	require.NoError(t, err)
	for _, k := range agent.Kinds {
		require.False(t, strings.HasPrefix(k.Workspace, "argocd"))
	}
	p.offered.Store(false)
	kinds, err = s.ListKinds(ctx, "k", "a")
	require.NoError(t, err)
	for _, k := range kinds.Kinds {
		require.False(t, strings.HasPrefix(k.Workspace, "argocd"))
	}
}

func (s *argoTestSession) ArgoSettings(ctx context.Context, _ core.ScopeSel, _ string) (core.ArgoSnapshot, error) {
	s.owner.settingsCalls.Add(1)
	return core.ArgoSnapshot{Settings: []core.ArgoSetting{}, Discovery: "ready"}, ctx.Err()
}
func TestArgoSettingsUseSameUIBoundaryAndExplicitRequest(t *testing.T) {
	p := &argoTestProvider{openable: newOpenable("a")}
	p.offered.Store(true)
	s, _ := newService(t, p)
	req := ArgoRequest{Provider: "k", Target: "a", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "argo"}, Settings: "repository"}
	_, err := s.ArgoCD(t.Context(), req)
	require.Error(t, err)
	require.Zero(t, p.settingsCalls.Load())
	_, err = s.ConnectTarget(UIContext(t.Context()), "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	_, err = s.ArgoCD(UIContext(t.Context()), req)
	require.NoError(t, err)
	require.EqualValues(t, 1, p.settingsCalls.Load())
	require.Zero(t, p.calls.Load())
	req.Settings = ""
	_, err = s.ArgoCD(UIContext(t.Context()), req)
	require.NoError(t, err)
	require.EqualValues(t, 1, p.settingsCalls.Load())
	require.EqualValues(t, 1, p.calls.Load())
}
