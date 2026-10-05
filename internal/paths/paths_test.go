package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveHonorsEnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvHome, dir)
	p, err := Resolve()
	require.NoError(t, err)
	assert.Equal(t, dir, p.DataDir)
	assert.Equal(t, filepath.Join(dir, "ocular.db"), p.DBFile)
	assert.Equal(t, filepath.Join(dir, "tmp"), p.TmpDir)
	assert.Equal(t, AgentEndpoint(dir), p.AgentSocket)
	assert.Equal(t, filepath.Join(dir, "agent.sock.lock"), p.AgentLock)
}

func TestResolveDefaultsToSpkOcular(t *testing.T) {
	home := t.TempDir()
	t.Setenv(EnvHome, "")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	p, err := Resolve()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".spk", "ocular"), p.DataDir)
}

func TestEnsureCreatesOwnerOnlyDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "ocular")
	p := Paths{DataDir: dir}
	require.NoError(t, p.Ensure())
	st, err := os.Stat(dir)
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		assert.Equal(t, os.FileMode(0o700), st.Mode().Perm())
	}
}
