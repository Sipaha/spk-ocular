package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func rec(target, name, uid string, at int) Recent {
	return Recent{Provider: "k", Target: target, Kind: "pods", Scope: "demo", Name: name, UID: uid, Title: name, OpenedAt: time.UnixMilli(int64(at) * 1000)}
}

func names(rs []Recent) []string {
	out := []string{}
	for _, r := range rs {
		out = append(out, r.Name+"@"+r.UID)
	}
	return out
}

func TestRecentsNewestFirstPerTarget(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "db"))
	require.NoError(t, s.TouchRecent(ctx, rec("a", "web", "1", 1), 50, 500))
	require.NoError(t, s.TouchRecent(ctx, rec("a", "db", "2", 2), 50, 500))
	require.NoError(t, s.TouchRecent(ctx, rec("b", "other", "3", 3), 50, 500))
	require.NoError(t, s.TouchRecent(ctx, rec("a", "web", "1", 4), 50, 500)) // opened again: moves up, no duplicate
	got, err := s.RecentObjects(ctx, "k", "a")
	require.NoError(t, err)
	assert.Equal(t, []string{"web@1", "db@2"}, names(got))
	assert.Equal(t, int64(4000), got[0].OpenedAt.UnixMilli())
	got, _ = s.RecentObjects(ctx, "compose", "a")
	assert.Empty(t, got)
}

func TestRecentIdentityIncludesUID(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "db"))
	require.NoError(t, s.TouchRecent(ctx, rec("a", "web", "old", 1), 50, 500))
	require.NoError(t, s.TouchRecent(ctx, rec("a", "web", "new", 2), 50, 500)) // replaced under the same name
	got, _ := s.RecentObjects(ctx, "k", "a")
	assert.Equal(t, []string{"web@new", "web@old"}, names(got))
}

func TestRecentsAreBoundedPerTargetAndInTotal(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "db"))
	for i := range 5 {
		require.NoError(t, s.TouchRecent(ctx, rec("a", fmt.Sprint("a", i), "u", i), 3, 5))
	}
	got, _ := s.RecentObjects(ctx, "k", "a")
	assert.Equal(t, []string{"a4@u", "a3@u", "a2@u"}, names(got), "the oldest of a target go first")
	for i := range 3 {
		require.NoError(t, s.TouchRecent(ctx, rec("b", fmt.Sprint("b", i), "u", 10+i), 3, 5))
	}
	a, _ := s.RecentObjects(ctx, "k", "a")
	b, _ := s.RecentObjects(ctx, "k", "b")
	assert.Equal(t, []string{"a4@u", "a3@u"}, names(a), "over the total the oldest of any target go")
	assert.Equal(t, []string{"b2@u", "b1@u", "b0@u"}, names(b))
}
