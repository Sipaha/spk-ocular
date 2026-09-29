package main

import (
	"encoding/json"
	"net/http"
	"runtime"
	"runtime/debug"

	"github.com/spk/spk-ocular/internal/providers/synthetic"
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
		st := c.Service.Stats()
		if c.Synthetic != nil { // what its terminals and tunnels hold
			for k, v := range c.Synthetic.LiveStats() {
				st[k] = v
			}
		}
		_ = json.NewEncoder(w).Encode(st)
	})
	if c.Synthetic != nil {
		// Push lines/states into the synthetic provider's open log streams.
		mux.HandleFunc("POST /api/_test/logs/emit", func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				Object string          `json:"object"`
				Event  synthetic.Event `json:"event"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]int{"delivered": c.Synthetic.Emit(req.Object, req.Event)})
		})
	}
	if c.Synthetic != nil {
		// Change the synthetic target's configuration (targets_changed).
		mux.HandleFunc("POST /api/_test/synthetic/reconfigure", func(w http.ResponseWriter, _ *http.Request) {
			c.Synthetic.Reconfigure()
			w.WriteHeader(http.StatusNoContent)
		})
	}
	mux.HandleFunc("POST /api/_test/gc", func(w http.ResponseWriter, _ *http.Request) {
		runtime.GC()
		debug.FreeOSMemory()
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}
