package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/spk/spk-ocular/internal/krr"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kt "k8s.io/client-go/testing"
)

func krrSnapshotSession(t *testing.T) (*session, *dynamicfake.FakeDynamicClient) {
	t.Helper()
	ns := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": "tools", "uid": "ns-uid"}}}
	sa := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": map[string]any{"name": "default", "namespace": "tools", "uid": "default-uid"}}}
	kinds := map[schema.GroupVersionResource]string{krrAccounts: "ServiceAccountList", namespaceGV: "NamespaceList", podsKind.gvr: "PodList", krrJobs: "JobList", {Version: "v1", Resource: "services"}: "ServiceList", {Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"}: "HorizontalPodAutoscalerList"}
	for _, r := range []string{"deployments", "statefulsets", "daemonsets"} {
		kinds[schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: r}] = r + "List"
	}
	c := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(kruntime.NewScheme(), kinds, ns, sa)
	ssarAnswer(c, true, nil)
	s := newSession("fixture", "hash", c, false)
	t.Cleanup(s.Close)
	return s, c
}

func snapshotDeployment(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"name": name, "namespace": "tools", "uid": "uid-" + name, "annotations": map[string]any{"secret": "PRIVATE-ANNOTATION"}}, "spec": map[string]any{"selector": map[string]any{"matchLabels": map[string]any{"app": "app"}}, "template": map[string]any{"spec": map[string]any{"containers": []any{map[string]any{"name": "main", "image": "PRIVATE-IMAGE", "env": []any{map[string]any{"name": "SECRET", "value": "PRIVATE-ENV"}}, "resources": map[string]any{"requests": map[string]any{"cpu": "500m", "memory": "100Mi"}}}}}}}}}
}

func TestKRRAutomaticSnapshotProjectsAndMatchesPods(t *testing.T) {
	s, c := krrSnapshotSession(t)
	require.NoError(t, c.Tracker().Create(schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, snapshotDeployment("app"), "tools"))
	for _, name := range []string{"matching", "other"} {
		pod := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": name, "namespace": "tools", "uid": "uid-" + name, "labels": map[string]any{"app": map[string]string{"matching": "app", "other": "other"}[name]}}, "spec": map[string]any{"containers": []any{map[string]any{"name": "PRIVATE-CONTAINER", "env": []any{map[string]any{"value": "PRIVATE-ENV"}}}}}}}
		require.NoError(t, c.Tracker().Create(podsKind.gvr, pod, "tools"))
	}
	snapshot, e := s.krrSnapshot(t.Context(), "tools")
	require.NoError(t, e)
	require.NotContains(t, snapshot, "PRIVATE-")
	require.Contains(t, snapshot, "500m")
	var projected krrSnapshot
	require.NoError(t, json.Unmarshal([]byte(snapshot), &projected))
	require.Equal(t, []string{"matching"}, projected.Pods["Deployment/app"])
	o := krr.RunOptions{Automatic: true, Namespace: "tools", Source: "http://metrics.tools.svc:9090"}
	r, e := s.ReviewKRR(t.Context(), o)
	require.NoError(t, e)
	require.Equal(t, "default", r.Options.ServiceAccount)
	h, e := s.PrepareKRR(t.Context(), r, &krrTestJournal{})
	require.NoError(t, e)
	spec := h.(*krrExecution).g.podSpec()
	raw, e := json.Marshal(spec)
	require.NoError(t, e)
	require.NotContains(t, string(raw), "serviceAccountToken")
	require.NotContains(t, string(raw), "kube-root-ca")
	require.NotContains(t, string(raw), "PRIVATE-")
	require.Equal(t, false, spec["automountServiceAccountToken"])
	containers := spec["containers"].([]any)
	command := containers[0].(map[string]any)["command"].([]any)
	require.Equal(t, snapshot, command[len(command)-1])
	for _, a := range c.Actions() {
		if a.GetResource() != ssarGVR {
			require.NotContains(t, []string{"create", "update", "patch", "delete"}, a.GetVerb())
		}
	}
}

func TestKRRSnapshotDenialTruncationMalformedAndBoundsFailClosed(t *testing.T) {
	for _, mode := range []string{"denied", "continuation", "malformed", "oversize", "duplicates", "snapshot-size", "wrong-scope", "missing-uid"} {
		t.Run(mode, func(t *testing.T) {
			s, c := krrSnapshotSession(t)
			c.PrependReactor("list", "deployments", func(kt.Action) (bool, kruntime.Object, error) {
				if mode == "denied" {
					return true, nil, errors.New("PRIVATE-DENIED")
				}
				item := snapshotDeployment("app")
				list := &unstructured.UnstructuredList{Object: map[string]any{"apiVersion": "apps/v1", "kind": "DeploymentList"}, Items: []unstructured.Unstructured{*item}}
				switch mode {
				case "continuation":
					list.SetContinue("repeated")
				case "malformed":
					_ = unstructured.SetNestedField(list.Items[0].Object, "invalid", "spec", "template", "spec", "containers")
				case "oversize":
					_ = unstructured.SetNestedField(list.Items[0].Object, strings.Repeat("x", krrSnapshotLimit), "spec", "template", "spec", "containers", "invalid")
					list.Items[0].SetAnnotations(map[string]string{"large": strings.Repeat("x", 1<<20)})
				case "snapshot-size":
					containers := []any{}
					for i := 0; i < 1500; i++ {
						containers = append(containers, map[string]any{"name": fmt.Sprintf("container-%d", i), "resources": map[string]any{"requests": map[string]any{"cpu": "100m", "memory": "100Mi"}}})
					}
					require.NoError(t, unstructured.SetNestedSlice(list.Items[0].Object, containers, "spec", "template", "spec", "containers"))
				case "wrong-scope":
					list.Items[0].SetNamespace("other")
				case "missing-uid":
					list.Items[0].SetUID("")
				case "duplicates":
					list.Items = append(list.Items, *item)
				}
				return true, list, nil
			})
			_, e := s.krrSnapshot(t.Context(), "tools")
			require.Error(t, e)
		})
	}
}

