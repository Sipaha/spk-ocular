package helm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func chartArchive(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for name, body := range map[string]string{"Chart.yaml": "apiVersion: v2\nname: demo\nversion: 1.0.0\nappVersion: '2'\n", "values.yaml": "# keep this comment\ngreeting: hello\n", "README.md": "# Demo chart"} {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: "demo/" + name, Mode: 0600, Size: int64(len(body))}))
		_, err := tw.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return b.Bytes()
}
func TestRepositorySettingsAndPinnedArchive(t *testing.T) {
	archive := chartArchive(t)
	digest := sha256.Sum256(archive)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "reader" || p != "private" {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/index.yaml" {
			_, _ = fmt.Fprintf(w, "apiVersion: v1\nentries:\n  demo:\n    - name: demo\n      version: 1.0.0\n      appVersion: '2'\n      urls: [demo.tgz]\n      digest: %s\n", hex.EncodeToString(digest[:]))
		} else {
			_, _ = w.Write(archive)
		}
	}))
	defer server.Close()
	m := NewRepositories(t.TempDir())
	settings := Settings{Repositories: []Repository{{Name: "local", URL: server.URL, Username: "reader", Password: "private"}}, Storage: Storage{Driver: "secret"}}
	require.NoError(t, m.Save(settings))
	visible, err := m.Settings()
	require.NoError(t, err)
	require.Empty(t, visible.Repositories[0].Password)
	require.True(t, visible.Repositories[0].HasPassword)
	require.NoError(t, m.Save(visible))
	rows, err := m.Catalog(t.Context(), "local")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	ch, view, err := m.Load(t.Context(), rows[0].ChartRef)
	require.NoError(t, err)
	require.NotNil(t, ch)
	require.Contains(t, view.Values, "# keep this comment")
	require.Equal(t, "# Demo chart", view.Readme)
	wrong := rows[0].ChartRef
	wrong.Version = "^1"
	_, _, err = m.Load(t.Context(), wrong)
	require.Error(t, err)
	info, err := os.Stat(filepath.Join(m.dir, "settings.json"))
	require.NoError(t, err)
	require.Zero(t, info.Mode().Perm()&0077)
}
func TestRepositoryRedirectDoesNotLeakCredentials(t *testing.T) {
	received := ""
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("ok"))
	}))
	defer other.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, http.StatusFound) }))
	defer source.Close()
	_, err := fetch(t.Context(), Repository{URL: source.URL, Username: "private", Password: "secret"}, source.URL)
	require.NoError(t, err)
	require.Empty(t, received)
}
func TestRepositorySettingsValidationAndSQLRedaction(t *testing.T) {
	m := NewRepositories(t.TempDir())
	require.Error(t, m.Save(Settings{Storage: Storage{Driver: "other"}}))
	require.Error(t, m.Save(Settings{Repositories: []Repository{{Name: "bad", URL: "https://user:password@example.com"}}, Storage: Storage{Driver: "secret"}}))
	require.NoError(t, m.Save(Settings{Repositories: []Repository{}, Storage: Storage{Driver: "sql", SQLConnection: "postgres://secret"}}))
	s, err := m.Settings()
	require.NoError(t, err)
	require.True(t, s.HasSQLConnection)
	require.Empty(t, s.Storage.SQLConnection)
	require.NoError(t, m.Save(s))
	stored, err := m.Storage()
	require.NoError(t, err)
	require.Equal(t, "postgres://secret", stored.SQLConnection)
}
