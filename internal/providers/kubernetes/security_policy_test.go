package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
)

func policyFixture(api string) *unstructured.Unstructured {
	kind := "PolicyReport"
	if strings.HasPrefix(api, "openreports.io/") {
		kind = "Report"
	}
	rows := []any{}
	for _, result := range securityPolicyResults {
		rows = append(rows, map[string]any{"source": "engine-a", "policy": "same-policy", "rule": "same-rule", "result": result, "severity": "high", "scored": false, "category": "Reliability", "message": "literal <script>no execution</script> https://example.invalid/", "timestamp": map[string]any{"seconds": int64(1700000000), "nanos": int64(123000000)}, "properties": map[string]any{"process": "background scan", "exceptions": "reported-exception", "password": "PRIVATE-PROPERTY", "controlsJSON": "PRIVATE-RAW"}, "resources": []any{map[string]any{"kind": "Secret", "name": "old-secret", "namespace": "other", "uid": "old-subject"}}})
	}
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": api, "kind": kind, "metadata": map[string]any{"name": "policy-evidence", "namespace": "blue", "uid": "report-source", "resourceVersion": "9", "annotations": map[string]any{"raw": "PRIVATE-ANNOTATION"}}, "scope": map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "old-workload", "namespace": "blue", "uid": "old-scope"}, "summary": map[string]any{"pass": int64(1), "fail": int64(1), "warn": int64(1), "error": int64(1), "skip": int64(1)}, "results": rows}}
}

func policyFake(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	lists := map[schema.GroupVersionResource]string{}
	for _, source := range securitySources {
		lists[source.gvr] = source.kind + "List"
	}
	lists[schema.GroupVersionResource{Group: "wgpolicyk8s.io", Version: "v1beta1", Resource: "policyreports"}] = "PolicyReportList"
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), lists, objects...)
}

func policyCatalog(s *session, wgVersion string) {
	defs := append([]*kindDef{}, allKinds.list...)
	for _, resource := range []apiResource{
		{Group: "wgpolicyk8s.io", Version: wgVersion, Resource: "policyreports", Kind: "PolicyReport", Namespaced: true, Verbs: []string{"get", "list", "watch"}},
		{Group: "openreports.io", Version: "v1alpha1", Resource: "reports", Kind: "Report", Namespaced: true, Verbs: []string{"get", "list", "watch"}},
	} {
		defs = append(defs, discoveredDef(resource))
	}
	s.cat.cur.Store(&catalogSnap{reg: newKindRegistry(defs...), state: catalogReady, rev: 2})
}

