package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
)

func clusterPolicyFixture(api string) *unstructured.Unstructured {
	u := policyFixture(api)
	u.SetKind("Cluster" + u.GetKind())
	u.SetNamespace("")
	u.SetName("cluster-policy-evidence")
	u.SetUID(types.UID("global-source"))
	u.Object["scope"] = map[string]any{"apiVersion": "v1", "kind": "Node", "name": "old-node", "uid": "old-node-uid"}
	return u
}

func clusterPolicyCatalog(s *session, version, mode string) {
	policyCatalog(s, version)
	defs := append([]*kindDef{}, s.cat.snap().reg.list...)
	for _, r := range []apiResource{
		{Group: "wgpolicyk8s.io", Version: version, Resource: "clusterpolicyreports", Kind: "ClusterPolicyReport", Verbs: []string{"get", "list", "watch"}},
		{Group: "openreports.io", Version: "v1alpha1", Resource: "clusterreports", Kind: "ClusterReport", Verbs: []string{"get", "list", "watch"}},
	} {
		if mode == "absent" {
			continue
		}
		if mode == "scope" {
			r.Namespaced = true
		}
		if mode == "kind" {
			r.Kind = "Report"
		}
		if mode == "no-list" {
			r.Verbs = []string{"get"}
		}
		defs = append(defs, discoveredDef(r))
	}
	snap := &catalogSnap{reg: newKindRegistry(defs...), state: catalogReady, rev: 3}
	if mode == "unconfirmed" {
		snap.unconfirmed = []string{"*"}
	}
	s.cat.cur.Store(snap)
}

func clusterPolicyFake(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	lists := map[schema.GroupVersionResource]string{}
	for _, s := range securitySources {
		lists[s.gvr] = s.kind + "List"
	}
	lists[schema.GroupVersionResource{Group: "wgpolicyk8s.io", Version: "v1beta1", Resource: "policyreports"}] = "PolicyReportList"
	lists[schema.GroupVersionResource{Group: "wgpolicyk8s.io", Version: "v1beta1", Resource: "clusterpolicyreports"}] = "ClusterPolicyReportList"
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), lists, objects...)
}

func TestSecurityClusterPolicyConsentExclusiveGlobalRouteAndSourceIdentity(t *testing.T) {
	for _, api := range []string{"wgpolicyk8s.io/v1alpha2", "wgpolicyk8s.io/v1beta1", "openreports.io/v1alpha1"} {
		t.Run(api, func(t *testing.T) {
			global := clusterPolicyFixture(api)
			client := clusterPolicyFake(global, policyFixture(api))
			s := newSession("t", "h", client, false)
			defer s.Close()
			version := "v1alpha2"
			if strings.HasSuffix(api, "v1beta1") {
				version = "v1beta1"
			}
			clusterPolicyCatalog(s, version, "")
			selection := "wgpolicy"
			if strings.HasPrefix(api, "openreports") {
				selection = "openreports"
			}
			scope := core.ScopeSel{Mode: core.ScopeSome, Names: []string{"blue", "green"}}
			for _, options := range []core.SecurityOptions{{IncludeClusterReports: true}, {PolicyReportAPI: selection}} {
				_, err := s.Security(t.Context(), scope, options)
				require.NoError(t, err)
				for _, a := range client.Actions() {
					require.NotContains(t, []string{"clusterpolicyreports", "clusterreports"}, a.GetResource().Resource)
				}
				client.ClearActions()
			}
			options := core.SecurityOptions{PolicyReportAPI: selection, IncludeClusterPolicyReports: true}
			out, err := s.Security(t.Context(), scope, options)
			require.NoError(t, err)
			require.Equal(t, "available", auditState(out, global.GetKind()))
			require.Len(t, out.Findings, 10)
			globalReads := 0
			for _, a := range client.Actions() {
				require.Equal(t, "list", a.GetVerb())
				require.NotContains(t, []string{"secrets", "configmaps", "nodes"}, a.GetResource().Resource)
				if strings.HasPrefix(a.GetResource().Resource, "cluster") && (a.GetResource().Group == "wgpolicyk8s.io" || a.GetResource().Group == "openreports.io") {
					globalReads++
					require.Empty(t, a.GetNamespace())
					require.Equal(t, api, a.GetResource().GroupVersion().String())
				}
			}
			require.Equal(t, 1, globalReads)
			for _, f := range out.Findings {
				if f.Ref.UID != "global-source" {
					continue
				}
				require.Empty(t, f.Ref.Scope)
				require.NotEmpty(t, f.Ref.Kind)
				require.Equal(t, global.GetName(), f.Ref.Name)
				require.Equal(t, api, f.Policy.APIVersion)
				require.Equal(t, "9", f.Policy.ResourceVersion)
				require.Equal(t, "old-node-uid", f.Policy.Scope.UID)
				require.Equal(t, "old-subject", f.Policy.Subjects[0].UID)
				require.Equal(t, "reported-exception", f.Policy.Exceptions)
				require.Contains(t, securityPolicyResults, f.Policy.Result)
			}
			raw, err := json.Marshal(out)
			require.NoError(t, err)
			require.NotContains(t, string(raw), "PRIVATE")
			client.ClearActions()
			_, err = s.Security(t.Context(), core.ScopeSel{Mode: core.ScopeSome, Names: []string{}}, options)
			require.NoError(t, err)
			require.Empty(t, client.Actions())
			_, err = s.Security(t.Context(), scope, core.SecurityOptions{IncludeClusterPolicyReports: true})
			require.Error(t, err)
			require.Empty(t, client.Actions())
		})
	}
}

