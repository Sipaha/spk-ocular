package api

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type reliabilityTestSession struct {
	*argoTestSession
	onRead func(context.Context)
}

func (s *reliabilityTestSession) ResourceReliability(ctx context.Context, ref core.Ref) (core.Reliability, error) {
	s.owner.calls.Add(1)
	if s.onRead != nil {
		s.onRead(ctx)
	}
	return core.Reliability{Ref: ref}, ctx.Err()
}

type reliabilityTestProvider struct {
	*argoTestProvider
	onRead func(context.Context)
}

func (p *reliabilityTestProvider) Open(ctx context.Context, target string) (provider.Session, error) {
	s, e := p.argoTestProvider.Open(ctx, target)
	if e != nil {
		return nil, e
	}
	return &reliabilityTestSession{s.(*argoTestSession), p.onRead}, nil
}
func TestResourceReliabilityRequiresUIConnectionAndPinnedIdentity(t *testing.T) {
	p := &reliabilityTestProvider{argoTestProvider: &argoTestProvider{openable: newOpenable("a")}}
	s, _ := newService(t, p)
	ref := core.Ref{Provider: "k", Target: "a", Kind: "apps/deployments", Scope: "blue", Name: "p", UID: "uid"}
	_, err := s.ResourceReliability(t.Context(), ref)
	require.Error(t, err)
	_, err = s.ResourceReliability(UIContext(t.Context()), ref)
	require.Error(t, err)
	require.Empty(t, p.opened)
	require.Zero(t, p.calls.Load())
	_, err = s.ConnectTarget(UIContext(t.Context()), "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	out, err := s.ResourceReliability(UIContext(t.Context()), ref)
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
		_, err = s.ResourceReliability(UIContext(t.Context()), r)
		require.Error(t, err)
	}
	call, err := s.AgentCall(UIContext(t.Context()), "k", "a")
	require.NoError(t, err)
	defer call.Done()
	_, err = s.ResourceReliability(call.ctx, ref)
	require.Error(t, err)
	require.EqualValues(t, 1, p.calls.Load())
}

func (s *reliabilityTestSession) Get(_ context.Context, ref core.Ref) (*core.Resource, error) {
	return &core.Resource{Ref: ref, ReliabilityAvailable: true}, nil
}
func TestResourceReliabilityAvailabilityIsUIOnlyAndDoesNotCollect(t *testing.T) {
	p := &reliabilityTestProvider{argoTestProvider: &argoTestProvider{openable: newOpenable("a")}}
	s, _ := newService(t, p)
	_, err := s.ConnectTarget(UIContext(t.Context()), "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	ref := core.Ref{Provider: "k", Target: "a", Scope: "blue", Kind: "apps/deployments", Name: "web", UID: "uid"}
	ui, err := s.GetResource(UIContext(t.Context()), ref)
	require.NoError(t, err)
	require.True(t, ui.ReliabilityAvailable)
	plain, err := s.GetResource(t.Context(), ref)
	require.NoError(t, err)
	require.False(t, plain.ReliabilityAvailable)
	require.True(t, ui.ReliabilityAvailable)
	require.Zero(t, p.calls.Load())
}

func TestResourceReliabilityRejectsLateDisconnectedAndCancelledResult(t *testing.T) {
	for _, mode := range []string{"disconnect", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			p := &reliabilityTestProvider{argoTestProvider: &argoTestProvider{openable: newOpenable("a")}}
			s, _ := newService(t, p)
			ctx, cancel := context.WithCancel(UIContext(t.Context()))
			defer cancel()
			p.onRead = func(c context.Context) {
				deadline, ok := c.Deadline()
				require.True(t, ok)
				require.LessOrEqual(t, time.Until(deadline), 20*time.Second)
				if mode == "disconnect" {
					require.NoError(t, s.CloseTarget(UIContext(t.Context()), "k", "a"))
				} else {
					cancel()
				}
			}
			_, err := s.ConnectTarget(UIContext(t.Context()), "k", "a")
			require.NoError(t, err)
			awaitConnection(t, s, "connected")
			out, err := s.ResourceReliability(ctx, core.Ref{Provider: "k", Target: "a", Kind: "pods", Scope: "blue", Name: "web", UID: "uid"})
			require.Error(t, err)
			require.Empty(t, out.Observations)
		})
	}
}
