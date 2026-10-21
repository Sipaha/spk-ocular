package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/spk/spk-ocular/internal/helm"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ktesting "k8s.io/client-go/testing"
)

func TestHelmCurrentHookUIDOwnerEventsAndNoDependencyReads(t *testing.T) {
	job := mk("batch/v1", "Job", "blue", "check", "current-job", map[string]any{"status": map[string]any{"failed": int64(1)}})
	job.SetResourceVersion("7")
	pod := diagnosisPod()
	pod.SetResourceVersion("8")
	pod.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "batch/v1", Kind: "Job", Name: "check", UID: "current-job", Controller: ptrTrue()}})
	slice(pod.Object, "spec", "containers")[0]["envFrom"] = []any{map[string]any{"secretRef": map[string]any{"name": "do-not-read"}}}
	wrong := pod.DeepCopy()
	wrong.SetName("old-execution")
	wrong.SetUID("old-pod")
	wrong.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "batch/v1", Kind: "Job", UID: "historical-job", Controller: ptrTrue()}})
	client := fullFake(job, pod, wrong)
	s := newSession("t", "h", client, false)
	defer s.Close()
	h := helm.HookRecord{Name: "check", Namespace: "blue", Kind: "Job", APIVersion: "batch/v1", Coverage: "supported"}
	out, err := s.HelmHookCurrent(t.Context(), h)
	require.NoError(t, err)
	require.Equal(t, "current-job", out.Ref.UID)
	raw, _ := json.Marshal(out)
	require.Contains(t, string(raw), "p-uid")
	require.NotContains(t, string(raw), "old-pod")
	require.NotContains(t, string(raw), "do-not-read")
	for _, a := range client.Actions() {
		require.NotEqual(t, "secrets", a.GetResource().Resource)
		require.NotEqual(t, "configmaps", a.GetResource().Resource)
		require.Contains(t, []string{"get", "list"}, a.GetVerb())
	}
	gets := 0
	client.PrependReactor("get", "jobs", func(_ ktesting.Action) (bool, runtime.Object, error) {
		gets++
		v := job.DeepCopy()
		if gets > 1 {
			v.SetUID("replacement")
		}
		return true, v, nil
	})
	_, err = s.HelmHookCurrent(t.Context(), h)
	require.ErrorContains(t, err, "changed")
	client.PrependReactor("get", "jobs", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewNotFound(schema.GroupResource{Group: "batch", Resource: "jobs"}, "check")
	})
	out, err = s.HelmHookCurrent(t.Context(), h)
	require.NoError(t, err)
	require.Empty(t, out.Ref.UID)
	require.Len(t, out.Coverage, 1)
	require.NotEqual(t, "ready", out.Coverage[0].State)
}
func TestHelmHookDeniedPartialCancellationAndUnsupported(t *testing.T) {
	p := diagnosisPod()
	p.SetResourceVersion("1")
	c := fullFake(p)
	s := newSession("t", "h", c, false)
	defer s.Close()
	c.PrependReactor("list", "events", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "events"}, "", errors.New("PRIVATE"))
	})
	out, err := s.HelmHookCurrent(t.Context(), helm.HookRecord{Name: "p", Namespace: "blue", Kind: "Pod", APIVersion: "v1", Coverage: "supported"})
	require.NoError(t, err)
	raw, _ := json.Marshal(out)
	require.NotContains(t, string(raw), "PRIVATE")
	require.Contains(t, string(raw), "forbidden")
	_, err = s.HelmHookCurrent(t.Context(), helm.HookRecord{Name: "p", Namespace: "blue", Kind: "Secret", APIVersion: "v1", Coverage: "supported"})
	require.Error(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, err = s.HelmReadContext(ctx)
	require.Error(t, err)
}
func TestHelmFluxOnlyReportedLocalHistoryAndSingleNamespace(t *testing.T) {
	makeHR := func(name string) runtime.Object {
		return mk("helm.toolkit.fluxcd.io/v2", "HelmRelease", "controllers", name, "uid-"+name, map[string]any{"metadata": map[string]any{"generation": int64(2)}, "spec": map[string]any{"chartRef": map[string]any{"kind": "OCIRepository", "name": "source"}, "valuesFrom": []any{map[string]any{"kind": "Secret", "name": "PRIVATE"}}}, "status": map[string]any{"observedGeneration": int64(2), "storageNamespace": "storage", "history": []any{map[string]any{"name": "demo", "namespace": "workloads", "version": int64(3)}}}})
	}
	s, c := fluxSession(t, makeHR("one"))
	r := helm.Release{Name: "demo", Namespace: "workloads", Revision: 3}
	out, err := s.HelmFluxContext(t.Context(), "controllers", "storage", r)
	require.NoError(t, err)
	require.Equal(t, "ready", out.Coverage)
	require.Len(t, out.Candidates, 1)
	require.Equal(t, "reported_match", out.Candidates[0].State)
	require.Equal(t, "OCIRepository/source", out.Candidates[0].Source)
	for _, a := range c.Actions() {
		require.Equal(t, "controllers", a.GetNamespace())
		require.Equal(t, "helmreleases", a.GetResource().Resource)
		require.Equal(t, "list", a.GetVerb())
	}
	gvr := schema.GroupVersionResource{Group: "helm.toolkit.fluxcd.io", Version: "v2", Resource: "helmreleases"}
	u, err := c.Resource(gvr).Namespace("controllers").Get(t.Context(), "one", metav1.GetOptions{})
	require.NoError(t, err)
	u.Object["spec"].(map[string]any)["kubeConfig"] = map[string]any{"secretRef": map[string]any{"name": "PRIVATE"}}
	_, err = c.Resource(gvr).Namespace("controllers").Update(t.Context(), u, metav1.UpdateOptions{})
	require.NoError(t, err)
	out, err = s.HelmFluxContext(t.Context(), "controllers", "storage", r)
	require.NoError(t, err)
	require.Equal(t, "remote_unverified", out.Candidates[0].State)
	out, err = s.HelmFluxContext(t.Context(), "controllers", "wrong-storage", r)
	require.NoError(t, err)
	require.Empty(t, out.Candidates)
	r.Revision = 4
	out, err = s.HelmFluxContext(t.Context(), "controllers", "storage", r)
	require.NoError(t, err)
	require.Empty(t, out.Candidates)
	r.Revision = 3
	second := makeHR("two")
	_, err = c.Resource(gvr).Namespace("controllers").Create(t.Context(), second.(*unstructured.Unstructured), metav1.CreateOptions{})
	require.NoError(t, err)
	out, err = s.HelmFluxContext(t.Context(), "controllers", "storage", r)
	require.NoError(t, err)
	require.Len(t, out.Candidates, 2)
	require.Equal(t, "ambiguous", out.Candidates[0].State)
	raw, _ := json.Marshal(out)
	require.NotContains(t, string(raw), "PRIVATE")
}

func TestHelmRuntimeListsRespectLimitsEvenWhenServerIgnoresThem(t *testing.T) {
	p := diagnosisPod()
	p.SetResourceVersion("1")
	c := fullFake(p)
	s := newSession("t", "h", c, false)
	defer s.Close()
	calls := 0
	c.PrependReactor("list", "events", func(a ktesting.Action) (bool, runtime.Object, error) {
		calls++
		require.EqualValues(t, 32, a.(interface{ GetListOptions() metav1.ListOptions }).GetListOptions().Limit)
		list := &unstructured.UnstructuredList{}
		for i := 0; i < 129; i++ {
			u := mk("v1", "Event", "blue", "e", "event", map[string]any{"involvedObject": map[string]any{"uid": "p-uid", "name": "p", "namespace": "blue"}, "reason": "Warning", "message": "bounded"})
			list.Items = append(list.Items, *u)
		}
		return true, list, nil
	})
	out, err := s.HelmHookCurrent(t.Context(), helm.HookRecord{Name: "p", Namespace: "blue", Kind: "Pod", APIVersion: "v1", Coverage: "supported"})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.True(t, out.Truncated)
	require.LessOrEqual(t, len(out.Evidence), 256)
}
