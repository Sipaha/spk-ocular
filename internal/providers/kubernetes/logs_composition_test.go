package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"io"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	clienttesting "k8s.io/client-go/testing"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestComposedLogsOverlapReadsOneSourceAndKeepsRootCoverage(t *testing.T) {
	k := newFakeKubelet(t)
	p := memberOf("web-a", "pa", "rs1", 1, "app")
	client := groupClient(deployment("web", "d1", "app"), replicaSet("web-rs", "rs1", "d1"), p)
	s := newSession("ctx", "h", client, false)
	s.logs = k.fetcher(t)
	defer s.Close()
	k.start("web-a", "app", "cid-web-a-app")
	k.log("web-a", "app", fakeRec{ts: at(2), text: "overlap"})
	refs := []core.Ref{webRef(), {Provider: ProviderID, Target: "ctx", Kind: "pods", Scope: "ns", Name: "web-a", UID: "pa"}}
	sink := newLogRecorder()
	require.NoError(t, s.StreamComposedLogs(context.Background(), refs, provider.LogQuery{Channel: provider.ChannelAll, TailLines: 100}, sink))
	require.Equal(t, []string{"overlap"}, sink.texts())
	require.Equal(t, provider.LogStreaming, sink.state("root/ns/d1").State)
	require.Equal(t, provider.LogStreaming, sink.state("root/ns/pa").State)
	require.Equal(t, []string{"ns/pa/app"}, sink.lineSrc)
	k.mu.Lock()
	defer k.mu.Unlock()
	require.Len(t, k.requests, 1)
}

func TestComposedUnionRemovingWorkloadMembershipDoesNotEndExplicitPod(t *testing.T) {
	p := memberOf("web-a", "pa", "rs1", 1, "app")
	client := groupClient(deployment("web", "d1", "app"), replicaSet("web-rs", "rs1", "d1"), p)
	s := newSession("ctx", "h", client, false)
	defer s.Close()
	refs := []core.Ref{webRef(), {Provider: ProviderID, Target: "ctx", Kind: "pods", Scope: "ns", Name: "web-a", UID: "pa"}}
	tr, err := s.trackUnion(context.Background(), refs)
	require.NoError(t, err)
	defer tr.stop(s)
	require.Eventually(t, func() bool { m, s, _, _ := tr.snapshot(); return s && len(m) == 1 }, 3*time.Second, 10*time.Millisecond)
	members, _, _, _ := tr.snapshot()
	box := members[0].box
	p.SetOwnerReferences(nil)
	p.SetResourceVersion("2")
	_, err = client.Resource(podGVR).Namespace("ns").Update(context.Background(), p, metav1.UpdateOptions{})
	require.NoError(t, err)
	require.Eventually(t, func() bool { m, _, _, _ := tr.snapshot(); return len(m) == 1 && m[0].rv == "2" }, 3*time.Second, 10*time.Millisecond)
	members, _, _, _ = tr.snapshot()
	require.Same(t, box, members[0].box)
	obs, _, _, _ := box.get()
	require.True(t, obs.exists)
	require.Equal(t, provider.LogWaiting, tr.coverage()[0].State)
	require.Equal(t, provider.LogStreaming, tr.coverage()[1].State)
	// A same-name replacement must never inherit the explicitly selected UID.
	require.NoError(t, client.Resource(podGVR).Namespace("ns").Delete(context.Background(), p.GetName(), metav1.DeleteOptions{}))
	p.SetUID("replacement")
	p.SetResourceVersion("3")
	_, err = client.Resource(podGVR).Namespace("ns").Create(context.Background(), p, metav1.CreateOptions{})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		m, _, _, _ := tr.snapshot()
		return len(m) == 0 && tr.coverage()[1].Class == provider.ClassGone
	}, 3*time.Second, 10*time.Millisecond)
	obs, _, _, _ = box.get()
	require.False(t, obs.exists)
}

