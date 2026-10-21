package api

import (
	"context"
	"errors"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"time"
)

type ArgoRequest struct {
	Provider string        `json:"provider"`
	Target   string        `json:"target"`
	Scope    core.ScopeSel `json:"scope"`
}

func argoKind() core.KindDescriptor {
	return core.KindDescriptor{ID: "ocular.argocd", Title: "Argo CD", Group: "Cluster", Workspace: "argocd", Scoped: true, Columns: []core.Column{}}
}

func (s *Service) ArgoCD(ctx context.Context, req ArgoRequest) (core.ArgoSnapshot, error) {
	if !fromUI(ctx) {
		return core.ArgoSnapshot{}, coded("forbidden", errors.New("inspection of Argo CD is available only to the application UI"))
	}
	if !req.Scope.Valid() || req.Scope.Mode == core.ScopeNone {
		return core.ArgoSnapshot{}, coded(CodeBadRequest, errors.New("invalid namespace selection"))
	}
	e, err := s.sessionFor(ctx, req.Provider, req.Target)
	if err != nil {
		return core.ArgoSnapshot{}, err
	}
	source, ok := e.sess.(provider.ArgoSource)
	if !ok {
		return core.ArgoSnapshot{}, coded(CodeUnsupported, errors.New("connection does not support Argo CD inspection"))
	}
	ctx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	return source.ArgoCD(ctx, req.Scope)
}
