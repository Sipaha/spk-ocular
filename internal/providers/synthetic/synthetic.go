// Package synthetic is a test-only provider (spk-ocular --test-api
// --test-synthetic): one target whose objects have deterministic logs that
// e2e tests drive through /api/_test/logs. It exercises the generic log UI
// without a cluster — and shows that UI knows nothing about Kubernetes.
package synthetic

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

const (
	ID     = "synthetic"
	Target = "demo"
	Kind   = "services"
)

// Objects and their sources.
var objects = map[string][]string{
	"api":     {"api/main"},
	"workers": {"worker-1/main", "worker-2/main", "worker-3/main"},
}

// Provider holds the live log feeds tests push into.
type Provider struct {
	mu    sync.Mutex
	subs  map[*sub]struct{}
	clock time.Time
}

type sub struct {
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
	return &Provider{subs: map[*sub]struct{}{}, clock: time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)}
}

func (p *Provider) ID() string    { return ID }
func (p *Provider) Title() string { return "Synthetic (test)" }

func (p *Provider) Discover(context.Context) (provider.Discovery, error) {
	return provider.Discovery{Targets: []core.Target{{Provider: ID, ID: Target, Title: Target, Subtitle: "test provider", ConfigHash: "1"}}}, nil
}

func (p *Provider) Open(context.Context, string) (provider.Session, error) {
	return &session{p: p}, nil
}

// Emit sends ev to the open streams of object; it returns how many got it.
func (p *Provider) Emit(object string, ev Event) int {
	p.mu.Lock()
	var targets []*sub
	for s := range p.subs {
		if s.object == object {
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

type session struct{ p *Provider }

var _ provider.LogSource = (*session)(nil)

var kind = core.KindDescriptor{
	ID: Kind, Title: "Services", Group: "Synthetic", Logs: true,
	Columns: []core.Column{{ID: "name", Title: "Name", Type: core.ColText}, {ID: "sources", Title: "Sources", Type: core.ColNumber}},
}

func (s *session) ConfigHash() string                           { return "1" }
func (s *session) Kinds() []core.KindDescriptor                 { return []core.KindDescriptor{kind} }
func (s *session) Scopes(context.Context) ([]core.Scope, error) { return nil, nil }
func (s *session) ScopeKind() string                            { return "" }
func (s *session) Close()                                       {}
func (s *session) Get(_ context.Context, ref core.Ref) (*core.Resource, error) {
	if _, ok := objects[ref.Name]; !ok {
		return nil, &provider.Error{Class: provider.ClassNotFound, Message: ref.Name}
	}
	return &core.Resource{Ref: s.ref(ref.Name), Health: core.Health{State: core.HealthOK}, Facts: []core.Detail{{Key: "kind", Value: "Service"}}, YAML: "name: " + ref.Name + "\n"}, nil
}

func (s *session) ref(name string) core.Ref {
	return core.Ref{Provider: ID, Target: Target, Kind: Kind, Name: name, UID: "uid-" + name}
}

func (s *session) Watch(q provider.Query, sink provider.Sink) (func(), error) {
	var rows []core.Row
	for _, name := range []string{"api", "workers"} {
		if q.Name != "" && q.Name != name {
			continue
		}
		n := float64(len(objects[name]))
		rows = append(rows, core.Row{ID: "uid-" + name, Rev: "1", Ref: s.ref(name), Cells: []core.Cell{core.TextCell(name), core.NumCell(n, fmt.Sprint(n))}, Health: core.Health{State: core.HealthOK}})
	}
	sink.Apply(provider.Delta{Reset: true, Upserts: rows, Status: &provider.ViewStatus{State: provider.StatusReady}})
	return func() {}, nil
}

func (s *session) LogInfo(_ context.Context, ref core.Ref) (core.LogInfo, error) {
	srcs, ok := objects[ref.Name]
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
	srcs, ok := objects[ref.Name]
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
	sb := &sub{object: ref.Name, ch: make(chan Event, 16)}
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
