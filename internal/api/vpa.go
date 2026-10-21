package api

import (
	"context"
	"errors"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"time"
)

func (s *Service) ResourceVPA(ctx context.Context, ref core.Ref) (core.VPAInspection, error) {
	if !fromUI(ctx) {
		return core.VPAInspection{}, coded("forbidden", errors.New("VPA inspection is available only to the application UI"))
	}
	if ref.Provider != "kubernetes" || ref.UID == "" || len(ref.UID) > 256 || ref.Name == "" || len(ref.Name) > 253 || ref.Scope == "" || len(ref.Scope) > 253 || (ref.Kind != "apps/deployments" && ref.Kind != "apps/statefulsets" && ref.Kind != "apps/daemonsets") {
		return core.VPAInspection{}, coded(CodeBadRequest, errors.New("VPA inspection requires a namespaced UID-pinned workload"))
	}
	// The deadline includes session resolution and the provider's shared gate.
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	e, err := s.sessionFor(ctx, ref.Provider, ref.Target)
	if err != nil {
		return core.VPAInspection{}, err
	}
	source, ok := e.sess.(provider.VPASource)
	if !ok {
		return core.VPAInspection{}, coded(CodeUnsupported, errors.New("connection does not support VPA inspection"))
	}
	out, err := source.ResourceVPA(ctx, ref)
	if err != nil {
		return core.VPAInspection{}, fromProvider(err)
	}
	current, err := s.sessionFor(ctx, ref.Provider, ref.Target)
	if err != nil {
		return core.VPAInspection{}, err
	}
	if current != e {
		return core.VPAInspection{}, coded(CodeGone, errors.New("connection changed"))
	}
	if ctx.Err() != nil {
		return core.VPAInspection{}, ctx.Err()
	}
	return out, nil
}
