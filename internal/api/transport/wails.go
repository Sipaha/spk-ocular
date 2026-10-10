//go:build wails

package transport

import (
	"context"

	"github.com/spk/spk-ocular/internal/agentgrant"
	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/forwards"
	"github.com/spk/spk-ocular/internal/providers/kubernetes"
	"github.com/spk/spk-ocular/internal/store"
	"github.com/spk/spk-ocular/internal/views"
)

// API is the Wails service. Bindings are addressed by Go FQN:
//
//	github.com/spk/spk-ocular/internal/api/transport.API.<Method>
//
// (mirrored in web/src/api/client.ts).
type API struct{ a api.API }

func NewAPI(a api.API) *API { return &API{a: a} }

func (w *API) SetLanguage(language string) error {
	return w.a.SetLanguage(api.UIContext(context.Background()), language)
}
func (w *API) AppInfo() (api.AppInfo, error) { return w.a.AppInfo(api.UIContext(context.Background())) }
func (w *API) ListTargets() (api.TargetsView, error) {
	return w.a.ListTargets(api.UIContext(context.Background()))
}
func (w *API) SelectTarget(provider, id string) error {
	return w.a.SelectTarget(api.UIContext(context.Background()), provider, id)
}

func (w *API) ConnectTarget(provider, id string) (core.ConnectionStatus, error) {
	return w.a.ConnectTarget(api.UIContext(context.Background()), provider, id)
}

func (w *API) CancelConnectTarget(provider, id string, attempt uint64) error {
	return w.a.CancelConnectTarget(api.UIContext(context.Background()), provider, id, attempt)
}

func (w *API) CloseTarget(provider, id string) error {
	return w.a.CloseTarget(api.UIContext(context.Background()), provider, id)
}

func (w *API) ListKinds(provider, target string) (api.KindsView, error) {
	return w.a.ListKinds(api.UIContext(context.Background()), provider, target)
}
func (w *API) RefreshKinds(provider, target string) error {
	return w.a.RefreshKinds(api.UIContext(context.Background()), provider, target)
}
func (w *API) ListScopes(provider, target string) (api.ScopesView, error) {
	return w.a.ListScopes(api.UIContext(context.Background()), provider, target)
}
func (w *API) OpenView(req api.OpenViewRequest) (api.ViewInfo, error) {
	return w.a.OpenView(api.UIContext(context.Background()), req)
}
func (w *API) GetRows(viewID string, since uint64) (views.Page, error) {
	return w.a.GetRows(api.UIContext(context.Background()), viewID, since)
}
func (w *API) CloseView(viewID string) error {
	return w.a.CloseView(api.UIContext(context.Background()), viewID)
}
func (w *API) ResyncView(viewID string) error {
	return w.a.ResyncView(api.UIContext(context.Background()), viewID)
}
func (w *API) TouchViews(viewIDs []string) ([]string, error) {
	return w.a.TouchViews(api.UIContext(context.Background()), viewIDs)
}
func (w *API) GetResource(ref core.Ref) (*core.Resource, error) {
	return w.a.GetResource(api.UIContext(context.Background()), ref)
}

// GetMetrics takes the call's context: the page cancels a request it no
// longer waits for (the Wails runtime's CancellablePromise.cancel). That
// cancel is lost when it overtakes the call, so the page also sends
// CancelMetrics, and each request carries its seq.
func (w *API) GetMetrics(ctx context.Context, req api.MetricsRequest) (api.MetricsView, error) {
	return w.a.GetMetrics(ctx, req)
}
func (w *API) CancelMetrics(viewID string, seq uint64) error {
	return w.a.CancelMetrics(api.UIContext(context.Background()), viewID, seq)
}
func (w *API) GetTargetState(provider, target string) (map[string]string, error) {
	return w.a.GetTargetState(api.UIContext(context.Background()), provider, target)
}

func (w *API) GetNavSections() (map[string]bool, error) {
	return w.a.GetNavSections(api.UIContext(context.Background()))
}
func (w *API) SetNavSection(req api.NavSectionRequest) error {
	return w.a.SetNavSection(api.UIContext(context.Background()), req)
}

func (w *API) GetFavoriteKinds() ([]api.FavoriteKind, error) {
	return w.a.GetFavoriteKinds(api.UIContext(context.Background()))
}
func (w *API) SetKindFavorite(req api.KindFavoriteRequest) error {
	return w.a.SetKindFavorite(api.UIContext(context.Background()), req)
}
func (w *API) MoveFavoriteKind(req api.MoveFavoriteKindRequest) error {
	return w.a.MoveFavoriteKind(api.UIContext(context.Background()), req)
}
func (w *API) SetTargetState(provider, target, key, value string) error {
	return w.a.SetTargetState(api.UIContext(context.Background()), provider, target, key, value)
}
func (w *API) RecentObjects(provider, target string) ([]api.RecentObject, error) {
	return w.a.RecentObjects(api.UIContext(context.Background()), provider, target)
}
func (w *API) TouchRecent(req api.TouchRecentRequest) error {
	return w.a.TouchRecent(api.UIContext(context.Background()), req)
}

