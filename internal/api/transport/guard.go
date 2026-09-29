package transport

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func newAuthToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// LoopbackHostGuard rejects (403) every request whose Host header is not a
// loopback name (127.0.0.1, localhost, ::1; any port). It is the DNS-rebinding
// defense: a page on evil.example that re-resolves to 127.0.0.1 sends
// Host: evil.example, and OriginGuard alone would accept it because its Origin
// matches that Host — while `/` would hand it the API token.
func LoopbackHostGuard(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackHost(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func isLoopbackHost(host string) bool {
	h, _, _ := splitHostPortLoose(host)
	switch strings.ToLower(h) {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

// OriginGuard wraps h with a CSRF check: state-changing methods (POST, PUT,
// PATCH, DELETE) must carry an Origin OR Referer header that matches the
// server's Host. State-reading methods (GET, HEAD) are permitted without
// either header so curl-style probing of `/api/events` and friends keeps
// working. The pair-of-headers rule mirrors OWASP's "verify same-origin
// with standard headers" guidance — modern browsers reliably attach Origin
// (or at least Referer) to cross-site state-changing requests.
//
// Loopback aliases (localhost ↔ 127.0.0.1 ↔ ::1) are honored exactly via
// originMatchesHost; prefix-matching is intentionally not used (it was
// previously bypassed by `Origin: http://localhostevil:PORT` and re-added
// here would re-introduce that bypass).
func OriginGuard(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Origin")
		mutating := r.Method == http.MethodPost || r.Method == http.MethodPut ||
			r.Method == http.MethodPatch || r.Method == http.MethodDelete
		origin := r.Header.Get("Origin")
		referer := r.Header.Get("Referer")
		if mutating && origin == "" && referer == "" {
			http.Error(w, "missing Origin/Referer on state-changing request", http.StatusForbidden)
			return
		}
		if origin != "" && !originMatchesHost(origin, r.Host) {
			http.Error(w, "cross-origin request blocked", http.StatusForbidden)
			return
		}
		// Origin absent but Referer present (legacy / WebView edge cases) —
		// validate the Referer's origin instead. Same exact-host rule.
		if origin == "" && referer != "" && mutating {
			if !originMatchesHost(referer, r.Host) {
				http.Error(w, "cross-origin Referer blocked", http.StatusForbidden)
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

// originMatchesHost is true when the URL in the Origin header has the same
// host:port as r.Host. Comparison is on the parsed Hostname() to prevent
// prefix-match bypasses like `Origin: http://localhostevil:5174` matching a
// `Host: 127.0.0.1:5174` server.
func originMatchesHost(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Host, host) {
		return true
	}
	originHost := strings.ToLower(u.Hostname())
	originPort := u.Port()
	hostHost, hostPort, err := splitHostPortLoose(host)
	if err != nil {
		return false
	}
	hostHost = strings.ToLower(hostHost)
	if originPort != hostPort {
		return false
	}
	// Loopback aliases — exact match only, no prefix.
	loopbackAliases := map[string]map[string]bool{
		"localhost": {"127.0.0.1": true, "::1": true},
		"127.0.0.1": {"localhost": true},
		"::1":       {"localhost": true},
	}
	if aliases, ok := loopbackAliases[originHost]; ok && aliases[hostHost] {
		return true
	}
	return false
}

// splitHostPortLoose accepts both "host:port" and a bare "host" (no port).
// Returns the parsed hostname (without IPv6 brackets) and port string.
func splitHostPortLoose(s string) (string, string, error) {
	if h, p, err := splitHostPort(s); err == nil {
		return h, p, nil
	}
	// No port present — return host as-is.
	return strings.Trim(s, "[]"), "", nil
}

func splitHostPort(s string) (string, string, error) {
	// net.SplitHostPort would be ideal but it errors on bare host; we want
	// loose parsing. Find the LAST colon that isn't inside brackets.
	if strings.HasPrefix(s, "[") {
		if end := strings.Index(s, "]"); end > 0 {
			port := ""
			if len(s) > end+1 && s[end+1] == ':' {
				port = s[end+2:]
			}
			return s[1:end], port, nil
		}
	}
	if i := strings.LastIndex(s, ":"); i > 0 {
		return s[:i], s[i+1:], nil
	}
	return "", "", fmt.Errorf("no port")
}

// tokenEq compares in constant time so a wrong token cannot be recovered
// byte-by-byte from response timing.
func tokenEq(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func bearerOK(r *http.Request, token string) bool {
	return tokenEq(r.Header.Get("Authorization"), "Bearer "+token)
}

// AuthGuard wraps next with bearer-token auth (used for /api/_test/* mux).
//
// The query-token fallback is limited to GET/HEAD (the read-only db-dump / logs
// routes, which are handy to open straight in a browser). State-mutating routes
// — seed, reset, inject-message, clock — require the Authorization header, so a
// token cannot leak into access logs or browser history from a request that
// changes state.
func AuthGuard(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok := bearerOK(r, token)
		if !ok && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			ok = tokenEq(r.URL.Query().Get("token"), token)
		}
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
