// Package agentapi serves agent access over a unix socket (P14,
// docs/agent-api.md): local agents (Claude,
// Codex) learn what the user granted them and, within it, read targets'
// objects, logs and metrics, and prepare and run actions and edits. The
// rights are checked here, on every call, before the service is asked;
// agents never see the UI's ids (views, plans' expectations, signed
// edits): their plans are kept here behind a planId.
package agentapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/store"
)

type Options struct {
	Service *api.Service
	Store   *store.Store
	// Socket and Lock are the paths of the socket and its lock file.
	Socket, Lock string
	Version      string
	// Home shortens the socket's path in the instruction line ("~").
	Home string
	// Downloads resolves the application's local export destination.
	Downloads func() (string, error)
}

// Server is the agent socket and what the UI manages of it
// (api.AgentControl).
type Server struct {
	o       Options
	mux     *http.ServeMux
	methods []*method
	now     func() time.Time

	mu     sync.Mutex
	sock   *socket
	srv    *http.Server
	state  string
	errMsg string

	plans   *registry[*plan]
	sources *registry[*editSource]
	pend    *pendingSet

	// runCtx outlives the agents' requests: writes after the user's
	// confirmation (and every write, so an agent hanging up mid-write
	// does not cut it) run on it; Close cancels it.
	runCtx    context.Context
	runCancel context.CancelFunc
	runs      sync.WaitGroup
	// reqCtx is the agents' requests' base: Close cancels it first, so a
	// waiting GetRun or a read does not hold the shutdown.
	reqCtx    context.Context
	reqCancel context.CancelFunc
	// closing: Close waits for runs; no more are counted (track).
	runMu    sync.Mutex
	closing  bool
	logMu    sync.Mutex
	logReads map[*activeLogRead]struct{}
}

var _ api.AgentControl = (*Server)(nil)

// New builds the server (Start serves it).
func New(o Options) *Server {
	s := &Server{o: o, mux: http.NewServeMux(), now: time.Now, state: api.AgentFailed, errMsg: "not started", logReads: map[*activeLogRead]struct{}{}}
	s.runCtx, s.runCancel = context.WithCancel(context.Background())
	s.reqCtx, s.reqCancel = context.WithCancel(context.Background())
	s.plans = newRegistry[*plan](planTTL, maxPlans)
	s.sources = newRegistry[*editSource](planTTL, maxPlans)
	s.pend = newPendingSet(s)
	s.routes()
	return s
}

// Start serves the socket. Another instance owning it is not an error: the
// UI says so (AgentAccessStatus).
func (s *Server) Start() error {
	sock, err := listen(s.o.Socket, s.o.Lock)
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case errors.Is(err, ErrOtherInstance):
		s.state, s.errMsg = api.AgentOtherInstance, ""
		slog.Info("agent access is served by another instance", "socket", s.o.Socket)
		return nil
	case err != nil:
		s.state, s.errMsg = api.AgentFailed, err.Error()
		return err
	}
	srv := &http.Server{
		Handler:           s.mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return s.reqCtx },
	}
	s.sock, s.srv = sock, srv
	s.state, s.errMsg = api.AgentServing, ""
	// The goroutine keeps its own srv: Close clears the fields.
	go func() {
		if err := srv.Serve(sock.ln); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			slog.Warn("agent socket stopped", "err", err)
		}
	}()
	slog.Info("agent access", "socket", s.o.Socket)
	return nil
}

// Close stops serving, ends the writes in progress (bounded) and
// releases the socket.
func (s *Server) Close() {
	s.mu.Lock()
	srv, sock := s.srv, s.sock
	s.srv, s.sock = nil, nil
	s.mu.Unlock()
	s.pend.close() // the undecided are gone: their waiters are answered
	s.reqCancel()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
	}
	s.runCancel()
	s.runMu.Lock()
	s.closing = true
	s.runMu.Unlock()
	done := make(chan struct{})
	go func() { s.runs.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
	if sock != nil {
		sock.close()
	}
}

// track counts a write or a journal entry in flight for Close to wait
// for; false once Close waits (nothing more starts).
func (s *Server) track() bool {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if s.closing {
		return false
	}
	s.runs.Add(1)
	return true
}

// Handler serves the methods (tests: without a socket).
func (s *Server) Handler() http.Handler { return s.mux }

// instruction is the line for an agent's instructions.
func (s *Server) instruction() string {
	p := s.o.Socket
	if s.o.Home != "" && strings.HasPrefix(p, s.o.Home+string(filepath.Separator)) {
		p = "~" + strings.TrimPrefix(p, s.o.Home)
	}
	return fmt.Sprintf("SPK Ocular (Kubernetes/Docker): `curl -s --unix-socket %s http://ocular/v1` — how to use it and its methods; the user grants access to namespaces in Ocular.", p)
}

func (s *Server) Status(context.Context) (api.AgentAccessStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return api.AgentAccessStatus{State: s.state, Socket: s.o.Socket, Instruction: s.instruction(), Error: s.errMsg, Pending: s.pend.awaiting()}, nil
}

// agentHeader carries the agent's name (not verified).
const agentHeader = "X-Agent-Name"

// agentName is the name the agent gave, printable and bounded.
func agentName(r *http.Request) string {
	n := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(r.Header.Get(agentHeader)))
	if n == "" {
		return "unknown"
	}
	if rs := []rune(n); len(rs) > 64 {
		n = string(rs[:64])
	}
	return n
}

// caller is one request's agent.
type caller struct {
	agent string
}

// maxBody bounds a request (an edit's text is at most 3 MiB).
const maxBody = 4 << 20

// errorStatus: the HTTP status of an error code.
func errorStatus(code string) int {
	switch code {
	case api.CodeBadRequest:
		return http.StatusBadRequest
	case "forbidden":
		return http.StatusForbidden
	case "unauthorized":
		return http.StatusUnauthorized
	case api.CodeNotFound:
		return http.StatusNotFound
	case api.CodeGone, api.CodeRemoved:
		return http.StatusGone
	case api.CodeConflict:
		return http.StatusConflict
	case api.CodeLimit:
		return http.StatusTooManyRequests
	case api.CodeUnsupported:
		return http.StatusNotImplemented
	case api.CodeInternal:
		return http.StatusInternalServerError
	}
	return http.StatusBadGateway // the target's own failure (unavailable, ...)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	var ce *api.CodedError
	if !errors.As(err, &ce) {
		ce = &api.CodedError{Code: api.CodeInternal, Detail: err.Error()}
	}
	writeJSON(w, errorStatus(ce.Code), ce)
}

// forbidden is a refusal of the grants (English, for the agent).
func forbidden(format string, a ...any) *api.CodedError {
	return &api.CodedError{Code: "forbidden", Detail: fmt.Sprintf(format, a...)}
}

func badRequest(format string, a ...any) *api.CodedError {
	return &api.CodedError{Code: api.CodeBadRequest, Detail: fmt.Sprintf(format, a...)}
}
