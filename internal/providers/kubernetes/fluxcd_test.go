package kubernetes

import (
	"context"
	"encoding/json"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
	"strings"
	"testing"
)

func fluxFixture(ns, name string) *unstructured.Unstructured {
	u := mk("kustomize.toolkit.fluxcd.io/v1", "Kustomization", ns, name, "flux-"+name, map[string]any{
		"metadata": map[string]any{"generation": int64(2)},
		"spec":     map[string]any{"path": "./app", "sourceRef": map[string]any{"kind": "GitRepository", "name": "source"}, "postBuild": map[string]any{"substitute": map[string]any{"TOKEN": "PRIVATE-TOKEN"}}, "decryption": map[string]any{"secretRef": map[string]any{"name": "PRIVATE-KEY"}}},
		"status":   map[string]any{"observedGeneration": int64(2), "lastAppliedRevision": "main@sha1:" + strings.Repeat("a", 40), "conditions": []any{map[string]any{"type": "Ready", "status": "True", "observedGeneration": int64(2), "message": "Reconciled"}}, "inventory": map[string]any{"entries": []any{map[string]any{"id": ns + "_api_apps_Deployment", "v": "v1"}}}}})
	return u
}
func fluxSession(t *testing.T, objects ...runtime.Object) (*session, *dynamicfake.FakeDynamicClient) {
	t.Helper()
	lists := map[schema.GroupVersionResource]string{{Version: "v1", Resource: "events"}: "EventList"}
	defs := []*kindDef{}
	for _, k := range core.FluxResourceKinds {
		version := "v1"
		if k.Kind == "HelmRelease" {
			version = "v2"
		}
		gvr := schema.GroupVersionResource{Group: k.Group, Version: version, Resource: k.Resource}
		lists[gvr] = k.Kind + "List"
		defs = append(defs, discoveredDef(apiResource{Group: k.Group, Version: version, Resource: k.Resource, Kind: k.Kind, Namespaced: true, Verbs: []string{"list", "get", "watch"}}))
	}
	c := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), lists, objects...)
	s := newSession("ctx", "rev", c, false)
	snap := *s.cat.snap()
	snap.reg = newKindRegistry(append(snap.reg.list, defs...)...)
	s.cat.cur.Store(&snap)
	t.Cleanup(s.Close)
	return s, c
}
func TestFluxProjectionScopePrivacyAndControllerStatus(t *testing.T) {
	a := fluxFixture("ns", "app")
	source := mk("source.toolkit.fluxcd.io/v1", "GitRepository", "ns", "source", "source-uid", map[string]any{"spec": map[string]any{"url": "https://USER:PRIVATE-PASSWORD@git.invalid/repo?token=PRIVATE-TOKEN", "secretRef": map[string]any{"name": "PRIVATE-SECRET"}}, "status": map[string]any{"artifact": map[string]any{"revision": "main@sha1:" + strings.Repeat("b", 40)}}})
	s, c := fluxSession(t, a, source, fluxFixture("outside", "app"))
	out, err := s.FluxCD(t.Context(), core.ScopeSel{Mode: core.ScopeOne, Name: "ns"})
	require.NoError(t, err)
	require.Len(t, out.Objects, 2)
	raw, _ := json.Marshal(out)
	assert.NotContains(t, string(raw), "PRIVATE")
	assert.NotContains(t, string(raw), "USER:")
	assert.Contains(t, string(raw), "https://git.invalid/repo")
	for _, o := range out.Objects {
		if o.Kind == "Kustomization" {
			assert.Equal(t, "Ready", o.State)
			require.Len(t, o.Inventory, 1)
			assert.Equal(t, "Deployment", o.Inventory[0].Kind)
			require.Len(t, o.Links, 1)
			assert.Equal(t, "ns", o.Links[0].Namespace)
		}
	}
	for _, a := range c.Actions() {
		assert.Equal(t, "list", a.GetVerb())
		assert.Equal(t, "ns", a.GetNamespace())
		assert.NotEqual(t, "secrets", a.GetResource().Resource)
	}
	c.ClearActions()
	out, err = s.FluxCD(t.Context(), core.ScopeSel{Mode: core.ScopeSome, Names: []string{}})
	require.NoError(t, err)
	assert.Empty(t, out.Objects)
	assert.Empty(t, c.Actions())
}
func TestFluxStatusGenerationSuspensionAndRemoteDestination(t *testing.T) {
	s, _ := fluxSession(t)
	for _, tc := range []struct {
		condition, status string
		observed          int64
		suspend           bool
		want              string
	}{{"Ready", "True", 2, false, "Ready"}, {"Ready", "True", 1, false, "Unknown"}, {"Ready", "False", 2, false, "Failed"}, {"Reconciling", "True", 2, false, "Reconciling"}, {"Stalled", "True", 2, false, "Failed"}, {"Ready", "True", 2, true, "Suspended"}} {
		u := fluxFixture("ns", "app")
		require.NoError(t, unstructured.SetNestedField(u.Object, tc.suspend, "spec", "suspend"))
		require.NoError(t, unstructured.SetNestedSlice(u.Object, []any{map[string]any{"type": tc.condition, "status": tc.status, "observedGeneration": tc.observed}}, "status", "conditions"))
		require.NoError(t, unstructured.SetNestedField(u.Object, int64(1), "status", "observedGeneration"))
		o := s.fluxObject(u, "Workloads")
		assert.Equal(t, tc.want, o.State)
	}
	u := fluxFixture("ns", "app")
	require.NoError(t, unstructured.SetNestedMap(u.Object, map[string]any{"secretRef": map[string]any{"name": "PRIVATE"}}, "spec", "kubeConfig"))
	o := s.fluxObject(u, "Workloads")
	assert.False(t, o.LocalDestination)
}
func TestFluxReadsOnlyDiscoveredKindsAndClassifiesDenied(t *testing.T) {
	s, c := fluxSession(t, fluxFixture("ns", "app"))
	c.PrependReactor("list", "kustomizations", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "kustomize.toolkit.fluxcd.io", Resource: "kustomizations"}, "PRIVATE", assert.AnError)
	})
	out, err := s.FluxCD(t.Context(), core.ScopeSel{Mode: core.ScopeOne, Name: "ns"})
	require.NoError(t, err)
	assert.Empty(t, out.Objects)
	found := false
	for _, coverage := range out.Coverage {
		if coverage.Kind == "Kustomization" {
			found = true
			assert.Equal(t, "forbidden", coverage.State)
		}
	}
	assert.True(t, found)
	raw, _ := json.Marshal(out)
	assert.NotContains(t, string(raw), "PRIVATE")
	snap := *s.cat.snap()
	snap.reg = newKindRegistry()
	s.cat.cur.Store(&snap)
	c.ClearActions()
	out, err = s.FluxCD(t.Context(), core.ScopeSel{Mode: core.ScopeAll})
	require.NoError(t, err)
	assert.Empty(t, c.Actions())
	assert.Empty(t, out.Coverage)
}
func TestFluxInventoryAndReferenceBounds(t *testing.T) {
	s, _ := fluxSession(t)
	u := fluxFixture("ns", "app")
	entries := make([]any, 501)
	for i := range entries {
		entries[i] = map[string]any{"id": "ns_api_apps_Deployment", "v": "v1"}
	}
	require.NoError(t, unstructured.SetNestedSlice(u.Object, entries, "status", "inventory", "entries"))
	o := s.fluxObject(u, "Workloads")
	assert.True(t, o.Truncated)
	assert.Len(t, o.Inventory, 500)
}

