package synthetic

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// rowSink collects a view's rows.
type rowSink struct {
	mu   sync.Mutex
	rows map[string]core.Row
}

func (s *rowSink) Apply(d provider.Delta) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d.Reset || s.rows == nil {
		s.rows = map[string]core.Row{}
	}
	for _, r := range d.Upserts {
		s.rows[r.ID] = r
	}
	for _, id := range d.Deletes {
		delete(s.rows, id)
	}
}

func (s *rowSink) cells(name string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rows {
		if r.Ref.Name == name {
			var out []string
			for _, c := range r.Cells {
				out = append(out, c.Text)
			}
			return out
		}
	}
	return nil
}

func open(t *testing.T) (*Provider, *session) {
	t.Helper()
	p := New()
	s, err := p.Open(context.Background(), Target)
	require.NoError(t, err)
	return p, s.(*session)
}

func wref(name string) core.Ref {
	return core.Ref{Provider: ID, Target: Target, Kind: WorkloadKind, Name: name}
}

func count(n int) core.ActionParams { return core.ActionParams{Count: &n} }

// act prepares and runs like the UI (the plan's ref and Expect go back).
func act(t *testing.T, s *session, name, action string, p core.ActionParams) (core.ActionResult, error) {
	t.Helper()
	plan, err := s.PrepareAction(context.Background(), wref(name), action, p)
	require.NoError(t, err)
	return s.RunAction(context.Background(), provider.ActionRun{Ref: plan.Where.Ref, Action: action, Params: p, Expect: plan.Expect})
}

func class(t *testing.T, err error) provider.ErrorClass {
	t.Helper()
	var pe *provider.Error
	require.True(t, errors.As(err, &pe), "a provider error: %v", err)
	return pe.Class
}

func TestWorkloadsOfferActionsAndServicesDoNot(t *testing.T) {
	_, s := open(t)
	kinds := s.Kinds()
	require.Len(t, kinds, 5)
	assert.Empty(t, kinds[0].Actions, "services are for logs, terminals and tunnels")
	assert.Empty(t, kinds[2].Actions, "problems: a row offers its object's kind's")
	assert.Empty(t, kinds[3].Actions, "crates: a regression of the generic UI, no actions")
	var ids []string
	for _, a := range kinds[1].Actions {
		ids = append(ids, a.ID)
	}
	assert.Equal(t, []string{"restart", "scale", "undo", "pause", "resume", "delete", "forceDelete", "evacuate", "debug"}, ids)
}

func TestActionsChangeTheLiveView(t *testing.T) {
	_, s := open(t)
	view := &rowSink{}
	stop, err := s.Watch(provider.Query{Kind: WorkloadKind}, view)
	require.NoError(t, err)
	defer stop()
	assert.Equal(t, []string{"web", "2", "0"}, view.cells("web"))

	res, err := act(t, s, "web", "restart", core.ActionParams{})
	require.NoError(t, err)
	assert.Equal(t, "workload web: restart requested", res.Message.Text)
	assert.Equal(t, []string{"web", "2", "1"}, view.cells("web"))

	_, err = act(t, s, "web", "scale", count(5))
	require.NoError(t, err)
	assert.Equal(t, []string{"web", "5", "1"}, view.cells("web"))

	_, err = act(t, s, "web", "delete", core.ActionParams{})
	require.NoError(t, err)
	assert.Nil(t, view.cells("web"))
	_, err = s.Get(context.Background(), wref("web"))
	assert.Equal(t, provider.ClassNotFound, class(t, err))
}

func TestAViewOfOneObjectSeesOnlyIt(t *testing.T) {
	_, s := open(t)
	view := &rowSink{}
	stop, err := s.Watch(provider.Query{Kind: WorkloadKind, Name: "db"}, view)
	require.NoError(t, err)
	defer stop()
	_, err = act(t, s, "web", "scale", count(3))
	require.NoError(t, err)
	assert.Nil(t, view.cells("web"))
	assert.Equal(t, []string{"db", "1", "0"}, view.cells("db"))
	stop()
	_, err = act(t, s, "db", "scale", count(3))
	require.NoError(t, err)
	assert.Equal(t, []string{"db", "1", "0"}, view.cells("db"), "a stopped view gets nothing")
}

