package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func open(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(context.Background(), path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenIsIdempotentAndOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "ocular.db")
	s := open(t, path)
	require.NoError(t, s.SetUIPref(context.Background(), "k", "v"))
	require.NoError(t, s.Close())

	s2 := open(t, path) // migrations already applied: must not fail or wipe data
	v, err := s2.GetUIPref(context.Background(), "k")
	require.NoError(t, err)
	assert.Equal(t, "v", v)

	st, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), st.Mode().Perm())
}

func TestUIPrefsRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "db"))
	v, err := s.GetUIPref(ctx, "missing")
	require.NoError(t, err)
	assert.Empty(t, v)
	require.NoError(t, s.SetUIPref(ctx, "a", "1"))
	require.NoError(t, s.SetUIPref(ctx, "a", "2"))
	v, _ = s.GetUIPref(ctx, "a")
	assert.Equal(t, "2", v)
	require.NoError(t, s.DeleteUIPref(ctx, "a"))
	require.NoError(t, s.DeleteUIPref(ctx, "a"))
	v, _ = s.GetUIPref(ctx, "a")
	assert.Empty(t, v)
}

func TestTargetStateIsKeyedByProviderAndTarget(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "db"))
	require.NoError(t, s.SetTargetState(ctx, "kubernetes", "prod", "scope", `"default"`))
	require.NoError(t, s.SetTargetState(ctx, "kubernetes", "dev", "scope", `"kube-system"`))
	require.NoError(t, s.SetTargetState(ctx, "kubernetes", "prod", "scope", `"web"`))
	v, err := s.GetTargetState(ctx, "kubernetes", "prod", "scope")
	require.NoError(t, err)
	assert.Equal(t, `"web"`, v)
	v, _ = s.GetTargetState(ctx, "kubernetes", "dev", "scope")
	assert.Equal(t, `"kube-system"`, v)
	v, _ = s.GetTargetState(ctx, "compose", "prod", "scope")
	assert.Empty(t, v)
}

func TestTargetStateLists(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "db"))
	require.NoError(t, s.SetTargetState(ctx, "k", "a", "kind", `"pods"`))
	require.NoError(t, s.SetTargetState(ctx, "k", "a", "scope", `{"mode":"all"}`))
	require.NoError(t, s.SetTargetState(ctx, "k", "b", "kind", `"nodes"`))
	m, err := s.TargetState(ctx, "k", "a")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"kind": `"pods"`, "scope": `{"mode":"all"}`}, m)
	m, _ = s.TargetState(ctx, "k", "zzz")
	assert.Empty(t, m)
}

func TestDeleteTargetStateKeyRemovesOneKeyAcrossTargets(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "db"))
	require.NoError(t, s.SetTargetState(ctx, "k", "a", "pageMemo", `{"v":1}`))
	require.NoError(t, s.SetTargetState(ctx, "k", "b", "pageMemo", `{"v":1}`))
	require.NoError(t, s.SetTargetState(ctx, "k", "a", "kind", `"pods"`))

	require.NoError(t, s.DeleteTargetStateKey(ctx, "pageMemo"))

	m, err := s.TargetState(ctx, "k", "a")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"kind": `"pods"`}, m, "other keys survive")
	m, _ = s.TargetState(ctx, "k", "b")
	assert.Empty(t, m)
	require.NoError(t, s.DeleteTargetStateKey(ctx, "pageMemo"), "nothing to remove is not an error")
}
