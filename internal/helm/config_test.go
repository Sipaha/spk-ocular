package helm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"
)

func TestTransportKeepsResponseAliveAndCancelsBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, "first\n")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c := &http.Client{Transport: requestTransport{http.DefaultTransport, ctx}}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL, strings.NewReader("{}"))
	require.NoError(t, err)
	res, err := c.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Empty(t, res.Header.Get("Retry-After"))
	buf := make([]byte, 6)
	_, err = io.ReadFull(res.Body, buf)
	require.NoError(t, err)
	require.Equal(t, "first\n", string(buf))
	cancel()
	_, err = io.ReadAll(res.Body)
	require.Error(t, err)
}
func TestGetterUsesSnapshotWithoutReadingKubeconfig(t *testing.T) {
	t.Setenv("KUBECONFIG", "/does-not-exist")
	original := &rest.Config{Host: "https://snapshot.invalid", BearerToken: "private"}
	g := Getter{Config: original, Namespace: "selected"}
	config, err := g.ToRawKubeConfigLoader().ClientConfig()
	require.NoError(t, err)
	require.Equal(t, "private", config.BearerToken)
	config.Host = "https://other.invalid"
	require.Equal(t, "https://snapshot.invalid", original.Host)
	ns, explicit, err := g.ToRawKubeConfigLoader().Namespace()
	require.NoError(t, err)
	require.True(t, explicit)
	require.Equal(t, "selected", ns)
}
