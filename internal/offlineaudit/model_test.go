package offlineaudit

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixture() []byte {
	return []byte(`{"PolarisOutputVersion":"1.0","AuditTime":"2026-10-22T00:00:00Z","SourceType":"Cluster","SourceName":"PRIVATE-SOURCE","Score":100,"Results":[{"Name":"web","Namespace":"blue","Kind":"Deployment","UID":"PRIVATE-UID","Results":{"cpuRequestsMissing":{"ID":"cpuRequestsMissing","Success":false,"Severity":"warning","Category":"Resources","Message":"PRIVATE-MESSAGE","Details":["PRIVATE-DETAIL"],"Mutations":[{"value":"PRIVATE-MUTATION"}]}},"PodResult":{"Results":{},"ContainerResults":[{"Name":"app","Results":{"readinessProbeMissing":{"ID":"readinessProbeMissing","Success":true,"Severity":"danger","Category":"Health Checks"}}}]}}],"manifest":{"env":"PRIVATE-ENV"}}`)
}
func TestProjectionKeepsSourceNativeScopeWithoutPayloadOrScore(t *testing.T) {
	r, e := Parse(fixture(), "10.2.5")
	require.NoError(t, e)
	require.Equal(t, 2, r.Checks)
	require.Len(t, r.Subjects, 1)
	checks := r.Subjects[0].Checks
	require.False(t, checks[0].Success)
	require.Equal(t, "resource", checks[0].Scope)
	require.Equal(t, "warning", checks[0].Severity)
	require.True(t, checks[1].Success)
	require.Equal(t, "container", checks[1].Scope)
	require.Equal(t, "app", checks[1].Container)
	data, e := json.Marshal(r)
	require.NoError(t, e)
	for _, absent := range []string{"PRIVATE-", "Score", "score", "Message", "Mutations", "UID"} {
		require.NotContains(t, string(data), absent)
	}
	empty, e := Parse([]byte(`{"PolarisOutputVersion":"1.0","SourceType":"Path","AuditTime":"0001-01-01T00:00:00Z","Results":[]}`), "10.2.5")
	require.NoError(t, e)
	require.Empty(t, empty.AuditTime)
	require.Empty(t, empty.Subjects)
	ignored := strings.ReplaceAll(string(fixture()), `"Severity":"warning"`, `"Severity":"ignore"`)
	r, e = Parse([]byte(ignored), "10.2.5")
	require.NoError(t, e)
	require.Equal(t, "ignore", r.Subjects[0].Checks[0].Severity)
	require.False(t, r.Subjects[0].Checks[0].Success)
	unknown := strings.ReplaceAll(string(fixture()), `"Category":"Resources"`, `"Category":"PRIVATE-CATEGORY"`)
	r, e = Parse([]byte(unknown), "10.2.5")
	require.NoError(t, e)
	require.Equal(t, "unknown", r.Subjects[0].Checks[0].Category)
}
func TestStrictAdmissionRejectsAmbiguityAndSensitiveErrors(t *testing.T) {
	valid := string(fixture())
	cases := []string{
		"", valid + "{}", strings.Replace(valid, `"1.0"`, `"2.0"`, 1), strings.Replace(valid, `"Success":false`, `"Success":null`, 1), strings.Replace(valid, `"Severity":"warning"`, `"Severity":"error"`, 1), strings.Replace(valid, `"ID":"cpuRequestsMissing"`, `"ID":"different"`, 1), strings.Replace(valid, `"Namespace":"blue"`, `"Namespace":"../PRIVATE-NAMESPACE"`, 1), strings.Replace(valid, `"Name":"web"`, `"Name":"web..replacement"`, 1), strings.Replace(valid, `"Results":[`, `"Results":null,"duplicate":[`, 1), strings.Replace(valid, `"Score":100`, `"Score":100,"Score":0`, 1), strings.Replace(valid, `"Score":100`, `"Score":100,"Sc\u006fre":0`, 1), strings.Replace(valid, `"Success":false`, `"Success":false,"Success":true`, 1), strings.Replace(valid, `"manifest":{`, `"manifest":{"x":1,"x":2,`, 1), strings.Replace(valid, `"Score":100`, `"version":"old","Score":100`, 1), strings.Repeat(" ", MaxBytes+1), string([]byte{255}), `{"PRIVATE-ERROR":`,
	}
	for i, b := range cases {
		_, e := Parse([]byte(b), "10.2.5")
		require.Error(t, e, "case %d", i)
		require.NotContains(t, e.Error(), "PRIVATE-")
	}
	for _, version := range []string{"", "old", "1.0", "v10.2.5"} {
		_, e := Parse(fixture(), version)
		require.Error(t, e)
	}
	// Bounds apply inside discarded source fields too.
	deep := strings.Replace(valid, `"manifest":{"env":"PRIVATE-ENV"}`, `"manifest":`+strings.Repeat("[", 33)+"0"+strings.Repeat("]", 33), 1)
	_, e := Parse([]byte(deep), "10.2.5")
	require.Error(t, e)
	many := strings.Replace(valid, `"manifest":{"env":"PRIVATE-ENV"}`, `"manifest":[`+strings.Repeat("0,", 100000)+"0]", 1)
	_, e = Parse([]byte(many), "10.2.5")
	require.Error(t, e)
}
func TestOccurrenceBudgetsAndNoSameNameMerge(t *testing.T) {
	var source map[string]any
	require.NoError(t, json.Unmarshal(fixture(), &source))
	subject := source["Results"].([]any)[0]
	source["Results"] = []any{subject, subject}
	b, _ := json.Marshal(source)
	r, e := Parse(b, "10.2.5")
	require.NoError(t, e)
	require.Len(t, r.Subjects, 2)
	require.Equal(t, 4, r.Checks)
	objects := make([]any, MaxObjects+1)
	for i := range objects {
		objects[i] = subject
	}
	source["Results"] = objects
	b, _ = json.Marshal(source)
	_, e = Parse(b, "10.2.5")
	require.Error(t, e)
	source["Results"] = []any{subject}
	m := subject.(map[string]any)
	p := m["PodResult"].(map[string]any)
	container := p["ContainerResults"].([]any)[0]
	cs := make([]any, 17)
	for i := range cs {
		cs[i] = container
	}
	p["ContainerResults"] = cs
	b, _ = json.Marshal(source)
	_, e = Parse(b, "10.2.5")
	require.Error(t, e)
}
func TestRegistryCopiesLimitsCancellationExpiryAndShutdown(t *testing.T) {
	now := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	r := NewRegistry(func() time.Time { return now })
	ctx := context.Background()
	stored, e := r.Import(ctx, fixture(), "10.2.5", "fixture-target", "config-revision")
	require.NoError(t, e)
	id := stored.Summary.ID
	stored.Report.Subjects[0].Checks[0].ID = "changed"
	got, e := r.Get(id)
	require.NoError(t, e)
	require.Equal(t, "cpuRequestsMissing", got.Report.Subjects[0].Checks[0].ID)
	c, cancel := context.WithCancel(ctx)
	cancel()
	_, e = r.Import(c, fixture(), "10.2.5", "t", "r")
	require.ErrorIs(t, e, context.Canceled)
	require.Len(t, r.List(), 1)
	for range 7 {
		_, e = r.Import(ctx, fixture(), "10.2.5", "t", "r")
		require.NoError(t, e)
	}
	_, e = r.Import(ctx, fixture(), "10.2.5", "t", "r")
	require.ErrorIs(t, e, ErrLimit)
	require.Len(t, r.List(), 8)
	require.NoError(t, r.Forget(id))
	_, e = r.Get(id)
	require.ErrorIs(t, e, ErrGone)
	now = now.Add(2 * time.Hour)
	require.Empty(t, r.List())
	_, e = r.Get(id)
	require.Error(t, e)
	r.Close()
	_, e = r.Import(ctx, fixture(), "10.2.5", "t", "r")
	require.ErrorIs(t, e, ErrGone)
	require.Empty(t, r.List())
}
func TestRegistryParallelImportsRespectSharedLimit(t *testing.T) {
	r := NewRegistry(time.Now)
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() { _, _ = r.Import(context.Background(), fixture(), "10.2.5", "t", "r") })
	}
	wg.Wait()
	require.LessOrEqual(t, len(r.List()), 8)
	r.slots <- struct{}{}
	r.slots <- struct{}{}
	_, e := r.Import(context.Background(), fixture(), "10.2.5", "t", "r")
	require.ErrorIs(t, e, ErrLimit)
	<-r.slots
	<-r.slots
}