func (w *API) LogInfo(ref core.Ref) (core.LogInfo, error) {
	return w.a.LogInfo(api.UIContext(context.Background()), ref)
}
func (w *API) OpenLogStream(req api.LogStreamRequest) (api.LogStreamInfo, error) {
	return w.a.OpenLogStream(api.UIContext(context.Background()), req)
}
func (w *API) ExecInfo(ref core.Ref) (core.ExecInfo, error) {
	return w.a.ExecInfo(api.UIContext(context.Background()), ref)
}
func (w *API) OpenTerminal(req api.TerminalRequest) (api.TerminalInfo, error) {
	return w.a.OpenTerminal(api.UIContext(context.Background()), req)
}
func (w *API) ReopenTerminal(req api.ReopenTerminalRequest) (api.TerminalInfo, error) {
	return w.a.ReopenTerminal(api.UIContext(context.Background()), req)
}
func (w *API) ForgetTerminal(terminalID string) error {
	return w.a.ForgetTerminal(api.UIContext(context.Background()), terminalID)
}
func (w *API) PrepareAction(req api.ActionRequest) (core.ActionPlan, error) {
	return w.a.PrepareAction(api.UIContext(context.Background()), req)
}
func (w *API) RunAction(req api.ActionRunRequest) (core.ActionResult, error) {
	return w.a.RunAction(api.UIContext(context.Background()), req)
}
func (w *API) GetEditSource(ref core.Ref) (core.EditDoc, error) {
	return w.a.GetEditSource(api.UIContext(context.Background()), ref)
}
func (w *API) PrepareEdit(req api.EditPrepareRequest) (core.EditPlan, error) {
	return w.a.PrepareEdit(api.UIContext(context.Background()), req)
}
func (w *API) RunEdit(req api.EditRunRequest) (core.EditResult, error) {
	return w.a.RunEdit(api.UIContext(context.Background()), req)
}
func (w *API) GetValues(ref core.Ref) (core.ValueList, error) {
	return w.a.GetValues(api.UIContext(context.Background()), ref)
}
func (w *API) RevealValue(req api.ValueRevealRequest) (core.Value, error) {
	return w.a.RevealValue(api.UIContext(context.Background()), req)
}
func (w *API) PrepareValueEdit(req api.ValueEditRequest) (core.ValuePlan, error) {
	return w.a.PrepareValueEdit(api.UIContext(context.Background()), req)
}
func (w *API) RunValueEdit(req api.ValueRunRequest) (core.ValueResult, error) {
	return w.a.RunValueEdit(api.UIContext(context.Background()), req)
}
func (w *API) ForwardInfo(ref core.Ref) (core.ForwardInfo, error) {
	return w.a.ForwardInfo(api.UIContext(context.Background()), ref)
}
func (w *API) StartForward(req api.StartForwardRequest) (forwards.Info, error) {
	return w.a.StartForward(api.UIContext(context.Background()), req)
}
func (w *API) StopForward(id string) error {
	return w.a.StopForward(api.UIContext(context.Background()), id)
}
func (w *API) ListForwards() ([]forwards.Info, error) {
	return w.a.ListForwards(api.UIContext(context.Background()))
}
func (w *API) StreamBase() (string, error) {
	return w.a.StreamBase(api.UIContext(context.Background()))
}
func (w *API) AgentAccessStatus() (api.AgentAccessStatus, error) {
	return w.a.AgentAccessStatus(api.UIContext(context.Background()))
}
func (w *API) ListAgentGrants() ([]agentgrant.Target, error) {
	return w.a.ListAgentGrants(api.UIContext(context.Background()))
}
func (w *API) SaveAgentGrants(req api.SaveAgentGrantsRequest) error {
	return w.a.SaveAgentGrants(api.UIContext(context.Background()), req)
}
func (w *API) RevokeAllAgentGrants() error {
	return w.a.RevokeAllAgentGrants(api.UIContext(context.Background()))
}
func (w *API) ReconfirmAgentTarget(req api.ReconfirmAgentTargetRequest) error {
	return w.a.ReconfirmAgentTarget(api.UIContext(context.Background()), req)
}
func (w *API) ListAgentPending() ([]api.AgentPending, error) {
	return w.a.ListAgentPending(api.UIContext(context.Background()))
}
func (w *API) DecideAgentPending(req api.DecideAgentPendingRequest) error {
	return w.a.DecideAgentPending(api.UIContext(context.Background()), req)
}
func (w *API) ListAgentAudit(f store.AuditFilter) ([]store.AuditEntry, error) {
	return w.a.ListAgentAudit(api.UIContext(context.Background()), f)
}

func (w *API) Helm(ctx context.Context, req api.HelmRequest) (api.HelmResponse, error) {
	return w.a.Helm(api.UIContext(ctx), req)
}

func (w *API) Configurations(req kubernetes.ConfigRequest) (kubernetes.ConfigState, error) {
	return w.a.Configurations(api.UIContext(context.Background()), req)
}

func (w *API) Files(req api.FilesRequest) (api.FilesResponse, error) {
	return w.a.Files(api.UIContext(context.Background()), req)
}

func (w *API) ClusterGraph(ctx context.Context, req api.GraphRequest) (core.Graph, error) {
	return w.a.ClusterGraph(api.UIContext(ctx), req)
}

func (w *API) ClusterTimeline(ctx context.Context, req api.TimelineRequest) (core.Timeline, error) {
	return w.a.ClusterTimeline(api.UIContext(ctx), req)
}

func (w *API) RBACSnapshot(ctx context.Context, req api.RBACRequest) (core.RBACSnapshot, error) {
	return w.a.RBACSnapshot(api.UIContext(ctx), req)
}
func (w *API) CheckAccess(ctx context.Context, req api.AccessRequest) (core.AccessReview, error) {
	return w.a.CheckAccess(api.UIContext(ctx), req)
}
