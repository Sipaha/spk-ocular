package api

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFavoriteKindsAreGlobalPersistentAndIdempotent(t *testing.T) {
	s, _ := newService(t) // No provider or target needs to be opened.
	ctx := context.Background()
	items, err := s.GetFavoriteKinds(ctx)
	require.NoError(t, err)
	require.NotNil(t, items)
	require.Empty(t, items)
	req := KindFavoriteRequest{Provider: "kubernetes", Kind: "pods", Favorite: true}
	require.NoError(t, s.SetKindFavorite(ctx, req))
	require.NoError(t, s.SetKindFavorite(ctx, req))
	require.NoError(t, s.SetKindFavorite(ctx, KindFavoriteRequest{Provider: "compose", Kind: "pods", Favorite: true}))
	// A new service instance loads the same application preference. Provider
	// identity separates kinds, while connection identity never participates.
	other := NewService(s.reg, s.store, s.em, Options{})
	defer other.Close()
	items, err = other.GetFavoriteKinds(ctx)
	require.NoError(t, err)
	assert.Equal(t, []FavoriteKind{{Provider: "kubernetes", Kind: "pods"}, {Provider: "compose", Kind: "pods"}}, items)
	req.Favorite = false
	require.NoError(t, s.SetKindFavorite(ctx, req))
	require.NoError(t, s.SetKindFavorite(ctx, req))
	items, err = other.GetFavoriteKinds(ctx)
	require.NoError(t, err)
	assert.Equal(t, []FavoriteKind{{Provider: "compose", Kind: "pods"}}, items)
	require.Error(t, s.SetKindFavorite(ctx, KindFavoriteRequest{Favorite: true}))
}

func TestConcurrentFavoriteMembershipChangesDoNotLoseOtherKinds(t *testing.T) {
	s, _ := newService(t)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			assert.NoError(t, s.SetKindFavorite(context.Background(), KindFavoriteRequest{Provider: "kubernetes", Kind: fmt.Sprintf("custom.example/kind-%d", i), Favorite: true}))
		})
	}
	wg.Wait()
	items, err := s.GetFavoriteKinds(context.Background())
	require.NoError(t, err)
	assert.Len(t, items, 20)
}

func TestFavoriteOrderIsPersistentAndDoesNotMoveAnotherProvidersKinds(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	for _, item := range []FavoriteKind{{"kubernetes", "pods"}, {"compose", "pods"}, {"kubernetes", "services"}, {"kubernetes", "deployments"}} {
		require.NoError(t, s.SetKindFavorite(ctx, KindFavoriteRequest{Provider: item.Provider, Kind: item.Kind, Favorite: true}))
	}
	require.NoError(t, s.MoveFavoriteKind(ctx, MoveFavoriteKindRequest{Provider: "kubernetes", Kind: "deployments", Before: "pods"}))
	items, err := s.GetFavoriteKinds(ctx)
	require.NoError(t, err)
	assert.Equal(t, []FavoriteKind{{"kubernetes", "deployments"}, {"kubernetes", "pods"}, {"compose", "pods"}, {"kubernetes", "services"}}, items)
	require.NoError(t, s.MoveFavoriteKind(ctx, MoveFavoriteKindRequest{Provider: "kubernetes", Kind: "deployments"}))
	other := NewService(s.reg, s.store, s.em, Options{})
	defer other.Close()
	items, err = other.GetFavoriteKinds(ctx)
	require.NoError(t, err)
	assert.Equal(t, []FavoriteKind{{"kubernetes", "pods"}, {"compose", "pods"}, {"kubernetes", "services"}, {"kubernetes", "deployments"}}, items)
	require.Error(t, s.MoveFavoriteKind(ctx, MoveFavoriteKindRequest{Provider: "kubernetes", Kind: "pods", Before: "missing"}))
	unchanged, err := s.GetFavoriteKinds(ctx)
	require.NoError(t, err)
	assert.Equal(t, items, unchanged)
}
