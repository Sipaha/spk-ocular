package kubernetes

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/spk/spk-ocular/internal/core"
)

// CronJob actions (P12). A CronJob is a discovered kind (P8): its actions
// come with the exact resource batch/v1 cronjobs, next to delete.
var (
	// No new runs on schedule (suspend), runs on schedule again (resume).
	actSuspend = core.ActionDescriptor{ID: "suspend", Title: "Suspend"}
	actResume  = core.ActionDescriptor{ID: "resume", Title: "Resume"}
)

var cronJobsV1GVR = schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"}

// discoveredActions: what a discovered resource offers — delete by its
// verbs, and the CronJob actions for batch/v1 cronjobs only.
func discoveredActions(r apiResource) []core.ActionDescriptor {
	var out []core.ActionDescriptor
	gvr := schema.GroupVersionResource{Group: r.Group, Version: r.Version, Resource: r.Resource}
	if gvr == cronJobsV1GVR && r.has("get") && r.has("patch") {
		out = append(out, actSuspend, actResume)
	}
	if r.has("delete") {
		out = append(out, actDelete)
	}
	return out
}

// isCronJobs: def is batch/v1 cronjobs (the CronJob actions' objects).
func isCronJobs(def *kindDef) bool { return def.discovered && def.gvr == cronJobsV1GVR }

// cronJobEffectState: what the suspend and resume texts read.
func cronJobEffectState(action string, o map[string]any, st map[string]any) {
	st["suspend"] = boolAt(o, "spec", "suspend")
	if action == actResume.ID {
		st["startingDeadlineSeconds"] = fieldAt(o, "spec", "startingDeadlineSeconds")
	}
}

// cronJobUnavailable: suspend of a suspended CronJob, resume of one that
// is not.
func cronJobUnavailable(action string, u *unstructured.Unstructured) *core.Message {
	on := boolAt(u.Object, "spec", "suspend")
	switch {
	case action == actSuspend.ID && on:
		m := msg("unavailable.suspended", "name", u.GetName())
		return &m
	case action == actResume.ID && !on:
		m := msg("unavailable.notSuspended", "name", u.GetName())
		return &m
	}
	return nil
}

// cronJobEffects: suspend stops new runs on schedule (running Jobs go on;
// the active runs are what was seen at the review, not a promise);
// resume starts them again, and a run time missed meanwhile may start a
// run at once — the controller decides, by startingDeadlineSeconds.
func cronJobEffects(action string, u *unstructured.Unstructured) []core.Message {
	o := u.Object
	switch action {
	case actSuspend.ID:
		out := []core.Message{msg("cronjob.suspend", "name", u.GetName())}
		if n := len(slice(o, "status", "active")); n > 0 {
			out = append(out, countMsg("cronjob.activeSeenOne", "cronjob.activeSeen", n))
		}
		return out
	case actResume.ID:
		out := []core.Message{msg("cronjob.resume", "name", u.GetName())}
		if d, found, _ := unstructured.NestedFieldNoCopy(o, "spec", "startingDeadlineSeconds"); found && d != nil {
			out = append(out, msg("cronjob.missedDeadline", "seconds", i64(o, "spec", "startingDeadlineSeconds")))
		} else {
			out = append(out, msg("cronjob.missed"))
		}
		return out
	}
	return nil
}