func TestFluxTruncatedConditionsAndMalformedInventoryStayUnknown(t *testing.T) {
	s, _ := fluxSession(t)
	u := fluxFixture("ns", "app")
	conditions := make([]any, 33)
	for i := range conditions {
		conditions[i] = map[string]any{"type": "Ready", "status": "True", "observedGeneration": int64(2)}
	}
	require.NoError(t, unstructured.SetNestedSlice(u.Object, conditions, "status", "conditions"))
	o := s.fluxObject(u, "Workloads")
	assert.Equal(t, "Unknown", o.State)
	assert.True(t, o.Truncated)
	u = fluxFixture("ns", "app")
	require.NoError(t, unstructured.SetNestedSlice(u.Object, []any{map[string]any{"id": "invalid", "v": "v1"}}, "status", "inventory", "entries"))
	o = s.fluxObject(u, "Workloads")
	assert.True(t, o.Truncated)
	assert.Empty(t, o.Inventory)
}
func TestFluxTrackingAndEditWarningAreHintsAndOwnerUIDIsPreserved(t *testing.T) {
	u := mk("apps/v1", "Deployment", "blue", "web", "uid-web", nil)
	assert.Nil(t, fluxTracking(u))
	u.SetLabels(map[string]string{"kustomize.toolkit.fluxcd.io/name": "delivery", "kustomize.toolkit.fluxcd.io/namespace": "ops"})
	h := fluxTracking(u)
	require.NotNil(t, h)
	require.Len(t, h.Candidates, 1)
	assert.Empty(t, h.Candidates[0].UID)
	assert.Equal(t, "apps", h.Group)
	assert.Equal(t, "Deployment", h.Kind)
	assert.Equal(t, "Flux tracking metadata", managedBy(u))
	u.SetLabels(map[string]string{"kustomize.toolkit.fluxcd.io/name": "invalid/name", "kustomize.toolkit.fluxcd.io/namespace": "ops"})
	assert.Nil(t, fluxTracking(u))
}
func TestFluxRepeatedPaginationAndCancellationAreBounded(t *testing.T) {
	s, c := fluxSession(t)
	calls := 0
	c.PrependReactor("list", "kustomizations", func(k8stesting.Action) (bool, runtime.Object, error) {
		calls++
		list := &unstructured.UnstructuredList{}
		list.SetContinue("same")
		return true, list, nil
	})
	out, err := s.FluxCD(t.Context(), core.ScopeSel{Mode: core.ScopeOne, Name: "ns"})
	require.NoError(t, err)
	assert.True(t, out.Truncated)
	assert.LessOrEqual(t, calls, 3)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	c.ClearActions()
	_, err = s.FluxCD(ctx, core.ScopeSel{Mode: core.ScopeAll})
	require.Error(t, err)
	assert.Empty(t, c.Actions())
}

