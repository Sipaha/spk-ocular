package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
)

var clusterAuditOptions = core.SecurityOptions{IncludeClusterReports: true}

func clusterAuditFixture(kind string) *unstructured.Unstructured {
	u := auditFixture(kind)
	u.SetNamespace("")
	return u
}

func TestSecurityClusterAuditRequiresExplicitOptInAndIsNotNamespaceEvidence(t *testing.T) {
	client := securityFake(clusterAuditFixture("ClusterConfigAuditReport"), clusterAuditFixture("ClusterRbacAssessmentReport"))
	s := newSession("t", "h", client, false)
	defer s.Close()
	for _, scope := range []core.ScopeSel{{Mode: core.ScopeOne, Name: "blue"}, {Mode: core.ScopeAll}} {
		client.ClearActions()
		out, err := s.Security(t.Context(), scope, core.SecurityOptions{})
		require.NoError(t, err)
		require.Empty(t, out.Findings)
		require.Equal(t, "not-requested", auditState(out, "ClusterConfigAuditReport"))
		require.Equal(t, "not-requested", auditState(out, "ClusterRbacAssessmentReport"))
		for _, a := range client.Actions() {
			require.NotContains(t, []string{"clusterconfigauditreports", "clusterrbacassessmentreports"}, a.GetResource().Resource)
		}
	}
	client.ClearActions()
	out, err := s.Security(t.Context(), core.ScopeSel{Mode: core.ScopeSome, Names: []string{"blue", "green"}}, clusterAuditOptions)
	require.NoError(t, err)
	require.Len(t, out.Findings, 4)
	counts := map[string]int{}
	for _, a := range client.Actions() {
		require.Equal(t, "list", a.GetVerb())
		require.NotContains(t, []string{"secrets", "configmaps", "nodes", "policyreports", "reports", "clusterpolicyreports", "clusterreports"}, a.GetResource().Resource)
		if strings.HasPrefix(a.GetResource().Resource, "cluster") && a.GetResource().Resource != "clusterroles" {
			require.Empty(t, a.GetNamespace())
			counts[a.GetResource().Resource]++
		}
	}
	require.Equal(t, map[string]int{"clusterconfigauditreports": 1, "clusterrbacassessmentreports": 1}, counts)
	for _, f := range out.Findings {
		require.Empty(t, f.Ref.Scope)
		require.Equal(t, "report-uid", f.Ref.UID)
		require.Equal(t, "old-workload-uid", f.Audit.OwnerUID)
		require.Equal(t, "7", f.Audit.ResourceVersion)
		require.Zero(t, f.ScannedAt)
	}
	for _, source := range out.AuditSources {
		switch source.Kind {
		case "ClusterConfigAuditReport", "ClusterRbacAssessmentReport":
			require.True(t, source.ClusterScoped)
			require.Equal(t, "available", source.State)
			require.Equal(t, 1, source.Reports)
		case "ClusterPolicyReport", "ClusterReport":
			require.True(t, source.ClusterScoped)
			require.Equal(t, "not-requested", source.State)
			require.Zero(t, source.Reports)
		default:
			require.False(t, source.ClusterScoped)
		}
	}
	client.ClearActions()
	out, err = s.Security(t.Context(), core.ScopeSel{Mode: core.ScopeSome, Names: []string{}}, clusterAuditOptions)
	require.NoError(t, err)
	require.Empty(t, client.Actions())
	require.Equal(t, "not-requested", auditState(out, "ClusterConfigAuditReport"))
	require.Equal(t, "not-requested", out.CVEState)
}

