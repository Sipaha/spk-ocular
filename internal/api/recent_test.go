package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

func TestRecentObjectsRoundTripNewestFirst(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t, &fakeProvider{id: "k", targets: []string{"a", "b"}})
	now := time.UnixMilli(1000)
	s.now = func() time.Time { return now }
	web := core.Ref{Provider: "k", Target: "a", Scope: "demo", Kind: "pods", Name: "web", UID: "u1"}
	require.NoError(t, s.TouchRecent(ctx, TouchRecentRequest{Ref: web, Title: "web"}))
	now = now.Add(time.Second)
	db := core.Ref{Provider: "k", Target: "a", Kind: "nodes", Name: "n1", UID: "u2"}
	require.NoError(t, s.TouchRecent(ctx, TouchRecentRequest{Ref: db, Title: "n1"}))

	got, err := s.RecentObjects(ctx, "k", "a")
	require.NoError(t, err)
	assert.Equal(t, []RecentObject{{Ref: db, Title: "n1", OpenedAt: 2000}, {Ref: web, Title: "web", OpenedAt: 1000}}, got)
	got, err = s.RecentObjects(ctx, "k", "b")
	require.NoError(t, err)
	assert.NotNil(t, got, "an empty list, not null, for the UI")
	assert.Empty(t, got)
}

func TestTouchRecentRejectsIncompleteOrOversizedEntries(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t, &fakeProvider{id: "k", targets: []string{"a"}})
	ok := core.Ref{Provider: "k", Target: "a", Kind: "pods", Name: "web"}
	for name, req := range map[string]TouchRecentRequest{
		"no kind":          {Ref: core.Ref{Provider: "k", Target: "a", Name: "web"}},
		"no name":          {Ref: core.Ref{Provider: "k", Target: "a", Kind: "pods"}},
		"no target":        {Ref: core.Ref{Provider: "k", Kind: "pods", Name: "web"}},
		"unknown provider": {Ref: core.Ref{Provider: "zzz", Target: "a", Kind: "pods", Name: "web"}},
		"long name":        {Ref: core.Ref{Provider: "k", Target: "a", Kind: "pods", Name: strings.Repeat("x", 2000)}},
		"long title":       {Ref: ok, Title: strings.Repeat("x", 2000)},
	} {
		assert.True(t, IsCoded(s.TouchRecent(ctx, req), CodeBadRequest), name)
	}
	got, _ := s.RecentObjects(ctx, "k", "a")
	assert.Empty(t, got)
}

type aliasedProvider struct{ fakeProvider }

func (a *aliasedProvider) CommandAliases() provider.CommandAliases {
	return provider.CommandAliases{Scope: []string{"ns"}, Target: []string{"ctx"}}
}

func TestTargetGroupsCarryTheProvidersCommandAliases(t *testing.T) {
	s, _ := newService(t, &aliasedProvider{fakeProvider{id: "k"}}, &fakeProvider{id: "plain"})
	v, err := s.ListTargets(context.Background())
	require.NoError(t, err)
	assert.Equal(t, provider.CommandAliases{Scope: []string{"ns"}, Target: []string{"ctx"}}, v.Groups[0].Aliases)
	assert.Equal(t, provider.CommandAliases{}, v.Groups[1].Aliases)
}
