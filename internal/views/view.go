// Package views is the provider-agnostic hot layer between a provider's
// watch and the UI: current rows of an open table plus enough change history
// to answer "what changed since version N" (docs/specs, "Живые таблицы").
package views

import (
	"reflect"
	"sort"
	"sync"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// maxTombstones bounds deletion history. Beyond it the history is dropped
// and every cursor older than "now" gets a reset (full snapshot) instead.
const maxTombstones = 4096

// Page answers GetRows. Version is the cursor the client stores only after
// applying the page.
type Page struct {
	ViewID  string              `json:"viewId"`
	Version uint64              `json:"version"`
	Reset   bool                `json:"reset"`
	Upserts []core.Row          `json:"upserts"`
	Deleted []string            `json:"deleted"`
	Status  provider.ViewStatus `json:"status"`
}

// View holds one open query's rows. Safe for concurrent use; Apply is the
// provider.Sink and never blocks beyond its own short critical section.
type View struct {
	id       string
	onChange func() // called after a version bump, outside the lock

	mu      sync.Mutex
	version uint64
	rows    map[string]core.Row
	changed map[string]uint64 // row id -> version of its last upsert
	tombs   map[string]uint64 // deleted row id -> version of the delete
	// minRetained: cursors below it cannot be answered incrementally.
	minRetained  uint64
	status       provider.ViewStatus
	statusChange uint64
}

func newView(id string, onChange func()) *View {
	return &View{
		id:       id,
		onChange: onChange,
		version:  1, // 0 is the client's "nothing yet"
		rows:     map[string]core.Row{},
		changed:  map[string]uint64{},
		tombs:    map[string]uint64{},
		status:   provider.ViewStatus{State: provider.StatusLoading},
	}
}

// Apply implements provider.Sink.
func (v *View) Apply(d provider.Delta) {
	v.mu.Lock()
	bumped := v.apply(d)
	v.mu.Unlock()
	if bumped && v.onChange != nil {
		v.onChange()
	}
}

func (v *View) apply(d provider.Delta) bool {
	next := v.version + 1
	dirty := false
	if d.Reset {
		keep := make(map[string]bool, len(d.Upserts))
		for _, r := range d.Upserts {
			keep[r.ID] = true
		}
		for id := range v.rows {
			if !keep[id] {
				v.del(id, next)
				dirty = true
			}
		}
	}
	for _, r := range d.Upserts {
		if old, ok := v.rows[r.ID]; ok && reflect.DeepEqual(old, r) {
			continue // no-op update (informer relist): no traffic
		}
		v.rows[r.ID] = r
		v.changed[r.ID] = next
		delete(v.tombs, r.ID)
		dirty = true
	}
	for _, id := range d.Deletes {
		if _, ok := v.rows[id]; ok {
			v.del(id, next)
			dirty = true
		}
	}
	if d.Status != nil && *d.Status != v.status {
		v.status = *d.Status
		v.statusChange = next
		dirty = true
	}
	if !dirty {
		return false
	}
	v.version = next
	if len(v.tombs) > maxTombstones {
		v.tombs = map[string]uint64{}
		v.minRetained = v.version
	}
	return true
}

func (v *View) del(id string, at uint64) {
	delete(v.rows, id)
	delete(v.changed, id)
	v.tombs[id] = at
}

// Since returns everything that changed after cursor since, or a full
// snapshot (Reset) when since is 0, older than retained history, or not
// from this view's history at all (ahead of its version).
func (v *View) Since(since uint64) Page {
	v.mu.Lock()
	defer v.mu.Unlock()
	p := Page{ViewID: v.id, Version: v.version, Status: v.status, Upserts: []core.Row{}, Deleted: []string{}}
	if since == 0 || since < v.minRetained || since > v.version {
		p.Reset = true
		for _, r := range v.rows {
			p.Upserts = append(p.Upserts, r)
		}
	} else {
		for id, at := range v.changed {
			if at > since {
				p.Upserts = append(p.Upserts, v.rows[id])
			}
		}
		for id, at := range v.tombs {
			if at > since {
				p.Deleted = append(p.Deleted, id)
			}
		}
		sort.Strings(p.Deleted)
	}
	sort.Slice(p.Upserts, func(i, j int) bool { return p.Upserts[i].ID < p.Upserts[j].ID })
	return p
}

// Version is the current version (for change notifications).
func (v *View) Version() uint64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.version
}
