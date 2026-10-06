package kubernetes

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/helm"
	"helm.sh/helm/v4/pkg/action"
)

// HelmConfiguration shares the admitted session's identity, proxy and exec shim.
func (s *session) HelmConfiguration(ctx context.Context, namespace string, storage helm.Storage) (*action.Configuration, error) {
	operationCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	context.AfterFunc(operationCtx, func() { stop() })
	config, err := helm.Configuration(operationCtx, s.conn.cfg, namespace, storage)
	if err != nil {
		cancel()
	}
	return config, err
}

// HelmResourceRef links declared resources to the current provider catalogue.
// It does not claim that an object exists or is healthy; opening it performs
// the usual fresh read and establishes the actual UID.
func (s *session) HelmResourceRef(r helm.Resource, namespace string) *core.Ref {
	id := s.kindFor(r.APIVersion, r.Kind)
	def := s.kind(id)
	if def == nil || r.Name == "" {
		return nil
	}
	scope := ""
	if def.desc.Scoped {
		scope = r.Namespace
		if scope == "" {
			scope = namespace
		}
	}
	return &core.Ref{Provider: ProviderID, Target: s.target, Kind: id, Scope: scope, Name: r.Name}
}
