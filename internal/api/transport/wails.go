//go:build wails

package transport

import (
	"context"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/core"
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

func (w *API) ListKinds(provider, target string) ([]core.KindDescriptor, error) {
	return w.a.ListKinds(context.Background(), provider, target)
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
func (w *API) CloseView(viewID string) error { return w.a.CloseView(context.Background(), viewID) }
func (w *API) TouchViews(viewIDs []string) ([]string, error) {
	return w.a.TouchViews(context.Background(), viewIDs)
}
func (w *API) GetResource(ref core.Ref) (*core.Resource, error) {
	return w.a.GetResource(context.Background(), ref)
}
func (w *API) GetMetrics(viewID string) (api.MetricsView, error) {
	return w.a.GetMetrics(context.Background(), viewID)
}
func (w *API) GetTargetState(provider, target string) (map[string]string, error) {
	return w.a.GetTargetState(context.Background(), provider, target)
}
func (w *API) SetTargetState(provider, target, key, value string) error {
	return w.a.SetTargetState(context.Background(), provider, target, key, value)
}

func (w *API) LogInfo(ref core.Ref) (core.LogInfo, error) {
	return w.a.LogInfo(context.Background(), ref)
}
func (w *API) OpenLogStream(req api.LogStreamRequest) (api.LogStreamInfo, error) {
	return w.a.OpenLogStream(context.Background(), req)
}
func (w *API) StreamBase() (string, error) { return w.a.StreamBase(context.Background()) }
