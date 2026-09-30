package kubernetes

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

func obj(created time.Duration, o map[string]any) *unstructured.Unstructured {
	meta := map[string]any{"name": "x", "namespace": "ns", "uid": "u", "creationTimestamp": ts(created), "generation": int64(2)}
	if m, ok := o["metadata"].(map[string]any); ok {
		for k, v := range m {
			meta[k] = v
		}
	}
	o["metadata"] = meta
	return &unstructured.Unstructured{Object: o}
}

func project(t *testing.T, d *kindDef, u *unstructured.Unstructured) ([]core.Cell, core.Health, time.Time) {
	t.Helper()
	if d.pre != nil {
		d.pre(u)
	}
	u = trim(u, d.keep)
	cells, h, next := d.project(u, now)
	require.Len(t, cells, len(d.desc.Columns), "one cell per column")
	return cells, h, next
}

func deploy(replicas, ready, updated, avail, observed int64, conds ...any) *unstructured.Unstructured {
	return obj(time.Hour, map[string]any{
		"spec": map[string]any{"replicas": replicas, "selector": map[string]any{}},
		"status": map[string]any{"replicas": replicas, "readyReplicas": ready, "updatedReplicas": updated,
			"availableReplicas": avail, "observedGeneration": observed, "conditions": conds},
	})
}

func TestDeploymentHealth(t *testing.T) {
	cases := []struct {
		name   string
		u      *unstructured.Unstructured
		ready  string
		state  core.HealthState
		reason string
	}{
		{"healthy", deploy(3, 3, 3, 3, 2), "3/3", core.HealthOK, ""},
		{"scaled to zero", deploy(0, 0, 0, 0, 2), "0/0", core.HealthOK, "ScaledToZero"},
		{"spec not observed yet", deploy(3, 3, 3, 3, 1), "3/3", core.HealthProgressing, "RollingOut"},
		{"rolling", deploy(3, 2, 1, 2, 2), "2/3", core.HealthProgressing, "RollingOut"},
		{"partially unavailable", deploy(3, 2, 3, 2, 2), "2/3", core.HealthWarning, "Unavailable"},
		{"nothing available", deploy(3, 0, 3, 0, 2), "0/3", core.HealthError, "Unavailable"},
		{"deadline exceeded", deploy(3, 1, 2, 1, 2, map[string]any{"type": "Progressing", "status": "False", "reason": "ProgressDeadlineExceeded", "message": "timed out"}),
			"1/3", core.HealthError, "ProgressDeadlineExceeded"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cells, h, _ := project(t, deploymentsKind, c.u)
			assert.Equal(t, c.ready, cells[2].Text)
			assert.Equal(t, c.state, h.State, "%+v", h)
			assert.Equal(t, c.reason, h.Reason)
		})
	}
}

func TestStatefulSetAndDaemonSetHealth(t *testing.T) {
	sts := obj(time.Hour, map[string]any{"spec": map[string]any{"replicas": int64(3)},
		"status": map[string]any{"readyReplicas": int64(1), "observedGeneration": int64(2), "currentRevision": "a", "updateRevision": "a"}})
	cells, h, _ := project(t, statefulSetsKind, sts)
	assert.Equal(t, "1/3", cells[2].Text)
	assert.Equal(t, core.HealthWarning, h.State)

	ds := obj(time.Hour, map[string]any{"status": map[string]any{"desiredNumberScheduled": int64(4), "currentNumberScheduled": int64(4),
		"numberReady": int64(4), "updatedNumberScheduled": int64(4), "numberAvailable": int64(4), "numberMisscheduled": int64(1), "observedGeneration": int64(2)}})
	cells, h, _ = project(t, daemonSetsKind, ds)
	assert.Equal(t, "4/4", cells[4].Text)
	assert.Equal(t, core.HealthWarning, h.State)
	assert.Equal(t, "Misscheduled", h.Reason)
}

