package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
)

func networkObjects() (*unstructured.Unstructured, *unstructured.Unstructured, *unstructured.Unstructured) {
	svc := mk("v1", "Service", "blue", "web", "svc-uid", map[string]any{"spec": map[string]any{"selector": map[string]any{"app": "web"}, "ports": []any{map[string]any{"name": "http", "port": int64(80), "targetPort": "http"}}}})
	pod := mk("v1", "Pod", "blue", "web-1", "pod-uid", map[string]any{"metadata": map[string]any{"labels": map[string]any{"app": "web"}, "ownerReferences": []any{map[string]any{"apiVersion": "apps/v1", "kind": "ReplicaSet", "name": "web-rs", "uid": "rs-uid"}}}, "spec": map[string]any{"containers": []any{map[string]any{"name": "app", "env": []any{map[string]any{"name": "SECRET", "value": "PRIVATE-ENV"}}, "ports": []any{map[string]any{"name": "http", "containerPort": int64(8080)}}}}}})
	sl := mk("discovery.k8s.io/v1", "EndpointSlice", "blue", "web-a", "slice-uid", map[string]any{"metadata": map[string]any{"labels": map[string]any{"kubernetes.io/service-name": "web"}, "ownerReferences": []any{map[string]any{"apiVersion": "v1", "kind": "Service", "name": "web", "uid": "svc-uid"}}}, "addressType": "IPv4", "ports": []any{map[string]any{"name": "http", "port": int64(8080)}}, "endpoints": []any{map[string]any{"addresses": []any{"10.0.0.1"}, "conditions": map[string]any{"ready": true, "serving": true, "terminating": false}, "targetRef": map[string]any{"apiVersion": "v1", "kind": "Pod", "namespace": "blue", "name": "web-1", "uid": "pod-uid"}}}})
	return svc, pod, sl
}
func networkFake(objs ...runtime.Object) *fake.FakeDynamicClient {
	return fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{servicesKind.gvr: "ServiceList", ingressesKind.gvr: "IngressList", podsKind.gvr: "PodList", networkSlices: "EndpointSliceList"}, objs...)
}
func networkRef() core.Ref {
	return core.Ref{Provider: ProviderID, Target: "t", Kind: "services", Scope: "blue", Name: "web", UID: "svc-uid"}
}
func networkText(t *testing.T, out core.NetworkPath) string {
	t.Helper()
	b, err := json.Marshal(out)
	require.NoError(t, err)
	return string(b)
}
func TestNetworkPathDeclaredPortsUIDsAndReadOnlyProjection(t *testing.T) {
	svc, pod, sl := networkObjects()
	client := networkFake(svc, pod, sl)
	s := newSession("t", "h", client, false)
	defer s.Close()
	out, err := s.ResourceNetworkPath(t.Context(), networkRef())
	require.NoError(t, err)
	require.Len(t, out.Routes, 1)
	route := out.Routes[0]
	require.Empty(t, route.Checks)
	require.Equal(t, "http", route.TargetPort)
	require.EqualValues(t, 80, route.Port)
	require.Len(t, route.Endpoints, 1)
	e := route.Endpoints[0]
	require.Equal(t, "pod-uid", e.Pod.UID)
	require.Equal(t, "slice-uid", e.Slice.UID)
	require.Equal(t, "rs-uid", e.Owners[0].UID)
	require.EqualValues(t, 8080, e.Port)
	require.Empty(t, e.Checks)
	require.NotContains(t, networkText(t, out), "PRIVATE-ENV")
	for _, a := range client.Actions() {
		require.Equal(t, "blue", a.GetNamespace())
		require.Contains(t, []string{"get", "list"}, a.GetVerb())
		require.Contains(t, []string{"services", "pods", "endpointslices"}, a.GetResource().Resource)
	}
}
func TestNetworkPathIndependentIngressPathsAndMissingBackends(t *testing.T) {
	svc, pod, sl := networkObjects()
	path := func(name string, port any, p string) any {
		return map[string]any{"path": p, "pathType": "Prefix", "backend": map[string]any{"service": map[string]any{"name": name, "port": port}}}
	}
	ing := mk("networking.k8s.io/v1", "Ingress", "blue", "entry", "ing-uid", map[string]any{"spec": map[string]any{"defaultBackend": map[string]any{"resource": map[string]any{"apiGroup": "example.org", "kind": "StorageBucket", "name": "assets"}}, "rules": []any{map[string]any{"host": "web.example", "http": map[string]any{"paths": []any{path("web", map[string]any{"name": "http"}, "/good"), path("web", map[string]any{"number": int64(81)}, "/port"), path("gone", map[string]any{"number": int64(80)}, "/gone")}}}}}})
	s := newSession("t", "h", networkFake(svc, pod, sl, ing), false)
	defer s.Close()
	ref := networkRef()
	ref.Kind = ingressesKind.desc.ID
	ref.Name = "entry"
	ref.UID = "ing-uid"
	out, err := s.ResourceNetworkPath(t.Context(), ref)
	require.NoError(t, err)
	require.Len(t, out.Routes, 4)
	require.Contains(t, out.Coverage, core.NetworkCoverage{Source: "services/gone", State: "absent"})
	require.True(t, out.Routes[0].Route.DefaultBackend)
	require.Equal(t, "backend_unsupported", out.Routes[0].Checks[0].Code)
	require.Equal(t, "/good", out.Routes[1].Route.Path)
	require.Empty(t, out.Routes[1].Checks)
	require.Equal(t, "service_port_missing", out.Routes[2].Checks[0].Code)
	require.Equal(t, "backend_missing", out.Routes[3].Checks[0].Code)
}
func TestNetworkPathSelectorNamedPortsAndEndpointReadiness(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		change     func(*unstructured.Unstructured, *unstructured.Unstructured, *unstructured.Unstructured)
	}{
		{"selector empty", "selector_empty", func(s, _, _ *unstructured.Unstructured) {
			s.Object["spec"].(map[string]any)["selector"] = map[string]any{"app": "other"}
		}},
		{"named port", "named_port_missing", func(_, p, _ *unstructured.Unstructured) { slice(p.Object, "spec", "containers")[0]["ports"] = []any{} }},
		{"port mismatch", "target_port_mismatch", func(_, _, e *unstructured.Unstructured) { slice(e.Object, "ports")[0]["port"] = int64(9000) }},
		{"not ready", "endpoint_not_ready", func(_, _, e *unstructured.Unstructured) {
			slice(e.Object, "endpoints")[0]["conditions"] = map[string]any{"ready": false, "serving": true, "terminating": true}
		}},
		{"nil ready", "readiness_unknown", func(_, _, e *unstructured.Unstructured) {
			slice(e.Object, "endpoints")[0]["conditions"] = map[string]any{}
		}},
		{"replacement pod", "pod_unverified", func(_, p, _ *unstructured.Unstructured) { p.SetUID("replacement") }},
		{"publish not ready", "publish_not_ready", func(s, _, _ *unstructured.Unstructured) {
			s.Object["spec"].(map[string]any)["publishNotReadyAddresses"] = true
		}},
		{"old slice owner", "slice_owner_mismatch", func(_, _, e *unstructured.Unstructured) {
			e.Object["metadata"].(map[string]any)["ownerReferences"] = []any{map[string]any{"apiVersion": "v1", "kind": "Service", "name": "web", "uid": "old"}}
		}},
		{"UDP", "protocol_unsupported", func(s, _, _ *unstructured.Unstructured) { slice(s.Object, "spec", "ports")[0]["protocol"] = "UDP" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, pod, sl := networkObjects()
			tc.change(svc, pod, sl)
			s := newSession("t", "h", networkFake(svc, pod, sl), false)
			defer s.Close()
			out, err := s.ResourceNetworkPath(t.Context(), networkRef())
			require.NoError(t, err)
			require.Contains(t, networkText(t, out), tc.code)
			if tc.name == "replacement pod" {
				require.Nil(t, out.Routes[0].Endpoints[0].Pod)
			}
		})
	}
}
func TestNetworkPathSelectorlessHeadlessExternalAndNumericDefault(t *testing.T) {
	for _, mode := range []string{"selectorless", "headless", "externalName", "numeric"} {
		t.Run(mode, func(t *testing.T) {
			svc, pod, sl := networkObjects()
			spec := svc.Object["spec"].(map[string]any)
			delete(spec, "selector")
			delete(slice(sl.Object, "endpoints")[0], "targetRef")
			switch mode {
			case "headless":
				spec["clusterIP"] = "None"
			case "externalName":
				spec["type"] = "ExternalName"
				spec["externalName"] = "db.example"
			case "numeric":
				delete(slice(svc.Object, "spec", "ports")[0], "targetPort")
				slice(sl.Object, "ports")[0]["port"] = int64(80)
			}
			s := newSession("t", "h", networkFake(svc, pod, sl), false)
			defer s.Close()
			out, err := s.ResourceNetworkPath(t.Context(), networkRef())
			require.NoError(t, err)
			text := networkText(t, out)
			require.NotContains(t, text, "selector_empty")
			if mode == "numeric" {
				require.Equal(t, "80", out.Routes[0].TargetPort)
			} else {
				require.Equal(t, mode, out.Routes[0].Mode)
			}
			if mode == "externalName" {
				require.Contains(t, text, "db.example")
				require.Empty(t, out.Routes[0].Endpoints)
			} else {
				require.Len(t, out.Routes[0].Endpoints, 1)
			}
		})
	}
}
func TestNetworkPathDeniedPartialPaginationAndBoundedIdentity(t *testing.T) {
	for _, failure := range []string{"denied", "repeated", "wrong namespace", "too many", "oversize"} {
		t.Run(failure, func(t *testing.T) {
			svc, _, _ := networkObjects()
			client := networkFake(svc)
			client.PrependReactor("list", "pods", func(_ ktesting.Action) (bool, runtime.Object, error) {
				if failure == "denied" {
					return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", fmt.Errorf("PRIVATE-SERVER"))
				}
				l := &unstructured.UnstructuredList{}
				if failure == "repeated" {
					l.SetContinue("same")
				}
				count := 1
				if failure == "too many" {
					count = 130
				}
				for i := range count {
					_, p, _ := networkObjects()
					p.SetName(fmt.Sprint("p", i))
					if failure == "wrong namespace" {
						p.SetNamespace("outside")
					}
					if failure == "oversize" {
						p.Object["private"] = strings.Repeat("x", 1<<20)
					}
					l.Items = append(l.Items, *p)
				}
				return true, l, nil
			})
			s := newSession("t", "h", client, false)
			defer s.Close()
			out, err := s.ResourceNetworkPath(t.Context(), networkRef())
			require.NoError(t, err)
			text := networkText(t, out)
			require.NotContains(t, text, "selector_empty")
			require.NotContains(t, text, fmt.Errorf("PRIVATE-SERVER"))
			require.NotContains(t, text, "outside")
			require.NotEqual(t, "ready", out.Coverage[0].State)
			if failure == "too many" || failure == "oversize" || failure == "repeated" {
				require.True(t, out.Truncated)
			}
		})
	}
}
func TestNetworkPathSubjectAndBackendChangeCancellation(t *testing.T) {
	svc, _, _ := networkObjects()
	client := networkFake(svc)
	s := newSession("t", "h", client, false)
	defer s.Close()
	calls := 0
	client.PrependReactor("get", "services", func(_ ktesting.Action) (bool, runtime.Object, error) {
		calls++
		u := svc.DeepCopy()
		if calls > 1 {
			u.SetUID("replacement")
		}
		return true, u, nil
	})
	_, err := s.ResourceNetworkPath(t.Context(), networkRef())
	require.Error(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = s.ResourceNetworkPath(ctx, networkRef())
	require.ErrorIs(t, err, context.Canceled)
	for _, ref := range []core.Ref{{Provider: ProviderID, Target: "other", Scope: "blue", Kind: "services", Name: "web", UID: "svc-uid"}, {Provider: ProviderID, Target: "t", Scope: "blue", Kind: "pods", Name: "web", UID: "svc-uid"}} {
		_, err = s.ResourceNetworkPath(t.Context(), ref)
		require.Error(t, err)
	}
}

func TestNetworkPathHTTPPaginationAndNoProbeOrWrite(t *testing.T) {
	svc, pod, sl := networkObjects()
	var requests []string
	pages := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		require.Equal(t, http.MethodGet, req.Method)
		requests = append(requests, req.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		var value any
		switch req.URL.Path {
		case "/api/v1/namespaces/blue/services/web":
			value = svc.Object
		case "/api/v1/namespaces/blue/pods":
			pages++
			require.Equal(t, "32", req.URL.Query().Get("limit"))
			if pages == 1 {
				value = map[string]any{"apiVersion": "v1", "kind": "PodList", "metadata": map[string]any{"continue": "page-two"}, "items": []any{}}
			} else {
				require.Equal(t, "page-two", req.URL.Query().Get("continue"))
				value = map[string]any{"apiVersion": "v1", "kind": "PodList", "metadata": map[string]any{}, "items": []any{pod.Object}}
			}
		case "/apis/discovery.k8s.io/v1/namespaces/blue/endpointslices":
			value = map[string]any{"apiVersion": "discovery.k8s.io/v1", "kind": "EndpointSliceList", "metadata": map[string]any{}, "items": []any{sl.Object}}
		default:
			t.Errorf("Unexpected request %s", req.URL)
			http.Error(w, "unexpected", 500)
			return
		}
		require.NoError(t, json.NewEncoder(w).Encode(value))
	}))
	defer server.Close()
	client, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
	require.NoError(t, err)
	s := newSession("t", "h", client, false)
	defer s.Close()
	out, err := s.ResourceNetworkPath(t.Context(), networkRef())
	require.NoError(t, err)
	require.Len(t, requests, 5)
	require.Equal(t, "ready", out.Coverage[0].State)
	require.Equal(t, "pod-uid", out.Routes[0].Endpoints[0].Pod.UID)
	require.NotContains(t, networkText(t, out), "PRIVATE-ENV")
}
func TestNetworkPathOutputBudgetsPreserveUIDs(t *testing.T) {
	svc, pod, sl := networkObjects()
	ports := []any{}
	for i := range 40 {
		ports = append(ports, map[string]any{"name": "http", "port": int64(80 + i), "targetPort": "http"})
	}
	svc.Object["spec"].(map[string]any)["ports"] = ports
	pod.SetLabels(map[string]string{"app": "web", "long": strings.Repeat("x", 4096)})
	svc.Object["spec"].(map[string]any)["selector"] = map[string]any{"app": "web", "long": strings.Repeat("x", 4096)}
	s := newSession("t", "h", networkFake(svc, pod, sl), false)
	defer s.Close()
	out, err := s.ResourceNetworkPath(t.Context(), networkRef())
	require.NoError(t, err)
	require.Len(t, out.Routes, 32)
	require.True(t, out.Truncated)
	require.LessOrEqual(t, len(out.Routes[0].Selector), 2051)
	require.Equal(t, "pod-uid", out.Routes[0].Endpoints[0].Pod.UID)
	require.Equal(t, "svc-uid", out.Routes[0].Service.UID)
}

