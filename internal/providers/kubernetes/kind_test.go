package kubernetes

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/views"
)

// kindProvider returns a provider reading only the test cluster's kubeconfig
// (OCULAR_KIND_KUBECONFIG, set by `make test-kind`), or skips.
func kindProvider(t *testing.T) (*Provider, string) {
	t.Helper()
	cfg := os.Getenv("OCULAR_KIND_KUBECONFIG")
	if cfg == "" {
		t.Skip("OCULAR_KIND_KUBECONFIG not set (make test-kind)")
	}
	cfg, _ = filepath.Abs(cfg)
	p := NewWith(func(k string) string {
		if k == "KUBECONFIG" {
			return cfg
		}
		return ""
	}, t.TempDir())
	d, err := p.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, d.Targets, 1, "the kind kubeconfig has exactly one context")
	return p, d.Targets[0].ID
}

func TestKindWatchPodsWithWatchList(t *testing.T) {
	p, target := kindProvider(t)
	sess, err := p.Open(context.Background(), target)
	require.NoError(t, err)
	defer sess.Close()
	m := views.NewManager(events.NewEmitter())
	defer m.CloseAll()

	start := time.Now()
	id, err := m.Open("s", sess, provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "kube-system"}})
	require.NoError(t, err)
	var page views.Page
	require.Eventually(t, func() bool {
		page, _ = m.Get(id, 0)
		return page.Status.State == provider.StatusReady
	}, 30*time.Second, 20*time.Millisecond)
	t.Logf("kube-system pods ready in %v: %d rows", time.Since(start), len(page.Upserts))
	require.NotEmpty(t, page.Upserts, "ready means the initial rows are in")
	var sawCoreDNS bool
	for _, r := range page.Upserts {
		sawCoreDNS = sawCoreDNS || (len(r.Ref.Name) > 7 && r.Ref.Name[:7] == "coredns")
		assert.Equal(t, "kube-system", r.Ref.Scope)
		assert.NotEmpty(t, r.Ref.UID)
	}
	assert.True(t, sawCoreDNS)

	scopes, err := sess.Scopes(context.Background())
	require.NoError(t, err)
	assert.Contains(t, scopes, core.Scope{Name: "kube-system"})
}

func TestKindUnreachableServerIsAnError(t *testing.T) {
	p, target := kindProvider(t)
	sess, err := p.Open(context.Background(), target)
	require.NoError(t, err)
	s := sess.(*session)
	defer s.Close()
	// Same session machinery against a closed port.
	bad, _ := newUnreachable(t)
	s2 := newSession("x", "h", bad, true)
	defer s2.Close()
	m := views.NewManager(events.NewEmitter())
	defer m.CloseAll()
	id, err := m.Open("s", s2, provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeAll}})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		pg, _ := m.Get(id, 0)
		return pg.Status.State == provider.StatusError && pg.Status.Class == provider.ClassUnavailable
	}, 20*time.Second, 50*time.Millisecond)
}