func TestComposedLogsDeniedRootDoesNotHidePermittedPod(t *testing.T) {
	k := newFakeKubelet(t)
	p := memberOf("web-a", "pa", "rs1", 1, "app")
	client := groupClient(p)
	client.PrependReactor("get", "deployments", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(deployGVR.GroupResource(), "web", errors.New(strings.Repeat("界", 5000)))
	})
	s := newSession("ctx", "h", client, false)
	s.logs = k.fetcher(t)
	defer s.Close()
	k.start("web-a", "app", "cid-web-a-app")
	k.log("web-a", "app", fakeRec{ts: at(2), text: "available"})
	refs := []core.Ref{webRef(), {Provider: ProviderID, Target: "ctx", Kind: "pods", Scope: "ns", Name: "web-a", UID: "pa"}}
	sink := newLogRecorder()
	require.NoError(t, s.StreamComposedLogs(context.Background(), refs, provider.LogQuery{Channel: provider.ChannelAll, TailLines: 100}, sink))
	require.Equal(t, []string{"available"}, sink.texts())
	require.Equal(t, provider.LogError, sink.state("root/ns/d1").State)
	require.Equal(t, provider.ClassForbidden, sink.state("root/ns/d1").Class)
	require.LessOrEqual(t, len(sink.state("root/ns/d1").Message), 4096)
	require.True(t, utf8.ValidString(sink.state("root/ns/d1").Message))
	require.Equal(t, provider.LogStreaming, sink.state("root/ns/pa").State)
}

func TestComposedLogsNamespacesSurviveActualHTTPAndShareNoSourceKey(t *testing.T) {
	p1 := memberOf("same", "uid-blue", "none", 1, "app")
	p1.SetNamespace("blue")
	p2 := memberOf("same", "uid-green", "none", 1, "app")
	p2.SetNamespace("green")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/namespaces/blue/pods/same/log":
			_, _ = fmt.Fprintln(w, "2026-10-22T00:00:00Z blue")
		case "/api/v1/namespaces/green/pods/same/log":
			_, _ = fmt.Fprintln(w, "2026-10-22T00:00:00Z green")
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	s := newSession("ctx", "h", groupClient(p1, p2), false)
	defer s.Close()
	var err error
	s.logs, err = httpLogFetcher(&rest.Config{Host: server.URL})
	require.NoError(t, err)
	refs := []core.Ref{{Provider: ProviderID, Target: "ctx", Kind: "pods", Scope: "blue", Name: "same", UID: "uid-blue"}, {Provider: ProviderID, Target: "ctx", Kind: "pods", Scope: "green", Name: "same", UID: "uid-green"}}
	sink := newLogRecorder()
	require.NoError(t, s.StreamComposedLogs(t.Context(), refs, provider.LogQuery{Channel: "*", TailLines: 100}, sink))
	require.ElementsMatch(t, []string{"blue", "green"}, sink.texts())
	require.ElementsMatch(t, []string{"blue/uid-blue/app", "green/uid-green/app"}, sink.lineSrc)
	for _, action := range s.dyn.(*dynamicfake.FakeDynamicClient).Actions() {
		if action.GetVerb() == "list" && action.GetResource() == podGVR {
			list := action.(clienttesting.ListAction)
			require.NotEmpty(t, list.GetListRestrictions().Fields.String(), "Pod-only compositions must never list a whole namespace")
		}
	}
}

