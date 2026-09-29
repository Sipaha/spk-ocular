package events

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func waitWake(t *testing.T, s *Subscription) {
	t.Helper()
	select {
	case <-s.Wake():
	case <-time.After(2 * time.Second):
		t.Fatal("no wake")
	}
}

func TestEmitDeliversToAllSubscribers(t *testing.T) {
	e := NewEmitter()
	a, unsubA := e.Subscribe()
	b, unsubB := e.Subscribe()
	defer unsubA()
	defer unsubB()
	e.Emit(Event{Type: "x", Payload: map[string]any{"n": 1}})
	for _, s := range []*Subscription{a, b} {
		waitWake(t, s)
		got := s.Drain()
		require.Len(t, got, 1)
		assert.Equal(t, "x", got[0].Type)
	}
}

func TestLatestPerKeyWinsAndOrderIsFirstArrival(t *testing.T) {
	e := NewEmitter()
	s, unsub := e.Subscribe()
	defer unsub()
	e.Emit(Event{Type: "view_changed", Key: "1", Payload: map[string]any{"v": 1}})
	e.Emit(Event{Type: "view_changed", Key: "2", Payload: map[string]any{"v": 10}})
	e.Emit(Event{Type: "view_changed", Key: "1", Payload: map[string]any{"v": 3}})
	e.Emit(Event{Type: "targets_changed"})
	waitWake(t, s)
	got := s.Drain()
	require.Len(t, got, 3)
	assert.Equal(t, 3, got[0].Payload["v"], "key 1 keeps its place, carries the latest payload")
	assert.Equal(t, 10, got[1].Payload["v"])
	assert.Equal(t, "targets_changed", got[2].Type)
	assert.Empty(t, s.Drain())
}

// The defect this design fixes: with a bounded channel that drops on full,
// the last invalidation could be lost and the UI stay stale forever.
func TestFinalInvalidationSurvivesASlowConsumer(t *testing.T) {
	e := NewEmitter()
	s, unsub := e.Subscribe()
	defer unsub()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100_000; i++ {
			e.Emit(Event{Type: "targets_changed", Key: "kubernetes", Payload: map[string]any{"n": i}})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Emit blocked on a consumer that never drains")
	}
	waitWake(t, s)
	got := s.Drain()
	require.Len(t, got, 1)
	assert.Equal(t, 99_999, got[0].Payload["n"])
}

func TestOverflowCollapsesToResync(t *testing.T) {
	e := NewEmitter()
	s, unsub := e.Subscribe()
	defer unsub()
	for i := 0; i < maxPending+5; i++ {
		e.Emit(Event{Type: "view_changed", Key: string(rune(i + 1))})
	}
	waitWake(t, s)
	assert.Equal(t, []Event{{Type: TypeResync}}, s.Drain())
	e.Emit(Event{Type: "after"})
	waitWake(t, s)
	assert.Equal(t, "after", s.Drain()[0].Type, "back to normal after the resync")
}

func TestUnsubscribeIsIdempotentAndStopsDelivery(t *testing.T) {
	e := NewEmitter()
	s, unsub := e.Subscribe()
	unsub()
	unsub()
	e.Emit(Event{Type: "after"}) // must not panic
	assert.Empty(t, s.Drain())
}

func TestNilEmitterIsNoop(_ *testing.T) {
	var e *Emitter
	e.Emit(Event{Type: "x"})
}
