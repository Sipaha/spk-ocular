package api

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
	"testing"
)

type networkPathTestSession struct{ *argoTestSession }

func (s *networkPathTestSession) ResourceNetworkPath(ctx context.Context, ref core.Ref) (core.NetworkPath, error) {
	s.owner.calls.Add(1)
	return core.NetworkPath{Ref: ref}, ctx.Err()
}

type networkPathTestProvider struct{ *argoTestProvider }

func (p *networkPathTestProvider) Open(ctx context.Context, target string) (provider.Session, error) {
	s, e := p.argoTestProvider.Open(ctx, target)
	if e != nil {
		return nil, e
	}
	return &networkPathTestSession{s.(*argoTestSession)}, nil
}
func TestNetworkPathRequiresUIConnectionAndPinnedIdentity(t *testing.T) {
	p := &networkPathTestProvider{&argoTestProvider{openable: newOpenable("a")}}
	s, _ := newService(t, p)
	ref := core.Ref{Provider: "k", Target: "a", Kind: "services", Scope: "blue", Name: "p", UID: "uid"}
	_, err := s.ResourceNetworkPath(t.Context(), ref)
	require.Error(t, err)
	_, err = s.ResourceNetworkPath(UIContext(t.Context()), ref)
	require.Error(t, err)
	require.Empty(t, p.opened)
	require.Zero(t, p.calls.Load())
	_, err = s.ConnectTarget(UIContext(t.Context()), "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	out, err := s.ResourceNetworkPath(UIContext(t.Context()), ref)
	require.NoError(t, err)
	require.Equal(t, ref, out.Ref)
	for _, field := range []string{"uid", "name", "kind", "scope"} {
		r := ref
		switch field {
		case "uid":
			r.UID = ""
		case "name":
			r.Name = ""
		case "kind":
			r.Kind = ""
		case "scope":
			r.Scope = ""
		}
		_, err = s.ResourceNetworkPath(UIContext(t.Context()), r)
		require.Error(t, err)
	}
	call, err := s.AgentCall(UIContext(t.Context()), "k", "a")
	require.NoError(t, err)
	defer call.Done()
	_, err = s.ResourceNetworkPath(call.ctx, ref)
	require.Error(t, err)
	require.EqualValues(t, 1, p.calls.Load())
}

func (s *networkPathTestSession) Get(_ context.Context, ref core.Ref) (*core.Resource, error) {
	return &core.Resource{Ref: ref, NetworkPathAvailable: true}, nil
}
func TestNetworkPathAvailabilityIsUIOnlyAndDoesNotCollect(t *testing.T) {
	p := &networkPathTestProvider{&argoTestProvider{openable: newOpenable("a")}}
	s, _ := newService(t, p)
	_, err := s.ConnectTarget(UIContext(t.Context()), "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	ref := core.Ref{Provider: "k", Target: "a", Scope: "blue", Kind: "services", Name: "web", UID: "uid"}
	ui, err := s.GetResource(UIContext(t.Context()), ref)
	require.NoError(t, err)
	require.True(t, ui.NetworkPathAvailable)
	plain, err := s.GetResource(t.Context(), ref)
	require.NoError(t, err)
	require.False(t, plain.NetworkPathAvailable)
	require.True(t, ui.NetworkPathAvailable)
	require.Zero(t, p.calls.Load())
}
