//go:build !windows

package agentapi

import (
	"github.com/stretchr/testify/require"
	"os"
	"testing"
)

func assertPrivateExport(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
