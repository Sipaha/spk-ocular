package kubernetes

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/views"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestDiagnosisBuiltinHealth(t *testing.T) {
	at := "2026-10-21T08:00:00Z"
	condition := func(name, status, reason string) map[string]any {
		return map[string]any{"type": name, "status": status, "reason": reason, "message": "controller evidence", "lastTransitionTime": at}
	}
	tests := []struct {
		name, version, kind string
		spec, status        map[string]any
		state               core.HealthState
		reason              string
	}{
		{"failed Job", "batch/v1", "Job", nil, map[string]any{"conditions": []any{condition("Failed", "True", "BackoffLimitExceeded")}}, core.HealthError, "BackoffLimitExceeded"},
		{"early failure target", "batch/v1", "Job", nil, map[string]any{"conditions": []any{condition("FailureTarget", "True", "PodFailurePolicy")}}, core.HealthError, "PodFailurePolicy"},
		{"retry is not final failure", "batch/v1", "Job", nil, map[string]any{"failed": int64(3), "active": int64(1)}, core.HealthProgressing, "Active"},
		{"new Job without spec/status", "batch/v1", "Job", nil, nil, core.HealthProgressing, "Pending"},
		{"completed Job", "batch/v1", "Job", nil, map[string]any{"conditions": []any{condition("Complete", "True", "CompletionsReached")}}, core.HealthOK, "Complete"},
		{"success criteria before termination", "batch/v1", "Job", nil, map[string]any{"conditions": []any{condition("SuccessCriteriaMet", "True", "SuccessPolicy")}}, core.HealthProgressing, "Pending"},
		{"suspended Job", "batch/v1", "Job", map[string]any{"suspend": true}, nil, core.HealthOK, "Suspended"},
		{"suspended CronJob", "batch/v1", "CronJob", map[string]any{"suspend": true}, nil, core.HealthOK, "Suspended"},
		{"PVC binding pending", "v1", "PersistentVolumeClaim", nil, map[string]any{"phase": "Pending"}, core.HealthWarning, "Pending"},
		{"PVC bound", "v1", "PersistentVolumeClaim", nil, map[string]any{"phase": "Bound"}, core.HealthOK, "Bound"},
		{"PVC lost", "v1", "PersistentVolumeClaim", nil, map[string]any{"phase": "Lost"}, core.HealthError, "Lost"},
		{"PVC phase absent", "v1", "PersistentVolumeClaim", nil, nil, core.HealthUnknown, ""},
		{"HPA metrics missing", "autoscaling/v2", "HorizontalPodAutoscaler", nil, map[string]any{"conditions": []any{condition("AbleToScale", "True", "ReadyForNewScale"), condition("ScalingActive", "False", "FailedGetResourceMetric")}}, core.HealthWarning, "FailedGetResourceMetric"},
		{"HPA scale read failed", "autoscaling/v2", "HorizontalPodAutoscaler", nil, map[string]any{"conditions": []any{condition("AbleToScale", "False", "FailedGetScale")}}, core.HealthWarning, "FailedGetScale"},
		{"HPA deliberately disabled", "autoscaling/v2", "HorizontalPodAutoscaler", nil, map[string]any{"conditions": []any{condition("AbleToScale", "True", "SucceededGetScale"), condition("ScalingActive", "False", "ScalingDisabled")}}, core.HealthProgressing, "ScalingDisabled"},
		{"HPA min max clamp is not a failure", "autoscaling/v2", "HorizontalPodAutoscaler", nil, map[string]any{"conditions": []any{condition("AbleToScale", "True", "ReadyForNewScale"), condition("ScalingActive", "True", "ValidMetricFound"), condition("ScalingLimited", "True", "TooFewReplicas")}}, core.HealthOK, ""},
		{"HPA no observations", "autoscaling/v2", "HorizontalPodAutoscaler", nil, nil, core.HealthUnknown, ""},
		{"HPA v1 supported", "autoscaling/v1", "HorizontalPodAutoscaler", nil, map[string]any{"conditions": []any{condition("ScalingActive", "False", "FailedGetResourceMetric")}}, core.HealthWarning, "FailedGetResourceMetric"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			u := mk(tc.version, tc.kind, "ns", "subject", "uid", nil)
			if tc.spec != nil {
				u.Object["spec"] = tc.spec
			}
			if tc.status != nil {
				u.Object["status"] = tc.status
			}
			h := genericHealth(u)
			require.Equal(t, tc.state, h.State)
			require.Equal(t, tc.reason, h.Reason)
			if len(h.Issues) > 0 && h.Reason != "Pending" && h.Reason != "Lost" {
				require.Equal(t, time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC).UnixMilli(), h.Issues[0].Since)
			}
		})
	}
}

