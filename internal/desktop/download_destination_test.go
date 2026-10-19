package desktop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUniqueLogDownloadKeepsExistingFiles(t *testing.T) {
	directory := t.TempDir()
	name := filepath.Join(directory, "app.log")
	require.NoError(t, os.WriteFile(name, []byte("existing"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "app (1).log"), []byte("other"), 0600))
	staged, err := newStagedLogExport(name, 1024)
	require.NoError(t, err)
	defer staged.Abort()
	staged.unique = true
	require.NoError(t, staged.Write("new"))
	require.NoError(t, staged.Complete())
	require.Equal(t, filepath.Join(directory, "app (2).log"), staged.destination)
	for file, expected := range map[string]string{"app.log": "existing", "app (1).log": "other", "app (2).log": "new"} {
		actual, err := os.ReadFile(filepath.Join(directory, file))
		require.NoError(t, err)
		require.Equal(t, expected, string(actual))
	}
}

func TestDownloadCancellationRunsOwnedCallbackOutsideLock(t *testing.T) {
	history := newDownloadHistory("")
	id := history.begin("app.log", "logs")
	called := 0
	history.registerCancel(id, func() { called++; history.finish(id, "", "cancelled") })
	history.cancel("unknown")
	history.cancel(id)
	history.cancel(id)
	require.Equal(t, 1, called)
	require.Equal(t, "cancelled", history.snapshot().Records[0].Status)
}
