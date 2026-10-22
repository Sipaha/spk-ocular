package agentapi

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPEndpointOwnershipRestartAndCollision(t *testing.T) {
	profile := t.TempDir()
	ln, lock, config, err := openEndpoint(profile)
	require.NoError(t, err)
	require.Contains(t, ln.Addr().String(), "127.0.0.1:")
	_, _, _, err = openEndpoint(profile)
	assert.ErrorIs(t, err, ErrOtherInstance)
	data, err := os.ReadFile(filepath.Join(profile, "mcp.json"))
	require.NoError(t, err)
	info, err := os.Stat(filepath.Join(profile, "mcp.json"))
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
	address := ln.Addr().String()
	require.NoError(t, ln.Close())
	require.NoError(t, lock.Unlock())
	foreign, err := net.Listen("tcp4", address)
	require.NoError(t, err)
	_, _, _, err = openEndpoint(profile)
	require.Error(t, err)
	after, err := os.ReadFile(filepath.Join(profile, "mcp.json"))
	require.NoError(t, err)
	assert.Equal(t, data, after, "collision cannot rewrite connection settings")
	require.NoError(t, foreign.Close())
	second, secondLock, same, err := openEndpoint(profile)
	require.NoError(t, err)
	defer func() { _ = second.Close(); _ = secondLock.Unlock() }()
	assert.Equal(t, config, same)
	assert.Equal(t, address, second.Addr().String())
	other, otherLock, otherConfig, err := openEndpoint(t.TempDir())
	require.NoError(t, err)
	defer func() { _ = other.Close(); _ = otherLock.Unlock() }()
	assert.NotEqual(t, config.URL, otherConfig.URL)
	assert.NotEqual(t, config.Token, otherConfig.Token)
}

func TestMCPEndpointRefusesMalformedOrNonlocalConfiguration(t *testing.T) {
	for _, config := range []string{`{}`, `{"url":"http://0.0.0.0:10000/mcp","token":"invalid"}`, `{"url":"http://127.0.0.1:0/mcp","token":"invalid"}`, `{"url":"http://127.0.0.1:1234/mcp?token=x","token":"invalid"}`} {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(config), 0o600))
		_, _, _, err := openEndpoint(dir)
		require.Error(t, err)
	}
}

func TestMCPServerCloseReleasesPortAndProfileOwnership(t *testing.T) {
	e := newEnv(t)
	e.srv.o.Profile = t.TempDir()
	require.NoError(t, e.srv.Start())
	status, err := e.srv.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, api.AgentServing, status.State)
	client := connectMCP(t, e.srv, "lifecycle", nil)
	out, err := client.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Len(t, out.Tools, 19)
	var copied struct {
		Servers map[string]struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal([]byte(status.ClientConfig), &copied))
	assert.Equal(t, status.Endpoint, copied.Servers["ocular"].URL)
	assert.NotContains(t, status.Instruction, e.srv.token)
	e.srv.Close()
	_, err = client.ListTools(t.Context(), nil)
	assert.Error(t, err)
	next := New(Options{Service: e.svc, Store: e.st, Profile: e.srv.o.Profile, Version: "test"})
	defer next.Close()
	require.NoError(t, next.Start())
	same, err := next.Status(t.Context())
	require.NoError(t, err)
	assert.Equal(t, status.Endpoint, same.Endpoint)
	assert.Equal(t, status.ClientConfig, same.ClientConfig)
}
