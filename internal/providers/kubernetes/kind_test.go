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

func kindKubeconfig(t *testing.T) string {
	t.Helper()
	p, _ := filepath.Abs(os.Getenv("OCULAR_KIND_KUBECONFIG"))
	return p
}

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

// rbacSession opens the kind cluster as a namespace-limited ServiceAccount
// (scripts/kind-rbac.sh, run by make test-kind).
func rbacSession(t *testing.T, who string) provider.Session {
	t.Helper()
	dir := os.Getenv("OCULAR_KIND_RBAC_DIR")
	if dir == "" {
		t.Skip("OCULAR_KIND_RBAC_DIR not set (make test-kind)")
	}
	cfg := filepath.Join(dir, who+".kubeconfig")
	p := NewWith(func(k string) string {
		if k == "KUBECONFIG" {
			return cfg
		}
		return ""
	}, t.TempDir())
	d, err := p.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, d.Targets, 1)
	s, err := p.Open(context.Background(), d.Targets[0].ID)
	require.NoError(t, err)
	t.Cleanup(s.Close)
	return s
}

func waitStatus(t *testing.T, m *views.Manager, id string, ok func(views.Page) bool) views.Page {
	t.Helper()
	var p views.Page
	require.Eventually(t, func() bool {
		p, _ = m.Get(id, 0)
		return ok(p)
	}, 30*time.Second, 20*time.Millisecond, "last: %+v", p.Status)
	return p
}

func TestKindNamespaceLimitedUser(t *testing.T) {
	s := rbacSession(t, "viewer")
	m := views.NewManager(events.NewEmitter())
	defer m.CloseAll()

	_, err := s.Scopes(context.Background())
	var pe *provider.Error
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, provider.ClassForbidden, pe.Class, "listing namespaces is denied")

	own, err := m.Open("s", s, provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "ocular-demo"}})
	require.NoError(t, err)
	p := waitStatus(t, m, own, func(p views.Page) bool { return p.Status.State == provider.StatusReady })
	assert.NotEmpty(t, p.Upserts, "its own namespace works")

	all, err := m.Open("s", s, provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeAll}})
	require.NoError(t, err)
	p = waitStatus(t, m, all, func(p views.Page) bool { return p.Status.State == provider.StatusError })
	assert.Equal(t, provider.ClassForbidden, p.Status.Class, "all namespaces: denied, not an empty table")
	assert.Empty(t, p.Upserts)
}

// list allowed, watch denied: the rows are shown (the reflector falls back
// from WatchList to a plain list) but the view is stale with the reason.
func TestKindListWithoutWatch(t *testing.T) {
	s := rbacSession(t, "nowatch")
	m := views.NewManager(events.NewEmitter())
	defer m.CloseAll()
	id, err := m.Open("s", s, provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "ocular-demo"}})
	require.NoError(t, err)
	// Settles on stale (rows from the list, live updates denied) — it may
	// pass through error while the watch fails before the list is processed.
	p := waitStatus(t, m, id, func(p views.Page) bool { return p.Status.State == provider.StatusStale })
	assert.Equal(t, provider.ClassForbidden, p.Status.Class)
	assert.Contains(t, p.Status.Message, "cannot watch")
	assert.NotEmpty(t, p.Upserts)
}

func TestKindEveryKindBecomesReady(t *testing.T) {
	p, target := kindProvider(t)
	sess, err := p.Open(context.Background(), target)
	require.NoError(t, err)
	defer sess.Close()
	m := views.NewManager(events.NewEmitter())
	defer m.CloseAll()
	for _, k := range sess.Kinds() {
		scope := core.ScopeSel{Mode: core.ScopeAll}
		if !k.Scoped {
			scope = core.ScopeSel{Mode: core.ScopeNone}
		}
		id, err := m.Open("s", sess, provider.Query{Kind: k.ID, Scope: scope})
		require.NoError(t, err, k.ID)
		pg := waitStatus(t, m, id, func(p views.Page) bool { return p.Status.State != provider.StatusLoading })
		assert.Equal(t, provider.StatusReady, pg.Status.State, "%s: %+v", k.ID, pg.Status)
		for _, r := range pg.Upserts {
			assert.Len(t, r.Cells, len(k.Columns), k.ID)
		}
		t.Logf("%-28s %4d rows", k.ID, len(pg.Upserts))
	}
}

