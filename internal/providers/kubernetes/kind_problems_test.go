package kubernetes

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/views"
)

func hasIssue(h core.Health, reason string) bool {
	for _, is := range h.Issues {
		if is.Reason == reason {
			return true
		}
	}
	return false
}

// Real failures show up in Problems with the reasons of their own tables,
// the crash loop with its recent termination, and Warning events as recent
// evidence.
func TestKindProblems(t *testing.T) {
	c := kindActionCluster(t)
	c.kubectlNS("run", "crash", "--image=nginx:1.27-alpine", "--restart=Always", "--command", "--", "sh", "-c", "exit 3")
	c.kubectlNS("run", "pull", "--image=ocular.invalid/none:1", "--restart=Never")
	c.kubectlNS("run", "stuck", "--image=nginx:1.27-alpine", "--restart=Never", "--overrides", `{"spec":{"nodeSelector":{"ocular":"nowhere"}}}`)
	crash, pull, stuck := c.ref("pods", "crash").UID, c.ref("pods", "pull").UID, c.ref("pods", "stuck").UID

	m := views.NewManager(events.NewEmitter())
	defer m.CloseAll()
	id, err := m.Open("s", c.sess, provider.Query{Kind: "problems", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: c.ns}})
	require.NoError(t, err)
	p := waitStatus(t, m, id, func(p views.Page) bool {
		cr, ok1 := rowByID(p, "pods#"+crash)
		_, ok2 := rowByID(p, "pods#"+pull)
		st, ok3 := rowByID(p, "pods#"+stuck)
		evidence := false
		for _, r := range p.Upserts {
			evidence = evidence || (strings.HasPrefix(r.ID, "events#") && r.Cells[0].Text == "recent")
		}
		return p.Status.State == provider.StatusReady && ok1 && ok2 && ok3 && evidence &&
			hasIssue(cr.Health, "RecentRestart") && st.Health.Reason == "Unschedulable"
	})
	// Between crashes the container runs for a moment: then the pod is only
	// a warning (its recent termination), in back-off an error.
	cr, _ := rowByID(p, "pods#"+crash)
	assert.Contains(t, []core.HealthState{core.HealthError, core.HealthWarning}, cr.Health.State)
	pl, _ := rowByID(p, "pods#"+pull)
	assert.Contains(t, []string{"ErrImagePull", "ImagePullBackOff"}, pl.Health.Reason)
	for _, cov := range p.Status.Coverage {
		assert.Equal(t, provider.CoverageReady, cov.State, cov.Source)
	}
}

// A user who may only see pods in one namespace: Problems works and says
// what it cannot see.
func TestKindProblemsCoverageOfAViewer(t *testing.T) {
	s := rbacSession(t, "viewer")
	m := views.NewManager(events.NewEmitter())
	defer m.CloseAll()
	id, err := m.Open("s", s, provider.Query{Kind: "problems", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "ocular-demo"}})
	require.NoError(t, err)
	p := waitStatus(t, m, id, func(p views.Page) bool { return p.Status.State != provider.StatusLoading })
	assert.Equal(t, provider.StatusReady, p.Status.State, "%+v", p.Status)
	states := map[string]provider.CoverageState{}
	for _, cov := range p.Status.Coverage {
		states[cov.Source] = cov.State
	}
	assert.Equal(t, provider.CoverageReady, states["Pods"])
	assert.Equal(t, provider.CoverageDenied, states["Nodes (cluster-wide)"])
	assert.Equal(t, provider.CoverageDenied, states["Warning events"])
	assert.Equal(t, provider.CoverageDenied, states["Deployments"])
}
