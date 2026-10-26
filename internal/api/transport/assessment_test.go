package transport

import (
	"context"
	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"strings"
	"testing"
)

type assessmentTransportAPI struct {
	api.API
	request core.NamespaceAssessmentRequest
	calls   int
}

func (a *assessmentTransportAPI) AssessNamespace(_ context.Context, r core.NamespaceAssessmentRequest) (core.NamespaceAssessment, error) {
	a.request = r
	a.calls++
	return core.NamespaceAssessment{Namespace: r.Namespace}, nil
}
func TestAssessNamespaceHTTPRequiresAuthenticationOriginAndNoStore(t *testing.T) {
	a := &assessmentTransportAPI{}
	h := NewHTTP(a, events.NewEmitter())
	for _, entry := range []struct {
		auth   bool
		origin string
		status int
	}{{false, "http://127.0.0.1", 401}, {true, "https://foreign.invalid", 403}, {true, "http://127.0.0.1", 200}} {
		r := httptest.NewRequest("POST", "http://127.0.0.1/api/AssessNamespace", strings.NewReader(`{"provider":"kubernetes","namespace":"blue","check":"configuration"}`))
		r.Header.Set("Origin", entry.origin)
		if entry.auth {
			r.Header.Set("Authorization", "Bearer "+h.AuthToken())
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		require.Equal(t, entry.status, w.Code)
		if entry.status == 200 {
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		}
	}
	require.Equal(t, 1, a.calls)
	require.Equal(t, "blue", a.request.Namespace)
}
