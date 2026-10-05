package kubernetes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

func TestConnectionRejectsUntrustedCertificateWithoutRetryClass(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("untrusted server must not receive a request") }))
	defer server.Close()
	client, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
	require.NoError(t, err)
	s := newSession("tls", "h", client, false)
	defer s.Close()
	err = s.CheckConnection(context.Background())
	var failure *provider.Error
	require.ErrorAs(t, err, &failure)
	require.Equal(t, provider.ClassInvalid, failure.Class)
}
