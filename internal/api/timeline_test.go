package api

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/providers/synthetic"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestTimelineIsUIOnlyAndNeverImplicitlyConnects(t *testing.T) {
	s, _ := newService(t, synthetic.New())
	req := TimelineRequest{Provider: synthetic.ID, Target: synthetic.Target, Scope: core.ScopeSel{Mode: core.ScopeAll}}
	_, err := s.ClusterTimeline(context.Background(), req)
	require.Error(t, err)
	_, err = s.ClusterTimeline(UIContext(context.Background()), req)
	require.Error(t, err)
	_, err = s.ListTargets(context.Background())
	require.NoError(t, err)
	_, err = s.ConnectTarget(UIContext(context.Background()), req.Provider, req.Target)
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	timeline, err := s.ClusterTimeline(UIContext(context.Background()), req)
	require.NoError(t, err)
	require.NotEmpty(t, timeline.Events)
	require.NotEmpty(t, timeline.Resources)
	ui, err := s.ListKinds(UIContext(context.Background()), req.Provider, req.Target)
	require.NoError(t, err)
	found := false
	for _, kind := range ui.Kinds {
		if kind.Workspace == "timeline" {
			found = true
		}
	}
	require.True(t, found)
	agent, err := s.ListKinds(context.Background(), req.Provider, req.Target)
	require.NoError(t, err)
	for _, kind := range agent.Kinds {
		require.NotEqual(t, "timeline", kind.Workspace)
	}
	for _, scope := range []core.ScopeSel{{Mode: core.ScopeNone}, {Mode: core.ScopeOne}, {Mode: core.ScopeAll, Name: "blue"}} {
		req.Scope = scope
		_, err = s.ClusterTimeline(UIContext(context.Background()), req)
		require.Error(t, err)
	}
}
