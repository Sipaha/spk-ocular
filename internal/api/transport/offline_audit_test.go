package transport

import (
	"context"
	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type auditTransportAPI struct {
	api.API
	calls int
}

func (a *auditTransportAPI) OfflineAudit(_ context.Context, _ api.OfflineAuditRequest) (api.OfflineAuditResponse, error) {
	a.calls++
	return api.OfflineAuditResponse{}, nil
}
func TestOfflineAuditTransportAuthenticationOriginBodyAndNoStore(t *testing.T) {
	a := &auditTransportAPI{}
	h := NewHTTP(a, events.NewEmitter())
	for _, entry := range []struct {
		auth         bool
		origin, body string
		status       int
	}{
		{false, "http://127.0.0.1", `{"command":"status"}`, http.StatusUnauthorized},
		{true, "https://foreign.invalid", `{"command":"status"}`, http.StatusForbidden},
		{true, "http://127.0.0.1", `{"command":"status"}`, http.StatusOK},
		{true, "http://127.0.0.1", strings.Repeat(" ", (2<<20)+1), http.StatusBadRequest},
		{true, "http://127.0.0.1", `{"command":"status"} {}`, http.StatusBadRequest},
	} {
		r := httptest.NewRequest("POST", "http://127.0.0.1/api/OfflineAudit", strings.NewReader(entry.body))
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
