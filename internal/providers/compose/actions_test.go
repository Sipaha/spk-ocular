package compose

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/compose/engine"
)

func intp(n int) *int { return &n }

func started(c engine.ContainerInspect, at string) engine.ContainerInspect {
	t, _ := time.Parse(time.RFC3339, at)
	c.State.StartedAt = engine.TimeOf(t)
	return c
}

func keys(ms []core.Message) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, strings.TrimPrefix(m.Key, ProviderID+"."))
	}
	return out
}

// writes: the container writes the fake received (method and path, the
// stop timeout when sent).
func writes(e *testEnv) []string {
	var out []string
	for _, r := range e.fe.Requests() {
		if r.Method == http.MethodGet {
			continue
		}
		w := r.Method + " " + r.Path
		if t := r.Query.Get("t"); t != "" {
			w += " t=" + t
		}
		out = append(out, w)
	}
	return out
}

func runOf(plan core.ActionPlan, ref core.Ref) provider.ActionRun {
	return provider.ActionRun{Ref: ref, Action: plan.Action.ID, Expect: plan.Expect}
}

// The plan says what happens with the container's own stop signal and
// timeout, its AutoRemove and restart policy; the Engine checks nothing,
// and there are no rights to check.
func TestStopPlanShowsTheContainersOwnStop(t *testing.T) {
	e := newTestEnv(t)
	c := started(replica("aaaa1111", "p", "web", "1", "running"), "2026-09-30T10:00:00Z")
	c.Config.StopSignal, c.Config.StopTimeout = "SIGQUIT", intp(30)
	c.HostConfig.AutoRemove = true
	c.HostConfig.RestartPolicy.Name = "always"
	e.fe.PutContainer(c)
	plan, err := e.s.PrepareAction(t.Context(), ctrRef(c), "stop", core.ActionParams{})
	require.NoError(t, err)
	assert.Nil(t, plan.Unavailable)
	assert.Equal(t, []string{"act.stop", "act.autoRemove", "act.policyAlways"}, keys(plan.Effects))
	assert.Equal(t, map[string]string{"name": "p-web-1", "signal": "SIGQUIT", "timeout": "30"}, plan.Effects[0].Params)
	assert.Equal(t, []string{"act.noPrecondition"}, keys(plan.Warnings))
	assert.Equal(t, core.RightsUnknown, plan.Rights.State)
	assert.False(t, plan.Destructive)
	assert.Equal(t, c.ID, plan.Where.Ref.UID)
	assert.Equal(t, "p-web-1", plan.Where.Ref.Title)
	assert.Equal(t, e.s.cl.Endpoint(), plan.Where.Endpoint)
	assert.NotEmpty(t, plan.Expect)

	for _, x := range []struct {
		timeout *int
		key     string
	}{{intp(0), "act.stopKill"}, {intp(-1), "act.stopWait"}, {nil, "act.stop"}} {
		c.Config.StopTimeout = x.timeout
		c.Config.StopSignal = ""
		c.HostConfig.AutoRemove, c.HostConfig.RestartPolicy.Name = false, "unless-stopped"
		e.fe.PutContainer(c)
		plan, err := e.s.PrepareAction(t.Context(), ctrRef(c), "stop", core.ActionParams{})
		require.NoError(t, err)
		assert.Equal(t, []string{x.key}, keys(plan.Effects))
		if x.key == "act.stop" {
			assert.Equal(t, "10", plan.Effects[0].Params["timeout"], "the daemon's default")
			assert.Equal(t, "SIGTERM", plan.Effects[0].Params["signal"])
		}
	}

	plan, err = e.s.PrepareAction(t.Context(), ctrRef(c), "restart", core.ActionParams{})
	require.NoError(t, err)
	assert.Equal(t, []string{"act.restart"}, keys(plan.Effects))
}

