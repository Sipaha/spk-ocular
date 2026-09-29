package streams

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSafeFileName(t *testing.T) {
	for in, want := range map[string]string{
		"web-1_20260929.log":   "web-1_20260929.log",
		"../../etc/passwd":     "passwd",
		`..\..\x.log`:          "x.log",
		".hidden":              "hidden",
		"":                     "logs.log",
		"..":                   "logs.log",
		"a\x00b\nc:d*e?.log":   "a_b_c_d_e_.log",
		"deploy/web 12:00.log": "web 12_00.log",
	} {
		assert.Equal(t, want, safeFileName(in), "%q", in)
	}
}

func TestWriteUniqueNeverReplaces(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Downloads") // created on demand
	p1, err := writeUnique(dir, "x.log", []byte("one"))
	require.NoError(t, err)
	p2, err := writeUnique(dir, "x.log", []byte("two"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "x.log"), p1)
	assert.Equal(t, filepath.Join(dir, "x (1).log"), p2)
	b, _ := os.ReadFile(p1)
	assert.Equal(t, "one", string(b))
	st, err := os.Stat(p2)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), st.Mode().Perm(), "a log may carry secrets")
}
