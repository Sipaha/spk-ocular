package provider

import (
	"sync"
	"testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type unionRecorder struct {
	rows     map[string]core.Row
	status   ViewStatus
	statuses []ViewStatus
	calls    int
}

func (s *unionRecorder) Apply(d Delta) {
	if s.rows == nil || d.Reset {
		s.rows = map[string]core.Row{}
	}
	for _, row := range d.Upserts {
		s.rows[row.ID] = row
	}
	for _, id := range d.Deletes {
		delete(s.rows, id)
	}
	if d.Status != nil {
		s.status = *d.Status
		s.statuses = append(s.statuses, *d.Status)
	}
	s.calls++
}

func TestScopeUnionSharedRowsResetCoverageAndStop(t *testing.T) {
	sink := &unionRecorder{}
	feeds := map[string]Sink{}
	stopped := 0
	q := Query{Kind: "things", Scope: core.ScopeSel{Mode: core.ScopeSome, Names: []string{"b", "a", "a"}}, Schema: 7, Name: "x"}
	stop, err := WatchScopes(q, sink, func(child Query, feed Sink) (func(), error) {
		require.Equal(t, core.ScopeOne, child.Scope.Mode)
		require.Equal(t, uint64(7), child.Schema)
		require.Equal(t, "x", child.Name)
		feeds[child.Scope.Name] = feed
		return func() { stopped++ }, nil
	})
	require.NoError(t, err)
	require.Len(t, feeds, 2)
	initial := sink.status
	ready := &ViewStatus{State: StatusReady}
	feeds["a"].Apply(Delta{Reset: true, Upserts: []core.Row{{ID: "shared"}, {ID: "a"}}, Status: ready})
	assert.Equal(t, StatusLoading, sink.status.State)
	feeds["b"].Apply(Delta{Reset: true, Upserts: []core.Row{{ID: "shared"}, {ID: "b"}}, Status: ready})
	assert.Equal(t, StatusReady, sink.status.State)
	assert.Len(t, sink.rows, 3)
	feeds["a"].Apply(Delta{Reset: true, Upserts: []core.Row{{ID: "new"}}})
	assert.Contains(t, sink.rows, "shared")
	assert.NotContains(t, sink.rows, "a")
	feeds["b"].Apply(Delta{Deletes: []string{"shared"}})
	assert.NotContains(t, sink.rows, "shared")
	feeds["b"].Apply(Delta{Status: &ViewStatus{State: StatusError, Class: ClassForbidden, Message: "denied"}})
	assert.Equal(t, StatusReady, sink.status.State)
	assert.Equal(t, CoverageDenied, sink.status.Coverage[1].State)
	assert.Equal(t, CoverageLoading, initial.Coverage[1].State, "previous deliveries are immutable")
	stop()
	stop()
	assert.Equal(t, 2, stopped)
	calls := sink.calls
	feeds["a"].Apply(Delta{Upserts: []core.Row{{ID: "late"}}})
	assert.Equal(t, calls, sink.calls)
}

func TestScopeUnionEmptyAndFailedStarts(t *testing.T) {
	sink := &unionRecorder{}
	stop, err := WatchScopes(Query{Scope: core.ScopeSel{Mode: core.ScopeSome}}, sink, func(Query, Sink) (func(), error) { t.Fatal("empty set opened a watch"); return nil, nil })
	require.NoError(t, err)
	stop()
	assert.Equal(t, StatusReady, sink.status.State)
	q := Query{Scope: core.ScopeSel{Mode: core.ScopeSome, Names: []string{"a", "b"}}}
	for _, class := range []ErrorClass{ClassForbidden, ClassSchemaChanged} {
		stops := 0
		stop, err = WatchScopes(q, sink, func(child Query, feed Sink) (func(), error) {
			if child.Scope.Name == "b" {
				return nil, &Error{Class: class, Message: "failure"}
			}
			feed.Apply(Delta{Status: &ViewStatus{State: StatusReady}})
			return func() { stops++ }, nil
		})
		if class == ClassForbidden {
			require.NoError(t, err)
			assert.Equal(t, CoverageDenied, sink.status.Coverage[1].State)
			stop()
		} else {
			require.Error(t, err)
		}
		assert.Equal(t, 1, stops, "started children always released")
	}
}

func TestScopeUnionConcurrentDeliveriesAndTerminalStatus(t *testing.T) {
	sink := &unionRecorder{}
	var feeds []Sink
	stop, err := WatchScopes(Query{Scope: core.ScopeSel{Mode: core.ScopeSome, Names: []string{"a", "b"}}}, sink, func(_ Query, feed Sink) (func(), error) { feeds = append(feeds, feed); return func() {}, nil })
	require.NoError(t, err)
	var wg sync.WaitGroup
	for _, feed := range feeds {
		wg.Go(func() {
			for range 100 {
				feed.Apply(Delta{Upserts: []core.Row{{ID: "shared"}}, Status: &ViewStatus{State: StatusReady}})
			}
		})
	}
	wg.Wait()
	assert.Len(t, sink.rows, 1)
	feeds[0].Apply(Delta{Status: &ViewStatus{State: StatusError, Class: ClassSchemaChanged}})
	feeds[1].Apply(Delta{Status: &ViewStatus{State: StatusReady}})
	assert.Equal(t, ClassSchemaChanged, sink.status.Class, "one changed schema ends the entire view")
	wg.Go(func() { feeds[0].Apply(Delta{Deletes: []string{"shared"}}) })
	stop()
	wg.Wait()
}
