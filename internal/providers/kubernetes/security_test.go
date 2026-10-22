package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
)

func securityFake(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	lists := map[schema.GroupVersionResource]string{}
	for _, d := range allKinds.list {
		if !d.virtual {
			lists[d.gvr] = kindOf(d) + "List"
		}
	}
	for _, source := range securitySources {
		lists[source.gvr] = source.kind + "List"
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), lists, objects...)
}
func securityPodFixture(ns, name string) *unstructured.Unstructured {
	return mk("v1", "Pod", ns, name, "uid-"+ns+name, map[string]any{"metadata": map[string]any{"labels": map[string]any{"app": "api"}}, "spec": map[string]any{"securityContext": map[string]any{"runAsNonRoot": true, "seccompProfile": map[string]any{"type": "RuntimeDefault"}}, "automountServiceAccountToken": false, "containers": []any{map[string]any{"name": "main", "image": "private/image", "env": []any{map[string]any{"name": "PASSWORD", "value": "PRIVATE-VALUE"}}, "securityContext": map[string]any{"allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true, "capabilities": map[string]any{"drop": []any{"ALL"}, "add": []any{"NET_BIND_SERVICE"}}}}}}})
}
func securityRules(out core.SecuritySnapshot) []string {
	r := []string{}
	for _, f := range out.Findings {
		r = append(r, f.Rule)
	}
	return r
}
func TestSecurityScopePrivacyInheritedContextsAndPolicySelection(t *testing.T) {
	pod := securityPodFixture("blue", "api")
	policy := mk("networking.k8s.io/v1", "NetworkPolicy", "blue", "api", "policy", map[string]any{"spec": map[string]any{"podSelector": map[string]any{"matchExpressions": []any{map[string]any{"key": "app", "operator": "In", "values": []any{"api"}}}}, "policyTypes": []any{"Ingress", "Egress"}}})
	client := securityFake(pod, securityPodFixture("green", "other"), policy)
	s := newSession("t", "h", client, false)
	defer s.Close()
	out, err := s.Security(context.Background(), core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}, core.SecurityOptions{})
	require.NoError(t, err)
	assert.Empty(t, out.Findings)
	assert.Equal(t, "empty", out.CVEState)
	raw, _ := json.Marshal(out)
	assert.NotContains(t, string(raw), "PRIVATE-VALUE")
	assert.NotContains(t, string(raw), "private/image")
	for _, action := range client.Actions() {
		assert.Equal(t, "list", action.GetVerb())
		assert.NotContains(t, []string{"secrets", "configmaps"}, action.GetResource().Resource)
		if action.GetResource().Resource != "clusterroles" {
			assert.Equal(t, "blue", action.GetNamespace())
		}
	}
	client.ClearActions()
	out, err = s.Security(context.Background(), core.ScopeSel{Mode: core.ScopeSome, Names: []string{}}, core.SecurityOptions{})
	require.NoError(t, err)
	assert.Empty(t, client.Actions())
	assert.Equal(t, "not-requested", out.CVEState)
}
func TestSecurityRisksAccountOverridesAndWindows(t *testing.T) {
	pod := securityPodFixture("blue", "api")
	spec := pod.Object["spec"].(map[string]any)
	delete(spec, "automountServiceAccountToken")
	spec["hostPID"] = true
	spec["volumes"] = []any{map[string]any{"hostPath": map[string]any{"path": "PRIVATE-PATH"}}}
	sc := spec["containers"].([]any)[0].(map[string]any)["securityContext"].(map[string]any)
	sc["privileged"] = true
	sc["runAsUser"] = int64(0)
	sc["allowPrivilegeEscalation"] = true
	sc["seccompProfile"] = map[string]any{"type": "Unconfined"}
	sc["readOnlyRootFilesystem"] = false
	sc["capabilities"] = map[string]any{}
	account := mk("v1", "ServiceAccount", "blue", "default", "sa", map[string]any{"automountServiceAccountToken": false, "secrets": []any{map[string]any{"name": "PRIVATE-TOKEN"}}})
	s := newSession("t", "h", securityFake(pod, account), false)
	defer s.Close()
	out, err := s.Security(context.Background(), core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}, core.SecurityOptions{})
	require.NoError(t, err)
	rules := securityRules(out)
	for _, rule := range []string{"privileged", "host-access", "host-path", "root", "escalation", "seccomp", "writable-root", "capabilities"} {
		assert.Contains(t, rules, rule)
	}
	assert.NotContains(t, rules, "token-mount")
	raw, _ := json.Marshal(out)
	assert.NotContains(t, string(raw), "PRIVATE-")
	spec["automountServiceAccountToken"] = true
	w := securityWorkload{ref: core.Ref{UID: "p", Scope: "blue"}, kind: "Pod", spec: spec}
	findings := []core.SecurityFinding{}
	truncated := false
	s.securityPod(w, map[string]bool{"blue/default": false}, map[string]bool{"blue": true}, func(f core.SecurityFinding) { findings = append(findings, f) }, &truncated)
	assert.Contains(t, securityRules(core.SecuritySnapshot{Findings: findings}), "token-mount")
	spec["os"] = map[string]any{"name": "windows"}
	findings = nil
	s.securityPod(w, nil, map[string]bool{}, func(f core.SecurityFinding) { findings = append(findings, f) }, &truncated)
	assert.NotContains(t, securityRules(core.SecuritySnapshot{Findings: findings}), "seccomp")
	assert.Contains(t, securityRules(core.SecuritySnapshot{Findings: findings}), "privileged")
}
func TestSecurityIncompletePolicyAndReportAccessStates(t *testing.T) {
	for _, state := range []string{"missing", "denied", "error"} {
		t.Run(state, func(t *testing.T) {
			client := securityFake(securityPodFixture("blue", "api"))
			client.PrependReactor("list", "networkpolicies", func(k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "networkpolicies"}, "", errors.New("PRIVATE-ERROR"))
			})
			client.PrependReactor("list", "vulnerabilityreports", func(k8stesting.Action) (bool, runtime.Object, error) {
				switch state {
				case "missing":
					return true, nil, apierrors.NewNotFound(vulnerabilityReportGVR.GroupResource(), "")
				case "denied":
					return true, nil, apierrors.NewForbidden(vulnerabilityReportGVR.GroupResource(), "", errors.New("PRIVATE-ERROR"))
				default:
					return true, nil, fmt.Errorf("PRIVATE-ERROR")
				}
			})
			s := newSession("t", "h", client, false)
			defer s.Close()
			out, err := s.Security(context.Background(), core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}, core.SecurityOptions{})
			require.NoError(t, err)
			assert.Equal(t, state, out.CVEState)
			assert.NotContains(t, securityRules(out), "network-ingress")
			assert.NotContains(t, securityRules(out), "network-egress")
			if state == "missing" {
				require.Len(t, out.Problems, 1)
			} else {
				require.Len(t, out.Problems, 2)
			}
			raw, _ := json.Marshal(out)
			assert.NotContains(t, string(raw), "PRIVATE-ERROR")
		})
	}
}
func TestSecurityRBACLiteralGroupsAndReportProjection(t *testing.T) {
	role := mk("rbac.authorization.k8s.io/v1", "Role", "blue", "reader", "role", map[string]any{"rules": []any{map[string]any{"apiGroups": []any{""}, "resources": []any{"secrets"}, "verbs": []any{"get"}, "resourceNames": []any{"one"}}, map[string]any{"apiGroups": []any{"example.org"}, "resources": []any{"secrets"}, "verbs": []any{"get"}}}})
	report := mk("aquasecurity.github.io/v1alpha1", "VulnerabilityReport", "blue", "api", "report", map[string]any{"metadata": map[string]any{"ownerReferences": []any{map[string]any{"kind": "ReplicaSet", "name": "api", "uid": "OLD-WORKLOAD"}}, "labels": map[string]any{"trivy-operator.container.name": "main"}}, "report": map[string]any{"updateTimestamp": "2026-10-12T01:00:00Z", "artifact": map[string]any{"repository": "app", "tag": "latest", "digest": "sha256:abc"}, "scanner": map[string]any{"name": "Trivy", "version": "0.69.3"}, "vulnerabilities": []any{map[string]any{"vulnerabilityID": "CVE-TEST-1", "resource": "lib", "severity": "HIGH", "installedVersion": "1", "fixedVersion": "2", "title": "Example", "description": "PRIVATE-UNUSED"}}}})
	s := newSession("t", "h", securityFake(role, report), false)
	defer s.Close()
	out, err := s.Security(context.Background(), core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}, core.SecurityOptions{})
	require.NoError(t, err)
	assert.Equal(t, "available", out.CVEState)
	assert.Equal(t, 1, out.Reports)
	require.Len(t, out.Findings, 2)
	rules := securityRules(out)
	assert.Contains(t, rules, "rbac-secrets")
	assert.Contains(t, rules, "CVE-TEST-1")
	var cve core.SecurityFinding
	for _, f := range out.Findings {
		if f.Category == "cve" {
			cve = f
		}
	}
	assert.Equal(t, "report", cve.Ref.UID)
	assert.Equal(t, "ReplicaSet/api", cve.Workload)
	assert.Equal(t, "app:latest@sha256:abc", cve.Image)
	assert.Equal(t, "2", cve.Fixed)
	assert.Positive(t, cve.ScannedAt)
	raw, _ := json.Marshal(out)
	assert.NotContains(t, string(raw), "PRIVATE-UNUSED")
	assert.NotContains(t, string(raw), "OLD-WORKLOAD")
}
func TestSecurityPaginationCapsAndCancellation(t *testing.T) {
	client := securityFake()
	calls := 0
	client.PrependReactor("list", "pods", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		calls++
		list := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*securityPodFixture("blue", fmt.Sprint(calls))}}
		list.SetContinue("same")
		return true, list, nil
	})
	s := newSession("t", "h", client, false)
	defer s.Close()
	out, err := s.Security(context.Background(), core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}, core.SecurityOptions{})
	require.NoError(t, err)
	assert.Equal(t, 2, calls)
	assert.True(t, out.Truncated)
	truncated := false
	assert.Equal(t, securityTextLimit/3, len([]rune(securityText(strings.Repeat("界", 1000), &truncated))))
	assert.True(t, truncated)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.Security(ctx, core.ScopeSel{Mode: core.ScopeAll}, core.SecurityOptions{})
	assert.ErrorIs(t, err, context.Canceled)
}