func TestActionsUnavailableInTheContainersState(t *testing.T) {
	e := newTestEnv(t)
	running := replica("aaaa1111", "p", "web", "1", "running")
	paused := replica("bbbb2222", "p", "web", "2", "running")
	paused.State.Paused = true
	exited := replica("cccc3333", "p", "web", "3", "exited")
	removing := replica("dddd4444", "p", "web", "4", "removing")
	for _, c := range []engine.ContainerInspect{running, paused, exited, removing} {
		e.fe.PutContainer(c)
	}
	for _, x := range []struct {
		c      engine.ContainerInspect
		action string
		key    string // "": available
	}{
		{running, "start", "act.running"},
		{running, "stop", ""},
		{running, "delete", "act.stopFirst"},
		{running, "restart", ""},
		{paused, "start", "act.startPaused"},
		{paused, "stop", ""},
		{paused, "delete", "act.stopFirst"},
		{exited, "stop", "act.notRunning"},
		{exited, "start", ""},
		{exited, "delete", ""},
		{exited, "restart", ""},
		{removing, "restart", "act.removing"},
	} {
		plan, err := e.s.PrepareAction(t.Context(), ctrRef(x.c), x.action, core.ActionParams{})
		require.NoError(t, err)
		if x.key == "" {
			assert.Nil(t, plan.Unavailable, "%s %s", x.action, x.c.ID)
			continue
		}
		require.NotNil(t, plan.Unavailable, "%s %s", x.action, x.c.ID)
		assert.Equal(t, ProviderID+"."+x.key, plan.Unavailable.Key, "%s %s", x.action, x.c.ID)
		assert.Empty(t, plan.Effects)
	}
	plan, err := e.s.PrepareAction(t.Context(), ctrRef(exited), "delete", core.ActionParams{})
	require.NoError(t, err)
	assert.True(t, plan.Destructive)
	assert.Equal(t, []string{"act.remove"}, keys(plan.Effects))
	_, err = e.s.PrepareAction(t.Context(), svcRef("p", "web"), "delete", core.ActionParams{})
	assert.Equal(t, provider.ClassUnsupported, errClass(err), "a service is not removed here")
}

// A run sends exactly the timeout the plan showed, once, and says what
// happened; the fake's container is stopped.
func TestStopRunsWithTheShownTimeout(t *testing.T) {
	e := newTestEnv(t)
	c := replica("aaaa1111", "p", "web", "1", "running")
	c.Config.StopTimeout = intp(0)
	e.fe.PutContainer(c)
	plan, err := e.s.PrepareAction(t.Context(), ctrRef(c), "stop", core.ActionParams{})
	require.NoError(t, err)
	res, err := e.s.RunAction(t.Context(), runOf(plan, ctrRef(c)))
	require.NoError(t, err)
	assert.Equal(t, core.OutcomeDone, res.Outcome)
	assert.Equal(t, "container p-web-1 stopped", res.Message)
	assert.Empty(t, res.Parts)
	assert.Equal(t, []string{"POST /containers/aaaa1111/stop t=0"}, writes(e))
	got, err := e.s.cl.InspectContainer(t.Context(), c.ID)
	require.NoError(t, err)
	assert.Equal(t, "exited", got.State.Status)
	assert.Equal(t, 137, got.State.ExitCode)
}

// Anything the plan showed that changed is a conflict before any write:
// a restart (StartedAt), a new stop timeout, a state change.
func TestARunRefusesAChangedContainerBeforeWriting(t *testing.T) {
	e := newTestEnv(t)
	c := started(replica("aaaa1111", "p", "web", "1", "running"), "2026-09-30T10:00:00Z")
	e.fe.PutContainer(c)
	plan, err := e.s.PrepareAction(t.Context(), ctrRef(c), "stop", core.ActionParams{})
	require.NoError(t, err)
	for _, change := range []func(*engine.ContainerInspect){
		func(x *engine.ContainerInspect) { *x = started(*x, "2026-09-30T11:00:00Z") },
		func(x *engine.ContainerInspect) { x.Config.StopTimeout = intp(60) },
		func(x *engine.ContainerInspect) { x.Config.StopSignal = "SIGINT" },
		func(x *engine.ContainerInspect) { x.HostConfig.AutoRemove = true },
	} {
		x := c
		change(&x)
		e.fe.PutContainer(x)
		_, err = e.s.RunAction(t.Context(), runOf(plan, ctrRef(c)))
		assert.Equal(t, provider.ClassConflict, errClass(err))
	}
	e.fe.PutContainer(c)
	e.fe.RemoveContainer(c.ID)
	_, err = e.s.RunAction(t.Context(), runOf(plan, ctrRef(c)))
	assert.Equal(t, provider.ClassNotFound, errClass(err), "removed: nothing to act on")
	assert.Empty(t, writes(e), "nothing was sent")

	other := replica("bbbb2222", "p", "web", "1", "exited")
	e.fe.PutContainer(other)
	plan, err = e.s.PrepareAction(t.Context(), ctrRef(other), "start", core.ActionParams{})
	require.NoError(t, err)
	other.State = engine.ContainerState{Status: "running", Running: true}
	e.fe.PutContainer(other)
	_, err = e.s.RunAction(t.Context(), runOf(plan, ctrRef(other)))
	assert.Equal(t, provider.ClassConflict, errClass(err))
	assert.Empty(t, writes(e))
}

