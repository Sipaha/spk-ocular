package api

import (
	"context"
	"errors"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"time"
)

func (s *Service) ResourceNetworkPath(ctx context.Context, ref core.Ref) (core.NetworkPath, error) {
	if !fromUI(ctx) {
		return core.NetworkPath{}, coded("forbidden", errors.New("network path is available only to the application UI"))
	}
	if ref.UID == "" || ref.Name == "" || ref.Scope == "" || len(ref.UID) > 256 || len(ref.Name) > 253 || len(ref.Scope) > 253 || (ref.Kind != "services" && ref.Kind != "networking.k8s.io/ingresses") {
		return core.NetworkPath{}, coded(CodeBadRequest, errors.New("network path requires a namespaced UID-pinned Service or Ingress"))
	}
	e, err := s.sessionFor(ctx, ref.Provider, ref.Target)
	if err != nil {
		return core.NetworkPath{}, err
	}
	source, ok := e.sess.(provider.NetworkPathSource)
	if !ok {
		return core.NetworkPath{}, coded(CodeUnsupported, errors.New("connection does not support network path"))
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := source.ResourceNetworkPath(ctx, ref)
	if err != nil {
		return core.NetworkPath{}, fromProvider(err)
	}
	return out, nil
}
