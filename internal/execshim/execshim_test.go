//go:build !windows

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

// guiEnv: what a plugin could reach a person with.
var guiEnv = map[string]string{
	"DISPLAY": ":9", "WAYLAND_DISPLAY": "wayland-9", "DBUS_SESSION_BUS_ADDRESS": "unix:path=/x",
	"BROWSER": "firefox", "GPG_TTY": "/dev/pts/9", "SSH_ASKPASS": "/usr/bin/askpass",
}

const seesAll = `echo "stdin=$(cat)"; for v in DISPLAY WAYLAND_DISPLAY DBUS_SESSION_BUS_ADDRESS BROWSER GPG_TTY SSH_ASKPASS KEEP; do echo "$v=$(printenv $v)"; done; echo "info=$KUBERNETES_EXEC_INFO"`

// Under a hold (the session is in the background) the plugin runs
// headless: nothing to ask on stdin, nowhere to open a browser, told it is
// not interactive. Without the hold file it gets everything, as before.
func TestShimRunsThePluginHeadlessUnderAHold(t *testing.T) {
	plugin, _ := script(t, seesAll+"\n")
	for k, v := range guiEnv {
		t.Setenv(k, v)
	}
	t.Setenv("KEEP", "kept")
	t.Setenv("KUBERNETES_EXEC_INFO", `{"apiVersion":"client.authentication.k8s.io/v1","kind":"ExecCredential","spec":{"interactive":true,"cluster":{"server":"https://x"}}}`)
	hold := filepath.Join(t.TempDir(), "bg-1")

	var out, errb bytes.Buffer
	code := Main([]string{"--timeout", "5s", "--hold", hold, "--", plugin}, strings.NewReader("typed\n"), &out, &errb)
	require.Equal(t, 0, code, errb.String())
	assert.Contains(t, out.String(), "stdin=typed\nDISPLAY=:9\n", "no hold file: in the foreground")
	assert.Contains(t, out.String(), `"interactive":true`)

	require.NoError(t, os.WriteFile(hold, nil, 0o600))
	out.Reset()
	code = Main([]string{"--timeout", "5s", "--hold", hold, "--", plugin}, strings.NewReader("typed\n"), &out, &errb)
	require.Equal(t, 0, code, errb.String())
	assert.Equal(t, "stdin=\nDISPLAY=\nWAYLAND_DISPLAY=\nDBUS_SESSION_BUS_ADDRESS=\nBROWSER=\nGPG_TTY=\nSSH_ASKPASS=\nKEEP=kept\n"+
		`info={"apiVersion":"client.authentication.k8s.io/v1","kind":"ExecCredential","spec":{"cluster":{"server":"https://x"},"interactive":false}}`+"\n", out.String())
}

// A plugin that would open a browser for a login ends fast under a hold:
// no display, and a short deadline if it waits anyway.
func TestShimUnderAHoldEndsALoginFast(t *testing.T) {
	opener, _ := script(t, `if [ -n "$DISPLAY" ]; then exec sleep 3600; fi; echo "cannot open a browser" >&2; exit 1`+"\n")
	waiter, pidFile := script(t, "exec sleep 3600\n") // prints a URL and waits for its callback
	t.Setenv("DISPLAY", ":9")
	hold := filepath.Join(t.TempDir(), "bg-1")
	require.NoError(t, os.WriteFile(hold, nil, 0o600))

	var out, errb bytes.Buffer
	start := time.Now()
	assert.Equal(t, 1, Main([]string{"--timeout", "5s", "--hold", hold, "--", opener}, strings.NewReader(""), &out, &errb))
	assert.Less(t, time.Since(start), 2*time.Second)

	start = time.Now()
	assert.Equal(t, 1, Main([]string{"--timeout", "5s", "--hold", hold, "--hold-timeout", "300ms", "--", waiter}, strings.NewReader(""), &out, &errb))
	assert.Less(t, time.Since(start), 3*time.Second)
	assert.Contains(t, errb.String(), "in the background")
	assert.False(t, alive(t, pidFile))
}

func TestWrapPassesTheHold(t *testing.T) {
	cfg := &rest.Config{ExecProvider: &clientcmdapi.ExecConfig{Command: "/usr/bin/yc", Args: []string{"k8s", "create-token"}}}
	WrapHeld(cfg, "/opt/ocular", time.Minute, "/data/tmp/bg-1")
	assert.Equal(t, []string{Subcommand, "--timeout", "1m0s", "--hold", "/data/tmp/bg-1", "--", "/usr/bin/yc", "k8s", "create-token"}, cfg.ExecProvider.Args)
}

// Through client-go: a background request whose plugin needs a person
// fails fast instead of waiting for a login.
func TestClientGoRequestUnderAHoldFailsFast(t *testing.T) {
	plugin, _ := script(t, `if [ -n "$DISPLAY" ]; then exec sleep 3600; fi; exit 1`+"\n")
	t.Setenv("DISPLAY", ":9")
	self, err := os.Executable()
	require.NoError(t, err)
	hold := filepath.Join(t.TempDir(), "bg-1")
	require.NoError(t, os.WriteFile(hold, nil, 0o600))
	cfg := &rest.Config{
		Host: "https://127.0.0.1:1",
		ExecProvider: &clientcmdapi.ExecConfig{
			APIVersion: "client.authentication.k8s.io/v1beta1", Command: plugin,
			InteractiveMode: clientcmdapi.NeverExecInteractiveMode,
		},
	}
	WrapHeld(cfg, self, time.Minute, hold)
	dc, err := dynamic.NewForConfig(cfg)
	require.NoError(t, err)
	start := time.Now()
	_, err = dc.Resource(schema.GroupVersionResource{Version: "v1", Resource: "pods"}).List(context.Background(), metav1.ListOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "getting credentials")
	assert.Less(t, time.Since(start), 5*time.Second)
}
