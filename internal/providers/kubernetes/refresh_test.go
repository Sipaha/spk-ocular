package kubernetes

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRefreshReplacesOnlyRequestedCacheAndPreservesOtherLeases(t *testing.T) {
	h := newHarness(t, fakeClient(pod("a", "one", "u-a"), pod("b", "two", "u-b")))
	a := h.open("pods", core.ScopeSel{Mode: core.ScopeOne, Name: "a"})
	same := h.open("pods", core.ScopeSel{Mode: core.ScopeOne, Name: "a"})
	b := h.open("pods", core.ScopeSel{Mode: core.ScopeOne, Name: "b"})
	h.until(a, isReady)
	h.until(same, isReady)
	h.until(b, isReady)
	key := cacheKey{gvr: podsKind.gvr, namespace: "a"}
	otherKey := cacheKey{gvr: podsKind.gvr, namespace: "b"}
	h.sess.caches.mu.Lock()
	old := h.sess.caches.caches[key]
	other := h.sess.caches.caches[otherKey]
	h.sess.caches.mu.Unlock()
	require.NoError(t, h.m.Refresh(context.Background(), a))
	h.sess.caches.mu.Lock()
	fresh := h.sess.caches.caches[key]
	_, retired := h.sess.caches.retired[old]
	require.Same(t, other, h.sess.caches.caches[otherKey])
	h.sess.caches.mu.Unlock()
	require.NotSame(t, old, fresh)
	require.True(t, retired)
	select {
	case <-old.stop:
		t.Fatal("another consumer still leases the old watch")
	default:
	}
	p, err := h.m.Get(a, 0)
	require.NoError(t, err)
	require.Equal(t, provider.StatusReady, p.Status.State)
	require.Len(t, p.Upserts, 1)
	h.m.Close(same)
	select {
	case <-old.stop:
	default:
		t.Fatal("retired watch must close with its last lease")
	}
}
