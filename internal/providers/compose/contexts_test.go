package compose

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
)

// env returns a getenv over vars (nothing else is set).
func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func write(t *testing.T, path, content string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// addContext writes a context the way `docker context create` stores it.
func addContext(t *testing.T, cfg, name, host string, skipTLS bool, meta map[string]any) string {
	t.Helper()
	m := map[string]any{"Name": name, "Metadata": meta, "Endpoints": map[string]any{"docker": map[string]any{"Host": host, "SkipTLSVerify": skipTLS}}}
	b, _ := json.Marshal(m)
	return write(t, filepath.Join(cfg, "contexts", "meta", contextDir(name), "meta.json"), string(b))
}

func addTLS(t *testing.T, cfg, name string, files map[string]string) {
	t.Helper()
	for f, content := range files {
		write(t, filepath.Join(cfg, "contexts", "tls", contextDir(name), "docker", f), content)
	}
}

func discover(t *testing.T, p *Provider) ([]core.Target, []core.Problem) {
	t.Helper()
	d, err := p.Discover(context.Background())
	require.NoError(t, err)
	return d.Targets, d.Problems
}

func byTitle(ts []core.Target) map[string]core.Target {
	m := map[string]core.Target{}
	for _, x := range ts {
		m[x.Title] = x
	}
	return m
}

func titles(ts []core.Target) []string {
	var out []string
	for _, x := range ts {
		out = append(out, x.Title)
	}
	return out
}

func current(ts []core.Target) []string {
	var out []string
	for _, x := range ts {
		if x.Current {
			out = append(out, x.Title)
		}
	}
	return out
}

func detail(x core.Target, key string) string {
	for _, d := range x.Details {
		if d.Key == key {
			return d.Value
		}
	}
	return ""
}

const (
	pemCA   = "-----BEGIN CERTIFICATE-----\nCA-BYTES\n-----END CERTIFICATE-----\n"
	pemCert = "-----BEGIN CERTIFICATE-----\nCERT-BYTES\n-----END CERTIFICATE-----\n"
	pemKey  = "-----BEGIN PRIVATE KEY-----\nSECRET-KEY-BYTES\n-----END PRIVATE KEY-----\n"
)

func TestOnlyTheBuiltInDefaultWithoutAConfigDir(t *testing.T) {
	ts, probs := discover(t, NewWith(env(nil), t.TempDir()))
	assert.Empty(t, probs)
	require.Len(t, ts, 1)
	assert.Equal(t, "context:default", ts[0].ID)
	assert.Equal(t, "unix:///var/run/docker.sock", ts[0].Subtitle)
	assert.True(t, ts[0].Current)
	assert.Empty(t, ts[0].DefaultScope, "all projects on a first visit")
}

func TestContextsFromTheStoreAndTheCurrentOneFromConfig(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, ".docker")
	addContext(t, cfg, "rootless", "unix:///run/user/1000/docker.sock", false, map[string]any{"Description": "Rootless mode"})
	addContext(t, cfg, "remote", "tcp://10.0.0.5:2376", false, nil)
	write(t, filepath.Join(cfg, "config.json"), `{"auths":{"registry.example":{"auth":"c2VjcmV0"}},"currentContext":"rootless"}`)
	ts, probs := discover(t, NewWith(env(nil), home))
	assert.Empty(t, probs)
	assert.Equal(t, []string{"default", "remote", "rootless"}, titles(ts))
	assert.Equal(t, []string{"rootless"}, current(ts))
	r := byTitle(ts)["rootless"]
	assert.Equal(t, "context:rootless", r.ID)
	assert.Equal(t, "Rootless mode", detail(r, "description"))
	assert.Equal(t, "unix:///run/user/1000/docker.sock", detail(r, "endpoint"))
	b, _ := json.Marshal(ts)
	assert.NotContains(t, string(b), "c2VjcmV0", "config.json's credentials are never read into targets")
}

func TestDockerConfigDockerContextAndDockerHostAsTheCLI(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "cfg")
	addContext(t, cfg, "a", "tcp://a:2375", false, nil)
	addContext(t, cfg, "b", "tcp://b:2375", false, nil)
	write(t, filepath.Join(cfg, "config.json"), `{"currentContext":"a"}`)

	ts, _ := discover(t, NewWith(env(map[string]string{"DOCKER_CONFIG": cfg}), t.TempDir()))
	assert.Equal(t, []string{"a"}, current(ts), "config.json")

	ts, _ = discover(t, NewWith(env(map[string]string{"DOCKER_CONFIG": cfg, "DOCKER_CONTEXT": "b"}), t.TempDir()))
	assert.Equal(t, []string{"b"}, current(ts), "DOCKER_CONTEXT over config.json")

	// DOCKER_HOST: the default context points to it and is current; the
	// others are listed as usual; DOCKER_CONTEXT does not apply.
	ts, _ = discover(t, NewWith(env(map[string]string{"DOCKER_CONFIG": cfg, "DOCKER_CONTEXT": "b", "DOCKER_HOST": "tcp://127.0.0.1:23750"}), t.TempDir()))
	assert.Equal(t, []string{"default"}, current(ts))
	assert.Equal(t, "tcp://127.0.0.1:23750", byTitle(ts)["default"].Subtitle)
	assert.Equal(t, []string{"default", "a", "b"}, titles(ts))
}

