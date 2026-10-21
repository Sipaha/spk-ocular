package api

import (
	"context"
	"errors"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"time"
)

func (s *Service) DiagnoseResource(ctx context.Context, ref core.Ref) (core.Diagnosis, error) {
	if !fromUI(ctx) {
		return core.Diagnosis{}, coded("forbidden", errors.New("resource diagnosis is available only to the application UI"))
	}
	if ref.UID == "" || ref.Name == "" || ref.Kind == "" || ref.Scope == "" || len(ref.UID) > 256 || len(ref.Name) > 253 || len(ref.Scope) > 253 || len(ref.Kind) > 512 {
		return core.Diagnosis{}, coded(CodeBadRequest, errors.New("diagnosis requires a namespaced UID-pinned reference"))
	}
	e, err := s.sessionFor(ctx, ref.Provider, ref.Target)
	if err != nil {
		return core.Diagnosis{}, err
	}
	source, ok := e.sess.(provider.DiagnosisSource)
	if !ok {
		return core.Diagnosis{}, coded(CodeUnsupported, errors.New("connection does not support resource diagnosis"))
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := source.DiagnoseResource(ctx, ref)
	if err != nil {
		return core.Diagnosis{}, fromProvider(err)
	}
	return out, nil
}
