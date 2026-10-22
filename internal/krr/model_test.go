package krr

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	b, e := os.ReadFile("testdata/simple.json")
	require.NoError(t, e)
	return b
}
func modify(t *testing.T, fn func(map[string]any)) []byte {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(fixture(t), &m))
	fn(m)
	b, e := json.Marshal(m)
	require.NoError(t, e)
	return b
}
func TestProjectionPrivacyUnitsAndUnknowns(t *testing.T) {
	report, e := Parse(fixture(t), "1.31.0")
	require.NoError(t, e)
	require.Len(t, report.Rows, 1)
	row := report.Rows[0]
	require.Equal(t, []Quantity{{State: "number", Value: .5}, {State: "number", Value: 104857600}, {State: "unset"}, {State: "number", Value: 209715200}}, row.Current)
	require.Equal(t, []Quantity{{State: "number", Value: .125}, {State: "unavailable"}, {State: "unset"}, {State: "number", Value: 100000000}}, row.Proposed)
	require.Equal(t, []string{"WARNING", "UNKNOWN", "GOOD", "OK"}, row.Severities)
	require.Equal(t, []string{"Not enough data"}, row.Reasons)
	safe, e := json.Marshal(report)
	require.NoError(t, e)
	require.NotContains(t, string(safe), "PRIVATE-")
	require.NotContains(t, string(safe), "score")
	require.NotContains(t, string(safe), "sampleCount")
	require.Equal(t, 1, report.Errors)
}
func TestIndependentRowsAndMissingCluster(t *testing.T) {
	b := modify(t, func(m map[string]any) {
		scans := m["scans"].([]any)
		row := scans[0].(map[string]any)
		row["object"].(map[string]any)["cluster"] = nil
		m["scans"] = []any{row, row}
	})
	r, e := Parse(b, "1.31.0")
	require.NoError(t, e)
	require.Len(t, r.Rows, 2)
	require.Empty(t, r.Rows[0].Cluster)
}
func TestQuantityValidation(t *testing.T) {
	for _, raw := range []string{"-1", "1e999", "9007199254740992", "\"100m\"", "true", "{}", "NaN"} {
		t.Run(raw, func(t *testing.T) {
			b := strings.Replace(string(fixture(t)), "0.125", raw, 1)
			_, e := Parse([]byte(b), "1.31.0")
			require.ErrorIs(t, e, ErrInvalid)
		})
	}
	b := modify(t, func(m map[string]any) {
		row := m["scans"].([]any)[0].(map[string]any)
		old := row["object"].(map[string]any)["allocations"].(map[string]any)["requests"].(map[string]any)
		delete(old, "cpu")
		rec := row["recommended"].(map[string]any)["requests"].(map[string]any)["cpu"].(map[string]any)
		rec["value"] = 0
	})
	r, e := Parse(b, "1.31.0")
	require.NoError(t, e)
	require.Equal(t, "unknown", r.Rows[0].Current[0].State)
	require.Equal(t, Quantity{State: "number"}, r.Rows[0].Proposed[0])
}
func TestMalformedAndBoundedInput(t *testing.T) {
	cases := [][]byte{nil, []byte("null"), []byte("[]"), []byte("{\"scans\":[]}"), {0xff}, []byte(strings.Repeat(" ", MaxBytes+1)), []byte(strings.Replace(string(fixture(t)), `"name": "app"`, `"name": "app","\u006eame":"other"`, 1))}
	for _, b := range cases {
		_, e := Parse(b, "1.31.0")
		require.ErrorIs(t, e, ErrInvalid)
	}
	_, e := Parse(fixture(t), "1.30.0")
	require.ErrorIs(t, e, ErrInvalid)
	for _, change := range []func(map[string]any){
		func(m map[string]any) { m["strategy"].(map[string]any)["name"] = "simple_limit" },
		func(m map[string]any) {
			delete(m["strategy"].(map[string]any)["settings"].(map[string]any), "points_required")
		},
		func(m map[string]any) { m["schemaVersion"] = "2" },
		func(m map[string]any) { m["resources"] = []any{"cpu", "cpu"} },
		func(m map[string]any) {
			m["scans"].([]any)[0].(map[string]any)["object"].(map[string]any)["name"] = strings.Repeat("界", 86)
		},
		func(m map[string]any) {
			scans := m["scans"].([]any)
			for len(scans) <= MaxRows {
				scans = append(scans, scans[0])
			}
			m["scans"] = scans
		},
		func(m map[string]any) {
			var deep any = 0
			for range 34 {
				deep = []any{deep}
			}
			m["ignored"] = deep
		},
	} {
		_, e := Parse(modify(t, change), "1.31.0")
		require.ErrorIs(t, e, ErrInvalid)
	}
}
func TestKnownReasonsOnlyAndEmptyNotPass(t *testing.T) {
	b := modify(t, func(m map[string]any) {
		row := m["scans"].([]any)[0].(map[string]any)
		row["object"].(map[string]any)["hpa"] = map[string]any{"private": "PRIVATE-HPA"}
		row["object"].(map[string]any)["warnings"] = []any{"NoPrometheusPods", "PRIVATE-WARNING"}
		row["recommended"].(map[string]any)["info"].(map[string]any)["cpu"] = "PRIVATE-REASON"
	})
	r, e := Parse(b, "1.31.0")
	require.NoError(t, e)
	require.Equal(t, "present", r.Rows[0].HPA)
	require.Equal(t, []string{"NoPrometheusPods", "unknown"}, r.Rows[0].Warnings)
	require.Equal(t, []string{"unknown", "Not enough data"}, r.Rows[0].Reasons)
	r, e = Parse(modify(t, func(m map[string]any) { m["scans"] = []any{} }), "1.31.0")
	require.NoError(t, e)
	require.Empty(t, r.Rows)
}
func TestRegistryOwnsCopiesBoundsExpiryAndClose(t *testing.T) {
	now := time.Unix(100, 0)
	r := NewRegistry(func() time.Time { return now })
	data := fixture(t)
	stored, e := r.Import(t.Context(), data, "1.31.0")
	require.NoError(t, e)
	require.Len(t, r.List(), 1)
	clear(data)
	stored.Report.Rows[0].Name = "MUTATED"
	read, e := r.Get(stored.Summary.ID)
	require.NoError(t, e)
	require.Equal(t, "app", read.Report.Rows[0].Name)
	for range 7 {
		_, e = r.Import(t.Context(), fixture(t), "1.31.0")
		require.NoError(t, e)
	}
	_, e = r.Import(t.Context(), fixture(t), "1.31.0")
	require.ErrorIs(t, e, ErrLimit)
	require.NoError(t, r.Forget(stored.Summary.ID))
	_, e = r.Get(stored.Summary.ID)
	require.ErrorIs(t, e, ErrGone)
	now = now.Add(2 * time.Hour)
	require.Empty(t, r.List())
	_, e = r.Import(t.Context(), fixture(t), "1.31.0")
	require.NoError(t, e)
	r.Close()
	require.Empty(t, r.List())
	_, e = r.Import(t.Context(), fixture(t), "1.31.0")
	require.ErrorIs(t, e, ErrGone)
}
func TestRegistryRejectsCancelledBeforeRegistration(t *testing.T) {
	r := NewRegistry(time.Now)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, e := r.Import(ctx, fixture(t), "1.31.0")
	require.ErrorIs(t, e, context.Canceled)
	require.Empty(t, r.List())
}