// Rolling out must not hide that nothing runs (StatefulSets and DaemonSets
// have no progress deadline); an intended difference of revisions (OnDelete,
// a partition) or a paused rollout is not a fault by itself.
func TestWorkloadHealthAudit(t *testing.T) {
	sts := func(strategy map[string]any, replicas, ready, updated int64, cur, upd string) *unstructured.Unstructured {
		spec := map[string]any{"replicas": replicas}
		if strategy != nil {
			spec["updateStrategy"] = strategy
		}
		return obj(time.Hour, map[string]any{"spec": spec, "status": map[string]any{"replicas": replicas, "readyReplicas": ready,
			"updatedReplicas": updated, "observedGeneration": int64(2), "currentRevision": cur, "updateRevision": upd}})
	}
	ds := func(strategy map[string]any, desired, ready, updated, avail int64) *unstructured.Unstructured {
		spec := map[string]any{}
		if strategy != nil {
			spec["updateStrategy"] = strategy
		}
		return obj(time.Hour, map[string]any{"spec": spec, "status": map[string]any{"desiredNumberScheduled": desired, "currentNumberScheduled": desired,
			"numberReady": ready, "updatedNumberScheduled": updated, "numberAvailable": avail, "observedGeneration": int64(2)}})
	}
	paused := func(replicas, updated, avail int64) *unstructured.Unstructured {
		u := deploy(replicas, avail, updated, avail, 2)
		u.Object["spec"].(map[string]any)["paused"] = true
		return u
	}
	onDelete := map[string]any{"type": "OnDelete"}
	partition := func(p int64) map[string]any {
		return map[string]any{"type": "RollingUpdate", "rollingUpdate": map[string]any{"partition": p}}
	}
	cases := []struct {
		name   string
		def    *kindDef
		u      *unstructured.Unstructured
		state  core.HealthState
		reason string
	}{
		{"sts rolling, some ready", statefulSetsKind, sts(nil, 3, 2, 1, "a", "b"), core.HealthProgressing, "RollingOut"},
		{"sts rolling, none ready", statefulSetsKind, sts(nil, 3, 0, 1, "a", "b"), core.HealthError, "NotReady"},
		{"sts OnDelete, revisions differ, all ready", statefulSetsKind, sts(onDelete, 3, 3, 0, "a", "b"), core.HealthOK, "UpdatePending"},
		{"sts OnDelete, revisions differ, one not ready", statefulSetsKind, sts(onDelete, 3, 2, 0, "a", "b"), core.HealthWarning, "NotReady"},
		{"sts partition reached", statefulSetsKind, sts(partition(2), 3, 3, 1, "a", "b"), core.HealthOK, "UpdatePending"},
		{"sts partition not reached", statefulSetsKind, sts(partition(1), 3, 3, 1, "a", "b"), core.HealthProgressing, "RollingOut"},
		{"ds rolling, none available", daemonSetsKind, ds(nil, 3, 0, 1, 0), core.HealthError, "Unavailable"},
		{"ds OnDelete, not updated, all available", daemonSetsKind, ds(onDelete, 3, 3, 1, 3), core.HealthOK, "UpdatePending"},
		{"deployment paused, available", deploymentsKind, paused(3, 1, 3), core.HealthOK, "Paused"},
		{"deployment paused, unavailable", deploymentsKind, paused(3, 3, 1), core.HealthWarning, "Unavailable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, h, _ := project(t, c.def, c.u)
			assert.Equal(t, c.state, h.State, "%+v", h)
			assert.Equal(t, c.reason, h.Reason)
		})
	}
}

