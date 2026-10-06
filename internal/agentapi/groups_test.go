package agentapi

import (
	"context"
	"testing"

	"github.com/spk/spk-ocular/internal/agentgrant"
	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNamedGroupsAreAdditiveAndSwitchable(t *testing.T) {
	e := newEnv(t)
	scope := agentgrant.Scope{Mode: agentgrant.ScopeOne, Name: "a"}
	req := api.SaveAgentGrantsRequest{Provider: "k", Target: "t", Groups: []agentgrant.Group{
		{ID: "base", Name: "Base", Scope: scope, Grants: []agentgrant.Grant{one("a", "read")}},
		{ID: "limited", Name: "Pods only", Scope: scope, Grants: []agentgrant.Grant{one("a", "read", "pods")}},
	}}
	save := func() { require.NoError(t, e.svc.SaveAgentGrants(context.Background(), req)) }
	save()
	e.ok("GetObject", map[string]any{"ref": ref("a", "apps/deployments", "web")})
	req.Groups[1].Disabled = true
	save()
	e.ok("GetObject", map[string]any{"ref": ref("a", "apps/deployments", "web")})
	req.Groups[0].Disabled = true
	req.Groups[1].Disabled = false
	save()
	e.refused("GetObject", map[string]any{"ref": ref("a", "apps/deployments", "web")})
	e.ok("GetObject", map[string]any{"ref": ref("a", "pods", "web-1")})
	req.Groups[1].Disabled = true
	save()
	access := e.ok("Access", nil)["targets"].([]any)[0].(map[string]any)
	assert.Equal(t, "paused", access["state"])
	assert.Empty(t, access["grants"])
	e.refused("ListKinds", tgt)
	got, err := e.svc.ListAgentGrants(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, req.Groups, got[0].Groups)
}

func TestNamespaceMasterBlocksAllMethodsIncludingInheritedGrants(t *testing.T) {
	e := newEnv(t)
	all := agentgrant.Scope{Mode: agentgrant.ScopeAll}
	req := api.SaveAgentGrantsRequest{Provider: "k", Target: "t", Groups: []agentgrant.Group{
		{ID: "all", Name: "All", Scope: all, Grants: []agentgrant.Grant{
			{Scope: all, Verb: "read"}, {Scope: all, Verb: "logs"}, {Scope: all, Verb: "edit"}, {Scope: all, Verb: "action:restart"},
		}},
	}, DisabledScopes: []agentgrant.Scope{{Mode: agentgrant.ScopeOne, Name: "a"}}}
	require.NoError(t, e.svc.SaveAgentGrants(context.Background(), req))
	pod := ref("a", "pods", "web-1")
	deployment := ref("a", "apps/deployments", "web")
	for method, body := range map[string]any{
		"GetObject":     map[string]any{"ref": pod},
		"GetLogs":       map[string]any{"ref": pod},
		"GetLogInfo":    map[string]any{"ref": pod},
		"GetMetrics":    map[string]any{"refs": []core.Ref{pod}},
		"ListObjects":   with(tgt, "kind", "pods", "scope", "a"),
		"Problems":      with(tgt, "scope", "a"),
		"GetEditSource": map[string]any{"ref": deployment},
		"PrepareAction": map[string]any{"ref": deployment, "action": "restart"},
	} {
		e.refused(method, body)
	}
	out := e.ok("ListObjects", with(tgt, "kind", "pods"))
	assert.Equal(t, []string{"b/api-1", "c/x-1"}, names(out))
	out = e.ok("Problems", tgt)
	for _, row := range out["rows"].([]any) {
		assert.NotEqual(t, "a", row.(map[string]any)["ref"].(map[string]any)["scope"])
	}
	req.DisabledScopes = nil
	require.NoError(t, e.svc.SaveAgentGrants(context.Background(), req))
	assert.Equal(t, []string{"a/web-1", "b/api-1", "c/x-1"}, names(e.ok("ListObjects", with(tgt, "kind", "pods"))))
}

func TestPausedGroupOrNamespaceStopsPreparedAndPendingWrites(t *testing.T) {
	for _, master := range []bool{false, true} {
		t.Run(map[bool]string{true: "scope", false: "group"}[master], func(t *testing.T) {
			e := newEnv(t)
			scope := agentgrant.Scope{Mode: agentgrant.ScopeOne, Name: "a"}
			req := api.SaveAgentGrantsRequest{Provider: "k", Target: "t", Groups: []agentgrant.Group{
				{ID: "write", Name: "Maintenance", Scope: scope, Grants: []agentgrant.Grant{one("a", "action:restart"), one("a", "action:delete", "apps/deployments")}},
			}}
			save := func() { require.NoError(t, e.svc.SaveAgentGrants(context.Background(), req)) }
			save()
			restart := prepare(e, "web", "restart", nil)
			removal := prepare(e, "web", "delete", nil)
			pending := e.ok("RunAction", map[string]any{"planId": removal["planId"]})
			if master {
				req.DisabledScopes = []agentgrant.Scope{scope}
			} else {
				req.Groups[0].Disabled = true
			}
			save()
			e.refused("RunAction", map[string]any{"planId": restart["planId"]})
			runID := pending["runId"].(string)
			require.NoError(t, e.svc.DecideAgentPending(context.Background(), api.DecideAgentPendingRequest{ID: runID, Approve: true}))
			result := waitRun(e, runID)
			assert.Equal(t, stateFailed, result["state"])
			assert.Empty(t, e.runs())
		})
	}
}

func TestEmptyNamedGroupHasAnArrayInTheUI(t *testing.T) {
	e := newEnv(t)
	require.NoError(t, e.svc.SaveAgentGrants(context.Background(), api.SaveAgentGrantsRequest{
		Provider: "k", Target: "t", Groups: []agentgrant.Group{{ID: "empty", Name: "Draft", Scope: agentgrant.Scope{Mode: agentgrant.ScopeOne, Name: "a"}}},
	}))
	out, err := e.svc.ListAgentGrants(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Len(t, out[0].Groups, 1)
	require.NotNil(t, out[0].Groups[0].Grants)
	assert.Empty(t, out[0].Groups[0].Grants)
}

// A scoped Docker kind can now contain standalone rows with an empty project.
// Existing all-project or cluster grants must not expose these additional rows.
func TestAllProjectGrantsDoNotAcquireStandaloneContainers(t *testing.T) {
	e := newEnv(t)
	oldKinds, oldWorld := fakeKinds, world
	t.Cleanup(func() { fakeKinds = oldKinds; world = oldWorld })
	fakeKinds = append(append([]core.KindDescriptor(nil), oldKinds...), core.KindDescriptor{ID: "containers", Scoped: true, Logs: true, Actions: []core.ActionDescriptor{actRestart}})
	world = map[string][]core.Ref{"containers": {ref("a", "containers", "compose-container"), ref("", "containers", "standalone")}}
	e.grant(
		agentgrant.Grant{Scope: agentgrant.Scope{Mode: agentgrant.ScopeAll}, Verb: "read"},
		agentgrant.Grant{Scope: agentgrant.Scope{Mode: agentgrant.ScopeAll}, Verb: "logs"},
		agentgrant.Grant{Scope: agentgrant.Scope{Mode: agentgrant.ScopeAll}, Verb: "action:restart", Kinds: []string{"containers"}},
		agentgrant.Grant{Scope: agentgrant.Scope{Mode: agentgrant.ScopeCluster}, Verb: "read"},
	)
	require.Equal(t, []string{"a/compose-container"}, names(e.ok("ListObjects", with(tgt, "kind", "containers"))))
	standalone := ref("", "containers", "standalone")
	e.refused("GetObject", map[string]any{"ref": standalone})
	e.refused("GetLogInfo", map[string]any{"ref": standalone})
	e.refused("PrepareAction", map[string]any{"ref": standalone, "action": "restart"})
}