func TestPlans(t *testing.T) {
	_, s := open(t)
	ctx := context.Background()

	plan, err := s.PrepareAction(ctx, wref("web"), "scale", core.ActionParams{})
	require.NoError(t, err)
	require.NotNil(t, plan.Current)
	assert.Equal(t, 2, *plan.Current)
	assert.Equal(t, "uid-web", plan.Where.Ref.UID, "the plan pins the object")
	assert.Equal(t, "synthetic.local", plan.Where.Endpoint)
	assert.Equal(t, core.RightsAllowed, plan.Rights.State)

	plan, err = s.PrepareAction(ctx, wref("web"), "scale", count(0))
	require.NoError(t, err)
	assert.True(t, plan.Destructive, "scale to zero")
	assert.Contains(t, core.Texts(plan.Effects), "2 → 0: all instances stop.")

	plan, err = s.PrepareAction(ctx, wref("db"), "scale", count(2))
	require.NoError(t, err)
	assert.False(t, plan.Destructive)
	assert.Equal(t, []string{"An autoscaler may override the count."}, core.Texts(plan.Warnings))

	plan, err = s.PrepareAction(ctx, wref("db"), "delete", core.ActionParams{})
	require.NoError(t, err)
	assert.True(t, plan.Destructive)
	assert.Contains(t, core.Texts(plan.Effects), "Its 1 instance is removed too.")

	plan, err = s.PrepareAction(ctx, wref("paused"), "restart", core.ActionParams{})
	require.NoError(t, err)
	assert.Equal(t, "paused is paused: resume it first", plan.Unavailable.Text)
	_, err = s.RunAction(ctx, provider.ActionRun{Ref: plan.Where.Ref, Action: "restart", Expect: plan.Expect})
	assert.Equal(t, provider.ClassConflict, class(t, err))

	_, err = s.PrepareAction(ctx, wref("nope"), "delete", core.ActionParams{})
	assert.Equal(t, provider.ClassNotFound, class(t, err))
}

func TestAChangeByAnotherActorIsAConflict(t *testing.T) {
	p, s := open(t)
	ctx := context.Background()
	plan, err := s.PrepareAction(ctx, wref("web"), "scale", count(3))
	require.NoError(t, err)
	require.NoError(t, p.Mutate(Mutation{Object: "web", Replicas: new(7)}))
	_, err = s.RunAction(ctx, provider.ActionRun{Ref: plan.Where.Ref, Action: "scale", Params: count(3), Expect: plan.Expect})
	assert.Equal(t, provider.ClassConflict, class(t, err))
	got, err := s.Get(ctx, wref("web"))
	require.NoError(t, err)
	assert.Contains(t, got.YAML, "replicas: 7")

	// A different count than planned is refused as well.
	plan, err = s.PrepareAction(ctx, wref("web"), "scale", count(3))
	require.NoError(t, err)
	_, err = s.RunAction(ctx, provider.ActionRun{Ref: plan.Where.Ref, Action: "scale", Params: count(4), Expect: plan.Expect})
	assert.Equal(t, provider.ClassConflict, class(t, err))
}

func TestAReplacedObjectIsGone(t *testing.T) {
	p, s := open(t)
	ctx := context.Background()
	plan, err := s.PrepareAction(ctx, wref("web"), "delete", core.ActionParams{})
	require.NoError(t, err)
	require.NoError(t, p.Mutate(Mutation{Object: "web", Recreate: true}))
	_, err = s.RunAction(ctx, provider.ActionRun{Ref: plan.Where.Ref, Action: "delete", Expect: plan.Expect})
	assert.Equal(t, provider.ClassGone, class(t, err))
	_, err = s.Get(ctx, wref("web"))
	assert.NoError(t, err, "the new one is untouched")
}

