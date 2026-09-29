package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/paths"
)

func newTestCore(t *testing.T) *appCore {
	t.Helper()
	t.Setenv(paths.EnvHome, t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KUBECONFIG", "")
	c, err := newCore(context.Background(), "browser")
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
