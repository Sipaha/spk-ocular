package kubernetes

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
)

// kubeconfig renders a minimal kubeconfig with one context per name; each
// context gets its own cluster and user, all named after it. Names are
// quoted: kubeconfig YAML is YAML 1.1, where a bare y/n/on/off is a bool.
func kubeconfig(current string, names ...string) string {
	var b strings.Builder
	b.WriteString("apiVersion: v1\nkind: Config\n")
	if current != "" {
		b.WriteString("current-context: \"" + current + "\"\n")
	}
	b.WriteString("clusters:\n")
	for _, n := range names {
		b.WriteString("- name: " + n + "-cluster\n  cluster:\n    server: https://" + n + ".example:6443\n")
	}
	b.WriteString("users:\n")
	for _, n := range names {
		b.WriteString("- name: " + n + "-user\n  user:\n    token: SECRET-" + n + "\n")
	}
	b.WriteString("contexts:\n")
	for _, n := range names {
		b.WriteString("- name: \"" + n + "\"\n  context:\n    cluster: " + n + "-cluster\n    user: " + n + "-user\n    namespace: ns-" + n + "\n")
	}
	return b.String()
}

func write(t *testing.T, path, body string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func discover(t *testing.T, p *Provider) ([]core.Target, []core.Problem) {
	t.Helper()
	d, err := p.Discover(context.Background())
	require.NoError(t, err)
	return d.Targets, d.Problems
}

func titles(ts []core.Target) []string {
	out := []string{}
	for _, t := range ts {
		out = append(out, t.Title)
	}
	return out
}

func byTitle(t *testing.T, ts []core.Target, title string) []core.Target {
	t.Helper()
	var out []core.Target
	for _, x := range ts {
		if x.Title == title {
			out = append(out, x)
		}
	}
	return out
}

func detail(t core.Target, key string) string {
	for _, d := range t.Details {
		if d.Key == key {
			return d.Value
		}
	}
	return ""
}

func TestDefaultKubeconfigWhenKUBECONFIGUnset(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, ".kube", "config"), kubeconfig("b", "a", "b"))
	ts, probs := discover(t, NewWith(env(nil), home))
	assert.Empty(t, probs)
	assert.Equal(t, []string{"a", "b"}, titles(ts))
	assert.False(t, ts[0].Current)
	assert.True(t, ts[1].Current)
	assert.Equal(t, "a-cluster", ts[0].Subtitle)
	assert.Equal(t, "https://a.example:6443", detail(ts[0], "server"))
	assert.Equal(t, "ns-a", detail(ts[0], "namespace"))
	assert.Equal(t, "token", detail(ts[0], "auth"))
}

func TestKUBECONFIGListMergesFirstWins(t *testing.T) {
	home, dir := t.TempDir(), t.TempDir()
	one := write(t, filepath.Join(dir, "one"), kubeconfig("shared", "shared", "x"))
	// Redefines "shared" pointing at another server and sets another current:
	// kubectl keeps the first file's definition and current-context.
	two := write(t, filepath.Join(dir, "two"), strings.ReplaceAll(kubeconfig("y", "shared", "y"), "shared.example", "other.example"))
	missing := filepath.Join(dir, "missing")
	ts, probs := discover(t, NewWith(env(map[string]string{"KUBECONFIG": one + string(os.PathListSeparator) + missing + string(os.PathListSeparator) + two}), home))
	assert.Empty(t, probs, "a missing KUBECONFIG entry is not a problem (kubectl ignores it)")
	assert.Equal(t, []string{"shared", "x", "y"}, titles(ts))
	assert.Equal(t, "https://shared.example:6443", detail(ts[0], "server"))
	assert.True(t, ts[0].Current)
	assert.False(t, ts[2].Current)
	assert.Equal(t, one, detail(ts[0], "file"), "shown: the file that defines the context")
	assert.Equal(t, two, detail(ts[2], "file"))
}

func TestBrokenPrimaryFileIsAProblemAndOthersStay(t *testing.T) {
	home, dir := t.TempDir(), t.TempDir()
	bad := write(t, filepath.Join(dir, "bad"), "apiVersion: v1\nkind: Config\ncontexts: [ {{{\n")
	good := write(t, filepath.Join(dir, "good"), kubeconfig("", "ok"))
	ts, probs := discover(t, NewWith(env(map[string]string{"KUBECONFIG": bad + string(os.PathListSeparator) + good}), home))
	assert.Equal(t, []string{"ok"}, titles(ts))
	require.Len(t, probs, 1)
	assert.Equal(t, bad, probs[0].Source)
	assert.NotContains(t, probs[0].Message, bad, "the path is the problem's source, not repeated in the message")
}

