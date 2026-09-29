package views

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/events"
	"github.com/spk/spk-ocular/internal/provider"
)

// EventViewChanged: {viewId, version}; the UI pulls GetRows(viewId, cursor).
const EventViewChanged = "view_changed"

// coalesceDelay folds bursts of changes into one notification per view.
const coalesceDelay = 100 * time.Millisecond

// leaseTTL: a view nobody pulled or renewed for this long is closed. The UI
// renews its open views (Touch) well within it; a reloaded/crashed/closed
// page simply stops renewing, so its views cannot leak.
const leaseTTL = 60 * time.Second

// ErrGone: the view does not exist (closed, or from an earlier process).
var ErrGone = errors.New("view is gone")

// Source is what a view reads from (a provider.Session).
type Source interface {
	Watch(q provider.Query, sink provider.Sink) (stop func(), err error)
}

type entry struct {
	view    *View
	owner   string
	query   provider.Query
	stop    func()
	touched time.Time
}

// Manager owns open views. View ids are opaque and never reused: a random
// per-process epoch plus a counter, so a cursor from another view or an
// earlier run can never be mistaken for this one's.
type Manager struct {
	em    *events.Emitter
	coal  *events.Coalescer
	epoch string

	now func() time.Time
	ttl time.Duration

	mu     sync.Mutex
	seq    uint64
	views  map[string]*entry
	closed bool
	sweep  *time.Timer // armed only while views exist
}

func NewManager(em *events.Emitter) *Manager {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return &Manager{
		em: em, coal: events.NewCoalescer(coalesceDelay), epoch: hex.EncodeToString(b),
		views: map[string]*entry{}, now: time.Now, ttl: leaseTTL,
	}
}

// Open starts a view of q from src. owner groups views for CloseOwner (a
// session). The view starts in StatusLoading until the source reports ready.
func (m *Manager) Open(owner string, src Source, q provider.Query) (string, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return "", ErrGone
	}
	m.seq++
	id := fmt.Sprintf("v%s-%d", m.epoch, m.seq)
	m.mu.Unlock()

	v := newView(id, func() { m.notify(id) })
	stop, err := src.Watch(q, v)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		stop()
		return "", ErrGone
	}
	m.views[id] = &entry{view: v, owner: owner, query: q, stop: stop, touched: m.now()}
	m.armSweepLocked()
	return id, nil
}

// armSweepLocked schedules one expiry check while views exist. A local
// timer, not cluster polling: it only closes views whose page went away.
func (m *Manager) armSweepLocked() {
	if m.sweep != nil || m.closed || len(m.views) == 0 {
		return
	}
	m.sweep = time.AfterFunc(m.ttl/2, func() {
		m.mu.Lock()
		m.sweep = nil
		m.mu.Unlock()
		m.Expire()
		m.mu.Lock()
		m.armSweepLocked()
		m.mu.Unlock()
	})
}

// Touch renews the leases of ids (the UI's open views); unknown ids are
// returned so the UI can reopen them.
func (m *Manager) Touch(ids []string) (gone []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	for _, id := range ids {
		if e := m.views[id]; e != nil {
			e.touched = now
		} else {
			gone = append(gone, id)
		}
	}
	return gone
}

// Expire closes views whose lease ran out and returns their ids.
func (m *Manager) Expire() []string {
	m.mu.Lock()
	cutoff := m.now().Add(-m.ttl)
	var ids []string
	for id, e := range m.views {
		if e.touched.Before(cutoff) {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Close(id)
	}
	return ids
}

func (m *Manager) notify(id string) {
	m.coal.Schedule(id, func() {
		m.mu.Lock()
		e := m.views[id]
		m.mu.Unlock()
		if e == nil {
			return
		}
		m.em.Emit(events.Event{Type: EventViewChanged, Key: id, Payload: map[string]any{"viewId": id, "version": e.view.Version()}})
	})
}

// Get returns the page after cursor since.
func (m *Manager) Get(id string, since uint64) (Page, error) {
	m.mu.Lock()
	e := m.views[id]
	if e != nil {
		e.touched = m.now() // pulling is using
	}
	m.mu.Unlock()
	if e == nil {
		return Page{}, ErrGone
	}
	return e.view.Since(since), nil
}

// Close stops one view; closing an unknown view is a no-op.
func (m *Manager) Close(id string) {
	m.mu.Lock()
	e := m.views[id]
	delete(m.views, id)
	m.mu.Unlock()
	if e != nil {
		e.stop()
	}
}

// CloseOwner stops every view of owner (a session that went away) and
// returns their ids. Each closed view gets a final view_changed so an idle
// UI pulls, learns it is gone and reopens it — it would wait forever
// otherwise.
func (m *Manager) CloseOwner(owner string) []string {
	m.mu.Lock()
	var gone []*entry
	var ids []string
	for id, e := range m.views {
		if e.owner == owner {
			gone = append(gone, e)
			ids = append(ids, id)
			delete(m.views, id)
		}
	}
	m.mu.Unlock()
	for _, e := range gone {
		e.stop()
	}
	for _, id := range ids {
		m.em.Emit(events.Event{Type: EventViewChanged, Key: id, Payload: map[string]any{"viewId": id, "gone": true}})
	}
	return ids
}

// Owners lists owners that still have views.
func (m *Manager) Owners() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]int{}
	for _, e := range m.views {
		out[e.owner]++
	}
	return out
}

// CloseAll stops everything; later Opens fail with ErrGone.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	m.closed = true
	if m.sweep != nil {
		m.sweep.Stop()
		m.sweep = nil
	}
	all := m.views
	m.views = map[string]*entry{}
	m.mu.Unlock()
	for _, e := range all {
		e.stop()
	}
	m.coal.Close()
}
