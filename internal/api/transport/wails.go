//go:build wails

package transport

import (
	"context"

	"github.com/spk/spk-ocular/internal/api"
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
