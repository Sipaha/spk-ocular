// Package synthetic is a test-only provider (spk-ocular --test-api
// --test-synthetic): one target whose objects have deterministic logs that
// e2e tests drive through /api/_test/logs, an echo terminal (live.go) and
// ports served by in-process HTTP servers, and workloads to act on
// (actions.go). It exercises the generic log, terminal, tunnel and action
// UI without a cluster — and shows that UI knows nothing about Kubernetes.
package synthetic

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

const (
	ID      = "synthetic"
	Target  = "demo"
	Target2 = "demo2" // test-only second target (enabled at the app edge by an env var)
	Kind    = "services"
)

// Objects and their sources.
var objects = map[string][]string{
	"api":     {"api/main"},
	"workers": {"worker-1/main", "worker-2/main", "worker-3/main"},
}
var objectOrder = []string{"api", "workers"}

// The second target serves the same objects with its own log feeds (streams
// are scoped by target), so e2e exercises the whole feature set on both
// targets and switches between two warm pages (P18).
var objects2 = map[string][]string{
	"api":     {"api/main"},
	"workers": {"worker-1/main", "worker-2/main"},
}
var objectOrder2 = []string{"api", "workers"}

// Provider holds the live log feeds tests push into.
type Provider struct {
	live    live
	wl      workloads
	crates  crates
	parcels parcels
	// rev is the configuration revision (Reconfigure bumps it); changed
	// wakes Watch.
	rev     atomic.Int64
	changed chan struct{}
	mu      sync.Mutex
	subs    map[*sub]struct{}
	clock   time.Time
	// second: the test-only second target (demo2) is discovered too.
	second bool
}

type sub struct {
	target string
	object string
	ch     chan Event
}

// Event is something a test pushes to an object's open streams.
type Event struct {
	// Source index within the object; -1 = every source.
	Source int                `json:"source"`
	Lines  []string           `json:"lines,omitempty"`
	State  *provider.LogState `json:"state,omitempty"`
	// End ends the streams (a finished source set).
	End bool `json:"end,omitempty"`
}

func New() *Provider {
	p := &Provider{subs: map[*sub]struct{}{}, clock: time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC), changed: make(chan struct{}, 1)}
	p.rev.Store(1)
	p.wl.init()
	return p
}

func (p *Provider) hash() string { return fmt.Sprint(p.rev.Load()) }

// EnableSecondTarget discovers the test-only second target (demo2) with its
// own objects and log feeds. Call before the first Discover.
func (p *Provider) EnableSecondTarget() { p.second = true }

// Reconfigure changes the target's configuration (as an edited kubeconfig
// would): open sessions are rebuilt, live resources keep the old one.
func (p *Provider) Reconfigure() {
	p.rev.Add(1)
	select {
	case p.changed <- struct{}{}:
	default:
	}
}

var _ provider.TargetWatcher = (*Provider)(nil)

func (p *Provider) Watch(ctx context.Context, onChange func()) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-p.changed:
			onChange()
		}
	}
}

func (p *Provider) ID() string    { return ID }
func (p *Provider) Title() string { return "Synthetic (test)" }

func (p *Provider) Discover(context.Context) (provider.Discovery, error) {
	targets := []core.Target{{Provider: ID, ID: Target, Title: Target, Subtitle: "test provider", ConfigHash: p.hash(), DefaultScope: DefaultScope, Identity: "synthetic.local"}}
	if p.second {
		targets = append(targets, core.Target{Provider: ID, ID: Target2, Title: Target2, Subtitle: "test provider 2", ConfigHash: p.hash(), DefaultScope: DefaultScope, Identity: "synthetic-2.local"})
	}
	return provider.Discovery{Targets: targets}, nil
}

var _ provider.ScopeNamer = (*Provider)(nil)

// ScopeNames: zones, not namespaces (no translations: the English is shown).
func (p *Provider) ScopeNames() core.ScopeNames {
	return core.ScopeNames{
		Singular: core.Message{Key: ID + ".scope.singular", Text: "Zone"},
		Plural:   core.Message{Key: ID + ".scope.plural", Text: "zones"},
		All:      core.Message{Key: ID + ".scope.all", Text: "All zones"},
	}
}

func (p *Provider) Open(_ context.Context, id string) (provider.Session, error) {
	switch id {
	case Target:
		return &session{p: p, target: Target, objects: objects, order: objectOrder, hash: p.hash()}, nil
	case Target2:
		if p.second {
			return &session{p: p, target: Target2, objects: objects2, order: objectOrder2, hash: p.hash()}, nil
		}
	}
	return nil, &provider.Error{Class: provider.ClassNotFound, Message: id}
}

// Emit sends ev to the open streams of object's target; it returns how many
// got it.
func (p *Provider) Emit(target, object string, ev Event) int {
	p.mu.Lock()
	var targets []*sub
	for s := range p.subs {
		if s.target == target && s.object == object {
			targets = append(targets, s)
		}
	}
	p.mu.Unlock()
	for _, s := range targets {
		s.ch <- ev
	}
	return len(targets)
}

// ts hands out strictly increasing timestamps.
func (p *Provider) ts() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clock = p.clock.Add(10 * time.Millisecond)
	return p.clock.Format(time.RFC3339Nano)
}

type session struct {
	p       *Provider
	target  string // the target it serves (demo or demo2)
	objects map[string][]string
	order   []string
	hash    string // the configuration it was opened with
}

var _ provider.LogSource = (*session)(nil)

