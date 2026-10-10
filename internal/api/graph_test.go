package api

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/providers/synthetic"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestClusterGraphIsUIOnlyAndRequiresAnExplicitConnection(t *testing.T) {
	s, _ := newService(t, synthetic.New())
	req := GraphRequest{Provider: synthetic.ID, Target: synthetic.Target, Scope: core.ScopeSel{Mode: core.ScopeAll}}
	_, err := s.ClusterGraph(context.Background(), req)
	require.Error(t, err)
	_, err = s.ClusterGraph(UIContext(context.Background()), req)
	require.Error(t, err)
	_, err = s.ListTargets(context.Background())
	require.NoError(t, err)
	_, err = s.ConnectTarget(UIContext(context.Background()), req.Provider, req.Target)
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	g, err := s.ClusterGraph(UIContext(context.Background()), req)
	require.NoError(t, err)
	require.NotEmpty(t, g.Nodes)
	ui, err := s.ListKinds(UIContext(context.Background()), req.Provider, req.Target)
	require.NoError(t, err)
	found := false
	for _, k := range ui.Kinds {
		if k.Workspace == "graph" {
			found = true
		}
	}
	require.True(t, found)
	agent, err := s.ListKinds(context.Background(), req.Provider, req.Target)
	require.NoError(t, err)
	for _, k := range agent.Kinds {
		require.NotEqual(t, "graph", k.Workspace)
	}
	req.Scope = core.ScopeSel{Mode: core.ScopeNone}
	_, err = s.ClusterGraph(UIContext(context.Background()), req)
	require.Error(t, err)
}
