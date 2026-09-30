package api

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

var (
	restartAction = core.ActionDescriptor{ID: "restart", Title: "Restart"}
	scaleAction   = core.ActionDescriptor{ID: "scale", Title: "Scale", Param: &core.ActionParam{Kind: core.ParamCount, Min: 0, Max: 10}}
	deleteAction  = core.ActionDescriptor{ID: "delete", Title: "Delete", Destructive: true}
	deployRef     = core.Ref{Provider: "k", Target: "a", Scope: "ns", Kind: "deployments", Name: "web", UID: "uid-web"}
)

// actionSession offers restart, scale and delete on deployments and
// records the runs (with the session they ran on).
type actionSession struct {
	*fakeSession
	o *actionOpenable
}

func (a *actionSession) Kinds() []core.KindDescriptor {
	return []core.KindDescriptor{{ID: "deployments", Title: "Deployments", Scoped: true, Actions: []core.ActionDescriptor{restartAction, scaleAction, deleteAction}}}
}

func (a *actionSession) PrepareAction(_ context.Context, ref core.Ref, _ string, p core.ActionParams) (core.ActionPlan, error) {
	if h := a.o.duringPrepare; h != nil {
		h()
	}
	ref.UID = "uid-web"
	return core.ActionPlan{Where: core.LiveTarget{Provider: "k", Target: a.target, TargetTitle: a.target, Endpoint: "server", ConfigHash: a.hash, Ref: ref},
		Params: p, Rights: core.Rights{State: core.RightsAllowed}, Expect: "e1"}, nil
}

func (a *actionSession) RunAction(_ context.Context, run provider.ActionRun) (core.ActionResult, error) {
	a.o.mu.Lock()
	a.o.runs = append(a.o.runs, ranAction{run: run, on: a.fakeSession})
	h := a.o.duringRun
	a.o.mu.Unlock()
	if h != nil {
		h()
	}
	if r := a.o.result; r != nil {
		return *r, nil
	}
	return core.ActionResult{Message: core.Message{Text: run.Action + " requested"}}, nil
}

type ranAction struct {
	run provider.ActionRun
	on  *fakeSession
}

type actionOpenable struct {
	*openable
	mu            sync.Mutex
	runs          []ranAction
	duringPrepare func()
	duringRun     func()
	// result: what runs return (nil: done, \"<action> requested\")
	result *core.ActionResult
}

func (o *actionOpenable) Open(ctx context.Context, target string) (provider.Session, error) {
	s, _ := o.openable.Open(ctx, target)
	return &actionSession{fakeSession: s.(*fakeSession), o: o}, nil
}

func (o *actionOpenable) ran() []ranAction {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]ranAction(nil), o.runs...)
}

func newActionService(t *testing.T) (*Service, *actionOpenable) {
	t.Helper()
	k := &actionOpenable{openable: newOpenable("a", "b")}
	k.setHash("b", "hb")
	s, _ := newService(t, k)
	return s, k
}

func runFor(plan core.ActionPlan, count *int) ActionRunRequest {
	return ActionRunRequest{Ref: plan.Where.Ref, Action: plan.Action.ID, Params: core.ActionParams{Count: count}, Expect: plan.Expect, ConfigRev: plan.Where.ConfigRev}
}

func intp(n int) *int { return &n }

func TestAPlanNamesItsTargetFromTheSessionItWasReadIn(t *testing.T) {
	ctx := context.Background()
	s, k := newActionService(t)
	before := targetRev(t, s, "a")
	// The configuration changes while the plan is being read: the plan still
	// describes the session it came from, so running it is refused.
	k.duringPrepare = func() { k.setHash("a", "h2"); k.duringPrepare = nil }
	plan, err := s.PrepareAction(ctx, ActionRequest{Ref: deployRef, Action: "restart"})
	require.NoError(t, err)
	assert.Equal(t, before, plan.Where.ConfigRev)
	assert.Equal(t, "a", plan.Where.TargetTitle)
	assert.Equal(t, restartAction, plan.Action, "the descriptor is the session's, not the provider's plan")
	assert.Equal(t, "uid-web", plan.Where.Ref.UID)
	_, err = s.RunAction(ctx, runFor(plan, nil))
	assert.True(t, IsCoded(err, CodeConflict), "%v", err)
	assert.Empty(t, k.ran())
}

func TestInvalidRunsReachNoProvider(t *testing.T) {
	ctx := context.Background()
	s, k := newActionService(t)
	plan, err := s.PrepareAction(ctx, ActionRequest{Ref: deployRef, Action: "scale"})
	require.NoError(t, err)
	good := runFor(plan, intp(3))
	for name, c := range map[string]struct {
		mut  func(r *ActionRunRequest)
		code string
	}{
		"no uid":          {func(r *ActionRunRequest) { r.Ref.UID = "" }, CodeBadRequest},
		"no name":         {func(r *ActionRunRequest) { r.Ref.Name = "" }, CodeBadRequest},
		"no config rev":   {func(r *ActionRunRequest) { r.ConfigRev = "" }, CodeBadRequest},
		"no expect":       {func(r *ActionRunRequest) { r.Expect = "" }, CodeBadRequest},
		"no count":        {func(r *ActionRunRequest) { r.Params.Count = nil }, CodeBadRequest},
		"count too big":   {func(r *ActionRunRequest) { r.Params.Count = intp(11) }, CodeBadRequest},
		"negative count":  {func(r *ActionRunRequest) { r.Params.Count = intp(-1) }, CodeBadRequest},
		"count on delete": {func(r *ActionRunRequest) { r.Action = "delete" }, CodeBadRequest},
		"unknown action":  {func(r *ActionRunRequest) { r.Action = "explode" }, CodeUnsupported},
		"unknown kind":    {func(r *ActionRunRequest) { r.Ref.Kind = "nodes" }, CodeUnsupported},
		"another target":  {func(r *ActionRunRequest) { r.Ref.Target = "b" }, CodeConflict}, // b's revision differs
		"stale rev":       {func(r *ActionRunRequest) { r.ConfigRev = "0000000000000000" }, CodeConflict},
		"unknown target":  {func(r *ActionRunRequest) { r.Ref.Target = "zz" }, CodeNotFound},
	} {
		r := good
		r.Params.Count = intp(3)
		c.mut(&r)
		_, err := s.RunAction(ctx, r)
		assert.True(t, IsCoded(err, c.code), "%s: %v", name, err)
	}
	assert.Empty(t, k.ran())
	res, err := s.RunAction(ctx, good)
	require.NoError(t, err)
	assert.Equal(t, "scale requested", res.Message.Text)
	require.Len(t, k.ran(), 1)
	assert.Equal(t, provider.ActionRun{Ref: plan.Where.Ref, Action: "scale", Params: core.ActionParams{Count: intp(3)}, Expect: "e1"}, k.ran()[0].run)
}

