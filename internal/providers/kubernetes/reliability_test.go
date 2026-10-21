package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	kt "k8s.io/client-go/testing"
	"strings"
	"testing"
	"time"
)

func reliabilityObject() *unstructured.Unstructured {
	u := mk("apps/v1", "Deployment", "blue", "web", "workload-uid", map[string]any{"spec": map[string]any{"replicas": int64(1), "template": map[string]any{"metadata": map[string]any{"labels": map[string]any{"app": "web"}}, "spec": map[string]any{"containers": []any{map[string]any{"name": "web", "env": []any{map[string]any{"value": "PRIVATE-ENV"}}, "readinessProbe": map[string]any{"httpGet": map[string]any{"path": "PRIVATE-PATH", "httpHeaders": []any{map[string]any{"value": "PRIVATE-HEADER"}}}}, "resources": map[string]any{"requests": map[string]any{"cpu": "100m", "memory": "64Mi"}}}}}}}})
	u.SetResourceVersion("7")
	u.SetGeneration(2)
	return u
}
func reliabilityRef() core.Ref {
	return core.Ref{Provider: ProviderID, Target: "t", Kind: "apps/deployments", Scope: "blue", Name: "web", UID: "workload-uid"}
}
func reliabilityFake(objs ...runtime.Object) *fake.FakeDynamicClient {
	return fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{deploymentsKind.gvr: "DeploymentList", pdbGVR: "PodDisruptionBudgetList", reliabilityHPAGVR: "HorizontalPodAutoscalerList"}, objs...)
}
func reliabilityFind(t *testing.T, out core.Reliability, field string) core.ReliabilityObservation {
	t.Helper()
	for _, o := range out.Observations {
		if o.Field == field {
			return o
		}
	}
	t.Fatal("missing field", field)
	return core.ReliabilityObservation{}
}
func TestReliabilityProjectionAndRolloutRounding(t *testing.T) {
	for _, replicas := range []int64{0, 1, 3, 4} {
		t.Run(fmt.Sprint(replicas), func(t *testing.T) {
			u := reliabilityObject()
			_ = unstructured.SetNestedField(u.Object, replicas, "spec", "replicas")
			out := core.Reliability{Ref: reliabilityRef()}
			projectReliability(&out, u)
			require.Equal(t, "100m", reliabilityFind(t, out, "spec.template.spec.containers[0].resources.requests.cpu").Value)
			require.Equal(t, "cpuPolicy", reliabilityFind(t, out, "spec.template.spec.containers[0].resources.limits.cpu").Code)
			require.Equal(t, fmt.Sprintf("25%% → %d", replicas/4), reliabilityFind(t, out, "spec.strategy.rollingUpdate.maxUnavailable").Value)
			require.Equal(t, fmt.Sprintf("25%% → %d", (replicas+3)/4), reliabilityFind(t, out, "spec.strategy.rollingUpdate.maxSurge").Value)
			b, _ := json.Marshal(out)
			require.NotContains(t, string(b), "PRIVATE")
			require.NotContains(t, string(b), "httpGet")
		})
	}
	for _, mode := range []string{"Recreate", "invalid", "paused"} {
		u := reliabilityObject()
		if mode == "paused" {
			_ = unstructured.SetNestedField(u.Object, true, "spec", "paused")
		} else {
			_ = unstructured.SetNestedField(u.Object, mode, "spec", "strategy", "type")
		}
		out := core.Reliability{Ref: reliabilityRef()}
		projectReliability(&out, u)
		switch mode {
		case "Recreate":
			require.Equal(t, "review", reliabilityFind(t, out, "spec.strategy.type").Outcome)
		case "invalid":
			require.Equal(t, "unknown", reliabilityFind(t, out, "spec.strategy.type").Outcome)
		default:
			require.Equal(t, "true", reliabilityFind(t, out, "spec.paused").Value)
		}
	}
}
func TestReliabilityProbeApplicabilityAndBoundedProjection(t *testing.T) {
	for _, batch := range []bool{false, true} {
		u := reliabilityObject()
		if batch {
			_ = unstructured.SetNestedField(u.Object, "Never", "spec", "template", "spec", "restartPolicy")
		}
		_ = unstructured.SetNestedSlice(u.Object, []any{map[string]any{"name": "setup"}, map[string]any{"name": "proxy", "restartPolicy": "Always"}}, "spec", "template", "spec", "initContainers")
		out := core.Reliability{Ref: reliabilityRef()}
		projectReliability(&out, u)
		require.Equal(t, "not-applicable", reliabilityFind(t, out, "spec.template.spec.initContainers[0].readinessProbe").Outcome)
		expected := "review"
		if batch {
			expected = "not-applicable"
		}
		require.Equal(t, expected, reliabilityFind(t, out, "spec.template.spec.initContainers[1].readinessProbe").Outcome)
	}
	u := reliabilityObject()
	containers := []any{}
	for i := range 65 {
		containers = append(containers, map[string]any{"name": fmt.Sprintf("c-%d", i)})
	}
	_ = unstructured.SetNestedSlice(u.Object, containers, "spec", "template", "spec", "containers")
	out := core.Reliability{Ref: reliabilityRef()}
	projectReliability(&out, u)
	require.True(t, out.Partial)
	require.Len(t, out.Observations, 256)
	u = reliabilityObject()
	_ = unstructured.SetNestedField(u.Object, "PRIVATE-BAD-QUANTITY", "spec", "template", "spec", "containers-bad")
	_ = unstructured.SetNestedField(u.Object, []any{map[string]any{"name": "web", "resources": map[string]any{"requests": map[string]any{"cpu": "PRIVATE-BAD-QUANTITY", "memory": int64(2)}}}}, "spec", "template", "spec", "containers")
	out = core.Reliability{Ref: reliabilityRef()}
	projectReliability(&out, u)
	require.Equal(t, "unknown", reliabilityFind(t, out, "spec.template.spec.containers[0].resources.requests.cpu").Outcome)
	b, _ := json.Marshal(out)
	require.NotContains(t, string(b), "PRIVATE")
}
func TestReliabilityPDBSelectorAndCurrentStatus(t *testing.T) {
	for _, tc := range []struct {
		name         string
		selector     any
		match, valid bool
	}{{"missing", nil, false, true}, {"null", nil, false, true}, {"typed nil", map[string]any(nil), false, true}, {"empty", map[string]any{}, true, true}, {"match", map[string]any{"matchLabels": map[string]any{"app": "web"}}, true, true}, {"expression", map[string]any{"matchExpressions": []any{map[string]any{"key": "app", "operator": "In", "values": []any{"web"}}}}, true, true}, {"bad", map[string]any{"matchExpressions": []any{map[string]any{"key": "app", "operator": "Bogus"}}}, false, false}, {"oversize", map[string]any{"matchLabels": map[string]any{strings.Repeat("x", 1000): "web"}}, false, false}, {"wrongType", "PRIVATE", false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			u := pdb("blue", "budget", nil, 0)
			if tc.selector != nil || tc.name == "null" {
				u.Object["spec"].(map[string]any)["selector"] = tc.selector
			}
			m, v := reliabilitySelector(u, map[string]string{"app": "web"})
			require.Equal(t, tc.match, m)
			require.Equal(t, tc.valid, v)
		})
	}
	for _, mode := range []string{"current", "stale", "missing allowed", "missing generation", "negative", "wrong type"} {
		t.Run(mode, func(t *testing.T) {
			u := pdb("blue", "budget", map[string]any{"app": "web"}, 0)
			switch mode {
			case "stale":
				u.SetGeneration(2)
			case "missing allowed":
				unstructured.RemoveNestedField(u.Object, "status", "disruptionsAllowed")
			case "missing generation":
				unstructured.RemoveNestedField(u.Object, "metadata", "generation")
			case "negative":
				_ = unstructured.SetNestedField(u.Object, int64(-1), "status", "disruptionsAllowed")
			case "wrong type":
				_ = unstructured.SetNestedField(u.Object, "0", "status", "disruptionsAllowed")
			}
			_, current := currentPDBDisruptions(u)
			require.Equal(t, mode == "current", current)
			pod := mk("v1", "Pod", "blue", "web", "pod-uid", nil)
			pod.SetLabels(map[string]string{"app": "web"})
			s, _ := drainSession(t, u)
			warnings := fmt.Sprint(s.drainPDBs(t.Context(), []drainPod{{u: pod}}))
			if mode == "current" {
				require.Contains(t, warnings, "pdbBlocksOne")
				require.NotContains(t, warnings, "pdbUnchecked")
			} else {
				require.NotContains(t, warnings, "pdbBlocksOne")
				require.Contains(t, warnings, "pdbUnchecked")
			}
		})
	}
}
func TestReliabilitySnapshotIdentityCoveragePrivacyAndRelatedRechecks(t *testing.T) {
	for _, mode := range []string{"normal", "denied", "repeated", "ignored page", "duplicate", "oversize", "root changed", "root replaced", "related changed", "related replaced", "malformed selector"} {
		t.Run(mode, func(t *testing.T) {
			root := reliabilityObject()
			budget := pdb("blue", "budget", map[string]any{"app": "web"}, 0)
			budget.SetResourceVersion("1")
			client := reliabilityFake(root, budget)
			gets := 0
			client.PrependReactor("get", "deployments", func(kt.Action) (bool, runtime.Object, error) {
				gets++
				u := root.DeepCopy()
				if gets > 1 {
					if mode == "root changed" {
						u.SetResourceVersion("8")
					}
					if mode == "root replaced" {
						u.SetUID("new-uid")
					}
				}
				return true, u, nil
			})
			client.PrependReactor("get", "poddisruptionbudgets", func(kt.Action) (bool, runtime.Object, error) {
				u := budget.DeepCopy()
				if mode == "related changed" {
					u.SetResourceVersion("9")
				}
				if mode == "related replaced" {
					u.SetUID("replacement")
				}
				return true, u, nil
			})
			client.PrependReactor("list", "poddisruptionbudgets", func(kt.Action) (bool, runtime.Object, error) {
				if mode == "denied" {
					return true, nil, fmt.Errorf("PRIVATE-SERVER")
				}
				l := &unstructured.UnstructuredList{}
				u := budget.DeepCopy()
				if mode == "oversize" {
					u.SetAnnotations(map[string]string{"private": strings.Repeat("x", 1<<20)})
				}
				if mode == "malformed selector" {
					_ = unstructured.SetNestedField(u.Object, "PRIVATE", "spec", "selector")
				}
				l.Items = []unstructured.Unstructured{*u}
				if mode == "repeated" {
					l.SetContinue("same")
				}
				if mode == "duplicate" {
					l.Items = append(l.Items, *u)
				}
				if mode == "ignored page" {
					for range 32 {
						l.Items = append(l.Items, *u)
					}
				}
				return true, l, nil
			})
			s := newSession("t", "h", client, false)
			defer s.Close()
			out, err := s.ResourceReliability(t.Context(), reliabilityRef())
			if strings.HasPrefix(mode, "root ") {
				require.Error(t, err)
				require.Empty(t, out.Observations)
				return
			}
			require.NoError(t, err)
			if mode == "normal" {
				require.Equal(t, "complete", out.PDBState)
				o := reliabilityFind(t, out, "status.disruptionsAllowed")
				require.Equal(t, "0", o.Value)
				require.Equal(t, "policy/poddisruptionbudgets", o.Source.Kind)
				require.Equal(t, "pdbCandidate", reliabilityFind(t, out, "spec.selector").Code)
			} else {
				require.NotEqual(t, "complete", out.PDBState)
			}
			if strings.HasPrefix(mode, "related ") {
				for _, o := range out.Observations {
					require.NotEqual(t, "policy/poddisruptionbudgets", o.Source.Kind)
				}
			}
			b, _ := json.Marshal(out)
			require.NotContains(t, string(b), "PRIVATE")
			for _, a := range client.Actions() {
				require.Equal(t, "blue", a.GetNamespace())
				require.Contains(t, []string{"deployments", "poddisruptionbudgets", "horizontalpodautoscalers"}, a.GetResource().Resource)
				require.Contains(t, []string{"get", "list"}, a.GetVerb())
			}
		})
	}
}
func TestReliabilityGateCancellationAndUnsupportedScope(t *testing.T) {
	s := newSession("t", "h", reliabilityFake(reliabilityObject()), false)
	defer s.Close()
	s.graphGate <- struct{}{}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, err := s.ResourceReliability(ctx, reliabilityRef())
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Len(t, s.graphGate, 1)
	<-s.graphGate
	for _, field := range []string{"kind", "scope", "uid", "target", "provider"} {
		ref := reliabilityRef()
		switch field {
		case "kind":
			ref.Kind = "batch/jobs"
		case "scope":
			ref.Scope = ""
		case "uid":
			ref.UID = ""
		case "target":
			ref.Target = "foreign"
		case "provider":
			ref.Provider = "other"
		}
		_, err = s.ResourceReliability(t.Context(), ref)
		require.Error(t, err)
	}
}
func TestReliabilityHPAAssociationFreshnessAndUtilization(t *testing.T) {
	for _, mode := range []string{"current", "stale", "wrong GVK", "wrong name", "wrong type", "many metrics"} {
		t.Run(mode, func(t *testing.T) {
			u := mk("autoscaling/v2", "HorizontalPodAutoscaler", "blue", "scale", "hpa-uid", map[string]any{"spec": map[string]any{"scaleTargetRef": map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "web"}, "maxReplicas": int64(5), "metrics": []any{map[string]any{"type": "ContainerResource", "containerResource": map[string]any{"name": "cpu", "container": "web", "target": map[string]any{"type": "Utilization", "averageUtilization": int64(70)}}}}}, "status": map[string]any{"observedGeneration": int64(2), "currentReplicas": int64(1), "desiredReplicas": int64(2), "conditions": []any{map[string]any{"type": "ScalingActive", "status": "True", "observedGeneration": int64(2), "message": "PRIVATE"}}}})
			u.SetGeneration(2)
			u.SetResourceVersion("1")
			switch mode {
			case "stale":
				u.SetGeneration(3)
			case "wrong GVK":
				_ = unstructured.SetNestedField(u.Object, "apps/v2", "spec", "scaleTargetRef", "apiVersion")
			case "wrong name":
				_ = unstructured.SetNestedField(u.Object, "other", "spec", "scaleTargetRef", "name")
			case "wrong type":
				_ = unstructured.SetNestedField(u.Object, "PRIVATE", "status", "currentReplicas")
			case "many metrics":
				v := []any{}
				for range 33 {
					v = append(v, map[string]any{})
				}
				_ = unstructured.SetNestedSlice(u.Object, v, "spec", "metrics")
			}
			s := newSession("t", "h", reliabilityFake(reliabilityObject(), u), false)
			defer s.Close()
			out, err := s.ResourceReliability(t.Context(), reliabilityRef())
			require.NoError(t, err)
			if strings.HasPrefix(mode, "wrong GVK") || mode == "wrong name" {
				for _, o := range out.Observations {
					require.NotEqual(t, "autoscaling/horizontalpodautoscalers", o.Source.Kind)
				}
				return
			}
			o := reliabilityFind(t, out, "status.currentReplicas")
			switch mode {
			case "current":
				require.Equal(t, "1", o.Value)
			case "stale", "wrong type":
				require.Equal(t, "unknown", o.Outcome)
			}
			if mode == "many metrics" {
				require.True(t, out.Partial)
			} else {
				require.Equal(t, "hpaCPU", reliabilityFind(t, out, "spec.metrics[0]").Code)
			}
			b, _ := json.Marshal(out)
			require.NotContains(t, string(b), "PRIVATE")
		})
	}
}