func TestServiceColumnsAndPendingLoadBalancer(t *testing.T) {
	svc := obj(time.Minute, map[string]any{"spec": map[string]any{"type": "LoadBalancer", "clusterIP": "10.0.0.5",
		"ports": []any{map[string]any{"port": int64(443), "nodePort": int64(30443), "protocol": "TCP"}, map[string]any{"port": int64(53), "protocol": "UDP"}}}})
	cells, h, next := project(t, servicesKind, svc)
	assert.Equal(t, "LoadBalancer", cells[2].Text)
	assert.Equal(t, "443:30443/TCP,53/UDP", cells[5].Text)
	assert.Equal(t, core.HealthProgressing, h.State)
	assert.False(t, next.IsZero(), "turns into a warning later without an API event")

	old := obj(time.Hour, map[string]any{"spec": map[string]any{"type": "LoadBalancer"}})
	_, h, _ = project(t, servicesKind, old)
	assert.Equal(t, core.HealthWarning, h.State)

	lb := obj(time.Hour, map[string]any{"spec": map[string]any{"type": "LoadBalancer"},
		"status": map[string]any{"loadBalancer": map[string]any{"ingress": []any{map[string]any{"ip": "1.2.3.4"}}}}})
	cells, h, _ = project(t, servicesKind, lb)
	assert.Equal(t, "1.2.3.4", cells[4].Text)
	assert.Equal(t, core.HealthOK, h.State)

	plain := obj(time.Hour, map[string]any{"spec": map[string]any{"clusterIP": "10.0.0.1"}})
	cells, h, _ = project(t, servicesKind, plain)
	assert.Equal(t, "ClusterIP", cells[2].Text)
	assert.Equal(t, core.HealthOK, h.State)
}

func TestIngressColumns(t *testing.T) {
	ing := obj(time.Hour, map[string]any{"spec": map[string]any{"ingressClassName": "nginx",
		"rules": []any{map[string]any{"host": "a.example"}, map[string]any{}}, "tls": []any{map[string]any{}}},
		"status": map[string]any{"loadBalancer": map[string]any{"ingress": []any{map[string]any{"hostname": "lb.example"}}}}})
	cells, h, _ := project(t, ingressesKind, ing)
	assert.Equal(t, []string{"nginx", "a.example,*", "lb.example", "80,443"}, []string{cells[2].Text, cells[3].Text, cells[4].Text, cells[5].Text})
	assert.Equal(t, core.HealthOK, h.State)
}

func TestConfigMapsAndSecretsKeepOnlyKeyNames(t *testing.T) {
	sec := obj(time.Hour, map[string]any{"type": "Opaque", "data": map[string]any{"password": "c2VjcmV0", "user": "YWRtaW4="}})
	cells, _, _ := project(t, secretsKind, sec)
	assert.Equal(t, "Opaque", cells[2].Text)
	assert.Equal(t, "password, user", cells[3].Text)
	_, hasData := sec.Object["data"]
	assert.False(t, hasData, "values never reach the cache")
	trimmed := trim(sec, secretsKind.keep)
	secretsKind.pre(trimmed) // idempotent on an already transformed object
	assert.Equal(t, []string{"password", "user"}, keyNames(trimmed))

	big := map[string]any{}
	for _, k := range []string{"a", "b", "c", "d"} {
		big[k] = "value"
	}
	cm := obj(time.Hour, map[string]any{"data": big, "binaryData": map[string]any{"bin": "AAAA"}})
	cells, _, _ = project(t, configMapsKind, cm)
	assert.Equal(t, "5", cells[2].Text, "many keys: the count")
	assert.InDelta(t, 5, *cells[2].Num, 0)
}

func TestNodeHealth(t *testing.T) {
	node := func(ready string, extra ...any) *unstructured.Unstructured {
		conds := append([]any{map[string]any{"type": "Ready", "status": ready, "message": "kubelet says " + ready}}, extra...)
		n := obj(time.Hour, map[string]any{
			"metadata": map[string]any{"namespace": "", "labels": map[string]any{"node-role.kubernetes.io/control-plane": "", "node-role.kubernetes.io/worker": ""}},
			"spec":     map[string]any{"unschedulable": true},
			"status": map[string]any{"conditions": conds, "nodeInfo": map[string]any{"kubeletVersion": "v1.37.0"},
				"addresses": []any{map[string]any{"type": "InternalIP", "address": "10.1.1.1"}}},
		})
		return n
	}
	cells, h, _ := project(t, nodesKind, node("True"))
	assert.Equal(t, "Ready,SchedulingDisabled", cells[1].Text)
	assert.Equal(t, "control-plane,worker", cells[2].Text)
	assert.Equal(t, "v1.37.0", cells[3].Text)
	assert.Equal(t, "10.1.1.1", cells[4].Text)
	assert.Equal(t, core.HealthOK, h.State)

	_, h, _ = project(t, nodesKind, node("False"))
	assert.Equal(t, core.HealthError, h.State)
	_, h, _ = project(t, nodesKind, node("Unknown"))
	assert.Equal(t, core.HealthUnknown, h.State, "stopped reporting is not a confirmed failure")
	_, h, _ = project(t, nodesKind, node("True", map[string]any{"type": "DiskPressure", "status": "True", "message": "disk full"}))
	assert.Equal(t, core.HealthWarning, h.State)
	assert.Equal(t, "DiskPressure", h.Reason)
}

