package api

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

func (s *Service) GatekeeperAudit(ctx context.Context, ref core.Ref) (core.GatekeeperAudit, error) {
	if !fromUI(ctx) {
		return core.GatekeeperAudit{}, coded("forbidden", errors.New("constraint audit is available only to the application UI"))
	}
	if ref.UID == "" || len(ref.UID) > 256 || ref.Name == "" || len(ref.Name) > 253 || ref.Scope != "" || !strings.HasPrefix(ref.Kind, "constraints.gatekeeper.sh/") || strings.Count(ref.Kind, "/") != 1 || strings.TrimPrefix(ref.Kind, "constraints.gatekeeper.sh/") == "" || len(ref.Kind) > 320 {
		return core.GatekeeperAudit{}, coded(CodeBadRequest, errors.New("constraint audit requires one cluster-scoped UID-pinned constraint"))
	}
	e, err := s.sessionFor(ctx, ref.Provider, ref.Target)
	if err != nil {
		return core.GatekeeperAudit{}, err
	}
	source, ok := e.sess.(provider.GatekeeperSource)
	if !ok {
		return core.GatekeeperAudit{}, coded(CodeUnsupported, errors.New("connection does not support constraint audit"))
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := source.GatekeeperAudit(ctx, ref)
	if err != nil {
		return core.GatekeeperAudit{}, fromProvider(err)
	}
	return out, nil
}