func TestReliabilitySurgeAboveHundredAndNativeStrategyDeclarations(t *testing.T) {
	u := reliabilityObject()
	_ = unstructured.SetNestedField(u.Object, "200%", "spec", "strategy", "rollingUpdate", "maxSurge")
	out := core.Reliability{Ref: reliabilityRef()}
	projectReliability(&out, u)
	require.Equal(t, "200% → 2", reliabilityFind(t, out, "spec.strategy.rollingUpdate.maxSurge").Value)
	for _, kind := range []string{"StatefulSet", "DaemonSet"} {
		u := reliabilityObject()
		u.SetKind(kind)
		_ = unstructured.SetNestedField(u.Object, "OnDelete", "spec", "updateStrategy", "type")
		field := "maxSurge"
		value := any("200%")
		if kind == "StatefulSet" {
			field = "partition"
			value = int64(2)
		}
		_ = unstructured.SetNestedField(u.Object, value, "spec", "updateStrategy", "rollingUpdate", field)
		out := core.Reliability{Ref: reliabilityRef()}
		projectReliability(&out, u)
		require.Equal(t, "OnDelete", reliabilityFind(t, out, "spec.updateStrategy.type").Value)
		require.Equal(t, "observed", reliabilityFind(t, out, "spec.updateStrategy.rollingUpdate."+field).Outcome)
	}
}
func TestReliabilityPodAndTemplateCandidateDistinction(t *testing.T) {
	root := reliabilityObject()
	spec, _, _ := unstructured.NestedMap(root.Object, "spec", "template", "spec")
	pod := mk("v1", "Pod", "blue", "web", "workload-uid", map[string]any{"spec": spec})
	pod.SetLabels(map[string]string{"app": "web"})
	pod.SetResourceVersion("1")
	b := pdb("blue", "budget", map[string]any{"app": "web"}, 0)
	b.SetResourceVersion("1")
	s := newSession("t", "h", reliabilityFake(pod, b), false)
	defer s.Close()
	ref := reliabilityRef()
	ref.Kind = "pods"
	out, err := s.ResourceReliability(t.Context(), ref)
	require.NoError(t, err)
	require.Equal(t, "pdbSelected", reliabilityFind(t, out, "spec.selector").Code)
	require.Equal(t, "not-applicable", out.HPAState)
	for _, a := range s.dyn.(*fake.FakeDynamicClient).Actions() {
		require.NotEqual(t, "horizontalpodautoscalers", a.GetResource().Resource)
	}
}

