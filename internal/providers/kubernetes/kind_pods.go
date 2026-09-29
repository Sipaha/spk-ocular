package kubernetes

import (
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/spk/spk-ocular/internal/core"
)

// Health thresholds for pods (fake-clock tested).
const (
	podPendingWarnAfter  = 5 * time.Minute  // Pending this long is a problem
	podNotReadyWarnAfter = 2 * time.Minute  // Running but not ready this long
	podRecentOOM         = 10 * time.Minute // an OOM kill stays visible this long
)

// Waiting reasons that mean the container cannot run as configured.
var podFatalWaiting = map[string]bool{
	"CrashLoopBackOff": true, "ImagePullBackOff": true, "ErrImagePull": true,
	"InvalidImageName": true, "CreateContainerConfigError": true,
	"CreateContainerError": true, "RunContainerError": true, "ErrImageNeverPull": true,
}

// containerStateKeep: what status text and health read from a container state.
var containerStateKeep = fields{
	"waiting":    fields{"reason": true, "message": true},
	"running":    fields{"startedAt": true},
	"terminated": fields{"reason": true, "exitCode": true, "signal": true, "startedAt": true, "finishedAt": true, "containerID": true},
}

// containerID tells container instances apart (log sources follow restarts).
var containerStatusKeep = fields{"name": true, "ready": true, "restartCount": true, "containerID": true, "state": containerStateKeep, "lastState": containerStateKeep}

var conditionKeep = fields{"type": true, "status": true, "reason": true, "message": true, "lastTransitionTime": true}

var podsKind = &kindDef{
	desc: core.KindDescriptor{
		ID: "pods", Title: "Pods", Group: "Workloads", Scoped: true,
		Columns: []core.Column{
			{ID: "name", Title: "Name", Type: core.ColText},
			{ID: "namespace", Title: "Namespace", Type: core.ColText, ScopeColumn: true},
			{ID: "ready", Title: "Ready", Type: core.ColRatio, Width: 70},
			{ID: "status", Title: "Status", Type: core.ColStatus, Width: 150},
			{ID: "restarts", Title: "Restarts", Type: core.ColNumber, Width: 80},
			{ID: "node", Title: "Node", Type: core.ColText},
			{ID: "age", Title: "Age", Type: core.ColAge, Width: 70},
			{ID: "cpu", Title: "CPU", Type: core.ColCPU, Width: 70, Metric: true},
			{ID: "memory", Title: "Memory", Type: core.ColBytes, Width: 80, Metric: true},
		},
	},
	gvr:        schema.GroupVersionResource{Version: "v1", Resource: "pods"},
	namespaced: true,
	keep: fields{
		// Images, ports, env etc. are read from the full object in details.
		"spec": fields{
			"nodeName":            true,
			"restartPolicy":       true, // logs: will an exited container restart?
			"containers":          fields{"name": true},
			"initContainers":      fields{"name": true, "restartPolicy": true}, // Always = sidecar
			"ephemeralContainers": fields{"name": true},
		},
		"status": fields{
			"phase": true, "reason": true, "message": true, "podIP": true, "startTime": true,
			"conditions":                 conditionKeep,
			"containerStatuses":          containerStatusKeep,
			"initContainerStatuses":      containerStatusKeep,
			"ephemeralContainerStatuses": containerStatusKeep,
		},
	},
	project: projectPod,
}

func projectPod(u *unstructured.Unstructured, now time.Time) ([]core.Cell, core.Health, time.Time) {
	o := u.Object
	containers := slice(o, "spec", "containers")
	statuses := slice(o, "status", "containerStatuses")
	ready, restarts := 0, int64(0)
	for _, cs := range statuses {
		if b, _ := cs["ready"].(bool); b {
			ready++
		}
		restarts += i64(cs, "restartCount")
	}
	total := len(containers)
	frac := 0.0
	if total > 0 {
		frac = float64(ready) / float64(total)
	}
	status := podStatusText(u)
	h, next := podHealth(u, status, now)
	cells := []core.Cell{
		core.TextCell(u.GetName()),
		core.TextCell(u.GetNamespace()),
		core.NumCell(frac, fmt.Sprintf("%d/%d", ready, total)),
		core.TextCell(status),
		core.NumCell(float64(restarts), fmt.Sprint(restarts)),
		core.TextCell(str(o, "spec", "nodeName")),
		createdCell(u),
		{}, {}, // metrics come from GetMetrics
	}
	return cells, h, next
}

// podStatusText follows kubectl's STATUS column.
func podStatusText(u *unstructured.Unstructured) string {
	o := u.Object
	reason := str(o, "status", "phase")
	if r := str(o, "status", "reason"); r != "" {
		reason = r
	}
	inits := slice(o, "status", "initContainerStatuses")
	initDone := true
	for i, cs := range inits {
		switch {
		case str(cs, "state", "terminated", "reason") != "" && i64(cs, "state", "terminated", "exitCode") == 0:
			continue
		case str(cs, "state", "terminated", "reason") != "":
			reason = "Init:" + str(cs, "state", "terminated", "reason")
		case str(cs, "state", "waiting", "reason") != "" && str(cs, "state", "waiting", "reason") != "PodInitializing":
			reason = "Init:" + str(cs, "state", "waiting", "reason")
		default:
			reason = fmt.Sprintf("Init:%d/%d", i, len(slice(o, "spec", "initContainers")))
		}
		initDone = false
		break
	}
	if initDone {
		running := false
		statuses := slice(o, "status", "containerStatuses")
		for i := len(statuses) - 1; i >= 0; i-- {
			cs := statuses[i]
			switch {
			case str(cs, "state", "waiting", "reason") != "":
				reason = str(cs, "state", "waiting", "reason")
			case str(cs, "state", "terminated", "reason") != "":
				reason = str(cs, "state", "terminated", "reason")
			case str(cs, "state", "terminated", "startedAt") != "" || hasKey(cs, "state", "terminated"):
				if sig := i64(cs, "state", "terminated", "signal"); sig != 0 {
					reason = fmt.Sprintf("Signal:%d", sig)
				} else {
					reason = fmt.Sprintf("ExitCode:%d", i64(cs, "state", "terminated", "exitCode"))
				}
			case hasKey(cs, "state", "running"):
				running = true
			}
		}
		if reason == "Completed" && running {
			reason = "Running"
		}
	}
	if u.GetDeletionTimestamp() != nil {
		if str(o, "status", "reason") == "NodeLost" {
			return "Unknown"
		}
		return "Terminating"
	}
	return reason
}