func TestSecurityPolicyRequiresExclusiveExplicitAPIAndPreservesNativeEvidence(t *testing.T) {
	for _, version := range []string{"v1alpha2", "v1beta1"} {
		t.Run(version, func(t *testing.T) {
			client := policyFake(policyFixture("wgpolicyk8s.io/"+version), policyFixture("openreports.io/v1alpha1"))
			s := newSession("t", "h", client, false)
			defer s.Close()
			policyCatalog(s, version)
			out, err := s.Security(t.Context(), core.ScopeSel{Mode: core.ScopeAll}, core.SecurityOptions{})
			require.NoError(t, err)
			require.Empty(t, out.Findings)
			for _, a := range client.Actions() {
				require.NotContains(t, []string{"policyreports", "reports"}, a.GetResource().Resource)
			}
			for selection, kind := range map[string]string{"wgpolicy": "PolicyReport", "openreports": "Report"} {
				client.ClearActions()
				out, err = s.Security(t.Context(), core.ScopeSel{Mode: core.ScopeSome, Names: []string{"blue", "green"}}, core.SecurityOptions{PolicyReportAPI: selection})
				require.NoError(t, err)
				require.Equal(t, "available", auditState(out, kind))
				require.Len(t, out.Findings, 5)
				for i, f := range out.Findings {
					require.Equal(t, securityPolicyResults[i], f.Policy.Result)
					require.Equal(t, "report-source", f.Ref.UID)
					require.Equal(t, "old-scope", f.Policy.Scope.UID)
					require.Equal(t, "old-subject", f.Policy.Subjects[0].UID)
					require.Equal(t, "9", f.Policy.ResourceVersion)
					require.Equal(t, "engine-a", f.Policy.Source)
					require.NotNil(t, f.Policy.Scored)
					require.False(t, *f.Policy.Scored)
					require.Equal(t, int64(1700000000123), f.ScannedAt)
					require.Contains(t, f.Policy.Message, "<script>")
					require.Equal(t, "reported-exception", f.Policy.Exceptions)
				}
				policyReads := 0
				for _, a := range client.Actions() {
					require.Equal(t, "list", a.GetVerb())
					require.NotContains(t, []string{"secrets", "configmaps", "nodes", "clusterpolicyreports", "clusterreports"}, a.GetResource().Resource)
					if a.GetResource().Group == "wgpolicyk8s.io" || a.GetResource().Group == "openreports.io" {
						policyReads++
						require.Equal(t, map[string]string{"wgpolicy": "wgpolicyk8s.io", "openreports": "openreports.io"}[selection], a.GetResource().Group)
						require.Contains(t, []string{"blue", "green"}, a.GetNamespace())
					}
				}
				require.Equal(t, 2, policyReads)
				raw, err := json.Marshal(out)
				require.NoError(t, err)
				require.NotContains(t, string(raw), "PRIVATE")
			}
			client.ClearActions()
			_, err = s.Security(t.Context(), core.ScopeSel{Mode: core.ScopeSome, Names: []string{}}, core.SecurityOptions{PolicyReportAPI: "wgpolicy"})
			require.NoError(t, err)
			require.Empty(t, client.Actions())
			_, err = s.Security(t.Context(), core.ScopeSel{Mode: core.ScopeAll}, core.SecurityOptions{PolicyReportAPI: "both"})
			require.Error(t, err)
			require.Empty(t, client.Actions())
		})
	}
}

func TestSecurityPolicyDiscoveryAndIndependentCoverage(t *testing.T) {
	for _, mode := range []string{"absent", "unconfirmed", "wildcard", "unsupported", "scope", "empty", "missing", "denied", "error", "repeated-page"} {
		t.Run(mode, func(t *testing.T) {
			client := policyFake()
			s := newSession("t", "h", client, false)
			defer s.Close()
			policyCatalog(s, "v1alpha2")
			snap := *s.cat.snap()
			switch mode {
			case "absent":
				snap.reg = allKinds
			case "unconfirmed":
				snap.unconfirmed = []string{"wgpolicyk8s.io"}
			case "wildcard":
				snap.unconfirmed = []string{"*"}
			case "unsupported":
				policyCatalog(s, "v1alpha1")
				snap = *s.cat.snap()
			case "scope":
				defs := append([]*kindDef{}, allKinds.list...)
				defs = append(defs, discoveredDef(apiResource{Group: "wgpolicyk8s.io", Version: "v1alpha2", Resource: "policyreports", Kind: "PolicyReport", Namespaced: false, Verbs: []string{"list"}}))
				snap.reg = newKindRegistry(defs...)
			}
			s.cat.cur.Store(&snap)
			client.PrependReactor("list", "policyreports", func(a k8stesting.Action) (bool, runtime.Object, error) {
				switch mode {
				case "missing":
					return true, nil, apierrors.NewNotFound(a.GetResource().GroupResource(), "")
				case "denied":
					return true, nil, apierrors.NewForbidden(a.GetResource().GroupResource(), "", errors.New("PRIVATE"))
				case "error":
					return true, nil, errors.New("PRIVATE")
				case "repeated-page":
					return true, &unstructured.UnstructuredList{Object: map[string]any{"metadata": map[string]any{"continue": "same"}}}, nil
				}
				return true, &unstructured.UnstructuredList{}, nil
			})
			out, err := s.Security(t.Context(), core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}, core.SecurityOptions{PolicyReportAPI: "wgpolicy"})
			require.NoError(t, err)
			expected := map[string]string{"absent": "missing", "unconfirmed": "partial", "wildcard": "partial", "unsupported": "unsupported", "scope": "unsupported", "repeated-page": "partial"}[mode]
			if expected == "" {
				expected = mode
			}
			require.Equal(t, expected, auditState(out, "PolicyReport"))
			require.Equal(t, "not-requested", auditState(out, "Report"))
			raw, _ := json.Marshal(out)
			require.NotContains(t, string(raw), "PRIVATE")
			if slicesNoRead := mode == "absent" || mode == "unconfirmed" || mode == "wildcard" || mode == "unsupported" || mode == "scope"; slicesNoRead {
				for _, a := range client.Actions() {
					require.NotEqual(t, "wgpolicyk8s.io", a.GetResource().Group)
				}
			}
		})
	}
}

