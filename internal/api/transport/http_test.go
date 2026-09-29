package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/events"
)

type fakeAPI struct {
	api.API  // unimplemented methods panic: tests call only what they set up
	selected api.TargetRef
}

func (f *fakeAPI) ListTargets(context.Context) (api.TargetsView, error) {
	return api.TargetsView{Groups: []api.TargetGroup{{Provider: "kubernetes", Title: "Kubernetes"}}}, nil
}

func (f *fakeAPI) SelectTarget(_ context.Context, provider, id string) error {
	if id == "missing" {
		return &api.CodedError{Code: api.CodeNotFound, Detail: "no target"}
	}
	f.selected = api.TargetRef{Provider: provider, ID: id}
	return nil
}

func call(t *testing.T, h *HTTP, srvURL, method, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, srvURL+"/api/"+method, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+h.AuthToken())
	req.Header.Set("Origin", srvURL)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestPostRoutesToAPI(t *testing.T) {
	f := &fakeAPI{}
	h := NewHTTP(f, events.NewEmitter())
	ts := httptest.NewServer(h)
	defer ts.Close()

	resp := call(t, h, ts.URL, "SelectTarget", `{"provider":"kubernetes","id":"prod"}`)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, api.TargetRef{Provider: "kubernetes", ID: "prod"}, f.selected)

	resp = call(t, h, ts.URL, "ListTargets", `{}`)
	var v api.TargetsView
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&v))
	assert.Equal(t, "Kubernetes", v.Groups[0].Title)
}

func TestCodedErrorBecomes400WithCode(t *testing.T) {
	h := NewHTTP(&fakeAPI{}, events.NewEmitter())
	ts := httptest.NewServer(h)
	defer ts.Close()
	resp := call(t, h, ts.URL, "SelectTarget", `{"provider":"kubernetes","id":"missing"}`)
	assert.Equal(t, 400, resp.StatusCode)
	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, api.CodeNotFound, body["code"])
}

func TestMalformedBodyIsBadRequest(t *testing.T) {
	h := NewHTTP(&fakeAPI{}, events.NewEmitter())
	ts := httptest.NewServer(h)
	defer ts.Close()
	resp := call(t, h, ts.URL, "SelectTarget", `{not json`)
	assert.Equal(t, 400, resp.StatusCode)
	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, api.CodeBadRequest, body["code"])
}

func TestMissingOrWrongTokenIs401(t *testing.T) {
	h := NewHTTP(&fakeAPI{}, events.NewEmitter())
	ts := httptest.NewServer(h)
	defer ts.Close()
	for _, auth := range []string{"", "Bearer wrong-token"} {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/ListTargets", strings.NewReader(`{}`))
		req.Header.Set("Origin", ts.URL)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, 401, resp.StatusCode, auth)
	}
}

func TestQueryTokenOnlyAcceptedOnEventsRoute(t *testing.T) {
	h := NewHTTP(&fakeAPI{}, events.NewEmitter())
	ts := httptest.NewServer(h)
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/ListTargets?token="+h.AuthToken(), strings.NewReader(`{}`))
	req.Header.Set("Origin", ts.URL)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, 401, resp.StatusCode)
}

func TestCrossOriginPostIsForbidden(t *testing.T) {
	h := NewHTTP(&fakeAPI{}, events.NewEmitter())
	ts := httptest.NewServer(h)
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/ListTargets", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+h.AuthToken())
	req.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, 403, resp.StatusCode)
}

func TestSSEDeliversEvents(t *testing.T) {
	em := events.NewEmitter()
	h := NewHTTP(&fakeAPI{}, em)
	ts := httptest.NewServer(h)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/events?token="+h.AuthToken(), nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	r := bufio.NewReader(resp.Body)
	line, err := r.ReadString('\n') // ": ok" preamble — subscription is live
	require.NoError(t, err)
	assert.Equal(t, ": ok\n", line)

	em.Emit(events.Event{Type: api.EventTargetsChanged, Payload: map[string]any{"provider": "kubernetes"}})
	for {
		line, err = r.ReadString('\n')
		require.NoError(t, err)
		if strings.HasPrefix(line, "data: ") {
			var ev events.Event
			require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev))
			assert.Equal(t, api.EventTargetsChanged, ev.Type)
			assert.Equal(t, "kubernetes", ev.Payload["provider"])
			return
		}
	}
}