func TestReliabilityWholeEncodedObservationBudget(t *testing.T) {
	u := reliabilityObject()
	out := core.Reliability{Ref: reliabilityRef()}
	// Escaping identity fields can exceed the DTO budget even with no value.
	u.Object["metadata"].(map[string]any)["uid"] = strings.Repeat("\u0001", 256)
	u.SetResourceVersion(strings.Repeat("\u0001", 256))
	reliabilityAdd(&out, u, "resources", "spec.containers[0].resources.requests.cpu", "web", "observed", "resource", "")
	require.True(t, out.Partial)
	require.Empty(t, out.Observations)
	out = core.Reliability{Ref: reliabilityRef()}
	u = reliabilityObject()
	reliabilityAdd(&out, u, "resources", strings.Repeat("x", 513), "", "observed", "resource", "")
	require.True(t, out.Partial)
	require.Empty(t, out.Observations)
	out = core.Reliability{Ref: reliabilityRef()}
	projectReliability(&out, u)
	require.NotEmpty(t, out.Observations)
	for _, o := range out.Observations {
		b, err := json.Marshal(o)
		require.NoError(t, err)
		require.LessOrEqual(t, len(b), 2048)
	}
}

func TestReliabilityMultipleDeclaredAssociationsRetainIndependentSources(t *testing.T) {
	root := reliabilityObject()
	a := pdb("blue", "one", map[string]any{"app": "web"}, 0)
	a.SetResourceVersion("1")
	b := pdb("blue", "two", map[string]any{"app": "web"}, 1)
	b.SetResourceVersion("1")
	hpas := []runtime.Object{root, a, b}
	for _, name := range []string{"one", "two"} {
		u := mk("autoscaling/v2", "HorizontalPodAutoscaler", "blue", name, "hpa-"+name, map[string]any{"spec": map[string]any{"scaleTargetRef": map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "web"}}})
		u.SetResourceVersion("1")
		hpas = append(hpas, u)
	}
	s := newSession("t", "h", reliabilityFake(hpas...), false)
	defer s.Close()
	out, err := s.ResourceReliability(t.Context(), reliabilityRef())
	require.NoError(t, err)
	require.Equal(t, "complete", out.PDBState)
	require.Equal(t, "ambiguous", out.HPAState)
	pdbSources, hpaSources := map[string]bool{}, map[string]bool{}
	multiple := false
	for _, o := range out.Observations {
		switch o.Source.Kind {
		case "policy/poddisruptionbudgets":
			pdbSources[o.Source.UID] = true
		case "autoscaling/horizontalpodautoscalers":
			hpaSources[o.Source.UID] = true
		}
		if o.Code == "pdbMultiple" {
			multiple = true
			require.Equal(t, "2", o.Value)
			require.Equal(t, "spec.template.metadata.labels", o.Field)
		}
		b, err := json.Marshal(o)
		require.NoError(t, err)
		require.LessOrEqual(t, len(b), 2048)
	}
	require.Len(t, pdbSources, 2)
	require.Len(t, hpaSources, 2)
	require.True(t, multiple)
}

