package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/stretchr/testify/require"
)

type krrTransportAPI struct {
	api.API
	calls int
}

func (a *krrTransportAPI) KRR(_ context.Context, _ api.KRRRequest) (api.KRRResponse, error) {
	a.calls++
	return api.KRRResponse{Reports: nil}, nil
}
func TestKRRLocalHTTPAdmission(t *testing.T) {
	a := &krrTransportAPI{}
	h := NewHTTP(a, events.NewEmitter())
	for _, entry := range []struct {
		auth   bool
		origin string
		size   int
		status int
	}{{false, "http://127.0.0.1", 0, http.StatusUnauthorized}, {true, "https://foreign.invalid", 0, http.StatusForbidden}, {true, "http://127.0.0.1", 0, http.StatusOK}, {true, "http://127.0.0.1", (2 << 20) + 1, http.StatusBadRequest}} {
		body := `{"command":"status"}`
		if entry.size > 0 {
			body = strings.Repeat(" ", entry.size)
		}
		r := httptest.NewRequest("POST", "http://127.0.0.1/api/KRR", strings.NewReader(body))
		r.Header.Set("Origin", entry.origin)
		if entry.auth {
			r.Header.Set("Authorization", "Bearer "+h.AuthToken())
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		require.Equal(t, entry.status, w.Code)
		if entry.status == http.StatusOK || entry.status == http.StatusBadRequest {
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		}
	}
	require.Equal(t, 1, a.calls)
}
