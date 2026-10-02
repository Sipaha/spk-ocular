package api

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
)

const prefFavoriteKinds = "favorite_kinds"

func (s *Service) GetFavoriteKinds(ctx context.Context) ([]FavoriteKind, error) {
	s.favoritesMu.Lock()
	defer s.favoritesMu.Unlock()
	return s.favoriteKinds(ctx)
}

func (s *Service) favoriteKinds(ctx context.Context) ([]FavoriteKind, error) {
	raw, err := s.store.GetUIPref(ctx, prefFavoriteKinds)
	if err != nil {
		return nil, coded(CodeInternal, err)
	}
	items := []FavoriteKind{}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &items); err != nil {
			return nil, coded(CodeInternal, err)
		}
	}
	if items == nil {
		items = []FavoriteKind{}
	}
	return items, nil
}

// One idempotent membership change under a lock; independent windows adding
// different kinds cannot replace each other's global favorites. No target is
// opened, and temporarily unavailable discovered kinds stay remembered.
func (s *Service) SetKindFavorite(ctx context.Context, req KindFavoriteRequest) error {
	if req.Provider == "" || req.Kind == "" || len(req.Provider) > 128 || len(req.Kind) > 512 {
		return coded(CodeBadRequest, errors.New("invalid favorite kind"))
	}
	s.favoritesMu.Lock()
	defer s.favoritesMu.Unlock()
	items, err := s.favoriteKinds(ctx)
	if err != nil {
		return err
	}
	item := FavoriteKind{Provider: req.Provider, Kind: req.Kind}
	index := slices.Index(items, item)
	if req.Favorite && index < 0 {
		if len(items) >= 512 {
			return coded(CodeBadRequest, errors.New("too many favorite kinds"))
		}
		items = append(items, item)
	} else if !req.Favorite && index >= 0 {
		items = slices.Delete(items, index, index+1)
	} else {
		return nil
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return coded(CodeInternal, err)
	}
	if err := s.store.SetUIPref(ctx, prefFavoriteKinds, string(raw)); err != nil {
		return coded(CodeInternal, err)
	}
	return nil
}

func (s *Service) MoveFavoriteKind(ctx context.Context, req MoveFavoriteKindRequest) error {
	s.favoritesMu.Lock()
	defer s.favoritesMu.Unlock()
	items, err := s.favoriteKinds(ctx)
	if err != nil {
		return err
	}
	item := FavoriteKind{Provider: req.Provider, Kind: req.Kind}
	index := slices.Index(items, item)
	if index < 0 {
		return coded(CodeBadRequest, errors.New("kind is not a favorite"))
	}
	if req.Before == req.Kind {
		return nil
	}
	items = slices.Delete(items, index, index+1)
	before := len(items)
	if req.Before != "" {
		before = slices.Index(items, FavoriteKind{Provider: req.Provider, Kind: req.Before})
		if before < 0 {
			return coded(CodeBadRequest, errors.New("destination is not a favorite"))
		}
	}
	items = slices.Insert(items, before, item)
	raw, err := json.Marshal(items)
	if err != nil {
		return coded(CodeInternal, err)
	}
	if err := s.store.SetUIPref(ctx, prefFavoriteKinds, string(raw)); err != nil {
		return coded(CodeInternal, err)
	}
	return nil
}
