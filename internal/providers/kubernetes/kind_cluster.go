package kubernetes

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/spk/spk-ocular/internal/core"
)

func keyNames(u *unstructured.Unstructured) []string {
	v, _ := u.Object[keysField].([]any)
	out := make([]string, 0, len(v))
	for _, k := range v {
		if s, ok := k.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func keysCell(u *unstructured.Unstructured) core.Cell {
	keys := keyNames(u)
	text := fmt.Sprint(len(keys))
	if len(keys) > 0 && len(keys) <= 3 {
		text = strings.Join(keys, ", ")
	}
	return core.NumCell(float64(len(keys)), text)
}

var configMapsKind = &kindDef{
	desc: core.KindDescriptor{
		ID: "configmaps", Title: "ConfigMaps", Group: "Config", Scoped: true,
		Columns: []core.Column{colName, colNS, {ID: "keys", Title: "Keys", Type: core.ColText}, colAge},
	},
	gvr:        schema.GroupVersionResource{Version: "v1", Resource: "configmaps"},
	namespaced: true,
	pre:        keysOnly("data", "binaryData"),
	keep:       fields{},
	project: func(u *unstructured.Unstructured, _ time.Time) ([]core.Cell, core.Health, time.Time) {
		return []core.Cell{core.TextCell(u.GetName()), core.TextCell(u.GetNamespace()), keysCell(u), createdCell(u)}, core.Health{State: core.HealthOK}, time.Time{}
	},
}

// Secrets: values never enter the list cache — only key names.
var secretsKind = &kindDef{
	desc: core.KindDescriptor{
		ID: "secrets", Title: "Secrets", Group: "Config", Scoped: true, Sensitive: true,
		Columns: []core.Column{colName, colNS, {ID: "type", Title: "Type", Type: core.ColText}, {ID: "keys", Title: "Keys", Type: core.ColText}, colAge},
	},
	gvr:        schema.GroupVersionResource{Version: "v1", Resource: "secrets"},
	namespaced: true,
	pre:        keysOnly("data", "stringData"),
	keep:       fields{"type": true},
	project: func(u *unstructured.Unstructured, _ time.Time) ([]core.Cell, core.Health, time.Time) {
		return []core.Cell{core.TextCell(u.GetName()), core.TextCell(u.GetNamespace()), core.TextCell(str(u.Object, "type")), keysCell(u), createdCell(u)},
			core.Health{State: core.HealthOK}, time.Time{}
	},
}

var nodesKind = &kindDef{
	desc: core.KindDescriptor{
		ID: "nodes", Title: "Nodes", Group: "Cluster",
		Columns: []core.Column{colName,
			{ID: "status", Title: "Status", Type: core.ColStatus, Width: 170},
			{ID: "roles", Title: "Roles", Type: core.ColText},
			{ID: "version", Title: "Version", Type: core.ColText, Width: 110},
			{ID: "internalip", Title: "Internal IP", Type: core.ColText, Width: 130},
			colAge,
			{ID: "cpu", Title: "CPU", Type: core.ColCPU, Width: 70, Metric: true},
			{ID: "memory", Title: "Memory", Type: core.ColBytes, Width: 80, Metric: true},
		},
	},
	gvr: schema.GroupVersionResource{Version: "v1", Resource: "nodes"},
	keep: fields{
		"spec":   fields{"unschedulable": true, "taints": true, "podCIDR": true},
		"status": fields{"conditions": true, "addresses": true, "nodeInfo": fields{"kubeletVersion": true, "osImage": true, "containerRuntimeVersion": true, "architecture": true}, "capacity": true, "allocatable": true},
	},
	project: projectNode,
}

// Node conditions that are problems when True.
var nodePressure = []string{"MemoryPressure", "DiskPressure", "PIDPressure", "NetworkUnavailable"}

func projectNode(u *unstructured.Unstructured, _ time.Time) ([]core.Cell, core.Health, time.Time) {
	o := u.Object
	cond := podConditions(o)
	ready := cond["Ready"]
	status := "Unknown"
	switch ready.status {
	case "True":
		status = "Ready"
	case "False":
		status = "NotReady"
	}
	unsched, _, _ := unstructured.NestedBool(o, "spec", "unschedulable")
	if unsched {
		status += ",SchedulingDisabled"
	}
	var roles []string
	for k := range u.GetLabels() {
		if r, ok := strings.CutPrefix(k, "node-role.kubernetes.io/"); ok && r != "" {
			roles = append(roles, r)
		}
	}
	sort.Strings(roles)
	var ip string
	for _, a := range slice(o, "status", "addresses") {
		if str(a, "type") == "InternalIP" {
			ip = str(a, "address")
		}
	}
	var issues []core.Issue
	switch ready.status {
	case "False":
		issues = append(issues, core.Issue{State: core.HealthError, Reason: "NotReady", Message: ready.message, Since: unixMs(ready.at)})
	case "True":
	default:
		// The kubelet stopped reporting: not a confirmed failure.
		issues = append(issues, core.Issue{State: core.HealthUnknown, Reason: "Unknown", Message: nonEmpty(ready.message, "the node stopped reporting"), Since: unixMs(ready.at)})
	}
	for _, p := range nodePressure {
		if cond[p].status == "True" {
			issues = append(issues, core.Issue{State: core.HealthWarning, Reason: p, Message: cond[p].message, Since: unixMs(cond[p].at)})
		}
	}
	if u.GetDeletionTimestamp() != nil {
		issues = append(issues, core.Issue{State: core.HealthTerminating, Reason: "Terminating"})
	}
	cells := []core.Cell{
		core.TextCell(u.GetName()), core.TextCell(status), core.TextCell(strings.Join(roles, ",")),
		core.TextCell(str(o, "status", "nodeInfo", "kubeletVersion")), core.TextCell(ip), createdCell(u), {}, {},
	}
	return cells, core.HealthFrom(issues), time.Time{}
}

var namespacesKind = &kindDef{
	desc: core.KindDescriptor{
		ID: "namespaces", Title: "Namespaces", Group: "Cluster",
		Columns: []core.Column{colName, {ID: "status", Title: "Status", Type: core.ColStatus, Width: 120}, colAge},
	},
	gvr:  namespacesGVR,
	keep: fields{"status": fields{"phase": true}},
	project: func(u *unstructured.Unstructured, _ time.Time) ([]core.Cell, core.Health, time.Time) {
		phase := nonEmpty(str(u.Object, "status", "phase"), "Active")
		h := core.Health{State: core.HealthOK}
		if phase == "Terminating" {
			h = core.HealthFrom([]core.Issue{{State: core.HealthTerminating, Reason: "Terminating"}})
		}
		return []core.Cell{core.TextCell(u.GetName()), core.TextCell(phase), createdCell(u)}, h, time.Time{}
	},
}

// eventsKind is core/v1 Events. Scoped by namespace; narrowed to one object
// by Query.Subject (involvedObject.uid).
var eventsKind = &kindDef{
	desc: core.KindDescriptor{
		ID: "events", Title: "Events", Group: "Cluster", Scoped: true,
		Columns: []core.Column{
			{ID: "lastseen", Title: "Last seen", Type: core.ColAge, Width: 90},
			{ID: "type", Title: "Type", Type: core.ColStatus, Width: 90},
			{ID: "reason", Title: "Reason", Type: core.ColText, Width: 160},
			{ID: "object", Title: "Object", Type: core.ColText},
			{ID: "message", Title: "Message", Type: core.ColText},
			{ID: "count", Title: "Count", Type: core.ColNumber, Width: 70},
			colNS,
		},
	},
	gvr:        schema.GroupVersionResource{Version: "v1", Resource: "events"},
	namespaced: true,
	keep: fields{
		"involvedObject": fields{"apiVersion": true, "kind": true, "name": true, "namespace": true, "uid": true},
		"reason":         true, "message": true, "type": true, "count": true,
		"firstTimestamp": true, "lastTimestamp": true, "eventTime": true,
		"series": fields{"count": true, "lastObservedTime": true},
		"source": fields{"component": true},
	},
	project: func(u *unstructured.Unstructured, _ time.Time) ([]core.Cell, core.Health, time.Time) {
		o := u.Object
		last, _ := eventLastSeen(u)
		count := eventCount(o)
		typ := nonEmpty(str(o, "type"), "Normal")
		obj := strings.ToLower(str(o, "involvedObject", "kind")) + "/" + str(o, "involvedObject", "name")
		cells := []core.Cell{
			core.TimeCell(last.UnixMilli()), core.TextCell(typ), core.TextCell(str(o, "reason")), core.TextCell(obj),
			core.TextCell(str(o, "message")), num(count), core.TextCell(u.GetNamespace()),
		}
		h := core.Health{State: core.HealthOK}
		if typ == "Warning" {
			h = core.HealthFrom([]core.Issue{{State: core.HealthWarning, Reason: str(o, "reason"), Message: str(o, "message")}})
		}
		return cells, h, time.Time{}
	},
}

// eventLastSeen is when the event (series) was last observed:
// series.lastObservedTime, lastTimestamp, eventTime, else the object's
// creation (created says so).
func eventLastSeen(u *unstructured.Unstructured) (last time.Time, created bool) {
	o := u.Object
	for _, t := range []time.Time{timeAt(o, "series", "lastObservedTime"), timeAt(o, "lastTimestamp"), timeAtMicro(o, "eventTime")} {
		if !t.IsZero() {
			return t, false
		}
	}
	return u.GetCreationTimestamp().Time, true
}

// eventCount is the API's cumulative count of the event (series): one of
// them, never a sum.
func eventCount(o map[string]any) int64 {
	if n := i64(o, "series", "count"); n > 0 {
		return n
	}
	if n := i64(o, "count"); n > 0 {
		return n
	}
	return 1
}

// eventRecent: a Warning event last observed this recently is evidence in
// Problems.
const eventRecent = 15 * time.Minute

// recentWarning is a Warning event as recent evidence: while it was last
// observed within eventRecent. next is when that changes: the end of the
// window, or, for a time further ahead than clock skew allows, when local
// time catches up with it.
func recentWarning(u *unstructured.Unstructured, now time.Time) (is core.Issue, next time.Time, ok bool) {
	o := u.Object
	if str(o, "type") != "Warning" {
		return core.Issue{}, time.Time{}, false
	}
	at, created := eventLastSeen(u)
	switch {
	case at.IsZero():
		return core.Issue{}, time.Time{}, false
	case at.After(now.Add(clockSkewTolerance)):
		return core.Issue{}, at.Add(-clockSkewTolerance), false
	case now.Sub(at) >= eventRecent:
		return core.Issue{}, time.Time{}, false
	}
	since := at
	if since.After(now) {
		since = now
	}
	msg := str(o, "message")
	if n := eventCount(o); n > 1 {
		msg += fmt.Sprintf(" (%d in total)", n)
	}
	if created {
		msg += " (time of creation: the event has no observation time)"
	}
	return core.Issue{State: core.HealthWarning, Reason: str(o, "reason"), Message: msg, Since: since.UnixMilli()}, at.Add(eventRecent), true
}

// timeAtMicro parses MicroTime (RFC3339 with fractional seconds).
func timeAtMicro(u map[string]any, path ...string) time.Time {
	s := str(u, path...)
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
