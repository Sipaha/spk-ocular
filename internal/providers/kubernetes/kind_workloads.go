package kubernetes

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/spk/spk-ocular/internal/core"
)

// Workload columns shared by the controllers.
var (
	colName  = core.Column{ID: "name", Title: "Name", Type: core.ColText}
	colNS    = core.Column{ID: "namespace", Title: "Namespace", Type: core.ColText, ScopeColumn: true}
	colAge   = core.Column{ID: "age", Title: "Age", Type: core.ColAge, Width: 70}
	colReady = core.Column{ID: "ready", Title: "Ready", Type: core.ColRatio, Width: 80}
)

func num(n int64) core.Cell { return core.NumCell(float64(n), fmt.Sprint(n)) }

func ratio(a, b int64) core.Cell {
	f := 1.0
	if b > 0 {
		f = float64(a) / float64(b)
	}
	return core.NumCell(f, fmt.Sprintf("%d/%d", a, b))
}

// desiredReplicas: spec.replicas defaults to 1 when unset.
func desiredReplicas(o map[string]any) int64 {
	if _, ok, _ := unstructured.NestedFieldNoCopy(o, "spec", "replicas"); !ok {
		return 1
	}
	return i64(o, "spec", "replicas")
}

// rolloutPending: the controller has not seen the latest spec yet.
func rolloutPending(u *unstructured.Unstructured) bool {
	og := i64(u.Object, "status", "observedGeneration")
	return og != 0 && og < u.GetGeneration()
}

var workloadStatusKeep = fields{
	"replicas": true, "readyReplicas": true, "updatedReplicas": true, "availableReplicas": true,
	"unavailableReplicas": true, "currentReplicas": true, "observedGeneration": true, "conditions": true,
	"currentRevision": true, "updateRevision": true,
}

var deploymentsKind = &kindDef{
	desc: core.KindDescriptor{
		ID: "apps/deployments", Title: "Deployments", Group: "Workloads", Scoped: true,
		Columns: []core.Column{colName, colNS, colReady,
			{ID: "uptodate", Title: "Up-to-date", Type: core.ColNumber, Width: 90},
			{ID: "available", Title: "Available", Type: core.ColNumber, Width: 90},
			colAge},
	},
	gvr:        schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"},
	namespaced: true,
	keep:       fields{"spec": fields{"replicas": true, "selector": true, "paused": true}, "status": workloadStatusKeep},
	project: func(u *unstructured.Unstructured, _ time.Time) ([]core.Cell, core.Health, time.Time) {
		o := u.Object
		desired := desiredReplicas(o)
		ready, updated, avail := i64(o, "status", "readyReplicas"), i64(o, "status", "updatedReplicas"), i64(o, "status", "availableReplicas")
		cells := []core.Cell{core.TextCell(u.GetName()), core.TextCell(u.GetNamespace()), ratio(ready, desired), num(updated), num(avail), createdCell(u)}
		return cells, deploymentHealth(u, desired, updated, avail), time.Time{}
	},
}

func deploymentHealth(u *unstructured.Unstructured, desired, updated, avail int64) core.Health {
	o := u.Object
	if u.GetDeletionTimestamp() != nil {
		return core.HealthFrom([]core.Issue{{State: core.HealthTerminating, Reason: "Terminating"}})
	}
	cond := podConditions(o)
	if c := cond["Progressing"]; c.reason == "ProgressDeadlineExceeded" {
		return core.HealthFrom([]core.Issue{{State: core.HealthError, Reason: "ProgressDeadlineExceeded", Message: c.message}})
	}
	if desired == 0 {
		return core.Health{State: core.HealthOK, Reason: "ScaledToZero"}
	}
	// Paused on purpose: no rollout to judge, but availability still counts.
	paused, _, _ := unstructured.NestedBool(o, "spec", "paused")
	if !paused && (rolloutPending(u) || updated < desired || i64(o, "status", "replicas") > updated) {
		return core.HealthFrom([]core.Issue{{State: core.HealthProgressing, Reason: "RollingOut",
			Message: fmt.Sprintf("%d of %d replicas updated", updated, desired)}})
	}
	if avail < desired {
		msg := fmt.Sprintf("%d of %d replicas available", avail, desired)
		if c, ok := cond["Available"]; ok && c.status == "False" && c.message != "" {
			msg = c.message
		}
		state := core.HealthWarning
		if avail == 0 {
			state = core.HealthError
		}
		return core.HealthFrom([]core.Issue{{State: state, Reason: "Unavailable", Message: msg}})
	}
	if paused {
		return core.Health{State: core.HealthOK, Reason: "Paused"}
	}
	return core.Health{State: core.HealthOK}
}

