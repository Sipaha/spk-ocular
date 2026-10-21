package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
)

var argoRef = core.Ref{Provider: ProviderID, Target: "ctx", Scope: "ns", Kind: "argoproj.io/applications", Name: "web", UID: "argo-web"}

func argoFixture(ns, name string) *unstructured.Unstructured {
	source := map[string]any{"repoURL": "https://USER:PRIVATE-PASSWORD@git.invalid/repo?token=PRIVATE-TOKEN#fragment", "path": "deploy", "targetRevision": "main", "helm": map[string]any{"values": "PRIVATE-VALUES"}, "plugin": map[string]any{"env": []any{map[string]any{"name": "TOKEN", "value": "PRIVATE-ENV"}}}}
	dest := map[string]any{"server": "https://kubernetes.default.svc", "namespace": "ns"}
	return mk("argoproj.io/v1alpha1", "Application", ns, name, "argo-"+name, map[string]any{"metadata": map[string]any{"resourceVersion": "7"}, "spec": map[string]any{"source": source, "destination": dest, "project": "default", "syncPolicy": map[string]any{"syncOptions": []any{"ServerSideApply=true"}}}, "status": map[string]any{"sync": map[string]any{"status": "OutOfSync", "revision": strings.Repeat("a", 40), "comparedTo": map[string]any{"source": runtime.DeepCopyJSONValue(source), "destination": runtime.DeepCopyJSONValue(dest)}}, "health": map[string]any{"status": "Degraded"}, "reconciledAt": "2026-10-20T12:00:00Z", "conditions": []any{map[string]any{"type": "ComparisonError", "message": "Failed https://USER:PRIVATE-PASSWORD@git.invalid/repo?token=PRIVATE-TOKEN"}}, "resources": []any{map[string]any{"group": "apps", "version": "v1", "kind": "Deployment", "name": "api", "namespace": "ns", "status": "OutOfSync", "health": map[string]any{"status": "Degraded", "message": "ProgressDeadlineExceeded"}}}}})
}
func argoFake(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	lists := map[schema.GroupVersionResource]string{{Version: "v1", Resource: "events"}: "EventList", {Version: "v1", Resource: "services"}: "ServiceList"}
	for _, source := range argoResources {
		if source.kind != "Event" && source.kind != "Service" {
			lists[schema.GroupVersionResource{Group: argoGroup, Version: "v1alpha1", Resource: source.resource}] = source.kind + "List"
		}
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), lists, objects...)
}
func installArgoKind(s *session) {
	snap := *s.cat.snap()
	defs := append([]*kindDef{}, snap.reg.list...)
	for _, r := range argoResources {
		if r.kind != "Event" && r.kind != "Service" {
			defs = append(defs, discoveredDef(apiResource{Group: argoGroup, Version: "v1alpha1", Resource: r.resource, Kind: r.kind, Namespaced: true, Verbs: []string{"list", "watch", "get", "patch"}}))
		}
	}
	snap.reg = newKindRegistry(defs...)
	s.cat.cur.Store(&snap)
}
func argoSession(t *testing.T, objects ...runtime.Object) (*session, *dynamicfake.FakeDynamicClient) {
	t.Helper()
	c := argoFake(objects...)
	s := newSession("ctx", "hash", c, false)
	installArgoKind(s)
	t.Cleanup(s.Close)
	return s, c
}
func TestArgoSnapshotScopeProjectionPrivacyAndEventIdentity(t *testing.T) {
	app := argoFixture("ns", "web")
	other := argoFixture("other", "web")
	event := mk("v1", "Event", "ns", "failure", "event", map[string]any{"involvedObject": map[string]any{"apiVersion": "argoproj.io/v1alpha1", "kind": "Application", "name": "web", "namespace": "ns", "uid": "argo-web"}, "type": "Warning", "reason": "SyncFailed", "message": "sync failed", "lastTimestamp": "2026-10-20T12:00:00Z", "count": int64(3)})
	historical := event.DeepCopy()
	historical.SetName("old")
	historical.SetUID("old-event")
	_ = unstructured.SetNestedField(historical.Object, "old-uid", "involvedObject", "uid")
	set := mk("argoproj.io/v1alpha1", "ApplicationSet", "ns", "set", "set-uid", map[string]any{"spec": map[string]any{"generators": []any{map[string]any{"git": map[string]any{"token": "PRIVATE-GENERATOR"}}}}})
	s, c := argoSession(t, app, other, event, historical, set)
	out, err := s.ArgoCD(t.Context(), core.ScopeSel{Mode: core.ScopeOne, Name: "ns"})
	require.NoError(t, err)
	require.Len(t, out.Objects, 2)
	require.Len(t, out.Events, 2)
	raw, _ := json.Marshal(out)
	assert.NotContains(t, string(raw), "PRIVATE-")
	assert.NotContains(t, string(raw), "USER:")
	assert.Contains(t, string(raw), "https://git.invalid/repo")
	assert.Contains(t, string(raw), "ProgressDeadlineExceeded")
	var got core.ArgoObject
	for _, o := range out.Objects {
		if o.Kind == "Application" {
			got = o
		}
	}
	assert.Equal(t, "Degraded", got.Health)
	assert.Equal(t, "OutOfSync", got.Sync)
	assert.True(t, got.LocalDestination)
	require.Len(t, got.Sources, 1)
	assert.Equal(t, strings.Repeat("a", 40), got.Sources[0].ObservedRevision)
	for _, e := range out.Events {
		assert.Equal(t, e.Ref.UID == "argo-web", e.Openable)
	}
	for _, a := range c.Actions() {
		assert.Equal(t, "list", a.GetVerb())
		assert.Equal(t, "ns", a.GetNamespace())
		assert.NotContains(t, []string{"secrets", "configmaps"}, a.GetResource().Resource)
	}
	c.ClearActions()
	out, err = s.ArgoCD(t.Context(), core.ScopeSel{Mode: core.ScopeSome, Names: []string{}})
	require.NoError(t, err)
	assert.Empty(t, c.Actions())
	assert.Empty(t, out.Objects)
}
func TestArgoCoverageDistinguishesMissingDeniedNamespaceAndEmpty(t *testing.T) {
	s, c := argoSession(t)
	c.PrependReactor("list", "applications", func(a k8stesting.Action) (bool, runtime.Object, error) {
		switch a.GetNamespace() {
		case "missing":
			return true, nil, apierrors.NewNotFound(schema.GroupResource{Group: argoGroup, Resource: "applications"}, "")
		case "denied":
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: argoGroup, Resource: "applications"}, "", errors.New("PRIVATE-TEXT"))
		case "gone":
			return true, nil, apierrors.NewNotFound(schema.GroupResource{Resource: "namespaces"}, "gone")
		}
		return false, nil, nil
	})
	for ns, want := range map[string]string{"missing": "missing", "denied": "forbidden", "gone": "not_found", "empty": "ready"} {
		out, err := s.ArgoCD(t.Context(), core.ScopeSel{Mode: core.ScopeOne, Name: ns})
		require.NoError(t, err)
		assert.Equal(t, want, out.Coverage[0].State)
		raw, _ := json.Marshal(out)
		assert.NotContains(t, string(raw), "PRIVATE-TEXT")
	}
}
func TestArgoPagingBudgetsAndCancelledRequests(t *testing.T) {
	s, c := argoSession(t, argoFixture("ns", "web"))
	calls := 0
	c.PrependReactor("list", "applications", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		calls++
		list := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*argoFixture("ns", "web")}}
		list.SetContinue("repeat")
		return true, list, nil
	})
	out, err := s.ArgoCD(t.Context(), core.ScopeSel{Mode: core.ScopeOne, Name: "ns"})
	require.NoError(t, err)
	assert.True(t, out.Truncated)
	assert.LessOrEqual(t, calls, 2)
	assert.Len(t, out.Objects, 1)
	c.ClearActions()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = s.ArgoCD(ctx, core.ScopeSel{Mode: core.ScopeAll})
	require.Error(t, err)
	assert.Empty(t, c.Actions())
}
func TestArgoUnknownHealthAndDestinationAreNotGuessed(t *testing.T) {
	s, _ := argoSession(t)
	app := argoFixture("ns", "web")
	_ = unstructured.SetNestedField(app.Object, "Running", "status", "health", "status")
	_ = unstructured.SetNestedField(app.Object, "Healthy", "status", "sync", "status")
	_ = unstructured.SetNestedMap(app.Object, map[string]any{"name": "in-cluster", "namespace": "ns"}, "spec", "destination")
	got := s.argoObject(app, "Application")
	assert.False(t, got.LocalDestination)
	assert.Equal(t, "Unknown", got.Health)
	assert.Equal(t, "Unknown", got.Sync)
	_ = unstructured.SetNestedMap(app.Object, map[string]any{"server": "https://remote.invalid", "namespace": "ns"}, "spec", "destination")
	assert.False(t, s.argoObject(app, "Application").LocalDestination)
	_ = unstructured.SetNestedMap(app.Object, map[string]any{"server": s.conn.cfg.Host, "namespace": "ns"}, "spec", "destination")
	assert.True(t, s.argoObject(app, "Application").LocalDestination)
	assert.Equal(t, "git.invalid:team/repo", argoURL("git@git.invalid:team/repo"))
	assert.True(t, len(argoText(strings.Repeat("界", 400))) <= 512)
}
func TestArgoActionsAreExactAndMarkedUIOnly(t *testing.T) {
	for _, a := range argoActions {
		assert.True(t, a.NoAgents)
	}
	good := apiResource{Group: argoGroup, Version: "v1alpha1", Resource: "applications", Kind: "Application", Namespaced: true, Verbs: []string{"list", "watch", "get", "patch"}}
	require.Len(t, discoveredActions(good), 4)
	for _, change := range []func(*apiResource){func(r *apiResource) { r.Group = "other.io" }, func(r *apiResource) { r.Version = "v2" }, func(r *apiResource) { r.Kind = "Other" }, func(r *apiResource) { r.Namespaced = false }, func(r *apiResource) { r.Verbs = []string{"get"} }} {
		r := good
		change(&r)
		assert.Empty(t, discoveredActions(r))
	}
}
func TestArgoWritesPinRevisionPruneAndRefreshWithUIDVersion(t *testing.T) {
	for _, action := range argoActions {
		t.Run(action.ID, func(t *testing.T) {
			s, c := argoSession(t, argoFixture("ns", "web"))
			plan := prepare(t, s, argoRef, action.ID, core.ActionParams{})
			require.Nil(t, plan.Unavailable)
			assert.Contains(t, text(plan), "kubernetes patch permission")
			assert.Empty(t, writes(c))
			result, err := s.RunAction(t.Context(), provider.ActionRun{Ref: argoRef, Action: action.ID, Expect: plan.Expect})
			require.NoError(t, err)
			assert.Equal(t, core.OutcomeDone, result.Outcome)
			ws := writes(c)
			require.Len(t, ws, 1)
			var p map[string]any
			require.NoError(t, json.Unmarshal(ws[0].(k8stesting.PatchAction).GetPatch(), &p))
			meta := p["metadata"].(map[string]any)
			assert.Equal(t, "argo-web", meta["uid"])
			assert.Equal(t, "7", meta["resourceVersion"])
			if argoSyncAction(action.ID) {
				sync := p["operation"].(map[string]any)["sync"].(map[string]any)
				assert.Equal(t, strings.Repeat("a", 40), sync["revision"])
				assert.Equal(t, action.ID == "argo-sync-prune", sync["prune"])
				assert.Contains(t, text(plan), "hooks")
				assert.True(t, plan.Destructive)
			} else {
				mode := "normal"
				if action.ID == "argo-refresh-hard" {
					mode = "hard"
				}
				assert.Equal(t, mode, meta["annotations"].(map[string]any)[argoRefreshKey])
			}
		})
	}
}
func TestArgoChangesInvalidateReviewAndNoUnknownWriteIsRetried(t *testing.T) {
	for _, mutate := range []func(*unstructured.Unstructured){func(u *unstructured.Unstructured) {
		_ = unstructured.SetNestedField(u.Object, "new-project", "spec", "project")
	}, func(u *unstructured.Unstructured) {
		_ = unstructured.SetNestedField(u.Object, strings.Repeat("b", 40), "status", "sync", "revision")
	}, func(u *unstructured.Unstructured) { u.SetUID("new-uid") }} {
		s, c := argoSession(t, argoFixture("ns", "web"))
		plan := prepare(t, s, argoRef, "argo-sync", core.ActionParams{})
		now, err := c.Resource(argoApplicationGVR).Namespace("ns").Get(t.Context(), "web", metav1.GetOptions{})
		require.NoError(t, err)
		mutate(now)
		_, err = c.Resource(argoApplicationGVR).Namespace("ns").Update(t.Context(), now, metav1.UpdateOptions{})
		require.NoError(t, err)
		c.ClearActions()
		_, err = s.RunAction(t.Context(), provider.ActionRun{Ref: argoRef, Action: "argo-sync", Expect: plan.Expect})
		require.Error(t, err)
		assert.Empty(t, writes(c))
	}
	for _, code := range []int{409, 500} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			ws := &wireServer{obj: argoFixture("ns", "web"), reply: func(_ int, w http.ResponseWriter) (bool, bool) {
				if code == 409 {
					statusReply(w, 409, "Conflict", false)
				} else {
					statusReply(w, 500, "InternalError", true)
				}
				return true, true
			}}
			s := wireSession(t, ws)
			installArgoKind(s)
			def := s.kind(argoRef.Kind)
			_, err := s.RunAction(t.Context(), provider.ActionRun{Ref: argoRef, Action: "argo-refresh", Expect: actionExpect(def, "argo-refresh", core.ActionParams{}, ws.obj)})
			require.Error(t, err)
			assert.Equal(t, 1, ws.writes)
		})
	}
}
func TestArgoUnavailableForBusySkippedAndStaleOrSymbolicComparison(t *testing.T) {
	for name, change := range map[string]func(*unstructured.Unstructured){"busy": func(u *unstructured.Unstructured) { u.Object["operation"] = map[string]any{} }, "skip": func(u *unstructured.Unstructured) {
		u.SetAnnotations(map[string]string{"argocd.argoproj.io/skip-reconcile": "true"})
	}, "refresh": func(u *unstructured.Unstructured) { u.SetAnnotations(map[string]string{argoRefreshKey: "hard"}) }, "stale": func(u *unstructured.Unstructured) {
		_ = unstructured.SetNestedField(u.Object, "new", "spec", "source", "path")
	}, "branch": func(u *unstructured.Unstructured) {
		_ = unstructured.SetNestedField(u.Object, "HEAD", "status", "sync", "revision")
	}, "unknown": func(u *unstructured.Unstructured) {
		_ = unstructured.SetNestedField(u.Object, "Unknown", "status", "sync", "status")
	}} {
		t.Run(name, func(t *testing.T) {
			app := argoFixture("ns", "web")
			change(app)
			s, c := argoSession(t, app)
			plan := prepare(t, s, argoRef, "argo-sync", core.ActionParams{})
			require.NotNil(t, plan.Unavailable)
			_, err := s.RunAction(t.Context(), provider.ActionRun{Ref: argoRef, Action: "argo-sync", Expect: plan.Expect})
			require.Error(t, err)
			assert.Empty(t, writes(c))
		})
	}
}

