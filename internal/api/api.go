// Package api is the transport-independent surface the UI talks to. The
// desktop build exposes it as Wails bindings, browser mode as POST
// /api/<Method> + SSE (internal/api/transport).
package api

import (
	"context"
	"errors"
	"time"

	"github.com/spk/spk-ocular/internal/agentgrant"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/spk/spk-ocular/internal/forwards"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/store"
	"github.com/spk/spk-ocular/internal/views"
)

type API interface {
	AppInfo(ctx context.Context) (AppInfo, error)
	// ListTargets re-reads local configuration (kubeconfig, ...): no network.
	ListTargets(ctx context.Context) (TargetsView, error)
	// SelectTarget remembers the user's choice across restarts; the two
	// targets left last stay open, other idle sessions close (P18).
	SelectTarget(ctx context.Context, provider, id string) error
	// ConnectTarget starts an explicit, cancellable connection attempt.
	ConnectTarget(ctx context.Context, provider, id string) (core.ConnectionStatus, error)
	CancelConnectTarget(ctx context.Context, provider, id string, attempt uint64) error
	// CloseTarget closes a target's connection (not the selected one's):
	// its caches, watches and log streams (P18).
	CloseTarget(ctx context.Context, provider, id string) error

	// ListKinds: what the target's session can show now, with the
	// catalog's revision (EventKindsChanged tells of a new one).
	ListKinds(ctx context.Context, provider, target string) (KindsView, error)
	// RefreshKinds reads the target's kinds again in the background (F5 in
	// the navigation); a change comes as EventKindsChanged.
	RefreshKinds(ctx context.Context, provider, target string) error
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
	// ResyncView asks the view's own session (not the current target's) to
	// read its sources again (ViewInfo.Resync); a closed view or one of a
	// retired session is CodeGone.
	ResyncView(ctx context.Context, viewID string) error
	// TouchViews renews the leases of the UI's open views and returns the
	// ids that are gone.
	TouchViews(ctx context.Context, viewIDs []string) ([]string, error)
	// GetResource: the details of one object (YAML, facts, relations).
	GetResource(ctx context.Context, ref core.Ref) (*core.Resource, error)
	// GetTargetState / SetTargetState keep small per-target UI state (last
	// kind and scope) as opaque JSON strings by key.
	GetTargetState(ctx context.Context, provider, target string) (map[string]string, error)
	SetTargetState(ctx context.Context, provider, target, key, value string) error
	// Favorite kinds are global UI preferences, independent of any target.
	GetFavoriteKinds(ctx context.Context) ([]FavoriteKind, error)
	SetKindFavorite(ctx context.Context, req KindFavoriteRequest) error
	MoveFavoriteKind(ctx context.Context, req MoveFavoriteKindRequest) error
	// Navigation section expansion is global, independent of any target.
	GetNavSections(ctx context.Context) (map[string]bool, error)
	SetNavSection(ctx context.Context, req NavSectionRequest) error
	// RecentObjects: the target's objects whose details were opened, newest
	// first (at most 50 per target).
	RecentObjects(ctx context.Context, provider, target string) ([]RecentObject, error)
	// TouchRecent records a successful open of an object's details.
	TouchRecent(ctx context.Context, req TouchRecentRequest) error
	// GetMetrics: usage for req.RowIDs — the rows of an open view the page
	// shows (ids not of the view are ignored; at most MaxMetricRows, more
	// are cut and said). Status is "ok" or an error class (unsupported = no
	// metrics API) — never an empty success. One request per view runs: it
	// ends with ctx, after 10 s, when the view closes, when a request with
	// a higher Seq starts or CancelMetrics covers its Seq; one whose Seq was
	// already passed (cancelled or overtaken before it started) is refused
	// without asking the provider.
	GetMetrics(ctx context.Context, req MetricsRequest) (MetricsView, error)
	// CancelMetrics: the page gave up the view's requests up to seq — also
	// one not started yet (a transport's own cancel can overtake the call).
	CancelMetrics(ctx context.Context, viewID string, seq uint64) error

	// LogInfo: what logs an object has (channels = containers).
	LogInfo(ctx context.Context, ref core.Ref) (core.LogInfo, error)
	// OpenLogStream registers a log stream bound to the target's session;
	// the page reads it from StreamBase (NDJSON, web/src/logs/ndjson.ts).
	OpenLogStream(ctx context.Context, req LogStreamRequest) (LogStreamInfo, error)
	// ExecInfo: where a command can run for an object (instances = pods,
	// channels = containers).
	ExecInfo(ctx context.Context, ref core.Ref) (core.ExecInfo, error)
	// OpenTerminal prepares a command and registers its terminal (owned by
	// the app, not the session); the page opens a WebSocket to
	// <StreamBase>/term/<id>.
	OpenTerminal(ctx context.Context, req TerminalRequest) (TerminalInfo, error)
	// ReopenTerminal runs a terminal's command again with the connection
	// and pod it was opened with (a new stream id, the same terminal id).
	ReopenTerminal(ctx context.Context, req ReopenTerminalRequest) (TerminalInfo, error)
	// ForgetTerminal: the terminal's tab closed.
	ForgetTerminal(ctx context.Context, terminalID string) error
	// PrepareAction reads what an action would do (nothing changes): the
	// confirmation shows the plan; RunAction carries its Expect and the
	// target's revision back.
	PrepareAction(ctx context.Context, req ActionRequest) (core.ActionPlan, error)
	// RunAction performs a confirmed plan on the confirmed object (its UID)
	// in the session the target's revision was checked against.
	RunAction(ctx context.Context, req ActionRunRequest) (core.ActionResult, error)
	// GetEditSource reads an object's text for the editor with a signed
	// base; PrepareEdit reviews an edit (nothing changes) and signs a plan
	// that can be written; RunEdit writes that plan once.
	GetEditSource(ctx context.Context, ref core.Ref) (core.EditDoc, error)
	PrepareEdit(ctx context.Context, req EditPrepareRequest) (core.EditPlan, error)
	RunEdit(ctx context.Context, req EditRunRequest) (core.EditResult, error)
	// GetValues lists an object's protected values by key (sizes, no
	// values) with a signed base; RevealValue reads one key's value (the
	// only answer a value leaves in); PrepareValueEdit reviews a key's
	// change (nothing changes) and signs a plan that can be written;
	// RunValueEdit writes that plan once.
	GetValues(ctx context.Context, ref core.Ref) (core.ValueList, error)
	RevealValue(ctx context.Context, req ValueRevealRequest) (core.Value, error)
	PrepareValueEdit(ctx context.Context, req ValueEditRequest) (core.ValuePlan, error)
	RunValueEdit(ctx context.Context, req ValueRunRequest) (core.ValueResult, error)
	// ForwardInfo: the ports of an object that can be forwarded.
	ForwardInfo(ctx context.Context, ref core.Ref) (core.ForwardInfo, error)
	// StartForward starts a tunnel (owned by the app, not the session):
	// listens on loopback and connects once; a failed first connect is the
	// call's error.
	StartForward(ctx context.Context, req StartForwardRequest) (forwards.Info, error)
	StopForward(ctx context.Context, id string) error
	// ListForwards: the tunnels (EventForwardsChanged says when to reload).
	ListForwards(ctx context.Context) ([]forwards.Info, error)
	// StreamBase: where streams are served (desktop: a loopback URL with a
	// token; browser: a path on this server).
	StreamBase(ctx context.Context) (string, error)

	// Agent access (P14, agentaccess.go): the socket's state and the line
	// for an agent's instructions; the grants per target (saved whole;
	// Reconfirm grants them for the identity the user was shown, if the
	// target still has it — else conflict); the
	// agents' destructive plans waiting for the user and the decision; the
	// journal, newest first. EventAgent*Changed say when to reload.
	AgentAccessStatus(ctx context.Context) (AgentAccessStatus, error)
	ListAgentGrants(ctx context.Context) ([]agentgrant.Target, error)
	SaveAgentGrants(ctx context.Context, req SaveAgentGrantsRequest) error
	RevokeAllAgentGrants(ctx context.Context) error
	ReconfirmAgentTarget(ctx context.Context, req ReconfirmAgentTargetRequest) error
	ListAgentPending(ctx context.Context) ([]AgentPending, error)
	DecideAgentPending(ctx context.Context, req DecideAgentPendingRequest) error
	ListAgentAudit(ctx context.Context, f store.AuditFilter) ([]store.AuditEntry, error)
}

