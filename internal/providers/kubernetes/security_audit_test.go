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

	"github.com/spk/spk-ocular/internal/core"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
)

func auditFixture(kind string) *unstructured.Unstructured {
	return mk("aquasecurity.github.io/v1alpha1", kind, "blue", "report-web", "report-uid", map[string]any{"metadata": map[string]any{"resourceVersion": "7", "ownerReferences": []any{ownerRef("Deployment", "web", "old-workload-uid")}, "annotations": map[string]any{"ignored": "PRIVATE-ANNOTATION"}}, "report": map[string]any{"scanner": map[string]any{"name": "Example", "version": "1.2", "vendor": "Test"}, "summary": map[string]any{"highCount": int64(1)}, "checks": []any{
		map[string]any{"checkID": "root", "success": false, "severity": "HIGH", "category": "Reliability", "title": "External root rule", "description": "An external description", "messages": []any{"<script>alert(1)</script>", "Evidence only"}, "remediation": "https://example.invalid/do-not-open", "scope": map[string]any{"type": "JSONPath", "value": ".spec.template.spec.containers[0]"}, "ignored": "PRIVATE-CHECK"},
		map[string]any{"checkID": "KSV002", "success": true, "severity": "CRITICAL", "scope": map[string]any{"type": "Container", "value": "main"}},
	}}})
}
func auditState(out core.SecuritySnapshot, kind string) string {
	for _, s := range out.AuditSources {
		if s.Kind == kind {
			return s.State
		}
	}
	return "absent"
}
func TestSecurityAuditTypedProvenanceResultsAndPrivacy(t *testing.T) {
	for _, kind := range []string{"ConfigAuditReport", "RbacAssessmentReport"} {
		t.Run(kind, func(t *testing.T) {
			u := auditFixture(kind)
			s := newSession("t", "h", securityFake(u), false)
			defer s.Close()
			out, err := s.Security(context.Background(), core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}, core.SecurityOptions{})
			require.NoError(t, err)
			require.Len(t, out.Findings, 2)
			require.Equal(t, "available", auditState(out, kind))
			require.Equal(t, "empty", out.CVEState)
			require.Zero(t, out.Reports)
			f := out.Findings[0]
			require.Equal(t, "report-uid", f.Ref.UID)
			require.Equal(t, "old-workload-uid", f.Audit.OwnerUID)
			require.Equal(t, "7", f.Audit.ResourceVersion)
			require.Zero(t, f.ScannedAt)
			require.Equal(t, "pass", f.Audit.Result)
			require.Equal(t, "main", f.Container)
			f = out.Findings[1]
			require.Equal(t, "fail", f.Audit.Result)
			require.Equal(t, "root", f.Rule)
			require.Equal(t, "External root rule", f.Title)
			require.Equal(t, "HIGH", f.Audit.NativeSeverity)
			require.Equal(t, "JSONPath", f.Audit.ScopeType)
			require.Equal(t, "Example", f.Audit.ScannerName)
			require.Equal(t, "1.2", f.Audit.ScannerVersion)
			require.Equal(t, "Reliability", f.Audit.Category)
			require.Len(t, f.Audit.Messages, 2)
			require.Equal(t, "https://example.invalid/do-not-open", f.Audit.Remediation)
			raw, _ := json.Marshal(out)
			require.NotContains(t, string(raw), "PRIVATE")
			require.NotContains(t, string(raw), `"messages":null`)
		})
	}
}
func TestSecurityAuditUnknownOptionalFieldsAndMalformedEvidence(t *testing.T) {
	for _, mode := range []string{"optional", "missing-success", "malformed-success", "summary", "missing-checks", "invalid-row", "messages", "oversize", "wrong-version", "missing-rv", "unknown-severity"} {
		t.Run(mode, func(t *testing.T) {
			u := auditFixture("ConfigAuditReport")
			r := u.Object["report"].(map[string]any)
			c := r["checks"].([]any)[0].(map[string]any)
			switch mode {
			case "optional":
				delete(r, "scanner")
				delete(r, "summary")
				delete(c, "description")
				delete(c, "remediation")
				delete(c, "messages")
				delete(c, "scope")
			case "missing-success":
				delete(c, "success")
			case "malformed-success":
				c["success"] = "true"
			case "summary":
				r["summary"].(map[string]any)["highCount"] = int64(3)
			case "missing-checks":
				delete(r, "checks")
			case "invalid-row":
				r["checks"] = []any{false}
			case "messages":
				c["messages"] = []any{int64(9)}
			case "oversize":
				c["description"] = strings.Repeat("界", 600)
			case "wrong-version":
				u.SetAPIVersion("aquasecurity.github.io/v9")
			case "missing-rv":
				u.SetResourceVersion("")
			case "unknown-severity":
				c["severity"] = "PRODUCER-LEVEL"
				delete(r, "summary")
			}
			source := securitySources[len(securitySources)-2]
			findings := []core.SecurityFinding{}
			truncated := false
			s := newSession("t", "h", securityFake(), false)
			defer s.Close()
			complete := s.securityAuditReport(core.Ref{UID: "report-uid"}, source, u, func(f core.SecurityFinding) { findings = append(findings, f) }, &truncated)
			if mode == "optional" || mode == "unknown-severity" {
				require.True(t, complete)
				require.False(t, truncated)
			} else {
				require.False(t, complete)
				require.Equal(t, mode == "oversize", truncated)
			}
			if mode == "missing-success" || mode == "malformed-success" {
				require.Equal(t, "unknown", findings[0].Audit.Result)
			}
			if mode == "optional" {
				require.Empty(t, findings[0].Scanner)
				require.Empty(t, findings[0].Audit.Messages)
				require.NotNil(t, findings[0].Audit.Messages)
			}
			if mode == "unknown-severity" {
				require.Equal(t, "unknown", findings[0].Severity)
				require.Equal(t, "PRODUCER-LEVEL", findings[0].Audit.NativeSeverity)
			}
			if mode == "wrong-version" || mode == "missing-rv" {
				require.Empty(t, findings)
			}
		})
	}
}
func TestSecurityAuditSourceCoverageScopeAndNoGlobalReads(t *testing.T) {
	for _, mode := range []string{"missing", "denied", "error", "namespace-missing", "partial", "empty"} {
		t.Run(mode, func(t *testing.T) {
			client := securityFake()
			client.PrependReactor("list", "configauditreports", func(a k8stesting.Action) (bool, runtime.Object, error) {
				switch mode {
				case "missing":
					return true, nil, apierrors.NewNotFound(securitySources[len(securitySources)-2].gvr.GroupResource(), "")
				case "namespace-missing":
					return true, nil, apierrors.NewNotFound(coreNamespaceGVR.GroupResource(), "blue")
				case "denied":
					return true, nil, apierrors.NewForbidden(securitySources[len(securitySources)-2].gvr.GroupResource(), "", errors.New("PRIVATE-ERROR"))
				case "error":
					return true, nil, errors.New("PRIVATE-ERROR")
				case "partial":
					if a.GetNamespace() == "green" {
						return true, nil, apierrors.NewForbidden(securitySources[len(securitySources)-2].gvr.GroupResource(), "", errors.New("PRIVATE-ERROR"))
					}
				}
				return false, nil, nil
			})
			s := newSession("t", "h", client, false)
			defer s.Close()
			out, err := s.Security(context.Background(), core.ScopeSel{Mode: core.ScopeSome, Names: []string{"blue", "green"}}, core.SecurityOptions{})
			require.NoError(t, err)
			expected := mode
			if mode == "namespace-missing" {
				expected = "error"
			}
			require.Equal(t, expected, auditState(out, "ConfigAuditReport"))
			require.Equal(t, "empty", auditState(out, "RbacAssessmentReport"))
			require.Equal(t, "empty", out.CVEState)
			raw, _ := json.Marshal(out)
			require.NotContains(t, string(raw), "PRIVATE")
			for _, a := range client.Actions() {
				require.Equal(t, "list", a.GetVerb())
				require.NotContains(t, []string{"clusterconfigauditreports", "clusterrbacassessmentreports", "policyreports", "secrets", "configmaps"}, a.GetResource().Resource)
				if strings.Contains(a.GetResource().Resource, "audit") || strings.Contains(a.GetResource().Resource, "assessment") {
					require.Contains(t, []string{"blue", "green"}, a.GetNamespace())
				}
			}
			client.ClearActions()
			out, err = s.Security(context.Background(), core.ScopeSel{Mode: core.ScopeSome, Names: []string{}}, core.SecurityOptions{})
			require.NoError(t, err)
			require.Empty(t, client.Actions())
			require.Equal(t, "not-requested", auditState(out, "ConfigAuditReport"))
		})
	}
}
func TestSecurityAuditActualHTTPNamespacePaginationAndCancellation(t *testing.T) {
	held := make(chan struct{})
	cancelled := make(chan struct{})
	var hold atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.NotContains(t, r.URL.Path, "secrets")
		require.NotContains(t, r.URL.Path, "configmaps")
		require.Empty(t, r.URL.Query().Get("watch"))
		items := []any{}
		next := ""
		if strings.HasSuffix(r.URL.Path, "/configauditreports") {
			require.Contains(t, r.URL.Path, "/namespaces/blue/")
			require.Equal(t, "50", r.URL.Query().Get("limit"))
			if hold.Load() {
				close(held)
				<-r.Context().Done()
				close(cancelled)
				return
			}
			if r.URL.Query().Get("continue") == "" {
				next = "page2"
				items = append(items, auditFixture("ConfigAuditReport").Object)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "List", "metadata": map[string]any{"continue": next}, "items": items})
	}))
	defer server.Close()
	d, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
	require.NoError(t, err)
	s := newSession("t", "h", d, false)
	defer s.Close()
	out, err := s.Security(context.Background(), core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}, core.SecurityOptions{})
	require.NoError(t, err)
	require.Len(t, out.Findings, 2)
	require.Equal(t, "available", auditState(out, "ConfigAuditReport"))
	hold.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, e := s.Security(ctx, core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}, core.SecurityOptions{})
		done <- e
	}()
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("read did not start")
	}
	cancel()
	select {
	case e := <-done:
		require.Error(t, e)
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not finish")
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP request survived cancel")
	}
}