func TestAMissingCurrentContextIsAProblemNotAGuess(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "cfg")
	addContext(t, cfg, "a", "tcp://a:2375", false, nil)
	ts, probs := discover(t, NewWith(env(map[string]string{"DOCKER_CONFIG": cfg, "DOCKER_CONTEXT": "gone"}), t.TempDir()))
	assert.Empty(t, current(ts))
	require.Len(t, probs, 1)
	assert.Contains(t, probs[0].Message, `"gone"`)
}

func TestABrokenMetaIsAProblemAndTheOthersStay(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "cfg")
	addContext(t, cfg, "good", "tcp://good:2375", false, nil)
	write(t, filepath.Join(cfg, "contexts", "meta", contextDir("bad"), "meta.json"), "{not json")
	write(t, filepath.Join(cfg, "contexts", "meta", contextDir("elsewhere"), "meta.json"), `{"Name":"moved","Endpoints":{"docker":{"Host":"tcp://x:1"}}}`)
	write(t, filepath.Join(cfg, "contexts", "meta", contextDir("noendpoint"), "meta.json"), `{"Name":"noendpoint","Endpoints":{}}`)
	require.NoError(t, os.MkdirAll(filepath.Join(cfg, "contexts", "meta", contextDir("half-written")), 0o755))
	ts, probs := discover(t, NewWith(env(map[string]string{"DOCKER_CONFIG": cfg}), t.TempDir()))
	assert.Equal(t, []string{"default", "good"}, titles(ts))
	assert.Len(t, probs, 3, "bad JSON, a name under another name's dir, no endpoint; a dir without meta.json yet is not a problem")
}

func TestSSHContextsAreListed(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "cfg")
	addContext(t, cfg, "remote-ssh", "ssh://me@host", false, nil)
	ts, _ := discover(t, NewWith(env(map[string]string{"DOCKER_CONFIG": cfg}), t.TempDir()))
	assert.Equal(t, "ssh://host", byTitle(ts)["remote-ssh"].Subtitle, "listed; the user part is not shown")
}

func TestTLSOfAContextAndOfDockerHost(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "cfg")
	addContext(t, cfg, "secure", "tcp://secure:2376", false, nil)
	addTLS(t, cfg, "secure", map[string]string{"ca.pem": pemCA, "cert.pem": pemCert, "key.pem": pemKey})
	addContext(t, cfg, "half", "tcp://half:2376", false, nil)
	addTLS(t, cfg, "half", map[string]string{"ca.pem": pemCA, "cert.pem": pemCert})
	addContext(t, cfg, "insecure", "tcp://insecure:2376", true, nil)
	p := NewWith(env(map[string]string{"DOCKER_CONFIG": cfg}), t.TempDir())
	ts, _ := discover(t, p)
	m := byTitle(ts)
	assert.Equal(t, "TLS, verified", detail(m["secure"], "tls"))
	assert.Contains(t, detail(m["half"], "tlsProblem"), "incomplete TLS pair", "half a pair: a problem of the target, never plaintext")
	assert.Equal(t, "TLS without verification", detail(m["insecure"], "tls"))
	c, ok := p.contextByID("context:secure")
	require.True(t, ok)
	assert.Equal(t, pemKey, string(c.TLS.Key), "the session gets the bytes")
	h, ok := p.contextByID("context:half")
	require.True(t, ok)
	assert.Nil(t, h.TLS)
	assert.NotEmpty(t, h.TLSError)

	certs := t.TempDir()
	write(t, filepath.Join(certs, "ca.pem"), pemCA)
	write(t, filepath.Join(certs, "cert.pem"), pemCert)
	write(t, filepath.Join(certs, "key.pem"), pemKey)
	p = NewWith(env(map[string]string{"DOCKER_CONFIG": cfg, "DOCKER_HOST": "tcp://h:2376", "DOCKER_TLS_VERIFY": "1", "DOCKER_CERT_PATH": certs}), t.TempDir())
	ts, _ = discover(t, p)
	assert.Equal(t, "TLS, verified", detail(byTitle(ts)["default"], "tls"))
	d, _ := p.contextByID("context:default")
	assert.Equal(t, pemCert, string(d.TLS.Cert))

	p = NewWith(env(map[string]string{"DOCKER_CONFIG": cfg, "DOCKER_HOST": "tcp://h:2376", "DOCKER_TLS": "1"}), t.TempDir())
	ts, _ = discover(t, p)
	assert.Equal(t, "TLS without verification", detail(byTitle(ts)["default"], "tls"), "DOCKER_TLS: TLS, no verification; no files needed")
}

