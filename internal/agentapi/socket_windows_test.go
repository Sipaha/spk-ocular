package agentapi

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/spk/spk-ocular/internal/paths"
	"github.com/stretchr/testify/require"
)

func TestWindowsPipeOwnsProfileAndServesHTTP(t *testing.T) {
	path := paths.AgentEndpoint(t.TempDir())
	s := New(Options{Socket: path, Version: "test"})
	require.NoError(t, s.Start())
	defer s.Close()
	second, err := listen(path, "")
	if second != nil {
		second.close()
	}
	require.ErrorIs(t, err, ErrOtherInstance)
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return winio.DialPipeContext(ctx, path)
	}}}
	defer client.CloseIdleConnections()
	response, err := client.Get("http://ocular/v1")
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	require.NoError(t, err)
	require.Equal(t, 200, response.StatusCode)
	require.Contains(t, string(body), "SPK Ocular agent access")
	s.Close()
	next, err := listen(path, "")
	require.NoError(t, err)
	next.close()
}