func TestSecurityHTTPListsAreReadOnlyAndNamespaceBounded(t *testing.T) {
	var seen []string
	report := mk("aquasecurity.github.io/v1alpha1", "VulnerabilityReport", "blue", "api", "report", map[string]any{"report": map[string]any{"scanner": map[string]any{"name": "Trivy"}, "vulnerabilities": []any{map[string]any{"vulnerabilityID": "CVE-EXAMPLE", "severity": "MEDIUM", "resource": "lib", "installedVersion": "1"}}}})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "50", r.URL.Query().Get("limit"))
		seen = append(seen, r.URL.Path)
		if !strings.Contains(r.URL.Path, "/clusterroles") {
			assert.Contains(t, r.URL.Path, "/namespaces/blue/")
		}
		items := []any{}
		if strings.HasSuffix(r.URL.Path, "/vulnerabilityreports") {
			items = append(items, report.Object)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "List", "metadata": map[string]any{}, "items": items})
	}))
	defer server.Close()
	dyn, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
	require.NoError(t, err)
	s := newSession("t", "h", dyn, false)
	defer s.Close()
	out, err := s.Security(context.Background(), core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}, core.SecurityOptions{})
	require.NoError(t, err)
	require.Len(t, seen, 14)
	assert.NotContains(t, seen, "/apis/aquasecurity.github.io/v1alpha1/clusterconfigauditreports")
	assert.NotContains(t, seen, "/apis/aquasecurity.github.io/v1alpha1/clusterrbacassessmentreports")
	assert.Equal(t, "available", out.CVEState)
	require.Len(t, out.Findings, 1)
	assert.Equal(t, "CVE-EXAMPLE", out.Findings[0].Rule)
	assert.Equal(t, "report", out.Findings[0].Ref.UID)
	assert.Empty(t, out.Findings[0].Fixed)
	assert.Zero(t, out.Findings[0].ScannedAt)
}