func TestEventColumns(t *testing.T) {
	ev := obj(time.Hour, map[string]any{
		"involvedObject": map[string]any{"kind": "Pod", "name": "web-1", "uid": "p1"},
		"reason":         "BackOff", "message": "Back-off restarting failed container", "type": "Warning",
		"count": int64(7), "lastTimestamp": ts(2 * time.Minute),
	})
	cells, h, _ := project(t, eventsKind, ev)
	assert.Equal(t, now.Add(-2*time.Minute).UnixMilli(), cells[0].Time)
	assert.Equal(t, "Warning", cells[1].Text)
	assert.Equal(t, "pod/web-1", cells[3].Text)
	assert.Equal(t, "7", cells[5].Text)
	assert.Equal(t, core.HealthWarning, h.State)

	series := obj(time.Hour, map[string]any{"type": "Normal", "eventTime": now.Add(-time.Minute).Format(time.RFC3339Nano),
		"series": map[string]any{"count": int64(3), "lastObservedTime": ts(10 * time.Second)}})
	cells, h, _ = project(t, eventsKind, series)
	assert.Equal(t, now.Add(-10*time.Second).UnixMilli(), cells[0].Time)
	assert.Equal(t, "3", cells[5].Text)
	assert.Equal(t, core.HealthOK, h.State)
}

// A Warning event is recent evidence while it was last observed within
// eventRecent; it expires through the projection's next (no API event
// needed). The count is the API's cumulative one.
func TestRecentWarningEvent(t *testing.T) {
	ev := func(fields map[string]any) *unstructured.Unstructured {
		o := map[string]any{"involvedObject": map[string]any{"apiVersion": "v1", "kind": "Pod", "name": "web-1", "uid": "p1"},
			"reason": "BackOff", "message": "Back-off restarting failed container", "type": "Warning"}
		for k, v := range fields {
			o[k] = v
		}
		return trim(obj(time.Hour, o), eventsKind.keep)
	}
	t.Run("recent", func(t *testing.T) {
		is, next, ok := recentWarning(ev(map[string]any{"count": int64(7), "lastTimestamp": ts(2 * time.Minute)}), now)
		require.True(t, ok)
		assert.Equal(t, core.HealthWarning, is.State)
		assert.Equal(t, "BackOff", is.Reason)
		assert.Equal(t, "Back-off restarting failed container (7 in total)", is.Message)
		assert.Equal(t, now.Add(-2*time.Minute).UnixMilli(), is.Since)
		assert.Equal(t, now.Add(-2*time.Minute).Add(eventRecent), next)
	})
	t.Run("series time and count lead", func(t *testing.T) {
		is, _, ok := recentWarning(ev(map[string]any{"count": int64(2), "lastTimestamp": ts(time.Hour),
			"series": map[string]any{"count": int64(40), "lastObservedTime": ts(time.Minute)}}), now)
		require.True(t, ok)
		assert.Contains(t, is.Message, "(40 in total)")
		assert.Equal(t, now.Add(-time.Minute).UnixMilli(), is.Since)
	})
	t.Run("a single one", func(t *testing.T) {
		is, _, ok := recentWarning(ev(map[string]any{"eventTime": now.Add(-time.Minute).Format(time.RFC3339Nano)}), now)
		require.True(t, ok)
		assert.Equal(t, "Back-off restarting failed container", is.Message)
	})
	t.Run("only the creation time: said", func(t *testing.T) {
		u := ev(nil)
		u.SetCreationTimestamp(metav1.NewTime(now.Add(-time.Minute)))
		is, _, ok := recentWarning(u, now)
		require.True(t, ok)
		assert.Contains(t, is.Message, "time of creation")
	})
	t.Run("old", func(t *testing.T) {
		_, next, ok := recentWarning(ev(map[string]any{"lastTimestamp": ts(time.Hour)}), now)
		assert.False(t, ok)
		assert.True(t, next.IsZero())
	})
	t.Run("normal", func(t *testing.T) {
		u := ev(map[string]any{"lastTimestamp": ts(time.Minute)})
		u.Object["type"] = "Normal"
		_, _, ok := recentWarning(u, now)
		assert.False(t, ok)
	})
	t.Run("far ahead: judged when local time catches up", func(t *testing.T) {
		u := ev(map[string]any{"lastTimestamp": ts(-10 * time.Minute)})
		_, next, ok := recentWarning(u, now)
		assert.False(t, ok)
		assert.Equal(t, now.Add(10*time.Minute-clockSkewTolerance), next)
		is, _, ok := recentWarning(u, next)
		require.True(t, ok)
		assert.Equal(t, next.UnixMilli(), is.Since, "never in the future")
	})
	t.Run("the involved object keeps its API group", func(t *testing.T) {
		u := ev(map[string]any{"lastTimestamp": ts(time.Minute)})
		assert.Equal(t, "v1", str(u.Object, "involvedObject", "apiVersion"))
	})
}

