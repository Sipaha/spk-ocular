package kubernetes

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// newUnreachable is a dynamic client for a port nothing listens on.
func newUnreachable(t *testing.T) (dynamic.Interface, string) {
	t.Helper()
	host := "https://127.0.0.1:1"
	c, err := dynamic.NewForConfig(&rest.Config{Host: host, TLSClientConfig: rest.TLSClientConfig{Insecure: true}})
	require.NoError(t, err)
	return c, host
}
