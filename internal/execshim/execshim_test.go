package execshim

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// The test binary doubles as the shim (like the real app binary does).
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == Subcommand {
		os.Exit(Main(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

func script(t *testing.T, body string) (path, pidFile string) {
	t.Helper()
	dir := t.TempDir()
	pidFile = filepath.Join(dir, "pid")
	path = filepath.Join(dir, "plugin.sh")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\necho $$ > "+pidFile+"\n"+body), 0o755))
	return path, pidFile
}

func alive(t *testing.T, pidFile string) bool {
	t.Helper()
	b, err := os.ReadFile(pidFile)
	require.NoError(t, err)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return syscall.Kill(pid, 0) == nil
}

func TestShimPassesThroughProtocol(t *testing.T) {
	plugin, _ := script(t, `cat; echo "info=$KUBERNETES_EXEC_INFO"; echo "args=$*"; exit 3`)
	t.Setenv("KUBERNETES_EXEC_INFO", `{"kind":"ExecCredential"}`)
	var out, errb bytes.Buffer
	code := Main([]string{"--timeout", "5s", "--", plugin, "a", "b"}, strings.NewReader("stdin-data\n"), &out, &errb)
	assert.Equal(t, 3, code, "the plugin's exit code")
	assert.Equal(t, "stdin-data\ninfo={\"kind\":\"ExecCredential\"}\nargs=a b\n", out.String())
}

func TestShimKillsAHangingPlugin(t *testing.T) {
	plugin, pidFile := script(t, "exec sleep 3600\n")
	var out, errb bytes.Buffer
	start := time.Now()
	code := Main([]string{"--timeout", "300ms", "--", plugin}, strings.NewReader(""), &out, &errb)
	assert.Equal(t, 1, code)
	assert.Less(t, time.Since(start), 3*time.Second)
	assert.Contains(t, errb.String(), "did not answer within 300ms")
	assert.False(t, alive(t, pidFile), "the plugin is killed, not orphaned")
}

func TestWrapRewritesOnlyExecConfigs(t *testing.T) {
	cfg := &rest.Config{ExecProvider: &clientcmdapi.ExecConfig{Command: "/usr/bin/yc", Args: []string{"k8s", "create-token"}}}
	orig := cfg.ExecProvider
	Wrap(cfg, "/opt/ocular", time.Minute)
	assert.Equal(t, "/opt/ocular", cfg.ExecProvider.Command)
	assert.Equal(t, []string{Subcommand, "--timeout", "1m0s", "--", "/usr/bin/yc", "k8s", "create-token"}, cfg.ExecProvider.Args)
	assert.Equal(t, "/usr/bin/yc", orig.Command, "the caller's ExecConfig is not mutated")

	plain := &rest.Config{BearerToken: "x"}
	Wrap(plain, "/opt/ocular", time.Minute)
	assert.Nil(t, plain.ExecProvider)
}

// End to end through client-go: a hanging plugin no longer blocks a request
// forever — it fails after the shim's timeout, and the plugin is gone.
func TestClientGoRequestWithHangingPluginFails(t *testing.T) {
	plugin, pidFile := script(t, "exec sleep 3600\n")
	self, err := os.Executable()
	require.NoError(t, err)
	cfg := &rest.Config{
		Host: "https://127.0.0.1:1",
		ExecProvider: &clientcmdapi.ExecConfig{
			APIVersion: "client.authentication.k8s.io/v1beta1", Command: plugin,
			InteractiveMode: clientcmdapi.NeverExecInteractiveMode,
		},
	}
	Wrap(cfg, self, 500*time.Millisecond)
	dc, err := dynamic.NewForConfig(cfg)
	require.NoError(t, err)
	start := time.Now()
	_, err = dc.Resource(schema.GroupVersionResource{Version: "v1", Resource: "pods"}).List(context.Background(), metav1.ListOptions{})
	require.Error(t, err)
	assert.Less(t, time.Since(start), 5*time.Second)
	assert.Contains(t, err.Error(), "getting credentials") // the plugin's own stderr goes to the app log
	assert.False(t, alive(t, pidFile))
}
