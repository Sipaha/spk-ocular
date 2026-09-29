// Package api is the transport-independent surface the UI talks to. The
// desktop build exposes it as Wails bindings, browser mode as POST
// /api/<Method> + SSE (internal/api/transport).
package api

import (
	"context"

	"github.com/spk/spk-ocular/internal/core"
)

type API interface {
	AppInfo(ctx context.Context) (AppInfo, error)
	// ListTargets re-reads local configuration (kubeconfig, ...): no network.
	ListTargets(ctx context.Context) (TargetsView, error)
	// SelectTarget remembers the user's choice across restarts.
	SelectTarget(ctx context.Context, provider, id string) error
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
)

// Error codes (CodedError.Code) — stable, the UI switches on them.
const (
	CodeInternal   = "internal"
	CodeBadRequest = "bad_request"
	CodeNotFound   = "not_found"
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

func coded(code string, err error) *CodedError {
	if err == nil {
		return &CodedError{Code: code}
	}
	return &CodedError{Code: code, Detail: err.Error()}
}
