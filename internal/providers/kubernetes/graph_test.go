package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/rest"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestGraphScopeUIDReferencesAndNoValues(t *testing.T) {
	pod := mk("v1", "Pod", "blue", "api-0", "p", map[string]any{
		"metadata": map[string]any{"labels": map[string]any{"app": "api"}, "ownerReferences": []any{map[string]any{"apiVersion": "apps/v1", "kind": "ReplicaSet", "name": "api", "uid": "owner"}}},
		"spec":     map[string]any{"nodeName": "node-1", "containers": []any{map[string]any{"name": "main", "env": []any{map[string]any{"value": "PRIVATE-ENV-VALUE", "name": "private"}}, "envFrom": []any{map[string]any{"secretRef": map[string]any{"name": "credentials"}}}}}},
	})
	owner := mk("apps/v1", "ReplicaSet", "blue", "api", "owner", nil)
	service := mk("v1", "Service", "blue", "api", "svc", map[string]any{"spec": map[string]any{"selector": map[string]any{"app": "api"}}})
	ingress := mk("networking.k8s.io/v1", "Ingress", "blue", "edge", "ing", map[string]any{"spec": map[string]any{"defaultBackend": map[string]any{"service": map[string]any{"name": "api"}}}})
	secret := mk("v1", "Secret", "blue", "credentials", "secret", map[string]any{"data": map[string]any{"password": "PRIVATE-SECRET-VALUE"}})
	other := mk("v1", "Pod", "green", "api-0", "other", map[string]any{"metadata": map[string]any{"labels": map[string]any{"app": "api"}}})
	node := mk("v1", "Node", "", "node-1", "node", nil)
	s := newSession("t", "h", fullFake(pod, owner, service, ingress, secret, other, node), false)
	defer s.Close()
	g, err := s.Graph(context.Background(), core.ScopeSel{Mode: core.ScopeSome, Names: []string{"blue"}})
	require.NoError(t, err)
	assert.Len(t, g.Nodes, 6)
	assert.Contains(t, g.Edges, core.GraphEdge{Source: "owner", Target: "p", Type: "owns"})
	assert.Contains(t, g.Edges, core.GraphEdge{Source: "svc", Target: "p", Type: "selects"})
	assert.Contains(t, g.Edges, core.GraphEdge{Source: "ing", Target: "svc", Type: "routes-to"})
	assert.Contains(t, g.Edges, core.GraphEdge{Source: "p", Target: "secret", Type: "uses"})
	assert.Contains(t, g.Edges, core.GraphEdge{Source: "p", Target: "node", Type: "runs-on"})
	b, err := json.Marshal(g)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "PRIVATE-")
	assert.NotContains(t, string(b), "other")
	// Empty selection must not broaden to all namespaces.
	empty, err := s.Graph(context.Background(), core.ScopeSel{Mode: core.ScopeSome, Names: []string{}})
	require.NoError(t, err)
	require.Len(t, empty.Nodes, 1)
	assert.Equal(t, "node", empty.Nodes[0].ID)
}
func TestGraphDeniedSourceIsPartialNotEmptySuccess(t *testing.T) {
	client := fullFake(mk("v1", "Pod", "blue", "p", "p", nil))
	client.PrependReactor("list", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "", fmt.Errorf("denied"))
	})
	s := newSession("t", "h", client, false)
	defer s.Close()
	g, err := s.Graph(context.Background(), core.ScopeSel{Mode: core.ScopeOne, Name: "blue"})
	require.NoError(t, err)
	require.NotEmpty(t, g.Nodes)
	assert.Contains(t, g.Problems, core.GraphProblem{Kind: "secrets", Scope: "blue", Class: "forbidden"})
	for _, a := range client.Actions() {
		if a.GetResource().Resource == "pods" {
			assert.Equal(t, "blue", a.GetNamespace())
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.Graph(ctx, core.ScopeSel{Mode: core.ScopeAll})
	require.ErrorIs(t, err, context.Canceled)
}
func TestGraphRejectsSameNameOwnerReplacementAndSelectorMismatch(t *testing.T) {
	records := []graphRecord{
		{node: core.GraphNode{ID: "new-owner", Ref: core.Ref{Kind: "custom/owners", Name: "owner", Scope: "blue"}}},
		{node: core.GraphNode{ID: "p", Ref: core.Ref{Kind: "pods", Name: "p", Scope: "blue"}}, owners: []metav1.OwnerReference{{Name: "owner", UID: "old-owner"}}, labels: map[string]string{"app": "api", "tier": "db"}},
		{node: core.GraphNode{ID: "svc", Ref: core.Ref{Kind: "services", Name: "s", Scope: "blue"}}, selector: map[string]string{"app": "api", "tier": "web"}},
	}
	g := core.Graph{}
	buildGraph(&g, records)
	assert.Empty(t, g.Edges)
}
func BenchmarkGraphIndexed(b *testing.B) {
	records := make([]graphRecord, 0, 20000)
	for i := range 10000 {
		records = append(records, graphRecord{node: core.GraphNode{ID: fmt.Sprint("p", i), Ref: core.Ref{Kind: "pods", Name: fmt.Sprint(i), Scope: "blue"}}, labels: map[string]string{"app": fmt.Sprint(i)}})
		records = append(records, graphRecord{node: core.GraphNode{ID: fmt.Sprint("s", i), Ref: core.Ref{Kind: "services", Name: fmt.Sprint(i), Scope: "blue"}}, selector: map[string]string{"app": fmt.Sprint(i)}})
	}
	b.ResetTimer()
	for b.Loop() {
		g := core.Graph{}
		buildGraph(&g, records)
		if len(g.Edges) != 10000 {
			b.Fatal(len(g.Edges))
		}
	}
}

func TestGraphPaginationAndDiscoveredReferences(t *testing.T) {
	pvcDef := &kindDef{desc: core.KindDescriptor{ID: "persistentvolumeclaims", Title: "PersistentVolumeClaims", Singular: "PersistentVolumeClaim"}, gvr: schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}, namespaced: true, discovered: true, kind: "PersistentVolumeClaim"}
	pvDef := &kindDef{desc: core.KindDescriptor{ID: "persistentvolumes", Title: "PersistentVolumes", Singular: "PersistentVolume"}, gvr: schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumes"}, discovered: true, kind: "PersistentVolume"}
	defs := append(append([]*kindDef{}, allKinds.list...), pvcDef, pvDef)
	lists := map[schema.GroupVersionResource]string{}
	for _, d := range defs {
		if !d.virtual {
			kind := kindOf(d)
			if d.discovered {
				kind = d.kind
			}
			lists[d.gvr] = kind + "List"
		}
	}
	pvc := mk("v1", "PersistentVolumeClaim", "blue", "disk", "pvc", map[string]any{"spec": map[string]any{"volumeName": "disk"}})
	pv := mk("v1", "PersistentVolume", "", "disk", "pv", nil)
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), lists, pvc, pv)
	page := 0
	client.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		page++
		item := mk("v1", "Pod", "blue", fmt.Sprint("p", page), fmt.Sprint("p", page), nil)
		list := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*item}}
		if page == 1 {
			list.SetContinue("next")
		}
		return true, list, nil
	})
	s := newSession("t", "h", client, false)
	defer s.Close()
	s.cat = newCatalog(s.ctx, newKindRegistry(defs...), nil)
	g, err := s.Graph(context.Background(), core.ScopeSel{Mode: core.ScopeOne, Name: "blue"})
	require.NoError(t, err)
	require.Equal(t, 2, page)
	assert.Contains(t, g.Edges, core.GraphEdge{Source: "pvc", Target: "pv", Type: "bound-to"})
	assert.False(t, g.Truncated)
}

