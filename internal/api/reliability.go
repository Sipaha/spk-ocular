package api

import (
	"context"
	"errors"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"time"
)

func (s *Service) ResourceReliability(ctx context.Context, ref core.Ref) (core.Reliability, error) {
	if !fromUI(ctx) {
		return core.Reliability{}, coded("forbidden", errors.New("reliability is available only to the application UI"))
	}
	if ref.UID == "" || len(ref.UID) > 256 || ref.Name == "" || len(ref.Name) > 253 || ref.Scope == "" || len(ref.Scope) > 253 || (ref.Kind != "pods" && ref.Kind != "apps/deployments" && ref.Kind != "apps/statefulsets" && ref.Kind != "apps/daemonsets") {
		return core.Reliability{}, coded(CodeBadRequest, errors.New("reliability requires a namespaced UID-pinned workload"))
	}
	// The deadline includes session resolution and the provider's shared gate.
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	e, err := s.sessionFor(ctx, ref.Provider, ref.Target)
	if err != nil {
		return core.Reliability{}, err
	}
	source, ok := e.sess.(provider.ReliabilitySource)
	if !ok {
		return core.Reliability{}, coded(CodeUnsupported, errors.New("connection does not support reliability"))
	}
	out, err := source.ResourceReliability(ctx, ref)
	if err != nil {
		return core.Reliability{}, fromProvider(err)
	}
	current, err := s.sessionFor(ctx, ref.Provider, ref.Target)
	if err != nil {
		return core.Reliability{}, err
	}
	if current != e {
		return core.Reliability{}, coded(CodeGone, errors.New("connection changed"))
	}
	if ctx.Err() != nil {
		return core.Reliability{}, ctx.Err()
	}
	return out, nil
}
