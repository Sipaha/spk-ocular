// Package provider defines what a provider (Kubernetes, Docker Compose, ...)
// implements and keeps the ordered set of registered providers.
package provider

import (
	"context"
	"fmt"

	"github.com/spk/spk-ocular/internal/core"
)

// Discovery is what a provider found: its targets plus non-fatal problems.
type Discovery struct {
	Targets  []core.Target
	Problems []core.Problem
}

// Provider is the minimal contract. Optional capabilities are separate
// interfaces checked with a type assertion (TargetWatcher, ...).
type Provider interface {
	ID() string
	Title() string
	// Discover lists targets from local configuration only — no network, so
	// it is safe on the startup path.
	Discover(ctx context.Context) (Discovery, error)
}

// TargetWatcher is implemented by providers whose target list can change
// while the app runs (kubeconfig edited). Watch blocks until ctx is done and
// calls onChange (from its own goroutine) after each change settles.
type TargetWatcher interface {
	Watch(ctx context.Context, onChange func()) error
}

// Registry is the ordered set of providers; order is the UI order.
type Registry struct {
	list []Provider
	byID map[string]Provider
}

func NewRegistry(ps ...Provider) (*Registry, error) {
	r := &Registry{byID: map[string]Provider{}}
	for _, p := range ps {
		if _, dup := r.byID[p.ID()]; dup {
			return nil, fmt.Errorf("provider %q registered twice", p.ID())
		}
		r.byID[p.ID()] = p
		r.list = append(r.list, p)
	}
	return r, nil
}

func (r *Registry) All() []Provider { return append([]Provider(nil), r.list...) }

func (r *Registry) Get(id string) (Provider, bool) {
	p, ok := r.byID[id]
	return p, ok
}