func TestKRRAutomaticUsesExistingJournalAndCleanup(t *testing.T) {
	s, c := krrSnapshotSession(t)
	j := &krrTestJournal{}
	r, e := s.ReviewKRR(t.Context(), krr.RunOptions{Automatic: true, Namespace: "tools", Source: "http://metrics.tools.svc:9090"})
	require.NoError(t, e)
	execution, e := s.PrepareKRR(t.Context(), r, j)
	require.NoError(t, e)
	// Existing protocol helper verifies intent-before-create, UID pin and resume.
	krrReady(c, j, "")
	s.logs = func(_ context.Context, _ podLogRequest) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("2026-10-22T00:00:00Z " + strings.TrimSpace(string(krrProtocolReport(t, "tools"))) + "\n")), nil
	}
	_, e = execution.Execute(t.Context())
	require.NoError(t, e)
	require.True(t, j.clean)
	require.True(t, j.finished)
	require.Equal(t, "default-uid", j.record.ServiceAccountUID)
}

func TestKRRSetupCandidatesAreBoundedAndNeverWrite(t *testing.T) {
	s, c := krrSnapshotSession(t)
	for _, name := range []string{"prometheus", "prometheus-operator", "prometheus-alertmanager", "prometheus-adapter", "other"} {
		item := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Service", "metadata": map[string]any{"name": name, "namespace": "monitoring", "uid": "uid-" + name}, "spec": map[string]any{"ports": []any{map[string]any{"port": int64(9090), "name": "web"}}}}}
		require.NoError(t, c.Tracker().Create(schema.GroupVersionResource{Version: "v1", Resource: "services"}, item, "monitoring"))
	}
	setup, e := s.SetupKRR(t.Context())
	require.NoError(t, e)
	require.True(t, setup.Complete)
	require.Equal(t, []string{"http://prometheus.monitoring.svc:9090"}, setup.Sources)
	c.PrependReactor("list", "services", func(kt.Action) (bool, kruntime.Object, error) { return true, nil, errors.New("PRIVATE-DENIED") })
	setup, e = s.SetupKRR(t.Context())
	require.NoError(t, e)
	require.False(t, setup.Complete)
	for _, a := range c.Actions() {
		require.Equal(t, "list", a.GetVerb())
	}
}

func TestKRRSnapshotPreservesHPAGuardFieldsAndExcludesUnrelatedMetrics(t *testing.T) {
	s, c := krrSnapshotSession(t)
	hpa := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "autoscaling/v2", "kind": "HorizontalPodAutoscaler", "metadata": map[string]any{"name": "hpa", "namespace": "tools", "uid": "hpa-uid", "annotations": map[string]any{"private": "PRIVATE-ANNOTATION"}}, "spec": map[string]any{"scaleTargetRef": map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "app"}, "maxReplicas": int64(3), "metrics": []any{map[string]any{"type": "Resource", "resource": map[string]any{"name": "cpu", "target": map[string]any{"type": "Utilization", "averageUtilization": int64(80)}}}, map[string]any{"type": "External", "external": map[string]any{"metric": map[string]any{"name": "PRIVATE-METRIC"}}}}}, "status": map[string]any{"currentReplicas": int64(1), "desiredReplicas": int64(1)}}}
	require.NoError(t, c.Tracker().Create(schema.GroupVersionResource{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"}, hpa, "tools"))
	raw, e := s.krrSnapshot(t.Context(), "tools")
	require.NoError(t, e)
	require.NotContains(t, raw, "PRIVATE-")
	var snapshot krrSnapshot
	require.NoError(t, json.Unmarshal([]byte(raw), &snapshot))
	hpaList := snapshot.API["/apis/autoscaling/v2/namespaces/{namespace}/horizontalpodautoscalers"].(map[string]any)
	items := hpaList["items"].([]any)
	require.Len(t, items, 1)
	metrics, found, e := unstructured.NestedSlice(items[0].(map[string]any), "spec", "metrics")
	require.NoError(t, e)
	require.True(t, found)
	require.Len(t, metrics, 1)
	require.Contains(t, raw, `"averageUtilization":80`)
	require.NotContains(t, raw, "minReplicas")
}
