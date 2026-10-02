package provider

import (
	"errors"
	"sync"

	"github.com/spk/spk-ocular/internal/core"
)

// WatchScopes combines explicit, individually scoped watches. It never opens
// an all-scopes watch. Shared rows live until their last source removes them;
// a source's Reset replaces only its own rows. Sink calls remain ordered.
func WatchScopes(q Query, sink Sink, watch func(Query, Sink) (func(), error)) (func(), error) {
	names := q.Scope.SelectedNames()
	u := &scopeUnion{sink: sink, names: names, rows: make([]map[string]core.Row, len(names)), states: make([]ViewStatus, len(names))}
	for i := range names {
		u.rows[i] = map[string]core.Row{}
		u.states[i] = ViewStatus{State: StatusLoading}
	}
	st := u.status()
	sink.Apply(Delta{Status: &st})
	var stops []func()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			u.mu.Lock()
			u.stopped = true
			u.mu.Unlock()
			for i := len(stops) - 1; i >= 0; i-- {
				stops[i]()
			}
		})
	}
	for i, name := range names {
		child := q
		child.Scope = core.ScopeSel{Mode: core.ScopeOne, Name: name}
		feed := &scopeFeed{union: u, index: i}
		end, err := watch(child, feed)
		if err != nil {
			if end != nil {
				end()
			}
			var e *Error
			if !errors.As(err, &e) || terminalClass(e.Class) || e.Class == ClassInternal || e.Class == ClassInvalid {
				stop()
				return nil, err
			}
			feed.Apply(Delta{Status: &ViewStatus{State: StatusError, Class: e.Class, Message: e.Message}})
		} else if end != nil {
			stops = append(stops, end)
		}
	}
	return stop, nil
}

type scopeUnion struct {
	mu      sync.Mutex
	sink    Sink
	names   []string
	rows    []map[string]core.Row
	states  []ViewStatus
	stopped bool
}
type scopeFeed struct {
	union *scopeUnion
	index int
}

func (f *scopeFeed) Apply(d Delta) {
	u := f.union
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.stopped {
		return
	}
	mine := u.rows[f.index]
	touched := map[string]bool{}
	if d.Reset {
		for id := range mine {
			touched[id] = true
		}
		mine = map[string]core.Row{}
		u.rows[f.index] = mine
	}
	for _, row := range d.Upserts {
		mine[row.ID] = row
		touched[row.ID] = true
	}
	for _, id := range d.Deletes {
		delete(mine, id)
		touched[id] = true
	}
	out := Delta{}
	for id := range touched {
		found := false
		// A deterministic owner prevents asynchronous shared sources from
		// repeatedly replacing a row with an older snapshot.
		for _, rows := range u.rows {
			if row, ok := rows[id]; ok {
				out.Upserts = append(out.Upserts, row)
				found = true
				break
			}
		}
		if !found {
			out.Deletes = append(out.Deletes, id)
		}
	}
	if d.Status != nil {
		u.states[f.index] = d.Status.Clone()
		st := u.status()
		out.Status = &st
	}
	if len(out.Upserts) != 0 || len(out.Deletes) != 0 || out.Status != nil {
		u.sink.Apply(out)
	}
}

func terminalClass(c ErrorClass) bool {
	return c == ClassGone || c == ClassRemoved || c == ClassSchemaChanged
}

func (u *scopeUnion) status() ViewStatus {
	out := ViewStatus{State: StatusReady}
	loading, ready, stale := false, false, false
	var failure ViewStatus
	for i, st := range u.states {
		if terminalClass(st.Class) {
			return st.Clone()
		}
		cov := SourceCoverage{Source: u.names[i], Class: st.Class, Message: st.Message}
		switch st.State {
		case StatusLoading:
			loading = true
			cov.State = CoverageLoading
		case StatusReady:
			ready = true
			cov.State = CoverageReady
		case StatusStale:
			stale = true
			cov.State = CoverageStale
			failure = st
		default:
			failure = st
			cov.State = CoverageError
			if st.Class == ClassForbidden || st.Class == ClassUnauthorized {
				cov.State = CoverageDenied
			}
		}
		out.Coverage = append(out.Coverage, cov)
		for _, nested := range st.Coverage {
			nested.Source = u.names[i] + " / " + nested.Source
			out.Coverage = append(out.Coverage, nested)
		}
	}
	switch {
	case loading:
		out.State = StatusLoading
	case ready || len(u.states) == 0:
	case stale:
		out.State = StatusStale
		out.Class = failure.Class
		out.Message = failure.Message
	default:
		out.State = StatusError
		out.Class = failure.Class
		out.Message = failure.Message
	}
	return out
}