func TestKindEventsOfOneObject(t *testing.T) {
	p, target := kindProvider(t)
	sess, err := p.Open(context.Background(), target)
	require.NoError(t, err)
	defer sess.Close()
	m := views.NewManager(events.NewEmitter())
	defer m.CloseAll()
	ns := core.ScopeSel{Mode: core.ScopeOne, Name: "ocular-demo"}
	pods, _ := m.Open("s", sess, provider.Query{Kind: "pods", Scope: ns})
	pg := waitStatus(t, m, pods, func(p views.Page) bool { return p.Status.State == provider.StatusReady })
	var crash core.Ref
	for _, r := range pg.Upserts {
		if r.Ref.Name == "crashloop" {
			crash = r.Ref
		}
	}
	require.NotEmpty(t, crash.UID, "seeded crashloop pod")
	evs, err := m.Open("s", sess, provider.Query{Kind: "events", Scope: ns, Subject: &crash})
	require.NoError(t, err)
	pg = waitStatus(t, m, evs, func(p views.Page) bool { return p.Status.State == provider.StatusReady && len(p.Upserts) > 0 })
	for _, r := range pg.Upserts {
		assert.Equal(t, "pod/crashloop", r.Cells[3].Text, "only the subject's events (server-side selector)")
	}
	_, err = m.Open("s", sess, provider.Query{Kind: "pods", Scope: ns, Subject: &crash})
	assert.Error(t, err, "only events narrow to an object")
}

func TestKindDetailsAndRelations(t *testing.T) {
	p, target := kindProvider(t)
	sess, err := p.Open(context.Background(), target)
	require.NoError(t, err)
	defer sess.Close()
	r, err := sess.Get(context.Background(), core.Ref{Kind: "apps/deployments", Scope: "ocular-demo", Name: "web"})
	require.NoError(t, err)
	assert.Empty(t, r.RelationsError)
	var rs, pods int
	for _, rel := range r.Relations {
		switch rel.Ref.Kind {
		case "apps/replicasets":
			rs++
		case "pods":
			pods++
		}
	}
	assert.Equal(t, 1, rs)
	assert.Equal(t, 3, pods)

	sec, err := sess.Get(context.Background(), core.Ref{Kind: "secrets", Scope: "ocular-demo", Name: "web-credentials"})
	require.NoError(t, err)
	assert.NotContains(t, sec.YAML, "bm90LWEtcmVhbC1wYXNzd29yZA==") // base64 of the seeded password
	assert.Contains(t, sec.YAML, "password: <19 bytes>")
}

// Needs metrics-server (scripts/kind-metrics.sh, run by make test-kind).
func TestKindMetrics(t *testing.T) {
	p, target := kindProvider(t)
	sess, err := p.Open(context.Background(), target)
	require.NoError(t, err)
	defer sess.Close()
	src := sess.(provider.MetricsSource)
	// Metrics are attributed to incarnations the open views observe.
	vm := views.NewManager(events.NewEmitter())
	defer vm.CloseAll()
	for _, q := range []provider.Query{
		{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "ocular-demo"}},
		{Kind: "nodes", Scope: core.ScopeSel{Mode: core.ScopeNone}},
	} {
		id, err := vm.Open("s", sess, q)
		require.NoError(t, err)
		waitStatus(t, vm, id, func(p views.Page) bool { return p.Status.State == provider.StatusReady })
	}
	var m provider.Metrics
	require.Eventually(t, func() bool {
		m, err = src.Metrics(context.Background(), provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "ocular-demo"}}, nil)
		return err == nil && len(m.Values) > 0
	}, 90*time.Second, 5*time.Second, "err: %v", err)
	for _, u := range m.Values {
		require.NotNil(t, u.Memory)
		assert.Greater(t, *u.Memory, 0.0)
		assert.False(t, u.At.IsZero())
	}
	nodes, err := src.Metrics(context.Background(), provider.Query{Kind: "nodes", Scope: core.ScopeSel{Mode: core.ScopeNone}}, nil)
	require.NoError(t, err)
	assert.Len(t, nodes.Values, 1, "the control-plane node, keyed by its UID")
}
