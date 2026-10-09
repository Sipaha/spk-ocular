package desktop

import (
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLogExportWritesChosenFilePrivatelyAndReplacesAfterSuccessfulWrite(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "chosen logs.log")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0644))
	text := "INFO привет\nplain text\r\n"
	require.NoError(t, writeLogExport(path, text))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, text, string(got))
	stat, err := os.Stat(path)
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		require.Equal(t, os.FileMode(0600), stat.Mode().Perm())
	}
	files, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Error(t, writeLogExport(filepath.Join(directory, "missing", "file.log"), "x"))
	got, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, text, string(got))
}