func TestSecurityClusterAuditIndependentCoverageAndMalformedIdentity(t *testing.T) {
	for _, mode := range []string{"empty", "missing", "denied", "error", "namespace", "wrong-kind", "wrong-api", "missing-rv", "unknown-result", "repeated-page"} {
		t.Run(mode, func(t *testing.T) {
			u := clusterAuditFixture("ClusterConfigAuditReport")
			switch mode {
			case "namespace":
				u.SetNamespace("blue")
			case "wrong-kind":
				u.SetKind("ConfigAuditReport")
			case "wrong-api":
				u.SetAPIVersion("aquasecurity.github.io/v9")
			case "missing-rv":
				u.SetResourceVersion("")
			case "unknown-result":
				delete(u.Object["report"].(map[string]any)["checks"].([]any)[0].(map[string]any), "success")
			}
			client := securityFake(clusterAuditFixture("ClusterRbacAssessmentReport"))
			gvr := schema.GroupVersionResource{Group: "aquasecurity.github.io", Version: "v1alpha1", Resource: "clusterconfigauditreports"}
			client.PrependReactor("list", "clusterconfigauditreports", func(a k8stesting.Action) (bool, runtime.Object, error) {
				require.Empty(t, a.GetNamespace())
				switch mode {
				case "missing":
					return true, nil, apierrors.NewNotFound(gvr.GroupResource(), "")
				case "denied":
					return true, nil, apierrors.NewForbidden(gvr.GroupResource(), "", errors.New("PRIVATE-ERROR"))
				case "error":
					return true, nil, errors.New("PRIVATE-ERROR")
				case "empty":
					return true, &unstructured.UnstructuredList{}, nil
				case "repeated-page":
					return true, &unstructured.UnstructuredList{Object: map[string]any{"metadata": map[string]any{"continue": "repeated"}}}, nil
				default:
					return true, &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*u}}, nil
				}
			})
			s := newSession("t", "h", client, false)
			defer s.Close()
			out, err := s.Security(t.Context(), core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}, clusterAuditOptions)
			require.NoError(t, err)
			expected := mode
			if mode == "namespace" || mode == "wrong-kind" || mode == "wrong-api" || mode == "missing-rv" || mode == "unknown-result" || mode == "repeated-page" {
				expected = "partial"
			}
			require.Equal(t, expected, auditState(out, "ClusterConfigAuditReport"))
			require.Equal(t, "available", auditState(out, "ClusterRbacAssessmentReport"))
			require.Equal(t, "empty", out.CVEState)
			require.Equal(t, mode == "repeated-page", out.Truncated)
			for _, f := range out.Findings {
				if f.KindTitle == "ClusterConfigAuditReport" {
					require.Equal(t, "unknown-result", mode)
				}
			}
			raw, err := json.Marshal(out)
			require.NoError(t, err)
			require.NotContains(t, string(raw), "PRIVATE")
		})
	}
}

func TestSecurityClusterAuditActualGlobalHTTPAndSocketCancellation(t *testing.T) {
	var hold atomic.Bool
	held := make(chan struct{})
	cancelled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Empty(t, r.URL.Query().Get("watch"))
		for _, forbidden := range []string{"/nodes", "/secrets", "/configmaps"} {
			require.NotContains(t, r.URL.Path, forbidden)
		}
		items := []any{}
		if strings.Contains(r.URL.Path, "clusterconfigauditreports") || strings.Contains(r.URL.Path, "clusterrbacassessmentreports") {
			require.NotContains(t, r.URL.Path, "/namespaces/")
			require.Equal(t, "50", r.URL.Query().Get("limit"))
			if strings.HasSuffix(r.URL.Path, "/clusterconfigauditreports") {
				if hold.Load() {
					close(held)
					<-r.Context().Done()
					close(cancelled)
					return
				}
				items = append(items, clusterAuditFixture("ClusterConfigAuditReport").Object)
			} else {
				items = append(items, clusterAuditFixture("ClusterRbacAssessmentReport").Object)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "List", "metadata": map[string]any{}, "items": items})
	}))
	defer server.Close()
	d, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
	require.NoError(t, err)
	s := newSession("t", "h", d, false)
	defer s.Close()
	out, err := s.Security(t.Context(), core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}, clusterAuditOptions)
	require.NoError(t, err)
	require.Len(t, out.Findings, 4)
	hold.Store(true)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, e := s.Security(ctx, core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}, clusterAuditOptions)
		done <- e
	}()
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("cluster read not started")
	}
	cancel()
	select {
	case e := <-done:
		require.Error(t, e)
	case <-time.After(5 * time.Second):
		t.Fatal("cluster snapshot survived cancellation")
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("global HTTP request survived cancellation")
	}
}

func TestSecurityClusterAuditCoverageDoesNotInheritNamespaceTruncation(t *testing.T) {
	names := make([]string, 129)
	for i := range names {
		names[i] = fmt.Sprintf("namespace-%03d", i)
	}
	client := securityFake(clusterAuditFixture("ClusterConfigAuditReport"))
	s := newSession("t", "h", client, false)
	defer s.Close()
	out, err := s.Security(t.Context(), core.ScopeSel{Mode: core.ScopeSome, Names: names}, clusterAuditOptions)
	require.NoError(t, err)
	require.True(t, out.Truncated)
	require.Equal(t, "available", auditState(out, "ClusterConfigAuditReport"))
	require.Equal(t, "empty", auditState(out, "ClusterRbacAssessmentReport"))
	require.Equal(t, "partial", auditState(out, "ConfigAuditReport"))
	reads := 0
	for _, a := range client.Actions() {
		if a.GetResource().Resource == "clusterconfigauditreports" {
			reads++
			require.Empty(t, a.GetNamespace())
		}
	}
	require.Equal(t, 1, reads)
}
