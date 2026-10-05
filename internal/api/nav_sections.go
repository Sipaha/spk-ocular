package api

import (
	"context"
	"encoding/json"
	"errors"
)

const prefNavSections = "nav_sections"

// Navigation expansion is shared by all targets. Update one section at a time
// so independent changes never overwrite other sections or unavailable groups.
func (s *Service) GetNavSections(ctx context.Context) (map[string]bool, error) {
	s.navSectionsMu.Lock()
	defer s.navSectionsMu.Unlock()
	return s.navSections(ctx)
}

func (s *Service) navSections(ctx context.Context) (map[string]bool, error) {
	raw, err := s.store.GetUIPref(ctx, prefNavSections)
	if err != nil {
		return nil, coded(CodeInternal, err)
	}
	sections := map[string]bool{}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &sections); err != nil {
			return nil, coded(CodeInternal, err)
		}
		if sections == nil {
			sections = map[string]bool{}
		}
		return sections, nil
	}
	// One-time migration: the last selected target supplies the former
	// per-target expansion. No target is opened or contacted.
	selected, err := s.selected(ctx)
	if err != nil {
		return nil, coded(CodeInternal, err)
	}
	if selected != nil {
		legacy, err := s.store.GetTargetState(ctx, selected.Provider, selected.ID, "navOpen")
		if err != nil {
			return nil, coded(CodeInternal, err)
		}
		var keys []string
		if json.Unmarshal([]byte(legacy), &keys) == nil {
			for _, key := range keys {
				if validNavSection(key) && len(sections) < 2048 {
					sections[key] = true
				}
			}
		}
	}
	if err := s.saveNavSections(ctx, sections); err != nil {
		return nil, err
	}
	return sections, nil
}

func validNavSection(key string) bool { return key != "" && len(key) <= 1024 }

func (s *Service) SetNavSection(ctx context.Context, req NavSectionRequest) error {
	if !validNavSection(req.Key) {
		return coded(CodeBadRequest, errors.New("invalid navigation section"))
	}
	s.navSectionsMu.Lock()
	defer s.navSectionsMu.Unlock()
	sections, err := s.navSections(ctx)
	if err != nil {
		return err
	}
	if _, exists := sections[req.Key]; !exists && len(sections) >= 2048 {
		return coded(CodeBadRequest, errors.New("too many navigation sections"))
	}
	sections[req.Key] = req.Open
	return s.saveNavSections(ctx, sections)
}

func (s *Service) saveNavSections(ctx context.Context, sections map[string]bool) error {
	raw, err := json.Marshal(sections)
	if err != nil {
		return coded(CodeInternal, err)
	}
	if err := s.store.SetUIPref(ctx, prefNavSections, string(raw)); err != nil {
		return coded(CodeInternal, err)
	}
	return nil
}
