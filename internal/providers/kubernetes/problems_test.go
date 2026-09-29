package kubernetes

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stesting "k8s.io/client-go/testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/views"
)

func healthRow(id string, state core.HealthState) core.Row {
	return core.Row{ID: id, Ref: core.Ref{Kind: "pods", Name: id, UID: id}, Health: core.Health{State: state}}
}

// feeds builds a Problems merge of n sources over a recording sink.
func feeds(n int) (*problemsView, []*problemsFeed, *recordingSink) {
	rec := &recordingSink{}
	v := &problemsView{sink: rec, cov: make([]provider.SourceCoverage, n), emitted: make([]map[string]bool, n)}
	out := make([]*problemsFeed, n)
	for i := range out {
		v.cov[i] = provider.SourceCoverage{Source: string(rune('A' + i)), State: provider.CoverageLoading}
		v.emitted[i] = map[string]bool{}
		out[i] = &problemsFeed{v: v, i: i, prefix: string(rune('a'+i)) + "#"}
	}
	return v, out, rec
}

func TestProblemsFeedFiltersAndRenames(t *testing.T) {
	_, f, rec := feeds(1)
	f[0].Apply(provider.Delta{Upserts: []core.Row{healthRow("bad", core.HealthError), healthRow("fine", core.HealthOK), healthRow("rolling", core.HealthProgressing)}})
	require.Len(t, rec.d, 1)
	require.Len(t, rec.d[0].Upserts, 1)
	assert.Equal(t, "a#bad", rec.d[0].Upserts[0].ID)
	assert.Equal(t, "bad", rec.d[0].Upserts[0].Ref.UID, "the row still points at the object")

	f[0].Apply(provider.Delta{Upserts: []core.Row{healthRow("fine", core.HealthOK)}})
	assert.Len(t, rec.d, 1, "a row that never was a problem changes nothing")

	f[0].Apply(provider.Delta{Upserts: []core.Row{healthRow("bad", core.HealthOK)}})
	assert.Equal(t, []string{"a#bad"}, rec.d[1].Deletes, "recovered: gone from Problems")

	f[0].Apply(provider.Delta{Upserts: []core.Row{healthRow("bad", core.HealthWarning)}})
	f[0].Apply(provider.Delta{Deletes: []string{"bad", "never"}})
	assert.Equal(t, []string{"a#bad"}, rec.d[3].Deletes)
}

// A source's Reset replaces only its own rows: passed on as a Reset it
// would erase every other source's.
func TestProblemsSourceResetKeepsOtherSources(t *testing.T) {
	_, f, rec := feeds(2)
	f[0].Apply(provider.Delta{Upserts: []core.Row{healthRow("x", core.HealthError), healthRow("y", core.HealthError)}})
	f[1].Apply(provider.Delta{Upserts: []core.Row{healthRow("z", core.HealthWarning)}})
	f[0].Apply(provider.Delta{Reset: true, Upserts: []core.Row{healthRow("y", core.HealthError), healthRow("w", core.HealthUnknown)}})
	last := rec.d[len(rec.d)-1]
	assert.False(t, last.Reset)
	assert.ElementsMatch(t, []string{"a#y", "a#w"}, ids(last.Upserts))
	assert.Equal(t, []string{"a#x"}, last.Deletes)

	f[0].Apply(provider.Delta{Reset: true})
	last = rec.d[len(rec.d)-1]
	assert.ElementsMatch(t, []string{"a#y", "a#w"}, last.Deletes, "an empty reset of one source; b#z untouched")
}

func ids(rows []core.Row) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

func TestProblemsStatusIsOneAggregateAndLateDeliveriesAreDropped(t *testing.T) {
	v, f, rec := feeds(2)
	ready := provider.ViewStatus{State: provider.StatusReady}
	f[0].Apply(provider.Delta{Status: &ready})
	require.Len(t, rec.d, 1)
	assert.Equal(t, provider.StatusLoading, rec.d[0].Status.State, "B still loads")
	f[0].Apply(provider.Delta{Status: &ready})
	assert.Len(t, rec.d, 1, "the same coverage again: nothing")
	denied := provider.ViewStatus{State: provider.StatusError, Class: provider.ClassForbidden, Message: "nodes is forbidden"}
	f[1].Apply(provider.Delta{Status: &denied})
	st := rec.d[1].Status
	assert.Equal(t, provider.StatusReady, st.State)
	assert.Equal(t, []provider.SourceCoverage{{Source: "A", State: provider.CoverageReady},
		{Source: "B", State: provider.CoverageDenied, Class: provider.ClassForbidden, Message: "nodes is forbidden"}}, st.Coverage)

	st.Coverage[0].State = provider.CoverageError // the view's copy, not ours
	assert.Equal(t, provider.CoverageReady, v.cov[0].State)

	v.mu.Lock()
	v.stopped = true
	v.mu.Unlock()
	f[0].Apply(provider.Delta{Upserts: []core.Row{healthRow("late", core.HealthError)}, Status: &denied})
	assert.Len(t, rec.d, 2, "after close nothing is delivered")
}