func TestSecurityClusterPolicyDiscoveryAndIndependentFailures(t *testing.T) {
	for _, mode := range []string{"absent", "scope", "kind", "no-list", "unconfirmed", "old-version", "denied", "missing", "empty", "error", "repeated-page", "namespace-truncated"} {
		t.Run(mode, func(t *testing.T) {
			client := clusterPolicyFake()
			s := newSession("t", "h", client, false)
			defer s.Close()
			version := "v1alpha2"
			if mode == "old-version" {
				version = "v1alpha1"
			}
			clusterPolicyCatalog(s, version, mode)
			client.PrependReactor("list", "clusterpolicyreports", func(a k8stesting.Action) (bool, runtime.Object, error) {
				switch mode {
				case "denied":
					return true, nil, apierrors.NewForbidden(a.GetResource().GroupResource(), "", errors.New("PRIVATE"))
				case "missing":
					return true, nil, apierrors.NewNotFound(a.GetResource().GroupResource(), "")
				case "error":
					return true, nil, errors.New("PRIVATE")
				case "repeated-page":
					return true, &unstructured.UnstructuredList{Object: map[string]any{"metadata": map[string]any{"continue": "repeat"}}}, nil
				case "namespace-truncated":
					return true, &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*clusterPolicyFixture("wgpolicyk8s.io/v1alpha2")}}, nil
				}
				return true, &unstructured.UnstructuredList{}, nil
			})
			scope := core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}
			if mode == "namespace-truncated" {
				scope = core.ScopeSel{Mode: core.ScopeSome, Names: make([]string, 129)}
				for i := range scope.Names {
					scope.Names[i] = "namespace-" + strconv.Itoa(i)
				}
			}
			out, err := s.Security(t.Context(), scope, core.SecurityOptions{PolicyReportAPI: "wgpolicy", IncludeClusterPolicyReports: true})
			require.NoError(t, err)
			expected := map[string]string{"absent": "missing", "scope": "unsupported", "kind": "unsupported", "no-list": "unsupported", "unconfirmed": "partial", "old-version": "unsupported", "repeated-page": "partial", "namespace-truncated": "available"}[mode]
			if expected == "" {
				expected = mode
			}
			if mode == "namespace-truncated" {
				require.True(t, out.Truncated)
			}
			require.Equal(t, expected, auditState(out, "ClusterPolicyReport"))
			require.Equal(t, "not-requested", auditState(out, "ClusterReport"))
			reads := 0
			for _, a := range client.Actions() {
				if a.GetResource().Resource == "clusterpolicyreports" {
					reads++
					require.Empty(t, a.GetNamespace())
				}
			}
			if expected == "unsupported" || mode == "absent" || mode == "unconfirmed" {
				require.Zero(t, reads)
			} else {
				require.Positive(t, reads)
			}
			raw, err := json.Marshal(out)
			require.NoError(t, err)
			require.NotContains(t, string(raw), "PRIVATE")
		})
	}
}

func TestSecurityClusterPolicyRejectsNamespacedMetadataAndMalformedSource(t *testing.T) {
	for _, mode := range []string{"namespace", "kind", "api", "uid", "revision", "summary", "cap", "subjects"} {
		t.Run(mode, func(t *testing.T) {
			u := clusterPolicyFixture("openreports.io/v1alpha1")
			switch mode {
			case "namespace":
				u.SetNamespace("blue")
			case "kind":
				u.SetKind("Report")
			case "api":
				u.SetAPIVersion("openreports.io/v2")
			case "uid":
				u.SetUID("")
			case "revision":
				u.SetResourceVersion("")
			case "summary":
				u.Object["summary"] = map[string]any{"fail": int64(securityPolicyMaxCount + 1)}
			case "cap":
				u.Object["configuration"] = map[string]any{"limits": map[string]any{"maxResults": int64(5)}}
			case "subjects":
				u.Object["scopeSelector"] = map[string]any{}
			}
			s := newSession("t", "h", clusterPolicyFake(), false)
			defer s.Close()
			count := 0
			truncated := false
			ok := s.securityPolicyReport(core.Ref{UID: "global-source"}, securitySource{gvr: schema.GroupVersionResource{Group: "openreports.io", Version: "v1alpha1", Resource: "clusterreports"}, kind: "ClusterReport", global: true}, u, func(core.SecurityFinding) { count++ }, &truncated)
			require.False(t, ok)
			if mode == "summary" || mode == "cap" || mode == "subjects" {
				require.Equal(t, 5, count)
			} else {
				require.Zero(t, count)
			}
		})
	}
}

func TestSecurityClusterPolicyRealHTTPFiniteCancellation(t *testing.T) {
	held, cancelled := make(chan struct{}), make(chan struct{})
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "clusterpolicyreports") {
			reads.Add(1)
			require.Equal(t, "/apis/wgpolicyk8s.io/v1beta1/clusterpolicyreports", r.URL.Path)
			require.Equal(t, "50", r.URL.Query().Get("limit"))
			close(held)
			<-r.Context().Done()
			close(cancelled)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "List", "items": []any{}})
	}))
	defer server.Close()
	client, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
	require.NoError(t, err)
	s := newSession("t", "h", client, false)
	defer s.Close()
	clusterPolicyCatalog(s, "v1beta1", "")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := s.Security(ctx, core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}, core.SecurityOptions{PolicyReportAPI: "wgpolicy", IncludeClusterPolicyReports: true})
		done <- err
	}()
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("global policy LIST not started")
	}
	cancel()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("snapshot survived cancellation")
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("global policy socket survived cancellation")
	}
	require.Equal(t, int32(1), reads.Load())
}
