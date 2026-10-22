package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type setupAPI struct {
	fakeAPI
	calls int
}

func (a *setupAPI) AgentClientSetup(context.Context, api.AgentClientSetupRequest) (api.AgentClientSetupView, error) {
	a.calls++
	return api.AgentClientSetupView{}, nil
}
func TestAgentClientSetupHTTPAdmission(t *testing.T) {
	a := &setupAPI{}
	handler := NewHTTP(a, events.NewEmitter())
	server := httptest.NewServer(handler)
	defer server.Close()
	for _, tc := range []struct {
		name, auth, origin, body string
		status                   int
	}{
		{"missing auth", "", server.URL, `{}`, 401},
		{"bad origin", handler.AuthToken(), "https://foreign.example", `{}`, 403},
		{"unknown path input", handler.AuthToken(), server.URL, `{"client":"codex","install":true,"path":"/foreign"}`, 400},
		{"oversized", handler.AuthToken(), server.URL, `{"revision":"` + strings.Repeat("a", 4096) + `"}`, 400},
		{"trailing JSON", handler.AuthToken(), server.URL, `{} {}`, 400},
		{"valid inspect", handler.AuthToken(), server.URL, `{}`, 200},
		{"valid install", handler.AuthToken(), server.URL, `{"client":"codex","install":true,"revision":"prepared"}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, server.URL+"/api/AgentClientSetup", strings.NewReader(tc.body))
			require.NoError(t, err)
			if tc.auth != "" {
				req.Header.Set("Authorization", "Bearer "+tc.auth)
			}
			req.Header.Set("Origin", tc.origin)
			res, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer res.Body.Close()
			assert.Equal(t, tc.status, res.StatusCode)
			if res.StatusCode == 200 {
				assert.Equal(t, "no-store", res.Header.Get("Cache-Control"))
			}
		})
	}
	assert.Equal(t, 2, a.calls)
}
