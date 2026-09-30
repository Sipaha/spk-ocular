package compose

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/providers/compose/engine"
)

// The container health table of decision 6, row by row.
func TestContainerHealthTable(t *testing.T) {
	now := t0
	cases := []struct {
		name   string
		opts   []ctrOpt
		state  core.HealthState
		reason string
		msg    string
		since  time.Time
		next   time.Time
		issues []string // reasons, worst first (nil: not checked)
	}{
		{name: "running without healthcheck", state: core.HealthOK},
		{name: "running healthy", opts: []ctrOpt{health("healthy")}, state: core.HealthOK},
		{name: "running starting", opts: []ctrOpt{health("starting")}, state: core.HealthProgressing, reason: "Starting"},
		{
			name: "running unhealthy: the last failing check is the evidence, no onset",
			opts: []ctrOpt{health("unhealthy",
				check(1, "first failure", now.Add(-time.Minute)),
				check(0, "fine", now.Add(-50*time.Second)),
				check(1, "curl: (7) Failed to connect\n", now.Add(-10*time.Second)))},
			state: core.HealthError, reason: "Unhealthy", msg: "curl: (7) Failed to connect",
		},
		{name: "restarting", opts: []ctrOpt{status("restarting"), func(c *engine.ContainerInspect) {
			c.State.ExitCode = 2
			c.State.FinishedAt = engine.TimeOf(now.Add(-5 * time.Second))
		}}, state: core.HealthWarning, reason: "Restarting", msg: "restarting after exit code 2", since: now.Add(-5 * time.Second)},
		{name: "paused", opts: []ctrOpt{status("paused")}, state: core.HealthWarning, reason: "Paused"},
		{name: "created", opts: []ctrOpt{status("created")}, state: core.HealthProgressing, reason: "Created"},
		{name: "exited 0", opts: []ctrOpt{exited(0, now.Add(-time.Hour))}, state: core.HealthOK, reason: "Completed"},
		{name: "exited 3", opts: []ctrOpt{exited(3, now.Add(-time.Hour))}, state: core.HealthError, reason: "Exited", msg: "exit code 3", since: now.Add(-time.Hour)},
		{name: "exited 137 OOM", opts: []ctrOpt{exited(137, now.Add(-time.Hour)), func(c *engine.ContainerInspect) { c.State.OOMKilled = true }},
			state: core.HealthError, reason: "Exited", msg: "exit code 137, killed for running out of memory", since: now.Add(-time.Hour)},
		{name: "dead", opts: []ctrOpt{status("dead"), func(c *engine.ContainerInspect) { c.State.Error = "driver failed" }},
			state: core.HealthError, reason: "Dead", msg: "driver failed"},
		{name: "removing", opts: []ctrOpt{status("removing")}, state: core.HealthTerminating, reason: "Removing"},
		{name: "unknown state", opts: []ctrOpt{status("weird")}, state: core.HealthUnknown, reason: "Unknown", msg: "unknown state weird"},

		// Restarted by the restart manager (RestartCount > 0, started in the last 10 minutes).
		{name: "restarted by policy 3m ago", opts: []ctrOpt{restarted(2, now.Add(-3*time.Minute))},
			state: core.HealthWarning, reason: "RestartedByPolicy", msg: "restarted by its restart policy (2 restarts in total)",
			since: now.Add(-3 * time.Minute), next: now.Add(7 * time.Minute)},
		{name: "restarted 11m ago: over", opts: []ctrOpt{restarted(2, now.Add(-11*time.Minute))}, state: core.HealthOK},
		{name: "restarted exactly 10m ago: over", opts: []ctrOpt{restarted(1, now.Add(-10*time.Minute))}, state: core.HealthOK},
		{name: "started 1m ago without restarts", opts: []ctrOpt{restarted(0, now.Add(-time.Minute))}, state: core.HealthOK},
		{name: "restart count without a start time", opts: []ctrOpt{restarted(3, time.Time{})}, state: core.HealthOK},
		{name: "start far in the future is clock skew: nothing, until it is near",
			opts: []ctrOpt{restarted(1, now.Add(5*time.Minute))}, state: core.HealthOK, next: now.Add(4 * time.Minute)},
		{name: "start slightly ahead counts as now", opts: []ctrOpt{restarted(1, now.Add(30*time.Second))},
			state: core.HealthWarning, reason: "RestartedByPolicy", msg: "restarted by its restart policy (1 restart in total)",
			since: now, next: now.Add(30*time.Second + 10*time.Minute)},
		{name: "exited outranks a recent restart", opts: []ctrOpt{restarted(2, now.Add(-2*time.Minute)), exited(1, now.Add(-time.Minute))},
			state: core.HealthError, reason: "Exited", msg: "exit code 1", since: now.Add(-time.Minute), issues: []string{"Exited"}},
		{name: "exited 0 outranks a recent restart", opts: []ctrOpt{restarted(2, now.Add(-2*time.Minute)), exited(0, now.Add(-time.Minute))},
			state: core.HealthOK, reason: "Completed"},
		{name: "dead outranks a recent restart", opts: []ctrOpt{restarted(2, now.Add(-2*time.Minute)), status("dead")},
			state: core.HealthError, reason: "Dead", issues: []string{"Dead"}},
		{name: "removing outranks a recent restart", opts: []ctrOpt{restarted(2, now.Add(-2*time.Minute)), status("removing")},
			state: core.HealthTerminating, reason: "Removing", issues: []string{"Removing"}},
		{name: "unhealthy and restarted: both, the error first",
			opts:  []ctrOpt{restarted(1, now.Add(-2*time.Minute)), health("unhealthy", check(1, "down", now))},
			state: core.HealthError, reason: "Unhealthy", msg: "down", next: now.Add(8 * time.Minute),
			issues: []string{"Unhealthy", "RestartedByPolicy"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, next := containerHealth(ctr("c1", "p-web-1", "p", "web", c.opts...), now)
			assert.Equal(t, c.state, h.State)
			assert.Equal(t, c.reason, h.Reason)
			assert.Equal(t, c.msg, h.Message)
			assert.Equal(t, c.next, next)
			if len(h.Issues) > 0 {
				assert.Equal(t, unixMs(c.since), h.Issues[0].Since, "since")
			} else {
				assert.True(t, c.since.IsZero(), "an issue with an onset")
			}
			if c.issues != nil {
				var got []string
				for _, is := range h.Issues {
					got = append(got, is.Reason)
				}
				assert.Equal(t, c.issues, got)
			}
		})
	}
}