func TestControls(t *testing.T) {
	ctx := context.Background()
	t.Run("denied rights: the plan says so and the run is forbidden", func(t *testing.T) {
		p, s := open(t)
		p.SetControls(Controls{Rights: core.RightsDenied})
		plan, err := s.PrepareAction(ctx, wref("web"), "restart", core.ActionParams{})
		require.NoError(t, err)
		assert.Equal(t, core.Rights{State: core.RightsDenied, Reason: "you may not restart workloads (synthetic)"}, plan.Rights)
		_, err = s.RunAction(ctx, provider.ActionRun{Ref: plan.Where.Ref, Action: "restart", Expect: plan.Expect})
		assert.Equal(t, provider.ClassForbidden, class(t, err))
	})
	t.Run("unknown rights", func(t *testing.T) {
		p, s := open(t)
		p.SetControls(Controls{Rights: core.RightsUnknown})
		plan, err := s.PrepareAction(ctx, wref("web"), "restart", core.ActionParams{})
		require.NoError(t, err)
		assert.Equal(t, core.RightsUnknown, plan.Rights.State)
		_, err = s.RunAction(ctx, provider.ActionRun{Ref: plan.Where.Ref, Action: "restart", Expect: plan.Expect})
		assert.NoError(t, err, "unknown is not denied")
	})
	t.Run("a failing run changes nothing", func(t *testing.T) {
		p, s := open(t)
		p.SetControls(Controls{Fail: provider.ClassInternal})
		_, err := act(t, s, "web", "scale", count(4))
		assert.Equal(t, provider.ClassInternal, class(t, err))
		got, _ := s.Get(ctx, wref("web"))
		assert.Contains(t, got.YAML, "replicas: 2")
	})
	t.Run("an unknown outcome did change it", func(t *testing.T) {
		p, s := open(t)
		p.SetControls(Controls{Fail: provider.ClassUnknown})
		_, err := act(t, s, "web", "scale", count(4))
		assert.Equal(t, provider.ClassUnknown, class(t, err))
		got, _ := s.Get(ctx, wref("web"))
		assert.Contains(t, got.YAML, "replicas: 4")
	})
	t.Run("a slow run waits, a cancelled one does nothing", func(t *testing.T) {
		p, s := open(t)
		p.SetControls(Controls{DelayMS: 150})
		start := time.Now()
		_, err := act(t, s, "web", "restart", core.ActionParams{})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, time.Since(start), 150*time.Millisecond)

		plan, err := s.PrepareAction(ctx, wref("web"), "delete", core.ActionParams{})
		require.NoError(t, err)
		cctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		defer cancel()
		_, err = s.RunAction(cctx, provider.ActionRun{Ref: plan.Where.Ref, Action: "delete", Expect: plan.Expect})
		require.Error(t, err)
		_, err = s.Get(ctx, wref("web"))
		assert.NoError(t, err)
	})
	t.Run("reset brings everything back", func(t *testing.T) {
		p, s := open(t)
		p.SetControls(Controls{Rights: core.RightsDenied})
		require.NoError(t, p.Mutate(Mutation{Object: "web", Replicas: new(9)}))
		view := &rowSink{}
		stop, err := s.Watch(provider.Query{Kind: WorkloadKind}, view)
		require.NoError(t, err)
		defer stop()
		p.ResetActions()
		assert.Equal(t, []string{"web", "2", "0"}, view.cells("web"))
		plan, err := s.PrepareAction(ctx, wref("web"), "delete", core.ActionParams{})
		require.NoError(t, err)
		assert.Equal(t, core.RightsAllowed, plan.Rights.State)
		_, err = s.RunAction(ctx, provider.ActionRun{Ref: plan.Where.Ref, Action: "delete", Expect: plan.Expect})
		require.NoError(t, err)
		p.ResetActions()
		assert.Equal(t, []string{"web", "2", "0"}, view.cells("web"), "a deleted object comes back")
	})
	t.Run("mutating what is not there", func(t *testing.T) {
		p, _ := open(t)
		assert.Error(t, p.Mutate(Mutation{Object: "nope"}))
	})
}

// Evacuate: the generic UI's lists and parts (a plan names every instance,
// a run reports each; what it leaves alone is a skipped part with a reason).
func TestEvacuateListsAndParts(t *testing.T) {
	p, s := open(t)
	ctx := context.Background()
	p.SetControls(Controls{Items: 120, Refuse: 2})
	plan, err := s.PrepareAction(ctx, wref("web"), "evacuate", core.ActionParams{})
	require.NoError(t, err)
	assert.True(t, plan.Destructive)
	require.Len(t, plan.Lists, 2)
	assert.Equal(t, "Moved", plan.Lists[0].Title.Text)
	assert.True(t, plan.Lists[0].Destructive)
	require.Len(t, plan.Lists[0].Items, 120, "whole, never cut")
	assert.Equal(t, "web-120", plan.Lists[0].Items[119].Name)
	assert.True(t, plan.Lists[1].Collapsed)
	assert.Equal(t, "web-helper", plan.Lists[1].Items[0].Name)

	res, err := s.RunAction(ctx, provider.ActionRun{Ref: plan.Where.Ref, Action: "evacuate", Expect: plan.Expect})
	require.NoError(t, err)
	require.Len(t, res.Parts, 121)
	assert.Equal(t, core.OutcomeDone, res.Parts[0].Outcome)
	assert.Equal(t, core.OutcomeRefused, res.Parts[119].Outcome)
	require.NotNil(t, res.Parts[119].Why)
	assert.Equal(t, "refused (synthetic)", res.Parts[119].Why.Text)
	assert.Equal(t, core.OutcomeSkipped, res.Parts[120].Outcome)
	assert.Equal(t, "left alone", res.Parts[120].Why.Text)
	assert.Equal(t, core.OutcomeRefused, res.Outcome, "a refusal outranks what was left")

	p.SetControls(Controls{Items: 2})
	plan, err = s.PrepareAction(ctx, wref("web"), "evacuate", core.ActionParams{})
	require.NoError(t, err)
	res, err = s.RunAction(ctx, provider.ActionRun{Ref: plan.Where.Ref, Action: "evacuate", Expect: plan.Expect})
	require.NoError(t, err)
	assert.Equal(t, core.OutcomeSkipped, res.Outcome, "done, but something left: not everything")
}

