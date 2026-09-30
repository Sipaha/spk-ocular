package kubernetes

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/views"
)

// In the background the idle caches are kept (switching back is instant,
// no LIST); only maxIdle bounds them. Back in the foreground, the grace
// counts from the return.
func TestBackgroundKeepsIdleCaches(t *testing.T) {
	client := fakeClient(pod("a", "p", "u1"))
	var lists atomic.Int32
	client.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		lists.Add(1)
		return false, nil, nil
	})
	h := newHarness(t, client)
	now := time.Unix(1000, 0)
	h.sess.caches.now = func() time.Time { return now }
	h.sess.caches.grace = time.Minute
	v := h.open("pods", core.ScopeSel{Mode: core.ScopeOne, Name: "a"})
	h.until(v, isReady)
	listed := lists.Load()

	h.sess.SetBackground(true, func() {})
	h.m.Close(v) // the page went away
	now = now.Add(time.Hour)
	h.sess.caches.mu.Lock()
	h.sess.caches.evictLocked()
	h.sess.caches.mu.Unlock()
	_, idle := h.sess.caches.stats()
	assert.Equal(t, 1, idle, "kept past the grace")

	h.sess.SetBackground(false, nil)
	p := h.until(h.open("pods", core.ScopeSel{Mode: core.ScopeOne, Name: "a"}), isReady)
	assert.Equal(t, []string{"p"}, names(p))
	assert.Equal(t, listed, lists.Load(), "warm: no new LIST")
}

func TestBackgroundKeepsNoMoreThanMaxIdle(t *testing.T) {
	h := newHarness(t, fakeClient())
	h.sess.caches.grace = time.Hour
	h.sess.caches.maxIdle = 1
	h.sess.SetBackground(true, func() {})
	v1 := h.open("pods", core.ScopeSel{Mode: core.ScopeOne, Name: "a"})
	v2 := h.open("pods", core.ScopeSel{Mode: core.ScopeOne, Name: "b"})
	h.until(v1, isReady)
	h.until(v2, isReady)
	h.m.Close(v1)
	h.m.Close(v2)
	_, idle := h.sess.caches.stats()
	assert.Equal(t, 1, idle)
}

func TestForegroundAgainTheGraceCountsFromTheReturn(t *testing.T) {
	h := newHarness(t, fakeClient())
	now := time.Unix(1000, 0)
	h.sess.caches.now = func() time.Time { return now }
	h.sess.caches.grace = time.Minute
	h.sess.SetBackground(true, func() {})
	v := h.open("pods", core.ScopeSel{Mode: core.ScopeOne, Name: "a"})
	h.until(v, isReady)
	h.m.Close(v)
	now = now.Add(time.Hour)
	h.sess.SetBackground(false, nil)
	_, idle := h.sess.caches.stats()
	assert.Equal(t, 1, idle, "not evicted at the return")
	now = now.Add(time.Minute)
	h.sess.caches.mu.Lock()
	h.sess.caches.evictLocked()
	h.sess.caches.mu.Unlock()
	_, idle = h.sess.caches.stats()
	assert.Equal(t, 0, idle, "a grace after the return")
}

// A background session that cannot go on without a login (the plugin,
// headless, failed; or a 401) says lost — once; in the foreground it does
// not (the person is here).
func TestBackgroundUnauthorizedIsLost(t *testing.T) {
	client := fakeClient()
	var failing atomic.Bool
	client.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		if failing.Load() {
			return true, nil, apierrors.NewUnauthorized("token expired")
		}
		return false, nil, nil
	})
	h := newHarness(t, client)
	var lost atomic.Int32
	h.sess.SetBackground(false, func() { lost.Add(1) }) // selected: told so, as the API does
	failing.Store(true)
	v := h.open("pods", all) // foreground: an error in the table, nothing lost
	h.until(v, func(p views.Page) bool { return p.Status.State == provider.StatusError })
	assert.Zero(t, lost.Load())

	h.sess.SetBackground(true, func() { lost.Add(1) })
	h.open("pods", core.ScopeSel{Mode: core.ScopeOne, Name: "x"})
	require.Eventually(t, func() bool { return lost.Load() >= 1 }, 5*time.Second, 10*time.Millisecond)
	time.Sleep(2 * time.Second) // the reflector retries meanwhile
	assert.Equal(t, int32(1), lost.Load(), "once")
}

// The hold file exists exactly while the session is in the background;
// closing removes it.
func TestTheHoldFileFollowsTheBackground(t *testing.T) {
	h := newHarness(t, fakeClient())
	h.sess.hold = filepath.Join(t.TempDir(), "run", "bg-1")
	exists := func() bool { _, err := os.Stat(h.sess.hold); return err == nil }
	assert.False(t, exists())
	h.sess.SetBackground(true, func() {})
	assert.True(t, exists())
	h.sess.SetBackground(false, nil)
	assert.False(t, exists())
	h.sess.SetBackground(true, func() {})
	h.sess.Close()
	assert.False(t, exists())
}

// A log tab of a background target whose request needs a login: lost.
func TestBackgroundLogStreamUnauthorizedIsLost(t *testing.T) {
	client := fakeClient(pod("ns", "p", "uid-1", withLogContainers))
	client.PrependReactor("get", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewUnauthorized("token expired")
	})
	s := newSession("ctx", "h", client, false)
	t.Cleanup(s.Close)
	s.logs = newFakeKubelet(t).fetcher(t)
	var lost atomic.Int32
	s.SetBackground(true, func() { lost.Add(1) })
	ref := core.Ref{Provider: ProviderID, Target: "ctx", Scope: "ns", Kind: "pods", Name: "p", UID: "uid-1"}
	err := s.StreamLogs(context.Background(), ref, provider.LogQuery{Channel: "app", TailLines: 10}, newLogRecorder())
	require.Error(t, err)
	require.Eventually(t, func() bool { return lost.Load() == 1 }, 2*time.Second, 10*time.Millisecond)
}
