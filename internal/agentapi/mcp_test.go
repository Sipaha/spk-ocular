package agentapi

import (
	"context"
	"encoding/json"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spk/spk-ocular/internal/agentgrant"
	"github.com/spk/spk-ocular/internal/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}
func connectMCP(t *testing.T, s *Server, name string, opts *mcp.ClientOptions) *mcp.ClientSession {
	t.Helper()
	require.NoError(t, s.initErr)
	s.mu.Lock()
	endpoint, token := s.endpoint, s.token
	s.mu.Unlock()
	transport := &http.Transport{Proxy: nil}
	t.Cleanup(transport.CloseIdleConnections)
	if endpoint == "" {
		server := httptest.NewServer(s.Handler())
		t.Cleanup(server.Close)
		endpoint = server.URL + "/mcp"
	}
	client := mcp.NewClient(&mcp.Implementation{Name: name, Version: "test"}, opts)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: &http.Client{Transport: bearerTransport{token, transport}}}, &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestMCPToolsAndGrantBoundary(t *testing.T) {
	e := newEnv(t)
	client := connectMCP(t, e.srv, "MCP fixture", nil)
	instructions := client.InitializeResult().Instructions
	assert.Contains(t, instructions, "Kubernetes")
	assert.Contains(t, instructions, "Docker/Compose")
	assert.Contains(t, instructions, "bounded logs and metrics")
	assert.Contains(t, instructions, "Use Access first")
	assert.Contains(t, instructions, "Never retry an uncertain write")
	assert.NotContains(t, instructions, e.srv.token)
	_, setupErr := client.CallTool(t.Context(), &mcp.CallToolParams{Name: "AgentClientSetup", Arguments: map[string]any{"client": "codex", "install": true}})
	require.Error(t, setupErr, "client configuration is a trusted UI operation, never an MCP tool")
	listed, err := client.ListTools(t.Context(), nil)
	require.NoError(t, err)
	names := []string{}
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
		assert.NotNil(t, tool.InputSchema)
		assert.NotEmpty(t, tool.Description)
	}
	assert.ElementsMatch(t, []string{"Access", "ListKinds", "ListObjects", "GetObject", "Problems", "GetMetrics", "GetLogInfo", "GetLogs", "StreamLogs", "ExportLogs", "PrepareAction", "RunAction", "GetEditSource", "PrepareEdit", "RunEdit", "GetRun", "GetExecInfo", "PrepareExec", "RunExec"}, names)
	call := func(name string, args any) *mcp.CallToolResult {
		out, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
		require.NoError(t, err)
		return out
	}
	assert.False(t, call("Access", struct{}{}).IsError)
	assert.True(t, call("ListObjects", ListObjectsRequest{TargetRef: TargetRef{Provider: "k", Target: "t"}, Kind: "pods", Scope: "a"}).IsError)
	e.grant(one("a", agentgrant.VerbRead, "pods"))
	out := call("ListObjects", ListObjectsRequest{TargetRef: TargetRef{Provider: "k", Target: "t"}, Kind: "pods", Scope: "a"})
	require.False(t, out.IsError)
	data, err := json.Marshal(out.StructuredContent)
	require.NoError(t, err)
	assert.Contains(t, string(data), "web-1")
	assert.NotContains(t, string(data), "api-1")
	assert.True(t, call("ListObjects", map[string]any{"provider": "k", "target": "t", "kind": "pods", "scope": "a", "unexpected": true}).IsError)
	require.NoError(t, e.svc.RevokeAllAgentGrants(t.Context()))
	assert.True(t, call("ListObjects", ListObjectsRequest{TargetRef: TargetRef{Provider: "k", Target: "t"}, Kind: "pods", Scope: "a"}).IsError)
	_, err = client.CallTool(t.Context(), &mcp.CallToolParams{Name: "SaveAgentGrants", Arguments: struct{}{}})
	assert.Error(t, err, "UI-only operations are not tools")
}

