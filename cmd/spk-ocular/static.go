package main

import (
	"fmt"
	"io/fs"
	"net/http"
	"strings"
)

// tokenMetaName is read by web/src/api/client.ts.
const tokenMetaName = "spk-ocular-api-token"

// frontendHandler serves the SPA; index.html gets the per-run API token.
// Extension-less paths fall back to index.html (SPA).
func frontendHandler(token string, dist fs.FS) http.Handler {
	files := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" || p == "index.html" || !strings.Contains(p, ".") {
			serveIndex(w, dist, token)
			return
		}
		files.ServeHTTP(w, r)
	})
}

func serveIndex(w http.ResponseWriter, dist fs.FS, token string) {
	data, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		http.Error(w, "UI not built: run `make build`", http.StatusInternalServerError)
		return
	}
	tag := fmt.Sprintf(`<meta name="%s" content="%s">`, tokenMetaName, token)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The token changes every run; a cached index would 401 every call.
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(strings.Replace(string(data), "</head>", tag+"</head>", 1)))
}