func TestSecuritySparseReportsCannotEstablishZeroVulnerabilities(t *testing.T) {
	report := mk("aquasecurity.github.io/v1alpha1", "VulnerabilityReport", "blue", "api", "report", map[string]any{"report": map[string]any{"scanner": map[string]any{"name": "Trivy"}, "summary": map[string]any{"criticalCount": int64(2)}, "vulnerabilities": []any{}}})
	s := newSession("t", "h", securityFake(report), false)
	defer s.Close()
	out, err := s.Security(context.Background(), core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}, core.SecurityOptions{})
	require.NoError(t, err)
	assert.True(t, out.Truncated)
	assert.Equal(t, "partial", out.CVEState)
	assert.Empty(t, out.Findings)
}

func TestSecurityRBACWildcardDoesNotInventImpersonation(t *testing.T) {
	s := newSession("t", "h", securityFake(), false)
	defer s.Close()
	for _, test := range []struct {
		group, resource string
		critical        bool
	}{{"", "configmaps", false}, {"example.org", "users", false}, {"", "users", true}, {"authentication.k8s.io", "uids", true}, {"authentication.k8s.io", "userextras/scopes", true}} {
		t.Run(test.group+"/"+test.resource, func(t *testing.T) {
			role := mk("rbac.authorization.k8s.io/v1", "Role", "blue", "role", "role", map[string]any{"rules": []any{map[string]any{"apiGroups": []any{test.group}, "resources": []any{test.resource}, "verbs": []any{"*"}}}})
			findings := []core.SecurityFinding{}
			truncated := false
			s.securityRole(core.Ref{UID: "role"}, "Role", role, func(f core.SecurityFinding) { findings = append(findings, f) }, &truncated)
			if test.critical {
				assert.Contains(t, securityRules(core.SecuritySnapshot{Findings: findings}), "rbac-escalation")
			} else {
				assert.NotContains(t, securityRules(core.SecuritySnapshot{Findings: findings}), "rbac-escalation")
			}
		})
	}
}

func TestSecurityMissingOptionalReportAPIIsSetupNotCoverageFailure(t *testing.T) {
	for _, missingNamespace := range []bool{false, true} {
		t.Run(fmt.Sprint(missingNamespace), func(t *testing.T) {
			client := securityFake(securityPodFixture("blue", "api"))
			client.PrependReactor("list", "vulnerabilityreports", func(k8stesting.Action) (bool, runtime.Object, error) {
				if missingNamespace {
					return true, nil, apierrors.NewNotFound(schema.GroupResource{Resource: "namespaces"}, "blue")
				}
				return true, nil, apierrors.NewNotFound(vulnerabilityReportGVR.GroupResource(), "")
			})
			s := newSession("t", "h", client, false)
			defer s.Close()
			out, err := s.Security(context.Background(), core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}, core.SecurityOptions{})
			require.NoError(t, err)
			if missingNamespace {
				assert.Equal(t, "error", out.CVEState)
				require.Len(t, out.Problems, 1)
			} else {
				assert.Equal(t, "missing", out.CVEState)
				assert.Empty(t, out.Problems)
				assert.False(t, out.Truncated)
			}
		})
	}
}