// The daemon's own answers: 304 is done ("already"), 409 its refusal in
// its words (invalid, not "review again"), 404 gone, a 5xx or no answer
// after sending unknown.
func TestContainerRunAnswers(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		class  provider.ErrorClass
		msg    string
	}{
		{"not modified", http.StatusNotModified, "", "", "container p-web-1 was running already"},
		{"refused", http.StatusConflict, `{"message":"cannot start: port is already allocated"}`, provider.ClassInvalid, "port is already allocated"},
		{"gone", http.StatusNotFound, `{"message":"No such container"}`, provider.ClassGone, "removed meanwhile"},
		{"5xx", http.StatusInternalServerError, `{"message":"driver failed"}`, provider.ClassUnknown, "may have done it"},
	}
	for _, x := range cases {
		t.Run(x.name, func(t *testing.T) {
			e := newTestEnv(t)
			c := replica("aaaa1111", "p", "web", "1", "exited")
			e.fe.PutContainer(c)
			plan, err := e.s.PrepareAction(t.Context(), ctrRef(c), "start", core.ActionParams{})
			require.NoError(t, err)
			e.fe.AddHook(func(w http.ResponseWriter, r *http.Request, p string) bool {
				if r.Method != http.MethodPost || !strings.HasSuffix(p, "/start") {
					return false
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(x.status)
				_, _ = w.Write([]byte(x.body))
				return true
			})
			res, err := e.s.RunAction(t.Context(), runOf(plan, ctrRef(c)))
			if x.class == "" {
				require.NoError(t, err)
				assert.Equal(t, x.msg, res.Message)
				return
			}
			assert.Equal(t, x.class, errClass(err))
			assert.Contains(t, err.Error(), x.msg)
		})
	}
}

// A service acts on the containers of its plan in replica order, one
// after another; those the action does not apply to are said and left.
func TestServiceStopActsOnItsRunningContainersInOrder(t *testing.T) {
	e := newTestEnv(t)
	one := replica("cccc1111", "p", "web", "1", "running")
	two := replica("aaaa2222", "p", "web", "2", "exited")
	three := replica("bbbb3333", "p", "web", "3", "running")
	three.Config.StopTimeout = intp(3)
	for _, c := range []engine.ContainerInspect{three, two, one, replica("dddd4444", "p", "db", "1", "running")} {
		e.fe.PutContainer(c)
	}
	plan, err := e.s.PrepareAction(t.Context(), svcRef("p", "web"), "stop", core.ActionParams{})
	require.NoError(t, err)
	assert.Equal(t, []string{"act.stop", "act.stop", "act.notTouched"}, keys(plan.Effects))
	assert.Equal(t, "p-web-2", plan.Effects[2].Params["name"])
	assert.Equal(t, []string{"act.noPrecondition", "act.newMembers"}, keys(plan.Warnings))
	assert.Equal(t, "p/web", plan.Where.Ref.UID)
	res, err := e.s.RunAction(t.Context(), runOf(plan, svcRef("p", "web")))
	require.NoError(t, err)
	assert.Equal(t, core.OutcomeDone, res.Outcome)
	assert.Equal(t, []core.ActionPart{
		{ID: one.ID, Title: "p-web-1", Outcome: core.OutcomeDone, Message: "container p-web-1 stopped"},
		{ID: three.ID, Title: "p-web-3", Outcome: core.OutcomeDone, Message: "container p-web-3 stopped"},
	}, res.Parts)
	assert.Equal(t, "2 of 2 containers stopped", res.Message)
	assert.Equal(t, []string{"POST /containers/cccc1111/stop t=10", "POST /containers/bbbb3333/stop t=3"}, writes(e))

	_, err = e.s.RunAction(t.Context(), runOf(plan, svcRef("p", "web")))
	assert.Equal(t, provider.ClassConflict, errClass(err), "the same plan again: its containers changed")
	plan, err = e.s.PrepareAction(t.Context(), svcRef("p", "web"), "stop", core.ActionParams{})
	require.NoError(t, err)
	require.NotNil(t, plan.Unavailable)
	assert.Equal(t, ProviderID+".act.serviceNone.stop", plan.Unavailable.Key)
}

