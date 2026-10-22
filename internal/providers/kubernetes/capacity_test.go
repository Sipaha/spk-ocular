package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

func capacityNode() *unstructured.Unstructured {
	return mk("v1", "Node", "", "worker", "node-uid", map[string]any{"metadata": map[string]any{"name": "worker", "uid": "node-uid", "resourceVersion": "7"}, "status": map[string]any{"allocatable": map[string]any{"cpu": "4", "memory": "8Gi"}}})
}
func capacityPod() *unstructured.Unstructured {
	u := mk("v1", "Pod", "blue", "web", "pod-uid", map[string]any{"spec": map[string]any{"nodeName": "worker", "containers": []any{map[string]any{"name": "app", "env": []any{map[string]any{"value": "PRIVATE-ENV"}}, "resources": map[string]any{"requests": map[string]any{"cpu": "100m", "memory": "64Mi"}}}}, "initContainers": []any{map[string]any{"name": "init", "resources": map[string]any{"requests": map[string]any{"cpu": "500m", "memory": "128Mi"}}}}, "overhead": map[string]any{"cpu": "10m", "memory": "8Mi"}}, "status": map[string]any{"phase": "Running"}})
	u.SetResourceVersion("8")
	return u
}
func capacityRequest() core.NodeCapacityRequest {
	return core.NodeCapacityRequest{Ref: core.Ref{Provider: ProviderID, Target: "t", Kind: "nodes", Name: "worker", UID: "node-uid"}, Namespace: "blue"}
}
func TestCapacityProjectionSemanticsAndPrivacy(t *testing.T) {
	for _, name := range []string{"ordinary", "zero", "missing", "mixed-missing", "sidecar", "pod-level", "claim", "invalid", "large-exponent", "long-quantity"} {
		t.Run(name, func(t *testing.T) {
			u := capacityPod()
			spec := u.Object["spec"].(map[string]any)
			app := spec["containers"].([]any)[0].(map[string]any)
			requests := app["resources"].(map[string]any)["requests"].(map[string]any)
			switch name {
			case "zero":
				delete(spec, "initContainers")
				delete(spec, "overhead")
				requests["cpu"] = "0"
			case "missing":
				delete(spec, "initContainers")
				delete(spec, "overhead")
				delete(requests, "cpu")
			case "mixed-missing":
				delete(requests, "cpu")
			case "sidecar":
				spec["initContainers"] = append([]any{map[string]any{"name": "sidecar", "restartPolicy": "Always", "resources": map[string]any{"requests": map[string]any{"cpu": "100m", "memory": "32Mi"}}}}, spec["initContainers"].([]any)...)
			case "pod-level":
				spec["resources"] = map[string]any{"requests": map[string]any{"cpu": "800m", "memory": "512Mi"}}
			case "claim":
				spec["resourceClaims"] = []any{map[string]any{"name": "device"}}
			case "invalid":
				requests["cpu"] = "-1"
			case "large-exponent":
				requests["cpu"] = "1e999999999"
			case "long-quantity":
				requests["cpu"] = strings.Repeat("9", 100000)
			}
			out := capacityPodResources(u)
			b, e := json.Marshal(out)
			require.NoError(t, e)
			require.NotContains(t, string(b), "PRIVATE")
			require.NotContains(t, string(b), "env")
			switch name {
			case "ordinary":
				require.Equal(t, "510m", out.CPU.Value)
				require.Equal(t, "136Mi", out.Memory.Value)
			case "zero":
				require.Equal(t, core.CapacityQuantity{State: "declared", Value: "0"}, out.CPU)
			case "missing":
				require.Equal(t, "unspecified", out.CPU.State)
			case "mixed-missing", "claim", "invalid", "large-exponent", "long-quantity":
				require.Equal(t, "unknown", out.CPU.State)
			case "sidecar":
				require.Equal(t, "610m", out.CPU.Value)
				require.Equal(t, "168Mi", out.Memory.Value)
			case "pod-level":
				require.Equal(t, "810m", out.CPU.Value)
				require.Equal(t, "520Mi", out.Memory.Value)
			}
		})
	}
}
func TestCapacityActualHTTPBoundsScopeIdentityAndCoverage(t *testing.T) {
	for _, mode := range []string{"ok", "denied", "terminal", "foreign", "ignored-selector", "changed-node", "replaced-node", "repeated-token", "changed-list-rv", "missing-list-rv", "oversized-page", "oversized-object", "missing-request"} {
		t.Run(mode, func(t *testing.T) {
			gets, lists := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				require.Equal(t, "GET", r.Method)
				var value any
				switch r.URL.Path {
				case "/api/v1/nodes/worker":
					gets++
					u := capacityNode()
					if gets > 1 && mode == "changed-node" {
						u.SetResourceVersion("9")
					}
					if gets > 1 && mode == "replaced-node" {
						u.SetUID("replacement")
					}
					value = u
				case "/api/v1/namespaces/blue/pods":
					lists++
					require.Equal(t, "spec.nodeName=worker", r.URL.Query().Get("fieldSelector"))
					require.Equal(t, "32", r.URL.Query().Get("limit"))
					if mode == "denied" {
						w.WriteHeader(http.StatusForbidden)
						value = metav1.Status{Status: "Failure", Code: 403, Reason: metav1.StatusReasonForbidden, Message: "PRIVATE-DIAGNOSTIC"}
						break
					}
					u := capacityPod()
					list := &unstructured.UnstructuredList{Object: map[string]any{"apiVersion": "v1", "kind": "PodList"}}
					list.SetResourceVersion("10")
					switch mode {
					case "terminal":
						_ = unstructured.SetNestedField(u.Object, "Succeeded", "status", "phase")
					case "foreign":
						u.SetNamespace("red")
					case "ignored-selector":
						_ = unstructured.SetNestedField(u.Object, "other", "spec", "nodeName")
					case "missing-list-rv":
						list.SetResourceVersion("")
					case "repeated-token":
						list.SetContinue("same")
						u.SetName(fmt.Sprint("pod", lists))
						u.SetUID(types.UID("uid" + fmt.Sprint(lists)))
					case "changed-list-rv":
						if lists == 1 {
							list.SetContinue("next")
						} else {
							list.SetResourceVersion("11")
							u.SetName("pod2")
							u.SetUID("uid2")
						}
					case "oversized-object":
						_ = unstructured.SetNestedField(u.Object, strings.Repeat("p", capacityObjectBytes), "metadata", "annotations", "x")
					case "missing-request":
						spec := u.Object["spec"].(map[string]any)
						delete(spec, "initContainers")
						delete(spec, "overhead")
						delete(spec["containers"].([]any)[0].(map[string]any)["resources"].(map[string]any)["requests"].(map[string]any), "cpu")
					}
					list.Items = []unstructured.Unstructured{*u}
					if mode == "oversized-page" {
						for range 32 {
							list.Items = append(list.Items, *u.DeepCopy())
						}
					}
					value = list
				default:
					t.Errorf("unexpected source %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				require.NoError(t, json.NewEncoder(w).Encode(value))
			}))
			defer server.Close()
			client, e := dynamic.NewForConfig(&rest.Config{Host: server.URL})
			require.NoError(t, e)
			s := newSession("t", "hash", client, false)
			defer s.Close()
			out, e := s.NodeCapacity(t.Context(), capacityRequest())
			if mode == "changed-node" || mode == "replaced-node" {
				require.Error(t, e)
				return
			}
			require.NoError(t, e)
			require.Equal(t, 2, gets)
			require.LessOrEqual(t, lists, capacityPages)
			b, e := json.Marshal(out)
			require.NoError(t, e)
			require.NotContains(t, string(b), "PRIVATE")
			require.Equal(t, "4", out.Allocatable.CPU.Value)
			switch mode {
			case "ok":
				require.Equal(t, "complete", out.Coverage)
				require.Equal(t, "510m", out.Requests.CPU.Value)
				require.Len(t, out.Pods, 1)
			case "terminal":
				require.Equal(t, "complete", out.Coverage)
				require.Equal(t, 1, out.ExcludedTerminal)
				require.Empty(t, out.Pods)
				require.Equal(t, "0", out.Requests.CPU.Value)
			case "denied":
				require.Equal(t, "forbidden", out.Coverage)
				require.Equal(t, "unknown", out.Requests.CPU.State)
			case "missing-request":
				require.Equal(t, "complete", out.Coverage)
				require.Equal(t, "unknown", out.Requests.CPU.State)
				require.Equal(t, "unspecified", out.Pods[0].Requests.CPU.State)
			default:
				require.NotEqual(t, "complete", out.Coverage)
				require.Equal(t, "unknown", out.Requests.CPU.State)
			}
		})
	}
}
func TestCapacityInvalidRequestAndCancellationReadNothing(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests++; w.WriteHeader(500) }))
	defer server.Close()
	client, e := dynamic.NewForConfig(&rest.Config{Host: server.URL})
	require.NoError(t, e)
	s := newSession("t", "h", client, false)
	defer s.Close()
	for _, mode := range []string{"namespace", "scope", "uid", "target", "kind", "cancel"} {
		req := capacityRequest()
		ctx, cancel := context.WithCancel(t.Context())
		switch mode {
		case "namespace":
			req.Namespace = ""
		case "scope":
			req.Ref.Scope = "blue"
		case "uid":
			req.Ref.UID = ""
		case "target":
			req.Ref.Target = "other"
		case "kind":
			req.Ref.Kind = "pods"
		case "cancel":
			cancel()
		}
		_, e := s.NodeCapacity(ctx, req)
		cancel()
		require.Error(t, e)
	}
	require.Zero(t, requests)
}