var coreNamespaceGVR = securitySources[0].gvr.GroupVersion().WithResource("namespaces")

func TestSecurityAuditRepeatedCursorIsPartial(t *testing.T) {
	client := securityFake()
	client.PrependReactor("list", "configauditreports", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &unstructured.UnstructuredList{Object: map[string]any{"metadata": map[string]any{"continue": "same"}}}, nil
	})
	s := newSession("t", "h", client, false)
	defer s.Close()
	out, err := s.Security(context.Background(), core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}, core.SecurityOptions{})
	require.NoError(t, err)
	require.True(t, out.Truncated)
	require.Equal(t, "partial", auditState(out, "ConfigAuditReport"))
	require.Equal(t, "empty", out.CVEState)
}

func TestSecurityAuditFindingCheckAndMessageBudgets(t *testing.T) {
	u := auditFixture("ConfigAuditReport")
	report := u.Object["report"].(map[string]any)
	delete(report, "summary")
	checks := make([]any, 4097)
	for i := range checks {
		checks[i] = map[string]any{"checkID": "bulk", "success": false, "severity": "HIGH"}
	}
	report["checks"] = checks
	s := newSession("t", "h", securityFake(), false)
	defer s.Close()
	count := 0
	truncated := false
	complete := s.securityAuditReport(core.Ref{UID: "source"}, securitySources[len(securitySources)-2], u, func(core.SecurityFinding) { count++ }, &truncated)
	require.False(t, complete)
	require.True(t, truncated)
	require.Equal(t, 4096, count)
	objects := []runtime.Object{}
	for i := 0; i < 4; i++ {
		fixtureCopy := u.DeepCopy()
		fixtureCopy.SetName(strings.Repeat("x", i+1))
		fixtureCopy.SetUID(types.UID(fixtureCopy.GetName()))
		objects = append(objects, fixtureCopy)
	}
	bounded := newSession("t", "h", securityFake(objects...), false)
	defer bounded.Close()
	out, err := bounded.Security(context.Background(), core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}, core.SecurityOptions{})
	require.NoError(t, err)
	require.Len(t, out.Findings, 12000)
	require.True(t, out.Truncated)
	require.Equal(t, "partial", auditState(out, "ConfigAuditReport"))
	require.Equal(t, "empty", out.CVEState)
	report["checks"] = []any{map[string]any{"checkID": "many-messages", "success": true, "severity": "LOW", "messages": make([]any, 17)}}
	messages := report["checks"].([]any)[0].(map[string]any)["messages"].([]any)
	for i := range messages {
		messages[i] = "bounded"
	}
	truncated = false
	complete = s.securityAuditReport(core.Ref{UID: "source"}, securitySources[len(securitySources)-2], u, func(f core.SecurityFinding) { require.Len(t, f.Audit.Messages, 16) }, &truncated)
	require.False(t, complete)
	require.True(t, truncated)
}

func TestSecurityAuditRefIdentityIsRejectedRatherThanTruncated(t *testing.T) {
	for _, field := range []string{"uid", "name", "namespace", "resourceVersion"} {
		t.Run(field, func(t *testing.T) {
			u := auditFixture("ConfigAuditReport")
			u.Object["metadata"].(map[string]any)[field] = strings.Repeat("x", 600)
			s := newSession("t", "h", securityFake(), false)
			defer s.Close()
			truncated := false
			findings := []core.SecurityFinding{}
			complete := s.securityAuditReport(core.Ref{UID: "source"}, securitySources[len(securitySources)-2], u, func(f core.SecurityFinding) { findings = append(findings, f) }, &truncated)
			require.False(t, complete)
			require.Empty(t, findings)
			require.False(t, truncated)
		})
	}
}
