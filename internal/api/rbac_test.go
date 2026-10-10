package api

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/providers/synthetic"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRBACAndAccessChecksAreUIOnlyAndRequireExplicitConnection(t *testing.T) {
	s, _ := newService(t, synthetic.New())
	req := RBACRequest{Provider: synthetic.ID, Target: synthetic.Target, Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}}
	access := AccessRequest{Provider: req.Provider, Target: req.Target, Attributes: core.AccessAttributes{Verb: "get", Resource: "pods", Namespace: "blue"}}
	_, err := s.RBACSnapshot(context.Background(), req)
	require.Error(t, err)
	_, err = s.CheckAccess(context.Background(), access)
	require.Error(t, err)
	_, err = s.RBACSnapshot(UIContext(context.Background()), req)
	require.Error(t, err)
	_, err = s.CheckAccess(UIContext(context.Background()), access)
	require.Error(t, err)
	_, err = s.ListTargets(context.Background())
	require.NoError(t, err)
	_, err = s.ConnectTarget(UIContext(context.Background()), req.Provider, req.Target)
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	out, err := s.RBACSnapshot(UIContext(context.Background()), req)
	require.NoError(t, err)
	require.NotEmpty(t, out.Grants)
	review, err := s.CheckAccess(UIContext(context.Background()), access)
	require.NoError(t, err)
	require.Equal(t, "allowed", review.State)
	req.Subject = &core.Ref{Provider: req.Provider, Target: "other", Scope: "blue", Name: "x"}
	_, err = s.RBACSnapshot(UIContext(context.Background()), req)
	require.Error(t, err)
	req.Subject = &core.Ref{Provider: req.Provider, Target: req.Target, Scope: "green", Name: "x"}
	_, err = s.RBACSnapshot(UIContext(context.Background()), req)
	require.Error(t, err)
	access.Ref = &core.Ref{Provider: req.Provider, Target: "other", Kind: "pods"}
	_, err = s.CheckAccess(UIContext(context.Background()), access)
	require.Error(t, err)
	access.Ref = nil
	for _, attributes := range []core.AccessAttributes{{Verb: "get", Resource: "pods/log"}, {Verb: "create", Resource: "pods", Name: "web"}, {Verb: "deletecollection", Resource: "pods", Name: "web"}, {Verb: "get", Resource: "pods", Namespace: "blue\n"}, {Verb: "get", Resource: "pods", Subresource: "a/b"}} {
		access.Attributes = attributes
		_, err = s.CheckAccess(UIContext(context.Background()), access)
		require.Error(t, err)
	}
	ui, err := s.ListKinds(UIContext(context.Background()), req.Provider, req.Target)
	require.NoError(t, err)
	found := false
	for _, kind := range ui.Kinds {
		if kind.Workspace == "rbac" {
			found = true
		}
	}
	require.True(t, found)
	agent, err := s.ListKinds(context.Background(), req.Provider, req.Target)
	require.NoError(t, err)
	for _, kind := range agent.Kinds {
		require.NotEqual(t, "rbac", kind.Workspace)
	}
}