// onDelete: the controller replaces pods only when they are deleted, so an
// old revision may run on purpose.
func onDelete(o map[string]any) bool { return str(o, "spec", "updateStrategy", "type") == "OnDelete" }

var statefulSetsKind = &kindDef{
	desc: core.KindDescriptor{
		ID: "apps/statefulsets", Title: "StatefulSets", Group: "Workloads", Scoped: true,
		Columns: []core.Column{colName, colNS, colReady, colAge},
	},
	gvr:        schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"},
	namespaced: true,
	keep: fields{"spec": fields{"replicas": true, "selector": true, "updateStrategy": fields{"type": true, "rollingUpdate": fields{"partition": true}}},
		"status": workloadStatusKeep},
	project: func(u *unstructured.Unstructured, _ time.Time) ([]core.Cell, core.Health, time.Time) {
		o := u.Object
		desired := desiredReplicas(o)
		ready := i64(o, "status", "readyReplicas")
		cells := []core.Cell{core.TextCell(u.GetName()), core.TextCell(u.GetNamespace()), ratio(ready, desired), createdCell(u)}
		// Revisions differ: a rollout, unless the strategy keeps old pods on
		// purpose (OnDelete; below a partition once the rest is updated).
		behind := str(o, "status", "updateRevision") != "" && str(o, "status", "currentRevision") != str(o, "status", "updateRevision")
		intended := behind && (onDelete(o) || i64(o, "status", "updatedReplicas") >= desired-i64(o, "spec", "updateStrategy", "rollingUpdate", "partition"))
		var issues []core.Issue
		switch {
		case u.GetDeletionTimestamp() != nil:
			issues = append(issues, core.Issue{State: core.HealthTerminating, Reason: "Terminating"})
		case desired == 0:
			return cells, core.Health{State: core.HealthOK, Reason: "ScaledToZero"}, time.Time{}
		case ready == 0:
			// Nothing runs: no rollout excuses that (no progress deadline here).
			issues = append(issues, core.Issue{State: core.HealthError, Reason: "NotReady", Message: fmt.Sprintf("0 of %d replicas ready", desired)})
		case rolloutPending(u) || (behind && !intended):
			issues = append(issues, core.Issue{State: core.HealthProgressing, Reason: "RollingOut"})
		case ready < desired:
			issues = append(issues, core.Issue{State: core.HealthWarning, Reason: "NotReady", Message: fmt.Sprintf("%d of %d replicas ready", ready, desired)})
		case intended:
			return cells, core.Health{State: core.HealthOK, Reason: "UpdatePending"}, time.Time{}
		}
		return cells, core.HealthFrom(issues), time.Time{}
	},
}

