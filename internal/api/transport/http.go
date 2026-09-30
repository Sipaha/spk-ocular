// Package transport exposes api.API to the UI: HTTP+SSE for browser mode
// (this file) and Wails bindings for desktop (wails.go).
package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/events"
)

const (
	ssePing         = 25 * time.Second
	sseWriteTimeout = 10 * time.Second
)

type HTTP struct {
	api       api.API
	events    *events.Emitter
	mux       *http.ServeMux
	authToken string
}

func NewHTTP(a api.API, em *events.Emitter) *HTTP {
	h := &HTTP{api: a, events: em, mux: http.NewServeMux(), authToken: newAuthToken()}
	h.routes()
	return h
}

func (h *HTTP) AuthToken() string { return h.authToken }

func (h *HTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") && !h.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	OriginGuard(h.mux).ServeHTTP(w, r)
}

func (h *HTTP) authorized(r *http.Request) bool {
	if bearerOK(r, h.authToken) {
		return true
	}
	// EventSource cannot set headers; the query token is accepted on SSE only.
	return r.URL.Path == "/api/events" && tokenEq(r.URL.Query().Get("token"), h.authToken)
}

func handle[Req any](fn func(ctx context.Context, req *Req) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req Req
		if r.ContentLength != 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeErr(w, &api.CodedError{Code: api.CodeBadRequest, Detail: "bad request body: " + err.Error()})
				return
			}
		}
		out, err := fn(r.Context(), &req)
		if err != nil {
			writeErr(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if out == nil {
			out = struct{}{}
		}
		_ = json.NewEncoder(w).Encode(out)
	}
}