var kind = core.KindDescriptor{
	ID: Kind, Title: "Services", Singular: "Service", Group: "Synthetic", Logs: true, Exec: true, Forward: true,
	Columns: []core.Column{{ID: "name", Title: "Name", Type: core.ColText}, {ID: "sources", Title: "Sources", Type: core.ColNumber}},
}

func (s *session) ConfigHash() string { return s.hash }
func (s *session) Kinds() []core.KindDescriptor {
	return []core.KindDescriptor{kind, workloadKind, problemsKind, crateKind, parcelKind}
}

// Scopes cannot be listed: the UI takes a typed one.
func (s *session) Scopes(context.Context) ([]core.Scope, error) {
	return nil, &provider.Error{Class: provider.ClassForbidden, Message: "zones cannot be listed"}
}
func (s *session) ScopeKind() string { return "" }
func (s *session) Close()            {}
func (s *session) Get(_ context.Context, ref core.Ref) (*core.Resource, error) {
	switch ref.Kind {
	case WorkloadKind:
		return s.getWorkload(ref.Name)
	case CrateKind:
		return s.getCrate(ref)
	case ParcelKind:
		return s.getParcel(ref)
	}
	if _, ok := s.objects[ref.Name]; !ok {
		return nil, &provider.Error{Class: provider.ClassNotFound, Message: ref.Name}
	}
	return &core.Resource{Ref: s.ref(ref.Name), Health: core.Health{State: core.HealthOK}, Facts: []core.Detail{{Key: "kind", Value: "Service"}}, YAML: "name: " + ref.Name + "\n"}, nil
}

func (s *session) ref(name string) core.Ref {
	return core.Ref{Provider: ID, Target: s.target, Kind: Kind, Name: name, UID: "uid-" + name}
}

func (s *session) Watch(q provider.Query, sink provider.Sink) (func(), error) {
	switch q.Kind {
	case WorkloadKind:
		return s.watchWorkloads(q, sink)
	case ProblemsKind:
		return s.watchProblems(sink)
	case CrateKind:
		return s.watchCrates(q, sink)
	case ParcelKind:
		return s.watchParcels(q, sink)
	}
	var rows []core.Row
	for _, name := range s.order {
		if q.Name != "" && q.Name != name {
			continue
		}
		n := float64(len(s.objects[name]))
		rows = append(rows, core.Row{ID: "uid-" + name, Rev: "1", Ref: s.ref(name), Cells: []core.Cell{core.TextCell(name), core.NumCell(n, fmt.Sprint(n))}, Health: core.Health{State: core.HealthOK}})
	}
	sink.Apply(provider.Delta{Reset: true, Upserts: rows, Status: &provider.ViewStatus{State: provider.StatusReady}})
	return func() {}, nil
}

func (s *session) LogInfo(_ context.Context, ref core.Ref) (core.LogInfo, error) {
	srcs, ok := s.objects[ref.Name]
	if !ok {
		return core.LogInfo{}, &provider.Error{Class: provider.ClassNotFound, Message: ref.Name}
	}
	return core.LogInfo{Channels: []core.LogChannel{{ID: "main", Title: "main"}}, DefaultChannel: "main", Aggregate: len(srcs) > 1, Previous: len(srcs) == 1}, nil
}

// backlog is each source's history: levels, ANSI, a multi-line record and
// a long line.
func backlog(src string) []string {
	return []string{
		"INFO " + src + " starting",
		"\x1b[33mWARN\x1b[0m " + src + " config reloaded",
		"ERROR " + src + " request failed",
		"\tat handler (main.go:42)",
		"DEBUG " + src + " cache hit",
		"INFO " + src + " \x1b[1;32mready\x1b[0m",
	}
}

func (s *session) StreamLogs(ctx context.Context, ref core.Ref, q provider.LogQuery, sink provider.LogSink) error {
	srcs, ok := s.objects[ref.Name]
	if !ok {
		return &provider.Error{Class: provider.ClassNotFound, Message: ref.Name}
	}
	for i, src := range srcs {
		if err := sink.Source(i+1, ref.Name+"/"+src, src, "main"); err != nil {
			return err
		}
	}
	for line := range backlog("x") { // interleaved by time, like a merged backlog
		for i, src := range srcs {
			text := backlog(src)[line]
			if q.Previous {
				text = "previous: " + text
			}
			if err := sink.Lines(i+1, []provider.LogLine{{TS: s.p.ts(), Text: text}}); err != nil {
				return err
			}
		}
	}
	if err := sink.Ready(); err != nil {
		return err
	}
	if !q.Follow {
		return nil
	}
	sb := &sub{target: s.target, object: ref.Name, ch: make(chan Event, 16)}
	s.p.mu.Lock()
	s.p.subs[sb] = struct{}{}
	s.p.mu.Unlock()
	defer func() {
		s.p.mu.Lock()
		delete(s.p.subs, sb)
		s.p.mu.Unlock()
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev := <-sb.ch:
			for i := range srcs {
				if ev.Source >= 0 && ev.Source != i {
					continue
				}
				if len(ev.Lines) > 0 {
					lines := make([]provider.LogLine, len(ev.Lines))
					for j, l := range ev.Lines {
						lines[j] = provider.LogLine{TS: s.p.ts(), Text: l}
					}
					if err := sink.Lines(i+1, lines); err != nil {
						return err
					}
				}
				if ev.State != nil {
					if err := sink.State(i+1, *ev.State); err != nil {
						return err
					}
				}
			}
			if ev.End {
				return nil
			}
		}
	}
}
