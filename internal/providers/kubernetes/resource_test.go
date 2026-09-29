package kubernetes

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

func fullFake(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	lists := map[schema.GroupVersionResource]string{}
	for _, d := range allKinds.list {
		lists[d.gvr] = kindOf(d) + "List"
	}
	lists[podMetricsGVR] = "PodMetricsList"
	lists[nodeMetricsGVR] = "NodeMetricsList"
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), lists, objs...)
}

func mk(apiVersion, kind, ns, name, uid string, body map[string]any) *unstructured.Unstructured {
	o := map[string]any{"apiVersion": apiVersion, "kind": kind,
		"metadata": map[string]any{"name": name, "namespace": ns, "uid": uid, "creationTimestamp": "2026-09-29T10:00:00Z"}}
	for k, v := range body {
		if k == "metadata" {
			for mk, mv := range v.(map[string]any) {
				o["metadata"].(map[string]any)[mk] = mv
			}
			continue
		}
		o[k] = v
	}
	return &unstructured.Unstructured{Object: o}
}

func ownerRef(kind, name, uid string) map[string]any {
	return map[string]any{"apiVersion": "apps/v1", "kind": kind, "name": name, "uid": uid, "controller": true}
}

func TestGetCleansYAMLAndChecksUID(t *testing.T) {
	p := pod("web", "a", "uid-a")
	s := newSession("t", "h", fullFake(p), false)
	defer s.Close()
	r, err := s.Get(context.Background(), core.Ref{Kind: "pods", Scope: "web", Name: "a", UID: "uid-a"})
	require.NoError(t, err)
	assert.NotContains(t, r.YAML, "managedFields")
	assert.NotContains(t, r.YAML, "last-applied-configuration")
	assert.Contains(t, r.YAML, "image: nginx")
	assert.Equal(t, core.HealthOK, r.Health.State)
	assert.Contains(t, r.Facts, core.Detail{Key: "container app", Value: "nginx"})
	assert.Contains(t, r.Relations, core.Relation{Type: "runs-on", Ref: core.Ref{Provider: ProviderID, Target: "t", Kind: "nodes", Name: "node-1"}})

	_, err = s.Get(context.Background(), core.Ref{Kind: "pods", Scope: "web", Name: "a", UID: "old-uid"})
	var pe *provider.Error
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, provider.ClassGone, pe.Class, "a same-named replacement is not the clicked object")

	_, err = s.Get(context.Background(), core.Ref{Kind: "pods", Scope: "web", Name: "missing"})
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, provider.ClassNotFound, pe.Class)
}

func TestGetMasksSecretValues(t *testing.T) {
	sec := mk("v1", "Secret", "web", "creds", "s1", map[string]any{"type": "Opaque", "data": map[string]any{"password": "c2VjcmV0cGFzcw=="}})
	s := newSession("t", "h", fullFake(sec), false)
	defer s.Close()
	r, err := s.Get(context.Background(), core.Ref{Kind: "secrets", Scope: "web", Name: "creds"})
	require.NoError(t, err)
	assert.NotContains(t, r.YAML, "c2VjcmV0cGFzcw==")
	assert.Contains(t, r.YAML, "password: <10 bytes>") // "secretpass"
}

func TestDeploymentRelationsFollowControllerUID(t *testing.T) {
	sel := map[string]any{"matchLabels": map[string]any{"app": "web"}}
	dep := mk("apps/v1", "Deployment", "web", "web", "d1", map[string]any{"spec": map[string]any{"replicas": int64(2), "selector": sel}})
	rs := mk("apps/v1", "ReplicaSet", "web", "web-abc", "rs1", map[string]any{
		"metadata": map[string]any{"labels": map[string]any{"app": "web"}, "ownerReferences": []any{ownerRef("Deployment", "web", "d1")}},
		"spec":     map[string]any{"replicas": int64(2), "selector": sel}, "status": map[string]any{"replicas": int64(2)}})
	oldRS := mk("apps/v1", "ReplicaSet", "web", "web-old", "rs0", map[string]any{
		"metadata": map[string]any{"labels": map[string]any{"app": "web"}, "ownerReferences": []any{ownerRef("Deployment", "web", "d1")}},
		"spec":     map[string]any{"replicas": int64(0), "selector": sel}})
	own := func(name, uid, owner string) *unstructured.Unstructured {
		return mk("v1", "Pod", "web", name, uid, map[string]any{"metadata": map[string]any{
			"labels": map[string]any{"app": "web"}, "ownerReferences": []any{ownerRef("ReplicaSet", "x", owner)}}})
	}
	// Same labels, other controller: must not be claimed.
	stranger := own("stranger", "p9", "other-rs")
	s := newSession("t", "h", fullFake(dep, rs, oldRS, own("web-abc-1", "p1", "rs1"), own("web-abc-2", "p2", "rs1"), stranger), false)
	defer s.Close()
	r, err := s.Get(context.Background(), core.Ref{Kind: "apps/deployments", Scope: "web", Name: "web"})
	require.NoError(t, err)
	var names []string
	for _, rel := range r.Relations {
		names = append(names, rel.Type+":"+rel.Ref.Kind+"/"+rel.Ref.Name)
	}
	assert.ElementsMatch(t, []string{"owns:apps/replicasets/web-abc", "owns:pods/web-abc-1", "owns:pods/web-abc-2"}, names)
	assert.Empty(t, r.RelationsError)
}