// This fixture was emitted by the pinned OCI KRR runtime against owned fake
// Kubernetes/Prometheus sources. It is not evidence from a deployed workload.
func TestPinnedRunnerProjectionAdmission(t *testing.T) {
	data, err := os.ReadFile("testdata/runner-simple.json")
	require.NoError(t, err)
	report, err := Parse(data, "1.31.0")
	require.NoError(t, err)
	require.Len(t, report.Rows, 1)
	row := report.Rows[0]
	require.Equal(t, "app", row.Name)
	require.Equal(t, Quantity{State: "number", Value: 0.2}, row.Proposed[0])
	require.Equal(t, Quantity{State: "unset"}, row.Proposed[2])
	require.Equal(t, 1, report.Errors)
	require.NotContains(t, string(data), "PRIVATE-")
	require.NotContains(t, string(data), "clusterSummary")
	var emitted struct {
		Config map[string]float64 `json:"config"`
	}
	require.NoError(t, json.Unmarshal(data, &emitted))
	require.Equal(t, map[string]float64{"cpu_min_value": 10, "memory_min_value": 100}, emitted.Config)
	require.Equal(t, []Quantity{{State: "number", Value: 10}, {State: "number", Value: 100}}, report.Minimums)
	projected, err := json.Marshal(report)
	require.NoError(t, err)
	require.NotContains(t, string(projected), "config")
	require.NotContains(t, string(data), "annotations")
}

func TestMinimumProvenanceNeverInventsDefaults(t *testing.T) {
	report, e := Parse(fixture(t), "1.31.0")
	require.NoError(t, e)
	require.Equal(t, []Quantity{{State: "unknown"}, {State: "unknown"}}, report.Minimums)
	data := modify(t, func(m map[string]any) {
		m["config"] = map[string]any{"cpu_min_value": 0, "memory_min_value": 100, "token": "PRIVATE-TOKEN"}
	})
	report, e = Parse(data, "1.31.0")
	require.NoError(t, e)
	require.Equal(t, []Quantity{{State: "number"}, {State: "number", Value: 100}}, report.Minimums)
	safe, e := json.Marshal(report)
	require.NoError(t, e)
	require.NotContains(t, string(safe), "PRIVATE")
	for _, value := range []any{-1, 0.5, 1e10, nil, "100", true} {
		data = modify(t, func(m map[string]any) { m["config"] = map[string]any{"cpu_min_value": value} })
		_, e = Parse(data, "1.31.0")
		require.ErrorIs(t, e, ErrInvalid)
	}
}
