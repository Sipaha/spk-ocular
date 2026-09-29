package kubernetes

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/spk/spk-ocular/internal/provider"
)

// failingSink fails announcing source failID; records the largest Lines batch.
type failingSink struct {
	*logRecorder
	failID   int
	batchMu  sync.Mutex
	maxBatch int
}

var errPageGone = errors.New("page gone")

func (f *failingSink) Source(id int, key, label, ch string) error {
	if id == f.failID {
		return errPageGone
	}
	return f.logRecorder.Source(id, key, label, ch)
}

func (f *failingSink) Lines(id int, l []provider.LogLine) error {
	n := 0
	for _, x := range l {
		n += len(x.Text) + len(x.TS)
	}
	f.batchMu.Lock()
	f.maxBatch = max(f.maxBatch, n)
	f.batchMu.Unlock()
	return f.logRecorder.Lines(id, l)
}

// Review P1-1: a failure announcing a late source ends the group even while
// another source waits on a quiet pod (cancel before join).
func TestGroupEndsWhenAnnouncingALateSourceFails(t *testing.T) {
	k := newFakeKubelet(t)
	k.start("web-a", "app", "cid-web-a-app")
	client := groupClient(deployment("web", "d1", "app"), replicaSet("web-rs", "rs1", "d1"), memberOf("web-a", "pa", "rs1", 10, "app"))
	s := newSession("ctx", "h", client, false)
	t.Cleanup(s.Close)
	s.logs = k.fetcher(t)
	sink := &failingSink{logRecorder: newLogRecorder(), failID: 2}
	done := make(chan error, 1)
	go func() {
		done <- s.StreamLogs(context.Background(), webRef(), provider.LogQuery{Follow: true, TailLines: 10}, sink)
	}()
	sink.await(t, "ready", func() bool { sink.mu.Lock(); defer sink.mu.Unlock(); return sink.ready })
	_, err := client.Resource(podGVR).Namespace("ns").Create(context.Background(), memberOf("web-b", "pb", "rs1", 20, "app"), metav1.CreateOptions{})
	require.NoError(t, err)
	select {
	case err := <-done:
		assert.ErrorIs(t, err, errPageGone)
	case <-time.After(5 * time.Second):
		t.Fatal("the group did not end: it joined its sources before cancelling them")
	}
}

// Review P1-3: a pod that joins while the backlog is read is admitted even
// if nothing else changes afterwards.
func TestGroupAdmitsAPodThatAppearedDuringTheBacklog(t *testing.T) {
	k := newFakeKubelet(t)
	k.start("web-a", "app", "cid-web-a-app")
	k.start("web-b", "app", "cid-web-b-app")
	k.log("web-b", "app", fakeRec{ts: at(5), text: "b line"})
	k.slow["web-a"] = 600 * time.Millisecond
	client := groupClient(deployment("web", "d1", "app"), replicaSet("web-rs", "rs1", "d1"), memberOf("web-a", "pa", "rs1", 10, "app"))
	g := startGroup(t, client, k, webRef(), provider.LogQuery{Follow: true, TailLines: 10})
	time.Sleep(200 * time.Millisecond) // the backlog of A is being read
	g.create(t, podGVR, memberOf("web-b", "pb", "rs1", 20, "app"))
	g.sink.await(t, "B admitted without a later event", func() bool { return len(g.linesOf("pb/app")) == 1 })
}

// Review P1-3: a new container of a member pod (ephemeral) is a new channel.
func TestGroupAdmitsANewContainerOfAPod(t *testing.T) {
	k := newFakeKubelet(t)
	k.start("web-a", "app", "cid-web-a-app")
	client := groupClient(memberOf("web-a", "pa", "rs1", 10, "app"))
	ref := webRef()
	ref.Kind, ref.Name, ref.UID = "pods", "web-a", "pa"
	g := startGroup(t, client, k, ref, provider.LogQuery{Channel: provider.ChannelAll, Follow: true, TailLines: 10})
	g.sink.await(t, "ready", func() bool { g.sink.mu.Lock(); defer g.sink.mu.Unlock(); return g.sink.ready })
	k.start("web-a", "debugger", "cid-dbg")
	k.log("web-a", "debugger", fakeRec{ts: at(9), text: "debugging"})
	updated := memberOf("web-a", "pa", "rs1", 10, "app")
	updated.Object["metadata"].(map[string]any)["resourceVersion"] = "2"
	updated.Object["spec"].(map[string]any)["ephemeralContainers"] = []any{map[string]any{"name": "debugger"}}
	updated.Object["status"].(map[string]any)["ephemeralContainerStatuses"] = []any{map[string]any{"name": "debugger", "containerID": "cid-dbg",
		"state": map[string]any{"running": map[string]any{"startedAt": at(8).Format(time.RFC3339)}}}}
	_, err := client.Resource(podGVR).Namespace("ns").Update(context.Background(), updated, metav1.UpdateOptions{})
	require.NoError(t, err)
	g.sink.await(t, "the ephemeral container's lines", func() bool { return len(g.linesOf("pa/debugger")) == 1 })
}

