package kubernetes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/helm"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"
)

func TestClosingSessionCancelsHelmNetwork(t *testing.T) {
	reached := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { once.Do(func() { close(reached) }); <-r.Context().Done() }))
	defer server.Close()
	s, err := sessionFor(&rest.Config{Host: server.URL}, "fixture", "fixture", "hash")
	require.NoError(t, err)
	defer s.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	a, err := s.HelmConfiguration(ctx, "blue", helm.Storage{Driver: "secret"})
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- a.KubeClient.IsReachable() }()
	select {
	case <-reached:
	case <-ctx.Done():
		t.Fatal("Helm did not reach the disposable API server")
	}
	s.Close()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("closing the connection did not cancel Helm")
	}
}

func TestHelmResourceReferencesKeepActualScope(t *testing.T) {
	s, err := sessionFor(&rest.Config{Host: "https://unused.invalid"}, "fixture", "fixture", "hash")
	require.NoError(t, err)
	defer s.Close()
	ref := s.HelmResourceRef(helm.Resource{APIVersion: "v1", Kind: "ConfigMap", Name: "config"}, "blue")
	require.NotNil(t, ref)
	require.Equal(t, "blue", ref.Scope)
	require.Equal(t, "configmaps", ref.Kind)
	require.Empty(t, ref.UID)
	ref = s.HelmResourceRef(helm.Resource{APIVersion: "v1", Kind: "ConfigMap", Name: "config", Namespace: "green"}, "blue")
	require.Equal(t, "green", ref.Scope)
	ref = s.HelmResourceRef(helm.Resource{APIVersion: "v1", Kind: "Namespace", Name: "blue"}, "blue")
	require.NotNil(t, ref)
	require.Empty(t, ref.Scope)
	require.Nil(t, s.HelmResourceRef(helm.Resource{APIVersion: "unknown.test/v1", Kind: "Unknown", Name: "unknown"}, "blue"))
}
