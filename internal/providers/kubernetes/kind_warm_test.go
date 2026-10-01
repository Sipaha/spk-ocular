package kubernetes

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/spk/spk-ocular/internal/execshim"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/views"
)

// The test binary doubles as the exec-credential shim, like the app binary.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == execshim.Subcommand {
		os.Exit(execshim.Main(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

// kindRestConfig resolves the test cluster's context like Open does.
func kindRestConfig(t *testing.T) (*rest.Config, string) {
	t.Helper()
	p, target := kindProvider(t)
	l := load(p.sources())
	var kc *kubeContext
	for i := range l.Contexts {
		if l.Contexts[i].ID == target {
			kc = &l.Contexts[i]
		}
	}
	require.NotNil(t, kc)
	cfg, err := restConfig(*kc)
	require.NoError(t, err)
	return cfg, target
}

// countTotals counts the requests that prove a cold path, across the many
// clients of a session: GETs that are not watches (LISTs) and initial-sync
// watches (WatchList: a relist shows as another initial sync).
type countTotals struct {
	mu          sync.Mutex
	lists       int
	initialSync int
}

func (t *countTotals) add(list, sync bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if list {
		t.lists++
	}
	if sync {
		t.initialSync++
	}
}

func (t *countTotals) snapshot() (int, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lists, t.initialSync
}

type countTransport struct {
	totals *countTotals
	base   http.RoundTripper
}

func (c *countTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodGet {
		q := r.URL.Query()
		if q.Has("watch") {
			c.totals.add(false, q.Get("sendInitialEvents") == "true")
		} else {
			c.totals.add(true, false)
		}
	}
	return c.base.RoundTrip(r)
}

// A warm return to a recent target must not LIST again: its caches survived
// the background stay past the idle grace (P18, the warm path proven by no
// LIST and no initial-sync watch on a real cluster).
func TestKindWarmReturnMakesNoLists(t *testing.T) {
	cfg, target := kindRestConfig(t)
	var totals countTotals
	cfg.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
		return &countTransport{totals: &totals, base: rt}
	}
	sess, err := sessionFor(cfg, target, "kind", "h")
	require.NoError(t, err)
	defer sess.Close()
	sess.caches.grace = 2 * time.Second // a short idle grace for the test
	m := views.NewManager(events.NewEmitter())
	defer m.CloseAll()

	q := provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "ocular-demo"}}
	id, err := m.Open("s", sess, q)
	require.NoError(t, err)
	waitStatus(t, m, id, func(p views.Page) bool { return p.Status.State == provider.StatusReady })
	time.Sleep(3 * time.Second) // discovery and friends settle
	baseLists, baseSyncs := totals.snapshot()

	lost := make(chan struct{}, 1)
	sess.SetBackground(true, func() { lost <- struct{}{} })
	m.Close(id)                 // leaving the target: the view goes, its cache idles
	time.Sleep(5 * time.Second) // past the grace: only a background stay keeps the cache
	sess.SetBackground(false, nil)

	id2, err := m.Open("s", sess, q)
	require.NoError(t, err)
	waitStatus(t, m, id2, func(p views.Page) bool { return p.Status.State == provider.StatusReady })
	lists, syncs := totals.snapshot()
	assert.Equal(t, baseLists, lists, "the warm return made new LISTs")
	assert.Equal(t, baseSyncs, syncs, "the warm return re-synced a cache (a relist)")
	select {
	case <-lost:
		t.Fatal("lost fired on a healthy cluster")
	default:
	}
}

// pluginStubKubeconfig writes a kubeconfig equal to the kind one, but whose
// user is a credential-plugin stub. The stub answers with a fresh kind token
// (kubectl create token) for the warm-test ServiceAccount and reports an
// expiry seconds away, so client-go renews on its next request. Once the
// marker file exists the stub hangs, like a plugin waiting for a person (a
// browser login, an MFA prompt).
func pluginStubKubeconfig(t *testing.T) (string, string) {
	t.Helper()
	kind := os.Getenv("OCULAR_KIND_KUBECONFIG")
	if kind == "" {
		t.Skip("OCULAR_KIND_KUBECONFIG not set (make test-kind)")
	}
	// The identity the stub mints tokens for: list/watch pods and configmaps
	// in the fixtures' namespace, nothing else.
	const sa = `apiVersion: v1
kind: ServiceAccount
metadata:
  name: ocular-warm
  namespace: ocular-demo
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: ocular-warm
  namespace: ocular-demo
rules:
- apiGroups: [""]
  resources: [pods, configmaps]
  verbs: [list, watch]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: ocular-warm
  namespace: ocular-demo
subjects:
- kind: ServiceAccount
  name: ocular-warm
  namespace: ocular-demo
roleRef:
  kind: Role
  name: ocular-warm
  apiGroup: rbac.authorization.k8s.io
`
	apply := exec.Command("kubectl", "--kubeconfig", kind, "apply", "-f", "-")
	apply.Stdin = strings.NewReader(sa)
	apply.Stderr = os.Stderr
	require.NoError(t, apply.Run(), "the warm-test ServiceAccount is seeded")
	raw, err := os.ReadFile(kind)
	require.NoError(t, err)
	apiCfg, err := clientcmd.Load(raw)
	require.NoError(t, err)
	dir := t.TempDir()
	stub := filepath.Join(dir, "stub.sh")
	marker := filepath.Join(dir, "person")
	body := `#!/bin/sh
if [ -f "` + marker + `" ]; then sleep 3600; fi
token=$(kubectl --kubeconfig "` + kind + `" create token ocular-warm -n ocular-demo --duration=10m) || exit 1
exp=$(date -u -d '+5 seconds' +%Y-%m-%dT%H:%M:%SZ)
printf '{"kind":"ExecCredential","apiVersion":"client.authentication.k8s.io/v1","status":{"token":"%s","expirationTimestamp":"%s"}}\n' "$token" "$exp"
`
	require.NoError(t, os.WriteFile(stub, []byte(body), 0o755))
	for _, ctx := range apiCfg.Contexts {
		apiCfg.AuthInfos[ctx.AuthInfo] = &clientcmdapi.AuthInfo{
			Exec: &clientcmdapi.ExecConfig{
				APIVersion:      "client.authentication.k8s.io/v1",
				Command:         stub,
				InteractiveMode: clientcmdapi.NeverExecInteractiveMode,
			},
		}
	}
	out := filepath.Join(dir, "exec.kubeconfig")
	require.NoError(t, clientcmd.WriteToFile(*apiCfg, out))
	return out, marker
}

