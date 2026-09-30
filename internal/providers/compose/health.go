package compose

import (
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/providers/compose/engine"
)

const (
	// recentRestart: a restart by the restart manager stays visible this
	// long (as a Kubernetes container restart does).
	recentRestart = 10 * time.Minute
	// clockSkewTolerance: a start time further ahead than this is clock
	// skew — nothing is claimed about it; closer ones count as now.
	clockSkewTolerance = time.Minute
	// maxEvidence bounds a health check output quoted as evidence.
	maxEvidence = 1024
)

// containerHealth applies decision 6's table to one container; next is
// the earliest moment the verdict changes without a new inspect (the end
// of a RestartedByPolicy window), zero when none.
func containerHealth(c *engine.ContainerInspect, now time.Time) (core.Health, time.Time) {
	st := c.State
	since := func(t engine.Time) int64 { return unixMs(t.Time) }
	switch st.Status {
	case "exited":
		if st.ExitCode == 0 {
			return core.Health{State: core.HealthOK, Reason: "Completed"}, time.Time{}
		}
		m := msg("container.exited", "code", strconv.Itoa(st.ExitCode))
		if st.OOMKilled {
			m = msg("container.exitedOOM", "code", strconv.Itoa(st.ExitCode))
		}
		return core.HealthFrom([]core.Issue{{State: core.HealthError, Reason: "Exited", Message: m.Text, Since: since(st.FinishedAt)}}), time.Time{}
	case "dead":
		return core.HealthFrom([]core.Issue{{State: core.HealthError, Reason: "Dead", Message: strings.TrimSpace(st.Error), Since: since(st.FinishedAt)}}), time.Time{}
	case "removing":
		return core.HealthFrom([]core.Issue{{State: core.HealthTerminating, Reason: "Removing"}}), time.Time{}
	}

	var issues []core.Issue
	switch st.Status {
	case "running":
		if st.Health != nil {
			switch st.Health.Status {
			case "starting":
				issues = append(issues, core.Issue{State: core.HealthProgressing, Reason: "Starting"})
			case "unhealthy":
				// The onset is unknown (the log keeps only the last checks).
				issues = append(issues, core.Issue{State: core.HealthError, Reason: "Unhealthy", Message: lastFailure(st.Health)})
			}
		}
	case "restarting":
		issues = append(issues, core.Issue{State: core.HealthWarning, Reason: "Restarting",
			Message: msg("container.restarting", "code", strconv.Itoa(st.ExitCode)).Text, Since: since(st.FinishedAt)})
	case "paused":
		issues = append(issues, core.Issue{State: core.HealthWarning, Reason: "Paused"})
	case "created":
		issues = append(issues, core.Issue{State: core.HealthProgressing, Reason: "Created"})
	default:
		issues = append(issues, core.Issue{State: core.HealthUnknown, Reason: "Unknown", Message: msg("container.unknownState", "state", st.Status).Text})
	}
	is, next, ok := restartedByPolicy(c, now)
	if ok {
		issues = append(issues, is)
	}
	return core.HealthFrom(issues), next
}

// restartedByPolicy: evidence of the restart manager — RestartCount > 0
// (an explicit start/restart by the user resets it) and the current run
// started within recentRestart. next is when the verdict changes: the end
// of the window, or, for a start further ahead than clock skew allows,
// when local time catches up with it.
func restartedByPolicy(c *engine.ContainerInspect, now time.Time) (core.Issue, time.Time, bool) {
	at := c.State.StartedAt.Time
	switch {
	case c.RestartCount <= 0 || at.IsZero():
		return core.Issue{}, time.Time{}, false
	case at.After(now.Add(clockSkewTolerance)):
		return core.Issue{}, at.Add(-clockSkewTolerance), false
	case now.Sub(at) >= recentRestart:
		return core.Issue{}, time.Time{}, false
	}
	since := at
	if since.After(now) {
		since = now
	}
	key := "container.restartedByPolicy"
	if c.RestartCount == 1 {
		key = "container.restartedByPolicyOnce"
	}
	return core.Issue{State: core.HealthWarning, Reason: "RestartedByPolicy",
		Message: msg(key, "count", strconv.Itoa(c.RestartCount)).Text, Since: since.UnixMilli()}, at.Add(recentRestart), true
}