// The service precedence of decision 6: error > warning > progressing >
// all terminating > all exit 0 (Completed) > ok; the worst reason says
// how many of the members have it.
func TestServiceHealthPrecedence(t *testing.T) {
	now := t0
	run := func(id string, o ...ctrOpt) *engine.ContainerInspect {
		return ctr(id, "p-web-"+id, "p", "web", o...)
	}
	cases := []struct {
		name    string
		members []*engine.ContainerInspect
		state   core.HealthState
		reason  string
		msg     string
		next    time.Time
	}{
		{"one running with a starting healthcheck", []*engine.ContainerInspect{run("1", health("starting"))},
			core.HealthProgressing, "Starting", "1 of 1 containers", time.Time{}},
		{"all created", []*engine.ContainerInspect{run("1", status("created")), run("2", status("created"))},
			core.HealthProgressing, "Created", "2 of 2 containers", time.Time{}},
		{"all removing", []*engine.ContainerInspect{run("1", status("removing")), run("2", status("removing"))},
			core.HealthTerminating, "Removing", "2 of 2 containers", time.Time{}},
		{"removing beside running is not terminating", []*engine.ContainerInspect{run("1", status("removing")), run("2")},
			core.HealthOK, "", "", time.Time{}},
		{"running + completed", []*engine.ContainerInspect{run("1"), run("2", exited(0, now))},
			core.HealthOK, "", "", time.Time{}},
		{"all completed", []*engine.ContainerInspect{run("1", exited(0, now)), run("2", exited(0, now))},
			core.HealthOK, "Completed", "", time.Time{}},
		{"running + exited 3", []*engine.ContainerInspect{run("1"), run("2", exited(3, now))},
			core.HealthError, "Exited", "1 of 2 containers; p-web-2: exit code 3", time.Time{}},
		{"the most common error wins", []*engine.ContainerInspect{
			run("1", exited(3, now)), run("2", health("unhealthy")), run("3", health("unhealthy"))},
			core.HealthError, "Unhealthy", "2 of 3 containers", time.Time{}},
		{"error beats warning and progressing", []*engine.ContainerInspect{
			run("1", status("paused")), run("2", status("created")), run("3", status("dead"))},
			core.HealthError, "Dead", "1 of 3 containers", time.Time{}},
		{"warning beats progressing", []*engine.ContainerInspect{run("1", status("paused")), run("2", health("starting"))},
			core.HealthWarning, "Paused", "1 of 2 containers", time.Time{}},
		{"progressing beats all-terminating", []*engine.ContainerInspect{run("1", status("removing")), run("2", status("created"))},
			core.HealthProgressing, "Created", "1 of 2 containers", time.Time{}},
		{"a recent restart is a warning with the member's deadline", []*engine.ContainerInspect{
			run("1", restarted(1, now.Add(-4*time.Minute))), run("2", restarted(1, now.Add(-2*time.Minute)))},
			core.HealthWarning, "RestartedByPolicy", "2 of 2 containers", now.Add(6 * time.Minute)},
		{"no members", nil, core.HealthOK, "", "", time.Time{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, next := membersHealth(c.members, now)
			assert.Equal(t, c.state, h.State)
			assert.Equal(t, c.reason, h.Reason)
			assert.Equal(t, c.msg, h.Message)
			assert.Equal(t, c.next, next)
		})
	}
}

// A service's issue began when its earliest member's did.
func TestServiceIssueSinceIsTheEarliestMembers(t *testing.T) {
	h, _ := membersHealth([]*engine.ContainerInspect{
		ctr("1", "a", "p", "web", exited(1, t0.Add(-time.Minute))),
		ctr("2", "b", "p", "web", exited(2, t0.Add(-time.Hour))),
	}, t0)
	assert.Equal(t, t0.Add(-time.Hour).UnixMilli(), h.Issues[0].Since)
	assert.Equal(t, "2 of 2 containers", h.Message)
}