func TestNetworkPathControllerTargetDefaultsAndPortlessHeadless(t *testing.T) {
	svc, pod, sl := networkObjects()
	delete(slice(sl.Object, "endpoints")[0]["targetRef"].(map[string]any), "apiVersion")
	svc.Object["spec"].(map[string]any)["clusterIP"] = "None"
	svc.Object["spec"].(map[string]any)["ports"] = []any{}
	sl.Object["ports"] = []any{}
	s := newSession("t", "h", networkFake(svc, pod, sl), false)
	defer s.Close()
	out, err := s.ResourceNetworkPath(t.Context(), networkRef())
	require.NoError(t, err)
	require.Len(t, out.Routes, 1)
	require.Equal(t, "headless", out.Routes[0].Mode)
	require.Empty(t, out.Routes[0].Checks)
	require.Empty(t, out.Routes[0].TargetPort)
	require.Len(t, out.Routes[0].Endpoints, 1)
	require.EqualValues(t, 0, out.Routes[0].Endpoints[0].Port)
	require.Equal(t, "pod-uid", out.Routes[0].Endpoints[0].Pod.UID)
	require.Empty(t, out.Routes[0].Endpoints[0].Checks)
}

func TestNetworkPathVerificationBudgetIsLimitNotReplacement(t *testing.T) {
	svc, pod, sl := networkObjects()
	svc.Object["unprojected"] = strings.Repeat("x", 900000)
	pod.Object["unprojected"] = strings.Repeat("y", 900000)
	second, third := pod.DeepCopy(), pod.DeepCopy()
	second.SetName("web-2")
	second.SetUID("pod2")
	third.SetName("web-3")
	third.SetUID("pod3")
	s := newSession("t", "h", networkFake(svc, pod, second, third, sl), false)
	defer s.Close()
	_, err := s.ResourceNetworkPath(t.Context(), networkRef())
	var e *provider.Error
	require.ErrorAs(t, err, &e)
	require.Equal(t, provider.ClassScopeLimit, e.Class)
	require.NotContains(t, e.Message, "changed")
}

