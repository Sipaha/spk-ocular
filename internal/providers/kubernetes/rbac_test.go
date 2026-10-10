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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
)

func rbacFake(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{serviceAccountGVR: "ServiceAccountList", roleBindingGVR: "RoleBindingList", clusterRoleBindingGVR: "ClusterRoleBindingList"}, objects...)
	client.PrependReactor("create", "selfsubjectreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		requireObject := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured)
		if _, ok := requireObject.Object["spec"]; ok {
			return true, nil, fmt.Errorf("identity must not configure a subject")
		}
		return true, &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"userInfo": map[string]any{"username": "operator", "groups": []any{"team:dev", "system:authenticated"}, "extra": map[string]any{"credential": "PRIVATE-EXTRA"}}}}}, nil
	})
	return client
}
func bindingFixture(kind, ns, name, roleKind, roleName string, subjects ...any) *unstructured.Unstructured {
	return mk("rbac.authorization.k8s.io/v1", kind, ns, name, "uid-"+ns+"-"+name, map[string]any{"roleRef": map[string]any{"apiGroup": "rbac.authorization.k8s.io", "kind": roleKind, "name": roleName}, "subjects": subjects})
}
func roleFixture(kind, ns, name string) *unstructured.Unstructured {
	return mk("rbac.authorization.k8s.io/v1", kind, ns, name, "uid-role-"+name, map[string]any{"rules": []any{map[string]any{"apiGroups": []any{""}, "resources": []any{"pods", "pods/log"}, "verbs": []any{"get", "list"}}}})
}
func TestRBACServiceAccountGroupsScopeRoleProvenanceAndNoValues(t *testing.T) {
	sa := mk("v1", "ServiceAccount", "blue", "bot", "account", map[string]any{"secrets": []any{map[string]any{"name": "PRIVATE-TOKEN-REF"}}})
	direct := bindingFixture("RoleBinding", "blue", "direct", "Role", "reader", map[string]any{"kind": "ServiceAccount", "name": "bot"})
	group := bindingFixture("RoleBinding", "green", "group", "ClusterRole", "shared", map[string]any{"kind": "Group", "apiGroup": "rbac.authorization.k8s.io", "name": "system:serviceaccounts:blue"})
	user := bindingFixture("ClusterRoleBinding", "", "global", "ClusterRole", "shared", map[string]any{"kind": "User", "apiGroup": "rbac.authorization.k8s.io", "name": "system:serviceaccount:blue:bot"})
	other := bindingFixture("RoleBinding", "blue", "other", "Role", "reader", map[string]any{"kind": "ServiceAccount", "namespace": "green", "name": "bot"})
	shared := roleFixture("ClusterRole", "", "shared")
	shared.Object["aggregationRule"] = map[string]any{"clusterRoleSelectors": []any{}}
	client := rbacFake(sa, direct, group, user, other, roleFixture("Role", "blue", "reader"), shared)
	s := newSession("t", "h", client, false)
	defer s.Close()
	subject := core.Ref{Provider: ProviderID, Target: "t", Scope: "blue", Name: "bot", UID: "account"}
	out, err := s.RBAC(context.Background(), core.ScopeSel{Mode: core.ScopeSome, Names: []string{"blue", "green"}}, &subject)
	require.NoError(t, err)
	assert.Equal(t, "system:serviceaccount:blue:bot", out.Principal)
	require.Len(t, out.Grants, 3)
	got := map[string]core.RBACGrant{}
	for _, g := range out.Grants {
		got[g.Binding.Name] = g
	}
	assert.Equal(t, "blue", got["direct"].Role.Scope)
	assert.Equal(t, "uid-role-reader", got["direct"].Role.UID)
	assert.True(t, got["global"].ClusterWide)
	assert.True(t, got["global"].Aggregated)
	assert.False(t, got["group"].ClusterWide)
	assert.Equal(t, "green", got["group"].Namespace)
	assert.Contains(t, out.Groups, "system:authenticated")
	b, err := json.Marshal(out)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "PRIVATE-")
	globalLists := 0
	sharedReads := 0
	for _, a := range client.Actions() {
		assert.NotContains(t, []string{"secrets", "configmaps"}, a.GetResource().Resource)
		if a.GetVerb() == "create" {
			t.Fatalf("ServiceAccount inspection created %s", a.GetResource())
		}
		if a.GetVerb() == "list" && a.GetResource() == clusterRoleBindingGVR {
			globalLists++
		}
		if a.GetVerb() == "get" && a.GetResource().Resource == "clusterroles" {
			sharedReads++
		}
		if a.GetVerb() == "list" && a.GetResource() == roleBindingGVR {
			assert.Contains(t, []string{"blue", "green"}, a.GetNamespace())
		}
	}
	assert.Equal(t, 1, globalLists)
	assert.Equal(t, 1, sharedReads)
	subject.UID = "old-account"
	_, err = s.RBAC(context.Background(), core.ScopeSel{Mode: core.ScopeAll}, &subject)
	require.Error(t, err)
}
func TestRBACCurrentIdentityPartialSourcesMissingRolesAndEmptySelection(t *testing.T) {
	user := map[string]any{"kind": "Group", "apiGroup": "rbac.authorization.k8s.io", "name": "team:dev"}
	client := rbacFake(bindingFixture("RoleBinding", "blue", "missing", "Role", "missing", user), bindingFixture("RoleBinding", "blue", "allowed", "Role", "reader", user), roleFixture("Role", "blue", "reader"))
	client.PrependReactor("list", "clusterrolebindings", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "clusterrolebindings"}, "", fmt.Errorf("PRIVATE-DIAGNOSTIC"))
	})
	s := newSession("t", "h", client, false)
	defer s.Close()
	out, err := s.RBAC(context.Background(), core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}, nil)
	require.NoError(t, err)
	assert.Equal(t, "operator", out.Principal)
	require.Len(t, out.Grants, 2)
	assert.Contains(t, out.Problems, core.GraphProblem{Kind: "clusterrolebindings", Class: "forbidden"})
	for _, g := range out.Grants {
		if g.Binding.Name == "missing" {
			assert.Equal(t, "not_found", g.Error)
			assert.Empty(t, g.Rules)
		}
	}
	b, _ := json.Marshal(out)
	assert.NotContains(t, string(b), "PRIVATE-")
	client.ClearActions()
	empty, err := s.RBAC(context.Background(), core.ScopeSel{Mode: core.ScopeSome, Names: []string{}}, nil)
	require.NoError(t, err)
	assert.Empty(t, empty.Accounts)
	for _, a := range client.Actions() {
		if a.GetVerb() == "list" {
			assert.Equal(t, clusterRoleBindingGVR, a.GetResource())
		}
	}
	client.PrependReactor("create", "selfsubjectreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "selfsubjectreviews"}, "", fmt.Errorf("denied"))
	})
	out, err = s.RBAC(context.Background(), core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}, nil)
	require.NoError(t, err)
	assert.Empty(t, out.Principal)
	assert.Empty(t, out.Grants)
	assert.Contains(t, out.Problems, core.GraphProblem{Kind: "selfsubjectreviews", Class: "forbidden"})
}
func TestRBACSubjectMatchingRejectsWrongGroupsAndNamespaces(t *testing.T) {
	cases := []struct {
		name    string
		binding *unstructured.Unstructured
		match   bool
	}{
		{"SA namespace defaults to binding", bindingFixture("RoleBinding", "blue", "x", "Role", "r", map[string]any{"kind": "ServiceAccount", "name": "bot"}), true},
		{"different namespace", bindingFixture("RoleBinding", "green", "x", "Role", "r", map[string]any{"kind": "ServiceAccount", "name": "bot"}), false},
		{"global missing namespace", bindingFixture("ClusterRoleBinding", "", "x", "ClusterRole", "r", map[string]any{"kind": "ServiceAccount", "name": "bot"}), false},
		{"wrong API group", bindingFixture("RoleBinding", "blue", "x", "Role", "r", map[string]any{"kind": "User", "apiGroup": "not-rbac", "name": "system:serviceaccount:blue:bot"}), false},
		{"group is literal", bindingFixture("RoleBinding", "blue", "x", "Role", "r", map[string]any{"kind": "Group", "apiGroup": "rbac.authorization.k8s.io", "name": "*"}), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, match := rbacBindingMatch(c.binding, "system:serviceaccount:blue:bot", []string{"system:serviceaccounts:blue"})
			assert.Equal(t, c.match, match)
		})
	}
}
func TestRBACPaginationCapsAndCancellation(t *testing.T) {
	client := rbacFake()
	page := 0
	client.PrependReactor("list", "rolebindings", func(k8stesting.Action) (bool, runtime.Object, error) {
		page++
		list := &unstructured.UnstructuredList{}
		if page == 1 {
			list.SetContinue("next")
		}
		return true, list, nil
	})
	s := newSession("t", "h", client, false)
	defer s.Close()
	out, err := s.RBAC(context.Background(), core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, page)
	assert.False(t, out.Truncated)
	client.PrependReactor("list", "rolebindings", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &unstructured.UnstructuredList{Items: make([]unstructured.Unstructured, rbacInventoryLimit+1)}, nil
	})
	out, err = s.RBAC(context.Background(), core.ScopeSel{Mode: core.ScopeAll}, nil)
	require.NoError(t, err)
	assert.True(t, out.Truncated)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.RBAC(ctx, core.ScopeSel{Mode: core.ScopeAll}, nil)
	require.ErrorIs(t, err, context.Canceled)
}
func TestRBACAccountRequestsMetadataOnly(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		assert.Equal(t, "GET", r.Method)
		assert.True(t, strings.HasPrefix(r.URL.Path, "/api/v1/namespaces/blue/serviceaccounts"))
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/bot") {
			assert.Contains(t, r.Header.Get("Accept"), "as=PartialObjectMetadata;")
			_, _ = w.Write([]byte(`{"apiVersion":"meta.k8s.io/v1","kind":"PartialObjectMetadata","metadata":{"name":"bot","namespace":"blue","uid":"account"}}`))
		} else {
			assert.Contains(t, r.Header.Get("Accept"), "as=PartialObjectMetadataList")
			_, _ = w.Write([]byte(`{"apiVersion":"meta.k8s.io/v1","kind":"PartialObjectMetadataList","metadata":{},"items":[]}`))
		}
	}))
	defer server.Close()
	client := rbacFake()
	client.PrependReactor("get", "serviceaccounts", func(k8stesting.Action) (bool, runtime.Object, error) {
		t.Fatal("raw ServiceAccount Get requested")
		return true, nil, nil
	})
	client.PrependReactor("list", "serviceaccounts", func(k8stesting.Action) (bool, runtime.Object, error) {
		t.Fatal("raw ServiceAccount List requested")
		return true, nil, nil
	})
	s := newSession("t", "h", client, false)
	defer s.Close()
	var err error
	s.graphMetadata, err = metadata.NewForConfig(&rest.Config{Host: server.URL})
	require.NoError(t, err)
	out, err := s.RBAC(context.Background(), core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}, &core.Ref{Scope: "blue", Name: "bot", UID: "account"})
	require.NoError(t, err)
	assert.Equal(t, 2, requests)
	assert.Equal(t, "account", out.Subject.UID)
}
func TestAccessReviewIsSelfOnlyUsesExactAttributesAndShowsEvaluationErrors(t *testing.T) {
	client := rbacFake()
	allowed := true
	problem := ""
	client.PrependReactor("create", "selfsubjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		u := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured)
		assert.Equal(t, "SelfSubjectAccessReview", u.GetKind())
		assert.Empty(t, str(u.Object, "spec", "user"))
		assert.Nil(t, fieldAt(u.Object, "spec", "groups"))
		assert.Equal(t, "blue", str(u.Object, "spec", "resourceAttributes", "namespace"))
		assert.Equal(t, "exec", str(u.Object, "spec", "resourceAttributes", "subresource"))
		assert.Equal(t, "pod", str(u.Object, "spec", "resourceAttributes", "name"))
		return true, &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"allowed": allowed, "reason": "RBAC evaluated", "evaluationError": problem}}}, nil
	})
	s := newSession("t", "h", client, false)
	defer s.Close()
	a := core.AccessAttributes{Verb: "create", Resource: "pods", Subresource: "exec", Namespace: "blue", Name: "pod"}
	review, err := s.CheckAccess(context.Background(), a)
	require.NoError(t, err)
	assert.Equal(t, "allowed", review.State)
	assert.Equal(t, a, review.Attributes)
	allowed = false
	review, err = s.CheckAccess(context.Background(), a)
	require.NoError(t, err)
	assert.Equal(t, "denied", review.State)
	problem = "one authorizer failed"
	review, err = s.CheckAccess(context.Background(), a)
	require.NoError(t, err)
	assert.Equal(t, "unknown", review.State)
	for _, action := range client.Actions() {
		assert.Equal(t, selfAccessGVR, action.GetResource())
		assert.Equal(t, "create", action.GetVerb())
	}
}
