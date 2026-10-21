package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic/fake"
	kt "k8s.io/client-go/testing"
)

func vpaRoot() *unstructured.Unstructured {
	u := mk("apps/v1", "Deployment", "blue", "web", "current-controller", map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"containers": []any{map[string]any{"name": "app", "resources": map[string]any{"requests": map[string]any{"cpu": "100m", "memory": "64Mi"}, "limits": map[string]any{"cpu": "1", "memory": "128Mi"}}, "env": []any{map[string]any{"value": "PRIVATE-ENV"}}}}}}}})
	u.SetResourceVersion("7")
	return u
}
func vpaObject() *unstructured.Unstructured {
	u := mk("autoscaling.k8s.io/v1", "VerticalPodAutoscaler", "blue", "sizing", "vpa-uid", map[string]any{"spec": map[string]any{"targetRef": map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "web"}, "updatePolicy": map[string]any{"updateMode": "Off"}, "resourcePolicy": map[string]any{"containerPolicies": []any{map[string]any{"containerName": "*", "mode": "Auto", "controlledResources": []any{"cpu", "memory"}, "controlledValues": "RequestsOnly", "minAllowed": map[string]any{"cpu": "10m"}, "maxAllowed": map[string]any{"memory": "1Gi"}}}}}, "status": map[string]any{"observedGeneration": int64(2), "recommendation": map[string]any{"containerRecommendations": []any{map[string]any{"containerName": "app", "target": map[string]any{"cpu": "250m", "memory": "96Mi"}, "lowerBound": map[string]any{"cpu": "0"}, "uncappedTarget": map[string]any{"memory": "2Gi"}}}}, "conditions": []any{map[string]any{"type": "RecommendationProvided", "status": "True", "observedGeneration": int64(2), "lastTransitionTime": "2026-10-21T10:00:00Z", "reason": "PRIVATE-REASON", "message": "PRIVATE-MESSAGE"}}}})
	u.SetResourceVersion("3")
	u.SetGeneration(2)
	return u
}
func vpaSubject() core.Ref {
	return core.Ref{Provider: ProviderID, Target: "t", Kind: "apps/deployments", Scope: "blue", Name: "web", UID: "current-controller"}
}
func vpaSession(objs ...runtime.Object) (*session, *fake.FakeDynamicClient) {
	listKinds := map[schema.GroupVersionResource]string{vpaGVR: "VerticalPodAutoscalerList", reliabilityHPAGVR: "HorizontalPodAutoscalerList"}
	for _, d := range allKinds.list {
		if !d.virtual {
			listKinds[d.gvr] = kindOf(d) + "List"
		}
	}
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds, objs...)
	s := newSession("t", "h", dyn, false)
	defs := append([]*kindDef{}, allKinds.list...)
	defs = append(defs, discoveredDef(apiResource{Group: vpaGVR.Group, Version: "v1", Resource: vpaGVR.Resource, Kind: "VerticalPodAutoscaler", Namespaced: true, Verbs: lw}))
	s.cat.cur.Store(&catalogSnap{reg: newKindRegistry(defs...), state: catalogReady})
	return s, dyn
}
func TestVPAExplicitCurrentIdentityAndTypedProjection(t *testing.T) {
	root, vpa := vpaRoot(), vpaObject()
	s, d := vpaSession(root, vpa)
	out, e := s.ResourceVPA(t.Context(), vpaSubject())
	require.NoError(t, e)
	require.Equal(t, "complete", out.State)
	require.Equal(t, "complete", out.HPAState)
	require.Len(t, out.Sources, 1)
	v := out.Sources[0]
	require.Equal(t, "Off", v.UpdateMode)
	require.Equal(t, "current", v.Freshness)
	require.Equal(t, "reported", v.State)
	require.Equal(t, "vpa-uid", v.Ref.UID)
	require.Equal(t, "250m", v.Recommendations[0].Target.CPU)
	require.Equal(t, "0", v.Recommendations[0].LowerBound.CPU)
	require.Empty(t, v.Recommendations[0].UpperBound.Memory)
	require.Equal(t, "64Mi", out.Declarations[0].Requests.Memory)
	require.True(t, v.Policies[0].ControlledResourcesKnown)
	encoded, e := json.Marshal(out)
	require.NoError(t, e)
	require.NotContains(t, string(encoded), "PRIVATE")
	require.NotContains(t, string(encoded), "sampleTime")
	for _, a := range d.Actions() {
		require.Equal(t, "blue", a.GetNamespace())
		require.Contains(t, []string{"get", "list"}, a.GetVerb())
		require.Contains(t, []string{"deployments", "verticalpodautoscalers", "horizontalpodautoscalers"}, a.GetResource().Resource)
	}
	availability, e := s.Get(t.Context(), vpaSubject())
	require.NoError(t, e)
	require.True(t, availability.VPAAvailable)
}
func TestVPAUnknownFreshnessAndProducerConditions(t *testing.T) {
	for _, tc := range []struct {
		name, state string
		edit        func(*unstructured.Unstructured)
	}{
		{"old status", "stale", func(u *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(u.Object, int64(1), "status", "observedGeneration")
		}},
		{"old condition", "stale", func(u *unstructured.Unstructured) {
			_ = unstructured.SetNestedSlice(u.Object, []any{map[string]any{"type": "RecommendationProvided", "status": "True", "observedGeneration": int64(1)}}, "status", "conditions")
		}},
		{"warming", "fetching-history", func(u *unstructured.Unstructured) {
			_ = unstructured.SetNestedSlice(u.Object, []any{map[string]any{"type": "FetchingHistory", "status": "True"}}, "status", "conditions")
		}},
		{"low confidence", "low-confidence", func(u *unstructured.Unstructured) {
			_ = unstructured.SetNestedSlice(u.Object, []any{map[string]any{"type": "LowConfidence", "status": "True"}}, "status", "conditions")
		}},
		{"none", "no-recommendation", func(u *unstructured.Unstructured) {
			unstructured.RemoveNestedField(u.Object, "status", "recommendation")
		}},
		{"no pods", "no-pods", func(u *unstructured.Unstructured) {
			_ = unstructured.SetNestedSlice(u.Object, []any{map[string]any{"type": "NoPodsMatched", "status": "True"}}, "status", "conditions")
		}},
		{"unsupported config", "config-unsupported", func(u *unstructured.Unstructured) {
			_ = unstructured.SetNestedSlice(u.Object, []any{map[string]any{"type": "ConfigUnsupported", "status": "True"}}, "status", "conditions")
		}},
		{"duplicate container", "invalid", func(u *unstructured.Unstructured) {
			rows, _, _ := unstructured.NestedSlice(u.Object, "status", "recommendation", "containerRecommendations")
			_ = unstructured.SetNestedSlice(u.Object, append(rows, rows...), "status", "recommendation", "containerRecommendations")
		}},
		{"foreign container", "invalid", func(u *unstructured.Unstructured) {
			_ = unstructured.SetNestedSlice(u.Object, []any{map[string]any{"containerName": "foreign", "target": map[string]any{"cpu": "5"}}}, "status", "recommendation", "containerRecommendations")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := vpaObject()
			tc.edit(u)
			out := projectVPA(u, vpaSubject(), map[string]bool{"app": true})
			require.Equal(t, tc.state, out.State)
			if tc.state == "stale" || tc.state == "invalid" || tc.state == "config-unsupported" || tc.state == "no-pods" {
				require.Empty(t, out.Recommendations)
			}
		})
	}
	u := vpaObject()
	unstructured.RemoveNestedField(u.Object, "status", "observedGeneration")
	unstructured.RemoveNestedField(u.Object, "status", "conditions")
	unstructured.RemoveNestedField(u.Object, "spec", "updatePolicy")
	v := projectVPA(u, vpaSubject(), map[string]bool{"app": true})
	require.Equal(t, "unknown", v.Freshness)
	require.Equal(t, "unknown", v.UpdateMode)
	require.Empty(t, v.Recommender)
	require.Len(t, v.Recommendations, 1)
	for _, q := range []any{"-1", "NaN", "Inf", strings.Repeat("9", 65), "PRIVATE", float64(4)} {
		require.Empty(t, vpaResources(map[string]any{"cpu": q}).CPU)
	}
}
func TestVPACollectionCoverageAndReplacement(t *testing.T) {
	for _, mode := range []string{"missing-api", "denied", "denied-source", "no-matches", "multiple", "changed-source", "changed-root", "replaced-root", "wrong-api", "page-overflow", "repeat-cursor", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			root, v := vpaRoot(), vpaObject()
			s, d := vpaSession(root, v)
			switch mode {
			case "missing-api":
				s.cat.cur.Store(&catalogSnap{reg: allKinds, state: catalogReady})
			case "denied":
				d.PrependReactor("list", vpaGVR.Resource, func(kt.Action) (bool, runtime.Object, error) {
					return true, nil, apierrors.NewForbidden(vpaGVR.GroupResource(), "", fmt.Errorf("PRIVATE"))
				})
			case "denied-source":
				d.PrependReactor("get", vpaGVR.Resource, func(kt.Action) (bool, runtime.Object, error) {
					return true, nil, apierrors.NewForbidden(vpaGVR.GroupResource(), "sizing", fmt.Errorf("PRIVATE"))
				})
			case "no-matches":
				_ = unstructured.SetNestedField(v.Object, "StatefulSet", "spec", "targetRef", "kind")
				require.NoError(t, d.Tracker().Update(vpaGVR, v, "blue"))
			case "multiple":
				b := v.DeepCopy()
				b.SetName("other")
				b.SetUID("other-uid")
				require.NoError(t, d.Tracker().Add(b))
			case "changed-source":
				d.PrependReactor("get", vpaGVR.Resource, func(kt.Action) (bool, runtime.Object, error) { v.SetResourceVersion("changed"); return true, v, nil })
			case "changed-root", "replaced-root":
				reads := 0
				d.PrependReactor("get", "deployments", func(kt.Action) (bool, runtime.Object, error) {
					reads++
					if reads > 1 {
						if mode == "changed-root" {
							root.SetResourceVersion("new")
						} else {
							root.SetUID("new")
						}
					}
					return true, root, nil
				})
			case "wrong-api":
				_ = unstructured.SetNestedField(v.Object, "apps/v2", "spec", "targetRef", "apiVersion")
				require.NoError(t, d.Tracker().Update(vpaGVR, v, "blue"))
			case "page-overflow", "repeat-cursor":
				d.PrependReactor("list", vpaGVR.Resource, func(kt.Action) (bool, runtime.Object, error) {
					list := &unstructured.UnstructuredList{}
					if mode == "page-overflow" {
						for range 33 {
							list.Items = append(list.Items, *v)
						}
					} else {
						list.SetContinue("same")
					}
					return true, list, nil
				})
			}
			ctx := t.Context()
			if mode == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			out, e := s.ResourceVPA(ctx, vpaSubject())
			if mode == "changed-root" || mode == "replaced-root" || mode == "cancel" {
				require.Error(t, e)
				return
			}
			require.NoError(t, e)
			expected := map[string]string{"missing-api": "not-discovered", "denied": "forbidden", "denied-source": "forbidden", "no-matches": "no-matches", "multiple": "ambiguous", "changed-source": "unknown", "wrong-api": "no-matches", "page-overflow": "partial", "repeat-cursor": "partial"}
			require.Equal(t, expected[mode], out.State)
			if mode == "multiple" {
				require.Len(t, out.Sources, 2)
			}
			if mode == "changed-source" || mode == "wrong-api" || mode == "denied" || mode == "denied-source" {
				require.Empty(t, out.Sources)
			}
		})
	}
}
func TestVPASameResourceHPAEvidenceIsSeparate(t *testing.T) {
	h := mk("autoscaling/v2", "HorizontalPodAutoscaler", "blue", "scale", "hpa-uid", map[string]any{"spec": map[string]any{"scaleTargetRef": map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "web"}, "metrics": []any{map[string]any{"type": "Resource", "resource": map[string]any{"name": "cpu", "target": map[string]any{"type": "Utilization"}}}, map[string]any{"type": "External", "external": map[string]any{"metric": map[string]any{"name": "PRIVATE"}}}}}})
	h.SetResourceVersion("8")
	s, _ := vpaSession(vpaRoot(), vpaObject(), h)
	out, e := s.ResourceVPA(t.Context(), vpaSubject())
	require.NoError(t, e)
	require.Len(t, out.HPAMetrics, 1)
	require.Equal(t, "cpu", out.HPAMetrics[0].Resource)
	require.Equal(t, "hpa-uid", out.HPAMetrics[0].Ref.UID)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, e = s.ResourceVPA(ctx, vpaSubject())
	require.Error(t, e)
	bad := vpaSubject()
	bad.Scope = ""
	_, e = s.ResourceVPA(t.Context(), bad)
	require.Error(t, e)
}

