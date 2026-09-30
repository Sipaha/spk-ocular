package agentapi

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// fakeProv is a target "t" of provider "k" with namespaced pods,
// deployments (actions, edits) and Secrets, nodes outside namespaces and a
// Problems view — the shapes the grants are judged by.
type fakeProv struct {
	mu        sync.Mutex
	identity  string
	hash      string
	allDenied bool
	// quiet: views never load (a snapshot waits for them).
	quiet bool
	// watched: the scopes views were opened with.
	watched []core.ScopeSel
	// daemon: sessions add a part of their own to the identity (as
	// Docker's daemon id), failing with daemonErr.
	daemon    bool
	daemonErr error
	runs      []provider.ActionRun
	edits     []provider.EditRun
	// runGate, when set, holds RunAction until it is closed or ctx ends.
	runGate chan struct{}
	opened  int
}

func newFake() *fakeProv { return &fakeProv{identity: "https://a | ca:1 | user:u", hash: "h1"} }

func (f *fakeProv) ID() string    { return "k" }
func (f *fakeProv) Title() string { return "Fake" }
func (f *fakeProv) Discover(context.Context) (provider.Discovery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return provider.Discovery{Targets: []core.Target{{Provider: "k", ID: "t", Title: "Target T", ConfigHash: f.hash, Identity: f.identity}}}, nil
}
func (f *fakeProv) Open(context.Context, string) (provider.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened++
	if f.daemon {
		return &daemonSess{&fakeSess{f: f, hash: f.hash}}, nil
	}
	return &fakeSess{f: f, hash: f.hash}, nil
}

// daemonSess asks its daemon for its part of the identity.
type daemonSess struct{ *fakeSess }

func (s *daemonSess) Identity(context.Context) (string, error) {
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	if s.f.daemonErr != nil {
		return "", s.f.daemonErr
	}
	return "daemon:1", nil
}

func (f *fakeProv) setIdentity(id string) { f.mu.Lock(); f.identity = id; f.mu.Unlock() }

type fakeSess struct {
	f    *fakeProv
	hash string
}

var (
	actRestart = core.ActionDescriptor{ID: "restart", Title: "Restart"}
	actScale   = core.ActionDescriptor{ID: "scale", Title: "Scale", Param: &core.ActionParam{Kind: core.ParamCount, Min: 0, Max: 10}}
	actDelete  = core.ActionDescriptor{ID: "delete", Title: "Delete", Destructive: true}
	actCordon  = core.ActionDescriptor{ID: "cordon", Title: "Cordon"}
	actUndo    = core.ActionDescriptor{ID: "undo", Title: "Roll back", Param: &core.ActionParam{Kind: core.ParamChoice}}
)

var fakeKinds = []core.KindDescriptor{
	{ID: "pods", Title: "Pods", Scoped: true, Logs: true, Columns: []core.Column{{ID: "name", Title: "Name"}, {ID: "cpu", Title: "CPU", Metric: true}, {ID: "age", Title: "Age", Type: core.ColAge}}},
	{ID: "apps/deployments", Title: "Deployments", Scoped: true, Editable: true, Actions: []core.ActionDescriptor{actRestart, actScale, actUndo, actDelete}, Columns: []core.Column{{ID: "name", Title: "Name"}}},
	{ID: "secrets", Title: "Secrets", Scoped: true, Editable: true, Sensitive: true, Actions: []core.ActionDescriptor{actDelete}, Columns: []core.Column{{ID: "name", Title: "Name"}}},
	{ID: "events", Title: "Events", Scoped: true, Columns: []core.Column{{ID: "name", Title: "Name"}}},
	{ID: "nodes", Title: "Nodes", Actions: []core.ActionDescriptor{actCordon}, Columns: []core.Column{{ID: "name", Title: "Name"}}},
	{ID: "problems", Title: "Problems", Scoped: true, Columns: []core.Column{{ID: "name", Title: "Name"}}},
}

func ref(scope, kind, name string) core.Ref {
	return core.Ref{Provider: "k", Target: "t", Scope: scope, Kind: kind, Name: name, UID: "uid-" + name}
}