func TestMCPRejectsOldRoutesAndNetworkAdmission(t *testing.T) {
	e := newEnv(t)
	server := httptest.NewServer(e.srv.Handler())
	defer server.Close()
	for _, test := range []struct {
		name, path, host, origin, auth string
		status                         int
	}{
		{"missing credential", "/mcp", "", "", "", 401},
		{"wrong credential", "/mcp", "", "", "Bearer wrong", 401},
		{"remote host", "/mcp", "evil.invalid:80", "", "Bearer " + e.srv.token, 403},
		{"foreign origin", "/mcp", "", "https://evil.invalid", "Bearer " + e.srv.token, 403},
		{"null origin", "/mcp", "", "null", "Bearer " + e.srv.token, 403},
		{"query credential", "/mcp?token=not-a-credential", "", "", "Bearer " + e.srv.token, 400},
		{"legacy intro", "/v1", "", "", "Bearer " + e.srv.token, 404},
		{"legacy catalogue", "/v1/methods", "", "", "Bearer " + e.srv.token, 404},
		{"legacy operation", "/v1/Access", "", "", "Bearer " + e.srv.token, 404},
	} {
		t.Run(test.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+test.path, strings.NewReader(`{}`))
			require.NoError(t, err)
			if test.host != "" {
				req.Host = test.host
			}
			if test.origin != "" {
				req.Header.Set("Origin", test.origin)
			}
			if test.auth != "" {
				req.Header.Set("Authorization", test.auth)
			}
			response, err := server.Client().Do(req)
			require.NoError(t, err)
			defer func() { _ = response.Body.Close() }()
			assert.Equal(t, test.status, response.StatusCode)
			assert.Equal(t, "no-store", response.Header.Get("Cache-Control"))
			assert.Empty(t, response.Header.Get("Access-Control-Allow-Origin"))
		})
	}
}

func mcpObject(t *testing.T, client *mcp.ClientSession, name string, args any) map[string]any {
	t.Helper()
	out, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	require.False(t, out.IsError, "tool %s failed", name)
	b, err := json.Marshal(out.StructuredContent)
	require.NoError(t, err)
	var value map[string]any
	require.NoError(t, json.Unmarshal(b, &value))
	return value
}
func TestMCPReviewedWriteUsesOcularConfirmationAndOneShotPlan(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", "action:delete", "apps/deployments"))
	client := connectMCP(t, e.srv, "MCP write fixture", nil)
	p := mcpObject(t, client, "PrepareAction", map[string]any{"ref": ref("a", "apps/deployments", "web"), "action": "delete"})
	run := mcpObject(t, client, "RunAction", map[string]any{"planId": p["planId"]})
	require.Equal(t, stateAwaiting, run["state"])
	assert.Empty(t, e.runs())
	require.NoError(t, e.svc.DecideAgentPending(context.Background(), api.DecideAgentPendingRequest{ID: run["runId"].(string), Approve: true}))
	result := mcpObject(t, client, "GetRun", map[string]any{"runId": run["runId"]})
	assert.Equal(t, stateDone, result["state"])
	assert.Len(t, e.runs(), 1)
	duplicate, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: "RunAction", Arguments: map[string]any{"planId": p["planId"]}})
	require.NoError(t, err)
	assert.True(t, duplicate.IsError)
	assert.Len(t, e.runs(), 1)
}

func TestMCPFiniteLogProgressAndRevocation(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", agentgrant.VerbLogs, "pods"))
	progress := make(chan string, 64)
	client := connectMCP(t, e.srv, "log fixture", &mcp.ClientOptions{ProgressNotificationHandler: func(_ context.Context, r *mcp.ProgressNotificationClientRequest) { progress <- r.Params.Message }})
	e.f.logs = func(_ context.Context, _ core.Ref, _ provider.LogQuery, sink provider.LogSink) error {
		if err := sink.Source(1, "source", "fixture/pod/app", "app"); err != nil {
			return err
		}
		if err := sink.Lines(1, []provider.LogLine{{Text: "private log fixture"}}); err != nil {
			return err
		}
		return nil
	}
	params := &mcp.CallToolParams{Name: "StreamLogs", Arguments: StreamLogsRequest{GetLogsRequest: GetLogsRequest{Ref: ref("a", "pods", "web-1"), Limit: 10}}}
	params.SetProgressToken("capture")
	out, err := client.CallTool(t.Context(), params)
	require.NoError(t, err)
	require.False(t, out.IsError)
	encoded, err := json.Marshal(out.StructuredContent)
	require.NoError(t, err)
	var captured LogStreamResult
	require.NoError(t, json.Unmarshal(encoded, &captured))
	require.NotEmpty(t, captured.Frames)
	assert.Equal(t, "end", captured.Frames[len(captured.Frames)-1].Type)
	assert.True(t, captured.Frames[len(captured.Frames)-1].Complete)
	for range len(captured.Frames) {
		select {
		case message := <-progress:
			var frame LogFrame
			require.NoError(t, json.Unmarshal([]byte(message), &frame))
		case <-time.After(time.Second):
			t.Fatal("MCP progress frame was not delivered")
		}
	}

	started := make(chan struct{})
	stopped := make(chan struct{})
	e.f.logs = func(ctx context.Context, _ core.Ref, _ provider.LogQuery, sink provider.LogSink) error {
		defer close(stopped)
		close(started)
		_ = sink.Source(1, "source", "fixture/pod/app", "app")
		_ = sink.Lines(1, []provider.LogLine{{Text: "buffered before revocation"}})
		<-ctx.Done()
		return ctx.Err()
	}
	result := make(chan *mcp.CallToolResult, 1)
	go func() {
		out, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: "StreamLogs", Arguments: StreamLogsRequest{GetLogsRequest: GetLogsRequest{Ref: ref("a", "pods", "web-1"), Limit: 10}, Follow: true}})
		if err != nil {
			result <- nil
		} else {
			result <- out
		}
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("MCP log read did not start")
	}
	e.grant()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("revoked MCP read stayed active")
	}
	select {
	case out := <-result:
		require.NotNil(t, out)
		require.True(t, out.IsError)
		bytes, err := json.Marshal(out)
		require.NoError(t, err)
		assert.NotContains(t, string(bytes), "buffered before revocation")
	case <-time.After(time.Second):
		t.Fatal("revoked MCP capture did not finish")
	}
}

