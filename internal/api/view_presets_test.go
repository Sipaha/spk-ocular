package api

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestViewPresetsPersistByProviderKindWithRevisionAndNoTarget(t *testing.T) {
	s, _ := newService(t)
	ctx := UIContext(context.Background())
	q := ViewPresetsRequest{Provider: "kubernetes", Kind: "pods"}
	r, err := s.ViewPresets(ctx, q)
	require.NoError(t, err)
	require.Empty(t, r.Presets)
	r.Active = "Team"
	r.Presets = []ViewPreset{{Name: "Team", Labels: []string{"app.kubernetes.io/version", "x:y"}, Hidden: []string{"age"}, Order: []string{"name", "metadata.labels:x:y"}, Filter: "api", Sort: &core.SortSpec{Column: "metadata.labels:x:y"}}}
	q.Registry = &r
	saved, err := s.ViewPresets(ctx, q)
	require.NoError(t, err)
	require.Equal(t, uint64(1), saved.Revision)
	_, err = s.ViewPresets(ctx, q)
	require.Error(t, err, "stale windows must not overwrite a newer definition")
	other := NewService(s.reg, s.store, s.em, Options{})
	defer other.Close()
	q.Registry = nil
	read, err := other.ViewPresets(ctx, q)
	require.NoError(t, err)
	require.Equal(t, saved, read)
	q.Kind = "services"
	read, err = other.ViewPresets(ctx, q)
	require.NoError(t, err)
	require.Empty(t, read.Presets)
	_, err = s.ViewPresets(context.Background(), q)
	require.Error(t, err, "not an agent method")
	_, err = s.OpenView(context.Background(), OpenViewRequest{Query: provider.Query{Scope: core.ScopeSel{Mode: core.ScopeAll}, LabelKeys: []string{"a"}}})
	require.Error(t, err)
}
func TestViewPresetsRejectBadDefinitionsAndPreserveCorruptState(t *testing.T) {
	s, _ := newService(t)
	ctx := UIContext(context.Background())
	q := ViewPresetsRequest{Provider: "kubernetes", Kind: "pods"}
	good := ViewPresetRegistry{Version: 1, Active: "x", Presets: []ViewPreset{{Name: "x", Labels: []string{"x:y/z.a"}, Hidden: []string{}, Order: []string{}, Filter: ""}}}
	for _, mutate := range []func(*ViewPresetRegistry){
		func(r *ViewPresetRegistry) { r.Version = 2 }, func(r *ViewPresetRegistry) { r.Active = "missing" }, func(r *ViewPresetRegistry) { r.Presets = append(r.Presets, r.Presets[0]) },
		func(r *ViewPresetRegistry) { r.Presets[0].Labels = []string{"a", "a"} }, func(r *ViewPresetRegistry) { r.Presets[0].Hidden = []string{"name"} }, func(r *ViewPresetRegistry) { r.Presets[0].Filter = strings.Repeat("é", 257) },
		func(r *ViewPresetRegistry) { r.Presets[0].Order = []string{"a", "a"} }, func(r *ViewPresetRegistry) { r.Presets[0].Labels = nil }, func(r *ViewPresetRegistry) { r.Presets[0].Name = "\x00" },
	} {
		b, _ := json.Marshal(good)
		var r ViewPresetRegistry
		require.NoError(t, json.Unmarshal(b, &r))
		mutate(&r)
		q.Registry = &r
		_, err := s.ViewPresets(ctx, q)
		require.Error(t, err)
	}
	for _, size := range []int{65, 64} {
		b, _ := json.Marshal(good)
		var r ViewPresetRegistry
		require.NoError(t, json.Unmarshal(b, &r))
		r.Presets[0].Order = []string{}
		for i := range size {
			r.Presets[0].Order = append(r.Presets[0].Order, strings.Repeat("x", 500)+fmt.Sprint(i))
		}
		q.Registry = &r
		_, err := s.ViewPresets(ctx, q)
		require.Error(t, err, "count and aggregate byte caps are independent")
	}
	key := `view_presets.["kubernetes","pods"]`
	require.NoError(t, s.store.SetUIPref(ctx, key, `{"version":1,"presets":null}`))
	q.Registry = &good
	_, err := s.ViewPresets(ctx, q)
	require.Error(t, err)
	raw, err := s.store.GetUIPref(ctx, key)
	require.NoError(t, err)
	require.Equal(t, `{"version":1,"presets":null}`, raw)
}
