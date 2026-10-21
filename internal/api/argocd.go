package api

import (
	"context"
	"errors"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"time"
)

type ArgoRequest struct {
	Settings string        `json:"settings,omitempty"`
	Provider string        `json:"provider"`
	Target   string        `json:"target"`
	Scope    core.ScopeSel `json:"scope"`
}

func argoKinds() []core.KindDescriptor {
	out := []core.KindDescriptor{}
	for _, page := range []struct{ id, title, workspace, subgroup string }{
		{"ocular.argocd", "Dashboard", "argocd", ""},
		{"ocular.argocd.applications", "Applications", "argocd-applications", ""},
		{"ocular.argocd.applicationsets", "Application Sets", "argocd-applicationsets", ""},
		{"ocular.argocd.projects", "Projects", "argocd-projects", ""},
		{"ocular.argocd.repositories", "Repositories", "argocd-repositories", "Settings"},
		{"ocular.argocd.clusters", "Clusters", "argocd-clusters", "Settings"},
	} {
		out = append(out, core.KindDescriptor{ID: page.id, Title: page.title, Group: "Argo CD", Subgroup: page.subgroup, Workspace: page.workspace, Scoped: true, Columns: []core.Column{}})
	}
	return out
}

func (s *Service) ArgoCD(ctx context.Context, req ArgoRequest) (core.ArgoSnapshot, error) {
	if !fromUI(ctx) {
		return core.ArgoSnapshot{}, coded("forbidden", errors.New("inspection of Argo CD is available only to the application UI"))
	}
	if req.Settings != "" && req.Settings != "repository" && req.Settings != "cluster" {
		return core.ArgoSnapshot{}, coded(CodeBadRequest, errors.New("invalid Argo CD settings kind"))
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
	if req.Settings != "" {
		settings, ok := e.sess.(provider.ArgoSettingsSource)
		if !ok {
			return core.ArgoSnapshot{}, coded(CodeUnsupported, errors.New("connection does not support Argo CD settings inspection"))
		}
		return settings.ArgoSettings(ctx, req.Scope, req.Settings)
	}
	return source.ArgoCD(ctx, req.Scope)
}
