package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/stretchr/testify/require"
)

type fluxTransportAPI struct {
	api.API
	called  bool
	request api.FluxRequest
}

func (a *fluxTransportAPI) FluxCD(_ context.Context, r api.FluxRequest) (core.FluxSnapshot, error) {
	a.called = true
	a.request = r
	return core.FluxSnapshot{Objects: []core.FluxObject{}, Events: []core.ArgoEvent{}, Coverage: []core.ArgoCoverage{}, Discovery: "ready"}, nil
}
func TestFluxHTTPRouteAuthenticationOriginAndRequest(t *testing.T) {
	a := &fluxTransportAPI{}
	h := NewHTTP(a, events.NewEmitter())
	body := `{"provider":"kubernetes","target":"cluster","scope":{"mode":"some","names":["blue"]}}`
	for _, auth := range []bool{false, true} {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/FluxCD", strings.NewReader(body))
		r.Header.Set("Origin", "http://127.0.0.1")
		if auth {
			r.Header.Set("Authorization", "Bearer "+h.AuthToken())
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if auth {
			require.Equal(t, 200, w.Code)
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			require.True(t, a.called)
			require.Equal(t, []string{"blue"}, a.request.Scope.Names)
		} else {
			require.Equal(t, 401, w.Code)
			require.False(t, a.called)
		}
	}
	a.called = false
	forged := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/FluxCD", strings.NewReader(body))
	forged.Header.Set("Authorization", "Bearer "+h.AuthToken())
	forged.Header.Set("Origin", "https://foreign.invalid")
	denied := httptest.NewRecorder()
	h.ServeHTTP(denied, forged)
	require.Equal(t, http.StatusForbidden, denied.Code)
	require.False(t, a.called)

}
