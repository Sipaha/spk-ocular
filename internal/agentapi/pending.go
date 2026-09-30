package agentapi

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/store"
)

// Run states (RunView.State).
const (
	stateAwaiting = "awaiting_confirmation"
	stateRunning  = "running"
	stateDone     = "done"
	stateFailed   = "failed"
	stateRejected = "rejected"
	stateExpired  = "expired"
	stateGone     = "gone"
)

// A plan waits for the user this long; its outcome is kept this long after;
// at most maxPending wait at once; GetRun waits this long for a change
// (vars for tests).
var (
	pendingTTL = 10 * time.Minute
	keptTTL    = 10 * time.Minute
	getRunWait = 25 * time.Second
	maxPending = 20
)

// pendingRun is a destructive plan waiting for the user's decision, then
// its outcome.
type pendingRun struct {
	id      string
	p       *plan
	at      time.Time
	expires time.Time
	view    RunView
	ended   time.Time
	// changed closes (and is replaced) at every change of view.
	changed chan struct{}
}

type pendingSet struct {
	s      *Server
	mu     sync.Mutex
	runs   map[string]*pendingRun
	timer  *time.Timer
	closed bool
}

func newPendingSet(s *Server) *pendingSet {
	return &pendingSet{s: s, runs: map[string]*pendingRun{}}
}

// add makes p wait for the user (limit: too many wait).
func (ps *pendingSet) add(p *plan) (*pendingRun, error) {
	ps.mu.Lock()
	ps.sweepLocked()
	if ps.awaitingLocked() >= maxPending {
		ps.mu.Unlock()
		return nil, &api.CodedError{Code: api.CodeLimit, Detail: "too many plans wait for the user's confirmation; ask GetRun about those first"}
	}
	now := ps.s.now()
	r := &pendingRun{id: newID("run-"), p: p, at: now, expires: now.Add(pendingTTL), view: RunView{State: stateAwaiting}, changed: make(chan struct{})}
	r.view.RunID = r.id
	ps.runs[r.id] = r
	ps.armLocked()
	ps.mu.Unlock()
	ps.s.o.Service.Emit(api.EventAgentPendingChanged, "", nil)
	return r, nil
}

// setLocked changes r's view and wakes its waiters.
func (ps *pendingSet) setLocked(r *pendingRun, v RunView) {
	v.RunID = r.id
	r.view = v
	if v.State != stateAwaiting && v.State != stateRunning {
		r.ended = ps.s.now()
	}
	close(r.changed)
	r.changed = make(chan struct{})
}

func (ps *pendingSet) awaitingLocked() int {
	n := 0
	for _, r := range ps.runs {
		if r.view.State == stateAwaiting {
			n++
		}
	}
	return n
}

func (ps *pendingSet) awaiting() int {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.sweepLocked()
	return ps.awaitingLocked()
}

// sweepLocked expires plans past their time and forgets old outcomes.
func (ps *pendingSet) sweepLocked() bool {
	now := ps.s.now()
	expired := false
	for id, r := range ps.runs {
		switch {
		case r.view.State == stateAwaiting && !now.Before(r.expires):
			ps.setLocked(r, RunView{State: stateExpired})
			expired = true
			ps.s.auditAsync(store.AuditEntry{Agent: r.p.agent, Method: r.p.method, Provider: r.p.provider, Target: r.p.target, Scope: r.p.ref.Scope, Object: objectOf(r.p.ref), Verb: r.p.verb, Destructive: true, ExpectHash: expectHash(r.p), Phase: store.AuditOutcome, Outcome: stateExpired})
		case !r.ended.IsZero() && now.Sub(r.ended) >= keptTTL:
			delete(ps.runs, id)
		}
	}
	return expired
}

// armLocked keeps one timer for the next expiry while plans wait.
func (ps *pendingSet) armLocked() {
	if ps.timer != nil || ps.closed || ps.awaitingLocked() == 0 {
		return
	}
	var next time.Time
	for _, r := range ps.runs {
		if r.view.State == stateAwaiting && (next.IsZero() || r.expires.Before(next)) {
			next = r.expires
		}
	}
	ps.timer = time.AfterFunc(max(next.Sub(ps.s.now()), time.Millisecond), func() {
		ps.mu.Lock()
		ps.timer = nil
		expired := ps.sweepLocked()
		ps.armLocked()
		ps.mu.Unlock()
		if expired {
			ps.s.o.Service.Emit(api.EventAgentPendingChanged, "", nil)
		}
	})
}