// Undo offers three revisions of web (the newest current); a revision
// rolled back to becomes the newest; paused ones wait for resume.
func TestWorkloadUndoPauseResume(t *testing.T) {
	_, s := open(t)
	ctx := context.Background()
	plan, err := s.PrepareAction(ctx, wref("web"), "undo", core.ActionParams{})
	require.NoError(t, err)
	require.Len(t, plan.Choices, 3)
	assert.Equal(t, "web-3", plan.Choices[0].Value)
	assert.True(t, plan.Choices[0].Current)
	assert.NotNil(t, plan.Choices[0].Unavailable)
	assert.Equal(t, "Revision 1", plan.Choices[2].Title.Text)

	one := "web-1"
	plan, err = s.PrepareAction(ctx, wref("web"), "undo", core.ActionParams{Choice: &one})
	require.NoError(t, err)
	require.Nil(t, plan.Unavailable)
	assert.Equal(t, []string{"image: app:3 → app:1"}, core.Texts(plan.Changes))
	res, err := act(t, s, "web", "undo", core.ActionParams{Choice: &one})
	require.NoError(t, err)
	assert.Equal(t, "workload web: rollback to revision 1 requested", res.Message.Text)
	plan, err = s.PrepareAction(ctx, wref("web"), "undo", core.ActionParams{})
	require.NoError(t, err)
	assert.Equal(t, "web-1", plan.Choices[0].Value, "the revision rolled back to is the newest")
	assert.Equal(t, "Revision 4", plan.Choices[0].Title.Text)
	assert.True(t, plan.Choices[0].Current)

	_, err = act(t, s, "web", "pause", core.ActionParams{})
	require.NoError(t, err)
	two := "web-2"
	plan, err = s.PrepareAction(ctx, wref("web"), "undo", core.ActionParams{Choice: &two})
	require.NoError(t, err)
	assert.NotNil(t, plan.Unavailable, "paused: resume first")
	_, err = act(t, s, "web", "pause", core.ActionParams{})
	assert.Equal(t, provider.ClassConflict, class(t, err), "already paused")
	_, err = act(t, s, "web", "resume", core.ActionParams{})
	require.NoError(t, err)
	_, err = act(t, s, "web", "undo", core.ActionParams{Choice: &two})
	require.NoError(t, err)
}

// Debug (as a pod's debug container): the defaults come back in the plan,
// the run asks for an attached terminal to the new debugger — an echo shell
// that ends with its exit code and cannot be reattached then.
func TestWorkloadDebugOpensAnAttachedTerminal(t *testing.T) {
	ctx := context.Background()
	p, s := open(t)
	plan, err := s.PrepareAction(ctx, wref("web"), "debug", core.ActionParams{})
	require.NoError(t, err)
	require.NotNil(t, plan.Params.Text)
	require.NotNil(t, plan.Params.Choice)
	assert.Equal(t, "busybox:1.36", *plan.Params.Text)
	assert.Equal(t, "main", *plan.Params.Choice)
	assert.Len(t, plan.Choices, 2)
	assert.Contains(t, plan.Effects[0].Text, "debugger-00001 with image busybox:1.36")
	assert.True(t, workloadKind.Actions[len(workloadKind.Actions)-1].NoAgents)

	bad, err := s.PrepareAction(ctx, wref("web"), "debug", core.ActionParams{Text: strp("a b")})
	require.NoError(t, err)
	require.NotNil(t, bad.Unavailable)

	img := "alpine:3"
	plan, err = s.PrepareAction(ctx, wref("web"), "debug", core.ActionParams{Text: &img})
	require.NoError(t, err)
	res, err := s.RunAction(ctx, provider.ActionRun{Ref: plan.Where.Ref, Action: "debug", Params: plan.Params, Expect: plan.Expect})
	require.NoError(t, err)
	require.NotNil(t, res.Terminal)
	to := *res.Terminal
	assert.Equal(t, core.TerminalOpen{Ref: plan.Where.Ref, Instance: "web", Channel: "debugger-00001", Attach: true}, to)

	_, err = s.PrepareExec(ctx, to.Ref, provider.ExecRequest{Instance: to.Instance, Channel: to.Channel})
	assert.Error(t, err, "a workload runs no commands: only its debuggers attach")
	_, err = s.PrepareExec(ctx, to.Ref, provider.ExecRequest{Channel: "debugger-99999", Attach: true})
	assert.Equal(t, provider.ClassNotFound, class(t, err))

	h, err := s.PrepareExec(ctx, to.Ref, provider.ExecRequest{Instance: to.Instance, Channel: to.Channel, Attach: true})
	require.NoError(t, err)
	assert.Equal(t, "debugger-00001", h.Describe().Channel)
	again, err := h.Again()
	require.NoError(t, err)
	tm := runTerm(h)
	tm.waitFor(t, "debugger debugger-00001 (alpine:3) on web\r\n$ ")
	_, _ = tm.in.Write([]byte("exit 3\r"))
	r := tm.wait(t)
	require.NoError(t, r.err)
	assert.Equal(t, provider.ExitStatus{Code: 3, Known: true}, r.st)

	r = runTerm(again).wait(t)
	assert.Equal(t, provider.ClassGone, class(t, r.err), "an ended debugger cannot restart")
	assert.Contains(t, r.err.Error(), "has ended")
	again.Close()
	assert.Equal(t, 0, p.LiveStats()["syn_handles"])
}

