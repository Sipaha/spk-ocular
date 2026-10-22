package api

import (
	"context"
	"errors"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"time"
)

type SecurityRequest struct {
	IncludeClusterReports       bool          `json:"includeClusterReports"`
	IncludeClusterPolicyReports bool          `json:"includeClusterPolicyReports"`
	PolicyReportAPI             string        `json:"policyReportAPI"`
	Provider                    string        `json:"provider"`
	Target                      string        `json:"target"`
	Scope                       core.ScopeSel `json:"scope"`
}

func securityKind() core.KindDescriptor {
	return core.KindDescriptor{ID: "ocular.security", Title: "Security", Group: "Cluster", Workspace: "security", Scoped: true, Columns: []core.Column{}}
}

func (s *Service) ClusterSecurity(ctx context.Context, req SecurityRequest) (core.SecuritySnapshot, error) {
	if !fromUI(ctx) {
		return core.SecuritySnapshot{}, coded("forbidden", errors.New("security inspection is available only to the application UI"))
	}
	if !req.Scope.Valid() || req.Scope.Mode == core.ScopeNone {
		return core.SecuritySnapshot{}, coded(CodeBadRequest, errors.New("invalid namespace selection"))
	}
	options := core.SecurityOptions{IncludeClusterReports: req.IncludeClusterReports, PolicyReportAPI: req.PolicyReportAPI, IncludeClusterPolicyReports: req.IncludeClusterPolicyReports}
	if !options.Valid() {
		return core.SecuritySnapshot{}, coded(CodeBadRequest, errors.New("unsupported policy report API selection"))
	}
	ctx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	e, err := s.sessionFor(ctx, req.Provider, req.Target)
	if err != nil {
		return core.SecuritySnapshot{}, err
	}
	source, ok := e.sess.(provider.SecuritySource)
	if !ok {
		return core.SecuritySnapshot{}, coded(CodeUnsupported, errors.New("connection does not support security inspection"))
	}
	out, err := source.Security(ctx, req.Scope, options)
	if err != nil {
		return core.SecuritySnapshot{}, fromProvider(err)
	}
	current, err := s.sessionFor(ctx, req.Provider, req.Target)
	if err != nil {
		return core.SecuritySnapshot{}, err
	}
	if current != e {
		return core.SecuritySnapshot{}, coded(CodeGone, errors.New("connection changed"))
	}
	if ctx.Err() != nil {
		return core.SecuritySnapshot{}, ctx.Err()
	}
	return out, nil
}
