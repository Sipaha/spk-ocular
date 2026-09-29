package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/spk/spk-ocular/internal/api/transport"
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
		// Steer the synthetic actions: rights, failures, a slow run.
		mux.HandleFunc("POST /api/_test/synthetic/controls", func(w http.ResponseWriter, r *http.Request) {
			var ctl synthetic.Controls
			if err := json.NewDecoder(r.Body).Decode(&ctl); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			c.Synthetic.SetControls(ctl)
			w.WriteHeader(http.StatusNoContent)
		})
		// Change a workload as another actor would.
		mux.HandleFunc("POST /api/_test/synthetic/mutate", func(w http.ResponseWriter, r *http.Request) {
			var m synthetic.Mutation
			if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if err := c.Synthetic.Mutate(m); err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
		// The workloads as at start, no controls.
		mux.HandleFunc("POST /api/_test/synthetic/reset", func(w http.ResponseWriter, _ *http.Request) {
			c.Synthetic.ResetActions()
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

// testAPIFile (in the data directory) tells automation where the desktop's
// test routes listen and their token.
const testAPIFile = "test-api.json"

// startTestAPI serves the test routes for the desktop app (--test-api, soak
// and desktop tests): a loopback port of their own with a token of their
// own, written to testAPIFile (owner-only). stop closes both.
func startTestAPI(c *appCore) (stop func(), err error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)
	srv := &http.Server{
		Handler:           transport.LoopbackHostGuard(transport.AuthGuard(token, testRoutes(c))),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
	file := filepath.Join(c.Paths.DataDir, testAPIFile)
	info, _ := json.Marshal(map[string]string{"url": "http://" + ln.Addr().String(), "token": token})
	if err := os.WriteFile(file, info, 0o600); err != nil {
		_ = srv.Close()
		return nil, err
	}
	return func() {
		_ = os.Remove(file)
		_ = srv.Close()
	}, nil
}
