package api

import (
	"context"
	"errors"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"time"
)

func (s *Service) CertificateStatus(ctx context.Context, ref core.Ref) (core.CertificateStatus, error) {
	if !fromUI(ctx) {
		return core.CertificateStatus{}, coded("forbidden", errors.New("certificate status is available only to the application UI"))
	}
	if ref.UID == "" || ref.Name == "" || ref.Scope == "" || len(ref.UID) > 256 || len(ref.Name) > 253 || len(ref.Scope) > 253 || ref.Kind != "cert-manager.io/certificates" {
		return core.CertificateStatus{}, coded(CodeBadRequest, errors.New("certificate status requires a namespaced UID-pinned Certificate"))
	}
	e, err := s.sessionFor(ctx, ref.Provider, ref.Target)
	if err != nil {
		return core.CertificateStatus{}, err
	}
	source, ok := e.sess.(provider.CertificateSource)
	if !ok {
		return core.CertificateStatus{}, coded(CodeUnsupported, errors.New("connection does not support certificate status"))
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := source.CertificateStatus(ctx, ref)
	if err != nil {
		return core.CertificateStatus{}, fromProvider(err)
	}
	return out, nil
}