func TestComposedFilteredRequestCapReservesFiniteHistoryAndReleasesOnCancel(t *testing.T) {
	var active, maxActive atomic.Int32
	fetch := composedLogFetcher(func(_ context.Context, _ podLogRequest) (io.ReadCloser, error) {
		n := active.Add(1)
		for {
			old := maxActive.Load()
			if n <= old || maxActive.CompareAndSwap(old, n) {
				break
			}
		}
		return &compositionTestBody{Reader: strings.NewReader(""), close: func() { active.Add(-1) }}, nil
	}, true, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var bodies []io.ReadCloser
	for range maxGroupStreams - backlogWorkers {
		body, err := fetch(ctx, podLogRequest{Follow: true})
		require.NoError(t, err)
		bodies = append(bodies, body)
	}
	done := make(chan error, 1)
	go func() {
		body, err := fetch(ctx, podLogRequest{Follow: true})
		if body != nil {
			_ = body.Close()
		}
		done <- err
	}()
	for range backlogWorkers {
		body, err := fetch(ctx, podLogRequest{})
		require.NoError(t, err)
		bodies = append(bodies, body)
	}
	require.Equal(t, int32(maxGroupStreams), active.Load())
	require.Equal(t, int32(maxGroupStreams), maxActive.Load())
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	for _, body := range bodies {
		require.NoError(t, body.Close())
		require.NoError(t, body.Close())
	}
	require.Zero(t, active.Load())
}

type compositionTestBody struct {
	io.Reader
	close func()
	once  sync.Once
}

func (b *compositionTestBody) Close() error { b.once.Do(b.close); return nil }

func TestComposedQuietHTTPReadersCancelAndReleaseEveryLease(t *testing.T) {
	p1 := memberOf("one", "uid-one", "none", 1, "app")
	p2 := memberOf("two", "uid-two", "none", 1, "app")
	var active atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		active.Add(1)
		defer active.Add(-1)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	s := newSession("ctx", "h", groupClient(p1, p2), false)
	defer s.Close()
	var err error
	s.logs, err = httpLogFetcher(&rest.Config{Host: server.URL})
	require.NoError(t, err)
	refs := []core.Ref{{Provider: ProviderID, Target: "ctx", Kind: "pods", Scope: "ns", Name: "one", UID: "uid-one"}, {Provider: ProviderID, Target: "ctx", Kind: "pods", Scope: "ns", Name: "two", UID: "uid-two"}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- s.StreamComposedLogs(ctx, refs, provider.LogQuery{Channel: "*", TailLines: 100, Follow: true, LiveOnly: true}, newLogRecorder())
	}()
	require.Eventually(t, func() bool { return active.Load() == 2 }, 3*time.Second, 10*time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("composed readers did not join")
	}
	require.Eventually(t, func() bool { return active.Load() == 0 }, 3*time.Second, 10*time.Millisecond)
	leased, _ := s.caches.stats()
	require.Zero(t, leased)
}

func TestComposedRootsShareOneGroupAdmissionLimit(t *testing.T) {
	k := newFakeKubelet(t)
	objs := []runtime.Object{deployment("web", "d1", "app"), deployment("other", "d2", "app"), replicaSet("web-rs", "rs1", "d1"), replicaSet("other-rs", "rs2", "d2")}
	for i := range 33 {
		name := fmt.Sprintf("pod-%02d", i)
		owner := "rs1"
		if i%2 == 1 {
			owner = "rs2"
		}
		objs = append(objs, memberOf(name, "uid-"+name, owner, i, "app"))
		k.start(name, "app", "cid-"+name+"-app")
		k.log(name, "app", fakeRec{ts: at(float64(i + 1)), text: name})
	}
	s := newSession("ctx", "h", groupClient(objs...), false)
	s.logs = k.fetcher(t)
	defer s.Close()
	refs := []core.Ref{webRef(), {Provider: ProviderID, Target: "ctx", Kind: "apps/deployments", Scope: "ns", Name: "other", UID: "d2"}}
	sink := newLogRecorder()
	require.NoError(t, s.StreamComposedLogs(t.Context(), refs, provider.LogQuery{Channel: "*", TailLines: 100}, sink))
	require.Len(t, sink.texts(), maxGroupStreams)
	require.Equal(t, maxGroupStreams+len(refs), sink.sourceCount())
	require.Equal(t, provider.LogLimited, sink.state("").State)
	k.mu.Lock()
	defer k.mu.Unlock()
	require.Len(t, k.requests, maxGroupStreams)
}

func TestComposedHistoryReservationEndsWithoutReducingLiveCapacity(t *testing.T) {
	historyDone := make(chan struct{})
	var active, maxActive atomic.Int32
	fetch := composedLogFetcher(func(context.Context, podLogRequest) (io.ReadCloser, error) {
		n := active.Add(1)
		for {
			old := maxActive.Load()
			if n <= old || maxActive.CompareAndSwap(old, n) {
				break
			}
		}
		return &compositionTestBody{Reader: strings.NewReader(""), close: func() { active.Add(-1) }}, nil
	}, true, historyDone)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var bodies []io.ReadCloser
	for range maxGroupStreams - backlogWorkers {
		b, err := fetch(ctx, podLogRequest{Follow: true})
		require.NoError(t, err)
		bodies = append(bodies, b)
	}
	close(historyDone)
	for range backlogWorkers {
		b, err := fetch(ctx, podLogRequest{Follow: true})
		require.NoError(t, err)
		bodies = append(bodies, b)
	}
	require.Equal(t, int32(maxGroupStreams), active.Load())
	require.Equal(t, int32(maxGroupStreams), maxActive.Load())
	done := make(chan error, 1)
	go func() {
		b, err := fetch(ctx, podLogRequest{Follow: true})
		if b != nil {
			_ = b.Close()
		}
		done <- err
	}()
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	for _, b := range bodies {
		require.NoError(t, b.Close())
	}
	require.Zero(t, active.Load())
}

func TestComposedNamespaceListDenialDoesNotHideExplicitNamedPod(t *testing.T) {
	k := newFakeKubelet(t)
	p := memberOf("web-a", "pa", "rs1", 1, "app")
	client := groupClient(deployment("web", "d1", "app"), replicaSet("web-rs", "rs1", "d1"), p)
	// Kubernetes resourceNames permissions may allow the named Pod cache while
	// rejecting the workload's whole-namespace collection.
	client.PrependReactor("list", "pods", func(action clienttesting.Action) (bool, runtime.Object, error) {
		if action.(clienttesting.ListAction).GetListRestrictions().Fields.Empty() {
			return true, nil, apierrors.NewForbidden(podGVR.GroupResource(), "", errors.New("namespace list denied; named Pod allowed"))
		}
		return false, nil, nil
	})
	s := newSession("ctx", "h", client, false)
	s.logs = k.fetcher(t)
	defer s.Close()
	k.start("web-a", "app", "cid-web-a-app")
	k.log("web-a", "app", fakeRec{ts: at(2), text: "named Pod remains available"})
	refs := []core.Ref{webRef(), {Provider: ProviderID, Target: "ctx", Kind: "pods", Scope: "ns", Name: "web-a", UID: "pa"}}
	sink := newLogRecorder()
	require.NoError(t, s.StreamComposedLogs(t.Context(), refs, provider.LogQuery{Channel: "*", TailLines: 100}, sink))
	require.Equal(t, []string{"named Pod remains available"}, sink.texts())
	require.Equal(t, provider.LogError, sink.state("root/ns/d1").State)
	require.Equal(t, provider.LogStreaming, sink.state("root/ns/pa").State)
	k.mu.Lock()
	defer k.mu.Unlock()
	require.Len(t, k.requests, 1)
}

func TestComposedOverlappingCachesFeedOneStableObservation(t *testing.T) {
	for _, podFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(podFirst), func(t *testing.T) {
			p := memberOf("web-a", "pa", "rs1", 1, "app")
			s := newSession("ctx", "h", groupClient(deployment("web", "d1", "app"), replicaSet("web-rs", "rs1", "d1"), p), false)
			defer s.Close()
			refs := []core.Ref{webRef(), {Provider: ProviderID, Target: "ctx", Kind: "pods", Scope: "ns", Name: "web-a", UID: "pa"}}
			if podFirst {
				refs[0], refs[1] = refs[1], refs[0]
			}
			tr, err := s.trackUnion(t.Context(), refs)
			require.NoError(t, err)
			defer tr.stop(s)
			require.Eventually(t, func() bool { m, ready, _, _ := tr.snapshot(); return ready && len(m) == 1 }, 3*time.Second, 10*time.Millisecond)
			var broad, narrow *unionLease
			for _, r := range tr.roots {
				if r.def == podsKind {
					narrow = r.pods
				} else {
					broad = r.pods
				}
			}
			require.NotSame(t, broad, narrow)
			current, stale := p.DeepCopy(), p.DeepCopy()
			current.SetResourceVersion("opaque-current")
			stale.SetResourceVersion("opaque-stale")
			stale.Object["status"].(map[string]any)["containerStatuses"].([]any)[0].(map[string]any)["containerID"] = "stale-container"
			require.NoError(t, broad.cache.inf.GetStore().Update(current))
			require.NoError(t, narrow.cache.inf.GetStore().Update(stale))
			tr.recompute()
			m, _, _, _ := tr.snapshot()
			require.Equal(t, "opaque-current", m[0].rv)
			obs, _, _, changed := m[0].box.get()
			require.Equal(t, "cid-web-a-app", obs.ctrs["app"].id)
			tr.recompute()
			select {
			case <-changed:
				t.Fatal("unchanged membership re-applied a stale overlapping root observation")
			default:
			}
		})
	}
}