func TestGatewayDeclaredRouteDirection(t *testing.T) {
	gateway := &kindDef{desc: core.KindDescriptor{ID: "gateway.networking.k8s.io/gateways", Singular: "Gateway"}, gvr: schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"}, namespaced: true, discovered: true, kind: "Gateway"}
	route := &kindDef{desc: core.KindDescriptor{ID: "gateway.networking.k8s.io/httproutes", Singular: "HTTPRoute"}, gvr: schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"}, namespaced: true, discovered: true, kind: "HTTPRoute"}
	s := newSession("t", "h", fullFake(), false)
	defer s.Close()
	s.cat = newCatalog(s.ctx, newKindRegistry(gateway, route, servicesKind), nil)
	obj := mk("gateway.networking.k8s.io/v1", "HTTPRoute", "blue", "route", "route", map[string]any{"spec": map[string]any{"parentRefs": []any{map[string]any{"name": "gateway"}}, "rules": []any{map[string]any{"backendRefs": []any{map[string]any{"name": "api"}}}}}})
	data := []graphRecord{
		{node: core.GraphNode{ID: "gateway", Ref: core.Ref{Kind: gateway.desc.ID, Name: "gateway", Scope: "blue"}}},
		{node: core.GraphNode{ID: "route", Ref: core.Ref{Kind: route.desc.ID, Name: "route", Scope: "blue"}}, links: s.graphLinks(route, obj)},
		{node: core.GraphNode{ID: "service", Ref: core.Ref{Kind: "services", Name: "api", Scope: "blue"}}},
	}
	graph := core.Graph{}
	buildGraph(&graph, data)
	assert.Contains(t, graph.Edges, core.GraphEdge{Source: "gateway", Target: "route", Type: "routes-to"})
	assert.Contains(t, graph.Edges, core.GraphEdge{Source: "route", Target: "service", Type: "routes-to"})
	assert.NotContains(t, graph.Edges, core.GraphEdge{Source: "route", Target: "gateway", Type: "routes-to"})
	invalid := mk("gateway.networking.k8s.io/v1", "HTTPRoute", "blue", "route", "route", map[string]any{"spec": map[string]any{"parentRefs": []any{map[string]any{"name": "gateway", "group": "example.test"}}, "rules": []any{map[string]any{"backendRefs": []any{map[string]any{"name": "api", "kind": "Service", "group": "example.test"}}}}}})
	data[1].links = s.graphLinks(route, invalid)
	empty := core.Graph{}
	buildGraph(&empty, data)
	assert.Empty(t, empty.Edges)

}

func TestGraphUsesMetadataOnlyForSecretsAndConfigMaps(t *testing.T) {
	var mu sync.Mutex
	paths := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.Header.Get("Accept"), "PartialObjectMetadata")
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/secrets") {
			_, _ = w.Write([]byte(`{"apiVersion":"meta.k8s.io/v1","kind":"PartialObjectMetadataList","items":[{"apiVersion":"meta.k8s.io/v1","kind":"PartialObjectMetadata","metadata":{"name":"credentials","namespace":"blue","uid":"secret"}}]}`))
		} else {
			_, _ = w.Write([]byte(`{"apiVersion":"meta.k8s.io/v1","kind":"PartialObjectMetadataList","items":[]}`))
		}
	}))
	defer server.Close()
	client := fullFake()
	for _, resource := range []string{"secrets", "configmaps"} {
		client.PrependReactor("list", resource, func(k8stesting.Action) (bool, runtime.Object, error) {
			t.Error("graph requested full Secret/ConfigMap objects")
			return true, nil, fmt.Errorf("unexpected raw list")
		})
	}
	meta, err := metadata.NewForConfig(&rest.Config{Host: server.URL})
	require.NoError(t, err)
	s := newSession("t", "h", client, false)
	defer s.Close()
	s.graphMetadata = meta
	graph, err := s.Graph(context.Background(), core.ScopeSel{Mode: core.ScopeOne, Name: "blue"})
	require.NoError(t, err)
	require.Empty(t, graph.Problems)
	require.Len(t, graph.Nodes, 1)
	assert.Equal(t, "secret", graph.Nodes[0].ID)
	assert.ElementsMatch(t, []string{"/api/v1/namespaces/blue/secrets", "/api/v1/namespaces/blue/configmaps"}, paths)
}