func TestArgoMultiSourceRevisionsAndTemplateChangesAreBoundToReview(t *testing.T) {
	app := argoFixture("ns", "web")
	git := fieldAt(app.Object, "spec", "source")
	chart := map[string]any{"repoURL": "https://charts.invalid", "chart": "redis", "targetRevision": "1.*"}
	sources := []any{git, chart}
	_ = unstructured.SetNestedSlice(app.Object, sources, "spec", "sources")
	_ = unstructured.SetNestedSlice(app.Object, runtime.DeepCopyJSONValue(sources).([]any), "status", "sync", "comparedTo", "sources")
	_ = unstructured.SetNestedStringSlice(app.Object, []string{strings.Repeat("a", 40), "1.2.3"}, "status", "sync", "revisions")
	s, c := argoSession(t, app)
	plan := prepare(t, s, argoRef, "argo-sync", core.ActionParams{})
	require.Nil(t, plan.Unavailable)
	_, err := s.RunAction(t.Context(), provider.ActionRun{Ref: argoRef, Action: "argo-sync", Expect: plan.Expect})
	require.NoError(t, err)
	ws := writes(c)
	require.Len(t, ws, 1)
	var p map[string]any
	require.NoError(t, json.Unmarshal(ws[0].(k8stesting.PatchAction).GetPatch(), &p))
	sync := p["operation"].(map[string]any)["sync"].(map[string]any)
	assert.Nil(t, sync["revision"])
	assert.Equal(t, []any{strings.Repeat("a", 40), "1.2.3"}, sync["revisions"])
	for _, revisions := range [][]string{{strings.Repeat("a", 40)}, {strings.Repeat("a", 40), ">=1.0"}, {"main", "1.2.3"}} {
		u := app.DeepCopy()
		_ = unstructured.SetNestedStringSlice(u.Object, revisions, "status", "sync", "revisions")
		_, ok := argoRevisions(u)
		assert.False(t, ok)
	}
	malformed := app.DeepCopy()
	unstructured.RemoveNestedField(malformed.Object, "spec", "sources")
	_ = unstructured.SetNestedField(malformed.Object, "bad", "spec", "source")
	_ = unstructured.SetNestedField(malformed.Object, "bad", "status", "sync", "comparedTo", "source")
	assert.NotPanics(t, func() { _, ok := argoRevisions(malformed); assert.False(t, ok) })
}
func TestArgoProjectionBoundsNestedCollections(t *testing.T) {
	s, _ := argoSession(t)
	app := argoFixture("ns", "web")
	resources := []any{}
	for i := 0; i < 501; i++ {
		resources = append(resources, map[string]any{"name": "api", "kind": "Pod", "namespace": "ns"})
	}
	_ = unstructured.SetNestedSlice(app.Object, resources, "status", "resources")
	got := s.argoObject(app, "Application")
	assert.True(t, got.Truncated)
	assert.Len(t, got.Resources, 500)
}

