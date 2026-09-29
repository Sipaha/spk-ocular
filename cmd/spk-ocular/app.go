package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/spk/spk-ocular/internal/paths"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/kubernetes"
	"github.com/spk/spk-ocular/internal/store"
)

// appCore is what both modes share: data dir, store, providers, the API service.
type appCore struct {
	Paths   paths.Paths
	Store   *store.Store
	Emitter *events.Emitter
	Service *api.Service
}

// newCore wires everything and starts the service's watchers. Nothing here
// touches the network: startup stays fast with an unreachable cluster.
func newCore(ctx context.Context, mode string) (*appCore, error) {
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
	reg, err := provider.NewRegistry(kubernetes.New())
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	em := events.NewEmitter()
	svc := api.NewService(reg, st, em, api.Options{Version: version, Mode: mode, Getenv: os.Getenv})
	svc.Start(ctx)
	return &appCore{Paths: p, Store: st, Emitter: em, Service: svc}, nil
}

// Close stops the service before the store it writes to.
func (c *appCore) Close() {
	c.Service.Close()
	_ = c.Store.Close()
}