func writeErr(w http.ResponseWriter, err error) {
	var ce *api.CodedError
	if !errors.As(err, &ce) {
		ce = &api.CodedError{Code: api.CodeInternal, Detail: err.Error()}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": ce.Code, "detail": ce.Detail})
}

func (h *HTTP) routes() {
	h.mux.HandleFunc("POST /api/AppInfo", handle(func(ctx context.Context, _ *struct{}) (any, error) {
		return h.api.AppInfo(ctx)
	}))
	h.mux.HandleFunc("POST /api/ListTargets", handle(func(ctx context.Context, _ *struct{}) (any, error) {
		return h.api.ListTargets(ctx)
	}))
	h.mux.HandleFunc("POST /api/SelectTarget", handle(func(ctx context.Context, r *api.TargetRef) (any, error) {
		return nil, h.api.SelectTarget(ctx, r.Provider, r.ID)
	}))
	type targetReq struct {
		Provider string `json:"provider"`
		Target   string `json:"target"`
	}
	h.mux.HandleFunc("POST /api/ListKinds", handle(func(ctx context.Context, r *targetReq) (any, error) {
		return h.api.ListKinds(ctx, r.Provider, r.Target)
	}))
	h.mux.HandleFunc("POST /api/ListScopes", handle(func(ctx context.Context, r *targetReq) (any, error) {
		return h.api.ListScopes(ctx, r.Provider, r.Target)
	}))
	h.mux.HandleFunc("POST /api/OpenView", handle(func(ctx context.Context, r *api.OpenViewRequest) (any, error) {
		return h.api.OpenView(ctx, *r)
	}))
	h.mux.HandleFunc("POST /api/GetRows", handle(func(ctx context.Context, r *struct {
		ViewID string `json:"viewId"`
		Since  uint64 `json:"since"`
	}) (any, error) {
		return h.api.GetRows(ctx, r.ViewID, r.Since)
	}))
	h.mux.HandleFunc("POST /api/CloseView", handle(func(ctx context.Context, r *struct {
		ViewID string `json:"viewId"`
	}) (any, error) {
		return nil, h.api.CloseView(ctx, r.ViewID)
	}))
	h.mux.HandleFunc("POST /api/ResyncView", handle(func(ctx context.Context, r *struct {
		ViewID string `json:"viewId"`
	}) (any, error) {
		return nil, h.api.ResyncView(ctx, r.ViewID)
	}))
	h.mux.HandleFunc("POST /api/TouchViews", handle(func(ctx context.Context, r *struct {
		ViewIDs []string `json:"viewIds"`
	}) (any, error) {
		return h.api.TouchViews(ctx, r.ViewIDs)
	}))
	h.mux.HandleFunc("POST /api/GetTargetState", handle(func(ctx context.Context, r *targetReq) (any, error) {
		return h.api.GetTargetState(ctx, r.Provider, r.Target)
	}))
	h.mux.HandleFunc("POST /api/SetTargetState", handle(func(ctx context.Context, r *struct {
		Provider string `json:"provider"`
		Target   string `json:"target"`
		Key      string `json:"key"`
		Value    string `json:"value"`
	}) (any, error) {
		return nil, h.api.SetTargetState(ctx, r.Provider, r.Target, r.Key, r.Value)
	}))
	h.mux.HandleFunc("POST /api/RecentObjects", handle(func(ctx context.Context, r *targetReq) (any, error) {
		return h.api.RecentObjects(ctx, r.Provider, r.Target)
	}))
	h.mux.HandleFunc("POST /api/TouchRecent", handle(func(ctx context.Context, r *api.TouchRecentRequest) (any, error) {
		return nil, h.api.TouchRecent(ctx, *r)
	}))
	h.mux.HandleFunc("POST /api/GetResource", handle(func(ctx context.Context, r *core.Ref) (any, error) {
		return h.api.GetResource(ctx, *r)
	}))
	h.mux.HandleFunc("POST /api/GetMetrics", handle(func(ctx context.Context, r *struct {
		ViewID string `json:"viewId"`
	}) (any, error) {
		return h.api.GetMetrics(ctx, r.ViewID)
	}))
	h.mux.HandleFunc("POST /api/LogInfo", handle(func(ctx context.Context, r *core.Ref) (any, error) {
		return h.api.LogInfo(ctx, *r)
	}))
	h.mux.HandleFunc("POST /api/OpenLogStream", handle(func(ctx context.Context, r *api.LogStreamRequest) (any, error) {
		return h.api.OpenLogStream(ctx, *r)
	}))
	h.mux.HandleFunc("POST /api/ExecInfo", handle(func(ctx context.Context, r *core.Ref) (any, error) {
		return h.api.ExecInfo(ctx, *r)
	}))
	h.mux.HandleFunc("POST /api/OpenTerminal", handle(func(ctx context.Context, r *api.TerminalRequest) (any, error) {
		return h.api.OpenTerminal(ctx, *r)
	}))
	h.mux.HandleFunc("POST /api/ReopenTerminal", handle(func(ctx context.Context, r *api.ReopenTerminalRequest) (any, error) {
		return h.api.ReopenTerminal(ctx, *r)
	}))
	h.mux.HandleFunc("POST /api/ForgetTerminal", handle(func(ctx context.Context, r *struct {
		TerminalID string `json:"terminalId"`
	}) (any, error) {
		return nil, h.api.ForgetTerminal(ctx, r.TerminalID)
	}))
	h.mux.HandleFunc("POST /api/PrepareAction", handle(func(ctx context.Context, r *api.ActionRequest) (any, error) {
		return h.api.PrepareAction(ctx, *r)
	}))
	h.mux.HandleFunc("POST /api/RunAction", handle(func(ctx context.Context, r *api.ActionRunRequest) (any, error) {
		return h.api.RunAction(ctx, *r)
	}))
	h.mux.HandleFunc("POST /api/ForwardInfo", handle(func(ctx context.Context, r *core.Ref) (any, error) {
		return h.api.ForwardInfo(ctx, *r)
	}))
	h.mux.HandleFunc("POST /api/StartForward", handle(func(ctx context.Context, r *api.StartForwardRequest) (any, error) {
		return h.api.StartForward(ctx, *r)
	}))
	h.mux.HandleFunc("POST /api/StopForward", handle(func(ctx context.Context, r *struct {
		ID string `json:"id"`
	}) (any, error) {
		return nil, h.api.StopForward(ctx, r.ID)
	}))
	h.mux.HandleFunc("POST /api/ListForwards", handle(func(ctx context.Context, _ *struct{}) (any, error) {
		return h.api.ListForwards(ctx)
	}))
	h.mux.HandleFunc("POST /api/StreamBase", handle(func(ctx context.Context, _ *struct{}) (any, error) {
		return h.api.StreamBase(ctx)
	}))
	h.mux.HandleFunc("GET /api/events", h.serveEvents)
}

func (h *HTTP) serveEvents(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	sub, unsub := h.events.Subscribe()
	defer unsub()
	write := func(s string) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(sseWriteTimeout))
		if _, err := fmt.Fprint(w, s); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if !write(": ok\n\n") {
		return
	}
	ping := time.NewTicker(ssePing)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			if !write(": ping\n\n") {
				return
			}
		case <-sub.Wake():
			for _, ev := range sub.Drain() {
				b, _ := json.Marshal(ev)
				if !write("data: " + string(b) + "\n\n") {
					return
				}
			}
		}
	}
}