// Optional local proof reads an independently pinned upstream example from
// solution scratch; normal checks never require private files or a scanner.
func TestPinnedUpstreamExampleWhenExplicitlyProvided(t *testing.T) {
	path := os.Getenv("OCULAR_POLARIS_SCHEMA_EXAMPLE")
	if path == "" {
		t.Skip("no explicit upstream schema example")
	}
	b, e := os.ReadFile(path)
	require.NoError(t, e)
	r, e := Parse(b, "10.2.5")
	require.NoError(t, e)
	require.Len(t, r.Subjects, 5)
	require.Equal(t, 76, r.Checks)
	require.Empty(t, r.AuditTime)
}

func TestCheckCountAndProjectedAggregateBudgets(t *testing.T) {
	source := map[string]any{"PolarisOutputVersion": "1.0", "SourceType": "Cluster"}
	containers := []any{}
	for ci := range 16 {
		checks := map[string]any{}
		for i := range 128 {
			id := fmt.Sprintf("rule%03d", i) + strings.Repeat("x", 121)
			checks[id] = map[string]any{"ID": id, "Success": false, "Severity": "warning", "Category": "Health Checks"}
		}
		containers = append(containers, map[string]any{"Name": fmt.Sprintf("app%d", ci) + strings.Repeat("x", 58), "Results": checks})
	}
	obj := map[string]any{"Name": "web", "Namespace": "blue", "Kind": "Deployment", "Results": map[string]any{}, "PodResult": map[string]any{"Results": map[string]any{}, "ContainerResults": containers}}
	source["Results"] = []any{obj}
	data, e := json.Marshal(source)
	require.NoError(t, e)
	require.LessOrEqual(t, len(data), MaxBytes)
	parsed, e := Parse(data, "10.2.5")
	require.NoError(t, e)
	require.Equal(t, MaxChecks, parsed.Checks)
	registry := NewRegistry(time.Now)
	accepted := 0
	for range 8 {
		_, e = registry.Import(context.Background(), data, "10.2.5", "t", "r")
		if e != nil {
			require.ErrorIs(t, e, ErrLimit)
			break
		}
		accepted++
	}
	require.Greater(t, accepted, 0)
	require.Less(t, accepted, 8, "4MiB projected budget must reject before the count-only limit")
	obj["Results"] = map[string]any{"extra": map[string]any{"ID": "extra", "Success": true, "Severity": "warning"}}
	data, e = json.Marshal(source)
	require.NoError(t, e)
	_, e = Parse(data, "10.2.5")
	require.Error(t, e)
}
