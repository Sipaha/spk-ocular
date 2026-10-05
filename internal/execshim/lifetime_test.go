package execshim

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestShimLifetimeChild(_ *testing.T) {
	if os.Getenv("SPK_TEST_LIFETIME_CHILD") != "1" {
		return
	}
	if err := os.WriteFile(os.Getenv("SPK_TEST_LIFETIME_READY"), nil, 0o600); err != nil {
		os.Exit(2)
	}
	time.Sleep(time.Hour)
	os.Exit(0)
}

func TestShimLifetimeCancelsRunningPlugin(t *testing.T) {
	dir := t.TempDir()
	lifetime, ready := filepath.Join(dir, "lifetime"), filepath.Join(dir, "ready")
	require.NoError(t, os.WriteFile(lifetime, nil, 0o600))
	self, err := os.Executable()
	require.NoError(t, err)
	t.Setenv("SPK_TEST_LIFETIME_CHILD", "1")
	t.Setenv("SPK_TEST_LIFETIME_READY", ready)
	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- Main([]string{"--timeout", "10s", "--lifetime", lifetime, "--", self, "-test.run=^TestShimLifetimeChild$"}, strings.NewReader(""), &stdout, &stderr)
	}()
	t.Cleanup(func() { _ = os.Remove(lifetime) })
	require.Eventually(t, func() bool { _, err := os.Stat(ready); return err == nil }, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, os.Remove(lifetime))
	select {
	case code := <-done:
		require.Equal(t, 1, code)
		require.Contains(t, stderr.String(), "connection cancelled")
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled plugin outlived its connection")
	}
}

func TestShimLifetimeRejectsLatePlugin(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	self, err := os.Executable()
	require.NoError(t, err)
	t.Setenv("SPK_TEST_LIFETIME_CHILD", "1")
	t.Setenv("SPK_TEST_LIFETIME_READY", ready)
	var stdout, stderr bytes.Buffer
	code := Main([]string{"--lifetime", filepath.Join(dir, "gone"), "--", self, "-test.run=^TestShimLifetimeChild$"}, strings.NewReader(""), &stdout, &stderr)
	require.Equal(t, 1, code)
	require.Contains(t, stderr.String(), "connection cancelled")
	_, err = os.Stat(ready)
	require.True(t, os.IsNotExist(err), "a cancelled connection must not launch a credential helper")
}
