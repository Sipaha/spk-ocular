package mcpclientconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This explicit local audit reads only disposable configs in a read-only host
// mount and a private network namespace. It never launches a model or signs in.
func TestInstalledClientConfigurationReaders(t *testing.T) {
	if os.Getenv("OCULAR_MCP_CLIENT_READERS") != "1" {
		t.Skip("opt in to installed client configuration readers")
	}
	for _, name := range []string{"bwrap", "codex", "claude"} {
		_, err := exec.LookPath(name)
		require.NoError(t, err)
	}
	root := t.TempDir()
	for _, dir := range []string{"home", "codex", "claude", "tmp", "cache", "config", "share", "state"} {
		require.NoError(t, os.Mkdir(filepath.Join(root, dir), 0o700))
	}
	m := New(func(key string) string {
		switch key {
		case "CODEX_HOME":
			return filepath.Join(root, "codex")
		case "CLAUDE_CONFIG_DIR":
			return filepath.Join(root, "claude")
		}
		return ""
	})
	m.Home = func() (string, error) { return filepath.Join(root, "home"), nil }
	codex, _ := m.path("codex")
	claude, _ := m.path("claude")
	save(t, codex, "# preserve reader audit\nmodel=\"gpt-6\"\n[mcp_servers.fixture_existing]\nurl=\"http://127.0.0.1:1/other\"\n")
	save(t, claude, `{"numStartups":0,"mcpServers":{"fixture_existing":{"type":"http","url":"http://127.0.0.1:1/other"}}}`)
	// fixture-only is not a valid Ocular token; no server is listening in the namespace.
	for client, wanted := range map[string]string{"codex": testTOML, "claude": testJSON} {
		status := m.Inspect(client, wanted)
		require.Equal(t, "ready", status.State)
		_, err := m.Install(t.Context(), client, wanted, status.Revision)
		require.NoError(t, err)
	}
	run := func(client string, args ...string) []byte {
		t.Helper()
		binary, err := exec.LookPath(client)
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, "bwrap", append([]string{"--unshare-user", "--ro-bind", "/", "/", "--bind", root, root, "--unshare-net", "--", binary}, args...)...)
		command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Join(root, "home"), "CODEX_HOME=" + filepath.Join(root, "codex"), "CLAUDE_CONFIG_DIR=" + filepath.Join(root, "claude"), "TMPDIR=" + filepath.Join(root, "tmp"), "XDG_CACHE_HOME=" + filepath.Join(root, "cache"), "XDG_CONFIG_HOME=" + filepath.Join(root, "config"), "XDG_DATA_HOME=" + filepath.Join(root, "share"), "XDG_STATE_HOME=" + filepath.Join(root, "state"), "LANG=C.UTF-8", "DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
		var stderr bytes.Buffer
		command.Stderr = &stderr
		out, err := command.Output()
		require.NoError(t, err, "isolated configuration reader failed: %s", stderr.String())
		return out
	}
	var entries []struct {
		Name      string `json:"name"`
		Transport struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"http_headers"`
		} `json:"transport"`
	}
	require.NoError(t, json.Unmarshal(run("codex", "mcp", "list", "--json"), &entries))
	require.Len(t, entries, 2)
	found := false
	for _, entry := range entries {
		if entry.Name == "ocular" {
			found = true
			assert.Equal(t, "http://127.0.0.1:12345/mcp", entry.Transport.URL)
			assert.Equal(t, "Bearer fixture-only", entry.Transport.Headers["Authorization"])
		}
	}
	assert.True(t, found)
	result := string(run("claude", "mcp", "get", "ocular"))
	assert.Contains(t, result, "http://127.0.0.1:12345/mcp")
	assert.Contains(t, result, "http")
	assert.Contains(t, result, "ocular")
	t.Log("installed Codex and Claude Code accepted both generated client entries; unrelated server retained; host read-only and network isolated")
}