func TestArgoHTTPPaginationUsesOnlyExplicitNamespace(t *testing.T) {
	var mu sync.Mutex
	paths := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if !strings.Contains(r.URL.Path, "/namespaces/blue/") {
			http.Error(w, "outside scope", http.StatusForbidden)
			return
		}
		assert.Equal(t, "50", r.URL.Query().Get("limit"))
		resource := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		kind := map[string]string{"applications": "ApplicationList", "applicationsets": "ApplicationSetList", "appprojects": "AppProjectList", "events": "EventList", "services": "ServiceList"}[resource]
		list := &unstructured.UnstructuredList{}
		list.SetKind(kind)
		list.SetAPIVersion("argoproj.io/v1alpha1")
		list.Items = []unstructured.Unstructured{}
		if resource == "services" {
			assert.Equal(t, "app.kubernetes.io/name=argocd-server", r.URL.Query().Get("labelSelector"))
		}
		if resource == "events" || resource == "services" {
			list.SetAPIVersion("v1")
		}
		if resource == "applications" {
			if r.URL.Query().Get("continue") == "" {
				list.Items = []unstructured.Unstructured{*argoFixture("blue", "a")}
				list.SetContinue("page-2")
			} else {
				assert.Equal(t, "page-2", r.URL.Query().Get("continue"))
				list.Items = []unstructured.Unstructured{*argoFixture("blue", "b")}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(list)
	}))
	defer server.Close()
	client, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
	require.NoError(t, err)
	s := newSession("ctx", "hash", client, false)
	defer s.Close()
	installArgoKind(s)
	out, err := s.ArgoCD(t.Context(), core.ScopeSel{Mode: core.ScopeOne, Name: "blue"})
	require.NoError(t, err)
	require.Len(t, out.Objects, 2)
	assert.False(t, out.Truncated)
	mu.Lock()
	defer mu.Unlock()
	assert.Len(t, paths, 6)
	for _, p := range paths {
		assert.Contains(t, p, "/namespaces/blue/")
		assert.NotContains(t, p, "/secrets")
	}
}

