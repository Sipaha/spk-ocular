// Package agentapi serves agent access through an in-process loopback MCP server.
// See docs/agent-api.md: local agents (Claude,
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
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/store"
)

type Options struct {
	Service *api.Service
	Store   *store.Store
	// Profile owns the stable endpoint, credential and exclusive instance lock.
	Profile string
	Version string
	// Downloads resolves the application's local export destination.
	Downloads func() (string, error)
}

// Server is the in-process agent MCP endpoint and what the UI manages of it
// (api.AgentControl).
type Server struct {
	o            Options
	mcp          *mcp.Server
	handler      http.Handler
	initErr      error
	token        string
	endpoint     string
	lock         *flock.Flock
	requests     chan struct{}
	bodies       chan struct{}
	controls     chan struct{}
	listens      chan struct{}
	initMu       sync.Mutex
	httpMu       sync.Mutex
	httpContexts map[string]context.Context
	methods      []*method
	now          func() time.Time

	mu       sync.Mutex
	listener net.Listener
	srv      *http.Server
	state    string
	errMsg   string

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
	execMu   sync.Mutex
	execs    map[*activeExec]struct{}
}

var _ api.AgentControl = (*Server)(nil)

// New builds the server (Start serves it).
func New(o Options) *Server {
	s := &Server{o: o, token: newToken(), requests: make(chan struct{}, 16), bodies: make(chan struct{}, 24), controls: make(chan struct{}, 8), listens: make(chan struct{}, 32), httpContexts: map[string]context.Context{}, now: time.Now, state: api.AgentFailed, errMsg: "not started", logReads: map[*activeLogRead]struct{}{}, execs: map[*activeExec]struct{}{}}
	s.runCtx, s.runCancel = context.WithCancel(context.Background())
	s.reqCtx, s.reqCancel = context.WithCancel(context.Background())
	s.plans = newRegistry[*plan](planTTL, maxPlans)
	s.sources = newRegistry[*editSource](planTTL, maxPlans)
	s.pend = newPendingSet(s)
	s.registerMethods()
	s.initErr = s.setupMCP()
	return s
}

// Start acquires the profile lock and serves only the profile's loopback endpoint.
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initErr != nil {
		s.errMsg = s.initErr.Error()
		return s.initErr
	}
	if s.reqCtx.Err() != nil {
		return errors.New("MCP server is closed")
	}
	if s.srv != nil {
		return errors.New("MCP server already started")
	}
	ln, lock, config, err := openEndpoint(s.o.Profile)
	if errors.Is(err, ErrOtherInstance) {
		s.state, s.errMsg = api.AgentOtherInstance, ""
		return nil
	}
	if err != nil {
		s.state, s.errMsg = api.AgentFailed, err.Error()
		return err
	}
	s.token, s.endpoint = config.Token, config.URL
	srv := &http.Server{Handler: s.handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 32 << 10, BaseContext: func(net.Listener) context.Context { return s.reqCtx }}
	s.listener, s.lock, s.srv = ln, lock, srv
	s.state, s.errMsg = api.AgentServing, ""
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			slog.Warn("agent MCP server stopped")
		}
	}()
	slog.Info("agent MCP access", "endpoint", s.endpoint)
	return nil
}

// Close stops serving, ends the writes in progress (bounded) and
// releases the port and profile lock.
func (s *Server) Close() {
	s.mu.Lock()
	srv, ln, lock := s.srv, s.listener, s.lock
	s.srv, s.listener, s.lock = nil, nil, nil
	s.mu.Unlock()
	s.pend.close() // the undecided are gone: their waiters are answered
	s.reqCancel()
	s.runCancel()
	s.runMu.Lock()
	s.closing = true
	s.runMu.Unlock()
	s.initMu.Lock()
	sessions := []*mcp.ServerSession{}
	if s.mcp != nil {
		for session := range s.mcp.Sessions() {
			sessions = append(sessions, session)
		}
	}
	s.initMu.Unlock()
	done := make(chan struct{})
	go func() {
		var joined sync.WaitGroup
		for _, session := range sessions {
			joined.Add(1)
			go func() { defer joined.Done(); _ = session.Close() }()
		}
		joined.Wait()
		s.runs.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}

	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if err := srv.Shutdown(ctx); err != nil {
			_ = srv.Close()
		}
		cancel()
	}
	if ln != nil {
		_ = ln.Close()
	}
	if lock != nil {
		_ = lock.Unlock()
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

// Handler serves MCP only; no proprietary routes are retained.
func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) Status(context.Context) (api.AgentAccessStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	codex, claude := "", ""
	if s.state == api.AgentServing {
		// The endpoint and token contain only validated ASCII URL/hex characters,
		// so Go's quoted strings are also TOML basic strings.
		codex = fmt.Sprintf("[mcp_servers.ocular]\nurl = %q\nhttp_headers = { Authorization = %q }\n", s.endpoint, "Bearer "+s.token)
		data, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{"ocular": map[string]any{"type": "http", "url": s.endpoint, "headers": map[string]string{"Authorization": "Bearer " + s.token}}}}, "", "  ")
		claude = string(data)
	}
	return api.AgentAccessStatus{State: s.state, Endpoint: s.endpoint, CodexConfig: codex, ClaudeConfig: claude, Instruction: "Connect to SPK Ocular using MCP Streamable HTTP. Copy the matching Codex or Claude Code configuration from Ocular; access remains limited by its grants and confirmations.", Error: s.errMsg, Pending: s.pend.awaiting()}, nil
}

// Agent names are reported client metadata, not authenticated identities.
func boundedAgentName(name string) string {
	n := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(name))
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

// forbidden is a refusal of the grants (English, for the agent).
func forbidden(format string, a ...any) *api.CodedError {
	return &api.CodedError{Code: "forbidden", Detail: fmt.Sprintf(format, a...)}
}

func badRequest(format string, a ...any) *api.CodedError {
	return &api.CodedError{Code: api.CodeBadRequest, Detail: fmt.Sprintf(format, a...)}
}
