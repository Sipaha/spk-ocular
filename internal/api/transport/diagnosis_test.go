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

type diagnosisTransportAPI struct {
	api.API
	called  bool
	request core.Ref
}

func (a *diagnosisTransportAPI) DiagnoseResource(_ context.Context, r core.Ref) (core.Diagnosis, error) {
	a.called = true
	a.request = r
	return core.Diagnosis{Ref: r}, nil
}
func TestDiagnosisHTTPRouteAuthenticationOriginAndRequest(t *testing.T) {
	a := &diagnosisTransportAPI{}
	h := NewHTTP(a, events.NewEmitter())
	body := `{"provider":"kubernetes","target":"cluster","scope":"blue","kind":"pods","name":"p","uid":"uid"}`
	for _, auth := range []bool{false, true} {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/DiagnoseResource", strings.NewReader(body))
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
			require.Equal(t, "blue", a.request.Scope)
			require.Equal(t, "uid", a.request.UID)
		} else {
			require.Equal(t, 401, w.Code)
			require.False(t, a.called)
		}
	}
	a.called = false
	forged := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/DiagnoseResource", strings.NewReader(body))
	forged.Header.Set("Authorization", "Bearer "+h.AuthToken())
	forged.Header.Set("Origin", "https://foreign.invalid")
	denied := httptest.NewRecorder()
	h.ServeHTTP(denied, forged)
	require.Equal(t, http.StatusForbidden, denied.Code)
	require.False(t, a.called)

}
