package views

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
)

type refreshSource struct {
	mu       sync.Mutex
	sinks    []provider.Sink
	prepares int
	entered  chan struct{}
	release  chan struct{}
	failure  error
}

func (s *refreshSource) Watch(_ provider.Query, sink provider.Sink) (func(), error) {
	s.mu.Lock()
	s.sinks = append(s.sinks, sink)
	s.mu.Unlock()
	return func() {}, nil
}
func (s *refreshSource) PrepareRefresh(ctx context.Context, _ provider.Query) error {
	s.mu.Lock()
	s.prepares++
	s.mu.Unlock()
	close(s.entered)
	select {
	case <-s.release:
		return s.failure
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *refreshSource) sink(t *testing.T, n int) provider.Sink {
	t.Helper()
	var out provider.Sink
	require.Eventually(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		if len(s.sinks) > n {
			out = s.sinks[n]
		}
		return out != nil
	}, time.Second, time.Millisecond)
	return out
}
func TestRefreshStagesReplacementAndRejectsOldDeliveries(t *testing.T) {
	s := &refreshSource{entered: make(chan struct{}), release: make(chan struct{})}
	m := NewManager(events.NewEmitter())
	defer m.CloseAll()
	id, err := m.Open("owner", s, provider.Query{})
	require.NoError(t, err)
	old := s.sink(t, 0)
	old.Apply(provider.Delta{Upserts: []core.Row{row("old", "same", "old")}, Status: ready})
	done := make(chan error, 2)
	go func() { done <- m.Refresh(context.Background(), id) }()
	<-s.entered
	p, err := m.Get(id, 0)
	require.NoError(t, err)
	require.Equal(t, provider.StatusLoading, p.Status.State)
	require.Equal(t, []string{"old"}, ids(p.Upserts))
	require.EqualValues(t, 1, p.Status.Refresh)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, m.Refresh(ctx, id), context.Canceled)
	old.Apply(provider.Delta{Reset: true, Upserts: []core.Row{row("late", "late", "late")}, Status: ready})
	close(s.release)
	fresh := s.sink(t, 1)
	fresh.Apply(provider.Delta{Upserts: []core.Row{row("new", "same", "replacement")}})
	p, _ = m.Get(id, 0)
	require.Equal(t, []string{"old"}, ids(p.Upserts), "candidate rows remain private until ready")
	fresh.Apply(provider.Delta{Status: ready})
	for range 1 {
		require.NoError(t, <-done)
	}
	p, _ = m.Get(id, 0)
	require.Equal(t, []string{"new"}, ids(p.Upserts))
	require.Equal(t, provider.StatusReady, p.Status.State)
	s.mu.Lock()
	require.Equal(t, 1, s.prepares)
	s.mu.Unlock()
}
func TestRefreshFailureRetainsRowsAndCloseCancels(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "close"}[cancel], func(t *testing.T) {
			s := &refreshSource{entered: make(chan struct{}), release: make(chan struct{}), failure: &provider.Error{Class: provider.ClassForbidden, Message: "denied"}}
			m := NewManager(events.NewEmitter())
			defer m.CloseAll()
			id, err := m.Open("owner", s, provider.Query{})
			require.NoError(t, err)
			s.sink(t, 0).Apply(provider.Delta{Upserts: []core.Row{row("old", "old", "old")}, Status: ready})
			done := make(chan error, 1)
			go func() { done <- m.Refresh(context.Background(), id) }()
			<-s.entered
			if cancel {
				m.CloseOwner("owner")
				select {
				case err := <-done:
					require.Error(t, err)
				case <-time.After(time.Second):
					t.Fatal("refresh did not cancel")
				}
				return
			}
			close(s.release)
			require.Error(t, <-done)
			p, _ := m.Get(id, 0)
			require.Equal(t, []string{"old"}, ids(p.Upserts))
			require.Equal(t, provider.StatusStale, p.Status.State)
			require.Equal(t, provider.ClassForbidden, p.Status.Class)
		})
	}
}
