package transport

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/core"
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

// metricsAPI answers GetMetrics once the request's context ends.
type metricsAPI struct {
	fakeAPI
	rowIDs chan []string
	ended  chan error
}

func (m *metricsAPI) GetMetrics(ctx context.Context, req api.MetricsRequest) (api.MetricsView, error) {
	m.rowIDs <- req.RowIDs
	<-ctx.Done()
	m.ended <- ctx.Err()
	return api.MetricsView{}, ctx.Err()
}

// The page's rows reach GetMetrics, and the page abandoning the request
// ends its context (the provider stops waiting).
func TestGetMetricsTakesTheRowsAndEndsWithThePage(t *testing.T) {
	m := &metricsAPI{rowIDs: make(chan []string, 1), ended: make(chan error, 1)}
	h := NewHTTP(m, events.NewEmitter())
	ts := httptest.NewServer(h)
	defer ts.Close()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/api/GetMetrics", strings.NewReader(`{"viewId":"v1","rowIds":["a","b"]}`))
	req.Header.Set("Authorization", "Bearer "+h.AuthToken())
	req.Header.Set("Origin", ts.URL)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}()
	assert.Equal(t, []string{"a", "b"}, <-m.rowIDs)
	cancel()
	select {
	case err := <-m.ended:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("the server kept waiting for a page that left")
	}
	<-done
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

type valueAPI struct {
	fakeAPI
	prepared api.ValueEditRequest
}

func (f *valueAPI) RevealValue(_ context.Context, req api.ValueRevealRequest) (core.Value, error) {
	if req.Key == "gone" {
		return core.Value{}, &api.CodedError{Code: api.CodeGone, Detail: "no key"}
	}
	return core.Value{Key: req.Key, Value: "v", UID: req.Ref.UID}, nil
}

func (f *valueAPI) PrepareValueEdit(_ context.Context, req api.ValueEditRequest) (core.ValuePlan, error) {
	f.prepared = req
	return core.ValuePlan{Key: req.Key}, nil
}

func TestRevealedValuesAreNotStored(t *testing.T) {
	h := NewHTTP(&valueAPI{}, events.NewEmitter())
	ts := httptest.NewServer(h)
	defer ts.Close()
	for _, key := range []string{"password", "gone"} {
		resp := call(t, h, ts.URL, "RevealValue", `{"ref":{"provider":"k","target":"a","kind":"secrets","name":"db","uid":"u"},"key":"`+key+`"}`)
		assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"), key)
	}
}

// A value of 1 MiB typed as base64 (~1.4 MB, in lines) passes the transport.
func TestTheTransportTakesAWholeValue(t *testing.T) {
	f := &valueAPI{}
	h := NewHTTP(f, events.NewEmitter())
	ts := httptest.NewServer(h)
	defer ts.Close()
	b64 := base64.StdEncoding.EncodeToString(make([]byte, 1<<20))
	var lines strings.Builder
	for i := 0; i < len(b64); i += 76 {
		lines.WriteString(b64[i:min(i+76, len(b64))])
		lines.WriteString("\r\n")
	}
	body, _ := json.Marshal(api.ValueEditRequest{Ref: core.Ref{Provider: "k", Target: "a"}, Base: "b", Key: "k", Op: core.ValueSet, Value: lines.String(), Encoding: api.ValueBase64})
	resp := call(t, h, ts.URL, "PrepareValueEdit", string(body))
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, lines.String(), f.prepared.Value)
}

func (f *valueAPI) RunValueEdit(_ context.Context, req api.ValueRunRequest) (core.ValueResult, error) {
	f.prepared = req.ValueEditRequest
	return core.ValueResult{Message: req.Token}, nil
}

// The UI sends a run flat: the change's fields and its token side by side.
func TestAValueRunIsFlat(t *testing.T) {
	f := &valueAPI{}
	h := NewHTTP(f, events.NewEmitter())
	ts := httptest.NewServer(h)
	defer ts.Close()
	resp := call(t, h, ts.URL, "RunValueEdit", `{"ref":{"provider":"k","target":"a"},"base":"b","key":"password","op":"set","value":"x","encoding":"text","token":"t"}`)
	var res core.ValueResult
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&res))
	assert.Equal(t, "t", res.Message)
	assert.Equal(t, api.ValueEditRequest{Ref: core.Ref{Provider: "k", Target: "a"}, Base: "b", Key: "password", Op: "set", Value: "x", Encoding: "text"}, f.prepared)
}
