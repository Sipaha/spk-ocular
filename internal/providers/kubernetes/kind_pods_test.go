package kubernetes

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/spk/spk-ocular/internal/core"
)

var now = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

func ts(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }

func podObj(created time.Duration, status map[string]any, meta ...func(map[string]any)) *unstructured.Unstructured {
	m := map[string]any{"name": "p", "namespace": "ns", "uid": "u", "creationTimestamp": ts(created)}
	for _, f := range meta {
		f(m)
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"metadata": m,
		"spec":     map[string]any{"containers": []any{map[string]any{"name": "app"}, map[string]any{"name": "side"}}},
		"status":   status,
	}}
}

func cs(name string, ready bool, restarts int64, state map[string]any, last map[string]any) map[string]any {
	c := map[string]any{"name": name, "ready": ready, "restartCount": restarts, "state": state}
	if last != nil {
		c["lastState"] = last
	}
	return c
}

var running = map[string]any{"running": map[string]any{}}

func waiting(reason string) map[string]any {
	return map[string]any{"waiting": map[string]any{"reason": reason, "message": reason + " details"}}
}

func readyCond(status string, since time.Duration) []any {
	return []any{map[string]any{"type": "Ready", "status": status, "lastTransitionTime": ts(since)}}
}

func TestPodProjection(t *testing.T) {
	cases := []struct {
		name        string
		pod         *unstructured.Unstructured
		ready       string
		status      string
		restarts    string
		state       core.HealthState
		reason      string
		wantNextSet bool
	}{
		{"healthy", podObj(time.Hour, map[string]any{"phase": "Running", "conditions": readyCond("True", time.Hour),
			"containerStatuses": []any{cs("app", true, 0, running, nil), cs("side", true, 1, running, nil)}}),
			"2/2", "Running", "1", core.HealthOK, "", false},
		{"crashloop", podObj(time.Hour, map[string]any{"phase": "Running", "conditions": readyCond("False", time.Hour),
			"containerStatuses": []any{cs("app", false, 12, waiting("CrashLoopBackOff"), nil), cs("side", true, 0, running, nil)}}),
			"1/2", "CrashLoopBackOff", "12", core.HealthError, "CrashLoopBackOff", false},
		{"exited with failure before back-off", podObj(time.Minute, map[string]any{"phase": "Running", "conditions": readyCond("False", 10*time.Second),
			"containerStatuses": []any{cs("app", false, 1, map[string]any{"terminated": map[string]any{"reason": "Error", "exitCode": int64(1)}}, nil)}}),
			"0/2", "Error", "1", core.HealthError, "Error", true},
		{"image pull", podObj(time.Minute, map[string]any{"phase": "Pending",
			"containerStatuses": []any{cs("app", false, 0, waiting("ImagePullBackOff"), nil)}}),
			"0/2", "ImagePullBackOff", "0", core.HealthError, "ImagePullBackOff", true},
		{"recent OOM", podObj(time.Hour, map[string]any{"phase": "Running", "conditions": readyCond("True", time.Minute),
			"containerStatuses": []any{cs("app", true, 3, running, map[string]any{"terminated": map[string]any{"reason": "OOMKilled", "finishedAt": ts(time.Minute)}})}}),
			"1/2", "Running", "3", core.HealthWarning, "OOMKilled", true},
		{"old OOM is history", podObj(time.Hour, map[string]any{"phase": "Running", "conditions": readyCond("True", time.Hour),
			"containerStatuses": []any{cs("app", true, 3, running, map[string]any{"terminated": map[string]any{"reason": "OOMKilled", "finishedAt": ts(time.Hour)}})}}),
			"1/2", "Running", "3", core.HealthOK, "", false},
		{"unschedulable", podObj(time.Minute, map[string]any{"phase": "Pending", "conditions": []any{map[string]any{
			"type": "PodScheduled", "status": "False", "reason": "Unschedulable", "message": "0/1 nodes are available"}}}),
			"0/2", "Pending", "0", core.HealthWarning, "Unschedulable", false},
		{"pending fresh", podObj(time.Minute, map[string]any{"phase": "Pending"}),
			"0/2", "Pending", "0", core.HealthProgressing, "Pending", true},
		{"pending long", podObj(10*time.Minute, map[string]any{"phase": "Pending"}),
			"0/2", "Pending", "0", core.HealthWarning, "Pending", false},
		{"starting", podObj(time.Minute, map[string]any{"phase": "Running", "conditions": readyCond("False", 30*time.Second),
			"containerStatuses": []any{cs("app", false, 0, running, nil)}}),
			"0/2", "Running", "0", core.HealthProgressing, "Starting", true},
		{"not ready long", podObj(time.Hour, map[string]any{"phase": "Running", "conditions": readyCond("False", 10*time.Minute),
			"containerStatuses": []any{cs("app", false, 0, running, nil)}}),
			"0/2", "Running", "0", core.HealthWarning, "NotReady", false},
		{"completed", podObj(time.Hour, map[string]any{"phase": "Succeeded",
			"containerStatuses": []any{cs("app", false, 0, map[string]any{"terminated": map[string]any{"reason": "Completed"}}, nil)}}),
			"0/2", "Completed", "0", core.HealthOK, "Completed", false},
		{"failed", podObj(time.Hour, map[string]any{"phase": "Failed", "reason": "Evicted", "message": "low on memory"}),
			"0/2", "Evicted", "0", core.HealthError, "Evicted", false},
		{"terminating", podObj(time.Hour, map[string]any{"phase": "Running"}, func(m map[string]any) { m["deletionTimestamp"] = ts(time.Second) }),
			"0/2", "Terminating", "0", core.HealthTerminating, "Terminating", false},
		{"init waiting", podObj(time.Minute, map[string]any{"phase": "Pending",
			"initContainerStatuses": []any{cs("init", false, 0, waiting("PodInitializing"), nil)}}),
			"0/2", "Init:0/0", "0", core.HealthProgressing, "Init:0/0", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cells, h, next := projectPod(c.pod, now)
			assert.Equal(t, c.ready, cells[2].Text, "ready")
			assert.Equal(t, c.status, cells[3].Text, "status")
			assert.Equal(t, c.restarts, cells[4].Text, "restarts")
			assert.Equal(t, c.state, h.State, "health %+v", h)
			assert.Equal(t, c.reason, h.Reason)
			assert.Equal(t, c.wantNextSet, !next.IsZero(), "re-evaluation time %v", next)
			assert.Len(t, cells, len(podsKind.desc.Columns))
		})
	}
}

