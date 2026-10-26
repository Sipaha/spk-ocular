package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
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

func assessmentRequest(check string) core.NamespaceAssessmentRequest {
	return core.NamespaceAssessmentRequest{Provider: ProviderID, Target: "t", Namespace: "blue", Check: check}
}
func TestNamespaceAssessmentExplicitScopeSummaryAndPrivacy(t *testing.T) {
	s, d := assessmentSession(reliabilityObject())
	out, err := s.AssessNamespace(t.Context(), assessmentRequest("configuration"))
	require.NoError(t, err)
	require.Len(t, out.Resources, 1)
	require.Equal(t, reliabilityRef(), out.Resources[0].Ref)
	require.Positive(t, out.Resources[0].Counts.Review)
	require.Positive(t, out.Resources[0].Counts.Observed)
	data, err := json.Marshal(out)
	require.NoError(t, err)
	require.NotContains(t, string(data), "PRIVATE")
	for _, action := range d.Actions() {
		require.Contains(t, []string{"get", "list"}, action.GetVerb())
		require.Equal(t, "blue", action.GetNamespace())
	}
	before := len(d.Actions())
	for _, req := range []core.NamespaceAssessmentRequest{{}, {Provider: ProviderID, Target: "t", Namespace: "", Check: "configuration"}, {Provider: ProviderID, Target: "t", Namespace: "blue", Check: "trivy"}, {Provider: ProviderID, Target: "foreign", Namespace: "blue", Check: "configuration"}} {
		_, err = s.AssessNamespace(t.Context(), req)
		require.Error(t, err)
	}
	require.Len(t, d.Actions(), before)
}
func TestNamespaceAssessmentMissingDeniedAndChangedSourcesAreNotSuccess(t *testing.T) {
	s, d := assessmentSession(reliabilityObject())
	d.PrependReactor("list", "statefulsets", func(kt.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "statefulsets"}, "", fmt.Errorf("fixture"))
	})
	d.PrependReactor("get", "deployments", func(kt.Action) (bool, runtime.Object, error) {
		u := reliabilityObject()
		u.SetUID("replacement")
		return true, u, nil
	})
	out, err := s.AssessNamespace(t.Context(), assessmentRequest("configuration"))
	require.NoError(t, err)
	require.True(t, out.Partial)
	require.Contains(t, out.Coverage, core.AssessmentCoverage{Kind: "apps/statefulsets", State: "forbidden"})
	require.Len(t, out.Resources, 1)
	require.Equal(t, "gone", out.Resources[0].State)
	require.Equal(t, 1, out.Resources[0].Counts.Unknown)
	require.Zero(t, out.Resources[0].Counts.Observed)
}
func TestNamespaceAssessmentAdmissionBoundsAndCancellation(t *testing.T) {
	s, d := assessmentSession()
	d.PrependReactor("list", "deployments", func(kt.Action) (bool, runtime.Object, error) {
		list := &unstructured.UnstructuredList{Object: map[string]any{"apiVersion": "apps/v1", "kind": "DeploymentList"}, Items: []unstructured.Unstructured{}}
		for i := range 40 {
			u := reliabilityObject()
			u.SetName(fmt.Sprintf("web-%02d", i))
			u.SetUID(types.UID(fmt.Sprintf("uid-%d", i)))
			list.Items = append(list.Items, *u)
		}
		list.SetContinue("next-page")
		return true, list, nil
	})
	out, err := s.AssessNamespace(t.Context(), assessmentRequest("configuration"))
	require.NoError(t, err)
	require.True(t, out.Partial)
	require.Len(t, out.Resources, assessmentResources)
	for _, a := range d.Actions() {
		if a.GetVerb() == "list" {
			opts := a.(kt.ListAction).GetListRestrictions()
			require.Empty(t, opts.Fields.String())
		}
	}
	before := len(d.Actions())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = s.AssessNamespace(ctx, assessmentRequest("configuration"))
	require.Error(t, err)
	require.Len(t, d.Actions(), before)
}
func TestNamespaceAssessmentVPAKeepsAmbiguityAndEmptyCoverage(t *testing.T) {
	root, first := vpaRoot(), vpaObject()
	second := first.DeepCopy()
	second.SetName("other")
	second.SetUID("other-vpa")
	s, _ := assessmentSession(root, first, second)
	out, err := s.AssessNamespace(t.Context(), assessmentRequest("vpa"))
	require.NoError(t, err)
	require.Len(t, out.Resources, 1)
	require.Equal(t, "ambiguous", out.Resources[0].State)
	require.Equal(t, 2, out.Resources[0].Counts.Observed)
	require.Positive(t, out.Resources[0].Counts.Unknown)
	empty, _ := assessmentSession()
	out, err = empty.AssessNamespace(t.Context(), assessmentRequest("diagnosis"))
	require.NoError(t, err)
	require.Empty(t, out.Resources)
	require.True(t, out.Partial)
	require.Contains(t, out.Coverage, core.AssessmentCoverage{Kind: "batch/jobs", State: "not-discovered"})
}
func TestNamespaceAssessmentRejectsForeignAndOversizedListObjects(t *testing.T) {
	s, d := assessmentSession()
	d.PrependReactor("list", "deployments", func(kt.Action) (bool, runtime.Object, error) {
		u := reliabilityObject()
		u.SetNamespace("foreign")
		return true, &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*u}, Object: map[string]any{"metadata": map[string]any{"resourceVersion": "7"}}}, nil
	})
	out, err := s.AssessNamespace(t.Context(), assessmentRequest("configuration"))
	require.NoError(t, err)
	require.True(t, out.Partial)
	require.Empty(t, out.Resources)
	for _, a := range d.Actions() {
		require.Equal(t, "list", a.GetVerb())
	}
	// A successful empty list must be distinguishable from discovery/denial.
	require.Contains(t, out.Coverage, core.AssessmentCoverage{Kind: "apps/statefulsets", State: "complete"})
}

