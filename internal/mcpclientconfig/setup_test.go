package mcpclientconfig

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testTOML = "[mcp_servers.ocular]\nurl = \"http://127.0.0.1:12345/mcp\"\nhttp_headers = { Authorization = \"Bearer fixture-only\" }\n"
const testJSON = `{"mcpServers":{"ocular":{"type":"http","url":"http://127.0.0.1:12345/mcp","headers":{"Authorization":"Bearer fixture-only"}}}}`

func fixture(t *testing.T) (*Manager, string) {
	t.Helper()
	root := t.TempDir()
	return &Manager{Home: func() (string, error) { return root, nil }, Getenv: func(string) string { return "" }, LookPath: func(s string) (string, error) { return filepath.Join(root, s), nil }}, root
}
func save(t *testing.T, path, data string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
}
func text(t *testing.T, path string) string {
	t.Helper()
	b, e := os.ReadFile(path)
	require.NoError(t, e)
	return string(b)
}

func TestInstallPreservesUnrelatedSettingsAndIsIdempotent(t *testing.T) {
	for _, client := range []string{"codex", "claude"} {
		t.Run(client, func(t *testing.T) {
			m, root := fixture(t)
			wanted := testTOML
			old := "# retain this comment\nmodel = \"custom-model\"\n[mcp_servers.other]\ncommand = \"other-tool\"\n"
			if client == "claude" {
				wanted = testJSON
				old = "{\n  \"largeNumber\":123456789012345678901234567890,\n  \"unknown\":{\"spaced\" : [1, 2]},\n  \"mcpServers\": {\"other\": { \"command\" : \"keep-me\" }}\n}\n"
			}
			path, err := m.path(client)
			require.NoError(t, err)
			save(t, path, old)
			before := m.Inspect(client, wanted)
			require.Equal(t, "ready", before.State)
			require.Equal(t, old, text(t, path), "inspection must never write settings")
			after, err := m.Install(t.Context(), client, wanted, before.Revision)
			require.NoError(t, err)
			require.Equal(t, "configured", after.State)
			got := text(t, path)
			if client == "codex" {
				assert.True(t, strings.HasPrefix(got, old))
				var parsed map[string]any
				err = toml.Unmarshal([]byte(got), &parsed)
				require.NoError(t, err)
				assert.Equal(t, "custom-model", parsed["model"])
			} else {
				assert.Contains(t, got, `"largeNumber":123456789012345678901234567890`)
				assert.Contains(t, got, `"unknown":{"spaced" : [1, 2]}`)
				assert.Contains(t, got, `"other": { "command" : "keep-me" }`)
				var parsed map[string]json.RawMessage
				require.NoError(t, json.Unmarshal([]byte(got), &parsed))
			}
			if runtime.GOOS != "windows" {
				info, err := os.Stat(path)
				require.NoError(t, err)
				assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
			}
			_, err = m.Install(t.Context(), client, wanted, after.Revision)
			require.NoError(t, err)
			assert.Equal(t, got, text(t, path))
			paths, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".ocular-mcp-*"))
			require.NoError(t, err)
			assert.Empty(t, paths)
			assert.NotEmpty(t, root)
		})
	}
}

func TestCreateOnlyWhenDetectedAndExplicitlyInstalled(t *testing.T) {
	m, root := fixture(t)
	m.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	for client, wanted := range map[string]string{"codex": testTOML, "claude": testJSON} {
		status := m.Inspect(client, wanted)
		assert.Equal(t, "not_detected", status.State)
		_, err := m.Install(t.Context(), client, wanted, status.Revision)
		require.ErrorIs(t, err, ErrUnavailable)
	}
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	assert.Empty(t, entries)
	m.LookPath = func(s string) (string, error) { return filepath.Join(root, s), nil }
	for client, wanted := range map[string]string{"codex": testTOML, "claude": testJSON} {
		s := m.Inspect(client, wanted)
		require.Equal(t, "ready", s.State)
		_, err = m.Install(t.Context(), client, wanted, s.Revision)
		require.NoError(t, err)
		assert.Equal(t, "configured", m.Inspect(client, wanted).State)
	}
}

func TestRejectsStaleRequestsAndForeignEntriesWithoutOverwriting(t *testing.T) {
	m, _ := fixture(t)
	for client, wanted := range map[string]string{"codex": testTOML, "claude": testJSON} {
		path, _ := m.path(client)
		before := m.Inspect(client, wanted)
		foreign := "[mcp_servers.ocular]\nurl=\"https://foreign.example/mcp\"\n"
		if client == "claude" {
			foreign = `{"mcpServers":{"ocular":{"type":"http","url":"https://foreign.example/mcp"}}}`
		}
		save(t, path, foreign)
		_, err := m.Install(t.Context(), client, wanted, before.Revision)
		require.Error(t, err)
		assert.Equal(t, foreign, text(t, path))
		assert.Equal(t, "conflict", m.Inspect(client, wanted).State)
		clean := "# changed by client\n"
		if client == "claude" {
			clean = `{"changed":true}`
		}
		save(t, path, clean)
		_, err = m.Install(t.Context(), client, wanted, before.Revision)
		require.ErrorIs(t, err, ErrChanged)
		assert.Equal(t, clean, text(t, path))
	}
}

