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

type historyTransportAPI struct {
	api.API
	request core.HistoryRequest
	calls   int
}

func (a *historyTransportAPI) ResourceMetricsHistory(_ context.Context, r core.HistoryRequest) (core.MetricsHistory, error) {
	a.request = r
	a.calls++
	return core.MetricsHistory{Ref: r.Ref}, nil
}
func TestResourceMetricsHistoryHTTPRequiresAuthenticationOriginAndNoStore(t *testing.T) {
	a := &historyTransportAPI{}
	h := NewHTTP(a, events.NewEmitter())
	for _, entry := range []struct {
		auth   bool
		origin string
		status int
	}{{false, "http://127.0.0.1", 401}, {true, "https://foreign.invalid", 403}, {true, "http://127.0.0.1", 200}} {
		r := httptest.NewRequest("POST", "http://127.0.0.1/api/ResourceMetricsHistory", strings.NewReader(`{"ref":{"provider":"kubernetes","kind":"pods"},"source":{"url":"https://source.invalid","token":"PRIVATE"},"window":"1h"}`))
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
	require.Equal(t, "pods", a.request.Ref.Kind)
}