func TestMCPHTTPDisconnectCancelsAttachedRead(t *testing.T) {
	e := newEnv(t)
	require.NoError(t, e.srv.Start())
	e.grant(one("a", agentgrant.VerbLogs, "pods"))
	client := connectMCP(t, e.srv, "disconnect fixture", nil)
	started, stopped := make(chan struct{}), make(chan struct{})
	e.f.logs = func(ctx context.Context, _ core.Ref, _ provider.LogQuery, sink provider.LogSink) error {
		defer close(stopped)
		close(started)
		_ = sink.Source(1, "source", "fixture/pod/app", "app")
		<-ctx.Done()
		return ctx.Err()
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "disconnect-capture", "method": "tools/call", "params": map[string]any{"name": "StreamLogs", "arguments": StreamLogsRequest{GetLogsRequest: GetLogsRequest{Ref: ref("a", "pods", "web-1"), Limit: 10}, Follow: true}, "_meta": map[string]any{"progressToken": "disconnect"}}})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, e.srv.endpoint, strings.NewReader(string(body)))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Mcp-Session-Id", client.ID())
	request.Header.Set("MCP-Protocol-Version", "2025-11-25")
	request.Header.Set("Authorization", "Bearer "+e.srv.token)
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport}).Do(request)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("capture did not reach provider")
	}
	cancel()
	_ = response.Body.Close()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("disconnected capture kept reading")
	}
}

func TestMCPBusyToolsDoNotBlockCancellationOrSessionDeletion(t *testing.T) {
	e := newEnv(t)
	require.NoError(t, e.srv.Start())
	client := connectMCP(t, e.srv, "admission fixture", nil)
	for range cap(e.srv.requests) {
		e.srv.requests <- struct{}{}
	}
	defer func() {
		for len(e.srv.requests) > 0 {
			<-e.srv.requests
		}
	}()
	send := func(method string, body string) *http.Response {
		request, err := http.NewRequestWithContext(t.Context(), method, e.srv.endpoint, strings.NewReader(body))
		require.NoError(t, err)
		request.Header.Set("Authorization", "Bearer "+e.srv.token)
		request.Header.Set("Mcp-Session-Id", client.ID())
		request.Header.Set("MCP-Protocol-Version", "2025-11-25")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		return response
	}
	response := send(http.MethodPost, `{"jsonrpc":"2.0","id":"busy","method":"tools/list"}`)
	assert.Equal(t, http.StatusTooManyRequests, response.StatusCode)
	_ = response.Body.Close()
	response = send(http.MethodPost, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"not-running"}}`)
	assert.Equal(t, http.StatusAccepted, response.StatusCode)
	_ = response.Body.Close()
	response = send(http.MethodDelete, "")
	assert.Equal(t, http.StatusNoContent, response.StatusCode)
	_ = response.Body.Close()
}

func TestMCPBoundsSessions(t *testing.T) {
	e := newEnv(t)
	server := httptest.NewServer(e.srv.Handler())
	defer server.Close()
	clients := []*mcp.ClientSession{}
	defer func() {
		for _, client := range clients {
			_ = client.Close()
		}
	}()
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	connect := func() (*mcp.ClientSession, error) {
		return mcp.NewClient(&mcp.Implementation{Name: "bounded client", Version: "1"}, nil).Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{e.srv.token, transport}}}, &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
	}
	for range maxMCPSessions {
		client, err := connect()
		require.NoError(t, err)
		clients = append(clients, client)
	}
	_, err := connect()
	assert.Error(t, err)
	require.NoError(t, clients[0].Close())
	clients = clients[1:]
	client, err := connect()
	require.NoError(t, err)
	clients = append(clients, client)
}

func TestMCPCurrentSDKNegotiatesSupportedProtocol(t *testing.T) {
	e := newEnv(t)
	server := httptest.NewServer(e.srv.Handler())
	defer server.Close()
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := mcp.NewClient(&mcp.Implementation{Name: "current SDK default", Version: "test"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{e.srv.token, transport}}}, nil)
	require.NoError(t, err)
	defer func() { _ = session.Close() }()
	tools, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	assert.Len(t, tools.Tools, 19)
}
