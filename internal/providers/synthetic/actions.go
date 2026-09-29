package synthetic

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// Workloads are the objects actions change: restart counts restarts,
// scale sets the replicas, delete removes the row. Their views are live.
// Tests steer them through /api/_test/synthetic/... (Controls, Mutate,
// ResetActions).

const WorkloadKind = "workloads"

var (
	actRestart = core.ActionDescriptor{ID: "restart", Title: "Restart"}
	actScale   = core.ActionDescriptor{ID: "scale", Title: "Scale", Param: &core.ActionParam{Kind: core.ParamCount, Min: 0, Max: 10}}
	actDelete  = core.ActionDescriptor{ID: "delete", Title: "Delete", Destructive: true}
)

var workloadKind = core.KindDescriptor{
	ID: WorkloadKind, Title: "Workloads", Singular: "Workload", Group: "Synthetic", Aliases: []string{"wl"},
	Columns: []core.Column{
		{ID: "name", Title: "Name", Type: core.ColText},
		{ID: "replicas", Title: "Replicas", Type: core.ColNumber},
		{ID: "restarts", Title: "Restarts", Type: core.ColNumber},
	},
	Actions: []core.ActionDescriptor{actRestart, actScale, actDelete},
}

type workload struct {
	name               string
	uid                string
	replicas, restarts int
	paused, autoscaled bool
}

// workloadsAtStart: web (2 replicas), db (1, an autoscaler), paused
// (restart unavailable).
func workloadsAtStart() []*workload {
	return []*workload{
		{name: "web", uid: "uid-web", replicas: 2},
		{name: "db", uid: "uid-db", replicas: 1, autoscaled: true},
		{name: "paused", uid: "uid-paused", replicas: 1, paused: true},
	}
}

// Controls steer the actions for tests.
type Controls struct {
	// Rights the plans report ("" = allowed); denied runs are forbidden.
	Rights core.RightsState `json:"rights,omitempty"`
	// Fail: runs fail with this class; "unknown" changes the object first
	// (sent, outcome not known).
	Fail provider.ErrorClass `json:"fail,omitempty"`
	// DelayMS: a run takes this long (a cancelled one changes nothing).
	DelayMS int `json:"delay_ms,omitempty"`
}

// Mutation is a change by another actor.
type Mutation struct {
	Object   string `json:"object"`
	Replicas *int   `json:"replicas,omitempty"`
	Paused   *bool  `json:"paused,omitempty"`
	// Recreate replaces the object by a same-named one (a new UID).
	Recreate bool `json:"recreate,omitempty"`
}

// workloads is the provider's workload state; mu also orders the views'
// deliveries (a change and its delivery are one step).
type workloads struct {
	mu       sync.Mutex
	objs     []*workload
	gen      int // for new UIDs
	controls Controls
	watchers map[*watcher]struct{}
}

type watcher struct {
	name string // "" = all
	sink provider.Sink
}

func (w *workloads) init() {
	w.objs = workloadsAtStart()
	w.watchers = map[*watcher]struct{}{}
}

func (w *workloads) find(name string) *workload {
	for _, o := range w.objs {
		if o.name == name {
			return o
		}
	}
	return nil
}

func (o *workload) row() core.Row {
	r, n := float64(o.replicas), float64(o.restarts)
	return core.Row{
		ID: o.uid, Rev: fmt.Sprintf("%d.%d", o.replicas, o.restarts),
		Ref:    core.Ref{Provider: ID, Target: Target, Kind: WorkloadKind, Name: o.name, UID: o.uid},
		Cells:  []core.Cell{core.TextCell(o.name), core.NumCell(r, fmt.Sprint(o.replicas)), core.NumCell(n, fmt.Sprint(o.restarts))},
		Health: core.Health{State: core.HealthOK},
	}
}

// changed delivers o's row (or its removal when gone) to the views that
// show it; under mu.
func (w *workloads) changed(o *workload, gone bool) {
	for wt := range w.watchers {
		if wt.name != "" && wt.name != o.name {
			continue
		}
		if gone {
			wt.sink.Apply(provider.Delta{Deletes: []string{o.uid}})
		} else {
			wt.sink.Apply(provider.Delta{Upserts: []core.Row{o.row()}})
		}
	}
}

// resetViews sends every view its rows anew; under mu.
func (w *workloads) resetViews() {
	for wt := range w.watchers {
		w.deliverAll(wt)
	}
}

func (w *workloads) deliverAll(wt *watcher) {
	rows := []core.Row{}
	for _, o := range w.objs {
		if wt.name == "" || wt.name == o.name {
			rows = append(rows, o.row())
		}
	}
	wt.sink.Apply(provider.Delta{Reset: true, Upserts: rows, Status: &provider.ViewStatus{State: provider.StatusReady}})
}

