package transport

import (
	"context"
	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type capacityTransportAPI struct {
	api.API
	calls int
}

func (a *capacityTransportAPI) NodeCapacity(_ context.Context, req core.NodeCapacityRequest) (core.NodeCapacity, error) {
	a.calls++
	return core.NodeCapacity{Ref: req.Ref, Namespace: req.Namespace}, nil
}
func TestNodeCapacityLocalHTTPAdmission(t *testing.T) {
	a := &capacityTransportAPI{}
	h := NewHTTP(a, events.NewEmitter())
	for _, test := range []struct {
		auth   bool
		origin string
		status int
	}{{false, "http://127.0.0.1", 401}, {true, "https://foreign.invalid", 403}, {true, "http://127.0.0.1", 200}} {
		req := httptest.NewRequest("POST", "http://127.0.0.1/api/NodeCapacity", strings.NewReader(`{"ref":{"kind":"nodes"},"namespace":"blue"}`))
		req.Header.Set("Origin", test.origin)
		if test.auth {
			req.Header.Set("Authorization", "Bearer "+h.AuthToken())
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		require.Equal(t, test.status, w.Code)
		if test.status == http.StatusOK {
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		}
	}
	require.Equal(t, 1, a.calls)
}
