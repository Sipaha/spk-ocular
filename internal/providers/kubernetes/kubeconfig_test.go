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
	assert.Equal(t, "ns-a", detail(ts[0], "defaultNamespace"))
	assert.Equal(t, "ns-a", ts[0].DefaultScope, "the first visit opens the context's namespace")
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

// A target's identity is what it points at — the server, the CA it trusts
// and the auth-info it names — never a credential: a rotated token is the
// same target, another server or CA is not.
func TestTargetIdentity(t *testing.T) {
	oneAs := func(t *testing.T, userName, cluster, user string, files map[string]string) core.Target {
		t.Helper()
		home := t.TempDir()
		for name, body := range files {
			write(t, filepath.Join(home, name), body)
		}
		cfg := "apiVersion: v1\nkind: Config\nclusters:\n- name: c\n  cluster:\n" + cluster +
			"users:\n- name: " + userName + "\n  user:\n" + user +
			"contexts:\n- name: ctx\n  context:\n    cluster: c\n    user: " + userName + "\n"
		cfg = strings.ReplaceAll(cfg, "HOME", home)
		write(t, filepath.Join(home, ".kube", "config"), cfg)
		ts, _ := discover(t, NewWith(env(nil), home))
		require.Len(t, ts, 1)
		return ts[0]
	}
	one := func(t *testing.T, cluster, user string, files map[string]string) core.Target {
		t.Helper()
		return oneAs(t, "u", cluster, user, files)
	}
	base := one(t, "    server: https://a.example:6443\n    certificate-authority-data: Q0EtMQ==\n", "    token: T1\n", nil)
	assert.Contains(t, base.Identity, "https://a.example:6443")
	assert.Contains(t, base.Identity, "u")
	assert.NotContains(t, base.Identity, "T1")
	assert.NotContains(t, base.Identity, "Q0EtMQ==", "a digest of the CA, not the CA")
	assert.NotEmpty(t, base.ConfigHash)
	assert.Empty(t, detail(base, "identity"), "not a displayed detail")

	rotated := one(t, "    server: https://a.example:6443\n    certificate-authority-data: Q0EtMQ==\n", "    token: T2\n", nil)
	assert.Equal(t, base.Identity, rotated.Identity, "a new token: the same target")
	assert.NotEqual(t, base.ConfigHash, rotated.ConfigHash)

	for name, other := range map[string]core.Target{
		"server":   one(t, "    server: https://b.example:6443\n    certificate-authority-data: Q0EtMQ==\n", "    token: T1\n", nil),
		"ca":       one(t, "    server: https://a.example:6443\n    certificate-authority-data: Q0EtMg==\n", "    token: T1\n", nil),
		"insecure": one(t, "    server: https://a.example:6443\n    insecure-skip-tls-verify: true\n", "    token: T1\n", nil),
		"tls name": one(t, "    server: https://a.example:6443\n    certificate-authority-data: Q0EtMQ==\n    tls-server-name: other.example\n", "    token: T1\n", nil),
		"proxy":    one(t, "    server: https://a.example:6443\n    certificate-authority-data: Q0EtMQ==\n    proxy-url: http://proxy.example:3128\n", "    token: T1\n", nil),
		"user":     oneAs(t, "v", "    server: https://a.example:6443\n    certificate-authority-data: Q0EtMQ==\n", "    token: T1\n", nil),
	} {
		assert.NotEqual(t, base.Identity, other.Identity, name)
	}

	// Credentials in a URL never enter the identity; changing them keeps it.
	withPass := one(t, "    server: https://adm:SRVPASS@a.example:6443\n    certificate-authority-data: Q0EtMQ==\n    proxy-url: http://pu:PXPASS@proxy.example:3128\n", "    token: T1\n", nil)
	otherPass := one(t, "    server: https://adm:OTHER@a.example:6443\n    certificate-authority-data: Q0EtMQ==\n    proxy-url: http://pu:OTHER2@proxy.example:3128\n", "    token: T1\n", nil)
	for _, secret := range []string{"SRVPASS", "PXPASS", "adm", "pu:"} {
		assert.NotContains(t, withPass.Identity, secret)
	}
	assert.Contains(t, withPass.Identity, "proxy.example:3128")
	assert.Equal(t, withPass.Identity, otherPass.Identity)

	// A CA file: its content counts, not its path.
	f1 := one(t, "    server: https://a.example:6443\n    certificate-authority: HOME/ca.crt\n", "    token: T1\n", map[string]string{"ca.crt": "CA-1"})
	f1same := one(t, "    server: https://a.example:6443\n    certificate-authority: HOME/ca.crt\n", "    token: T1\n", map[string]string{"ca.crt": "CA-1"})
	f2 := one(t, "    server: https://a.example:6443\n    certificate-authority: HOME/ca.crt\n", "    token: T1\n", map[string]string{"ca.crt": "CA-2"})
	assert.Equal(t, f1.Identity, f1same.Identity, "the same CA in another directory")
	assert.NotEqual(t, f1.Identity, f2.Identity)
	assert.Equal(t, base.Identity, f1.Identity, "the same CA inline (base64 of CA-1) and in a file")
}
