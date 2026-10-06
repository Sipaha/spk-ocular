package helm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOCICatalogAndPullWithPrivateCredentialsAndCA(t *testing.T) {
	data := chartArchive(t)
	config := []byte(`{"name":"demo","version":"1.0.0"}`)
	digest := func(b []byte) string { v := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(v[:]) }
	chartDigest, configDigest := digest(data), digest(config)
	manifest, err := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json", "config": map[string]any{"mediaType": "application/vnd.cncf.helm.config.v1+json", "digest": configDigest, "size": len(config)}, "layers": []any{map[string]any{"mediaType": "application/vnd.cncf.helm.chart.content.v1.tar+gzip", "digest": chartDigest, "size": len(data)}}})
	require.NoError(t, err)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "reader" || p != "private" {
			w.Header().Set("WWW-Authenticate", `Basic realm="fixture"`)
			w.WriteHeader(401)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/tags/list"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"name":"demo","tags":["1.0.0"]}`))
		case strings.Contains(r.URL.Path, "/manifests/"):
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			w.Header().Set("Docker-Content-Digest", digest(manifest))
			_, _ = w.Write(manifest)
		case strings.HasSuffix(r.URL.Path, chartDigest):
			_, _ = w.Write(data)
		case strings.HasSuffix(r.URL.Path, configDigest):
			_, _ = w.Write(config)
		case r.URL.Path == "/v2/":
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.pem")
	require.NoError(t, os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600))
	t.Setenv("DOCKER_CONFIG", dir)
	t.Setenv("HOME", dir)
	m := NewRepositories(filepath.Join(dir, "ocular-helm"))
	require.NoError(t, m.Save(Settings{Repositories: []Repository{{Name: "oci", URL: strings.Replace(server.URL, "https://", "oci://", 1) + "/demo", Username: "reader", Password: "private", CAFile: ca}}, Storage: Storage{Driver: "secret"}}))
	charts, err := m.Catalog(t.Context(), "oci")
	require.NoError(t, err)
	require.Len(t, charts, 1)
	raw, chart, err := m.Load(t.Context(), charts[0].ChartRef)
	require.NoError(t, err)
	require.NotNil(t, raw)
	require.Equal(t, "1.0.0", chart.Version)
	require.Contains(t, chart.Values, "greeting: hello")
}
