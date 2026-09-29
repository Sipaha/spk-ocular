package transport

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOriginMatchesHost(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		host   string
		want   bool
	}{
		{"exact match", "http://127.0.0.1:5174", "127.0.0.1:5174", true},
		{"localhost vs 127.0.0.1 same port", "http://localhost:5174", "127.0.0.1:5174", true},
		{"127.0.0.1 vs localhost same port", "http://127.0.0.1:5174", "localhost:5174", true},
		{"localhost vs IPv6 loopback", "http://localhost:5174", "[::1]:5174", true},
		{"IPv6 loopback vs localhost", "http://[::1]:5174", "localhost:5174", true},

		{"different port — even on loopback alias", "http://localhost:5174", "127.0.0.1:9999", false},
		{"prefix-match bypass attempt", "http://localhostevil:5174", "127.0.0.1:5174", false},
		{"prefix-match bypass attempt (127.0.0.1.evil)", "http://127.0.0.1.evil:5174", "127.0.0.1:5174", false},
		{"foreign cross-origin", "https://attacker.example", "127.0.0.1:5174", false},
		{"empty origin", "", "127.0.0.1:5174", false},
		{"malformed origin", "::not-a-url", "127.0.0.1:5174", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := originMatchesHost(c.origin, c.host)
			require.Equalf(t, c.want, got, "origin=%q host=%q", c.origin, c.host)
		})
	}
}

func TestOriginGuard_BlocksCrossOrigin(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(OriginGuard(inner))
	defer srv.Close()

	// POST without Origin or Referer → 403 (state-changing requests must
	// carry one of the two so a CSRF attacker can't hide behind a missing
	// header).
	req, _ := http.NewRequest("POST", srv.URL+"/", strings.NewReader(""))
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	// GET without Origin → 200 (read-only, no CSRF risk).
	req, _ = http.NewRequest("GET", srv.URL+"/", nil)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode)

	// Foreign Origin → 403.
	req, _ = http.NewRequest("POST", srv.URL+"/", strings.NewReader(""))
	req.Header.Set("Origin", "https://attacker.example")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	// Same-origin Origin → 200.
	req, _ = http.NewRequest("POST", srv.URL+"/", strings.NewReader(""))
	req.Header.Set("Origin", srv.URL)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode)

	// Origin absent but matching Referer → 200 (legacy WebView fallback).
	req, _ = http.NewRequest("POST", srv.URL+"/", strings.NewReader(""))
	req.Header.Set("Referer", srv.URL+"/page")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode)

	// Origin absent but Referer is foreign → 403.
	req, _ = http.NewRequest("POST", srv.URL+"/", strings.NewReader(""))
	req.Header.Set("Referer", "https://attacker.example/page")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	// Loopback alias accept path at the middleware layer: Origin:
	// http://localhost:PORT against the test server's 127.0.0.1:PORT must
	// pass the Hostname()-equivalent alias map (no prefix match).
	parsed, _ := url.Parse(srv.URL)
	_, port, _ := strings.Cut(parsed.Host, ":")
	req, _ = http.NewRequest("POST", srv.URL+"/", strings.NewReader(""))
	req.Header.Set("Origin", "http://localhost:"+port)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode)
}

// TestAuthGuard covers the AuthGuard middleware used for the /api/_test/*
// mux: the Authorization header works for both GET and POST, the ?token=
// query fallback works only for GET/HEAD, mutating (POST) requests must
// carry the header even if a valid query token is present, and an empty
// configured token turns the guard into a pass-through.
func TestAuthGuard(t *testing.T) {
	const token = "secret-test-token"
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	call := func(guard http.Handler, method, target string, headers map[string]string) int {
		req := httptest.NewRequest(method, target, nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		guard.ServeHTTP(rec, req)
		return rec.Code
	}

	guard := AuthGuard(token, ok)

	t.Run("bearer header passes GET", func(t *testing.T) {
		code := call(guard, http.MethodGet, "/api/_test/db-dump",
			map[string]string{"Authorization": "Bearer " + token})
		require.Equal(t, http.StatusOK, code)
	})

	t.Run("bearer header passes POST", func(t *testing.T) {
		code := call(guard, http.MethodPost, "/api/_test/seed",
			map[string]string{"Authorization": "Bearer " + token})
		require.Equal(t, http.StatusOK, code)
	})

	t.Run("query token passes GET", func(t *testing.T) {
		code := call(guard, http.MethodGet, "/api/_test/db-dump?token="+token, nil)
		require.Equal(t, http.StatusOK, code)
	})

	t.Run("query token passes HEAD", func(t *testing.T) {
		code := call(guard, http.MethodHead, "/api/_test/db-dump?token="+token, nil)
		require.Equal(t, http.StatusOK, code)
	})

	t.Run("query token rejected on POST", func(t *testing.T) {
		code := call(guard, http.MethodPost, "/api/_test/seed?token="+token, nil)
		require.Equal(t, http.StatusUnauthorized, code)
	})

	t.Run("wrong bearer token rejected", func(t *testing.T) {
		code := call(guard, http.MethodGet, "/api/_test/db-dump",
			map[string]string{"Authorization": "Bearer wrong-token"})
		require.Equal(t, http.StatusUnauthorized, code)
	})

	t.Run("missing credentials rejected", func(t *testing.T) {
		code := call(guard, http.MethodPost, "/api/_test/seed", nil)
		require.Equal(t, http.StatusUnauthorized, code)
	})

	t.Run("empty configured token is pass-through", func(t *testing.T) {
		passThrough := AuthGuard("", ok)
		code := call(passThrough, http.MethodPost, "/api/_test/seed", nil)
		require.Equal(t, http.StatusOK, code)
	})
}

func TestIsLoopbackHost(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1:5180":          true,
		"127.0.0.1":               true,
		"localhost:5180":          true,
		"LocalHost:5180":          true,
		"[::1]:5180":              true,
		"[::1]":                   true,
		"evil.example:5180":       false,
		"localhost.evil.com:5180": false,
		"127.0.0.1.evil:5180":     false,
		"":                        false,
	} {
		require.Equalf(t, want, isLoopbackHost(host), "host=%q", host)
	}
}

func TestLoopbackHostGuard(t *testing.T) {
	h := LoopbackHostGuard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	for host, want := range map[string]int{"127.0.0.1:1": http.StatusOK, "evil.example:1": http.StatusForbidden} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		require.Equal(t, want, rec.Code, host)
	}
}
