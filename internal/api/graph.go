package api

import (
	"context"
	"errors"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"time"
)

type GraphRequest struct {
	Provider string        `json:"provider"`
	Target   string        `json:"target"`
	Scope    core.ScopeSel `json:"scope"`
}

func graphKind() core.KindDescriptor {
	return core.KindDescriptor{ID: "ocular.graph", Title: "Cluster graph", Group: "Cluster", Workspace: "graph", Scoped: true, Columns: []core.Column{}}
}
func (s *Service) ClusterGraph(ctx context.Context, req GraphRequest) (core.Graph, error) {
	if !fromUI(ctx) {
		return core.Graph{}, coded("forbidden", errors.New("cluster graph is available only to the application UI"))
	}
	if !req.Scope.Valid() || req.Scope.Mode == core.ScopeNone {
		return core.Graph{}, coded(CodeBadRequest, errors.New("invalid namespace selection"))
	}
	e, err := s.sessionFor(ctx, req.Provider, req.Target)
	if err != nil {
		return core.Graph{}, err
	}
	source, ok := e.sess.(provider.GraphSource)
	if !ok {
		return core.Graph{}, coded(CodeUnsupported, errors.New("connection does not support a cluster graph"))
	}
	ctx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	return source.Graph(ctx, req.Scope)
}
