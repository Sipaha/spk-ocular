package kubernetes

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stesting "k8s.io/client-go/testing"
)

func argoSettingsFixture(ns, name, kind string, fields map[string]string) *unstructured.Unstructured {
	data := map[string]any{}
	for k, v := range fields {
		data[k] = base64.StdEncoding.EncodeToString([]byte(v))
	}
	return mk("v1", "Secret", ns, name, "uid-"+name, map[string]any{"metadata": map[string]any{"labels": map[string]any{"argocd.argoproj.io/secret-type": kind}}, "data": data})
}
func TestArgoSettingsAreExplicitScopedWhitelistProjections(t *testing.T) {
	repo := argoSettingsFixture("ns", "repository", "repository", map[string]string{"name": "commerce", "type": "git", "url": "https://private-user:private-password@git.invalid/repo?token=private-query#private-fragment", "project": "commerce", "username": "PRIVATE-USERNAME", "password": "PRIVATE-PASSWORD", "sshPrivateKey": "PRIVATE-KEY"})
	cluster := argoSettingsFixture("ns", "cluster", "cluster", map[string]string{"name": "production", "server": "https://private-user:private-password@cluster.invalid?token=PRIVATE-TOKEN", "config": "PRIVATE-CONFIG", "bearerToken": "PRIVATE-TOKEN"})
	credentials := argoSettingsFixture("ns", "template", "repo-creds", map[string]string{"url": "https://templates.invalid", "password": "PRIVATE-TEMPLATE"})
	other := argoSettingsFixture("other", "other", "repository", map[string]string{"url": "https://other.invalid"})
	s, c := argoSession(t, repo, cluster, credentials, other)
	_, err := s.ArgoCD(t.Context(), core.ScopeSel{Mode: core.ScopeOne, Name: "ns"})
	require.NoError(t, err)
	for _, a := range c.Actions() {
		assert.NotEqual(t, "secrets", a.GetResource().Resource)
	}
	c.ClearActions()
	out, err := s.ArgoSettings(t.Context(), core.ScopeSel{Mode: core.ScopeOne, Name: "ns"}, "repository")
	require.NoError(t, err)
	require.Len(t, out.Settings, 1)
	require.Empty(t, out.Objects)
	require.Empty(t, out.Events)
	assert.Equal(t, "https://git.invalid/repo", out.Settings[0].URL)
	assert.Equal(t, "commerce", out.Settings[0].Project)
	clusterOut, clusterErr := s.ArgoSettings(t.Context(), core.ScopeSel{Mode: core.ScopeOne, Name: "ns"}, "cluster")
	require.NoError(t, clusterErr)
	require.Len(t, clusterOut.Settings, 1)
	assert.Equal(t, "https://cluster.invalid", clusterOut.Settings[0].URL)
	out.Settings = append(out.Settings, clusterOut.Settings...)
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	for _, secret := range []string{"PRIVATE-", "private-user", "private-password", "private-query", "private-fragment", "templates.invalid", "other.invalid", "config", "password", "sshPrivateKey"} {
		assert.NotContains(t, string(raw), secret)
	}
	for _, a := range c.Actions() {
		require.Equal(t, "ns", a.GetNamespace())
		require.Equal(t, "secrets", a.GetResource().Resource)
		label := a.(k8stesting.ListAction).GetListRestrictions().Labels.String()
		assert.Contains(t, []string{"argocd.argoproj.io/secret-type=repository", "argocd.argoproj.io/secret-type=cluster"}, label)
	}
	c.ClearActions()
	out, err = s.ArgoSettings(t.Context(), core.ScopeSel{Mode: core.ScopeSome, Names: []string{}}, "repository")
	require.NoError(t, err)
	assert.Empty(t, out.Settings)
	assert.Empty(t, c.Actions())
}
func TestArgoSettingsDeniedErrorsAndMalformedFieldsDoNotLeak(t *testing.T) {
	repo := argoSettingsFixture("ns", "repository", "repository", map[string]string{"url": "ssh://" + strings.Repeat("PRIVATE", 100) + ":private-password@git.invalid/repo?token=PRIVATE", "password": "PRIVATE-PASSWORD"})
	_ = unstructured.SetNestedField(repo.Object, "!PRIVATE-BAD-BASE64!", "data", "project")
	s, c := argoSession(t, repo)
	out, err := s.ArgoSettings(t.Context(), core.ScopeSel{Mode: core.ScopeOne, Name: "ns"}, "repository")
	require.NoError(t, err)
	require.Len(t, out.Settings, 1)
	assert.Equal(t, "ssh://git.invalid/repo", out.Settings[0].URL)
	assert.True(t, out.Truncated)
	c.PrependReactor("list", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "PRIVATE-SECRET", assert.AnError)
	})
	out, err = s.ArgoSettings(t.Context(), core.ScopeSel{Mode: core.ScopeOne, Name: "ns"}, "repository")
	require.NoError(t, err)
	require.Len(t, out.Coverage, 1)
	assert.Equal(t, "forbidden", out.Coverage[0].State)
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "PRIVATE")
}
func TestArgoTrackingRequiresSelfReferenceAndDoesNotClaimLabelsAreOwnership(t *testing.T) {
	u := mk("apps/v1", "Deployment", "ns", "api", "uid-api", nil)
	u.SetLabels(map[string]string{"app.kubernetes.io/instance": "commerce"})
	hint := argoTracking(u)
	require.NotNil(t, hint)
	assert.Equal(t, "label", hint.Method)
	u.SetAnnotations(map[string]string{"argocd.argoproj.io/tracking-id": "commerce:apps/Deployment:ns/api"})
	hint = argoTracking(u)
	require.NotNil(t, hint)
	assert.Equal(t, "annotation", hint.Method)
	for _, value := range []string{"invalid", "commerce:apps/Deployment:other/api", "commerce:apps/Deployment:ns/copied", "commerce:/Service:ns/api"} {
		u.SetAnnotations(map[string]string{"argocd.argoproj.io/tracking-id": value})
		assert.Nil(t, argoTracking(u), value)
	}
	node := mk("v1", "Node", "", "worker", "uid-worker", nil)
	node.SetAnnotations(map[string]string{"argocd.argoproj.io/tracking-id": "infra:/Node:default/worker"})
	require.NotNil(t, argoTracking(node), "cluster-scoped tracking can use the Application destination namespace")
	u.SetAnnotations(nil)
	u.SetLabels(nil)
	assert.Nil(t, argoTracking(u))
}