// lastFailure is the output of the last failing check in the log (as of
// reading), trimmed and bounded.
func lastFailure(h *engine.Health) string {
	for i := len(h.Log) - 1; i >= 0; i-- {
		if h.Log[i].ExitCode != 0 {
			return bounded(strings.TrimSpace(h.Log[i].Output), maxEvidence)
		}
	}
	return ""
}

// bounded cuts s to at most n bytes on a rune boundary, marking the cut.
func bounded(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// membersHealth is a service's (or a project's) health from its member
// containers, by decision 6's precedence: an error, a warning (unknown
// ranks with them by severity), progressing; all terminating →
// terminating; all exited 0 → ok "Completed"; otherwise ok. The worst
// reason says how many members have it ("N of M containers", with the
// member's own message when one member has it); next is the earliest
// member deadline.
func membersHealth(members []*engine.ContainerInspect, now time.Time) (core.Health, time.Time) {
	type group struct {
		state   core.HealthState
		reason  string
		count   int
		since   int64
		example string // the first member's name and message
	}
	groups := map[string]*group{}
	var next time.Time
	terminating, completed := 0, 0
	for _, c := range members {
		h, n := containerHealth(c, now)
		next = earliest(next, n)
		switch {
		case h.State == core.HealthTerminating:
			terminating++
		case h.State == core.HealthOK && h.Reason == "Completed":
			completed++
		}
		seen := map[string]bool{}
		for _, is := range h.Issues {
			if is.State == core.HealthTerminating || seen[is.Reason] {
				continue
			}
			seen[is.Reason] = true
			k := string(is.State) + "\x00" + is.Reason
			g := groups[k]
			if g == nil {
				g = &group{state: is.State, reason: is.Reason}
				if is.Message != "" {
					g.example = containerName(c) + ": " + is.Message
				}
				groups[k] = g
			}
			g.count++
			if is.Since != 0 && (g.since == 0 || is.Since < g.since) {
				g.since = is.Since
			}
		}
	}
	total := strconv.Itoa(len(members))
	if len(groups) > 0 {
		list := make([]*group, 0, len(groups))
		for _, g := range groups {
			list = append(list, g)
		}
		sort.Slice(list, func(i, j int) bool {
			a, b := list[i], list[j]
			if a.state.Severity() != b.state.Severity() {
				return a.state.Severity() > b.state.Severity()
			}
			if a.count != b.count {
				return a.count > b.count
			}
			return a.reason < b.reason
		})
		issues := make([]core.Issue, 0, len(list))
		for _, g := range list {
			m := msg("service.members", "count", strconv.Itoa(g.count), "total", total)
			if g.count == 1 && g.example != "" {
				m = msg("service.membersExample", "count", "1", "total", total, "example", g.example)
			}
			issues = append(issues, core.Issue{State: g.state, Reason: g.reason, Message: m.Text, Since: g.since})
		}
		return core.HealthFrom(issues), next
	}
	switch {
	case len(members) > 0 && terminating == len(members):
		return core.HealthFrom([]core.Issue{{State: core.HealthTerminating, Reason: "Removing",
			Message: msg("service.members", "count", total, "total", total).Text}}), next
	case len(members) > 0 && completed == len(members):
		return core.Health{State: core.HealthOK, Reason: "Completed"}, next
	}
	return core.Health{State: core.HealthOK}, next
}

// containerName is the container's name without the leading "/" (its
// Ref.Title); the id when it has none.
func containerName(c *engine.ContainerInspect) string {
	if n := strings.TrimPrefix(c.Name, "/"); n != "" {
		return n
	}
	return shortID(c.ID)
}

// unixMs is t for Issue.Since; an unknown time stays unknown (0).
func unixMs(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// earliest returns the earlier non-zero time.
func earliest(a, b time.Time) time.Time {
	switch {
	case a.IsZero():
		return b
	case b.IsZero():
		return a
	case b.Before(a):
		return b
	}
	return a
}

// shortID is an Engine id as docker shows it (12 hex digits, no
// "sha256:").
func shortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