func TestProblemsAggregateStatus(t *testing.T) {
	c := func(states ...provider.CoverageState) []provider.SourceCoverage {
		out := []provider.SourceCoverage{}
		for _, s := range states {
			cc := provider.SourceCoverage{Source: "s", State: s}
			if s == provider.CoverageError {
				cc.Class, cc.Message = provider.ClassUnavailable, "down"
			}
			out = append(out, cc)
		}
		return out
	}
	L, R, S, D, E := provider.CoverageLoading, provider.CoverageReady, provider.CoverageStale, provider.CoverageDenied, provider.CoverageError
	cases := []struct {
		cov   []provider.SourceCoverage
		state provider.StatusState
		class provider.ErrorClass
	}{
		{c(L, R, R), provider.StatusLoading, ""},
		{c(L, D, E), provider.StatusLoading, ""},
		{c(R, S, D, E), provider.StatusReady, ""},
		{c(S, D), provider.StatusStale, ""},
		{c(S, S), provider.StatusStale, ""},
		{c(D, D), provider.StatusError, provider.ClassForbidden},
		{c(D, E), provider.StatusError, provider.ClassUnavailable},
	}
	for _, tc := range cases {
		st := aggregateStatus(tc.cov)
		assert.Equal(t, tc.state, st.State, "%v", tc.cov)
		assert.Equal(t, tc.class, st.Class, "%v", tc.cov)
		assert.Len(t, st.Coverage, len(tc.cov))
	}
}

func problemsFixture() []runtime.Object {
	now := time.Now()
	at := func(d time.Duration) string { return now.Add(-d).UTC().Format(time.RFC3339) }
	crash := mk("v1", "Pod", "web", "crash", "u-crash", map[string]any{
		"spec": map[string]any{"containers": []any{map[string]any{"name": "app"}}},
		"status": map[string]any{"phase": "Running", "containerStatuses": []any{map[string]any{"name": "app", "restartCount": int64(5),
			"state": map[string]any{"waiting": map[string]any{"reason": "CrashLoopBackOff"}}}}}})
	fine := mk("v1", "Pod", "web", "fine", "u-fine", map[string]any{
		"spec":   map[string]any{"containers": []any{map[string]any{"name": "app"}}},
		"status": map[string]any{"phase": "Running", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}}})
	down := mk("apps/v1", "Deployment", "web", "api", "u-api", map[string]any{
		"metadata": map[string]any{"generation": int64(1)},
		"spec":     map[string]any{"replicas": int64(2)},
		"status":   map[string]any{"replicas": int64(2), "updatedReplicas": int64(2), "availableReplicas": int64(0), "observedGeneration": int64(1)}})
	node := mk("v1", "Node", "", "n1", "u-n1", map[string]any{"status": map[string]any{"conditions": []any{
		map[string]any{"type": "Ready", "status": "Unknown", "lastTransitionTime": at(time.Hour)}}}})
	warn := mk("v1", "Event", "web", "crash.1", "u-ev", map[string]any{"type": "Warning", "reason": "BackOff", "message": "back-off",
		"count": int64(3), "lastTimestamp": at(time.Minute),
		"involvedObject": map[string]any{"apiVersion": "v1", "kind": "Pod", "name": "crash", "uid": "u-crash", "namespace": "web"}})
	old := mk("v1", "Event", "web", "crash.0", "u-old", map[string]any{"type": "Warning", "reason": "Failed", "lastTimestamp": at(time.Hour),
		"involvedObject": map[string]any{"kind": "Pod", "name": "crash"}})
	normal := mk("v1", "Event", "web", "crash.2", "u-norm", map[string]any{"type": "Normal", "reason": "Pulled", "lastTimestamp": at(time.Minute)})
	return []runtime.Object{crash, fine, down, node, warn, old, normal}
}

func rowByID(p views.Page, id string) (core.Row, bool) {
	for _, r := range p.Upserts {
		if r.ID == id {
			return r, true
		}
	}
	return core.Row{}, false
}