func TestComposedUIDDisagreementWaitsThenAdmitsOnlyTheReplacement(t *testing.T) {
	k := newFakeKubelet(t)
	p := memberOf("web-a", "pa", "rs1", 1, "app")
	s := newSession("ctx", "h", groupClient(deployment("web", "d1", "app"), replicaSet("web-rs", "rs1", "d1"), p), false)
	s.logs = k.fetcher(t)
	defer s.Close()
	refs := []core.Ref{webRef(), {Provider: ProviderID, Target: "ctx", Kind: "pods", Scope: "ns", Name: "web-a", UID: "pa"}}
	tr, err := s.trackUnion(t.Context(), refs)
	require.NoError(t, err)
	defer tr.stop(s)
	require.Eventually(t, func() bool { m, ready, _, _ := tr.snapshot(); return ready && len(m) == 1 }, 3*time.Second, 10*time.Millisecond)
	initial, _, _, _ := tr.snapshot()
	oldBox := initial[0].box
	replacement := memberOf("web-a", "pb", "rs1", 2, "app")
	replacement.SetResourceVersion("replacement-opaque")
	require.NoError(t, tr.roots[0].pods.cache.inf.GetStore().Update(replacement))
	tr.recompute()
	m, _, _, _ := tr.snapshot()
	require.Len(t, m, 2)
	for _, pod := range m {
		require.True(t, pod.conflicted)
		require.Empty(t, pod.ctrs)
		_, _, blind, _ := pod.box.get()
		require.Contains(t, blind, "identity")
	}
	for _, state := range tr.coverage() {
		require.Equal(t, provider.ClassUnavailable, state.Class)
	}
	run := func(sink *logRecorder) {
		for i, r := range refs {
			require.NoError(t, sink.Source(i+1, "root/"+r.UID, r.Name, ""))
		}
		open := composedLogOpener(s.logs, false, nil)
		group := &logGroup{s: s, tr: tr, q: provider.LogQuery{Channel: "*", TailLines: 100}, sink: &lockedSink{sink: sink}, composed: true, nextID: len(refs), channel: channelsFor("*", ""), fetchMember: func(m memberPod) logFetcher {
			return func(ctx context.Context, r podLogRequest) (io.ReadCloser, error) {
				return open(ctx, r, tr.memberCheck(m))
			}
		}}
		require.NoError(t, group.run(t.Context()))
	}
	blocked := newLogRecorder()
	run(blocked)
	require.Empty(t, blocked.texts())
	k.mu.Lock()
	require.Empty(t, k.requests)
	k.mu.Unlock()
	require.NoError(t, tr.roots[1].pods.cache.inf.GetStore().Update(replacement))
	tr.recompute()
	k.start("web-a", "app", "new-instance")
	k.log("web-a", "app", fakeRec{ts: at(3), text: "replacement UID only"})
	sink := newLogRecorder()
	run(sink)
	require.Equal(t, []string{"replacement UID only"}, sink.texts())
	require.Equal(t, []string{"ns/pb/app"}, sink.lineSrc)
	require.Equal(t, provider.ClassGone, tr.coverage()[1].Class)
	oldObs, oldSynced, oldBlind, _ := oldBox.get()
	require.True(t, oldSynced)
	require.False(t, oldObs.exists)
	require.Empty(t, oldBlind, "confirmed departure must end, not retain an orphaned uncertain source")
}

