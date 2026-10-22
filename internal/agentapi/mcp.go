package agentapi

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	schema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/spk/spk-ocular/internal/api"
)

const requestContextHeader = "X-Ocular-MCP-Request"
const maxMCPSessions = 32

// setupMCP binds the existing operations directly. No legacy HTTP routing or
// application-UI methods sit behind this transport.
func (s *Server) setupMCP() error {
	s.mcp = mcp.NewServer(&mcp.Implementation{Name: "SPK Ocular", Version: s.o.Version}, &mcp.ServerOptions{
		Instructions:              "SPK Ocular provides local, permission-controlled access to Kubernetes clusters and Docker/Compose environments while Ocular is running. Use these tools to inspect resources, investigate problems, read bounded logs and metrics, and prepare permitted resource changes or finite container commands. Only granted targets, namespaces/projects, kinds and operations are available. UI-only features are not exposed.\n\nUse Access first. Nothing is granted by default. Prepare changes with PrepareAction/PrepareEdit/PrepareExec, then RunAction/RunEdit/RunExec. If awaiting_confirmation, ask the person to confirm in Ocular and poll GetRun. Never retry an uncertain write or command automatically. Client names are unverified attribution, not authorization.",
		Capabilities:              &mcp.ServerCapabilities{},
		SupportedProtocolVersions: []string{"2025-11-25", "2025-06-18"},
	})
	catalogue := s.catalog()
	for i, doc := range catalogue.Methods {
		operation := s.methods[i]
		encoded, err := json.Marshal(doc.Request)
		if err != nil {
			return err
		}
		var definition any
		if err = json.Unmarshal(encoded, &definition); err != nil {
			return err
		}
		compiler := schema.NewCompiler()
		location := "urn:ocular:tool:" + doc.Name
		if err = compiler.AddResource(location, definition); err != nil {
			return err
		}
		validator, err := compiler.Compile(location)
		if err != nil {
			return fmt.Errorf("MCP schema %s: %w", doc.Name, err)
		}
		s.mcp.AddTool(&mcp.Tool{Name: doc.Name, Description: doc.Doc, InputSchema: definition, OutputSchema: doc.Response,
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: !doc.Writes && doc.Name != "ExportLogs", DestructiveHint: &doc.Writes, IdempotentHint: false},
		}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			stop := context.AfterFunc(s.reqCtx, cancel)
			defer stop()
			// Legacy MCP sessions can outlive an HTTP request. Our reads and direct
			// exec explicitly retain Ocular's disconnect cancellation contract.
			if req.Extra != nil {
				s.httpMu.Lock()
				requestCtx := s.httpContexts[req.Extra.Header.Get(requestContextHeader)]
				s.httpMu.Unlock()
				if requestCtx != nil {
					stopHTTP := context.AfterFunc(requestCtx, cancel)
					defer stopHTTP()
				}
			}
			args := req.Params.Arguments
			if len(args) == 0 {
				args = json.RawMessage(`{}`)
			}
			decoder := json.NewDecoder(bytes.NewReader(args))
			decoder.UseNumber()
			var value any
			if err := decoder.Decode(&value); err != nil {
				return toolError(badRequest("invalid tool arguments")), nil
			}
			if err := validator.Validate(value); err != nil {
				return toolError(badRequest("arguments do not match the %s input schema", operation.Name)), nil
			}
			name := "unknown"
			if info := req.ClientInfo(); info != nil {
				name = boundedAgentName(info.Name)
			}
			if operation.Streaming {
				progressToken := req.Params.GetProgressToken()
				if progressToken != nil {
					if text, ok := progressToken.(string); ok && len(text) > 128 {
						return toolError(badRequest("progress token exceeds 128 bytes")), nil
					}
					progress := 0
					ctx = context.WithValue(ctx, logProgressKey{}, func(frame LogFrame) error {
						data, err := json.Marshal(frame)
						if err != nil {
							return err
						}
						progress++
						notifyCtx, done := context.WithTimeout(ctx, 2*time.Second)
						defer done()
						return req.Session.NotifyProgress(notifyCtx, &mcp.ProgressNotificationParams{ProgressToken: progressToken, Progress: float64(progress), Message: string(data)})
					})
				}
			}
			out, err := operation.handle(ctx, caller{agent: name}, args)
			if err != nil {
				return toolError(err), nil
			}
			data, err := json.Marshal(out)
			if err != nil {
				return toolError(errors.New("could not encode tool result")), nil
			}
			if len(data) > 8<<20 {
				return toolError(&api.CodedError{Code: api.CodeLimit, Detail: "tool result exceeds the 8 MiB response limit"}), nil
			}
			result := &mcp.CallToolResult{StructuredContent: json.RawMessage(data), Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}
			wire, err := json.Marshal(result)
			if err != nil || len(wire) > 8<<20 {
				return toolError(&api.CodedError{Code: api.CodeLimit, Detail: "tool result exceeds the 8 MiB encoded response limit"}), nil
			}
			return result, nil
		})
	}
	stream := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		if s.reqCtx.Err() != nil {
			return nil
		}
		return s.mcp
	}, &mcp.StreamableHTTPOptions{SessionTimeout: 10 * time.Minute, MaxRequestBodyBytes: maxBody})
	s.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.serveMCP(w, r, stream) })
	return nil
}