func TestFluxDoesNotProjectHelmValuesNotificationEndpointsOrImageCredentials(t *testing.T) {
	objects := []runtime.Object{}
	for _, k := range core.FluxResourceKinds {
		version := "v1"
		if k.Kind == "HelmRelease" {
			version = "v2"
		}
		u := mk(k.Group+"/"+version, k.Kind, "ns", strings.ToLower(k.Kind), "uid-"+k.Kind, map[string]any{"spec": map[string]any{"values": map[string]any{"password": "PRIVATE-VALUES"}, "address": "https://user:PRIVATE-ENDPOINT@notify.invalid/PRIVATE-WEBHOOK", "secretRef": map[string]any{"name": "PRIVATE-CREDENTIAL"}, "certSecretRef": map[string]any{"name": "PRIVATE-CERT"}, "decryption": map[string]any{"secretRef": map[string]any{"name": "PRIVATE-DECRYPT"}}, "postBuild": map[string]any{"substitute": map[string]any{"TOKEN": "PRIVATE-SUBSTITUTE"}}}})
		objects = append(objects, u)
	}
	s, c := fluxSession(t, objects...)
	out, err := s.FluxCD(t.Context(), core.ScopeSel{Mode: core.ScopeOne, Name: "ns"})
	require.NoError(t, err)
	require.Len(t, out.Objects, 14)
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "PRIVATE")
	for _, action := range c.Actions() {
		assert.Equal(t, "list", action.GetVerb())
		assert.NotEqual(t, "secrets", action.GetResource().Resource)
	}
}
