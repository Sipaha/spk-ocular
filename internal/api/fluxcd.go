package api

import (
	"context"
	"errors"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"time"
)

type FluxRequest struct {
	Provider string        `json:"provider"`
	Target   string        `json:"target"`
	Scope    core.ScopeSel `json:"scope"`
}

func fluxKinds(kinds []core.KindDescriptor) []core.KindDescriptor {
	out := []core.KindDescriptor{}
	for _, k := range core.FluxResourceKinds {
		id := k.Group + "/" + k.Resource
		for _, served := range kinds {
			if served.ID == id && served.Scoped && served.Singular == k.Kind {
				if len(out) == 0 {
					out = append(out, core.KindDescriptor{ID: "ocular.fluxcd", Title: "Dashboard", Group: "Flux", Workspace: "fluxcd", Scoped: true, Columns: []core.Column{}})
				}
				out = append(out, core.KindDescriptor{ID: "ocular.fluxcd." + k.Resource, Title: k.Kind, Group: "Flux", Subgroup: k.Section, Workspace: "fluxcd-" + k.Resource, Scoped: true, Columns: []core.Column{}})
				break
			}
		}
	}
	return out
}
func (s *Service) FluxCD(ctx context.Context, req FluxRequest) (core.FluxSnapshot, error) {
	if !fromUI(ctx) {
		return core.FluxSnapshot{}, coded("forbidden", errors.New("inspection of Flux is available only to the application UI"))
	}
	if !req.Scope.Valid() || req.Scope.Mode == core.ScopeNone {
		return core.FluxSnapshot{}, coded(CodeBadRequest, errors.New("invalid namespace selection"))
	}
	e, err := s.sessionFor(ctx, req.Provider, req.Target)
	if err != nil {
		return core.FluxSnapshot{}, err
	}
	source, ok := e.sess.(provider.FluxSource)
	if !ok {
		return core.FluxSnapshot{}, coded(CodeUnsupported, errors.New("connection does not support Flux inspection"))
	}
	ctx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	return source.FluxCD(ctx, req.Scope)
}