func toolError(err error) *mcp.CallToolResult {
	data, _ := json.Marshal(errCoded(err))
	return &mcp.CallToolResult{IsError: true, StructuredContent: json.RawMessage(data), Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}
}

func (s *Server) serveMCP(w http.ResponseWriter, r *http.Request, next http.Handler) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if s.reqCtx.Err() != nil {
		http.Error(w, "MCP server is closing", http.StatusServiceUnavailable)
		return
	}
	if r.URL.Path != "/mcp" {
		http.NotFound(w, r)
		return
	}
	host, port, err := net.SplitHostPort(r.Host)
	s.mu.Lock()
	endpoint, token := s.endpoint, s.token
	s.mu.Unlock()
	if err != nil || host != "127.0.0.1" || port == "" {
		http.Error(w, "invalid MCP host", http.StatusForbidden)
		return
	}
	if endpoint != "" {
		u, _ := url.Parse(endpoint)
		if r.Host != u.Host {
			http.Error(w, "invalid MCP host", http.StatusForbidden)
			return
		}
	}
	origins := r.Header.Values("Origin")
	if len(origins) > 1 || len(origins) == 1 && origins[0] != "http://"+r.Host {
		http.Error(w, "invalid MCP origin", http.StatusForbidden)
		return
	}
	auth := r.Header.Values("Authorization")
	if len(auth) != 1 || subtle.ConstantTimeCompare([]byte(auth[0]), []byte("Bearer "+token)) != 1 {
		w.Header().Set("WWW-Authenticate", `Bearer realm="SPK Ocular MCP"`)
		http.Error(w, "MCP authentication required", http.StatusUnauthorized)
		return
	}
	if r.URL.RawQuery != "" {
		http.Error(w, "MCP query parameters are not accepted", http.StatusBadRequest)
		return
	}
	// Keep control traffic independent: busy tools and SSE listeners cannot
	// exhaust the slots needed to cancel them or delete a session.
	gate := s.requests
	if r.Method == http.MethodGet {
		gate = s.listens
	}
	if r.Method == http.MethodDelete {
		gate = s.controls
	}
	if r.Method == http.MethodPost {
		select {
		case s.bodies <- struct{}{}:
		default:
			http.Error(w, "too many MCP request bodies", http.StatusTooManyRequests)
			return
		}
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		<-s.bodies
		if err != nil {
			http.Error(w, "MCP request too large", http.StatusRequestEntityTooLarge)
			return
		}
		var envelope struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(data, &envelope)
		if envelope.Method == "initialize" && len(data) > 64<<10 {
			http.Error(w, "MCP initialization too large", http.StatusRequestEntityTooLarge)
			return
		}
		if envelope.Method == "notifications/cancelled" || envelope.Method == "notifications/initialized" || envelope.Method == "ping" {
			gate = s.controls
		}
		r = r.Clone(r.Context())
		r.Body = io.NopCloser(bytes.NewReader(data))
	}
	select {
	case gate <- struct{}{}:
		defer func() { <-gate }()
	default:
		http.Error(w, "too many MCP requests", http.StatusTooManyRequests)
		return
	}

	if r.Header.Get("Mcp-Session-Id") == "" {
		s.initMu.Lock()
		defer s.initMu.Unlock()
		if s.reqCtx.Err() != nil {
			http.Error(w, "MCP server is closing", http.StatusServiceUnavailable)
			return
		}
		n := 0
		for range s.mcp.Sessions() {
			n++
		}
		if n >= maxMCPSessions {
			http.Error(w, "too many MCP sessions", http.StatusTooManyRequests)
			return
		}
	}
	key := newID("http-")
	r = r.Clone(r.Context())
	r.Header.Set(requestContextHeader, key)
	// Authorization has already been checked. Do not copy credentials into SDK
	// request metadata, handlers, diagnostic errors or protocol logging.
	r.Header.Del("Authorization")
	s.httpMu.Lock()
	s.httpContexts[key] = r.Context()
	s.httpMu.Unlock()
	defer func() { s.httpMu.Lock(); delete(s.httpContexts, key); s.httpMu.Unlock() }()
	next.ServeHTTP(mcpResponseWriter{ResponseWriter: w, control: http.NewResponseController(w)}, r)
}

// Limit each physical write, rather than the duration of a legitimate finite
// tool call. A client that stops reading cannot retain an SSE writer forever.
type mcpResponseWriter struct {
	http.ResponseWriter
	control *http.ResponseController
}

func (w mcpResponseWriter) Write(data []byte) (int, error) {
	_ = w.control.SetWriteDeadline(time.Now().Add(2 * time.Second))
	return w.ResponseWriter.Write(data)
}
func (w mcpResponseWriter) FlushError() error {
	_ = w.control.SetWriteDeadline(time.Now().Add(2 * time.Second))
	return w.control.Flush()
}
func (w mcpResponseWriter) Flush()                      { _ = w.FlushError() }
func (w mcpResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
