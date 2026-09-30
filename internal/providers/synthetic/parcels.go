package synthetic

import (
	"fmt"
	"sync"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// Parcels are a scoped kind with actions, in the crates' zones: agent
// access grants rights per scope, and its e2e needs a scoped kind a grant
// can let an agent change (stamp) and remove (delete, destructive).
// Crates stay without actions (the generic UI's regression).

const ParcelKind = "parcels"

var actStamp = core.ActionDescriptor{ID: "stamp", Title: "Stamp"}

var parcelKind = core.KindDescriptor{
	ID: ParcelKind, Title: "Parcels", Singular: "Parcel", Group: "Synthetic", Scoped: true,
	Columns: []core.Column{
		{ID: "name", Title: "Name", Type: core.ColText},
		{ID: "zone", Title: "Zone", Type: core.ColText, ScopeColumn: true},
		{ID: "stamps", Title: "Stamps", Type: core.ColNumber},
	},
	Actions: []core.ActionDescriptor{actStamp, actDelete},
}

type parcel struct {
	name, zone string
	stamps     int
}

func parcelsAtStart() []*parcel {
	return []*parcel{{name: "parcel-1", zone: "blue"}, {name: "parcel-2", zone: "blue"}, {name: "parcel-3", zone: "green"}}
}

type parcels struct {
	mu       sync.Mutex
	objs     []*parcel
	watchers map[*parcelWatcher]struct{}
}

type parcelWatcher struct {
	q    provider.Query
	sink provider.Sink
}

func (p *parcel) ref() core.Ref {
	return core.Ref{Provider: ID, Target: Target, Scope: p.zone, Kind: ParcelKind, Name: p.name, UID: "uid-" + p.name}
}

// deliverLocked sends every watcher its rows anew.
func (ps *parcels) deliverLocked() {
	for w := range ps.watchers {
		var rows []core.Row
		for _, p := range ps.objs {
			if w.q.Scope.Mode == core.ScopeOne && w.q.Scope.Name != p.zone || w.q.Name != "" && w.q.Name != p.name {
				continue
			}
			rows = append(rows, core.Row{
				ID: "uid-" + p.name, Rev: fmt.Sprint(p.stamps), Ref: p.ref(),
				Cells:  []core.Cell{core.TextCell(p.name), core.TextCell(p.zone), core.NumCell(float64(p.stamps), fmt.Sprint(p.stamps))},
				Health: core.Health{State: core.HealthOK},
			})
		}
		w.sink.Apply(provider.Delta{Reset: true, Upserts: rows, Status: &provider.ViewStatus{State: provider.StatusReady}})
	}
}

func (ps *parcels) findLocked(name string) *parcel {
	if ps.objs == nil {
		ps.objs = parcelsAtStart()
	}
	for _, p := range ps.objs {
		if p.name == name {
			return p
		}
	}
	return nil
}

func (ps *parcels) reset() {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.objs = parcelsAtStart()
	ps.deliverLocked()
}

func (s *session) watchParcels(q provider.Query, sink provider.Sink) (func(), error) {
	ps := &s.p.parcels
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.watchers == nil {
		ps.watchers = map[*parcelWatcher]struct{}{}
	}
	ps.findLocked("")
	w := &parcelWatcher{q: q, sink: sink}
	ps.watchers[w] = struct{}{}
	ps.deliverLocked()
	var once sync.Once
	return func() {
		once.Do(func() {
			ps.mu.Lock()
			delete(ps.watchers, w)
			ps.mu.Unlock()
		})
	}, nil
}

func (s *session) getParcel(ref core.Ref) (*core.Resource, error) {
	ps := &s.p.parcels
	ps.mu.Lock()
	defer ps.mu.Unlock()
	p := ps.findLocked(ref.Name)
	if p == nil {
		return nil, &provider.Error{Class: provider.ClassNotFound, Message: "parcel " + ref.Name + " not found"}
	}
	return &core.Resource{Ref: p.ref(), Health: core.Health{State: core.HealthOK}, Facts: []core.Detail{{Key: "zone", Value: p.zone}}, YAML: fmt.Sprintf("name: %s\nzone: %s\nstamps: %d\n", p.name, p.zone, p.stamps)}, nil
}

func parcelExpect(action string, p *parcel) string {
	return fmt.Sprintf("%s|%s|%d", action, p.name, p.stamps)
}

func (s *session) prepareParcel(ref core.Ref, action string, params core.ActionParams) (core.ActionPlan, error) {
	d, err := core.FindAction([]core.KindDescriptor{parcelKind}, ParcelKind, action)
	if err == nil {
		err = d.CheckParams(params, false)
	}
	if err != nil {
		return core.ActionPlan{}, &provider.Error{Class: provider.ClassInvalid, Message: err.Error()}
	}
	ps := &s.p.parcels
	ps.mu.Lock()
	defer ps.mu.Unlock()
	p := ps.findLocked(ref.Name)
	if p == nil {
		return core.ActionPlan{}, &provider.Error{Class: provider.ClassNotFound, Message: "parcel " + ref.Name + " not found"}
	}
	plan := core.ActionPlan{
		Where: liveTarget(p.ref(), s.hash), Action: d, Destructive: d.Destructive,
		Expect: parcelExpect(action, p), Rights: core.Rights{State: core.RightsAllowed},
	}
	if action == actDelete.ID {
		plan.Effects = texts(fmt.Sprintf("Parcel %s is removed.", p.name))
	} else {
		plan.Effects = texts(fmt.Sprintf("Parcel %s gets one more stamp.", p.name))
	}
	return plan, nil
}

func (s *session) runParcel(run provider.ActionRun) (core.ActionResult, error) {
	ps := &s.p.parcels
	ps.mu.Lock()
	defer ps.mu.Unlock()
	p := ps.findLocked(run.Ref.Name)
	if p == nil {
		return core.ActionResult{}, &provider.Error{Class: provider.ClassGone, Message: "parcel " + run.Ref.Name + " is gone"}
	}
	if parcelExpect(run.Action, p) != run.Expect {
		return core.ActionResult{}, &provider.Error{Class: provider.ClassConflict, Message: "parcel " + p.name + " changed since the action was reviewed; review it again"}
	}
	var msg string
	switch run.Action {
	case actStamp.ID:
		p.stamps++
		msg = "parcel " + p.name + ": stamped"
	case actDelete.ID:
		for i, x := range ps.objs {
			if x == p {
				ps.objs = append(ps.objs[:i:i], ps.objs[i+1:]...)
				break
			}
		}
		msg = "parcel " + p.name + ": deleted"
	default:
		return core.ActionResult{}, &provider.Error{Class: provider.ClassInvalid, Message: "parcels have no action " + run.Action}
	}
	ps.deliverLocked()
	return core.ActionResult{Message: core.Message{Text: msg}}, nil
}
