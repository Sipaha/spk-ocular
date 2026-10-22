package api

import (
	"context"
	"errors"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"k8s.io/apimachinery/pkg/util/validation"
	"time"
)

func (s *Service) NodeCapacity(ctx context.Context, req core.NodeCapacityRequest) (core.NodeCapacity, error) {
	if !fromUI(ctx) {
		return core.NodeCapacity{}, coded("forbidden", errors.New("node declarations are available only to the application UI"))
	}
	r := req.Ref
	if r.Kind != "nodes" || r.Scope != "" || r.UID == "" || len(r.UID) > 256 || r.Name == "" || len(validation.IsDNS1123Subdomain(r.Name)) > 0 || len(validation.IsDNS1123Label(req.Namespace)) > 0 {
		return core.NodeCapacity{}, coded(CodeBadRequest, errors.New("node declarations require a UID-pinned Node and one explicit namespace"))
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	e, err := s.sessionFor(ctx, r.Provider, r.Target)
	if err != nil {
		return core.NodeCapacity{}, err
	}
	source, ok := e.sess.(provider.NodeCapacitySource)
	if !ok {
		return core.NodeCapacity{}, coded(CodeUnsupported, errors.New("connection does not support node declarations"))
	}
	out, err := source.NodeCapacity(ctx, req)
	if err != nil {
		return core.NodeCapacity{}, fromProvider(err)
	}
	current, err := s.sessionFor(ctx, r.Provider, r.Target)
	if err != nil {
		return core.NodeCapacity{}, err
	}
	if current != e {
		return core.NodeCapacity{}, coded(CodeGone, errors.New("connection changed"))
	}
	if ctx.Err() != nil {
		return core.NodeCapacity{}, ctx.Err()
	}
	return out, nil
}