func TestDiagnosisBuiltinStaleConditionsAndIndependentIssues(t *testing.T) {
	u := mk("autoscaling/v2", "HorizontalPodAutoscaler", "ns", "autoscaler", "uid", map[string]any{
		"metadata": map[string]any{"generation": int64(2)},
		"status": map[string]any{"observedGeneration": int64(1), "conditions": []any{
			map[string]any{"type": "AbleToScale", "status": "False", "reason": "FailedGetScale"},
			map[string]any{"type": "ScalingActive", "status": "False", "reason": "FailedGetResourceMetric"},
		}},
	})
	require.Equal(t, core.HealthProgressing, genericHealth(u).State)
	u.Object["status"].(map[string]any)["observedGeneration"] = int64(2)
	h := genericHealth(u)
	require.Len(t, h.Issues, 2, "independent controller conditions must not be collapsed")
	require.Equal(t, "FailedGetScale", h.Issues[0].Reason)
	require.Equal(t, "FailedGetResourceMetric", h.Issues[1].Reason)
	for _, c := range slice(u.Object, "status", "conditions") {
		c["observedGeneration"] = int64(1)
	}
	require.Equal(t, core.HealthProgressing, genericHealth(u).State)
}

func TestDiagnosisTrimPreservesOnlyBuiltinHealthInputs(t *testing.T) {
	for _, tc := range []struct{ group, kind, resource string }{
		{"batch", "Job", "jobs"}, {"", "PersistentVolumeClaim", "persistentvolumeclaims"}, {"autoscaling", "HorizontalPodAutoscaler", "horizontalpodautoscalers"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			version := "v1"
			if tc.group != "" {
				version = tc.group + "/v1"
			}
			u := mk(version, tc.kind, "ns", "x", "uid", map[string]any{
				"spec":   map[string]any{"suspend": true, "token": "private-spec", "template": map[string]any{"secret": "private-template"}},
				"status": map[string]any{"phase": "Pending", "active": int64(1), "observedGeneration": int64(2), "token": "private-status", "conditions": []any{map[string]any{"type": "Failed", "status": "True", "reason": "BackoffLimitExceeded", "lastTransitionTime": "2026-10-21T08:00:00Z"}}},
			})
			def := discoveredDef(apiResource{Group: tc.group, Version: "v1", Resource: tc.resource, Kind: tc.kind, Namespaced: true, Verbs: lw})
			sch := newTableSchema(def, 1, false, nil, nil)
			expected := genericHealth(u)
			trim(u, sch.def.keep)
			_, actual, _ := sch.project(u, time.Now())
			require.Equal(t, expected, actual, "table and Problems health share lossless minimal inputs")
			encoded, err := json.Marshal(u)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "private-")
		})
	}
	custom := mk("example.dev/v1", "Job", "ns", "custom", "uid", map[string]any{"status": map[string]any{"phase": "Lost", "failed": int64(9)}})
	require.Equal(t, core.HealthUnknown, genericHealth(custom).State, "a matching Kind name in another group is not a built-in")
	trim(custom, diagnosisKeep(schema.GroupResource{Group: "example.dev", Resource: "jobs"}))
	require.Empty(t, custom.Object["status"])
}

func TestDiagnosisProblemsObserveNewKindsAndPreserveUID(t *testing.T) {
	objects := []runtime.Object{
		mk("batch/v1", "Job", "ns", "task", "job-failed", map[string]any{"status": map[string]any{"conditions": []any{map[string]any{"type": "Failed", "status": "True", "reason": "BackoffLimitExceeded"}}}}),
		mk("batch/v1", "Job", "ns", "retrying", "job-retrying", map[string]any{"status": map[string]any{"failed": int64(3), "active": int64(1)}}),
		mk("v1", "PersistentVolumeClaim", "ns", "disk", "pvc-pending", map[string]any{"status": map[string]any{"phase": "Pending"}}),
		mk("autoscaling/v2", "HorizontalPodAutoscaler", "ns", "autoscaler", "hpa-missing", map[string]any{"status": map[string]any{"conditions": []any{map[string]any{"type": "ScalingActive", "status": "False", "reason": "FailedGetResourceMetric"}}}}),
	}
	h := newHarness(t, fullFake(objects...))
	id := h.open("problems", core.ScopeSel{Mode: core.ScopeOne, Name: "ns"})
	p := h.until(id, func(p views.Page) bool { return isReady(p) && len(p.Upserts) == 3 })
	require.ElementsMatch(t, []string{"batch/jobs#job-failed", "persistentvolumeclaims#pvc-pending", "autoscaling/horizontalpodautoscalers#hpa-missing"}, ids(p.Upserts))
	for _, row := range p.Upserts {
		require.Equal(t, "ns", row.Ref.Scope)
		require.NotEmpty(t, row.Ref.UID)
	}
}

func TestDiagnosisProblemSourcesUseDiscoveredVersion(t *testing.T) {
	s := newSession("ctx", "hash", fullFake(), false)
	t.Cleanup(s.Close)
	def := discoveredDef(apiResource{Group: "autoscaling", Version: "v1", Resource: "horizontalpodautoscalers", Kind: "HorizontalPodAutoscaler", Namespaced: true, Verbs: lw})
	s.cat.cur.Store(&catalogSnap{reg: newKindRegistry(append(append([]*kindDef{}, allKinds.list...), def)...), state: catalogReady})
	for _, src := range s.resolvedProblemSources() {
		if src.def.kind == "HorizontalPodAutoscaler" {
			require.Equal(t, "v1", src.def.gvr.Version)
		}
	}
	for _, src := range problemSources {
		if src.def.kind == "HorizontalPodAutoscaler" {
			require.Equal(t, "v2", src.def.gvr.Version, "resolution must not mutate the global defaults")
		}
	}
}