func TestTargetsNeverCarryTLSMaterialButTheHashCoversIt(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "cfg")
	addContext(t, cfg, "secure", "tcp://secure:2376", false, nil)
	addTLS(t, cfg, "secure", map[string]string{"ca.pem": pemCA, "cert.pem": pemCert, "key.pem": pemKey})
	p := NewWith(env(map[string]string{"DOCKER_CONFIG": cfg}), t.TempDir())
	ts, _ := discover(t, p)
	b, err := json.Marshal(ts)
	require.NoError(t, err)
	for _, secret := range []string{"SECRET-KEY-BYTES", "CERT-BYTES", "CA-BYTES", byTitle(ts)["secure"].ConfigHash} {
		assert.NotContains(t, string(b), secret)
	}

	before := byTitle(ts)["secure"].ConfigHash
	addTLS(t, cfg, "secure", map[string]string{"key.pem": pemKey + "rotated"})
	ts, _ = discover(t, p)
	assert.NotEqual(t, before, byTitle(ts)["secure"].ConfigHash, "a rotated key is another configuration")
	again, _ := discover(t, p)
	assert.Equal(t, byTitle(ts)["secure"].ConfigHash, byTitle(again)["secure"].ConfigHash, "stable while nothing changes")
}

func startWatch(t *testing.T, p *Provider) *atomic.Int32 {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var n atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = p.Watch(ctx, func() { n.Add(1) })
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	time.Sleep(100 * time.Millisecond) // the watches are in place
	return &n
}

func waitMore(t *testing.T, n *atomic.Int32, than int32) {
	t.Helper()
	require.Eventually(t, func() bool { return n.Load() > than }, 3*time.Second, 20*time.Millisecond)
	time.Sleep(2 * watchDebounce) // the burst settles
}

func TestWatchSeesANewContextACurrentChangeAndACertReplacement(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "cfg")
	addContext(t, cfg, "a", "tcp://a:2376", false, nil)
	addTLS(t, cfg, "a", map[string]string{"ca.pem": pemCA, "cert.pem": pemCert, "key.pem": pemKey})
	p := NewWith(env(map[string]string{"DOCKER_CONFIG": cfg}), t.TempDir())
	n := startWatch(t, p)

	// docker context create: a new directory, then its meta.json.
	addContext(t, cfg, "b", "tcp://b:2375", false, nil)
	waitMore(t, n, 0)
	ts, _ := discover(t, p)
	assert.Equal(t, []string{"default", "a", "b"}, titles(ts))

	// docker context use: config.json replaced atomically.
	k := n.Load()
	tmp := write(t, filepath.Join(cfg, "config.json.tmp"), `{"currentContext":"b"}`)
	require.NoError(t, os.Rename(tmp, filepath.Join(cfg, "config.json")))
	waitMore(t, n, k)
	ts, _ = discover(t, p)
	assert.Equal(t, []string{"b"}, current(ts))

	// A rotated certificate: renamed over the old one.
	k = n.Load()
	certDir := filepath.Join(cfg, "contexts", "tls", contextDir("a"), "docker")
	tmp = write(t, filepath.Join(certDir, "cert.pem.new"), pemCert+"rotated")
	require.NoError(t, os.Rename(tmp, filepath.Join(certDir, "cert.pem")))
	waitMore(t, n, k)

	// TLS material added later to a context that had none.
	k = n.Load()
	addTLS(t, cfg, "b", map[string]string{"ca.pem": pemCA})
	waitMore(t, n, k)
	_, _ = discover(t, p)
	c, _ := p.contextByID("context:b")
	require.NotNil(t, c.TLS)
	assert.Equal(t, pemCA, string(c.TLS.CA))
}

func TestWatchAwaitsAConfigDirCreatedLaterAndIgnoresTheRest(t *testing.T) {
	home := t.TempDir()
	p := NewWith(env(nil), home)
	n := startWatch(t, p)

	write(t, filepath.Join(home, ".bash_history"), "x")
	time.Sleep(2 * watchDebounce)
	assert.Equal(t, int32(0), n.Load(), "unrelated files are ignored")

	require.NoError(t, os.Mkdir(filepath.Join(home, ".docker"), 0o755))
	waitMore(t, n, 0)
	// buildx state and tokens in the config dir are not the contexts'.
	k := n.Load()
	write(t, filepath.Join(home, ".docker", "buildx", "current"), "x")
	write(t, filepath.Join(home, ".docker", ".token_seed"), "x")
	time.Sleep(2 * watchDebounce)
	assert.Equal(t, k, n.Load())

	addContext(t, filepath.Join(home, ".docker"), "late", "tcp://late:2375", false, nil)
	waitMore(t, n, k)
	ts, _ := discover(t, p)
	assert.Equal(t, []string{"default", "late"}, titles(ts))
}