func TestKindIDsAreUniqueAndQualified(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range allKinds.list {
		assert.False(t, seen[d.desc.ID], d.desc.ID)
		seen[d.desc.ID] = true
		if d.virtual {
			continue // a view of other kinds, not an API resource
		}
		if d.gvr.Group != "" {
			assert.Equal(t, d.gvr.Group+"/"+d.gvr.Resource, d.desc.ID, "grouped kinds are qualified")
		}
		assert.Equal(t, d.namespaced, d.desc.Scoped, d.desc.ID)
	}
}

// Palette commands (":po", ":deploy web") name a kind by an alias, kubectl's
// short names: one alias names one kind.
func TestKindAliasesAreKubectlShortNamesAndUnique(t *testing.T) {
	byAlias := map[string]string{}
	for _, d := range allKinds.descriptors() {
		for _, a := range d.Aliases {
			assert.Empty(t, byAlias[a], "alias %q of %s is taken by %s", a, d.ID, byAlias[a])
			byAlias[a] = d.ID
		}
	}
	for alias, id := range map[string]string{"po": "pods", "deploy": "apps/deployments", "sts": "apps/statefulsets", "ds": "apps/daemonsets", "rs": "apps/replicasets", "svc": "services", "ing": "networking.k8s.io/ingresses", "cm": "configmaps", "no": "nodes", "ns": "namespaces", "ev": "events"} {
		assert.Equal(t, id, byAlias[alias], alias)
	}
	p := NewWith(func(string) string { return "" }, t.TempDir())
	assert.Equal(t, provider.CommandAliases{Scope: []string{"ns", "namespace"}, Target: []string{"ctx", "context"}}, p.CommandAliases())
}

// The generic UI learns from the descriptors what to open first and where
// an object's events are; it knows neither pods nor events.
func TestKindsSayWhichOpensFirstAndWhereEventsAre(t *testing.T) {
	var defaults []string
	for _, d := range allKinds.descriptors() {
		if d.Default {
			defaults = append(defaults, d.ID)
		}
		if d.ID == "events" {
			assert.Empty(t, d.EventsKind, "events have no events")
		} else {
			assert.Equal(t, "events", d.EventsKind, d.ID)
		}
	}
	assert.Equal(t, []string{"pods"}, defaults)
	p := NewWith(func(string) string { return "" }, t.TempDir())
	names := p.ScopeNames()
	assert.Equal(t, []string{"Namespace", "namespaces", "All namespaces"}, []string{names.Singular.Text, names.Plural.Text, names.All.Text})
	assert.Equal(t, "kubernetes.scope.all", names.All.Key)
}