func TestNetworkPathBackendVerificationFailureIsNotAConflict(t *testing.T) {
	for _, mode := range []string{"denied", "malformed", "changed"} {
		t.Run(mode, func(t *testing.T) {
			svc, pod, sl := networkObjects()
			ing := mk("networking.k8s.io/v1", "Ingress", "blue", "entry", "ing-uid", map[string]any{"spec": map[string]any{"defaultBackend": map[string]any{"service": map[string]any{"name": "web", "port": map[string]any{"number": int64(80)}}}}})
			client := networkFake(svc, pod, sl, ing)
			reads := 0
			client.PrependReactor("get", "services", func(_ ktesting.Action) (bool, runtime.Object, error) {
				reads++
				u := svc.DeepCopy()
				if reads > 1 {
					switch mode {
					case "denied":
						return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "services"}, "web", fmt.Errorf("PRIVATE-SERVER"))
					case "malformed":
						u.SetAPIVersion("foreign/v1")
					case "changed":
						u.SetResourceVersion("new")
					}
				}
				return true, u, nil
			})
			s := newSession("t", "h", client, false)
			defer s.Close()
			ref := networkRef()
			ref.Kind = ingressesKind.desc.ID
			ref.Name = "entry"
			ref.UID = "ing-uid"
			_, err := s.ResourceNetworkPath(t.Context(), ref)
			var e *provider.Error
			require.ErrorAs(t, err, &e)
			expected := map[string]provider.ErrorClass{"denied": provider.ClassForbidden, "malformed": provider.ClassUnavailable, "changed": provider.ClassConflict}
			require.Equal(t, expected[mode], e.Class)
			require.NotContains(t, e.Message, "PRIVATE-SERVER")
			if mode != "changed" {
				require.NotContains(t, e.Message, "changed")
			}
		})
	}
}