// stubSession opens the stubbed target through the shim with a test-sized
// timeout, exactly like Provider.Open does (a short hold timeout here).
func stubSession(t *testing.T, kubeconfig string, timeout time.Duration) *session {
	t.Helper()
	p := NewWith(func(k string) string {
		if k == "KUBECONFIG" {
			return kubeconfig
		}
		return ""
	}, t.TempDir())
	d, err := p.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, d.Targets, 1)
	l := load(p.sources())
	var kc *kubeContext
	for i := range l.Contexts {
		if l.Contexts[i].ID == d.Targets[0].ID {
			kc = &l.Contexts[i]
		}
	}
	require.NotNil(t, kc)
	cfg, err := restConfig(*kc)
	require.NoError(t, err)
	self, err := os.Executable()
	require.NoError(t, err)
	hold := filepath.Join(t.TempDir(), "run", "bg-"+randomHex(8))
	execshim.WrapHeld(cfg, self, timeout, hold)
	sess, err := sessionFor(cfg, kc.ID, kc.Name, kc.Hash)
	require.NoError(t, err)
	sess.hold = hold
	return sess
}

// A quiet plugin renews the token headless in the background: the session
// lives on across the expiry (P18).
func TestKindBackgroundSurvivesAPluginRenewal(t *testing.T) {
	kubeconfig, _ := pluginStubKubeconfig(t)
	sess := stubSession(t, kubeconfig, 5*time.Second)
	defer sess.Close()
	require.NotEmpty(t, sess.hold)
	m := views.NewManager(events.NewEmitter())
	defer m.CloseAll()

	id, err := m.Open("s", sess, provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "ocular-demo"}})
	require.NoError(t, err)
	waitStatus(t, m, id, func(p views.Page) bool { return p.Status.State == provider.StatusReady })

	lost := make(chan struct{}, 1)
	sess.SetBackground(true, func() { lost <- struct{}{} })
	require.FileExists(t, sess.hold)
	time.Sleep(6 * time.Second) // the reported expiry passes in the background
	// The session's own request renews the token through the headless shim.
	id2, err := m.Open("s", sess, provider.Query{Kind: "configmaps", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "ocular-demo"}})
	require.NoError(t, err)
	waitStatus(t, m, id2, func(p views.Page) bool { return p.Status.State == provider.StatusReady })
	select {
	case <-lost:
		t.Fatal("lost fired for a quiet plugin")
	case <-time.After(500 * time.Millisecond):
	}
}

// A plugin that needs a person cannot finish headless: after the expiry its
// renewal hangs, the shim kills it, the request is unauthorized and the
// background session is closed (lost) — instead of an invisible login window
// (P18).
func TestKindBackgroundAPluginNeedingAPersonIsLost(t *testing.T) {
	kubeconfig, marker := pluginStubKubeconfig(t)
	sess := stubSession(t, kubeconfig, 3*time.Second)
	defer sess.Close()
	require.NotEmpty(t, sess.hold)
	m := views.NewManager(events.NewEmitter())
	defer m.CloseAll()

	id, err := m.Open("s", sess, provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "ocular-demo"}})
	require.NoError(t, err)
	waitStatus(t, m, id, func(p views.Page) bool { return p.Status.State == provider.StatusReady })

	lost := make(chan struct{}, 1)
	sess.SetBackground(true, func() { lost <- struct{}{} })
	require.FileExists(t, sess.hold)
	require.NoError(t, os.WriteFile(marker, nil, 0o600)) // renewals now need a person
	time.Sleep(6 * time.Second)                          // the reported expiry passes
	_, err = m.Open("s", sess, provider.Query{Kind: "configmaps", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "ocular-demo"}})
	require.NoError(t, err)
	select {
	case <-lost:
	case <-time.After(30 * time.Second):
		t.Fatal("the session was not closed after the plugin needed a person in the background")
	}
}
