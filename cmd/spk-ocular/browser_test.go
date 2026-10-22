package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/paths"
)

func newTestCore(t *testing.T) *appCore {
	t.Helper()
	t.Setenv(paths.EnvHome, shortHome(t))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KUBECONFIG", "")
	c, err := newCore(context.Background(), "browser", false)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return c
}

var dist = fstest.MapFS{"index.html": {Data: []byte("<html><head></head><body></body></html>")}}

func TestIndexCarriesTokenAndIsNotCached(t *testing.T) {
	h, token := newBrowserHandler(newTestCore(t), dist, false)
	srv := httptest.NewServer(h)
	defer srv.Close()
	for _, p := range []string{"/", "/some/spa/route"} {
		resp, err := http.Get(srv.URL + p)
		require.NoError(t, err)
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		assert.Contains(t, string(body), `<meta name="spk-ocular-api-token" content="`+token+`">`, p)
		assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
	}
}

func TestNonLoopbackHostIsRejected(t *testing.T) {
	h, _ := newBrowserHandler(newTestCore(t), dist, false)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "evil.example:5190"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.NotContains(t, rec.Body.String(), "api-token")
}

func TestTestAPIOnlyWithFlagAndToken(t *testing.T) {
	c := newTestCore(t)
	h, token := newBrowserHandler(c, dist, false)
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/_test/paths?token=" + token)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.NotEqual(t, http.StatusOK, resp.StatusCode, "no --test-api: no test routes")

	h, token = newBrowserHandler(c, dist, true)
	srv2 := httptest.NewServer(h)
	defer srv2.Close()
	resp, err = http.Get(srv2.URL + "/api/_test/paths")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp, err = http.Get(srv2.URL + "/api/_test/paths?token=" + token)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, strings.Contains(string(body), c.Paths.DataDir))
}

// shortHome creates an isolated application profile under the configured scratch root.
func shortHome(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp(os.Getenv("TMPDIR"), "oc")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

// syncBuffer is a log sink written by the app's goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// Browser mode owns its in-process MCP port and never logs either credential.
func TestBrowserModeServesMCP(t *testing.T) {
	home := shortHome(t)
	t.Setenv(paths.EnvHome, home)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KUBECONFIG", "")
	logs := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serveBrowser(ctx, browserOpts{Port: port}, dist) }()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	var page string
	require.Eventually(t, func() bool {
		resp, err := http.Get(base + "/")
		if err != nil {
			return false
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		page = string(b)
		return resp.StatusCode == http.StatusOK
	}, 10*time.Second, 50*time.Millisecond)
	m := regexp.MustCompile(`spk-ocular-api-token" content="([^"]+)"`).FindStringSubmatch(page)
	require.Len(t, m, 2, "the page carries the token")
	token := m[1]

	configBytes, err := os.ReadFile(filepath.Join(home, "mcp.json"))
	require.NoError(t, err)
	var config struct{ URL, Token string }
	require.NoError(t, json.Unmarshal(configBytes, &config))
	require.True(t, strings.HasPrefix(config.URL, "http://127.0.0.1:"))
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, config.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"browser-test","version":"1"}}}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+config.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(body), "SPK Ocular")
	st, err := os.Stat(filepath.Join(home, "mcp.json"))
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		assert.Equal(t, os.FileMode(0o600), st.Mode().Perm())
	}

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("browser mode did not stop")
	}
	_, err = http.Post(config.URL, "application/json", strings.NewReader(`{}`))
	assert.Error(t, err, "MCP port closes on application exit")
	assert.Contains(t, logs.String(), config.URL)
	assert.NotContains(t, logs.String(), token, "the page token never reaches the log")
	assert.NotContains(t, logs.String(), config.Token, "the MCP credential never reaches the log")
}
