package api

import (
	"context"
	"errors"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"time"
)

type TimelineRequest struct {
	Provider string        `json:"provider"`
	Target   string        `json:"target"`
	Scope    core.ScopeSel `json:"scope"`
}

func timelineKind() core.KindDescriptor {
	return core.KindDescriptor{ID: "ocular.timeline", Title: "Events timeline", Group: "Cluster", Workspace: "timeline", Scoped: true, Columns: []core.Column{}}
}

func (s *Service) ClusterTimeline(ctx context.Context, req TimelineRequest) (core.Timeline, error) {
	if !fromUI(ctx) {
		return core.Timeline{}, coded("forbidden", errors.New("events timeline is available only to the application UI"))
	}
	if !req.Scope.Valid() || req.Scope.Mode == core.ScopeNone {
		return core.Timeline{}, coded(CodeBadRequest, errors.New("invalid namespace selection"))
	}
	e, err := s.sessionFor(ctx, req.Provider, req.Target)
	if err != nil {
		return core.Timeline{}, err
	}
	source, ok := e.sess.(provider.TimelineSource)
	if !ok {
		return core.Timeline{}, coded(CodeUnsupported, errors.New("connection does not support an events timeline"))
	}
	ctx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	return source.Timeline(ctx, req.Scope)
}
