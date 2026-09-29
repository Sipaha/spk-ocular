package paths

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDownloadsResolution(t *testing.T) {
	home := t.TempDir()
	env := map[string]string{"HOME": home}
	getenv := func(k string) string { return env[k] }

	d, err := Downloads(getenv)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "Downloads"), d, "no configuration: ~/Downloads")

	require.NoError(t, os.MkdirAll(filepath.Join(home, ".config"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".config", "user-dirs.dirs"),
		[]byte("# comment\nXDG_DESKTOP_DIR=\"$HOME/Desktop\"\nXDG_DOWNLOAD_DIR=\"$HOME/Загрузки\"\n"), 0o600))
	d, err = Downloads(getenv)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "Загрузки"), d, "user-dirs.dirs")

	env["XDG_DOWNLOAD_DIR"] = "/srv/dl"
	d, err = Downloads(getenv)
	require.NoError(t, err)
	assert.Equal(t, "/srv/dl", d, "environment wins")

	env["XDG_DOWNLOAD_DIR"] = "$HOME/"
	require.NoError(t, os.WriteFile(filepath.Join(home, ".config", "user-dirs.dirs"),
		[]byte("XDG_DOWNLOAD_DIR=\"$HOME/\"\n"), 0o600))
	d, err = Downloads(getenv)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "Downloads"), d, "disabled (= home) falls back")
}
