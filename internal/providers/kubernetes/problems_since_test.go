package kubernetes

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Review 2026-09-30 (Codex, P5): Since was set only for recent restarts and
// events; a current failure the API dates (a condition's
// lastTransitionTime, a container's finishedAt) had an empty Since. An
// onset the API does not give stays empty — never the creation time dressed
// up as one (except Pending, which is pending since creation).
func TestProblemsSinceComesFromTheConditionThatSaysIt(t *testing.T) {
	at := func(d time.Duration) int64 { return now.Add(-d).Truncate(time.Second).UnixMilli() }
	cond := func(typ, status, reason string, ago time.Duration) map[string]any {
		return map[string]any{"type": typ, "status": status, "reason": reason, "message": typ + " " + status, "lastTransitionTime": ts(ago)}
	}
	node := func(conds ...any) *unstructured.Unstructured {
		return obj(48*time.Hour, map[string]any{"metadata": map[string]any{"namespace": ""}, "status": map[string]any{"conditions": conds}})
	}
	cases := []struct {
		name   string
		d      *kindDef
		u      *unstructured.Unstructured
		reason string
		since  int64
	}{
		{"node not ready", nodesKind, node(cond("Ready", "False", "KubeletNotReady", time.Hour)), "NotReady", at(time.Hour)},
		{"node unknown", nodesKind, node(cond("Ready", "Unknown", "NodeStatusUnknown", 20*time.Minute)), "Unknown", at(20 * time.Minute)},
		{"node pressure", nodesKind, node(cond("Ready", "True", "", 40*time.Hour), cond("DiskPressure", "True", "", 3*time.Minute)), "DiskPressure", at(3 * time.Minute)},
		{"pod unschedulable", podsKind, podObj(time.Hour, map[string]any{"phase": "Pending", "conditions": []any{cond("PodScheduled", "False", "Unschedulable", 50*time.Minute)}}), "Unschedulable", at(50 * time.Minute)},
		{"pod pending since created", podsKind, podObj(time.Hour, map[string]any{"phase": "Pending"}), "Pending", at(time.Hour)},
		{"pod not ready", podsKind, podObj(time.Hour, map[string]any{"phase": "Running", "conditions": []any{cond("Ready", "False", "ContainersNotReady", 30*time.Minute)},
			"containerStatuses": []any{cs("app", false, 0, running, nil)}}), "NotReady", at(30 * time.Minute)},
		{"container exited with a failure", podsKind, podObj(time.Hour, map[string]any{"phase": "Running",
			"containerStatuses": []any{cs("app", false, 0, map[string]any{"terminated": map[string]any{"exitCode": int64(2), "reason": "Error", "finishedAt": ts(40 * time.Second)}}, nil)}}), "Error", at(40 * time.Second)},
		{"crash loop: onset unknown", podsKind, podObj(time.Hour, map[string]any{"phase": "Running",
			"containerStatuses": []any{cs("app", false, 3, waiting("CrashLoopBackOff"), nil)}}), "CrashLoopBackOff", 0},
		{"deployment deadline", deploymentsKind, deploy(3, 1, 2, 1, 2, cond("Progressing", "False", "ProgressDeadlineExceeded", 15*time.Minute)), "ProgressDeadlineExceeded", at(15 * time.Minute)},
		{"deployment unavailable", deploymentsKind, deploy(3, 0, 3, 0, 2, cond("Available", "False", "MinimumReplicasUnavailable", 7*time.Minute)), "Unavailable", at(7 * time.Minute)},
		{"deployment unavailable by counts only", deploymentsKind, deploy(3, 2, 3, 2, 2), "Unavailable", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, h, _ := project(t, c.d, c.u)
			assert.Equal(t, c.reason, h.Reason, "%+v", h)
			if assert.NotEmpty(t, h.Issues) {
				assert.Equal(t, c.since, h.Issues[0].Since, "the summary issue's onset")
			}
			pcells, _, _ := problemDef(c.d, false).project(trim(c.u, c.d.keep), now) // Problems' own columns
			since := pcells[len(pcells)-1]
			assert.Equal(t, c.since, since.Time, "Problems' Since cell")
		})
	}
}