func TestSecurityPolicyActualHTTPExclusivityAndSocketCancellation(t *testing.T) {
	var reads atomic.Int32
	held, cancelled := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "GET", r.Method)
		require.Empty(t, r.URL.Query().Get("watch"))
		require.NotContains(t, r.URL.Path, "openreports.io")
		for _, path := range []string{"/secrets", "/nodes", "/configmaps", "/clusterpolicyreports", "/clusterreports"} {
			require.NotContains(t, r.URL.Path, path)
		}
		if strings.Contains(r.URL.Path, "wgpolicyk8s.io") {
			require.Equal(t, "/apis/wgpolicyk8s.io/v1beta1/namespaces/blue/policyreports", r.URL.Path)
			require.Equal(t, "50", r.URL.Query().Get("limit"))
			reads.Add(1)
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
	policyCatalog(s, "v1beta1")
	_, err = s.Security(t.Context(), core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}, core.SecurityOptions{})
	require.NoError(t, err)
	require.Zero(t, reads.Load())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := s.Security(ctx, core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}, core.SecurityOptions{PolicyReportAPI: "wgpolicy"})
		done <- err
	}()
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("policy read not started")
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
		t.Fatal("policy socket survived cancellation")
	}
	require.Equal(t, int32(1), reads.Load())
}