// close: no more plans wait or are decided; the undecided are gone (their
// GetRun answered at once) and journaled so.
func (ps *pendingSet) close() {
	ps.mu.Lock()
	ps.closed = true
	if ps.timer != nil {
		ps.timer.Stop()
	}
	var gone []*pendingRun
	for _, r := range ps.runs {
		if r.view.State == stateAwaiting {
			ps.setLocked(r, RunView{State: stateGone})
			gone = append(gone, r)
		}
	}
	ps.mu.Unlock()
	for _, r := range gone {
		ps.s.audit(store.AuditEntry{Agent: r.p.agent, Method: r.p.method, Provider: r.p.provider, Target: r.p.target, Scope: r.p.ref.Scope, Object: objectOf(r.p.ref), Verb: r.p.verb, Destructive: true, ExpectHash: expectHash(r.p), Phase: store.AuditOutcome, Outcome: stateGone, Detail: "Ocular closed before the user decided"})
	}
	if len(gone) > 0 {
		ps.s.o.Service.Emit(api.EventAgentPendingChanged, "", nil)
	}
}

// decide: the user's yes runs the plan (its grants checked again at the
// write), no rejects it.
func (ps *pendingSet) decide(id string, approve bool) error {
	ps.mu.Lock()
	ps.sweepLocked()
	r := ps.runs[id]
	if r == nil || r.view.State != stateAwaiting || ps.closed {
		ps.mu.Unlock()
		return &api.CodedError{Code: api.CodeGone, Detail: "the plan no longer waits (decided, expired or withdrawn)"}
	}
	if !approve {
		ps.setLocked(r, RunView{State: stateRejected})
		ps.mu.Unlock()
		ps.s.o.Service.Emit(api.EventAgentPendingChanged, "", nil)
		ps.s.audit(store.AuditEntry{Agent: r.p.agent, Method: r.p.method, Provider: r.p.provider, Target: r.p.target, Scope: r.p.ref.Scope, Object: objectOf(r.p.ref), Verb: r.p.verb, Destructive: true, ExpectHash: expectHash(r.p), Phase: store.AuditOutcome, Outcome: stateRejected})
		return nil
	}
	if !ps.s.track() {
		ps.mu.Unlock()
		return &api.CodedError{Code: api.CodeGone, Detail: "Ocular is closing"}
	}
	ps.setLocked(r, RunView{State: stateRunning})
	ps.mu.Unlock()
	ps.s.o.Service.Emit(api.EventAgentPendingChanged, "", nil)
	go func() {
		defer ps.s.runs.Done()
		v := ps.s.execute(r.p)
		ps.mu.Lock()
		ps.setLocked(r, v)
		ps.mu.Unlock()
		ps.s.o.Service.Emit(api.EventAgentPendingChanged, "", nil)
	}()
	return nil
}

// wait answers r's state once it is not before, or after getRunWait.
func (ps *pendingSet) wait(ctx context.Context, id string) RunView {
	ps.mu.Lock()
	ps.sweepLocked()
	r := ps.runs[id]
	if r == nil {
		ps.mu.Unlock()
		return RunView{State: stateGone, RunID: id}
	}
	v, ch := r.view, r.changed
	ps.mu.Unlock()
	if v.State != stateAwaiting && v.State != stateRunning {
		return v
	}
	t := time.NewTimer(getRunWait)
	defer t.Stop()
	select {
	case <-ch:
	case <-t.C:
	case <-ctx.Done():
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return r.view
}

// list: the plans waiting, oldest first, as the UI shows them.
func (ps *pendingSet) list() []api.AgentPending {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.sweepLocked()
	out := []api.AgentPending{}
	for _, r := range ps.runs {
		if r.view.State != stateAwaiting {
			continue
		}
		out = append(out, api.AgentPending{ID: r.id, Agent: r.p.agent, At: r.at, Expires: r.expires, Provider: r.p.provider, Target: r.p.target, TargetTitle: r.p.title, Ref: r.p.ref, Action: r.p.actionPlan, Edit: r.p.editPlan})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

type GetRunRequest struct {
	RunID string `json:"runId" jsonschema:"required"`
}

func (s *Server) getRun(ctx context.Context, _ caller, req *GetRunRequest) (*RunView, error) {
	v := s.pend.wait(ctx, req.RunID)
	return &v, nil
}

// auditAsync journals off the caller's lock.
func (s *Server) auditAsync(e store.AuditEntry) {
	if !s.track() {
		slog.Warn("agent access: journal not written, Ocular is closing", "method", e.Method, "outcome", e.Outcome)
		return
	}
	go func() {
		defer s.runs.Done()
		s.audit(e)
	}()
}