// Review P1-2: backlog batches are bounded by bytes, not only by lines.
func TestGroupBacklogBatchesAreByteBounded(t *testing.T) {
	k := newFakeKubelet(t)
	k.start("web-a", "app", "cid-web-a-app")
	long := strings.Repeat("x", 250<<10)
	for i := range 40 {
		k.log("web-a", "app", fakeRec{ts: at(float64(i)), text: long})
	}
	client := groupClient(deployment("web", "d1", "app"), replicaSet("web-rs", "rs1", "d1"), memberOf("web-a", "pa", "rs1", 10, "app"))
	s := newSession("ctx", "h", client, false)
	t.Cleanup(s.Close)
	s.logs = k.fetcher(t)
	sink := &failingSink{logRecorder: newLogRecorder()}
	require.NoError(t, s.StreamLogs(context.Background(), webRef(), provider.LogQuery{TailLines: 100}, sink))
	assert.Len(t, sink.texts(), 40)
	assert.LessOrEqual(t, sink.maxBatch, batchBytes+maxLineBytes, "one frame stays far below the client's limit")
}

// Review P1-4: an explicit channel (the UI always sends one) must not skip
// the UID check: a same-named replacement is never streamed as the pod the
// user clicked — previous or current, follow or not.
func TestExplicitChannelStillChecksTheUID(t *testing.T) {
	k := newFakeKubelet(t)
	k.start("p", "app", "c1")
	k.log("p", "app", fakeRec{ts: at(1), text: "the new pod"})
	s := newSession("ctx", "h", fakeClient(pod("ns", "p", "uid-new")), false)
	t.Cleanup(s.Close)
	s.logs = k.fetcher(t)
	ref := webRef()
	ref.Kind, ref.Name, ref.UID = "pods", "p", "uid-old"
	for _, q := range []provider.LogQuery{
		{Channel: "app", Previous: true, TailLines: 10},
		{Channel: "app", TailLines: 10},
		{Channel: "app", Follow: true, TailLines: 10},
	} {
		err := s.StreamLogs(context.Background(), ref, q, newLogRecorder())
		var pe *provider.Error
		require.ErrorAs(t, err, &pe, "%+v", q)
		assert.Equal(t, provider.ClassGone, pe.Class)
	}
	assert.Empty(t, k.requestLog(), "no request for the replacement's logs")
}

// Review P1-4: a group member replaced while the backlog queue waited is
// not read by name.
func TestGroupBacklogSkipsAMemberThatIsGone(t *testing.T) {
	k := newFakeKubelet(t)
	k.start("web-a", "app", "cid-web-a-app")
	m := memberPod{uid: "pa", name: "web-a", box: newObsBox()}
	m.box.set(podObs{}) // deleted meanwhile
	m.box.setSynced()
	s := newSession("ctx", "h", fakeClient(), false)
	t.Cleanup(s.Close)
	s.logs = k.fetcher(t)
	g := &logGroup{s: s, ns: "ns", q: provider.LogQuery{TailLines: 10}, sink: &lockedSink{sink: newLogRecorder()}}
	x := &groupSrc{key: "pa/app", pod: m, ctr: "app"}
	x.src = newPodSource(1, "ns", "web-a", "pa", "app", s.logs, m.box, g.sink, g.q)
	require.NoError(t, g.backlog(context.Background(), []*groupSrc{x}))
	assert.Empty(t, k.requestLog())
}

// fetcherOf serves canned readers per request kind.
type scriptedFetch struct {
	mu   sync.Mutex
	reqs []podLogRequest
	body func(r podLogRequest) io.ReadCloser
}

func (f *scriptedFetch) fetch(_ context.Context, r podLogRequest) (io.ReadCloser, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, r)
	f.mu.Unlock()
	return f.body(r), nil
}

func brokenAfter(text string) io.ReadCloser {
	return io.NopCloser(io.MultiReader(strings.NewReader(text), iotest.ErrReader(io.ErrUnexpectedEOF)))
}