func TestArgoSanitizesCompleteURLsBeforeTruncating(t *testing.T) {
	text := "Failed https://user:" + strings.Repeat("1", 600) + "@git.invalid/path?token=PRIVATE-TOKEN"
	got := argoText(text)
	assert.NotContains(t, got, "1111")
	assert.NotContains(t, got, "PRIVATE-TOKEN")
	assert.Contains(t, got, "https://git.invalid/path")
	assert.LessOrEqual(t, len(got), 512)
	assert.Equal(t, "[invalid URL]", argoURL("https:user:password@host"))
}

func TestArgoServerDiscoveryUsesExactLabelAndScope(t *testing.T) {
	labeled := mk("v1", "Service", "ns", "ui", "ui-uid", map[string]any{"metadata": map[string]any{"labels": map[string]any{"app.kubernetes.io/name": "argocd-server"}}})
	unrelated := mk("v1", "Service", "ns", "argocd-server", "other-uid", nil)
	outside := labeled.DeepCopy()
	outside.SetNamespace("outside")
	outside.SetUID("outside-uid")
	s, c := argoSession(t, labeled, unrelated, outside)
	out, err := s.ArgoCD(t.Context(), core.ScopeSel{Mode: core.ScopeOne, Name: "ns"})
	require.NoError(t, err)
	require.Len(t, out.Servers, 1)
	assert.Equal(t, "ui-uid", out.Servers[0].UID)
	for _, a := range c.Actions() {
		if a.GetResource().Resource == "services" {
			assert.Equal(t, "app.kubernetes.io/name=argocd-server", a.(k8stesting.ListAction).GetListRestrictions().Labels.String())
			assert.Equal(t, "ns", a.GetNamespace())
		}
	}
}

