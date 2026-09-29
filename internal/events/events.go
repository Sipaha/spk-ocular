// Package events is the in-process fan-out bus from the Go core to the UI
// transports (Wails events, SSE). Emit never blocks.
package events

import (
	"log/slog"
	"sync"
)

const subBuffer = 256

type Event struct {
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload,omitempty"`
}

type subscription struct {
	ch     chan Event
	mu     sync.Mutex
	closed bool
}

// send drops the event when the buffer is full: a wedged subscriber (a
// backgrounded SSE tab) must never stall the goroutine that emits (informers, watchers).
func (s *subscription) send(ev Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.ch <- ev:
	default:
		slog.Warn("event dropped: subscriber buffer full", "type", ev.Type)
	}
}

func (s *subscription) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.ch)
}

type Emitter struct {
	mu   sync.Mutex
	subs []*subscription
}

func NewEmitter() *Emitter { return &Emitter{} }

func (e *Emitter) Subscribe() (<-chan Event, func()) {
	sub := &subscription{ch: make(chan Event, subBuffer)}
	e.mu.Lock()
	e.subs = append(e.subs, sub)
	e.mu.Unlock()
	var once sync.Once
	return sub.ch, func() {
		once.Do(func() {
			e.mu.Lock()
			for i, s := range e.subs {
				if s == sub {
					e.subs = append(e.subs[:i], e.subs[i+1:]...)
					break
				}
			}
			e.mu.Unlock()
			sub.close()
		})
	}
}

func (e *Emitter) Emit(ev Event) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, sub := range e.subs {
		sub.send(ev)
	}
}