func TestRelationErrorsDoNotFailTheResource(t *testing.T) {
	svc := mk("v1", "Service", "web", "web", "s1", map[string]any{"spec": map[string]any{"selector": map[string]any{"app": "web"}}})
	client := fullFake(svc)
	client.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", assert.AnError)
	})
	s := newSession("t", "h", client, false)
	defer s.Close()
	r, err := s.Get(context.Background(), core.Ref{Kind: "services", Scope: "web", Name: "web"})
	require.NoError(t, err)
	assert.Contains(t, r.RelationsError, "Pods")
	assert.Contains(t, r.RelationsError, "forbidden")
	assert.NotEmpty(t, r.YAML)
}

func TestSelectorlessServiceSelectsNothing(t *testing.T) {
	svc := mk("v1", "Service", "web", "ext", "s1", map[string]any{"spec": map[string]any{"type": "ExternalName", "externalName": "db.example"}})
	s := newSession("t", "h", fullFake(svc, pod("web", "a", "uid-a")), false)
	defer s.Close()
	r, err := s.Get(context.Background(), core.Ref{Kind: "services", Scope: "web", Name: "ext"})
	require.NoError(t, err)
	assert.Empty(t, r.Relations)
}

func podMetricsList(items ...*unstructured.Unstructured) *unstructured.UnstructuredList {
	l := &unstructured.UnstructuredList{Object: map[string]any{"apiVersion": "metrics.k8s.io/v1beta1", "kind": "PodMetricsList"}}
	for _, it := range items {
		l.Items = append(l.Items, *it)
	}
	return l
}

func podMetric(ns, name, at string) *unstructured.Unstructured {
	return mk("metrics.k8s.io/v1beta1", "PodMetrics", ns, name, "", map[string]any{
		"timestamp": at, "window": "15s",
		"containers": []any{
			map[string]any{"name": "app", "usage": map[string]any{"cpu": "250m", "memory": "64Mi"}},
			map[string]any{"name": "side", "usage": map[string]any{"cpu": "50m", "memory": "16Mi"}},
		}})
}

// metricsHarness: a session with a live pods view (so the cache knows the
// pods' incarnations) and a metrics list served from a reactor.
func metricsHarness(t *testing.T, objs []runtime.Object, samples func() *unstructured.UnstructuredList) (*session, *int) {
	t.Helper()
	client := fullFake(objs...)
	calls := 0
	client.PrependReactor("list", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetResource().Group != "metrics.k8s.io" {
			return false, nil, nil
		}
		calls++
		return true, samples(), nil
	})
	h := newHarness(t, client)
	h.until(h.open("pods", core.ScopeSel{Mode: core.ScopeOne, Name: "web"}), isReady)
	return h.sess, &calls
}

func TestMetricsAreKeyedByIncarnationAndCached(t *testing.T) {
	p := pod("web", "a", "uid-a") // created an hour ago
	s, calls := metricsHarness(t, []runtime.Object{p}, func() *unstructured.UnstructuredList {
		return podMetricsList(podMetric("web", "a", time.Now().UTC().Format(time.RFC3339)), podMetric("web", "unknown", time.Now().UTC().Format(time.RFC3339)))
	})
	q := provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "web"}}
	m, err := s.Metrics(context.Background(), q)
	require.NoError(t, err)
	require.Len(t, m.Values, 1, "a sample for an object no view observes is unknown")
	u := m.Values["uid-a"]
	assert.InDelta(t, 0.3, u.CPU, 1e-9)
	assert.InDelta(t, 80*1024*1024, u.Memory, 1)
	assert.False(t, u.At.IsZero())
	assert.Equal(t, "15s", m.Window)
	_, _ = s.Metrics(context.Background(), q)
	assert.Equal(t, 1, *calls, "reused within the TTL")
}

// Review 2026-09-29: a name-keyed sample of a deleted pod went to its
// same-named replacement.
func TestMetricsSampleOlderThanTheObjectIsNotAttributed(t *testing.T) {
	fresh := pod("web", "a", "uid-new", func(o map[string]any) {
		o["metadata"].(map[string]any)["creationTimestamp"] = time.Now().UTC().Format(time.RFC3339)
	})
	s, _ := metricsHarness(t, []runtime.Object{fresh}, func() *unstructured.UnstructuredList {
		return podMetricsList(podMetric("web", "a", time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)))
	})
	m, err := s.Metrics(context.Background(), provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "web"}})
	require.NoError(t, err)
	assert.Empty(t, m.Values, "the sample belongs to the previous incarnation")
}

