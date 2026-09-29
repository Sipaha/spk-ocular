package kubernetes

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

var (
	deployGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	rsGVR     = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}
)

func groupClient(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		podGVR: "PodList", namespacesGVR: "NamespaceList", deployGVR: "DeploymentList", rsGVR: "ReplicaSetList",
	}, objs...)
}

func ctrlRef(kind, name, uid string) map[string]any {
	return map[string]any{"apiVersion": "apps/v1", "kind": kind, "name": name, "uid": uid, "controller": true}
}

func deployment(name, uid string, ctrs ...string) *unstructured.Unstructured {
	var cs []any
	for _, c := range ctrs {
		cs = append(cs, map[string]any{"name": c, "image": "x"})
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"name": name, "namespace": "ns", "uid": uid},
		"spec": map[string]any{
			"selector": map[string]any{"matchLabels": map[string]any{"app": name}},
			"template": map[string]any{"spec": map[string]any{"containers": cs}},
		},
	}}
}

func replicaSet(name, uid, deployUID string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1", "kind": "ReplicaSet",
		"metadata": map[string]any{"name": name, "namespace": "ns", "uid": uid, "ownerReferences": []any{ctrlRef("Deployment", "web", deployUID)},
			"labels": map[string]any{"app": "web"}}, // a ReplicaSet carries its template's labels
		"spec": map[string]any{"replicas": int64(1)},
	}}
}

// memberOf makes a running pod of rs with the given containers, created at sec.
func memberOf(name, uid, rsUID string, sec int, ctrs ...string) *unstructured.Unstructured {
	return pod("ns", name, uid, func(o map[string]any) {
		md := o["metadata"].(map[string]any)
		md["ownerReferences"] = []any{ctrlRef("ReplicaSet", "web-rs", rsUID)}
		md["creationTimestamp"] = at(float64(sec)).Format(time.RFC3339)
		md["resourceVersion"] = "1"
		var cs, sts []any
		for _, c := range ctrs {
			cs = append(cs, map[string]any{"name": c})
			sts = append(sts, map[string]any{"name": c, "containerID": "cid-" + name + "-" + c, "restartCount": int64(0),
				"state": map[string]any{"running": map[string]any{"startedAt": at(float64(sec)).Format(time.RFC3339)}}})
		}
		o["spec"].(map[string]any)["containers"] = cs
		o["spec"].(map[string]any)["restartPolicy"] = "Always"
		o["status"].(map[string]any)["containerStatuses"] = sts
	})
}

type groupRun struct {
	sess   *session
	client *dynamicfake.FakeDynamicClient
	k      *fakeKubelet
	sink   *logRecorder
	done   chan error
	cancel context.CancelFunc
}

