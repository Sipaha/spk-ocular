package api

import (
	"context"
	"fmt"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/store"
)

// Bounds of the recent objects (the palette's "recent"): small UI state.
const (
	recentPerTarget = 50
	recentTotal     = 500
	maxRecentField  = 1024
)

// RecentObject is an object whose details were opened; its Ref carries the
// UID it had then.
type RecentObject struct {
	Ref   core.Ref `json:"ref"`
	Title string   `json:"title"`
	// OpenedAt is unix milliseconds.
	OpenedAt int64 `json:"openedAt"`
}

type TouchRecentRequest struct {
	Ref   core.Ref `json:"ref"`
	Title string   `json:"title"`
}

func (s *Service) RecentObjects(ctx context.Context, providerID, target string) ([]RecentObject, error) {
	rs, err := s.store.RecentObjects(ctx, providerID, target)
	if err != nil {
		return nil, coded(CodeInternal, err)
	}
	out := make([]RecentObject, 0, len(rs))
	for _, r := range rs {
		out = append(out, RecentObject{
			Ref:      core.Ref{Provider: r.Provider, Target: r.Target, Scope: r.Scope, Kind: r.Kind, Name: r.Name, UID: r.UID},
			Title:    r.Title,
			OpenedAt: r.OpenedAt.UnixMilli(),
		})
	}
	return out, nil
}

func (s *Service) TouchRecent(ctx context.Context, req TouchRecentRequest) error {
	ref := req.Ref
	if ref.Provider == "" || ref.Target == "" || ref.Kind == "" || ref.Name == "" {
		return coded(CodeBadRequest, fmt.Errorf("incomplete object %v", ref))
	}
	if _, ok := s.reg.Get(ref.Provider); !ok {
		return coded(CodeBadRequest, fmt.Errorf("unknown provider %q", ref.Provider))
	}
	for _, f := range []string{ref.Target, ref.Scope, ref.Kind, ref.Name, ref.UID, req.Title} {
		if len(f) > maxRecentField {
			return coded(CodeBadRequest, fmt.Errorf("a field of %v is over %d bytes", ref.Kind, maxRecentField))
		}
	}
	err := s.store.TouchRecent(ctx, store.Recent{
		Provider: ref.Provider, Target: ref.Target, Kind: ref.Kind, Scope: ref.Scope, Name: ref.Name, UID: ref.UID,
		Title: req.Title, OpenedAt: s.now(),
	}, recentPerTarget, recentTotal)
	if err != nil {
		return coded(CodeInternal, err)
	}
	return nil
}