var daemonSetsKind = &kindDef{
	desc: core.KindDescriptor{
		ID: "apps/daemonsets", Title: "DaemonSets", Group: "Workloads", Scoped: true,
		Columns: []core.Column{colName, colNS,
			{ID: "desired", Title: "Desired", Type: core.ColNumber, Width: 80},
			{ID: "current", Title: "Current", Type: core.ColNumber, Width: 80},
			colReady,
			{ID: "uptodate", Title: "Up-to-date", Type: core.ColNumber, Width: 90},
			{ID: "available", Title: "Available", Type: core.ColNumber, Width: 90},
			colAge},
	},
	gvr:        schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"},
	namespaced: true,
	keep: fields{"spec": fields{"selector": true, "updateStrategy": fields{"type": true}}, "status": fields{
		"desiredNumberScheduled": true, "currentNumberScheduled": true, "numberReady": true,
		"updatedNumberScheduled": true, "numberAvailable": true, "numberUnavailable": true,
		"numberMisscheduled": true, "observedGeneration": true,
	}},
	project: func(u *unstructured.Unstructured, _ time.Time) ([]core.Cell, core.Health, time.Time) {
		o := u.Object
		desired, current := i64(o, "status", "desiredNumberScheduled"), i64(o, "status", "currentNumberScheduled")
		ready, updated, avail := i64(o, "status", "numberReady"), i64(o, "status", "updatedNumberScheduled"), i64(o, "status", "numberAvailable")
		cells := []core.Cell{core.TextCell(u.GetName()), core.TextCell(u.GetNamespace()), num(desired), num(current), ratio(ready, desired), num(updated), num(avail), createdCell(u)}
		var issues []core.Issue
		pending := false
		switch {
		case u.GetDeletionTimestamp() != nil:
			issues = append(issues, core.Issue{State: core.HealthTerminating, Reason: "Terminating"})
		case desired > 0 && avail == 0:
			// Nothing available: no rollout excuses that (no progress deadline here).
			issues = append(issues, core.Issue{State: core.HealthError, Reason: "Unavailable", Message: fmt.Sprintf("0 of %d available", desired)})
		case rolloutPending(u) || (updated < desired && !onDelete(o)):
			issues = append(issues, core.Issue{State: core.HealthProgressing, Reason: "RollingOut", Message: fmt.Sprintf("%d of %d nodes updated", updated, desired)})
		case avail < desired:
			issues = append(issues, core.Issue{State: core.HealthWarning, Reason: "Unavailable", Message: fmt.Sprintf("%d of %d available", avail, desired)})
		case updated < desired:
			pending = true // OnDelete: old pods stay until deleted
		}
		if mis := i64(o, "status", "numberMisscheduled"); mis > 0 {
			issues = append(issues, core.Issue{State: core.HealthWarning, Reason: "Misscheduled", Message: fmt.Sprintf("%d pods on nodes they should not run on", mis)})
		}
		if pending && len(issues) == 0 {
			return cells, core.Health{State: core.HealthOK, Reason: "UpdatePending"}, time.Time{}
		}
		return cells, core.HealthFrom(issues), time.Time{}
	},
}

// replicaSetsKind is not in the navigation (Deployments are what people
// look at) but relations lead to ReplicaSets and they open in details.
var replicaSetsKind = &kindDef{
	desc: core.KindDescriptor{
		ID: "apps/replicasets", Title: "ReplicaSets", Group: "Workloads", Scoped: true, Hidden: true,
		Columns: []core.Column{colName, colNS, colReady, colAge},
	},
	gvr:        schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"},
	namespaced: true,
	keep:       fields{"spec": fields{"replicas": true, "selector": true}, "status": workloadStatusKeep},
	project: func(u *unstructured.Unstructured, _ time.Time) ([]core.Cell, core.Health, time.Time) {
		o := u.Object
		desired := desiredReplicas(o)
		ready := i64(o, "status", "readyReplicas")
		cells := []core.Cell{core.TextCell(u.GetName()), core.TextCell(u.GetNamespace()), ratio(ready, desired), createdCell(u)}
		var issues []core.Issue
		if desired > 0 && ready < desired {
			issues = append(issues, core.Issue{State: core.HealthProgressing, Reason: "NotReady", Message: fmt.Sprintf("%d of %d replicas ready", ready, desired)})
		}
		return cells, core.HealthFrom(issues), time.Time{}
	},
}
