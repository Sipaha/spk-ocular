package api

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

type fluxTestSession struct{ *argoTestSession }

func (s *fluxTestSession) Kinds() []core.KindDescriptor {
	out := s.Session.Kinds()
	if s.owner.offered.Load() {
		out = append(out, core.KindDescriptor{ID: "kustomize.toolkit.fluxcd.io/kustomizations", Singular: "Kustomization", Scoped: true})
	}
	return out
}
func (s *fluxTestSession) FluxCD(ctx context.Context, _ core.ScopeSel) (core.FluxSnapshot, error) {
	s.owner.calls.Add(1)
	return core.FluxSnapshot{Objects: []core.FluxObject{}, Discovery: "ready"}, ctx.Err()
}

type fluxTestProvider struct{ *argoTestProvider }

func (p *fluxTestProvider) Open(ctx context.Context, target string) (provider.Session, error) {
	s, e := p.argoTestProvider.Open(ctx, target)
	if e != nil {
		return nil, e
	}
	return &fluxTestSession{s.(*argoTestSession)}, nil
}
func TestFluxNavigationDetectionIsExactAndUIOnly(t *testing.T) {
	p := &fluxTestProvider{&argoTestProvider{openable: newOpenable("a")}}
	p.offered.Store(true)
	s, _ := newService(t, p)
	ctx := UIContext(t.Context())
	_, err := s.ConnectTarget(ctx, "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	view, err := s.ListKinds(ctx, "k", "a")
	require.NoError(t, err)
	count := 0
	rawFound := false
	for _, k := range view.Kinds {
		if k.ID == "kustomize.toolkit.fluxcd.io/kustomizations" {
			rawFound = true
			require.False(t, k.Hidden)
		}
		if strings.HasPrefix(k.Workspace, "fluxcd") {
			count++
			require.Equal(t, "Flux", k.Group)
		}
	}
	require.Equal(t, 2, count)
	require.True(t, rawFound)
	view, err = s.ListKinds(t.Context(), "k", "a")
	require.NoError(t, err)
	for _, k := range view.Kinds {
		require.False(t, strings.HasPrefix(k.Workspace, "fluxcd"))
	}
	p.offered.Store(false)
	view, err = s.ListKinds(ctx, "k", "a")
	require.NoError(t, err)
	for _, k := range view.Kinds {
		require.False(t, strings.HasPrefix(k.Workspace, "fluxcd"))
	}
	require.Empty(t, fluxKinds([]core.KindDescriptor{{ID: "unknown.toolkit.fluxcd.io/kustomizations", Scoped: true}}))
	require.Empty(t, fluxKinds([]core.KindDescriptor{{ID: "kustomize.toolkit.fluxcd.io/kustomizations", Scoped: false}}))
}
func TestFluxReadRequiresExplicitConnectionUIAndValidScope(t *testing.T) {
	p := &fluxTestProvider{&argoTestProvider{openable: newOpenable("a")}}
	s, _ := newService(t, p)
	req := FluxRequest{Provider: "k", Target: "a", Scope: core.ScopeSel{Mode: core.ScopeAll}}
	_, err := s.FluxCD(t.Context(), req)
	require.Error(t, err)
	_, err = s.FluxCD(UIContext(t.Context()), req)
	require.Error(t, err)
	require.Empty(t, p.opened)
	require.Zero(t, p.calls.Load())
	_, err = s.ConnectTarget(UIContext(t.Context()), "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	_, err = s.FluxCD(UIContext(t.Context()), req)
	require.NoError(t, err)
	require.EqualValues(t, 1, p.calls.Load())
	for _, scope := range []core.ScopeSel{{Mode: core.ScopeNone}, {Mode: core.ScopeOne}, {Mode: core.ScopeAll, Name: "outside"}} {
		req.Scope = scope
		_, err = s.FluxCD(UIContext(t.Context()), req)
		require.Error(t, err)
	}
	require.EqualValues(t, 1, p.calls.Load())
}
