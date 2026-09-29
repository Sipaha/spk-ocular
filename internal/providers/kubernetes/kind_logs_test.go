package kubernetes

import (
	"context"
	"fmt"
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

// A deployment's pods in one stream; scale-up adds a source from its first
// line, scale-down ends one.
func TestKindLogsOfADeployment(t *testing.T) {
	p, target := kindProvider(t)
	sess, err := p.Open(context.Background(), target)
	require.NoError(t, err)
	defer sess.Close()
	ls := sess.(provider.LogSource)
	uid := strings.TrimSpace(kubectlKind(t, "", "-n", "ocular-demo", "get", "deploy", "chatter", "-o", "jsonpath={.metadata.uid}"))
	ref := core.Ref{Provider: ProviderID, Target: target, Scope: "ocular-demo", Kind: "apps/deployments", Name: "chatter", UID: uid}
	info, err := ls.LogInfo(context.Background(), ref)
	require.NoError(t, err)
	assert.True(t, info.Aggregate)
	assert.Equal(t, "app", info.DefaultChannel)

	sink := newLogRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- ls.StreamLogs(ctx, ref, provider.LogQuery{Follow: true, TailLines: 6}, sink) }()
	sink.await(t, "ready", func() bool { sink.mu.Lock(); defer sink.mu.Unlock(); return sink.ready })
	sink.mu.Lock()
	assert.Len(t, sink.sources, 2)
	assert.LessOrEqual(t, sink.readyAt, 6, "the tail is overall")
	sink.mu.Unlock()

	kubectlKind(t, "", "-n", "ocular-demo", "scale", "deploy/chatter", "--replicas=3")
	t.Cleanup(func() { kubectlKind(t, "", "-n", "ocular-demo", "scale", "deploy/chatter", "--replicas=2") })
	require.Eventually(t, func() bool { return sink.sourceCount() == 3 }, 90*time.Second, 100*time.Millisecond)
	sink.mu.Lock()
	newKey := sink.sources[2]
	sink.mu.Unlock()
	require.Eventually(t, func() bool {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		for i, l := range sink.lines {
			if sink.lineSrc[i] == newKey && strings.HasSuffix(l.Text, " tick 1") {
				return true
			}
		}
		return false
	}, 60*time.Second, 100*time.Millisecond, "the new pod's first line")

	kubectlKind(t, "", "-n", "ocular-demo", "scale", "deploy/chatter", "--replicas=2")
	require.Eventually(t, func() bool {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		for key, st := range sink.stateOf {
			if key != "" && st.State == provider.LogEnded {
				return true
			}
		}
		return false
	}, 90*time.Second, 100*time.Millisecond, "a removed pod's source ends")
	cancel()
	<-done

	// no line twice per pod (tick numbers strictly increase per source)
	sink.mu.Lock()
	defer sink.mu.Unlock()
	last := map[string]int{}
	for i, l := range sink.lines {
		var host string
		var n int
		f := strings.Fields(l.Text)
		require.GreaterOrEqual(t, len(f), 4, l.Text)
		host = f[1]
		_, err := fmt.Sscanf(f[3], "%d", &n)
		require.NoError(t, err)
		assert.Greater(t, n, last[host], "%s at line %d (%s)", l.Text, i, sink.lineSrc[i])
		last[host] = n
	}
}

// pods/log allowed, watching pods not (list is): a pod's logs — one
// container or all — still stream; a deployment cannot be read at all by
// this user and says so.
func TestKindLogsWithoutPodWatch(t *testing.T) {
	s := rbacSession(t, "nowatch").(provider.LogSource)
	// the oldest pod: a scale-down (another test) removes the newest ones
	name := strings.TrimSpace(kubectlKind(t, "", "-n", "ocular-demo", "get", "pods", "-l", "app=chatter",
		"--sort-by=.metadata.creationTimestamp", "-o", "jsonpath={.items[0].metadata.name}"))
	uid := strings.TrimSpace(kubectlKind(t, "", "-n", "ocular-demo", "get", "pod", name, "-o", "jsonpath={.metadata.uid}"))
	ref := core.Ref{Provider: ProviderID, Scope: "ocular-demo", Kind: "pods", Name: name, UID: uid}
	for _, ch := range []string{"app", provider.ChannelAll} {
		sink := newLogRecorder()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- s.StreamLogs(ctx, ref, provider.LogQuery{Channel: ch, Follow: true, TailLines: 2}, sink)
		}()
		sink.await(t, "live lines of "+ch, func() bool { return len(sink.texts()) >= 4 })
		cancel()
		require.ErrorIs(t, <-done, context.Canceled)
	}

	err := s.StreamLogs(context.Background(), core.Ref{Provider: ProviderID, Scope: "ocular-demo", Kind: "apps/deployments", Name: "chatter"},
		provider.LogQuery{Follow: true, TailLines: 2}, newLogRecorder())
	var pe *provider.Error
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, provider.ClassForbidden, pe.Class, "%v", err)
}
