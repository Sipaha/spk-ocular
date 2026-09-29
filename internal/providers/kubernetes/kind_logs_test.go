package kubernetes

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

const restarterPod = `apiVersion: v1
kind: Pod
metadata: {name: restarter, namespace: ocular-demo}
spec:
  terminationGracePeriodSeconds: 0
  containers:
  - name: app
    image: "busybox:1.36"
    command: ["sh", "-c", "echo \"run $(cat /proc/sys/kernel/random/uuid)\"; for i in 1 2 3; do echo \"tick $i\"; sleep 1; done; exit 1"]
`

func kubectlKind(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("kubectl", append([]string{"--kubeconfig", kindKubeconfig(t)}, args...)...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	return string(out)
}

// One container through its restart: tail + follow, "waiting" while it is
// down, the next instance from its start, no line twice; then previous.
func TestKindLogsFollowARestartingContainer(t *testing.T) {
	p, target := kindProvider(t)
	kubectlKind(t, "", "-n", "ocular-demo", "delete", "pod", "restarter", "--ignore-not-found", "--wait=true")
	kubectlKind(t, restarterPod, "apply", "-f", "-")
	t.Cleanup(func() {
		_ = exec.Command("kubectl", "--kubeconfig", kindKubeconfig(t), "-n", "ocular-demo", "delete", "pod", "restarter", "--wait=false").Run()
	})
	kubectlKind(t, "", "-n", "ocular-demo", "wait", "--for=jsonpath={.status.containerStatuses[0].containerID}", "pod/restarter", "--timeout=60s")
	uid := strings.TrimSpace(kubectlKind(t, "", "-n", "ocular-demo", "get", "pod", "restarter", "-o", "jsonpath={.metadata.uid}"))

	sess, err := p.Open(context.Background(), target)
	require.NoError(t, err)
	defer sess.Close()
	ls := sess.(provider.LogSource)
	ref := core.Ref{Provider: ProviderID, Target: target, Scope: "ocular-demo", Kind: "pods", Name: "restarter", UID: uid}
	info, err := ls.LogInfo(context.Background(), ref)
	require.NoError(t, err)
	assert.Equal(t, "app", info.DefaultChannel)

	sink := newLogRecorder()
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		sink.mu.Lock()
		defer sink.mu.Unlock()
		for _, l := range sink.lines {
			t.Logf("line %s %q", l.TS, l.Text)
		}
		for _, st := range sink.states {
			t.Logf("state %+v", st)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- ls.StreamLogs(ctx, ref, provider.LogQuery{Follow: true, TailLines: 100}, sink) }()

	runs := func() []string { // distinct instances seen
		var out []string
		seen := map[string]bool{}
		for _, l := range sink.texts() {
			if strings.HasPrefix(l, "run ") && !seen[l] {
				seen[l] = true
				out = append(out, l)
			}
		}
		return out
	}
	deadline := func(what string, d time.Duration, cond func() bool) {
		t.Helper()
		require.Eventually(t, cond, d, 50*time.Millisecond, "%s; lines=%q states=%+v", what, sink.texts(), sink.states)
	}
	deadline("the first run", 30*time.Second, func() bool { return len(runs()) >= 1 })
	deadline("the container went down", 60*time.Second, func() bool { return sink.hasState(provider.LogWaiting) })
	deadline("the next instance", 90*time.Second, func() bool { return len(runs()) >= 2 })
	deadline("its ticks", 30*time.Second, func() bool {
		tx := sink.texts()
		return len(tx) > 0 && tx[len(tx)-1] == "tick 3"
	})

	// every run prints exactly its 4 lines: nothing lost, nothing repeated
	seen := map[string]int{}
	var cur string
	for _, l := range sink.texts() {
		if strings.HasPrefix(l, "run ") {
			cur = l
		}
		seen[cur+"|"+l]++
	}
	for k, n := range seen {
		assert.Equal(t, 1, n, "line %q", k)
	}
	for _, r := range runs() {
		for _, tick := range []string{"tick 1", "tick 2", "tick 3"} {
			assert.Equal(t, 1, seen[r+"|"+tick], "%s of %s", tick, r)
		}
	}
	for _, l := range sink.lines {
		assert.NotEmpty(t, l.TS, "timestamps are always requested")
	}
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)

	prev := newLogRecorder()
	require.NoError(t, ls.StreamLogs(context.Background(), ref, provider.LogQuery{Channel: "app", Previous: true, TailLines: 10}, prev))
	require.NotEmpty(t, prev.texts(), "states %+v", prev.states)
	assert.Equal(t, "tick 3", prev.texts()[len(prev.texts())-1])
	assert.Equal(t, provider.LogEnded, prev.lastState().State)
}

// The namespace viewer may list and watch pods but has no pods/log.
func TestKindLogsForbiddenIsSaid(t *testing.T) {
	s := rbacSession(t, "viewer").(provider.LogSource)
	uid := strings.TrimSpace(kubectlKind(t, "", "-n", "ocular-demo", "get", "pod", "crashloop", "-o", "jsonpath={.metadata.uid}"))
	sink := newLogRecorder()
	ref := core.Ref{Provider: ProviderID, Scope: "ocular-demo", Kind: "pods", Name: "crashloop", UID: uid}
	require.NoError(t, s.StreamLogs(context.Background(), ref, provider.LogQuery{Channel: "app", Follow: true, TailLines: 10}, sink))
	assert.Equal(t, provider.LogError, sink.lastState().State)
	assert.Equal(t, provider.ClassForbidden, sink.lastState().Class)
	assert.Empty(t, sink.texts())
}
