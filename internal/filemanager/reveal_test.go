package filemanager

import (
	"context"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPlatformSelectionArguments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a name;$(no),#?.yaml")
	for _, tt := range []struct {
		os, name string
		args     []string
	}{{"darwin", "open", []string{"-R", path}}, {"windows", "explorer.exe", []string{"/select," + path}}, {"unknown", "", nil}} {
		name, args := Command(tt.os, path)
		require.Equal(t, tt.name, name)
		require.Equal(t, tt.args, args)
	}
	require.Error(t, Reveal(context.Background(), "relative"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, Reveal(ctx, path), context.Canceled)
}

func TestSelectionProcessFixture(t *testing.T) {
	if os.Getenv("OCULAR_SELECTION_FIXTURE") != "1" {
		t.Skip("owned subprocess fixture")
	}
	path := os.Getenv("OCULAR_SELECTION_PATH")
	if err := os.WriteFile(path+".started", nil, 0600); err != nil {
		os.Exit(1)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path + ".release"); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := os.WriteFile(path+".stopped", nil, 0600); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
func TestSelectionDispatchDoesNotKillFileManagerAfterRequestEnds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manager")
	t.Setenv("OCULAR_SELECTION_FIXTURE", "1")
	t.Setenv("OCULAR_SELECTION_PATH", path)
	t.Cleanup(func() { _ = os.WriteFile(path+".release", nil, 0600) })
	binary, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	require.NoError(t, dispatchSelection(ctx, binary, []string{"-test.run=^TestSelectionProcessFixture$"}))
	exists := func(suffix string) bool { _, err := os.Stat(path + suffix); return err == nil }
	require.Eventually(t, func() bool { return exists(".started") }, time.Second, 10*time.Millisecond)
	cancel()
	require.False(t, exists(".stopped"))
	require.NoError(t, os.WriteFile(path+".release", nil, 0600))
	require.Eventually(t, func() bool { return exists(".stopped") }, time.Second, 10*time.Millisecond)
	require.ErrorIs(t, dispatchSelection(ctx, binary, nil), context.Canceled)
}
