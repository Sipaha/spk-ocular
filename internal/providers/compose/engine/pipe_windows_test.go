package engine

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/spk/spk-ocular/internal/paths"
	"github.com/spk/spk-ocular/internal/privatefs"
	"github.com/stretchr/testify/require"
)

func TestWindowsNamedPipeTransport(t *testing.T) {
	path := paths.AgentEndpoint(t.TempDir())
	sddl, err := privatefs.OwnerDescriptor(false)
	require.NoError(t, err)
	ln, err := winio.ListenPipe(path, &winio.PipeConfig{SecurityDescriptor: sddl})
	require.NoError(t, err)
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("pipe transport")) })}
	go func() { _ = server.Serve(ln) }()
	defer func() { _ = server.Close() }()
	client, err := New(Config{Host: "npipe:////./pipe/" + path[len(`\\.\pipe\`):]})
	require.NoError(t, err)
	defer client.Close()
	request, err := http.NewRequestWithContext(context.Background(), "GET", "http://docker/test", nil)
	require.NoError(t, err)
	response, err := client.hc.Do(request)
	require.NoError(t, err)
	_ = response.Body.Close()
	require.Equal(t, 200, response.StatusCode)
	for _, bad := range []string{"npipe:////remote/pipe/docker_engine", "npipe:////./pipe/", "npipe:////./pipe/a/b"} {
		_, err = New(Config{Host: bad})
		require.Error(t, err)
	}
}
