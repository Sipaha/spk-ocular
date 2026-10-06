package api

import (
	"context"
	"errors"
	"github.com/spk/spk-ocular/internal/provider"
	"helm.sh/helm/v4/pkg/action"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/helm"
	"github.com/spk/spk-ocular/internal/providers/synthetic"
	"github.com/stretchr/testify/require"
)

func TestHelmUIBoundaryScopesAndRevision(t *testing.T) {
	p := synthetic.New()
	s, _ := newService(t, p)
	s.helmRepos = helm.NewRepositories(t.TempDir())
	ctx := UIContext(t.Context())
	_, err := s.Helm(context.Background(), HelmRequest{Command: "settings"})
	require.ErrorContains(t, err, "only to the application UI")
	req := HelmRequest{Command: "list", Provider: synthetic.ID, Target: synthetic.Target, Scope: core.ScopeSel{Mode: core.ScopeAll}}
	_, err = s.Helm(ctx, req)
	require.Error(t, err, "reads cannot connect implicitly")
	state, err := s.ConnectTarget(ctx, synthetic.ID, synthetic.Target)
	require.NoError(t, err)
	require.Equal(t, "connecting", state.State)
	require.Eventually(t, func() bool { _, err := s.Helm(ctx, req); return err == nil }, time.Second, 5*time.Millisecond)
	rows, err := s.Helm(ctx, req)
	require.NoError(t, err)
	require.Len(t, rows.Releases, 1)
	req.Scope = core.ScopeSel{Mode: core.ScopeSome, Names: []string{}}
	rows, err = s.Helm(ctx, req)
	require.NoError(t, err)
	require.Empty(t, rows.Releases)
	req.Scope = core.ScopeSel{Mode: core.ScopeOne, Name: "green"}
	rows, err = s.Helm(ctx, req)
	require.NoError(t, err)
	require.Empty(t, rows.Releases)
	req.Scope = core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}
	rows, err = s.Helm(ctx, req)
	require.NoError(t, err)
	require.Len(t, rows.Releases, 1)
	kinds, err := s.ListKinds(ctx, synthetic.ID, synthetic.Target)
	require.NoError(t, err)
	found := false
	for _, k := range kinds.Kinds {
		if k.Workspace == "helm-releases" {
			found = true
		}
	}
	require.True(t, found)
	kinds, err = s.ListKinds(t.Context(), synthetic.ID, synthetic.Target)
	require.NoError(t, err)
	for _, k := range kinds.Kinds {
		require.Empty(t, k.Workspace, "agent catalog must not inherit Helm")
	}
	prepare := HelmRequest{Command: "prepare", Provider: synthetic.ID, Target: synthetic.Target, Operation: helm.Operation{Action: "uninstall", Name: "demo-web", Namespace: "blue", TimeoutSeconds: 30}}
	preview, err := s.Helm(ctx, prepare)
	require.NoError(t, err)
	require.NotEmpty(t, preview.Plan.ID)
	run := HelmRequest{Command: "run", Provider: synthetic.ID, Target: synthetic.Target, PlanID: preview.Plan.ID, ConfigRev: "stale"}
	_, err = s.Helm(ctx, run)
	require.ErrorContains(t, err, "conflict")
	run.ConfigRev = preview.ConfigRev
	result, err := s.Helm(ctx, run)
	require.NoError(t, err)
	require.Equal(t, "done", result.Result.Outcome)
	_, err = s.Helm(ctx, run)
	require.ErrorContains(t, err, "missing")
}

type partialHelmProvider struct{ *synthetic.Provider }
type partialHelmSession struct{ provider.Session }

func (p partialHelmProvider) Open(ctx context.Context, target string) (provider.Session, error) {
	s, e := p.Provider.Open(ctx, target)
	if e != nil {
		return nil, e
	}
	return &partialHelmSession{s}, nil
}
func (s *partialHelmSession) HelmConfiguration(ctx context.Context, namespace string, storage helm.Storage) (*action.Configuration, error) {
	if namespace == "denied" {
		return nil, errors.New("namespace access denied")
	}
	return s.Session.(helmSession).HelmConfiguration(ctx, namespace, storage)
}
func TestHelmKeepsSuccessfulNamespacesAndReportsDeniedCoverage(t *testing.T) {
	s, _ := newService(t, partialHelmProvider{synthetic.New()})
	s.helmRepos = helm.NewRepositories(t.TempDir())
	ctx := UIContext(t.Context())
	_, err := s.ConnectTarget(ctx, synthetic.ID, synthetic.Target)
	require.NoError(t, err)
	require.Eventually(t, func() bool { _, err := s.ListKinds(ctx, synthetic.ID, synthetic.Target); return err == nil }, time.Second, 5*time.Millisecond)
	result, err := s.Helm(UIContext(t.Context()), HelmRequest{Command: "list", Provider: synthetic.ID, Target: synthetic.Target, Scope: core.ScopeSel{Mode: core.ScopeSome, Names: []string{"blue", "denied"}}})
	require.NoError(t, err)
	require.Len(t, result.Releases, 1)
	require.Equal(t, "blue", result.Releases[0].Namespace)
	require.Equal(t, []string{"denied: namespace access denied"}, result.Problems)
}

func (s *partialHelmSession) CheckConnection(ctx context.Context) error {
	return s.Session.(provider.ConnectionChecker).CheckConnection(ctx)
}
