package events

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmitDeliversToAllSubscribers(t *testing.T) {
	e := NewEmitter()
	a, unsubA := e.Subscribe()
	b, unsubB := e.Subscribe()
	defer unsubA()
	defer unsubB()
	e.Emit(Event{Type: "x", Payload: map[string]any{"n": 1}})
	assert.Equal(t, "x", (<-a).Type)
	assert.Equal(t, "x", (<-b).Type)
}

func TestEmitNeverBlocksOnFullSubscriber(t *testing.T) {
	e := NewEmitter()
	_, unsub := e.Subscribe() // never drained
	defer unsub()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10*subBuffer; i++ {
			e.Emit(Event{Type: "flood"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Emit blocked on a full subscriber")
	}
}

func TestUnsubscribeClosesChannelAndIsIdempotent(t *testing.T) {
	e := NewEmitter()
	ch, unsub := e.Subscribe()
	unsub()
	unsub()
	_, ok := <-ch
	require.False(t, ok)
	e.Emit(Event{Type: "after"}) // must not panic
}

func TestNilEmitterIsNoop(_ *testing.T) {
	var e *Emitter
	e.Emit(Event{Type: "x"})
}