func TestExtraFilesInKubeDir(t *testing.T) {
	home := t.TempDir()
	kube := filepath.Join(home, ".kube")
	write(t, filepath.Join(kube, "config"), kubeconfig("main", "main", "dup"))
	write(t, filepath.Join(kube, "prod.yaml"), kubeconfig("prod", "prod", "dup"))
	write(t, filepath.Join(kube, "notes.txt"), "just some notes, not yaml: [")
	write(t, filepath.Join(kube, "empty.yaml"), "apiVersion: v1\nkind: Config\n")
	write(t, filepath.Join(kube, ".hidden"), kubeconfig("", "hidden"))
	write(t, filepath.Join(kube, "cache", "discovery", "x.json"), "{}")
	ts, probs := discover(t, NewWith(env(nil), home))
	assert.Empty(t, probs, "non-kubeconfig extras are skipped silently")
	assert.Equal(t, []string{"dup", "dup", "main", "prod"}, titles(ts))
	dups := byTitle(t, ts, "dup")
	assert.Equal(t, "kubeconfig:dup", dups[0].ID, "kubectl's own context first")
	assert.Equal(t, "dup-cluster", dups[0].Subtitle)
	assert.Equal(t, "file:"+filepath.Join(kube, "prod.yaml")+":dup", dups[1].ID)
	assert.Equal(t, "dup-cluster · prod.yaml", dups[1].Subtitle, "extras show their file")
	assert.Equal(t, filepath.Join(kube, "prod.yaml"), detail(dups[1], "file"))
	prod := byTitle(t, ts, "prod")[0]
	assert.False(t, prod.Current, "an extra file's current-context does not compete with kubectl's")
}

func TestKUBECONFIGFileInsideKubeDirIsNotListedTwice(t *testing.T) {
	home := t.TempDir()
	f := write(t, filepath.Join(home, ".kube", "work.yaml"), kubeconfig("", "work"))
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(f, link))
	ts, _ := discover(t, NewWith(env(map[string]string{"KUBECONFIG": link}), home))
	assert.Equal(t, []string{"work"}, titles(ts))
}

func TestNoKubeDirAtAllIsEmptyNotAnError(t *testing.T) {
	ts, probs := discover(t, NewWith(env(nil), t.TempDir()))
	assert.Empty(t, ts)
	assert.Empty(t, probs)
}

func TestTargetsNeverCarryCredentials(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, ".kube", "config"), kubeconfig("a", "a"))
	ts, _ := discover(t, NewWith(env(nil), home))
	for _, tg := range ts {
		for _, d := range tg.Details {
			assert.NotContains(t, d.Value, "SECRET", d.Key)
		}
	}
}

func TestAuthSummaryForExec(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, ".kube", "config"), `apiVersion: v1
kind: Config
clusters: [{name: c, cluster: {server: "https://c:443"}}]
users: [{name: u, user: {exec: {apiVersion: client.authentication.k8s.io/v1beta1, command: /usr/local/bin/yc, args: [k8s, create-token]}}}]
contexts: [{name: yc, context: {cluster: c, user: u}}]
`)
	ts, _ := discover(t, NewWith(env(nil), home))
	require.Len(t, ts, 1)
	assert.Equal(t, "exec: yc", detail(ts[0], "auth"))
}

// A remembered selection must not move to another cluster when some other
// file gains a same-named context (review finding, 2026-09-29).
func TestExtraContextIDIsStableWhenPrimaryGainsSameName(t *testing.T) {
	home := t.TempDir()
	kube := filepath.Join(home, ".kube")
	write(t, filepath.Join(kube, "extra.yaml"), kubeconfig("", "prod"))
	p := NewWith(env(nil), home)
	ts, _ := discover(t, p)
	require.Len(t, ts, 1)
	before := ts[0].ID

	write(t, filepath.Join(kube, "config"), kubeconfig("", "prod"))
	ts, _ = discover(t, p)
	require.Len(t, ts, 2)
	extra := byTitle(t, ts, "prod")[1]
	assert.Equal(t, before, extra.ID)
	assert.Equal(t, filepath.Join(kube, "extra.yaml"), detail(extra, "file"))
	assert.NotEqual(t, before, byTitle(t, ts, "prod")[0].ID)
}
