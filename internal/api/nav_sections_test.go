package api

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNavSectionsAreGlobalPersistentAndPreserveIndependentChanges(t *testing.T) {
	s, _ := newService(t) // No provider or target is opened.
	ctx := context.Background()
	sections, err := s.GetNavSections(ctx)
	require.NoError(t, err)
	require.Equal(t, map[string]bool{}, sections)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			assert.NoError(t, s.SetNavSection(ctx, NavSectionRequest{Key: fmt.Sprintf("group:%d", i), Open: i%2 == 0}))
		})
	}
	wg.Wait()
	require.NoError(t, s.SetNavSection(ctx, NavSectionRequest{Key: "favorites", Open: false}))
	other := NewService(s.reg, s.store, s.em, Options{})
	defer other.Close()
	sections, err = other.GetNavSections(ctx)
	require.NoError(t, err)
	require.Len(t, sections, 21)
	for i := range 20 {
		assert.Equal(t, i%2 == 0, sections[fmt.Sprintf("group:%d", i)])
	}
	assert.Contains(t, sections, "favorites")
	assert.False(t, sections["favorites"])
	require.Error(t, s.SetNavSection(ctx, NavSectionRequest{}))
	require.Error(t, s.SetNavSection(ctx, NavSectionRequest{Key: strings.Repeat("x", 1025)}))
}

func TestNavSectionsMigrateOnlyTheLastSelectedTargetsExpansionOnce(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	require.NoError(t, s.store.SetUIPref(ctx, prefSelectedTarget, `{"provider":"kubernetes","id":"prod"}`))
	require.NoError(t, s.store.SetTargetState(ctx, "kubernetes", "prod", "navOpen", `["group:API groups","API groups/ocular.dev"]`))
	require.NoError(t, s.store.SetTargetState(ctx, "kubernetes", "dev", "navOpen", `["group:other"]`))
	sections, err := s.GetNavSections(ctx)
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"group:API groups": true, "API groups/ocular.dev": true}, sections)
	require.NoError(t, s.SetNavSection(ctx, NavSectionRequest{Key: "group:API groups", Open: false}))
	require.NoError(t, s.store.SetUIPref(ctx, prefSelectedTarget, `{"provider":"kubernetes","id":"dev"}`))
	sections, err = s.GetNavSections(ctx)
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"group:API groups": false, "API groups/ocular.dev": true}, sections)
}

func TestNavSectionsReportUnreadableGlobalStateWithoutOverwritingIt(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	require.NoError(t, s.store.SetUIPref(ctx, prefNavSections, "broken"))
	_, err := s.GetNavSections(ctx)
	require.Error(t, err)
	require.Error(t, s.SetNavSection(ctx, NavSectionRequest{Key: "favorites", Open: false}))
	raw, err := s.store.GetUIPref(ctx, prefNavSections)
	require.NoError(t, err)
	assert.Equal(t, "broken", raw)
}
