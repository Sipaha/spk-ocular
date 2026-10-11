package desktop

import (
	"fmt"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestStagedLogHistoryStreamsBeyondTheOldBufferedLimit(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "history.log")
	staged, err := newStagedLogExport(destination, 1<<30)
	require.NoError(t, err)
	defer staged.Abort()
	chunk := strings.Repeat("x", 1<<20)
	for range 100 {
		require.NoError(t, staged.Write(chunk))
	}
	_, err = os.Stat(destination)
	require.True(t, os.IsNotExist(err))
	require.NoError(t, staged.Complete())
	stat, err := os.Stat(destination)
	require.NoError(t, err)
	require.Equal(t, int64(100<<20), stat.Size())
	require.Error(t, staged.Write("late"))
}
func TestStagedLogHistoryAbortOrLimitPreservesTheExistingDestination(t *testing.T) {
	for _, limit := range []int64{1024, 3} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			directory := t.TempDir()
			destination := filepath.Join(directory, "existing.log")
			require.NoError(t, os.WriteFile(destination, []byte("original"), 0600))
			staged, err := newStagedLogExport(destination, limit)
			require.NoError(t, err)
			if limit == 3 {
				require.Error(t, staged.Write("too large"))
			} else {
				require.NoError(t, staged.Write("pending"))
				staged.Abort()
			}
			require.Error(t, staged.Complete())
			data, err := os.ReadFile(destination)
			require.NoError(t, err)
			require.Equal(t, "original", string(data))
			files, err := os.ReadDir(directory)
			require.NoError(t, err)
			require.Len(t, files, 1)
		})
	}
}
