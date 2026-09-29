// Package api is the transport-independent surface the UI talks to. The
// desktop build exposes it as Wails bindings, browser mode as POST
// /api/<Method> + SSE (internal/api/transport).
package api

import (
	"context"
	"errors"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/views"
)

type API interface {
	AppInfo(ctx context.Context) (AppInfo, error)
	// ListTargets re-reads local configuration (kubeconfig, ...): no network.
	ListTargets(ctx context.Context) (TargetsView, error)
	// SelectTarget remembers the user's choice across restarts and closes
	// the sessions of other targets (one active context).
	SelectTarget(ctx context.Context, provider, id string) error

	// ListKinds: what the target's session can show.
	ListKinds(ctx context.Context, provider, target string) ([]core.KindDescriptor, error)
	// ListScopes lists namespaces / projects. A permission error is not a
	// failure of the call: ScopesView.Error says so and the UI lets the user
	// type a scope.
	ListScopes(ctx context.Context, provider, target string) (ScopesView, error)
	// OpenView starts a live table; rows are pulled with GetRows.
	OpenView(ctx context.Context, req OpenViewRequest) (ViewInfo, error)
	// GetRows returns changes after cursor since (0: a full snapshot). A
	// closed/unknown view is CodeGone: reopen it.
	GetRows(ctx context.Context, viewID string, since uint64) (views.Page, error)
	CloseView(ctx context.Context, viewID string) error
	// TouchViews renews the leases of the UI's open views and returns the
	// ids that are gone.
	TouchViews(ctx context.Context, viewIDs []string) ([]string, error)
	// GetResource: the details of one object (YAML, facts, relations).
	GetResource(ctx context.Context, ref core.Ref) (*core.Resource, error)
	// GetTargetState / SetTargetState keep small per-target UI state (last
	// kind and scope) as opaque JSON strings by key.
	GetTargetState(ctx context.Context, provider, target string) (map[string]string, error)
	SetTargetState(ctx context.Context, provider, target, key, value string) error
	// GetMetrics: usage for the rows of an open view. Status is "ok" or an
	// error class (unsupported = no metrics API) — never an empty success.
	GetMetrics(ctx context.Context, viewID string) (MetricsView, error)

	// LogInfo: what logs an object has (channels = containers).
	LogInfo(ctx context.Context, ref core.Ref) (core.LogInfo, error)
	// OpenLogStream registers a log stream bound to the target's session;
	// the page reads it from StreamBase (NDJSON, web/src/logs/ndjson.ts).
	OpenLogStream(ctx context.Context, req LogStreamRequest) (LogStreamInfo, error)
	// StreamBase: where streams are served (desktop: a loopback URL with a
	// token; browser: a path on this server).
	StreamBase(ctx context.Context) (string, error)
}

type MetricsView struct {
	Status    string    `json:"status"`
	Message   string    `json:"message,omitempty"`
	Timestamp time.Time `json:"timestamp,omitzero"`
	Window    string    `json:"window,omitempty"`
	// Values by row id; rows without a sample are absent (unknown).
	Values map[string]provider.Usage `json:"values"`
}

type OpenViewRequest struct {
	Provider string         `json:"provider"`
	Target   string         `json:"target"`
	Query    provider.Query `json:"query"`
}

type ViewInfo struct {
	ViewID string              `json:"viewId"`
	Kind   core.KindDescriptor `json:"kind"`
}

type ScopesView struct {
	Scopes []core.Scope `json:"scopes"`
	// Kind: open a view of this kind (scope "none") to keep the list live.
	Kind string `json:"kind,omitempty"`
	// Error is set when scopes cannot be listed (code + detail).
	Error *CodedError `json:"error,omitempty"`
}

type AppInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	// Mode is "desktop" or "browser".
	Mode string `json:"mode"`
	// Language is the UI language from the system locale: "ru" or "en".
	Language string `json:"language"`
}

type TargetGroup struct {
	Provider string         `json:"provider"`
	Title    string         `json:"title"`
	Targets  []core.Target  `json:"targets"`
	Problems []core.Problem `json:"problems"`
	// Error is set when the provider could not discover at all.
	Error string `json:"error,omitempty"`
}

type TargetRef struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
}

type TargetsView struct {
	Groups []TargetGroup `json:"groups"`
	// Selected is nil when nothing is selected or the remembered target is
	// currently absent (it comes back if the kubeconfig does).
	Selected *TargetRef `json:"selected"`
}

// Event types pushed to the UI.
const (
	// EventTargetsChanged: local configuration changed; call ListTargets.
	EventTargetsChanged = "targets_changed"
	// EventResync (events.TypeResync): the UI fell behind; reload all state.
	EventResync = events.TypeResync
	// EventViewChanged: {viewId, version} or {viewId, gone: true}.
	EventViewChanged = views.EventViewChanged
)

// Error codes (CodedError.Code) — stable, the UI switches on them.
// Provider error classes (provider.ErrorClass) travel as codes unchanged:
// forbidden, unauthorized, unavailable, gone, not_found, unsupported, ...
const (
	CodeInternal    = "internal"
	CodeBadRequest  = "bad_request"
	CodeNotFound    = "not_found"
	CodeGone        = "gone"
	CodeUnsupported = "unsupported"
	// CodeLimit: too many open streams.
	CodeLimit = "limit"
)

// CodedError is what API methods return: a stable code for the UI plus a
// human-readable detail. Over Wails it travels as the string "code: detail".
type CodedError struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func (e *CodedError) Error() string {
	if e.Detail == "" {
		return e.Code
	}
	return e.Code + ": " + e.Detail
}

// fromProvider maps a provider error to a coded one (class = code).
func fromProvider(err error) *CodedError {
	var pe *provider.Error
	if errors.As(err, &pe) {
		return &CodedError{Code: string(pe.Class), Detail: pe.Message}
	}
	var ce *CodedError
	if errors.As(err, &ce) {
		return ce
	}
	return coded(CodeInternal, err)
}

func coded(code string, err error) *CodedError {
	if err == nil {
		return &CodedError{Code: code}
	}
	return &CodedError{Code: code, Detail: err.Error()}
}