func TestPrepareChecksTheActionAndParams(t *testing.T) {
	ctx := context.Background()
	s, _ := newActionService(t)
	_, err := s.PrepareAction(ctx, ActionRequest{Ref: deployRef, Action: "explode"})
	assert.True(t, IsCoded(err, CodeUnsupported), "%v", err)
	_, err = s.PrepareAction(ctx, ActionRequest{Ref: deployRef, Action: "scale", Params: core.ActionParams{Count: intp(99)}})
	assert.True(t, IsCoded(err, CodeBadRequest), "%v", err)
	plan, err := s.PrepareAction(ctx, ActionRequest{Ref: deployRef, Action: "scale"})
	require.NoError(t, err, "a scale plan before the count is chosen")
	assert.Nil(t, plan.Params.Count)
	noUID := deployRef
	noUID.UID = ""
	plan, err = s.PrepareAction(ctx, ActionRequest{Ref: noUID, Action: "delete"})
	require.NoError(t, err, "a plan pins an unpinned reference")
	assert.Equal(t, "uid-web", plan.Where.Ref.UID)
}

// A run is checked against the session it then runs on: a configuration
// change after that does not redirect it or turn its result into an error.
func TestARunStaysOnTheSessionItWasCheckedAgainst(t *testing.T) {
	ctx := context.Background()
	s, k := newActionService(t)
	plan, err := s.PrepareAction(ctx, ActionRequest{Ref: deployRef, Action: "restart"})
	require.NoError(t, err)
	k.duringRun = func() {
		k.setHash("a", "h2")
		_, _ = s.session(ctx, "k", "a") // the session is replaced meanwhile
	}
	res, err := s.RunAction(ctx, runFor(plan, nil))
	require.NoError(t, err)
	assert.Equal(t, "restart requested", res.Message.Text)
	runs := k.ran()
	require.Len(t, runs, 1)
	assert.Equal(t, "h1", runs[0].on.hash, "ran on the checked session")
}

// A run's own outcome is its result, not an error: a partial one keeps
// its parts; a provider that says nothing has done it.
func TestARunReportsItsOutcomeAndParts(t *testing.T) {
	ctx := context.Background()
	s, k := newActionService(t)
	plan, err := s.PrepareAction(ctx, ActionRequest{Ref: deployRef, Action: "restart"})
	require.NoError(t, err)
	res, err := s.RunAction(ctx, runFor(plan, nil))
	require.NoError(t, err)
	assert.Equal(t, core.OutcomeDone, res.Outcome)

	partial := core.ActionResult{Message: core.Message{Text: "1 of 3 restarted"}, Outcome: core.OutcomeUnknown, Parts: []core.ActionPart{
		{ID: "a", Title: "web-1", Outcome: core.OutcomeDone},
		{ID: "b", Title: "web-2", Outcome: core.OutcomeUnknown, Why: &core.Message{Text: "no answer within 25s"}},
		{ID: "c", Title: "web-3", Outcome: core.OutcomeSkipped},
	}}
	k.result = &partial
	res, err = s.RunAction(ctx, runFor(plan, nil))
	require.NoError(t, err)
	assert.Equal(t, partial, res)
}

// A session reaped and opened again with the same configuration keeps the
// plan valid (the revision is of the configuration, not the incarnation).
func TestAPlanOutlivesAReapWithTheSameConfiguration(t *testing.T) {
	ctx := context.Background()
	s, k := newActionService(t)
	plan, err := s.PrepareAction(ctx, ActionRequest{Ref: deployRef, Action: "delete"})
	require.NoError(t, err)
	s.sessMu.Lock()
	s.closeSessionLocked(ownerKey("k", "a"))
	s.sessMu.Unlock()
	_, err = s.RunAction(ctx, runFor(plan, nil))
	require.NoError(t, err)
	require.Len(t, k.ran(), 1)

	k.set("b") // the target leaves the configuration
	_, err = s.RunAction(ctx, runFor(plan, nil))
	assert.True(t, IsCoded(err, CodeNotFound), "%v", err)
	assert.Len(t, k.ran(), 1)
}

func TestActionsNeedAnActioner(t *testing.T) {
	s, _ := newService(t, newOpenable("a"))
	_, err := s.PrepareAction(context.Background(), ActionRequest{Ref: podRef, Action: "delete"})
	assert.True(t, IsCoded(err, CodeUnsupported), "%v", err)
	_, err = s.RunAction(context.Background(), ActionRunRequest{Ref: podRef, Action: "delete", Expect: "e", ConfigRev: targetRev(t, s, "a")})
	assert.True(t, IsCoded(err, CodeUnsupported), "%v", err)
}
