package transport

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/stretchr/testify/require"
)

type compositionTransportAPI struct {
	api.API
	stream  api.ComposedLogStreamRequest
	history api.LogComposition
	calls   int
}

func (a *compositionTransportAPI) OpenComposedLogStream(_ context.Context, r api.ComposedLogStreamRequest) (api.LogStreamInfo, error) {
	a.stream = r
	a.calls++
	return api.LogStreamInfo{StreamID: "composed"}, nil
}
func (a *compositionTransportAPI) OpenComposedLogHistory(_ context.Context, c api.LogComposition) (api.LogStreamInfo, error) {
	a.history = c
	a.calls++
	return api.LogStreamInfo{StreamID: "archive"}, nil
}
func TestComposedLogsHTTPRequiresAuthenticationOriginNoStoreAndBoundedJSON(t *testing.T) {
	for _, method := range []string{"OpenComposedLogStream", "OpenComposedLogHistory"} {
		t.Run(method, func(t *testing.T) {
			a := &compositionTransportAPI{}
			h := NewHTTP(a, events.NewEmitter())
			c := `{"refs":[{"provider":"kubernetes","target":"one","kind":"pods","scope":"ns","name":"a","uid":"a"}],"configRev":"rev","connectionId":7}`
			body := c
			if method == "OpenComposedLogStream" {
				body = `{"composition":` + c + `,"query":{"channel":"*","tailLines":500}}`
			}
			for _, entry := range []struct {
				auth         bool
				origin, body string
				status       int
			}{{false, "http://127.0.0.1", body, 401}, {true, "https://foreign.invalid", body, 403}, {true, "http://127.0.0.1", body + `{}`, 400}, {true, "http://127.0.0.1", body, 200}} {
				r := httptest.NewRequest("POST", "http://127.0.0.1/api/"+method, strings.NewReader(entry.body))
				r.Header.Set("Origin", entry.origin)
				if entry.auth {
					r.Header.Set("Authorization", "Bearer "+h.AuthToken())
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				require.Equal(t, entry.status, w.Code, w.Body.String())
				if entry.status == 200 {
					require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
				}
			}
			require.Equal(t, 1, a.calls)
			if method == "OpenComposedLogStream" {
				require.Equal(t, "rev", a.stream.Composition.ConfigRev)
				require.Equal(t, "*", a.stream.Query.Channel)
			} else {
				require.Equal(t, uint64(7), a.history.ConnectionID)
			}
		})
	}
}
