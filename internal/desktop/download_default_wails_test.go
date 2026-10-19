//go:build wails

package desktop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultLogExportWithoutDialogAndCancellationFromDownloads(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "Downloads")
	t.Setenv("XDG_DOWNLOAD_DIR", directory)
	h := newDownloadHistory("")
	manager := &LogWindows{history: h}
	// No application or window exists: default saving must not invoke a dialog.
	id, err := manager.BeginLogExport("", "app.log", "Download logs", "Download", false)
	require.NoError(t, err)
	require.NoError(t, manager.AppendLogExport(id, "complete"))
	destination, err := manager.CompleteLogExport(id)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(directory, "app.log"), destination)
	id, err = manager.BeginLogExport("", "app.log", "Download logs", "Download", false)
	require.NoError(t, err)
	require.NoError(t, manager.AppendLogExport(id, "second"))
	destination, err = manager.CompleteLogExport(id)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(directory, "app (1).log"), destination)
	require.Equal(t, "app (1).log", h.snapshot().Records[0].Name)
	id, err = manager.BeginLogExport("", "app.log", "Download logs", "Download", false)
	require.NoError(t, err)
	require.NoError(t, manager.AppendLogExport(id, "partial"))
	record := h.snapshot().Records[0]
	(&Downloads{history: h}).Cancel(record.ID)
	require.Equal(t, "cancelled", h.snapshot().Records[0].Status)
	_, err = manager.CompleteLogExport(id)
	require.Error(t, err)
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	content, err := os.ReadFile(filepath.Join(directory, "app.log"))
	require.NoError(t, err)
	require.Equal(t, "complete", string(content))
}
