package paths

import (
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
	"path/filepath"
	"testing"
)

func TestWindowsDownloadsUsesKnownFolderUnlessExplicitlyIsolated(t *testing.T) {
	dir, err := Downloads(func(string) string { return "" })
	require.NoError(t, err)
	expected, err := windows.KnownFolderPath(windows.FOLDERID_Downloads, windows.KF_FLAG_DEFAULT)
	require.NoError(t, err)
	require.Equal(t, expected, dir)
	isolated := filepath.Join(t.TempDir(), "Downloads")
	dir, err = Downloads(func(key string) string {
		if key == "XDG_DOWNLOAD_DIR" {
			return isolated
		}
		return ""
	})
	require.NoError(t, err)
	require.Equal(t, isolated, dir)
}
