package kubernetes

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// kindTableView opens a discovered kind's view like OpenView does.
func kindTableView(t *testing.T, s *session, kind string, scope core.ScopeSel) (core.KindDescriptor, *deltaSink) {
	t.Helper()
	require.Eventually(t, func() bool { return s.kind(kind) != nil }, 30*time.Second, 20*time.Millisecond, "%s discovered", kind)
	desc, q, err := s.DescribeView(context.Background(), provider.Query{Kind: kind, Scope: scope})
	require.NoError(t, err)
	sink := &deltaSink{}
	stop, err := s.Watch(q, sink)
	require.NoError(t, err)
	t.Cleanup(stop)
	require.Eventually(t, func() bool { st := sink.status(); return st != nil && st.State == provider.StatusReady }, 30*time.Second, 20*time.Millisecond)
	return desc, sink
}

func rowByName(sink *deltaSink, name string) (core.Row, bool) {
	for _, r := range sink.rows() {
		if r.Ref.Name == name {
			return r, true
		}
	}
	return core.Row{}, false
}

// Discovered kinds show the server's columns (like kubectl get), typed,
// with health from their conditions, and follow changes live.
func TestKindTableColumnsLikeKubectl(t *testing.T) {
	p, target := kindProvider(t)
	sess, err := p.Open(context.Background(), target)
	require.NoError(t, err)
	defer sess.Close()
	s := sess.(*session)
	ns := core.ScopeSel{Mode: core.ScopeOne, Name: "ocular-crd"}

	desc, sink := kindTableView(t, s, "ocular.dev/widgets", ns)
	assert.Equal(t, []string{"name", "namespace", "size", "phase", "ready", "since"}, colIDs(desc.Columns), "Detail is wide")
	assert.Equal(t, core.ColNumber, desc.Columns[2].Type)
	assert.Equal(t, core.ColAge, desc.Columns[5].Type, "Since is .metadata.creationTimestamp in the CRD")
	alpha, ok := rowByName(sink, "alpha")
	require.True(t, ok)
	assert.Equal(t, 3.0, *alpha.Cells[2].Num)
	assert.Equal(t, "Running", alpha.Cells[3].Text)
	assert.Equal(t, "True", alpha.Cells[4].Text)
	assert.NotZero(t, alpha.Cells[5].Time)
	assert.Equal(t, core.HealthOK, alpha.Health.State)
	beta, _ := rowByName(sink, "beta")
	assert.Equal(t, core.HealthError, beta.Health.State)
	assert.Equal(t, "Broken", beta.Health.Reason)
	gamma, _ := rowByName(sink, "gamma")
	assert.Equal(t, core.HealthProgressing, gamma.Health.State, "a stale Ready says nothing")

	// live: a change of the object reaches the row (status: no new generation)
	w := s.dyn.Resource(widgetsGVR).Namespace("ocular-crd")
	phase := func(p string) {
		_, err := w.Patch(context.Background(), "alpha", types.MergePatchType, []byte(`{"status":{"phase":"`+p+`"}}`), metav1.PatchOptions{}, "status")
		require.NoError(t, err)
	}
	phase("Paused")
	t.Cleanup(func() { phase("Running") })
	require.Eventually(t, func() bool {
		r, _ := rowByName(sink, "alpha")
		return r.Cells[3].Text == "Paused"
	}, 30*time.Second, 20*time.Millisecond)

	desc, sink = kindTableView(t, s, "batch/jobs", ns)
	assert.Equal(t, []string{"name", "namespace", "status", "completions", "duration", "age"}, colIDs(desc.Columns))
	assert.Equal(t, core.ColAge, desc.Columns[5].Type, "a built-in's Age")
	once, ok := rowByName(sink, "once")
	require.True(t, ok)
	assert.Equal(t, "Complete", once.Cells[2].Text)
	assert.Equal(t, "1/1", once.Cells[3].Text)

	desc, _ = kindTableView(t, s, "persistentvolumeclaims", ns)
	assert.Contains(t, colIDs(desc.Columns), "status")
	assert.Equal(t, "age", colIDs(desc.Columns)[len(desc.Columns)-1])

	desc, sink = kindTableView(t, s, "ocular.dev/gadgets", core.ScopeSel{Mode: core.ScopeAll})
	assert.Equal(t, []string{"name", "age"}, colIDs(desc.Columns), "cluster-scoped, no columns")
	_, ok = rowByName(sink, "g1")
	assert.True(t, ok)
}
