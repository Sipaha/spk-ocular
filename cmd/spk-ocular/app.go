package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"k8s.io/klog/v2"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/spk/spk-ocular/internal/paths"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/kubernetes"
	"github.com/spk/spk-ocular/internal/providers/synthetic"
	"github.com/spk/spk-ocular/internal/store"
)

// appCore is what both modes share: data dir, store, providers, the API service.
type appCore struct {
	// Synthetic is the test provider (--test-synthetic), else nil.
	Synthetic *synthetic.Provider
	Paths     paths.Paths
	Store     *store.Store
	Emitter   *events.Emitter
	Service   *api.Service
}

// newCore wires everything and starts the service's watchers. Nothing here
// touches the network: startup stays fast with an unreachable cluster.
func newCore(ctx context.Context, mode string, withSynthetic bool) (*appCore, error) {
	quietClientGo()
	p, err := paths.Resolve()
	if err != nil {
		return nil, err
	}
	if err := p.Ensure(); err != nil {
		return nil, err
	}
	st, err := store.Open(ctx, p.DBFile)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	self, _ := os.Executable() // runs kubeconfig exec plugins with a timeout (internal/execshim)
	providers := []provider.Provider{kubernetes.New().WithExecShim(self)}
	var syn *synthetic.Provider
	if withSynthetic {
		syn = synthetic.New()
		providers = append(providers, syn)
	}
	reg, err := provider.NewRegistry(providers...)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	em := events.NewEmitter()
	svc := api.NewService(reg, st, em, api.Options{Version: version, Mode: mode, Getenv: os.Getenv})
	svc.Start(ctx)
	return &appCore{Synthetic: syn, Paths: p, Store: st, Emitter: em, Service: svc}, nil
}

// Close stops the service before the store it writes to.
func (c *appCore) Close() {
	c.Service.Close()
	_ = c.Store.Close()
}

// quietClientGo silences client-go's klog output (every failed watch retry
// is an error line on stderr): the same failures reach the UI as view
// statuses. SPK_OCULAR_KLOG=1 keeps it for debugging.
func quietClientGo() {
	if os.Getenv("SPK_OCULAR_KLOG") == "" {
		klog.SetSlogLogger(slog.New(slog.DiscardHandler))
	}
}