// The set of containers is the plan's: one added before the run is a
// conflict (nothing sent).
func TestServiceRunRefusesAChangedSet(t *testing.T) {
	e := newTestEnv(t)
	one := replica("aaaa1111", "p", "web", "1", "running")
	e.fe.PutContainer(one)
	plan, err := e.s.PrepareAction(t.Context(), svcRef("p", "web"), "restart", core.ActionParams{})
	require.NoError(t, err)
	two := replica("bbbb2222", "p", "web", "2", "running")
	e.fe.PutContainer(two)
	e.fe.Emit(containerEvent("create", two))
	require.Eventually(t, func() bool { // the feed has it
		p, err := e.s.PrepareAction(t.Context(), svcRef("p", "web"), "restart", core.ActionParams{})
		return err == nil && len(p.Effects) == 2
	}, 5*time.Second, 20*time.Millisecond)
	_, err = e.s.RunAction(t.Context(), runOf(plan, svcRef("p", "web")))
	assert.Equal(t, provider.ClassConflict, errClass(err))
	assert.Empty(t, writes(e))
}

// The first refused or unknown write stops the rest; the run still
// answers with its parts (not an error): what was done stays said.
func TestServiceRunStopsAtTheFirstFailure(t *testing.T) {
	for _, x := range []struct {
		status  int
		outcome core.ActionOutcome
		summary string
	}{
		{http.StatusConflict, core.OutcomeRefused, "1 of 3 containers restarted; 1 refused; 1 not run"},
		{http.StatusInternalServerError, core.OutcomeUnknown, "1 of 3 containers restarted; 1 outcome unknown; 1 not run"},
	} {
		t.Run(string(x.outcome), func(t *testing.T) {
			e := newTestEnv(t)
			cs := []engine.ContainerInspect{
				replica("aaaa1111", "p", "web", "1", "running"),
				replica("bbbb2222", "p", "web", "2", "running"),
				replica("cccc3333", "p", "web", "3", "running"),
			}
			for _, c := range cs {
				e.fe.PutContainer(c)
			}
			plan, err := e.s.PrepareAction(t.Context(), svcRef("p", "web"), "restart", core.ActionParams{})
			require.NoError(t, err)
			var n atomic.Int32
			e.fe.AddHook(func(w http.ResponseWriter, _ *http.Request, p string) bool {
				if !strings.HasSuffix(p, "/restart") || n.Add(1) != 2 {
					return false
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(x.status)
				_, _ = w.Write([]byte(`{"message":"it failed"}`))
				return true
			})
			res, err := e.s.RunAction(t.Context(), runOf(plan, svcRef("p", "web")))
			require.NoError(t, err, "a write was sent: the result, not an error")
			assert.Equal(t, x.outcome, res.Outcome)
			require.Len(t, res.Parts, 3)
			assert.Equal(t, core.OutcomeDone, res.Parts[0].Outcome)
			assert.Equal(t, x.outcome, res.Parts[1].Outcome)
			assert.Contains(t, res.Parts[1].Message, "it failed")
			assert.Equal(t, core.ActionPart{ID: cs[2].ID, Title: "p-web-3", Outcome: core.OutcomeSkipped}, res.Parts[2])
			assert.Equal(t, x.summary, res.Message)
			assert.Len(t, writes(e), 2, "the third was never sent")
		})
	}
}

// Removing: the container goes; the daemon's refusal of a running one (a
// race after the plan) is its own words.
func TestRemoveRun(t *testing.T) {
	e := newTestEnv(t)
	c := replica("aaaa1111", "p", "web", "1", "exited")
	e.fe.PutContainer(c)
	plan, err := e.s.PrepareAction(t.Context(), ctrRef(c), "delete", core.ActionParams{})
	require.NoError(t, err)
	res, err := e.s.RunAction(t.Context(), runOf(plan, ctrRef(c)))
	require.NoError(t, err)
	assert.Equal(t, "container p-web-1 removed", res.Message)
	assert.Equal(t, []string{"DELETE /containers/aaaa1111"}, writes(e))
	_, err = e.s.cl.InspectContainer(t.Context(), c.ID)
	assert.True(t, engine.IsNotFound(err))
}
