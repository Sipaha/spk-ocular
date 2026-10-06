package compose

import (
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
)

func TestStandaloneDetailsLogsExecAndReviewedActions(t *testing.T) {
	e := newTestEnv(t)
	e.s.proj = defaultProjections
	c := composeContainer(id(41), "", "", "running")
	c.Name = "/standalone"
	c.Config.Labels = map[string]string{}
	e.fe.PutContainer(c)
	ref := core.Ref{Provider: ProviderID, Target: e.s.target, Kind: KindContainers, Name: c.ID, UID: c.ID}
	obj, err := e.s.Get(t.Context(), ref)
	require.NoError(t, err)
	require.Empty(t, obj.Ref.Scope)
	require.Equal(t, "standalone", obj.Ref.Title)
	for _, r := range obj.Relations {
		require.NotEqual(t, KindServices, r.Ref.Kind)
	}
	info, err := e.s.LogInfo(t.Context(), ref)
	require.NoError(t, err)
	require.NotEmpty(t, info.Channels)
	execInfo, err := e.s.ExecInfo(t.Context(), ref)
	require.NoError(t, err)
	require.Equal(t, c.ID, execInfo.DefaultInstance)
	plan, err := e.s.PrepareAction(t.Context(), ref, "stop", core.ActionParams{})
	require.NoError(t, err)
	require.Nil(t, plan.Unavailable)
	require.Empty(t, plan.Where.Ref.Scope)
	for _, request := range e.fe.Requests() {
		require.Equal(t, "GET", request.Method, "prepare is read-only")
	}
	for _, bad := range []core.Ref{
		{Provider: ProviderID, Target: e.s.target, Kind: KindContainers, Name: c.ID, UID: c.ID, Scope: "other-project"},
		{Provider: ProviderID, Target: e.s.target, Kind: KindContainers, Name: c.ID, UID: "replacement"},
	} {
		_, err = e.s.Get(t.Context(), bad)
		require.Error(t, err)
		_, err = e.s.LogInfo(t.Context(), bad)
		require.Error(t, err)
		_, err = e.s.ExecInfo(t.Context(), bad)
		require.Error(t, err)
		_, err = e.s.PrepareAction(t.Context(), bad, "stop", core.ActionParams{})
		require.Error(t, err)
	}
}

func TestStandaloneCreationArrivesWithoutComposeLabels(t *testing.T) {
	e := newTestEnv(t)
	e.s.proj = defaultProjections
	sk := e.watch(KindContainers)
	sk.waitFor(t, "empty initial snapshot", hasRows())
	c := composeContainer(id(42), "", "", "running")
	c.Config.Labels = nil
	c.Name = "/standalone"
	e.fe.PutContainer(c)
	e.fe.Emit(containerEvent("create", c))
	sk.waitFor(t, "new standalone", hasRows(c.ID))
	e.fe.RemoveContainer(c.ID)
	e.fe.Emit(containerEvent("destroy", c))
	sk.waitFor(t, "standalone removed", hasRows())
}

func TestStandaloneMetricsRespectExplicitProjects(t *testing.T) {
	e := newTestEnv(t)
	c := replica(id(43), "", "", "1", "running")
	c.Config.Labels = nil
	e.fe.PutContainer(c)
	member := replica(id(44), "project", "web", "1", "running")
	e.fe.PutContainer(member)
	for _, scope := range []core.ScopeSel{{Mode: core.ScopeOne, Name: "project"}, {Mode: core.ScopeSome, Names: []string{}}} {
		m, err := e.s.Metrics(t.Context(), provider.Query{Kind: KindContainers, Scope: scope}, []string{c.ID, member.ID})
		require.NoError(t, err)
		require.NotContains(t, m.Values, c.ID)
		require.Zero(t, e.fe.Count("/containers/"+c.ID+"/stats"), "unselected containers must not even be sampled")
		if scope.Mode == core.ScopeSome {
			require.Empty(t, m.Values)
		} else {
			require.Contains(t, m.Values, member.ID)
		}
	}
	m, err := e.s.Metrics(t.Context(), metricsQuery(KindContainers), []string{c.ID})
	require.NoError(t, err)
	require.Contains(t, m.Values, c.ID)
}

func TestPartialComposeLabelsDoNotInventAProjectOrService(t *testing.T) {
	c := ctr("plain", "standalone", "", "web")
	w := world(c, network("unused", "unused", ""), vol("unused", "", ""), img("sha256:unused", "fixture:unused"))
	require.Empty(t, scopesOf(w))
	require.Empty(t, rowsOf(w, q(KindServices, "")))
	res, err := resourceOf(w, containerRef(c), time.Now())
	require.NoError(t, err)
	for _, relation := range res.Relations {
		require.NotEqual(t, "owner", relation.Type)
	}
	require.Len(t, rowsOf(w, q(KindNetworks, "")), 1)
	require.Len(t, rowsOf(w, q(KindVolumes, "")), 1)
	require.Len(t, rowsOf(w, q(KindImages, "")), 1)
	require.Empty(t, rowsOf(w, q(KindNetworks, "project")))
	require.Empty(t, rowsOf(w, q(KindVolumes, "project")))
}

// Explicit details retain the same full Engine inspect contract as Compose.
func TestStandaloneInspectPreservesDetails(t *testing.T) {
	c := ctr("plain", "standalone", "", "", raw(`{"Id":"plain","Config":{"Env":["SITE_MODE=local"]}}`))
	res, err := resourceOf(world(c), containerRef(c), time.Now())
	require.NoError(t, err)
	require.Contains(t, res.YAML, "SITE_MODE=local")
}
