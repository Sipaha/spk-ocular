package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/spk/spk-ocular/internal/mcpclientconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type setupControl struct {
	AgentControl
	status AgentAccessStatus
	calls  int
}

func (c *setupControl) Status(context.Context) (AgentAccessStatus, error) {
	c.calls++
	return c.status, nil
}

func TestAgentClientSetupIsUIOnlyAndWritesOnlySelectedCurrentConfig(t *testing.T) {
	s, _ := newService(t, newAgentProvider("a"))
	root := t.TempDir()
	s.agentClientConfigs = &mcpclientconfig.Manager{Home: func() (string, error) { return root, nil }, Getenv: func(string) string { return "" }, LookPath: func(name string) (string, error) { return filepath.Join(root, name), nil }}
	control := &setupControl{status: AgentAccessStatus{State: AgentServing, CodexConfig: "[mcp_servers.ocular]\nurl=\"http://127.0.0.1:12345/mcp\"\nhttp_headers={Authorization=\"Bearer fixture-only\"}\n", ClaudeConfig: `{"mcpServers":{"ocular":{"type":"http","url":"http://127.0.0.1:12345/mcp","headers":{"Authorization":"Bearer fixture-only"}}}}`}}
	s.SetAgentControl(control)
	for _, install := range []bool{false, true} {
		_, err := s.AgentClientSetup(t.Context(), AgentClientSetupRequest{Client: "codex", Install: install, Revision: "revision"})
		require.True(t, IsCoded(err, "forbidden"))
	}
	call, err := s.AgentCall(UIContext(t.Context()), "k", "a")
	require.NoError(t, err)
	defer call.Done()
	_, err = s.AgentClientSetup(call.ctx, AgentClientSetupRequest{})
	require.True(t, IsCoded(err, "forbidden"))
	assert.Zero(t, control.calls)
	view, err := s.AgentClientSetup(UIContext(t.Context()), AgentClientSetupRequest{})
	require.NoError(t, err)
	require.Len(t, view.Clients, 2)
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	assert.Empty(t, entries)
	updated, err := s.AgentClientSetup(UIContext(t.Context()), AgentClientSetupRequest{Client: "codex", Install: true, Revision: view.Clients[0].Revision})
	require.NoError(t, err)
	assert.Equal(t, "configured", updated.Clients[0].State)
	_, err = os.Stat(filepath.Join(root, ".codex", "config.toml"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(root, ".claude.json"))
	require.True(t, os.IsNotExist(err))
	_, err = s.AgentClientSetup(UIContext(t.Context()), AgentClientSetupRequest{Client: "codex", Install: true, Revision: view.Clients[0].Revision})
	require.True(t, IsCoded(err, CodeConflict))
	for _, req := range []AgentClientSetupRequest{{Client: "invalid"}, {Install: true}, {Client: "codex", Install: true}} {
		_, err = s.AgentClientSetup(UIContext(t.Context()), req)
		require.Error(t, err)
	}
	control.status.State = AgentFailed
	_, err = s.AgentClientSetup(UIContext(t.Context()), AgentClientSetupRequest{})
	require.True(t, IsCoded(err, CodeUnsupported))
}
