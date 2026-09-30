//go:build wails

package transport

import (
	"context"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/forwards"
	"github.com/spk/spk-ocular/internal/views"
)

// API is the Wails service. Bindings are addressed by Go FQN:
//
//	github.com/spk/spk-ocular/internal/api/transport.API.<Method>
//
// (mirrored in web/src/api/client.ts).
type API struct{ a api.API }

func NewAPI(a api.API) *API { return &API{a: a} }

func (w *API) AppInfo() (api.AppInfo, error) { return w.a.AppInfo(context.Background()) }
func (w *API) ListTargets() (api.TargetsView, error) {
	return w.a.ListTargets(context.Background())
}
func (w *API) SelectTarget(provider, id string) error {
	return w.a.SelectTarget(context.Background(), provider, id)
}

func (w *API) ListKinds(provider, target string) (api.KindsView, error) {
	return w.a.ListKinds(context.Background(), provider, target)
}
func (w *API) RefreshKinds(provider, target string) error {
	return w.a.RefreshKinds(context.Background(), provider, target)
}
func (w *API) ListScopes(provider, target string) (api.ScopesView, error) {
	return w.a.ListScopes(context.Background(), provider, target)
}
func (w *API) OpenView(req api.OpenViewRequest) (api.ViewInfo, error) {
	return w.a.OpenView(context.Background(), req)
}
func (w *API) GetRows(viewID string, since uint64) (views.Page, error) {
	return w.a.GetRows(context.Background(), viewID, since)
}
func (w *API) CloseView(viewID string) error  { return w.a.CloseView(context.Background(), viewID) }
func (w *API) ResyncView(viewID string) error { return w.a.ResyncView(context.Background(), viewID) }
func (w *API) TouchViews(viewIDs []string) ([]string, error) {
	return w.a.TouchViews(context.Background(), viewIDs)
}
func (w *API) GetResource(ref core.Ref) (*core.Resource, error) {
	return w.a.GetResource(context.Background(), ref)
}

// GetMetrics takes the call's context: the page cancels a request it no
// longer waits for (the Wails runtime's CancellablePromise.cancel). That
// cancel is lost when it overtakes the call, so the page also sends
// CancelMetrics, and each request carries its seq.
func (w *API) GetMetrics(ctx context.Context, req api.MetricsRequest) (api.MetricsView, error) {
	return w.a.GetMetrics(ctx, req)
}
func (w *API) CancelMetrics(viewID string, seq uint64) error {
	return w.a.CancelMetrics(context.Background(), viewID, seq)
}
func (w *API) GetTargetState(provider, target string) (map[string]string, error) {
	return w.a.GetTargetState(context.Background(), provider, target)
}
func (w *API) SetTargetState(provider, target, key, value string) error {
	return w.a.SetTargetState(context.Background(), provider, target, key, value)
}
func (w *API) RecentObjects(provider, target string) ([]api.RecentObject, error) {
	return w.a.RecentObjects(context.Background(), provider, target)
}
func (w *API) TouchRecent(req api.TouchRecentRequest) error {
	return w.a.TouchRecent(context.Background(), req)
}

func (w *API) LogInfo(ref core.Ref) (core.LogInfo, error) {
	return w.a.LogInfo(context.Background(), ref)
}
func (w *API) OpenLogStream(req api.LogStreamRequest) (api.LogStreamInfo, error) {
	return w.a.OpenLogStream(context.Background(), req)
}
func (w *API) ExecInfo(ref core.Ref) (core.ExecInfo, error) {
	return w.a.ExecInfo(context.Background(), ref)
}
func (w *API) OpenTerminal(req api.TerminalRequest) (api.TerminalInfo, error) {
	return w.a.OpenTerminal(context.Background(), req)
}
func (w *API) ReopenTerminal(req api.ReopenTerminalRequest) (api.TerminalInfo, error) {
	return w.a.ReopenTerminal(context.Background(), req)
}
func (w *API) ForgetTerminal(terminalID string) error {
	return w.a.ForgetTerminal(context.Background(), terminalID)
}
func (w *API) PrepareAction(req api.ActionRequest) (core.ActionPlan, error) {
	return w.a.PrepareAction(context.Background(), req)
}
func (w *API) RunAction(req api.ActionRunRequest) (core.ActionResult, error) {
	return w.a.RunAction(context.Background(), req)
}
func (w *API) GetEditSource(ref core.Ref) (core.EditDoc, error) {
	return w.a.GetEditSource(context.Background(), ref)
}
func (w *API) PrepareEdit(req api.EditPrepareRequest) (core.EditPlan, error) {
	return w.a.PrepareEdit(context.Background(), req)
}
func (w *API) RunEdit(req api.EditRunRequest) (core.EditResult, error) {
	return w.a.RunEdit(context.Background(), req)
}
func (w *API) GetValues(ref core.Ref) (core.ValueList, error) {
	return w.a.GetValues(context.Background(), ref)
}
func (w *API) RevealValue(req api.ValueRevealRequest) (core.Value, error) {
	return w.a.RevealValue(context.Background(), req)
}
func (w *API) PrepareValueEdit(req api.ValueEditRequest) (core.ValuePlan, error) {
	return w.a.PrepareValueEdit(context.Background(), req)
}
func (w *API) RunValueEdit(req api.ValueRunRequest) (core.ValueResult, error) {
	return w.a.RunValueEdit(context.Background(), req)
}
func (w *API) ForwardInfo(ref core.Ref) (core.ForwardInfo, error) {
	return w.a.ForwardInfo(context.Background(), ref)
}
func (w *API) StartForward(req api.StartForwardRequest) (forwards.Info, error) {
	return w.a.StartForward(context.Background(), req)
}
func (w *API) StopForward(id string) error { return w.a.StopForward(context.Background(), id) }
func (w *API) ListForwards() ([]forwards.Info, error) {
	return w.a.ListForwards(context.Background())
}
func (w *API) StreamBase() (string, error) { return w.a.StreamBase(context.Background()) }