// Review P2-5: a non-follow answer that breaks off is not "complete".
func TestOneShotReadFailureIsReported(t *testing.T) {
	f := &scriptedFetch{body: func(podLogRequest) io.ReadCloser {
		return brokenAfter(at(1).Format(time.RFC3339Nano) + " first\n" + at(2).Format(time.RFC3339Nano) + " cut in the mid")
	}}
	sink := newLogRecorder()
	src := newPodSource(1, "ns", "p", "u", "app", f.fetch, nil, sink, provider.LogQuery{Previous: true, TailLines: 10})
	require.NoError(t, src.run(context.Background()))
	assert.Equal(t, []string{"first"}, sink.texts(), "the broken fragment is not a line")
	assert.Equal(t, provider.LogError, sink.lastState().State)
	assert.Contains(t, sink.lastState().Message, "broke off")
}

// Review P2-11: a finite answer that stalls after its headers ends in time.
func TestOneShotStallEndsWithAnError(t *testing.T) {
	old := onceTimeout
	onceTimeout = 200 * time.Millisecond
	t.Cleanup(func() { onceTimeout = old })
	f := &scriptedFetch{body: func(podLogRequest) io.ReadCloser {
		pr, pw := io.Pipe()
		go func() { _, _ = pw.Write([]byte(at(1).Format(time.RFC3339Nano) + " one\n")) }() // then nothing
		return pr
	}}
	sink := newLogRecorder()
	src := newPodSource(1, "ns", "p", "u", "app", func(ctx context.Context, r podLogRequest) (io.ReadCloser, error) {
		rc, _ := f.fetch(ctx, r)
		go func() { <-ctx.Done(); _ = rc.Close() }()
		return rc, nil
	}, nil, sink, provider.LogQuery{TailLines: 10})
	start := time.Now()
	require.NoError(t, src.run(context.Background()))
	assert.Less(t, time.Since(start), 3*time.Second)
	assert.Equal(t, []string{"one"}, sink.texts())
	assert.Equal(t, provider.LogError, sink.lastState().State)
	assert.Contains(t, sink.lastState().Message, "did not complete")
}

// Review P2-11: an error response whose body stalls is classified in time.
func TestErrorBodyThatStallsIsBounded(t *testing.T) {
	old := errorBodyTimeout
	errorBodyTimeout = 200 * time.Millisecond
	t.Cleanup(func() { errorBodyTimeout = old })
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"kind":"Status","code":403,"message":"forbid`))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	fetch, err := httpLogFetcher(&rest.Config{Host: srv.URL})
	require.NoError(t, err)
	start := time.Now()
	_, err = fetch(context.Background(), podLogRequest{Namespace: "ns", Pod: "p"})
	require.Error(t, err)
	assert.Less(t, time.Since(start), 3*time.Second)
}

// Review P2-6: a native sidecar restarts whatever the pod's policy says.
func TestNativeSidecarRestartsUnderAnyPodPolicy(t *testing.T) {
	for _, policy := range []string{"Never", "OnFailure", "Always"} {
		o := podObs{exists: true, phase: "Running", restartPolicy: policy}
		assert.True(t, o.restartExpected(ctrObs{kind: ctrSidecar, exited: true, exitCode: 0}), policy)
	}
	done := podObs{exists: true, phase: "Succeeded", restartPolicy: "Never"}
	assert.False(t, done.restartExpected(ctrObs{kind: ctrSidecar}), "not after the pod finished")
}

// Review P2-5: lines of a backlog that broke off were shown; the follow
// that comes next resumes after them instead of replaying the initial tail.
func TestGroupPartialBacklogIsNotReplayed(t *testing.T) {
	f := &scriptedFetch{body: func(r podLogRequest) io.ReadCloser {
		if !r.Follow {
			return brokenAfter(at(1).Format(time.RFC3339Nano) + " one\n" + at(2).Format(time.RFC3339Nano) + " two\n")
		}
		pr, _ := io.Pipe()
		return pr
	}}
	box := syncedBox(runningObs("pa", "c1"))
	s := newSession("ctx", "h", fakeClient(), false)
	t.Cleanup(s.Close)
	s.logs = f.fetch
	g := &logGroup{s: s, ns: "ns", q: provider.LogQuery{Follow: true, TailLines: 10}, sink: &lockedSink{sink: newLogRecorder()}}
	x := &groupSrc{key: "pa/app", pod: memberPod{uid: "pa", name: "web-a", box: box}, ctr: "app"}
	x.src = newPodSource(1, "ns", "web-a", "pa", "app", s.logs, box, g.sink, g.q)
	require.NoError(t, g.backlog(context.Background(), []*groupSrc{x}))
	assert.True(t, x.src.opened, "what was shown counts")
	req, sk := x.src.plan(box.v.ctrs["app"], true)
	assert.Zero(t, req.TailLines, "no initial tail again")
	assert.Equal(t, at(2).Truncate(time.Second), req.SinceTime.UTC())
	require.NotNil(t, sk)
}