type NavSectionRequest struct {
	Key  string `json:"key"`
	Open bool   `json:"open"`
}

type FavoriteKind struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
}

type KindFavoriteRequest struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	Favorite bool   `json:"favorite"`
}

type MoveFavoriteKindRequest struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	// Before is a kind of the same provider; empty moves to the end.
	Before string `json:"before"`
}

type MetricsView struct {
	Status   string                    `json:"status"`
	Coverage []provider.SourceCoverage `json:"coverage,omitempty"`
	// Message: the error's detail (Status is not "ok").
	Message string `json:"message,omitempty"`
	// Limit: with "ok", only the first Limit of the asked rows were (more
	// than MaxMetricRows asked).
	Limit     int       `json:"limit,omitempty"`
	Timestamp time.Time `json:"timestamp,omitzero"`
	Window    string    `json:"window,omitempty"`
	// Values by row id; rows without a sample are absent (unknown).
	Values map[string]provider.Usage `json:"values"`
}

type MetricsRequest struct {
	ViewID string   `json:"viewId"`
	RowIDs []string `json:"rowIds"`
	// Seq orders the page's requests for the view (higher = newer); 0 —
	// unordered (still one at a time).
	Seq uint64 `json:"seq,omitempty"`
}

type OpenViewRequest struct {
	Provider string         `json:"provider"`
	Target   string         `json:"target"`
	Query    provider.Query `json:"query"`
}

