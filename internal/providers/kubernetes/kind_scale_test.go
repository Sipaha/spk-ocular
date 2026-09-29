package kubernetes

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/views"
)

// A scale-up and back down must end with exactly the remaining pods: the
// deleted pod's row goes away (regression guard for the delete path).
func TestKindScaleDownRemovesRows(t *testing.T) {
	p, target := kindProvider(t)
	sess, err := p.Open(context.Background(), target)
	require.NoError(t, err)
	defer sess.Close()
	m := views.NewManager(events.NewEmitter())
	defer m.CloseAll()
	id, err := m.Open("s", sess, provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "ocular-demo"}})
	require.NoError(t, err)
	webRows := func() int {
		pg, _ := m.Get(id, 0)
		n := 0
		for _, r := range pg.Upserts {
			if strings.HasPrefix(r.Ref.Name, "web-") {
				n++
			}
		}
		return n
	}
	require.Eventually(t, func() bool { return webRows() == 3 }, 30*time.Second, 50*time.Millisecond)
	kubectl := func(args ...string) {
		out, err := exec.Command("kubectl", append([]string{"--kubeconfig", kindKubeconfig(t), "-n", "ocular-demo"}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	kubectl("scale", "deploy/web", "--replicas=4")
	defer kubectl("scale", "deploy/web", "--replicas=3")
	require.Eventually(t, func() bool { return webRows() == 4 }, 60*time.Second, 50*time.Millisecond)
	kubectl("scale", "deploy/web", "--replicas=3")
	require.Eventually(t, func() bool { return webRows() == 3 }, 60*time.Second, 50*time.Millisecond, "rows: %d", webRows())
}