func TestReliabilityExcludedProbeAndAbsentPDBFieldAttribution(t *testing.T) {
	root := reliabilityObject()
	out := core.Reliability{Ref: reliabilityRef()}
	projectReliability(&out, root)
	excluded := reliabilityFind(t, out, "spec.template.spec.ephemeralContainers")
	require.Equal(t, "kubernetes.reliability.probes", excluded.Rule)
	require.Contains(t, excluded.Reference, "liveness-readiness-startup-probes")
	for _, pod := range []bool{false, true} {
		u := root.DeepCopy()
		ref := reliabilityRef()
		field := "spec.template.metadata.labels"
		if pod {
			u.SetAPIVersion("v1")
			u.SetKind("Pod")
			u.Object["spec"] = map[string]any{"containers": []any{map[string]any{"name": "web"}}}
			ref.Kind = "pods"
			field = "metadata.labels"
		}
		s := newSession("t", "h", reliabilityFake(u), false)
		out, err := s.ResourceReliability(t.Context(), ref)
		s.Close()
		require.NoError(t, err)
		found := false
		for _, o := range out.Observations {
			if o.Code == "pdbNone" {
				found = true
				require.Equal(t, field, o.Field)
				require.Equal(t, ref.UID, o.Source.UID)
			}
		}
		require.True(t, found)
	}
}