type ViewInfo struct {
	ViewID string              `json:"viewId"`
	Kind   core.KindDescriptor `json:"kind"`
	// Resync: the session can read the view's sources again on request
	// (ResyncView, F5).
	Resync bool `json:"resync,omitempty"`
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
	// Aliases name the provider's scopes and targets in palette commands.
	Aliases provider.CommandAliases `json:"aliases"`
	// ScopeNames: what the provider's scopes are called; nil: the UI's
	// generic words.
	ScopeNames *core.ScopeNames `json:"scopeNames,omitempty"`
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
	// EventKindsChanged: {provider, target, session, rev} — the session's
	// catalog has a new revision; call ListKinds (a hint: the UI re-lists
	// on resync and F5 as well).
	EventKindsChanged = "kinds_changed"
	// EventViewChanged: {viewId, version} or {viewId, gone: true}.
	EventViewChanged = views.EventViewChanged
)

// Error codes (CodedError.Code) — stable, the UI switches on them.
// Provider error classes (provider.ErrorClass) travel as codes unchanged:
// forbidden, unauthorized, unavailable, gone, not_found, unsupported, ...
const (
	CodeInternal   = "internal"
	CodeBadRequest = "bad_request"
	CodeNotFound   = "not_found"
	CodeGone       = "gone"
	// CodeRemoved: the kind is no longer served; reopening cannot help.
	CodeRemoved     = "removed"
	CodeUnsupported = "unsupported"
	// CodeConflict: the object or the target's configuration changed since
	// the user saw it; look again.
	CodeConflict = "conflict"
	// CodeLimit: too many open streams or tunnels.
	CodeLimit = "limit"
)

// KindsView is a session's kind catalog. Session identifies the session
// incarnation: a new one (the target was reconfigured) starts its Rev anew.
type KindsView struct {
	core.KindCatalog
	Session uint64 `json:"session"`
}

// CodedError is what API methods return: a stable code for the UI plus a
// human-readable detail; Why, when set, is the detail as a sentence by key
// (the UI says it in its language). Over Wails it travels as the string
// "code: detail", and as this JSON in the rejected call's cause.
type CodedError struct {
	Code   string        `json:"code"`
	Detail string        `json:"detail"`
	Why    *core.Message `json:"why,omitempty"`
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
		return &CodedError{Code: string(pe.Class), Detail: pe.Message, Why: pe.Why}
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
