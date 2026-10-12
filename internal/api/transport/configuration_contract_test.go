package transport

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/configuration"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/stretchr/testify/require"
)

type configurationAPI struct {
	api.API
	request configuration.ConfigRequest
}

func (f *configurationAPI) Configurations(_ context.Context, r configuration.ConfigRequest) (configuration.ConfigState, error) {
	f.request = r
	return configuration.ConfigState{YAML: "fixture inspected YAML", ContentRevision: "revision", Candidates: []configuration.ConfigCandidate{}, Entries: []configuration.ConfigEntry{}}, nil
}
func TestConfigurationContractHTTPPreservesDTOAndNoStore(t *testing.T) {
	f := &configurationAPI{}
	h := NewHTTP(f, events.NewEmitter())
	server := httptest.NewServer(h)
	defer server.Close()
	response := call(t, h, server.URL, "Configurations", `{"command":"inspect","id":"entry","expect":"revision","contentRevision":"content","targetRevision":"target","target":"fixture","paths":["fixture.yaml"],"name":"fixture","yaml":"fixture input","password":"fixture password"}`)
	defer response.Body.Close()
	require.Equal(t, 200, response.StatusCode)
	require.Equal(t, "no-store", response.Header.Get("Cache-Control"))
	require.Equal(t, configuration.ConfigRequest{Command: "inspect", ID: "entry", Expect: "revision", ContentRevision: "content", TargetRevision: "target", Target: "fixture", Paths: []string{"fixture.yaml"}, Name: "fixture", YAML: "fixture input", Password: "fixture password"}, f.request)
	var out configuration.ConfigState
	require.NoError(t, json.NewDecoder(response.Body).Decode(&out))
	require.Equal(t, "fixture inspected YAML", out.YAML)
	require.Equal(t, "revision", out.ContentRevision)
}
