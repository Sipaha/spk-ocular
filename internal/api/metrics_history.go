package api

import (
	"context"
	"errors"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/metricshistory"
	"github.com/spk/spk-ocular/internal/provider"
)

func (s *Service) ResourceMetricsHistory(ctx context.Context, req core.HistoryRequest) (core.MetricsHistory, error) {
	if !fromUI(ctx) {
		return core.MetricsHistory{}, coded("forbidden", errors.New("metrics history is available only to the application UI"))
	}
	if req.Ref.Provider != "kubernetes" || req.Ref.Kind != "pods" || !metricshistory.ValidUID(req.Ref.UID) || req.Ref.Scope == "" || len(req.Ref.Scope) > 253 || req.Ref.Name == "" || len(req.Ref.Name) > 253 {
		return core.MetricsHistory{}, coded(CodeBadRequest, errors.New("metrics history requires a namespaced UID-pinned Pod"))
	}
	if err := metricshistory.Validate(req.Source, req.Window); err != nil {
		return core.MetricsHistory{}, coded(CodeBadRequest, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	e, err := s.sessionFor(ctx, req.Ref.Provider, req.Ref.Target)
	if err != nil {
		return core.MetricsHistory{}, err
	}
	source, ok := e.sess.(provider.MetricsHistorySource)
	if !ok {
		return core.MetricsHistory{}, coded(CodeUnsupported, errors.New("connection does not support metrics history"))
	}
	out, err := source.ResourceMetricsHistory(ctx, req)
	if err != nil {
		return core.MetricsHistory{}, fromProvider(err)
	}
	current, err := s.sessionFor(ctx, req.Ref.Provider, req.Ref.Target)
	if err != nil {
		return core.MetricsHistory{}, err
	}
	if current != e {
		return core.MetricsHistory{}, coded(CodeGone, errors.New("connection changed"))
	}
	if ctx.Err() != nil {
		return core.MetricsHistory{}, ctx.Err()
	}
	return out, nil
}