func TestArgoComparisonAllowsTypedDefaultsButNotOpaqueValueChanges(t *testing.T) {
	app := argoFixture("ns", "web")
	_ = unstructured.SetNestedField(app.Object, "", "spec", "source", "targetRevision")
	_ = unstructured.SetNestedField(app.Object, "HEAD", "status", "sync", "comparedTo", "source", "targetRevision")
	_ = unstructured.SetNestedField(app.Object, false, "spec", "source", "helm", "passCredentials")
	_ = unstructured.SetNestedSlice(app.Object, []any{map[string]any{"name": "enabled", "value": "", "forceString": false}}, "spec", "source", "helm", "parameters")
	_ = unstructured.SetNestedSlice(app.Object, []any{map[string]any{"name": "enabled"}}, "status", "sync", "comparedTo", "source", "helm", "parameters")
	_ = unstructured.SetNestedField(app.Object, "", "spec", "destination", "name")
	before := app.DeepCopy()
	_, ok := argoRevisions(app)
	require.True(t, ok)
	assert.Equal(t, before.Object, app.Object, "comparison must not normalize the live spec in place")
	_ = unstructured.SetNestedSlice(app.Object, []any{map[string]any{"group": "apps", "kind": "Deployment", "jsonPointers": []any{"/spec/replicas"}}}, "spec", "ignoreDifferences")
	_, ok = argoRevisions(app)
	assert.False(t, ok, "a changed comparison policy requires refresh")
	unstructured.RemoveNestedField(app.Object, "spec", "ignoreDifferences")
	for _, path := range [][]string{{"helm", "valuesObject"}, {"plugin", "parameters"}} {
		next := app.DeepCopy()
		left := append([]string{"spec", "source"}, path...)
		right := append([]string{"status", "sync", "comparedTo", "source"}, path...)
		_ = unstructured.SetNestedField(next.Object, map[string]any{"enabled": false}, left...)
		_ = unstructured.SetNestedField(next.Object, map[string]any{"enabled": true}, right...)
		_, ok = argoRevisions(next)
		assert.False(t, ok)
	}
	unstructured.RemoveNestedField(app.Object, "spec", "destination")
	unstructured.RemoveNestedField(app.Object, "status", "sync", "comparedTo", "destination")
	_, ok = argoRevisions(app)
	assert.False(t, ok)
}
