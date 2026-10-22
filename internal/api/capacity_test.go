package api

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type capacityTestSession struct {
	*argoTestSession
	onRead func(context.Context)
}

func (s *capacityTestSession) NodeCapacity(ctx context.Context, req core.NodeCapacityRequest) (core.NodeCapacity, error) {
	s.owner.calls.Add(1)
	if s.onRead != nil {
		s.onRead(ctx)
	}
	return core.NodeCapacity{Ref: req.Ref, Namespace: req.Namespace}, ctx.Err()
}

type capacityTestProvider struct {
	*argoTestProvider
	onRead func(context.Context)
}

func (p *capacityTestProvider) Open(ctx context.Context, target string) (provider.Session, error) {
	s, e := p.argoTestProvider.Open(ctx, target)
	if e != nil {
		return nil, e
	}
	return &capacityTestSession{s.(*argoTestSession), p.onRead}, nil
}
func TestNodeCapacityRequiresUIConnectionAndPinnedIdentity(t *testing.T) {
	p := &capacityTestProvider{argoTestProvider: &argoTestProvider{openable: newOpenable("a")}}
	s, _ := newService(t, p)
	ref := core.Ref{Provider: "k", Target: "a", Kind: "nodes", Name: "p", UID: "uid"}
	_, err := s.NodeCapacity(t.Context(), core.NodeCapacityRequest{Ref: ref, Namespace: "blue"})
	require.Error(t, err)
	_, err = s.NodeCapacity(UIContext(t.Context()), core.NodeCapacityRequest{Ref: ref, Namespace: "blue"})
	require.Error(t, err)
	require.Empty(t, p.opened)
	require.Zero(t, p.calls.Load())
	_, err = s.ConnectTarget(UIContext(t.Context()), "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	out, err := s.NodeCapacity(UIContext(t.Context()), core.NodeCapacityRequest{Ref: ref, Namespace: "blue"})
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
			r.Scope = "blue"
		}
		_, err = s.NodeCapacity(UIContext(t.Context()), core.NodeCapacityRequest{Ref: r, Namespace: "blue"})
		require.Error(t, err)
	}
	call, err := s.AgentCall(UIContext(t.Context()), "k", "a")
	require.NoError(t, err)
	defer call.Done()
	_, err = s.NodeCapacity(call.ctx, core.NodeCapacityRequest{Ref: ref, Namespace: "blue"})
	require.Error(t, err)
	require.EqualValues(t, 1, p.calls.Load())
}

func (s *capacityTestSession) Get(_ context.Context, ref core.Ref) (*core.Resource, error) {
	return &core.Resource{Ref: ref, NodeCapacityAvailable: true}, nil
}
func TestNodeCapacityAvailabilityIsUIOnlyAndDoesNotCollect(t *testing.T) {
	p := &capacityTestProvider{argoTestProvider: &argoTestProvider{openable: newOpenable("a")}}
	s, _ := newService(t, p)
	_, err := s.ConnectTarget(UIContext(t.Context()), "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	ref := core.Ref{Provider: "k", Target: "a", Kind: "nodes", Name: "web", UID: "uid"}
	ui, err := s.GetResource(UIContext(t.Context()), ref)
	require.NoError(t, err)
	require.True(t, ui.NodeCapacityAvailable)
	plain, err := s.GetResource(t.Context(), ref)
	require.NoError(t, err)
	require.False(t, plain.NodeCapacityAvailable)
	require.True(t, ui.NodeCapacityAvailable)
	require.Zero(t, p.calls.Load())
}

func TestNodeCapacityRejectsLateDisconnectedAndCancelledResult(t *testing.T) {
	for _, mode := range []string{"disconnect", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			p := &capacityTestProvider{argoTestProvider: &argoTestProvider{openable: newOpenable("a")}}
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
			out, err := s.NodeCapacity(ctx, core.NodeCapacityRequest{Ref: core.Ref{Provider: "k", Target: "a", Kind: "nodes", Name: "web", UID: "uid"}, Namespace: "blue"})
			require.Error(t, err)
			require.Empty(t, out.Pods)
		})
	}
}