func assessmentSession(objs ...runtime.Object) (*session, *fake.FakeDynamicClient) {
	s, _ := vpaSession(objs...)
	lists := map[schema.GroupVersionResource]string{vpaGVR: "VerticalPodAutoscalerList", reliabilityHPAGVR: "HorizontalPodAutoscalerList", pdbGVR: "PodDisruptionBudgetList"}
	for _, d := range allKinds.list {
		if !d.virtual {
			lists[d.gvr] = kindOf(d) + "List"
		}
	}
	d := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), lists, objs...)
	s.dyn = d
	return s, d
}

func TestNamespaceAssessmentEmptyVPAIsKnownAbsenceNotFailedAcquisition(t *testing.T) {
	s, _ := assessmentSession(vpaRoot())
	out, err := s.AssessNamespace(t.Context(), assessmentRequest("vpa"))
	require.NoError(t, err)
	require.Len(t, out.Resources, 1)
	require.Equal(t, "no-matches", out.Resources[0].State)
	require.False(t, out.Partial)
	require.Zero(t, out.Resources[0].Counts.Unknown)
	require.Empty(t, out.Resources[0].Highlights)
}

func TestNamespaceAssessmentVPAOverviewRetainsRecommendationConfidence(t *testing.T) {
	root, source := vpaRoot(), vpaObject()
	require.NoError(t, unstructured.SetNestedSlice(source.Object, []any{map[string]any{"type": "LowConfidence", "status": "True"}}, "status", "conditions"))
	s, _ := assessmentSession(root, source)
	out, err := s.AssessNamespace(t.Context(), assessmentRequest("vpa"))
	require.NoError(t, err)
	require.Len(t, out.Resources, 1)
	row := out.Resources[0]
	require.Len(t, row.Highlights, 1)
	require.Equal(t, "low-confidence", row.Highlights[0].SourceState)
	require.Equal(t, "current", row.Highlights[0].Freshness)
	require.Positive(t, row.Counts.Unknown)
	require.True(t, out.Partial)
	require.Contains(t, row.Highlights[0].Value, "250m")
}

func TestNamespaceAssessmentVPAStalePreviewRetainsReasonAndHidesQuantities(t *testing.T) {
	root, source := vpaRoot(), vpaObject()
	require.NoError(t, unstructured.SetNestedField(source.Object, int64(1), "status", "observedGeneration"))
	s, _ := assessmentSession(root, source)
	out, err := s.AssessNamespace(t.Context(), assessmentRequest("vpa"))
	require.NoError(t, err)
	require.Len(t, out.Resources, 1)
	row := out.Resources[0]
	require.Zero(t, row.Counts.Observed)
	require.Len(t, row.Highlights, 1)
	require.Equal(t, "stale", row.Highlights[0].SourceState)
	require.Empty(t, row.Highlights[0].Value)
}