// A previous instance ended recently, by the API's own time of that
// termination (not a restart time; no local history). restartCount is the
// API's lifetime total. A termination time too far ahead (clock skew) is
// judged once local time catches up — never left out for good.
func TestRecentTermination(t *testing.T) {
	exited := func(ago time.Duration, reason string, code int64) map[string]any {
		return map[string]any{"terminated": map[string]any{"reason": reason, "exitCode": code, "finishedAt": ts(ago)}}
	}
	runningPod := func(status map[string]any) *unstructured.Unstructured {
		status["phase"] = "Running"
		status["conditions"] = readyCond("True", time.Minute)
		return podObj(time.Hour, status)
	}
	t.Run("app container", func(t *testing.T) {
		_, h, next := projectPod(runningPod(map[string]any{"containerStatuses": []any{cs("app", true, 4, running, exited(2*time.Minute, "Error", 1))}}), now)
		assert.Equal(t, core.HealthWarning, h.State)
		assert.Equal(t, "RecentRestart", h.Reason)
		assert.Equal(t, "container app: the previous instance ended with exit code 1 (Error); 4 restarts reported in total", h.Message)
		assert.Equal(t, now.Add(-2*time.Minute).UnixMilli(), h.Issues[0].Since)
		assert.Equal(t, now.Add(-2*time.Minute).Add(podRecentRestart), next)
	})
	t.Run("exit 0 is a restart too", func(t *testing.T) {
		_, h, _ := projectPod(runningPod(map[string]any{"containerStatuses": []any{cs("app", true, 1, running, exited(time.Minute, "Completed", 0))}}), now)
		assert.Equal(t, "RecentRestart", h.Reason)
		assert.Contains(t, h.Message, "1 restart reported in total")
	})
	t.Run("waiting in back-off: the error leads, the termination is listed", func(t *testing.T) {
		_, h, _ := projectPod(runningPod(map[string]any{"containerStatuses": []any{cs("app", false, 5, waiting("CrashLoopBackOff"), exited(time.Minute, "Error", 1))}}), now)
		assert.Equal(t, core.HealthError, h.State)
		assert.Equal(t, "CrashLoopBackOff", h.Reason)
		assert.Len(t, h.Issues, 2)
	})
	t.Run("sidecar in init statuses, several containers", func(t *testing.T) {
		_, h, next := projectPod(runningPod(map[string]any{
			"initContainerStatuses": []any{cs("proxy", true, 1, running, exited(time.Minute, "Error", 137))},
			"containerStatuses":     []any{cs("app", true, 2, running, exited(3*time.Minute, "Error", 1))}}), now)
		assert.Len(t, h.Issues, 2)
		assert.Equal(t, now.Add(-3*time.Minute).Add(podRecentRestart), next, "the earliest expiry")
	})
	t.Run("OOM keeps its reason, one issue", func(t *testing.T) {
		_, h, _ := projectPod(runningPod(map[string]any{"containerStatuses": []any{cs("app", true, 1, running, exited(time.Minute, "OOMKilled", 137))}}), now)
		assert.Equal(t, "OOMKilled", h.Reason)
		assert.Len(t, h.Issues, 1)
	})
	t.Run("no previous instance", func(t *testing.T) {
		_, h, next := projectPod(runningPod(map[string]any{"containerStatuses": []any{cs("app", true, 0, running, nil)}}), now)
		assert.Equal(t, core.HealthOK, h.State)
		assert.True(t, next.IsZero())
	})
	t.Run("long ago", func(t *testing.T) {
		_, h, next := projectPod(runningPod(map[string]any{"containerStatuses": []any{cs("app", true, 4, running, exited(time.Hour, "Error", 1))}}), now)
		assert.Equal(t, core.HealthOK, h.State)
		assert.True(t, next.IsZero())
	})
	t.Run("a finished pod stays finished", func(t *testing.T) {
		pod := podObj(time.Hour, map[string]any{"phase": "Succeeded", "containerStatuses": []any{cs("app", false, 1,
			map[string]any{"terminated": map[string]any{"reason": "Completed"}}, exited(time.Minute, "Error", 1))}})
		_, h, _ := projectPod(pod, now)
		assert.Equal(t, core.HealthOK, h.State)
	})
	t.Run("slightly ahead: now", func(t *testing.T) {
		_, h, next := projectPod(runningPod(map[string]any{"containerStatuses": []any{cs("app", true, 1, running, exited(-30*time.Second, "Error", 1))}}), now)
		assert.Equal(t, "RecentRestart", h.Reason)
		assert.Equal(t, now.UnixMilli(), h.Issues[0].Since, "never in the future")
		assert.False(t, next.IsZero())
	})
	t.Run("far ahead: judged when local time catches up", func(t *testing.T) {
		pod := runningPod(map[string]any{"containerStatuses": []any{cs("app", true, 1, running, exited(-5*time.Minute, "Error", 1))}})
		_, h, next := projectPod(pod, now)
		assert.Equal(t, core.HealthOK, h.State)
		assert.Equal(t, now.Add(5*time.Minute-clockSkewTolerance), next)
		_, h, _ = projectPod(pod, next)
		assert.Equal(t, "RecentRestart", h.Reason)
	})
}