func TestSecurityPolicyMalformedFieldsLimitsAndUnknowns(t *testing.T) {
	for _, mode := range []string{"missing-uid", "missing-rv", "wrong-api", "wrong-kind", "empty-namespace", "unknown-result", "missing-result", "bad-scored", "bad-time", "bad-summary", "summary-mismatch", "filtered", "capped", "bad-filter", "scope-conflict", "subject-conflict", "bad-selector", "text-limit", "subject-limit", "results-limit", "empty-results", "missing-results", "optional-absent", "root-source", "native-info", "summary-too-large", "limit-too-large", "long-uid", "long-name", "long-rv", "long-namespace"} {
		t.Run(mode, func(t *testing.T) {
			u := policyFixture("openreports.io/v1alpha1")
			rows := u.Object["results"].([]any)
			row := rows[0].(map[string]any)
			switch mode {
			case "long-uid":
				u.SetUID(types.UID(strings.Repeat("u", 257)))
			case "long-name":
				u.SetName(strings.Repeat("n", 254))
			case "long-rv":
				u.SetResourceVersion(strings.Repeat("v", 257))
			case "long-namespace":
				u.SetNamespace(strings.Repeat("n", 254))
			case "missing-uid":
				u.SetUID("")
			case "missing-rv":
				u.SetResourceVersion("")
			case "wrong-api":
				u.SetAPIVersion("openreports.io/v9")
			case "wrong-kind":
				u.SetKind("ClusterReport")
			case "empty-namespace":
				u.SetNamespace("")
			case "unknown-result":
				row["result"] = "exception"
			case "missing-result":
				delete(row, "result")
			case "bad-scored":
				row["scored"] = "true"
			case "bad-time":
				row["timestamp"] = map[string]any{"seconds": int64(1700000000), "nanos": int64(1e9)}
			case "bad-summary":
				u.Object["summary"] = map[string]any{"pass": "1"}
			case "summary-mismatch":
				u.Object["summary"] = map[string]any{"pass": int64(100)}
			case "filtered":
				u.Object["configuration"] = map[string]any{"limits": map[string]any{"statusFilter": []any{"fail"}}}
			case "capped":
				u.Object["configuration"] = map[string]any{"limits": map[string]any{"maxResults": int64(5)}}
			case "summary-too-large":
				u.Object["summary"] = map[string]any{"pass": int64(securityPolicyMaxCount + 1)}
			case "limit-too-large":
				u.Object["configuration"] = map[string]any{"limits": map[string]any{"maxResults": int64(securityPolicyMaxCount + 1)}}
			case "bad-filter":
				u.Object["configuration"] = map[string]any{"limits": map[string]any{"statusFilter": []any{true}}}
			case "scope-conflict":
				u.Object["scopeSelector"] = map[string]any{}
			case "subject-conflict":
				row["resourceSelector"] = map[string]any{}
			case "bad-selector":
				row["resourceSelector"] = map[string]any{"matchLabels": map[string]any{"bad": true}}
			case "text-limit":
				row["message"] = strings.Repeat("字", 260)
			case "subject-limit":
				row["resources"] = make([]any, 17)
				for i := range row["resources"].([]any) {
					row["resources"].([]any)[i] = map[string]any{"name": "subject"}
				}
			case "results-limit":
				u.Object["results"] = make([]any, 4097)
				for i := range u.Object["results"].([]any) {
					u.Object["results"].([]any)[i] = row
				}
			case "empty-results":
				u.Object["results"] = []any{}
				delete(u.Object, "summary")
			case "missing-results":
				delete(u.Object, "results")
			case "optional-absent":
				delete(u.Object, "summary")
				delete(u.Object, "scope")
				u.SetCreationTimestamp(metav1.NewTime(time.Now()))
				for _, raw := range rows {
					r := raw.(map[string]any)
					delete(r, "source")
					delete(r, "scored")
					delete(r, "timestamp")
					delete(r, "properties")
				}
			case "root-source":
				u.Object["source"] = "root-engine"
				delete(row, "source")
			case "native-info":
				row["severity"] = "info"
			}
			s := newSession("t", "h", policyFake(), false)
			defer s.Close()
			var findings []core.SecurityFinding
			truncated := false
			complete := s.securityPolicyReport(core.Ref{UID: "report-source"}, securitySource{gvr: schema.GroupVersionResource{Group: "openreports.io", Version: "v1alpha1", Resource: "reports"}, kind: "Report"}, u, func(f core.SecurityFinding) { findings = append(findings, f) }, &truncated)
			valid := mode == "empty-results" || mode == "optional-absent" || mode == "root-source" || mode == "native-info"
			require.Equal(t, valid, complete)
			if strings.HasPrefix(mode, "long-") || strings.HasPrefix(mode, "missing-u") || mode == "missing-rv" || mode == "wrong-api" || mode == "wrong-kind" || mode == "empty-namespace" || mode == "empty-results" || mode == "missing-results" {
				require.Empty(t, findings)
			}
			if mode == "unknown-result" || mode == "missing-result" {
				require.Equal(t, "unknown", findings[0].Policy.Result)
			}
			if mode == "optional-absent" {
				require.Nil(t, findings[0].Policy.Scored)
				require.Zero(t, findings[0].ScannedAt)
				require.Empty(t, findings[0].Policy.Source)
				require.Nil(t, findings[0].Policy.Summary)
			}
			if mode == "root-source" {
				require.Equal(t, "root-engine", findings[0].Policy.Source)
				require.Equal(t, "engine-a", findings[1].Policy.Source)
			}
			if mode == "summary-too-large" {
				require.NotContains(t, findings[0].Policy.Summary, "pass")
			}
			if mode == "limit-too-large" {
				require.Nil(t, findings[0].Policy.MaxResults)
			}
			if mode == "native-info" {
				require.Equal(t, "info", findings[0].Policy.NativeSeverity)
				require.Equal(t, "unknown", findings[0].Severity)
			}
			if mode == "text-limit" {
				require.LessOrEqual(t, len(findings[0].Policy.Message), 512)
				require.True(t, utf8.ValidString(findings[0].Policy.Message))
			}
			if mode == "subject-limit" {
				require.Len(t, findings[0].Policy.Subjects, 16)
			}
			if mode == "results-limit" {
				require.Len(t, findings, 4096)
			}
			require.Equal(t, mode == "text-limit" || mode == "subject-limit" || mode == "results-limit", truncated)
		})
	}
}