func TestMetricsAbsenceIsPerResource(t *testing.T) {
	var pe *provider.Error
	_, err := newSession("t", "h", fullFake(), false).Metrics(context.Background(), provider.Query{Kind: "services", Scope: core.ScopeSel{Mode: core.ScopeAll}})
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, provider.ClassUnsupported, pe.Class)

	noNodes := fullFake()
	noNodes.PrependReactor("list", "nodes", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetResource().Group == "metrics.k8s.io" {
			return true, nil, apierrors.NewNotFound(schema.GroupResource{Group: "metrics.k8s.io", Resource: "nodes"}, "")
		}
		return false, nil, nil
	})
	noNodes.PrependReactor("list", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetResource().Group == "metrics.k8s.io" {
			return true, podMetricsList(), nil
		}
		return false, nil, nil
	})
	s := newSession("t", "h", noNodes, false)
	defer s.Close()
	_, err = s.Metrics(context.Background(), provider.Query{Kind: "nodes", Scope: core.ScopeSel{Mode: core.ScopeNone}})
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, provider.ClassUnsupported, pe.Class)
	_, err = s.Metrics(context.Background(), provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeAll}})
	assert.NoError(t, err, "a missing nodes resource says nothing about pods")
}

func TestMetricsCallerCanStopWaiting(t *testing.T) {
	block := make(chan struct{})
	client := fullFake()
	client.PrependReactor("list", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetResource().Group == "metrics.k8s.io" {
			<-block
			return true, podMetricsList(), nil
		}
		return false, nil, nil
	})
	s := newSession("t", "h", client, false)
	defer func() { close(block); s.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := s.Metrics(ctx, provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeAll}})
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

// pagingReactor serves pods in pages (continue tokens), like the apiserver.
func pagingReactor(pages [][]*unstructured.Unstructured) k8stesting.ReactionFunc {
	return func(a k8stesting.Action) (bool, runtime.Object, error) {
		cont := a.(k8stesting.ListActionImpl).GetListOptions().Continue
		i := 0
		if cont != "" {
			_, _ = fmt.Sscanf(cont, "page-%d", &i)
		}
		l := &unstructured.UnstructuredList{Object: map[string]any{"apiVersion": "v1", "kind": "PodList"}}
		for _, p := range pages[i] {
			l.Items = append(l.Items, *p)
		}
		if i+1 < len(pages) {
			l.SetContinue(fmt.Sprintf("page-%d", i+1))
		}
		return true, l, nil
	}
}

// Review 2026-09-29: relations read only the first page; the owned pods on
// page 2 were lost behind same-labelled pods of another controller.
func TestRelationsFollowPagination(t *testing.T) {
	sel := map[string]any{"matchLabels": map[string]any{"app": "web"}}
	sts := mk("apps/v1", "StatefulSet", "web", "db", "s1", map[string]any{"spec": map[string]any{"replicas": int64(1), "selector": sel}})
	podOf := func(name, uid, owner string) *unstructured.Unstructured {
		return mk("v1", "Pod", "web", name, uid, map[string]any{"metadata": map[string]any{
			"labels": map[string]any{"app": "web"}, "ownerReferences": []any{map[string]any{"apiVersion": "apps/v1", "kind": "StatefulSet", "name": "x", "uid": owner, "controller": true}}}})
	}
	var page1 []*unstructured.Unstructured
	for i := 0; i < 3; i++ {
		page1 = append(page1, podOf(fmt.Sprintf("other-%d", i), fmt.Sprintf("o%d", i), "someone-else"))
	}
	client := fullFake(sts)
	client.PrependReactor("list", "pods", pagingReactor([][]*unstructured.Unstructured{page1, {podOf("db-0", "p0", "s1")}}))
	s := newSession("t", "h", client, false)
	defer s.Close()
	r, err := s.Get(context.Background(), core.Ref{Kind: "apps/statefulsets", Scope: "web", Name: "db"})
	require.NoError(t, err)
	require.Len(t, r.Relations, 1)
	assert.Equal(t, "db-0", r.Relations[0].Ref.Name)
	assert.False(t, r.RelationsTruncated)
}

func TestRelationsAreCappedAndSayIt(t *testing.T) {
	svc := mk("v1", "Service", "web", "web", "s1", map[string]any{"spec": map[string]any{"selector": map[string]any{"app": "web"}}})
	var many []*unstructured.Unstructured
	for i := 0; i < maxRelated+50; i++ {
		many = append(many, mk("v1", "Pod", "web", fmt.Sprintf("p-%d", i), fmt.Sprintf("u%d", i), map[string]any{"metadata": map[string]any{"labels": map[string]any{"app": "web"}}}))
	}
	client := fullFake(svc)
	client.PrependReactor("list", "pods", pagingReactor([][]*unstructured.Unstructured{many[:150], many[150:]}))
	s := newSession("t", "h", client, false)
	defer s.Close()
	r, err := s.Get(context.Background(), core.Ref{Kind: "services", Scope: "web", Name: "web"})
	require.NoError(t, err)
	assert.Empty(t, r.RelationsError)
	assert.Len(t, r.Relations, maxRelated)
	assert.True(t, r.RelationsTruncated)
}
