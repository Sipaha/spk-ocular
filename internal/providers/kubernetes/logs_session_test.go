package kubernetes

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

func withLogContainers(o map[string]any) {
	spec := o["spec"].(map[string]any)
	spec["containers"] = []any{map[string]any{"name": "app"}, map[string]any{"name": "proxy"}}
	spec["initContainers"] = []any{
		map[string]any{"name": "migrate"},
		map[string]any{"name": "mesh", "restartPolicy": "Always"},
	}
	spec["ephemeralContainers"] = []any{map[string]any{"name": "debugger"}}
	spec["restartPolicy"] = "Always"
	o["metadata"].(map[string]any)["annotations"] = map[string]any{defaultContainerAnnotation: "proxy"}
	o["status"].(map[string]any)["containerStatuses"] = []any{
		map[string]any{"name": "app", "containerID": "containerd://c1", "restartCount": int64(0), "state": map[string]any{"running": map[string]any{"startedAt": "2026-09-29T10:00:00Z"}}},
	}
}

func TestLogInfoListsContainersAndTheDefault(t *testing.T) {
	s := newSession("ctx", "h", fakeClient(pod("ns", "p", "uid-1", withLogContainers)), false)
	t.Cleanup(s.Close)
	ref := core.Ref{Provider: ProviderID, Target: "ctx", Scope: "ns", Kind: "pods", Name: "p", UID: "uid-1"}
	info, err := s.LogInfo(context.Background(), ref)
	require.NoError(t, err)
	assert.Equal(t, []core.LogChannel{
		{ID: "app", Title: "app"}, {ID: "proxy", Title: "proxy"},
		{ID: "migrate", Title: "migrate", Note: "init"}, {ID: "mesh", Title: "mesh", Note: "sidecar"},
		{ID: "debugger", Title: "debugger", Note: "ephemeral"},
	}, info.Channels)
	assert.Equal(t, "proxy", info.DefaultChannel, "kubectl's default-container annotation")
	assert.True(t, info.Previous)

	ref.UID = "uid-old"
	_, err = s.LogInfo(context.Background(), ref)
	var pe *provider.Error
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, provider.ClassGone, pe.Class, "never the logs of a same-named replacement")
}

// Through the real informer: the pod's restart reaches the source.
func TestStreamLogsFollowsARestartFromTheCache(t *testing.T) {
	client := fakeClient(pod("ns", "p", "uid-1", withLogContainers), pod("ns", "other", "uid-9"))
	s := newSession("ctx", "h", client, false)
	t.Cleanup(s.Close)
	k := newFakeKubelet(t)
	s.logs = k.fetcher(t)
	k.start("p", "app", "containerd://c1")
	k.log("p", "app", fakeRec{ts: at(1), text: "boot 1"})

	sink := newLogRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	ref := core.Ref{Provider: ProviderID, Target: "ctx", Scope: "ns", Kind: "pods", Name: "p", UID: "uid-1"}
	go func() {
		done <- s.StreamLogs(ctx, ref, provider.LogQuery{Channel: "app", Follow: true, TailLines: 100}, sink)
	}()
	sink.await(t, "boot 1", func() bool { return len(sink.texts()) == 1 })
	assert.Equal(t, []string{"uid-1/app"}, sink.sources)

	// exit → the cache says so → waiting; restart → the new instance
	k.end("p", "app")
	exitedPod := pod("ns", "p", "uid-1", withLogContainers, func(o map[string]any) {
		o["metadata"].(map[string]any)["resourceVersion"] = "2"
		o["status"].(map[string]any)["containerStatuses"] = []any{map[string]any{"name": "app", "containerID": "containerd://c1", "restartCount": int64(0),
			"state": map[string]any{"terminated": map[string]any{"exitCode": int64(1), "containerID": "containerd://c1"}}}}
	})
	_, err := client.Resource(podGVR).Namespace("ns").Update(ctx, exitedPod, metav1.UpdateOptions{})
	require.NoError(t, err)
	sink.await(t, "waiting", func() bool { return sink.lastState().State == provider.LogWaiting })

	k.start("p", "app", "containerd://c2")
	k.log("p", "app", fakeRec{ts: at(40), text: "boot 2"})
	restarted := pod("ns", "p", "uid-1", withLogContainers, func(o map[string]any) {
		o["metadata"].(map[string]any)["resourceVersion"] = "3"
		o["status"].(map[string]any)["containerStatuses"] = []any{map[string]any{"name": "app", "containerID": "containerd://c2", "restartCount": int64(1),
			"state": map[string]any{"running": map[string]any{"startedAt": "2026-09-29T10:00:39Z"}}}}
	})
	_, err = client.Resource(podGVR).Namespace("ns").Update(ctx, restarted, metav1.UpdateOptions{})
	require.NoError(t, err)
	sink.await(t, "boot 2", func() bool { return len(sink.texts()) == 2 })

	// the pod is deleted → the source ends; the stream is complete
	k.end("p", "app")
	require.NoError(t, client.Resource(podGVR).Namespace("ns").Delete(ctx, "p", metav1.DeleteOptions{}))
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not end with its pod")
	}
	assert.Equal(t, provider.LogEnded, sink.lastState().State)
	assert.Contains(t, sink.lastState().Message, "deleted")
	cancel()
	active, _ := s.caches.stats()
	assert.Equal(t, 0, active, "the pod observation lease is released")
}

// Observations come from the slim cache, where numbers are JSON float64s:
// exit codes and restart counts must survive that (an OnFailure container
// that failed must not look like a success).
func TestObservePodThroughTheSlimCache(t *testing.T) {
	u := pod("ns", "p", "uid-1", withLogContainers, func(o map[string]any) {
		o["spec"].(map[string]any)["restartPolicy"] = "OnFailure"
		o["status"].(map[string]any)["containerStatuses"] = []any{map[string]any{
			"name": "app", "containerID": "containerd://c2", "restartCount": int64(4),
			"state":     map[string]any{"waiting": map[string]any{"reason": "CrashLoopBackOff"}},
			"lastState": map[string]any{"terminated": map[string]any{"exitCode": int64(3), "containerID": "containerd://c2"}},
		}}
		o["status"].(map[string]any)["ephemeralContainerStatuses"] = []any{map[string]any{
			"name": "debugger", "containerID": "containerd://e1",
			"state": map[string]any{"terminated": map[string]any{"exitCode": int64(0), "containerID": "containerd://e1"}},
		}}
	})
	so, err := slim(trim(u, podsKind.keep))
	require.NoError(t, err)
	o := observePod(so.expand())
	app := o.ctrs["app"]
	assert.Equal(t, ctrObs{id: "containerd://c2", restarts: 4, waiting: "CrashLoopBackOff", exitCode: 3}, app)
	assert.True(t, o.restartExpected(app), "OnFailure + exit 3: it comes back")
	assert.Equal(t, ctrSidecar, o.ctrs["mesh"].kind)
	assert.Equal(t, ctrInit, o.ctrs["migrate"].kind)
	dbg := o.ctrs["debugger"]
	assert.Equal(t, ctrEphemeral, dbg.kind)
	assert.True(t, dbg.exited)
	assert.False(t, o.restartExpected(dbg))
}