// SetControls replaces the controls.
func (p *Provider) SetControls(c Controls) {
	p.wl.mu.Lock()
	defer p.wl.mu.Unlock()
	p.wl.controls = c
}

// Mutate changes a workload as another actor would.
func (p *Provider) Mutate(m Mutation) error {
	w := &p.wl
	w.mu.Lock()
	defer w.mu.Unlock()
	o := w.find(m.Object)
	if o == nil {
		return fmt.Errorf("no workload %q", m.Object)
	}
	if m.Recreate {
		w.changed(o, true)
		w.gen++
		o.uid, o.restarts = fmt.Sprintf("uid-%s-%d", o.name, w.gen), 0
	}
	if m.Replicas != nil {
		o.replicas = *m.Replicas
	}
	if m.Paused != nil {
		o.paused = *m.Paused
	}
	w.changed(o, false)
	return nil
}

// ResetActions brings back the workloads as at start and clears the
// controls; open views are reset.
func (p *Provider) ResetActions() {
	w := &p.wl
	w.mu.Lock()
	defer w.mu.Unlock()
	w.objs, w.controls = workloadsAtStart(), Controls{}
	w.resetViews()
}

func (s *session) watchWorkloads(q provider.Query, sink provider.Sink) (func(), error) {
	w := &s.p.wl
	wt := &watcher{name: q.Name, sink: sink}
	w.mu.Lock()
	w.watchers[wt] = struct{}{}
	w.deliverAll(wt)
	w.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			w.mu.Lock()
			delete(w.watchers, wt)
			w.mu.Unlock()
		})
	}, nil
}

func (s *session) getWorkload(name string) (*core.Resource, error) {
	w := &s.p.wl
	w.mu.Lock()
	defer w.mu.Unlock()
	o := w.find(name)
	if o == nil {
		return nil, &provider.Error{Class: provider.ClassNotFound, Message: "workload " + name + " not found"}
	}
	r := o.row()
	return &core.Resource{
		Ref: r.Ref, Health: r.Health,
		Facts: []core.Detail{{Key: "kind", Value: "Workload"}, {Key: "replicas", Value: fmt.Sprint(o.replicas)}, {Key: "restarts", Value: fmt.Sprint(o.restarts)}},
		YAML:  fmt.Sprintf("name: %s\nuid: %s\nreplicas: %d\nrestarts: %d\npaused: %t\n", o.name, o.uid, o.replicas, o.restarts, o.paused),
	}, nil
}

var _ provider.Actioner = (*session)(nil)

func expect(action string, p core.ActionParams, o *workload) string {
	count := -1
	if p.Count != nil {
		count = *p.Count
	}
	return fmt.Sprintf("%s|%d|%s|%d|%t", action, count, o.uid, o.replicas, o.paused)
}

func unavailable(action string, o *workload) *core.Message {
	if action == actRestart.ID && o.paused {
		return &core.Message{Text: o.name + " is paused: resume it first"}
	}
	return nil
}

// texts: messages without keys (the synthetic provider speaks English only).
func texts(ss ...string) []core.Message {
	out := make([]core.Message, 0, len(ss))
	for _, s := range ss {
		out = append(out, core.Message{Text: s})
	}
	return out
}

func instanceCount(n int) string {
	if n == 1 {
		return "1 instance is"
	}
	return fmt.Sprintf("%d instances are", n)
}

func actionOf(id string) (core.ActionDescriptor, error) {
	d, err := core.FindAction([]core.KindDescriptor{workloadKind}, WorkloadKind, id)
	if err != nil {
		return d, &provider.Error{Class: provider.ClassInvalid, Message: err.Error()}
	}
	return d, nil
}

