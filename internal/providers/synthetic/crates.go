package synthetic

import (
	"fmt"
	"sync"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// Crates are the regression of the generic UI's provider metadata: a
// scoped kind that opens first (not "pods"), in the target's default scope
// ("blue", not a namespace), scopes that cannot be listed (typed by hand),
// the provider's own scope words, no events, a display title apart from the
// key, a terminal with nowhere to run, and "read again" (Resyncer: each
// request is counted and redelivers the rows with the new count).

const (
	CrateKind    = "crates"
	DefaultScope = "blue"
)

var crateKind = core.KindDescriptor{
	ID: CrateKind, Title: "Crates", Singular: "Crate", Group: "Synthetic", Scoped: true, Default: true, Exec: true,
	Aliases: []string{"cr"},
	Columns: []core.Column{
		{ID: "name", Title: "Name", Type: core.ColText},
		{ID: "zone", Title: "Zone", Type: core.ColText, ScopeColumn: true},
		{ID: "reads", Title: "Reads", Type: core.ColNumber},
	},
}

type crate struct{ key, title, zone string }

var allCrates = []crate{{"crate-7f3a", "alpha", "blue"}, {"crate-91c2", "beta", "blue"}, {"crate-03de", "gamma", "green"}}

// crates holds the watchers and the count of reads (Resync requests).
type crates struct {
	mu       sync.Mutex
	reads    int
	watchers map[*crateWatcher]struct{}
}

type crateWatcher struct {
	q    provider.Query
	sink provider.Sink
}

func (c *crateWatcher) rows(reads int) []core.Row {
	var out []core.Row
	for _, x := range allCrates {
		if c.q.Scope.Mode == core.ScopeOne && c.q.Scope.Name != x.zone || c.q.Name != "" && c.q.Name != x.key {
			continue
		}
		out = append(out, core.Row{
			ID: x.key, Rev: fmt.Sprint(reads),
			Ref:    crateRef(x),
			Cells:  []core.Cell{core.TextCell(x.title), core.TextCell(x.zone), core.NumCell(float64(reads), fmt.Sprint(reads))},
			Health: core.Health{State: core.HealthOK},
		})
	}
	return out
}

func crateRef(x crate) core.Ref {
	return core.Ref{Provider: ID, Target: Target, Scope: x.zone, Kind: CrateKind, Name: x.key, UID: x.key, Title: x.title}
}

func (s *session) watchCrates(q provider.Query, sink provider.Sink) (func(), error) {
	c := &s.p.crates
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.watchers == nil {
		c.watchers = map[*crateWatcher]struct{}{}
	}
	w := &crateWatcher{q: q, sink: sink}
	c.watchers[w] = struct{}{}
	sink.Apply(provider.Delta{Reset: true, Upserts: w.rows(c.reads), Status: &provider.ViewStatus{State: provider.StatusReady}})
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			delete(c.watchers, w)
			c.mu.Unlock()
		})
	}, nil
}

var _ provider.Resyncer = (*session)(nil)

// Resync: every view of the session can be read again; crates show it.
func (s *session) Resync(provider.Query) error {
	c := &s.p.crates
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads++
	for w := range c.watchers {
		w.sink.Apply(provider.Delta{Reset: true, Upserts: w.rows(c.reads), Status: &provider.ViewStatus{State: provider.StatusReady}})
	}
	return nil
}

func (s *session) getCrate(ref core.Ref) (*core.Resource, error) {
	for _, x := range allCrates {
		if x.key == ref.Name {
			return &core.Resource{Ref: crateRef(x), Health: core.Health{State: core.HealthOK}, Facts: []core.Detail{{Key: "zone", Value: x.zone}}, YAML: "key: " + x.key + "\ntitle: " + x.title + "\n"}, nil
		}
	}
	return nil, &provider.Error{Class: provider.ClassNotFound, Message: ref.Name}
}
