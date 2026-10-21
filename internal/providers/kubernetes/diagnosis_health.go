package kubernetes

import (
	"strings"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// diagnosisHealth handles exact built-in semantics, not arbitrary CRDs that
// happen to have a phase, failed counter or a similarly named condition.
func diagnosisHealth(u *unstructured.Unstructured) (core.Health, bool) {
	group, _, grouped := strings.Cut(u.GetAPIVersion(), "/")
	if !grouped {
		group = ""
	}
	key := group + "/" + u.GetKind()
	switch key {
	case "batch/Job", "/PersistentVolumeClaim", "autoscaling/HorizontalPodAutoscaler", "batch/CronJob":
	default:
		return core.Health{}, false
	}
	if u.GetDeletionTimestamp() != nil {
		return core.HealthFrom([]core.Issue{{State: core.HealthTerminating, Reason: "Terminating"}}), true
	}
	if hasKey(u.Object, "status", "observedGeneration") && u.GetGeneration() > 0 && i64(u.Object, "status", "observedGeneration") < u.GetGeneration() {
		return core.Health{State: core.HealthProgressing, Reason: "ObservedGenerationBehind"}, true
	}
	conditions := map[string]map[string]any{}
	stale := false
	for _, c := range slice(u.Object, "status", "conditions") {
		if hasKey(c, "observedGeneration") && u.GetGeneration() > 0 && i64(c, "observedGeneration") < u.GetGeneration() {
			stale = true
			continue
		}
		conditions[str(c, "type")] = c
	}
	is := func(name, status string) bool { return str(conditions[name], "status") == status }
	issue := func(name string, state core.HealthState) core.Issue {
		c := conditions[name]
		return core.Issue{State: state, Reason: nonEmpty(str(c, "reason"), name), Message: str(c, "message"), Since: unixMs(timeAt(c, "lastTransitionTime"))}
	}
	switch key {
	case "batch/Job":
		for _, name := range []string{"Failed", "FailureTarget"} {
			if is(name, "True") {
				return core.HealthFrom([]core.Issue{issue(name, core.HealthError)}), true
			}
		}
		if is("Complete", "True") {
			return core.Health{State: core.HealthOK, Reason: "Complete"}, true
		}
		if suspended, _, _ := unstructured.NestedBool(u.Object, "spec", "suspend"); suspended || is("Suspended", "True") {
			return core.Health{State: core.HealthOK, Reason: "Suspended"}, true
		}
		// Failed attempts do not mean that the Job has exhausted its policy.
		reason := "Pending"
		if i64(u.Object, "status", "active") > 0 {
			reason = "Active"
		}
		return core.Health{State: core.HealthProgressing, Reason: reason}, true
	case "/PersistentVolumeClaim":
		phase := str(u.Object, "status", "phase")
		switch phase {
		case "Bound":
			return core.Health{State: core.HealthOK, Reason: phase}, true
		case "Pending":
			// Pending includes legitimate WaitForFirstConsumer binding. It is
			// an observation, not a claim about a failed provisioner.
			return core.HealthFrom([]core.Issue{{State: core.HealthWarning, Reason: phase}}), true
		case "Lost":
			return core.HealthFrom([]core.Issue{{State: core.HealthError, Reason: phase}}), true
		default:
			return core.Health{State: core.HealthUnknown, Reason: phase}, true
		}
	case "autoscaling/HorizontalPodAutoscaler":
		var issues []core.Issue
		for _, name := range []string{"AbleToScale", "ScalingActive"} {
			if is(name, "False") {
				state := core.HealthWarning
				if name == "ScalingActive" && str(conditions[name], "reason") == "ScalingDisabled" {
					state = core.HealthProgressing // deliberate scale-to-zero pauses HPA
				}
				issues = append(issues, issue(name, state))
			}
		}
		if len(issues) > 0 {
			return core.HealthFrom(issues), true
		}
		if is("AbleToScale", "True") && is("ScalingActive", "True") {
			// ScalingLimited is a min/max clamp, not by itself a failure.
			return core.Health{State: core.HealthOK}, true
		}
		if stale {
			return core.Health{State: core.HealthProgressing, Reason: "ObservedGenerationBehind"}, true
		}
		return core.Health{State: core.HealthUnknown}, true
	case "batch/CronJob":
		if suspended, _, _ := unstructured.NestedBool(u.Object, "spec", "suspend"); suspended {
			return core.Health{State: core.HealthOK, Reason: "Suspended"}, true
		}
		// Schedule evaluation is not part of this diagnosis slice.
		return core.Health{}, false
	}
	return core.Health{}, false
}

func diagnosisKeep(gr schema.GroupResource) fields {
	keep := fields{}
	for name, value := range genericKeep {
		keep[name] = value
	}
	status := fields{}
	for name, value := range genericKeep["status"].(fields) {
		status[name] = value
	}
	switch gr {
	case schema.GroupResource{Group: "batch", Resource: "jobs"}:
		keep["spec"] = fields{"suspend": true}
		status["active"] = true
	case schema.GroupResource{Group: "batch", Resource: "cronjobs"}:
		keep["spec"] = fields{"suspend": true}
	case schema.GroupResource{Resource: "persistentvolumeclaims"}:
		status["phase"] = true
	case schema.GroupResource{Group: "autoscaling", Resource: "horizontalpodautoscalers"}:
		status["observedGeneration"] = true
	}
	keep["status"] = status
	return keep
}

func diagnosisSource(id, title, kind, group, version, resource string) problemSource {
	def := &kindDef{desc: core.KindDescriptor{ID: id, Title: title, Scoped: true}, kind: kind,
		gvr: schema.GroupVersionResource{Group: group, Version: version, Resource: resource}, namespaced: true}
	def.keep = diagnosisKeep(def.gvr.GroupResource())
	def.project = func(u *unstructured.Unstructured, _ time.Time) ([]core.Cell, core.Health, time.Time) {
		health, supported := diagnosisHealth(u)
		if !supported {
			health = core.Health{State: core.HealthUnknown, Reason: "UnexpectedKind"}
		}
		return nil, health, time.Time{}
	}
	return problemSource{def: problemDef(def, false), title: title}
}
