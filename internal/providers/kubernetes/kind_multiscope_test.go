package kubernetes

import (
	"context"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/views"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKindMultipleNamespacesWithOnlyPerNamespaceRBACAndMetrics(t *testing.T) {
	s := rbacSession(t, "multiviewer")
	m := views.NewManager(events.NewEmitter())
	defer m.CloseAll()
	_, err := s.Scopes(context.Background())
	require.Error(t, err, "listing the namespace catalog is forbidden")
	q := provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeSome, Names: []string{"ocular-demo", "kube-system"}}}
	id, err := m.Open("s", s, q)
	require.NoError(t, err)
	p := waitStatus(t, m, id, isReady)
	byScope := map[string]int{}
	for _, row := range p.Upserts {
		byScope[row.Ref.Scope]++
	}
	require.Len(t, byScope, 2)
	assert.Positive(t, byScope["ocular-demo"])
	assert.Positive(t, byScope["kube-system"])
	for _, c := range p.Status.Coverage {
		assert.Equal(t, provider.CoverageReady, c.State)
	}
	require.Eventually(t, func() bool {
		usage, err := s.(provider.MetricsSource).Metrics(context.Background(), q, nil)
		if err != nil {
			return false
		}
		found := map[string]bool{}
		for _, row := range p.Upserts {
			if _, ok := usage.Values[row.ID]; ok {
				found[row.Ref.Scope] = true
			}
		}
		return found["ocular-demo"] && found["kube-system"]
	}, 30*time.Second, time.Second, "both permitted namespaces have correctly attributed metrics")
	q.Scope.Names = append(q.Scope.Names, "default")
	id, err = m.Open("s", s, q)
	require.NoError(t, err)
	p = waitStatus(t, m, id, isReady)
	denied := false
	for _, c := range p.Status.Coverage {
		if c.Source == "default" {
			denied = c.State == provider.CoverageDenied
		}
	}
	assert.True(t, denied, "an extra forbidden namespace stays visible in coverage")
	assert.NotEmpty(t, p.Upserts, "permitted namespaces still work")
	id, err = m.Open("s", s, provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeAll}})
	require.NoError(t, err)
	p = waitStatus(t, m, id, func(p views.Page) bool { return p.Status.State == provider.StatusError })
	assert.Equal(t, provider.ClassForbidden, p.Status.Class)
}