func (s *session) PrepareAction(_ context.Context, ref core.Ref, action string, p core.ActionParams) (core.ActionPlan, error) {
	if ref.Kind != WorkloadKind {
		return core.ActionPlan{}, &provider.Error{Class: provider.ClassInvalid, Message: ref.Kind + " have no actions"}
	}
	d, err := actionOf(action)
	if err != nil {
		return core.ActionPlan{}, err
	}
	if err := d.CheckParams(p, false); err != nil {
		return core.ActionPlan{}, &provider.Error{Class: provider.ClassInvalid, Message: err.Error()}
	}
	w := &s.p.wl
	w.mu.Lock()
	defer w.mu.Unlock()
	o := w.find(ref.Name)
	if o == nil || (ref.UID != "" && ref.UID != o.uid) {
		return core.ActionPlan{}, &provider.Error{Class: provider.ClassNotFound, Message: "workload " + ref.Name + " not found"}
	}
	plan := core.ActionPlan{
		Where:  liveTarget(o.row().Ref, s.hash),
		Action: d, Params: p, Destructive: d.Destructive,
		Unavailable: unavailable(action, o),
		Expect:      expect(action, p, o),
		Rights:      core.Rights{State: core.RightsAllowed},
	}
	switch w.controls.Rights {
	case core.RightsDenied:
		plan.Rights = core.Rights{State: core.RightsDenied, Reason: fmt.Sprintf("you may not %s workloads (synthetic)", action)}
	case core.RightsUnknown:
		plan.Rights = core.Rights{State: core.RightsUnknown, Reason: "the check failed (synthetic)"}
	}
	switch action {
	case actRestart.ID:
		plan.Effects = texts("Its instances are replaced one by one.")
	case actScale.ID:
		n := o.replicas
		plan.Current = &n
		if o.autoscaled {
			plan.Warnings = texts("An autoscaler may override the count.")
		}
		if p.Count == nil {
			plan.Effects = texts(fmt.Sprintf("It has %d replicas now.", n))
			break
		}
		switch m := *p.Count; {
		case m == n:
			plan.Effects = texts(fmt.Sprintf("%d → %d: the count does not change.", n, m))
		case m == 0:
			plan.Effects = texts(fmt.Sprintf("%d → 0: all instances stop.", n))
			plan.Destructive = true
		case m < n:
			plan.Effects = texts(fmt.Sprintf("%d → %d: %s removed.", n, m, instanceCount(n-m)))
		default:
			plan.Effects = texts(fmt.Sprintf("%d → %d: %s added.", n, m, instanceCount(m-n)))
		}
	case actDelete.ID:
		plan.Effects = texts(fmt.Sprintf("Its %s removed too.", instanceCount(o.replicas)))
	}
	return plan, nil
}

func (s *session) RunAction(ctx context.Context, run provider.ActionRun) (core.ActionResult, error) {
	d, err := actionOf(run.Action)
	if err != nil {
		return core.ActionResult{}, err
	}
	if err := d.CheckParams(run.Params, true); err != nil {
		return core.ActionResult{}, &provider.Error{Class: provider.ClassInvalid, Message: err.Error()}
	}
	w := &s.p.wl
	w.mu.Lock()
	delay := time.Duration(w.controls.DelayMS) * time.Millisecond
	w.mu.Unlock()
	if delay > 0 {
		select {
		case <-ctx.Done():
			return core.ActionResult{}, ctx.Err()
		case <-time.After(delay):
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	o := w.find(run.Ref.Name)
	if o == nil || o.uid != run.Ref.UID {
		return core.ActionResult{}, &provider.Error{Class: provider.ClassGone, Message: "workload " + run.Ref.Name + " was deleted or replaced"}
	}
	if why := unavailable(run.Action, o); why != nil {
		return core.ActionResult{}, &provider.Error{Class: provider.ClassConflict, Message: why.Text}
	}
	if expect(run.Action, run.Params, o) != run.Expect {
		return core.ActionResult{}, &provider.Error{Class: provider.ClassConflict, Message: "workload " + o.name + " changed since the action was reviewed; review it again"}
	}
	c := w.controls
	if c.Rights == core.RightsDenied {
		return core.ActionResult{}, &provider.Error{Class: provider.ClassForbidden, Message: fmt.Sprintf("you may not %s workloads (synthetic)", run.Action)}
	}
	if c.Fail != "" && c.Fail != provider.ClassUnknown {
		return core.ActionResult{}, &provider.Error{Class: c.Fail, Message: "the run failed (synthetic)"}
	}
	var msg string
	switch run.Action {
	case actRestart.ID:
		o.restarts++
		msg = "workload " + o.name + ": restart requested"
		w.changed(o, false)
	case actScale.ID:
		msg = fmt.Sprintf("workload %s: scale %d → %d requested", o.name, o.replicas, *run.Params.Count)
		o.replicas = *run.Params.Count
		w.changed(o, false)
	case actDelete.ID:
		for i, x := range w.objs {
			if x == o {
				w.objs = append(w.objs[:i:i], w.objs[i+1:]...)
				break
			}
		}
		msg = "workload " + o.name + ": deletion requested"
		w.changed(o, true)
	}
	if c.Fail == provider.ClassUnknown {
		return core.ActionResult{}, &provider.Error{Class: provider.ClassUnknown, Message: "the request was sent but its outcome is not known (synthetic)"}
	}
	return core.ActionResult{Message: msg}, nil
}