func TestComposedQueuedOpenRechecksIdentityAfterAdmission(t *testing.T) {
	p := memberOf("web-a", "pa", "rs1", 1, "app")
	s := newSession("ctx", "h", groupClient(deployment("web", "d1", "app"), replicaSet("web-rs", "rs1", "d1"), p), false)
	defer s.Close()
	tr, err := s.trackUnion(t.Context(), []core.Ref{webRef(), {Provider: ProviderID, Target: "ctx", Kind: "pods", Scope: "ns", Name: "web-a", UID: "pa"}})
	require.NoError(t, err)
	defer tr.stop(s)
	require.Eventually(t, func() bool { m, ready, _, _ := tr.snapshot(); return ready && len(m) == 1 }, 3*time.Second, 10*time.Millisecond)
	m, _, _, _ := tr.snapshot()
	var calls atomic.Int32
	open := composedLogOpener(func(context.Context, podLogRequest) (io.ReadCloser, error) {
		calls.Add(1)
		return io.NopCloser(strings.NewReader("")), nil
	}, false, nil)
	var bodies []io.ReadCloser
	for range maxGroupStreams {
		body, err := open(t.Context(), podLogRequest{}, nil)
		require.NoError(t, err)
		bodies = append(bodies, body)
	}
	done := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		body, err := open(t.Context(), podLogRequest{}, tr.memberCheck(m[0]))
		if body != nil {
			_ = body.Close()
		}
		done <- err
	}()
	<-started
	replacement := p.DeepCopy()
	replacement.SetUID("pb")
	require.NoError(t, tr.roots[0].pods.cache.inf.GetStore().Update(replacement))
	require.NoError(t, bodies[0].Close())
	err = <-done
	require.Error(t, err)
	var pe *provider.Error
	require.ErrorAs(t, err, &pe)
	require.Equal(t, provider.ClassUnavailable, pe.Class)
	require.Equal(t, int32(maxGroupStreams), calls.Load())
	for _, body := range bodies {
		require.NoError(t, body.Close())
	}
}

func TestComposedMalformedChildUIDDoesNotHideValidChildren(t *testing.T) {
	k := newFakeKubelet(t)
	good := memberOf("web-a", "pa", "rs1", 1, "app")
	bad := memberOf("bad", "", "rs1", 1, "app")
	s := newSession("ctx", "h", groupClient(deployment("web", "d1", "app"), replicaSet("web-rs", "rs1", "d1"), good, bad), false)
	s.logs = k.fetcher(t)
	defer s.Close()
	k.start("web-a", "app", "cid-web-a-app")
	k.log("web-a", "app", fakeRec{ts: at(2), text: "verified child UID"})
	sink := newLogRecorder()
	refs := []core.Ref{webRef(), {Provider: ProviderID, Target: "ctx", Kind: "apps/replicasets", Scope: "ns", Name: "web-rs", UID: "rs1"}}
	require.NoError(t, s.StreamComposedLogs(t.Context(), refs, provider.LogQuery{Channel: "*", TailLines: 100}, sink))
	require.Equal(t, []string{"verified child UID"}, sink.texts())
	require.Equal(t, provider.LogError, sink.state("root/ns/d1").State)
	k.mu.Lock()
	defer k.mu.Unlock()
	require.Len(t, k.requests, 1)
}
