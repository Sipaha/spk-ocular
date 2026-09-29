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
)

func startWatch(t *testing.T, p *Provider) *atomic.Int32 {
	t.Helper()
	var n atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		assert.NoError(t, p.Watch(ctx, func() { n.Add(1) }))
	}()
	t.Cleanup(func() { cancel(); <-done })
	time.Sleep(50 * time.Millisecond) // watches are added synchronously at start; let the goroutine get there
	return &n
}

func TestWatchSeesAtomicRenameWriteOnce(t *testing.T) {
	home := t.TempDir()
	cfg := write(t, filepath.Join(home, ".kube", "config"), kubeconfig("", "a"))
	p := NewWith(env(nil), home)
	n := startWatch(t, p)

	tmp := cfg + ".tmp"
	require.NoError(t, os.WriteFile(tmp, []byte(kubeconfig("", "a", "b")), 0o600))
	require.NoError(t, os.Rename(tmp, cfg))
	require.Eventually(t, func() bool { return n.Load() >= 1 }, 3*time.Second, 20*time.Millisecond)
	time.Sleep(2 * watchDebounce)
	assert.Equal(t, int32(1), n.Load(), "one burst, one callback")

	ts, _ := discover(t, p)
	assert.Equal(t, []string{"a", "b"}, titles(ts))
}

func TestWatchPicksUpKubeDirCreatedLater(t *testing.T) {
	home := t.TempDir()
	p := NewWith(env(nil), home)
	n := startWatch(t, p)

	require.NoError(t, os.WriteFile(filepath.Join(home, ".bash_history"), []byte("x"), 0o600))
	time.Sleep(2 * watchDebounce)
	assert.Equal(t, int32(0), n.Load(), "unrelated files in the home dir are ignored")

	require.NoError(t, os.Mkdir(filepath.Join(home, ".kube"), 0o755))
	require.Eventually(t, func() bool { return n.Load() >= 1 }, 3*time.Second, 20*time.Millisecond)

	// Now ~/.kube itself is watched: a new file there is seen.
	before := n.Load()
	write(t, filepath.Join(home, ".kube", "config"), kubeconfig("", "late"))
	require.Eventually(t, func() bool { return n.Load() > before }, 3*time.Second, 20*time.Millisecond)
	ts, _ := discover(t, p)
	assert.Equal(t, []string{"late"}, titles(ts))
}

func TestWatchSurvivesKubeDirReplacement(t *testing.T) {
	home := t.TempDir()
	kube := filepath.Join(home, ".kube")
	write(t, filepath.Join(kube, "config"), kubeconfig("", "a"))
	p := NewWith(env(nil), home)
	n := startWatch(t, p)

	require.NoError(t, os.RemoveAll(kube))
	require.Eventually(t, func() bool { return n.Load() >= 1 }, 3*time.Second, 20*time.Millisecond)
	ts, _ := discover(t, p)
	assert.Empty(t, ts)

	before := n.Load()
	write(t, filepath.Join(kube, "config"), kubeconfig("", "b"))
	require.Eventually(t, func() bool { return n.Load() > before }, 3*time.Second, 20*time.Millisecond)
	// And the recreated dir is watched again, not just its parent.
	time.Sleep(2 * watchDebounce)
	before = n.Load()
	write(t, filepath.Join(kube, "config"), kubeconfig("", "c"))
	require.Eventually(t, func() bool { return n.Load() > before }, 3*time.Second, 20*time.Millisecond)
	ts, _ = discover(t, p)
	assert.Equal(t, []string{"c"}, titles(ts))
}

func TestWatchFollowsSymlinkedKubeconfigTarget(t *testing.T) {
	home, elsewhere := t.TempDir(), t.TempDir()
	target := write(t, filepath.Join(elsewhere, "real.yaml"), kubeconfig("", "a"))
	link := filepath.Join(t.TempDir(), "kubeconfig")
	require.NoError(t, os.Symlink(target, link))
	p := NewWith(env(map[string]string{"KUBECONFIG": link}), home)
	n := startWatch(t, p)

	write(t, filepath.Join(elsewhere, "unrelated.txt"), "x")
	time.Sleep(2 * watchDebounce)
	assert.Equal(t, int32(0), n.Load(), "only the link target's name matters in its dir")

	write(t, target, kubeconfig("", "a", "b"))
	require.Eventually(t, func() bool { return n.Load() >= 1 }, 3*time.Second, 20*time.Millisecond)
	ts, _ := discover(t, p)
	assert.Equal(t, []string{"a", "b"}, titles(ts))
}

func TestWatchPlanWidensSourceDirs(t *testing.T) {
	home := t.TempDir()
	kube := filepath.Join(home, ".kube")
	require.NoError(t, os.MkdirAll(kube, 0o755))
	// KUBECONFIG in a missing dir under ~/.kube: its parent (~/.kube) must
	// still accept any name, since ~/.kube is a source dir itself.
	plan := watchPlan(Sources{Primary: []string{filepath.Join(kube, "sub", "cfg")}, KubeDir: kube})
	names, ok := plan[kube]
	assert.True(t, ok)
	assert.Nil(t, names)
}