func strp(v string) *string { return &v }

// Only: a failure (or a denial) of one object among several, for a bulk
// run's mixed outcome.
func TestControlsOnlyOneObject(t *testing.T) {
	p, s := open(t)
	p.SetControls(Controls{Fail: provider.ClassConflict, Only: "db"})
	_, err := act(t, s, "db", "restart", core.ActionParams{})
	assert.Equal(t, provider.ClassConflict, class(t, err))
	_, err = act(t, s, "web", "restart", core.ActionParams{})
	assert.NoError(t, err)
	p.SetControls(Controls{Rights: core.RightsDenied, Only: "db"})
	plan, err := s.PrepareAction(context.Background(), wref("web"), "restart", core.ActionParams{})
	require.NoError(t, err)
	assert.Equal(t, core.RightsAllowed, plan.Rights.State)
	plan, err = s.PrepareAction(context.Background(), wref("db"), "restart", core.ActionParams{})
	require.NoError(t, err)
	assert.Equal(t, core.RightsDenied, plan.Rights.State)
}

// Stuck: a delete leaves the workload Terminating (its row stays, nothing
// but a forced deletion is offered); force delete removes it at once.
func TestStuckDeleteAndForceDelete(t *testing.T) {
	p, s := open(t)
	view := &rowSink{}
	stop, err := s.Watch(provider.Query{Kind: WorkloadKind}, view)
	require.NoError(t, err)
	defer stop()
	plan, err := s.PrepareAction(context.Background(), wref("web"), "forceDelete", core.ActionParams{})
	require.NoError(t, err)
	assert.Nil(t, plan.Unavailable)
	assert.Equal(t, []string{"It is not being deleted: a plain delete lets its instances stop; force delete is for one stuck in Terminating."}, core.Texts(plan.Warnings))

	p.SetControls(Controls{Stuck: true})
	_, err = act(t, s, "web", "delete", core.ActionParams{})
	require.NoError(t, err)
	view.mu.Lock()
	r := view.rows["uid-web"]
	view.mu.Unlock()
	assert.Equal(t, core.HealthTerminating, r.Health.State, "still there, terminating")
	plan, err = s.PrepareAction(context.Background(), wref("web"), "restart", core.ActionParams{})
	require.NoError(t, err)
	assert.Equal(t, "web is being deleted", plan.Unavailable.Text)
	plan, err = s.PrepareAction(context.Background(), wref("web"), "forceDelete", core.ActionParams{})
	require.NoError(t, err)
	assert.Nil(t, plan.Unavailable)
	assert.Empty(t, plan.Warnings)
	assert.Equal(t, []string{"It is removed at once, without waiting for its instances to stop."}, core.Texts(plan.Effects))
	res, err := s.RunAction(context.Background(), provider.ActionRun{Ref: plan.Where.Ref, Action: "forceDelete", Expect: plan.Expect})
	require.NoError(t, err)
	assert.Equal(t, "workload web: forced deletion requested", res.Message.Text)
	assert.Nil(t, view.cells("web"))
}