// world: the objects by kind.
var world = map[string][]core.Ref{
	"pods":             {ref("a", "pods", "web-1"), ref("b", "pods", "api-1"), ref("c", "pods", "x-1")},
	"apps/deployments": {ref("a", "apps/deployments", "web"), ref("b", "apps/deployments", "api")},
	"secrets":          {ref("a", "secrets", "db")},
	"events":           {ref("a", "events", "web-1.1")},
	"nodes":            {ref("", "nodes", "n1")},
}

func (s *fakeSess) ConfigHash() string           { return s.hash }
func (s *fakeSess) Kinds() []core.KindDescriptor { return fakeKinds }
func (s *fakeSess) Scopes(context.Context) ([]core.Scope, error) {
	return []core.Scope{{Name: "a"}, {Name: "b"}, {Name: "c"}}, nil
}
func (s *fakeSess) ScopeKind() string { return "" }
func (s *fakeSess) Close()            {}

func rowOf(r core.Ref) core.Row {
	n := 0.25
	h := core.Health{State: core.HealthOK}
	if r.Name == "api" && r.Kind == "apps/deployments" {
		h.Reason = "Paused"
	}
	return core.Row{ID: r.Kind + "#" + r.UID, Ref: r, Cells: []core.Cell{core.TextCell(r.Name), {Num: &n}, core.TimeCell(0)}, Health: h}
}

func (s *fakeSess) Watch(q provider.Query, sink provider.Sink) (func(), error) {
	s.f.mu.Lock()
	denied, quiet := s.f.allDenied, s.f.quiet
	s.f.watched = append(s.f.watched, q.Scope)
	s.f.mu.Unlock()
	if quiet {
		return func() {}, nil
	}
	if q.Scope.Mode == core.ScopeAll && denied {
		sink.Apply(provider.Delta{Status: &provider.ViewStatus{State: provider.StatusError, Class: provider.ClassForbidden, Message: "pods is forbidden at the cluster scope"}})
		return func() {}, nil
	}
	in := func(r core.Ref) bool {
		switch q.Scope.Mode {
		case core.ScopeOne:
			return r.Scope == q.Scope.Name || r.Scope == "" && q.Kind == "problems"
		case core.ScopeNone:
			return r.Scope == ""
		}
		return true
	}
	var rows []core.Row
	if q.Kind == "problems" {
		for _, k := range []string{"pods", "events", "nodes"} {
			for _, r := range world[k] {
				if in(r) {
					rows = append(rows, rowOf(r))
				}
			}
		}
	} else {
		for _, r := range world[q.Kind] {
			if in(r) {
				rows = append(rows, rowOf(r))
			}
		}
	}
	sink.Apply(provider.Delta{Reset: true, Upserts: rows, Status: &provider.ViewStatus{State: provider.StatusReady}})
	return func() {}, nil
}

func (s *fakeSess) Get(_ context.Context, r core.Ref) (*core.Resource, error) {
	if r.Name == "stray" {
		// As a provider finding objects by name alone would: stray lives
		// in b whatever scope was asked.
		r.Scope = "b"
	}
	return &core.Resource{Ref: r, Health: core.Health{State: core.HealthOK}, YAML: "name: " + r.Name + "\n", Facts: []core.Detail{{Key: "Name", Value: r.Name}},
		Relations: []core.Relation{
			{Type: "owns", Ref: ref("a", "pods", "web-1")},
			{Type: "selects", Ref: ref("b", "pods", "api-1")},
			{Type: "runs-on", Ref: ref("", "nodes", "n1")},
		}}, nil
}