func hasKey(m map[string]any, path ...string) bool {
	_, ok, _ := unstructured.NestedFieldNoCopy(m, path...)
	return ok
}

// podHealth applies the spec's pod rules; next is the earliest moment a
// time-based rule changes the verdict without an API update.
func podHealth(u *unstructured.Unstructured, status string, now time.Time) (core.Health, time.Time) {
	o := u.Object
	if u.GetDeletionTimestamp() != nil {
		return core.HealthFrom([]core.Issue{{State: core.HealthTerminating, Reason: "Terminating"}}), time.Time{}
	}
	phase := str(o, "status", "phase")
	switch phase {
	case "Succeeded":
		return core.Health{State: core.HealthOK, Reason: "Completed"}, time.Time{}
	case "Failed":
		msg := str(o, "status", "message")
		return core.HealthFrom([]core.Issue{{State: core.HealthError, Reason: nonEmpty(status, "Failed"), Message: msg}}), time.Time{}
	}

	var issues []core.Issue
	var next time.Time
	for _, cs := range append(slice(o, "status", "initContainerStatuses"), slice(o, "status", "containerStatuses")...) {
		if r := str(cs, "state", "waiting", "reason"); podFatalWaiting[r] {
			issues = append(issues, core.Issue{State: core.HealthError, Reason: r,
				Message: containerMsg(cs, str(cs, "state", "waiting", "message"))})
		}
		// A container that exited with a failure and has not been restarted
		// yet (the moment before CrashLoopBackOff): a failure, not "starting".
		if hasKey(cs, "state", "terminated") && i64(cs, "state", "terminated", "exitCode") != 0 {
			issues = append(issues, core.Issue{State: core.HealthError, Reason: nonEmpty(str(cs, "state", "terminated", "reason"), "Error"),
				Message: containerMsg(cs, fmt.Sprintf("exited with code %d", i64(cs, "state", "terminated", "exitCode")))})
		}
		if str(cs, "lastState", "terminated", "reason") == "OOMKilled" {
			at := timeAt(cs, "lastState", "terminated", "finishedAt")
			if !at.IsZero() && now.Sub(at) < podRecentOOM {
				issues = append(issues, core.Issue{State: core.HealthWarning, Reason: "OOMKilled",
					Message: containerMsg(cs, "killed for exceeding its memory limit "+ago(now, at))})
				next = earliest(next, at.Add(podRecentOOM))
			}
		}
	}

	created := u.GetCreationTimestamp().Time
	cond := podConditions(o)
	switch phase {
	case "Pending", "":
		if c, ok := cond["PodScheduled"]; ok && c.status == "False" {
			issues = append(issues, core.Issue{State: core.HealthWarning, Reason: nonEmpty(c.reason, "Unschedulable"), Message: c.message})
		} else if now.Sub(created) >= podPendingWarnAfter {
			issues = append(issues, core.Issue{State: core.HealthWarning, Reason: "Pending", Message: "pending for " + dur(now.Sub(created))})
		} else {
			issues = append(issues, core.Issue{State: core.HealthProgressing, Reason: nonEmpty(status, "Pending")})
			next = earliest(next, created.Add(podPendingWarnAfter))
		}
	case "Running":
		if c, ok := cond["Ready"]; ok && c.status != "True" {
			since := c.at
			if since.IsZero() {
				since = created
			}
			if now.Sub(since) >= podNotReadyWarnAfter {
				issues = append(issues, core.Issue{State: core.HealthWarning, Reason: "NotReady", Message: nonEmpty(c.message, "not ready for "+dur(now.Sub(since)))})
			} else {
				issues = append(issues, core.Issue{State: core.HealthProgressing, Reason: "Starting"})
				next = earliest(next, since.Add(podNotReadyWarnAfter))
			}
		}
	case "Unknown":
		issues = append(issues, core.Issue{State: core.HealthUnknown, Reason: "Unknown", Message: "the node stopped reporting"})
	}
	return core.HealthFrom(issues), next
}

type condition struct {
	status, reason, message string
	at                      time.Time
}

func podConditions(o map[string]any) map[string]condition {
	out := map[string]condition{}
	for _, c := range slice(o, "status", "conditions") {
		out[str(c, "type")] = condition{status: str(c, "status"), reason: str(c, "reason"), message: str(c, "message"), at: timeAt(c, "lastTransitionTime")}
	}
	return out
}

func containerMsg(cs map[string]any, msg string) string {
	name := str(cs, "name")
	if msg == "" {
		return "container " + name
	}
	return "container " + name + ": " + msg
}

func nonEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func ago(now, t time.Time) string { return dur(now.Sub(t)) + " ago" }

// dur is a compact duration: 45s, 12m, 3h, 5d.
func dur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
