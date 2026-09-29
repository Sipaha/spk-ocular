package kubernetes

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// realisticPod resembles a production pod: long labels/annotations, two
// containers with env and resources, managedFields, full statuses.
func realisticPod(i int) *unstructured.Unstructured {
	env := []any{}
	for j := 0; j < 20; j++ {
		env = append(env, map[string]any{"name": fmt.Sprintf("ENV_%d", j), "value": fmt.Sprintf("value-%d-%d-some-longer-configuration-string", i, j)})
	}
	container := func(n string) map[string]any {
		return map[string]any{"name": n, "image": "registry.example.com/team/service:1.2.3", "env": env,
			"resources":    map[string]any{"limits": map[string]any{"cpu": "1", "memory": "1Gi"}, "requests": map[string]any{"cpu": "100m", "memory": "256Mi"}},
			"volumeMounts": []any{map[string]any{"name": "cfg", "mountPath": "/etc/cfg"}, map[string]any{"name": "token", "mountPath": "/var/run/secrets"}}}
	}
	status := func(n string) map[string]any {
		return map[string]any{"name": n, "ready": true, "restartCount": int64(2), "started": true, "image": "registry.example.com/team/service:1.2.3",
			"imageID":     "registry.example.com/team/service@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			"containerID": "containerd://0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			"state":       map[string]any{"running": map[string]any{"startedAt": "2026-09-29T10:00:00Z"}}}
	}
	mf := []any{}
	for j := 0; j < 4; j++ {
		mf = append(mf, map[string]any{"manager": "kube-controller-manager", "operation": "Update", "fieldsV1": map[string]any{"f:metadata": map[string]any{"f:labels": map[string]any{"x": fmt.Sprint(j)}}}})
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": fmt.Sprintf("service-%05d-abcde", i), "namespace": fmt.Sprintf("team-%d", i%40), "uid": fmt.Sprintf("uid-%08d", i),
			"resourceVersion": "123456", "creationTimestamp": "2026-09-29T10:00:00Z",
			"labels":          map[string]any{"app": "service", "team": "payments", "version": "1.2.3", "pod-template-hash": "5d7f9c8b6"},
			"annotations":     map[string]any{"prometheus.io/scrape": "true", "checksum/config": "0123456789abcdef0123456789abcdef"},
			"ownerReferences": []any{map[string]any{"apiVersion": "apps/v1", "kind": "ReplicaSet", "name": "service-5d7f9c8b6", "uid": "rs-uid", "controller": true}},
			"managedFields":   mf},
		"spec": map[string]any{"nodeName": fmt.Sprintf("node-%d", i%50), "containers": []any{container("app"), container("sidecar")},
			"volumes": []any{map[string]any{"name": "cfg", "configMap": map[string]any{"name": "cfg"}}}},
		"status": map[string]any{"phase": "Running", "podIP": "10.1.2.3", "startTime": "2026-09-29T10:00:00Z",
			"conditions":        []any{map[string]any{"type": "Ready", "status": "True"}, map[string]any{"type": "PodScheduled", "status": "True"}},
			"containerStatuses": []any{status("app"), status("sidecar")}},
	}}
}

// Synthetic, not a cluster measurement: retained memory of 10k realistic
// pods in a list cache with and without the whitelist, and projection cost.
// OCULAR_SYNTH=1 go test -run Synthetic -v ./internal/providers/kubernetes/
func TestSyntheticTenThousandPods(t *testing.T) {
	if os.Getenv("OCULAR_SYNTH") == "" {
		t.Skip("OCULAR_SYNTH=1 to run")
	}
	const n = 10000
	heap := func() uint64 {
		runtime.GC()
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return ms.HeapAlloc
	}
	base := heap()
	full := make([]*unstructured.Unstructured, n)
	for i := range full {
		full[i] = realisticPod(i)
	}
	untrimmed := heap() - base
	runtime.KeepAlive(full)
	full = nil
	base = heap()
	trimmed := make([]*unstructured.Unstructured, n)
	for i := range trimmed {
		trimmed[i] = trim(realisticPod(i), podsKind.keep)
	}
	kept := heap() - base
	start := time.Now()
	for _, u := range trimmed {
		podsKind.project(u, time.Now())
	}
	t.Logf("10k pods: untrimmed %.1f MB (%.1f KB/pod), trimmed %.1f MB (%.1f KB/pod); projecting all took %v",
		float64(untrimmed)/1e6, float64(untrimmed)/n/1e3, float64(kept)/1e6, float64(kept)/n/1e3, time.Since(start))
	runtime.KeepAlive(trimmed)
	runtime.KeepAlive(full)
}

func TestSyntheticCompactEncoding(t *testing.T) {
	if os.Getenv("OCULAR_SYNTH") == "" {
		t.Skip("OCULAR_SYNTH=1 to run")
	}
	const n = 10000
	heap := func() uint64 {
		runtime.GC()
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return ms.HeapAlloc
	}
	base := heap()
	raws := make([][]byte, n)
	for i := range raws {
		u := trim(realisticPod(i), podsKind.keep)
		b, _ := json.Marshal(u.Object)
		raws[i] = b
	}
	kept := heap() - base
	start := time.Now()
	for _, b := range raws {
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		podsKind.project(&unstructured.Unstructured{Object: m}, time.Now())
	}
	t.Logf("10k pods as trimmed JSON: %.1f MB (%.2f KB/pod); decode+project all %v", float64(kept)/1e6, float64(kept)/n/1e3, time.Since(start))
	runtime.KeepAlive(raws)
}

func TestSyntheticSlimCache(t *testing.T) {
	if os.Getenv("OCULAR_SYNTH") == "" {
		t.Skip("OCULAR_SYNTH=1 to run")
	}
	const n = 10000
	heap := func() uint64 {
		runtime.GC()
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return ms.HeapAlloc
	}
	base := heap()
	objs := make([]*slimObject, n)
	for i := range objs {
		o, err := slim(trim(realisticPod(i), podsKind.keep))
		if err != nil {
			t.Fatal(err)
		}
		objs[i] = o
	}
	kept := heap() - base
	start := time.Now()
	for _, o := range objs {
		podsKind.project(o.expand(), time.Now())
	}
	t.Logf("10k pods as slim objects: %.1f MB (%.2f KB/pod); expand+project all %v", float64(kept)/1e6, float64(kept)/n/1e3, time.Since(start))
	runtime.KeepAlive(objs)
}
