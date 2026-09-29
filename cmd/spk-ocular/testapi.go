package main

import (
	"encoding/json"
	"net/http"
	"runtime"
	"runtime/debug"
)

// testRoutes are automation hooks for e2e (--test-api only). They carry the
// bearer token like the rest of /api.
func testRoutes(c *appCore) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/_test/paths", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"data_dir": c.Paths.DataDir, "db": c.Paths.DBFile})
	})
	mux.HandleFunc("GET /api/_test/stats", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(c.Service.Stats())
	})
	mux.HandleFunc("POST /api/_test/gc", func(w http.ResponseWriter, _ *http.Request) {
		runtime.GC()
		debug.FreeOSMemory()
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}
