package agentapi

// These adapters exercise operation budgets and cancellation independently of
// their wire framing. Production exposes only MCP. MCP integration tests use an
// actual SDK client and the production authenticated HTTP handler.
import (
	"context"
	"encoding/json"
	"errors"
	"github.com/spk/spk-ocular/internal/api"
	"io"
	"net/http"
)

func (s *Server) invokeForTest(ctx context.Context, name string, body []byte) (any, error) {
	for _, m := range s.methods {
		if m.Name == name {
			return m.handle(ctx, caller{agent: "claude"}, body)
		}
	}
	return nil, notFound("operation", name)
}
func testOperationHandler(s *Server, name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			writeError(w, badRequest("request too large"))
			return
		}
		out, err := s.invokeForTest(r.Context(), name, data)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
}

type testLogOutput struct {
	http.ResponseWriter
	*http.ResponseController
}

func (o testLogOutput) Start() {
	o.Header().Set("Content-Type", "application/x-ndjson")
	o.WriteHeader(http.StatusOK)
}
func testStreamHandler(s *Server) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input StreamLogsRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeError(w, badRequest("invalid arguments"))
			return
		}
		if err := s.streamLogs(r.Context(), caller{agent: "claude"}, &input, testLogOutput{w, http.NewResponseController(w)}); err != nil {
			writeError(w, err)
		}
	})
}

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