func TestVPABudgetsAndJSONArrays(t *testing.T) {
	for _, mode := range []string{"large-root", "large-source", "many-policies", "many-conditions", "many-recommendations", "source-recheck-limit", "missing-hpa-metrics"} {
		t.Run(mode, func(t *testing.T) {
			root, v := vpaRoot(), vpaObject()
			objs := []runtime.Object{root, v}
			switch mode {
			case "large-root":
				root.SetAnnotations(map[string]string{"oversize": strings.Repeat("x", 1<<20)})
			case "large-source":
				v.SetAnnotations(map[string]string{"oversize": strings.Repeat("x", 1<<20)})
			case "many-policies":
				rows := []any{}
				for range 18 {
					rows = append(rows, map[string]any{"containerName": "*"})
				}
				_ = unstructured.SetNestedSlice(v.Object, rows, "spec", "resourcePolicy", "containerPolicies")
			case "many-conditions":
				rows := []any{}
				for range 17 {
					rows = append(rows, map[string]any{"type": "RecommendationProvided", "status": "True"})
				}
				_ = unstructured.SetNestedSlice(v.Object, rows, "status", "conditions")
			case "many-recommendations":
				rows := []any{}
				for range 17 {
					rows = append(rows, map[string]any{"containerName": "app", "target": map[string]any{"cpu": "1"}})
				}
				_ = unstructured.SetNestedSlice(v.Object, rows, "status", "recommendation", "containerRecommendations")
			case "source-recheck-limit":
				for i := range 16 {
					other := v.DeepCopy()
					other.SetName(fmt.Sprintf("sizing-%02d", i))
					other.SetUID(types.UID(fmt.Sprintf("uid-%d", i)))
					objs = append(objs, other)
				}
			case "missing-hpa-metrics":
				h := mk("autoscaling/v2", "HorizontalPodAutoscaler", "blue", "scale", "hpa-uid", map[string]any{"spec": map[string]any{"scaleTargetRef": map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "web"}}})
				h.SetResourceVersion("1")
				objs = append(objs, h)
			}
			s, d := vpaSession(objs...)
			out, e := s.ResourceVPA(t.Context(), vpaSubject())
			if mode == "large-root" {
				require.Error(t, e)
				return
			}
			require.NoError(t, e)
			switch mode {
			case "large-source":
				require.Equal(t, "partial", out.State)
			case "source-recheck-limit":
				require.Equal(t, "unknown", out.State)
				require.Len(t, out.Sources, 16)
				gets := 0
				for _, a := range d.Actions() {
					if a.GetVerb() == "get" && a.GetResource() == vpaGVR {
						gets++
					}
				}
				require.Equal(t, 16, gets)
			case "missing-hpa-metrics":
				require.Equal(t, "unknown", out.HPAState)
			default:
				require.True(t, out.Sources[0].Partial)
			}
		})
	}
	u := vpaObject()
	_ = unstructured.SetNestedField(u.Object, int64(1), "status", "observedGeneration")
	out := projectVPA(u, vpaSubject(), map[string]bool{"app": true})
	raw, e := json.Marshal(out)
	require.NoError(t, e)
	require.Contains(t, string(raw), `"recommendations":[]`)
}