func TestProblemsViewShowsCurrentProblemsAndRecentEvidence(t *testing.T) {
	h := newHarness(t, fullFake(problemsFixture()...))
	id := h.open("problems", all)
	p := h.until(id, func(p views.Page) bool { return isReady(p) && len(p.Upserts) == 4 })
	assert.ElementsMatch(t, []string{"pods#u-crash", "apps/deployments#u-api", "nodes#u-n1", "events#u-ev"}, ids(p.Upserts))

	crash, _ := rowByID(p, "pods#u-crash")
	assert.Equal(t, core.Ref{Provider: ProviderID, Target: "ctx", Scope: "web", Kind: "pods", Name: "crash", UID: "u-crash"}, crash.Ref)
	assert.Equal(t, []string{"error", "Pod", "web", "crash", "CrashLoopBackOff"}, texts(crash.Cells[:5]))
	assert.Equal(t, 4.0, *crash.Cells[0].Num)

	ev, _ := rowByID(p, "events#u-ev")
	assert.Equal(t, "events", ev.Ref.Kind, "the evidence opens as the event itself")
	assert.Equal(t, []string{"recent", "Event", "web", "pod/crash", "BackOff", "back-off (3 in total)"}, texts(ev.Cells[:6]))
	assert.NotZero(t, ev.Cells[6].Time)

	node, _ := rowByID(p, "nodes#u-n1")
	assert.Equal(t, "unknown", node.Cells[0].Text)

	for _, c := range p.Status.Coverage {
		assert.Equal(t, provider.CoverageReady, c.State, c.Source)
	}
	assert.Len(t, p.Status.Coverage, len(problemSources))
}

func texts(cells []core.Cell) []string {
	out := []string{}
	for _, c := range cells {
		out = append(out, c.Text)
	}
	return out
}

// Recent evidence leaves by the projection's own deadline: no API event.
func TestProblemsEventEvidenceExpires(t *testing.T) {
	ev := mk("v1", "Event", "web", "e", "u-e", map[string]any{"type": "Warning", "reason": "BackOff",
		"lastTimestamp": time.Now().Add(-eventRecent + 1500*time.Millisecond).UTC().Format(time.RFC3339)})
	h := newHarness(t, fullFake(ev))
	id := h.open("problems", all)
	h.until(id, func(p views.Page) bool { return isReady(p) && len(p.Upserts) == 1 })
	h.until(id, func(p views.Page) bool { return len(p.Upserts) == 0 })
}

func TestProblemsCoverageSaysWhatIsNotObserved(t *testing.T) {
	client := fullFake(problemsFixture()...)
	client.PrependReactor("list", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "nodes"}, "", errors.New("rbac"))
	})
	h := newHarness(t, client)
	id := h.open("problems", core.ScopeSel{Mode: core.ScopeOne, Name: "web"})
	p := h.until(id, func(p views.Page) bool { return isReady(p) && len(p.Upserts) == 3 })
	var nodes provider.SourceCoverage
	for _, c := range p.Status.Coverage {
		if c.Source == "Nodes (cluster-wide)" {
			nodes = c
		}
	}
	assert.Equal(t, provider.CoverageDenied, nodes.State, "%+v", p.Status.Coverage)
	assert.Equal(t, provider.ClassForbidden, nodes.Class)
}

func TestProblemsNothingObservableIsAnError(t *testing.T) {
	client := fullFake()
	client.PrependReactor("list", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "x"}, "", errors.New("rbac"))
	})
	h := newHarness(t, client)
	id := h.open("problems", all)
	p := h.until(id, func(p views.Page) bool { return p.Status.State == provider.StatusError })
	assert.Equal(t, provider.ClassForbidden, p.Status.Class)
}

// A table and Problems share an informer but hold a lease each: closing one
// leaves the other working.
func TestProblemsShareCachesWithTablesAndReleaseIndependently(t *testing.T) {
	h := newHarness(t, fullFake(problemsFixture()...))
	pods := h.open("pods", all)
	h.until(pods, isReady)
	probs := h.open("problems", all)
	h.until(probs, isReady)
	assert.Equal(t, len(problemSources), h.sess.Stats()["caches_active"], "pods shared: one cache per source")

	h.m.Close(probs)
	assert.Equal(t, 1, h.sess.Stats()["caches_active"], "the table keeps its pods cache")
	h.until(pods, func(p views.Page) bool { return isReady(p) && len(p.Upserts) == 2 })
}

// A failure while opening the sources releases the ones already opened.
func TestProblemsSetupFailureReleasesOpenedSources(t *testing.T) {
	s := newSession("ctx", "h", fullFake(), false)
	t.Cleanup(s.Close)
	s.problemSources = []problemSource{problemSources[0], {def: problemsKind, title: "broken"}}
	_, err := s.Watch(provider.Query{Kind: "problems", Scope: all}, &recordingSink{})
	require.Error(t, err)
	assert.Equal(t, 0, s.Stats()["caches_active"])
}

func TestProblemsCannotBeNarrowed(t *testing.T) {
	s := newSession("ctx", "h", fullFake(), false)
	t.Cleanup(s.Close)
	_, err := s.Watch(provider.Query{Kind: "problems", Scope: all, Name: "x"}, &recordingSink{})
	assertClass(t, err, provider.ClassUnsupported)
	_, err = s.Get(context.Background(), core.Ref{Kind: "problems", Name: "x"})
	assertClass(t, err, provider.ClassUnsupported)
}
