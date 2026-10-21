package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/spk/spk-ocular/internal/core"
)

type ViewPreset struct {
	Name   string         `json:"name"`
	Labels []string       `json:"labels"`
	Hidden []string       `json:"hidden"`
	Order  []string       `json:"order"`
	Filter string         `json:"filter"`
	Sort   *core.SortSpec `json:"sort,omitempty"`
}
type ViewPresetRegistry struct {
	Version  int          `json:"version"`
	Revision uint64       `json:"revision"`
	Active   string       `json:"active"`
	Presets  []ViewPreset `json:"presets"`
}
type ViewPresetsRequest struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	// Nil reads; saving uses the revision returned by the previous read.
	Registry *ViewPresetRegistry `json:"registry,omitempty"`
}

func validPresetString(s string, maxBytes int, empty bool) bool {
	if (!empty && strings.TrimSpace(s) == "") || len(s) > maxBytes || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func validatePresetRegistry(r ViewPresetRegistry) error {
	bad := errors.New("invalid or oversized view preset definitions")
	if r.Version != 1 || r.Presets == nil || len(r.Presets) > 4 || !validPresetString(r.Active, 64, true) {
		return bad
	}
	names := map[string]bool{}
	for _, p := range r.Presets {
		if p.Labels == nil || p.Hidden == nil || p.Order == nil || !validPresetString(p.Name, 64, false) || names[p.Name] || core.ValidLabelKeys(p.Labels) != nil || !validPresetString(p.Filter, 512, true) {
			return bad
		}
		names[p.Name] = true
		for _, ids := range [][]string{p.Hidden, p.Order} {
			if len(ids) > 64 {
				return bad
			}
			seen := map[string]bool{}
			for _, id := range ids {
				if !validPresetString(id, 512, false) || seen[id] {
					return bad
				}
				seen[id] = true
			}
		}
		for _, id := range p.Hidden {
			if id == "name" {
				return bad
			}
		}
		if p.Sort != nil && (!validPresetString(p.Sort.Column, 512, false) || !validPresetString(p.Sort.Then, 512, true)) {
			return bad
		}
	}
	if r.Active != "" && !names[r.Active] {
		return bad
	}
	b, err := json.Marshal(r)
	if err != nil || len(b) > 16<<10 {
		return bad
	}
	return nil
}

// ViewPresets is UI-local state. Reading/saving never opens a provider session.
func (s *Service) ViewPresets(ctx context.Context, req ViewPresetsRequest) (ViewPresetRegistry, error) {
	if !fromUI(ctx) {
		return ViewPresetRegistry{}, coded("forbidden", errors.New("view presets are available only to the application UI"))
	}
	if req.Provider != "kubernetes" || !validPresetString(req.Kind, 253, false) {
		return ViewPresetRegistry{}, coded(CodeBadRequest, errors.New("invalid preset scope"))
	}
	key, _ := json.Marshal([]string{req.Provider, req.Kind})
	s.viewPresetsMu.Lock()
	defer s.viewPresetsMu.Unlock()
	raw, err := s.store.GetUIPref(ctx, "view_presets."+string(key))
	if err != nil {
		return ViewPresetRegistry{}, coded(CodeInternal, err)
	}
	r := ViewPresetRegistry{Version: 1, Presets: []ViewPreset{}}
	if raw != "" {
		if len(raw) > 16<<10 || json.Unmarshal([]byte(raw), &r) != nil || validatePresetRegistry(r) != nil {
			return ViewPresetRegistry{}, coded(CodeInternal, errors.New("stored view presets are invalid; preserved without changes"))
		}
	}
	if req.Registry == nil {
		return r, nil
	}
	next := *req.Registry
	if err := validatePresetRegistry(next); err != nil {
		return ViewPresetRegistry{}, coded(CodeBadRequest, err)
	}
	if next.Revision != r.Revision {
		return ViewPresetRegistry{}, coded(CodeConflict, errors.New("view presets changed; reload before saving"))
	}
	next.Revision++
	b, err := json.Marshal(next)
	if err != nil || len(b) > 16<<10 {
		return ViewPresetRegistry{}, coded(CodeBadRequest, errors.New("view preset budget exceeded"))
	}
	if err = s.store.SetUIPref(ctx, "view_presets."+string(key), string(b)); err != nil {
		return ViewPresetRegistry{}, coded(CodeInternal, err)
	}
	return next, nil
}
