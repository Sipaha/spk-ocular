// Package events is the in-process fan-out bus from the Go core to the UI
// transports (Wails events, SSE).
//
// Every event is a state invalidation ("targets changed", "view 7 changed"),
// not a record of an occurrence: a subscriber needs the latest one per key,
// never the full history. So delivery is a per-subscriber mailbox keyed by
// (Type, Key) — a newer event replaces a pending one with the same key —
// plus a capacity-one wake signal. Emit never blocks and never loses the
// latest state: a slow subscriber sees fewer, newer events, and if it falls
// behind on more than maxPending distinct keys it gets a single Resync
// instead (reload everything).
package events

import "sync"

// maxPending bounds a subscriber's mailbox (distinct keys). Keys are few in
// practice (one per provider / open view); overflow means a wedged consumer.
const maxPending = 1024

// TypeResync replaces everything pending after a mailbox overflow: the
// consumer must reload all state it shows.
const TypeResync = "resync"

type Event struct {
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload,omitempty"`
	// Key scopes coalescing within Type (a provider id, a view id). Events
	// with the same Type and Key replace each other while pending.
	Key string `json:"-"`
}

func (ev Event) mailboxKey() string { return ev.Type + "\x00" + ev.Key }

// Subscription is one consumer's mailbox.
type Subscription struct {
	wake chan struct{}

	mu       sync.Mutex
	pending  map[string]Event
	order    []string // first-arrival order of pending keys
	overflow bool
	closed   bool
}

func newSubscription() *Subscription {
	return &Subscription{wake: make(chan struct{}, 1), pending: map[string]Event{}}
}

// Wake fires (possibly collapsed) when events are pending; call Drain then.
func (s *Subscription) Wake() <-chan struct{} { return s.wake }

// Drain takes everything pending, in first-arrival order of keys.
func (s *Subscription) Drain() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.overflow {
		s.overflow = false
		s.pending = map[string]Event{}
		s.order = nil
		return []Event{{Type: TypeResync}}
	}
	out := make([]Event, 0, len(s.order))
	for _, k := range s.order {
		out = append(out, s.pending[k])
	}
	s.pending = map[string]Event{}
	s.order = nil
	return out
}

func (s *Subscription) put(ev Event) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	k := ev.mailboxKey()
	switch _, ok := s.pending[k]; {
	case s.overflow:
		// everything will be reloaded anyway
	case ok:
		s.pending[k] = ev
	case len(s.order) >= maxPending:
		s.overflow = true
	default:
		s.pending[k] = ev
		s.order = append(s.order, k)
	}
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default: // a wake is already pending: it covers this event too
	}
}

type Emitter struct {
	mu   sync.Mutex
	subs []*Subscription
}

func NewEmitter() *Emitter { return &Emitter{} }

// Subscribe returns a mailbox and an idempotent unsubscribe.
func (e *Emitter) Subscribe() (*Subscription, func()) {
	sub := newSubscription()
	e.mu.Lock()
	e.subs = append(e.subs, sub)
	e.mu.Unlock()
	var once sync.Once
	return sub, func() {
		once.Do(func() {
			e.mu.Lock()
			for i, s := range e.subs {
				if s == sub {
					e.subs = append(e.subs[:i], e.subs[i+1:]...)
					break
				}
			}
			e.mu.Unlock()
			sub.mu.Lock()
			sub.closed = true
			sub.pending = nil
			sub.order = nil
			sub.mu.Unlock()
		})
	}
}

// Emit delivers ev to every subscriber. It never blocks on a consumer.
// Publishers must not mutate ev.Payload afterwards.
func (e *Emitter) Emit(ev Event) {
	if e == nil {
		return
	}
	e.mu.Lock()
	subs := append([]*Subscription(nil), e.subs...)
	e.mu.Unlock()
	for _, sub := range subs {
		sub.put(ev)
	}
}
