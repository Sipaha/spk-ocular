package kubernetes

import (
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/stretchr/testify/require"
)

func TestIdleCacheObjectBudgetEvictsOldestWithoutTouchingActiveLeases(t *testing.T) {
	h := newHarness(t, fakeClient(pod("a", "one", "u-a"), pod("b", "two", "u-b")))
	h.sess.caches.maxIdleObjects = 1
	a := h.open("pods", core.ScopeSel{Mode: core.ScopeOne, Name: "a"})
	b := h.open("pods", core.ScopeSel{Mode: core.ScopeOne, Name: "b"})
	h.until(a, isReady)
	h.until(b, isReady)
	active, idle := h.sess.caches.stats()
	require.Equal(t, 2, active)
	require.Zero(t, idle)
	h.m.Close(a)
	active, idle = h.sess.caches.stats()
	require.Equal(t, 1, active)
	require.Equal(t, 1, idle)
	h.m.Close(b)
	active, idle = h.sess.caches.stats()
	require.Zero(t, active)
	require.Equal(t, 1, idle)
	h.sess.caches.mu.Lock()
	_, old := h.sess.caches.caches[cacheKey{gvr: podsKind.gvr, namespace: "a"}]
	_, latest := h.sess.caches.caches[cacheKey{gvr: podsKind.gvr, namespace: "b"}]
	h.sess.caches.mu.Unlock()
	require.False(t, old)
	require.True(t, latest)
}

func TestIdleCachePayloadBudgetAppliesInBackground(t *testing.T) {
	h := newHarness(t, fakeClient(pod("a", "one", "u-a")))
	h.sess.caches.maxIdleBytes = 1
	h.sess.SetBackground(true, func() {})
	view := h.open("pods", core.ScopeSel{Mode: core.ScopeOne, Name: "a"})
	h.until(view, isReady)
	active, _ := h.sess.caches.stats()
	require.Equal(t, 1, active, "an active lease is never removed for idle retention")
	h.m.Close(view)
	active, idle := h.sess.caches.stats()
	require.Zero(t, active)
	require.Zero(t, idle)
}

func TestIdleCacheGrowthIsBudgetedWithoutAnotherViewOrAPIRead(t *testing.T) {
	client := fakeClient(pod("a", "one", "u-a"))
	h := newHarness(t, client)
	h.sess.caches.maxIdleObjects = 1
	h.sess.caches.budgetSweep = 20 * time.Millisecond
	h.sess.SetBackground(true, func() {})
	view := h.open("pods", core.ScopeSel{Mode: core.ScopeOne, Name: "a"})
	h.until(view, isReady)
	h.sess.caches.mu.Lock()
	c := h.sess.caches.caches[cacheKey{gvr: podsKind.gvr, namespace: "a"}]
	h.sess.caches.mu.Unlock()
	require.NotNil(t, c)
	h.m.Close(view)
	require.NoError(t, client.Tracker().Add(pod("a", "two", "u-b")))
	require.Eventually(t, func() bool { _, idle := h.sess.caches.stats(); return idle == 0 }, 2*time.Second, 10*time.Millisecond)
	select {
	case <-c.stop:
	default:
		t.Fatal("over-budget idle cache did not stop its watch")
	}
}