func startGroup(t *testing.T, client *dynamicfake.FakeDynamicClient, k *fakeKubelet, ref core.Ref, q provider.LogQuery) *groupRun {
	t.Helper()
	s := newSession("ctx", "h", client, false)
	s.logs = k.fetcher(t)
	g := &groupRun{sess: s, client: client, k: k, sink: newLogRecorder(), done: make(chan error, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	g.cancel = cancel
	go func() { g.done <- s.StreamLogs(ctx, ref, q, g.sink) }()
	t.Cleanup(func() {
		cancel()
		<-g.done
		s.Close()
	})
	return g
}

func webRef() core.Ref {
	return core.Ref{Provider: ProviderID, Target: "ctx", Scope: "ns", Kind: "apps/deployments", Name: "web", UID: "d1"}
}

func (g *groupRun) create(t *testing.T, gvr schema.GroupVersionResource, u *unstructured.Unstructured) {
	t.Helper()
	_, err := g.client.Resource(gvr).Namespace("ns").Create(context.Background(), u, metav1.CreateOptions{})
	require.NoError(t, err)
}

func (g *groupRun) linesOf(key string) []string {
	g.sink.mu.Lock()
	defer g.sink.mu.Unlock()
	var out []string
	for i, l := range g.sink.lines {
		if g.sink.lineSrc[i] == key {
			out = append(out, l.Text)
		}
	}
	return out
}

func TestMergeByTimeKeepsSourceOrder(t *testing.T) {
	ts := func(s float64) string { return at(s).Format(time.RFC3339Nano) }
	a := []provider.LogLine{{TS: ts(1), Text: "a1"}, {Text: "a1 continued", Flags: provider.LineNoTime}, {TS: ts(5), Text: "a5"}, {TS: ts(3), Text: "a3 (clock went back)"}}
	b := []provider.LogLine{{TS: ts(1), Text: "b1"}, {TS: ts(2), Text: "b2"}, {TS: ts(4), Text: "b4"}}
	var got []string
	for _, m := range mergeByTime([][]provider.LogLine{a, b}) {
		got = append(got, m.line.Text)
	}
	assert.Equal(t, []string{"a1", "a1 continued", "b1", "b2", "b4", "a5", "a3 (clock went back)"}, got)
}

// A deployment's pods by controller UID (through its ReplicaSet): the
// newest N lines overall, in time order, then live lines of every pod.
func TestGroupBacklogMergesAndFollows(t *testing.T) {
	k := newFakeKubelet(t)
	for _, p := range []string{"web-a", "web-b", "other"} {
		k.start(p, "app", "cid-"+p+"-app")
	}
	k.log("web-a", "app", fakeRec{ts: at(1), text: "a1"}, fakeRec{ts: at(3), text: "a3"}, fakeRec{ts: at(5), text: "a5"})
	k.log("web-b", "app", fakeRec{ts: at(2), text: "b2"}, fakeRec{ts: at(4), text: "b4"})
	k.log("other", "app", fakeRec{ts: at(4.5), text: "not ours"})
	client := groupClient(deployment("web", "d1", "app"), replicaSet("web-rs", "rs1", "d1"),
		memberOf("web-a", "pa", "rs1", 10, "app"), memberOf("web-b", "pb", "rs1", 20, "app"),
		pod("ns", "other", "po")) // no owner
	g := startGroup(t, client, k, webRef(), provider.LogQuery{Follow: true, TailLines: 4})

	g.sink.await(t, "ready", func() bool { g.sink.mu.Lock(); defer g.sink.mu.Unlock(); return g.sink.ready })
	assert.Equal(t, []string{"b2", "a3", "b4", "a5"}, g.sink.texts(), "the newest 4 of both pods, by time")
	assert.Equal(t, []string{"pb/app", "pa/app"}, g.sink.sources, "newest pod first")
	assert.Equal(t, 4, g.sink.readyAt, "the backlog comes before ready")

	k.log("web-a", "app", fakeRec{ts: at(6), text: "a6"})
	k.log("web-b", "app", fakeRec{ts: at(7), text: "b7"})
	g.sink.await(t, "live lines", func() bool { return len(g.sink.texts()) == 6 })
	assert.ElementsMatch(t, []string{"a6", "b7"}, g.sink.texts()[4:])
	assert.Equal(t, []string{"a1", "a3", "a5", "a6"}[1:], g.linesOf("pa/app"), "a1 was older than the window and is not replayed live")
}

// A pod that joins later (rollout, scale-up, or a pod seen late): its lines
// from the start of its container, even those written before it was seen.
func TestGroupPicksUpNewPodsFromTheirStart(t *testing.T) {
	k := newFakeKubelet(t)
	k.start("web-a", "app", "cid-web-a-app")
	k.log("web-a", "app", fakeRec{ts: at(1), text: "a1"})
	client := groupClient(deployment("web", "d1", "app"), replicaSet("web-rs", "rs1", "d1"), memberOf("web-a", "pa", "rs1", 10, "app"))
	g := startGroup(t, client, k, webRef(), provider.LogQuery{Follow: true, TailLines: 1})
	g.sink.await(t, "a1", func() bool { return len(g.sink.texts()) == 1 })

	k.start("web-c", "app", "cid-web-c-app")
	k.log("web-c", "app", fakeRec{ts: at(30), text: "c boot"}, fakeRec{ts: at(31), text: "c crashed early"})
	g.create(t, podGVR, memberOf("web-c", "pc", "rs1", 29, "app"))
	g.sink.await(t, "the new pod's startup lines", func() bool { return len(g.linesOf("pc/app")) == 2 })
	assert.Equal(t, []string{"c boot", "c crashed early"}, g.linesOf("pc/app"), "not only lines after discovery, and not the user's tail of 1")

	// deleted → its source ends
	k.end("web-c", "app")
	require.NoError(t, client.Resource(podGVR).Namespace("ns").Delete(context.Background(), "web-c", metav1.DeleteOptions{}))
	g.sink.await(t, "the source ended", func() bool { return g.sink.state("pc/app").State == provider.LogEnded })
}

func TestGroupPodBeforeItsReplicaSetAndScaleFromZero(t *testing.T) {
	k := newFakeKubelet(t)
	client := groupClient(deployment("web", "d1", "app"))
	g := startGroup(t, client, k, webRef(), provider.LogQuery{Follow: true, TailLines: 10})
	g.sink.await(t, "ready with no pods", func() bool { g.sink.mu.Lock(); defer g.sink.mu.Unlock(); return g.sink.ready })
	assert.Zero(t, g.sink.sourceCount())

	k.start("web-a", "app", "cid-web-a-app")
	k.log("web-a", "app", fakeRec{ts: at(1), text: "hello"})
	g.create(t, podGVR, memberOf("web-a", "pa", "rs1", 10, "app")) // its ReplicaSet is not known yet
	time.Sleep(200 * time.Millisecond)
	assert.Zero(t, g.sink.sourceCount(), "not a member until its owner chain is known")
	g.create(t, rsGVR, replicaSet("web-rs", "rs1", "d1"))
	g.sink.await(t, "hello", func() bool { return len(g.linesOf("pa/app")) == 1 })
}

// At most 20 container streams; the newest pods win, the stream says how
// many are hidden, and a freed slot is refilled.
func TestGroupCapsContainerStreamsAndRefills(t *testing.T) {
	k := newFakeKubelet(t)
	objs := []runtime.Object{deployment("web", "d1", "app", "side"), replicaSet("web-rs", "rs1", "d1")}
	for i := range 11 {
		name := fmt.Sprintf("web-%02d", i)
		for _, c := range []string{"app", "side"} {
			k.start(name, c, "cid-"+name+"-"+c)
			k.log(name, c, fakeRec{ts: at(float64(i)), text: name + " " + c})
		}
		objs = append(objs, memberOf(name, fmt.Sprintf("p%02d", i), "rs1", 100+i, "app", "side"))
	}
	client := groupClient(objs...)
	g := startGroup(t, client, k, webRef(), provider.LogQuery{Channel: provider.ChannelAll, Follow: true, TailLines: 100})
	g.sink.await(t, "ready", func() bool { g.sink.mu.Lock(); defer g.sink.mu.Unlock(); return g.sink.ready })
	assert.Equal(t, maxGroupStreams, g.sink.sourceCount())
	assert.Equal(t, provider.LogState{State: provider.LogLimited, Message: "showing 20 of 22 container streams (at most 20 at once)"}, g.sink.state(""))
	g.sink.mu.Lock()
	for _, key := range g.sink.sources {
		assert.NotContains(t, key, "p00/", "the oldest pod waits")
	}
	g.sink.mu.Unlock()

	k.end("web-10", "app")
	k.end("web-10", "side")
	require.NoError(t, client.Resource(podGVR).Namespace("ns").Delete(context.Background(), "web-10", metav1.DeleteOptions{}))
	g.sink.await(t, "the oldest pod admitted", func() bool { return len(g.linesOf("p00/app")) == 1 && len(g.linesOf("p00/side")) == 1 })
	g.sink.await(t, "no longer limited", func() bool { return g.sink.state("").State == provider.LogStreaming })
}

func TestGroupSlowBacklogDoesNotHoldOthers(t *testing.T) {
	old := backlogTimeout
	backlogTimeout = 300 * time.Millisecond
	t.Cleanup(func() { backlogTimeout = old })
	k := newFakeKubelet(t)
	k.start("web-a", "app", "cid-web-a-app")
	k.start("web-b", "app", "cid-web-b-app")
	k.log("web-a", "app", fakeRec{ts: at(1), text: "fast"})
	k.log("web-b", "app", fakeRec{ts: at(2), text: "slow"})
	k.slow["web-b"] = 3 * time.Second
	client := groupClient(deployment("web", "d1", "app"), replicaSet("web-rs", "rs1", "d1"),
		memberOf("web-a", "pa", "rs1", 10, "app"), memberOf("web-b", "pb", "rs1", 20, "app"))
	start := time.Now()
	g := startGroup(t, client, k, webRef(), provider.LogQuery{Follow: true, TailLines: 10})
	g.sink.await(t, "ready", func() bool { g.sink.mu.Lock(); defer g.sink.mu.Unlock(); return g.sink.ready })
	assert.Less(t, time.Since(start), 2*time.Second)
	assert.Equal(t, []string{"fast"}, g.sink.texts())
	assert.Contains(t, g.sink.state("pb/app").Message, "did not load in time")
	g.sink.await(t, "the slow one live", func() bool { return len(g.linesOf("pb/app")) == 1 })
}

// All containers of one pod: the same group machinery; a same-named
// replacement is not followed.
func TestAllContainersOfAPod(t *testing.T) {
	k := newFakeKubelet(t)
	for _, c := range []string{"app", "side"} {
		k.start("web-a", c, "cid-web-a-"+c)
	}
	k.log("web-a", "app", fakeRec{ts: at(1), text: "from app"})
	k.log("web-a", "side", fakeRec{ts: at(2), text: "from side"})
	client := groupClient(memberOf("web-a", "pa", "rs1", 10, "app", "side"), memberOf("web-b", "pb", "rs1", 10, "app"))
	ref := core.Ref{Provider: ProviderID, Target: "ctx", Scope: "ns", Kind: "pods", Name: "web-a", UID: "pa"}
	g := startGroup(t, client, k, ref, provider.LogQuery{Channel: provider.ChannelAll, Follow: true, TailLines: 10})
	g.sink.await(t, "both", func() bool { return len(g.sink.texts()) == 2 })
	assert.Equal(t, []string{"from app", "from side"}, g.sink.texts())
	assert.Equal(t, []string{"pa/app", "pa/side"}, g.sink.sources)
}

// The app-wide cap on open log requests: the second stream waits (and says
// so) until the first closes.
func TestLogRequestSlotsAreShared(t *testing.T) {
	k := newFakeKubelet(t)
	k.start("p", "app", "c1")
	k.log("p", "app", fakeRec{ts: at(1), text: "x"})
	slots := make(chan struct{}, 1)
	mk := func() (*logRecorder, context.CancelFunc, chan error) {
		sink := newLogRecorder()
		src := newPodSource(1, "ns", "p", "uid-1", "app", k.fetcher(t), syncedBox(runningObs("uid-1", "c1")), sink, follow100)
		src.slots = slots
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- src.run(ctx) }()
		t.Cleanup(func() { cancel(); <-done })
		return sink, cancel, done
	}
	first, cancelFirst, firstDone := mk()
	first.await(t, "x", func() bool { return len(first.texts()) == 1 })
	second, _, _ := mk()
	second.await(t, "waiting for a slot", func() bool { return second.lastState().State == provider.LogWaiting })
	assert.Contains(t, second.lastState().Message, "1 log streams are open at once")
	cancelFirst()
	<-firstDone
	firstDone <- nil
	second.await(t, "x", func() bool { return len(second.texts()) == 1 })
}