func (s *fakeSess) PrepareAction(_ context.Context, r core.Ref, action string, p core.ActionParams) (core.ActionPlan, error) {
	plan := core.ActionPlan{Where: core.LiveTarget{Provider: "k", Target: "t", TargetTitle: "Target T", Ref: r}, Params: p, Rights: core.Rights{State: core.RightsAllowed},
		Effects: []core.Message{{Key: "k.effect", Text: action + " " + r.Name}}, Expect: "e-" + action + "-" + r.Name}
	switch action {
	case actScale.ID:
		plan.Destructive = p.Count != nil && *p.Count == 0
	case actDelete.ID:
		plan.Destructive = true
	case actUndo.ID:
		why := core.Message{Text: "revision 2 is the current template"}
		plan.Choices = []core.ActionChoice{
			{Value: "web-2", Title: core.Message{Text: "Revision 2"}, Current: true, Unavailable: &why, Details: []core.Message{{Text: "Images: app:b"}}, At: 1790762400000},
			{Value: "web-1", Title: core.Message{Text: "Revision 1"}},
		}
		switch {
		case p.Choice == nil:
		case *p.Choice == "web-1":
			plan.Changes = []core.Message{{Text: "containers[app].image: app:b → app:a"}}
		default:
			return core.ActionPlan{}, &provider.Error{Class: provider.ClassInvalid, Message: "no revision " + *p.Choice}
		}
	}
	if r.Name == "listy" {
		plan.Lists = []core.ActionList{{Title: core.Message{Text: "Moved"}, Items: []core.ActionItem{{Name: "b/api-1", Ref: &core.Ref{Provider: "k", Target: "t", Scope: "b", Kind: "pods", Name: "api-1"}}}}}
	}
	return plan, nil
}

func (s *fakeSess) RunAction(ctx context.Context, run provider.ActionRun) (core.ActionResult, error) {
	s.f.mu.Lock()
	gate := s.f.runGate
	s.f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return core.ActionResult{}, &provider.Error{Class: provider.ClassUnknown, Message: "the write was cut: " + ctx.Err().Error()}
		}
	}
	if run.Expect != "e-"+run.Action+"-"+run.Ref.Name {
		return core.ActionResult{}, &provider.Error{Class: provider.ClassConflict, Message: "changed"}
	}
	s.f.mu.Lock()
	s.f.runs = append(s.f.runs, run)
	s.f.mu.Unlock()
	return core.ActionResult{Message: core.Message{Text: fmt.Sprintf("%s %s done", run.Action, run.Ref.Name)}, Outcome: core.OutcomeDone}, nil
}

func (s *fakeSess) EditSource(_ context.Context, r core.Ref) (core.EditDoc, provider.EditBase, error) {
	return core.EditDoc{Ref: r, Text: "name: " + r.Name + "\nsecret: SECRET-TEXT\n", Version: "1"}, provider.EditBase{Route: "r", Name: r.Name, UID: r.UID, Version: "1"}, nil
}

func (s *fakeSess) PrepareEdit(_ context.Context, req provider.EditRequest) (core.EditPlan, *provider.EditGrant, error) {
	p := core.EditPlan{Where: core.LiveTarget{Provider: "k", Target: "t", Ref: req.Ref}, Before: req.Original, After: req.Edited, Changed: req.Original != req.Edited, Checked: true,
		Destructive: strings.Contains(req.Edited, "DESTROY"), Rights: core.Rights{State: core.RightsAllowed}}
	if !p.Changed {
		return p, nil, nil
	}
	return p, &provider.EditGrant{Route: "r", Name: req.Ref.Name, UID: req.Ref.UID, Version: "1", PatchHash: "p", Mode: "checked"}, nil
}

func (s *fakeSess) RunEdit(_ context.Context, run provider.EditRun) (core.EditResult, error) {
	s.f.mu.Lock()
	s.f.edits = append(s.f.edits, run)
	s.f.mu.Unlock()
	return core.EditResult{Message: "edited " + run.Ref.Name, Version: "2"}, nil
}

func (s *fakeSess) LogInfo(context.Context, core.Ref) (core.LogInfo, error) {
	return core.LogInfo{}, nil
}
func (s *fakeSess) StreamLogs(_ context.Context, r core.Ref, _ provider.LogQuery, sink provider.LogSink) error {
	_ = sink.Source(1, "k1", r.Name, "main")
	_ = sink.Lines(1, []provider.LogLine{{TS: "t1", Text: "hello from " + r.Name}})
	return sink.Ready()
}

func (s *fakeSess) Metrics(_ context.Context, _ provider.Query, ids []string) (provider.Metrics, error) {
	cpu := 0.5
	out := provider.Metrics{Timestamp: time.Unix(0, 0), Window: "30s", Values: map[string]provider.Usage{}}
	for _, id := range ids {
		out.Values[id] = provider.Usage{CPU: &cpu}
	}
	return out, nil
}