func TestRepairOwnClaudeEntryAddsOnlyMissingType(t *testing.T) {
	m, _ := fixture(t)
	path, _ := m.path("claude")
	old := `{"outside": [1,  2], "mcpServers":{"ocular":{"url":"http://127.0.0.1:12345/mcp","headers":{"Authorization":"Bearer fixture-only","X-Keep":"yes"},"keep":true},"other":{"command":"other"}}}`
	save(t, path, old)
	status := m.Inspect("claude", testJSON)
	require.Equal(t, "ready", status.State)
	_, err := m.Install(t.Context(), "claude", testJSON, status.Revision)
	require.NoError(t, err)
	got := text(t, path)
	assert.Contains(t, got, `"outside": [1,  2]`)
	assert.Contains(t, got, `"X-Keep":"yes"`)
	assert.Contains(t, got, `"other":{"command":"other"}`)
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &parsed))
	entry := parsed["mcpServers"].(map[string]any)["ocular"].(map[string]any)
	assert.Equal(t, "http", entry["type"])
	assert.Equal(t, true, entry["keep"])
}

func TestMalformedOversizedAndManagedFilesStayUntouched(t *testing.T) {
	cases := []struct{ client, old string }{
		{"codex", "[broken"}, {"codex", `mcp_servers = {other = {command = "other"}}`},
		{"claude", `{"mcpServers":{},"mcpServers":{}}`}, {"claude", `{"mcpServers":{"other":{},"other":{}}}`}, {"claude", `{"mcpServers":null}`}, {"claude", `[]`}, {"claude", `{} {}`},
		{"codex", strings.Repeat("#", maxConfigBytes+1)},
	}
	for _, tc := range cases {
		t.Run(tc.client+"/invalid", func(t *testing.T) {
			m, _ := fixture(t)
			path, _ := m.path(tc.client)
			save(t, path, tc.old)
			wanted := testJSON
			if tc.client == "codex" {
				wanted = testTOML
			}
			status := m.Inspect(tc.client, wanted)
			assert.Contains(t, []string{"invalid", "unavailable"}, status.State)
			_, err := m.Install(t.Context(), tc.client, wanted, status.Revision)
			require.Error(t, err)
			assert.Equal(t, tc.old, text(t, path))
		})
	}
}

func TestCustomPathsExistingConfigDetectionAndCancellation(t *testing.T) {
	m, root := fixture(t)
	m.Getenv = func(k string) string {
		if k == "CODEX_HOME" {
			return filepath.Join(root, "custom-codex")
		}
		if k == "CLAUDE_CONFIG_DIR" {
			return filepath.Join(root, "custom-claude")
		}
		return ""
	}
	for client, wanted := range map[string]string{"codex": testTOML, "claude": testJSON} {
		status := m.Inspect(client, wanted)
		assert.Contains(t, status.Path, "custom-"+client)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := m.Install(ctx, client, wanted, status.Revision)
		require.ErrorIs(t, err, context.Canceled)
		_, err = os.Stat(filepath.Dir(status.Path))
		require.True(t, os.IsNotExist(err))
		path, _ := m.path(client)
		old := ""
		if client == "claude" {
			old = "{}"
		}
		save(t, path, old)
		m.LookPath = func(string) (string, error) { return "", errors.New("not found") }
		assert.Equal(t, "ready", m.Inspect(client, wanted).State)
	}
	m.Getenv = func(string) string { return "relative" }
	assert.Equal(t, "unavailable", m.Inspect("codex", testTOML).State)
	assert.Equal(t, "unavailable", m.Inspect("invalid", testTOML).State)
}

func TestSymlinksNeverFollowedOrReplaced(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires a Windows privilege")
	}
	m, root := fixture(t)
	target := filepath.Join(root, "managed.toml")
	save(t, target, "# managed\n")
	path, _ := m.path("codex")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.Symlink(target, path))
	assert.Equal(t, "unavailable", m.Inspect("codex", testTOML).State)
	_, err := m.Install(t.Context(), "codex", testTOML, "revision")
	require.Error(t, err)
	assert.Equal(t, "# managed\n", text(t, target))
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.Remove(filepath.Dir(path)))
	require.NoError(t, os.Symlink(root, filepath.Dir(path)))
	assert.Equal(t, "unavailable", m.Inspect("codex", testTOML).State)
}

func TestConcurrentInstallDoesNotLoseOtherClientSettings(t *testing.T) {
	m, _ := fixture(t)
	path, _ := m.path("codex")
	save(t, path, "# preserve\nmodel=\"local\"\n")
	s := m.Inspect("codex", testTOML)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Go(func() { _, err := m.Install(t.Context(), "codex", testTOML, s.Revision); errs <- err })
	}
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		} else {
			assert.ErrorIs(t, err, ErrChanged)
		}
	}
	assert.GreaterOrEqual(t, successes, 1)
	assert.Equal(t, 1, strings.Count(text(t, path), "[mcp_servers.ocular]"))
	assert.Contains(t, text(t, path), "# preserve\nmodel=\"local\"\n")
}
