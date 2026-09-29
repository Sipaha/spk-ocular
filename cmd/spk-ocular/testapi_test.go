package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/paths"
	"github.com/spk/spk-ocular/internal/providers/synthetic"
)

// synthServer is the browser API with the synthetic provider and the test
// routes; post sends JSON with the token.
func synthServer(t *testing.T) func(path string, body any) (int, []byte) {
	t.Helper()
	t.Setenv(paths.EnvHome, t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KUBECONFIG", "")
	c, err := newCore(context.Background(), "browser", true)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	h, token := newBrowserHandler(c, dist, true)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return func(path string, body any) (int, []byte) {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		req, err := http.NewRequest(http.MethodPost, srv.URL+path, bytes.NewReader(b))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", srv.URL)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		out, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, out
	}
}

// The synthetic actions through the real API, steered by the test routes
// (errors are 400 with the class in "code").
func TestSyntheticActionsThroughTheAPI(t *testing.T) {
	post := synthServer(t)
	ref := core.Ref{Provider: synthetic.ID, Target: synthetic.Target, Kind: synthetic.WorkloadKind, Name: "web"}
	prepare := func(action string, p core.ActionParams) core.ActionPlan {
		code, body := post("/api/PrepareAction", map[string]any{"ref": ref, "action": action, "params": p})
		require.Equal(t, http.StatusOK, code, string(body))
		var plan core.ActionPlan
		require.NoError(t, json.Unmarshal(body, &plan))
		return plan
	}
	run := func(plan core.ActionPlan) (int, string) {
		code, body := post("/api/RunAction", map[string]any{"ref": plan.Where.Ref, "action": plan.Action.ID, "params": plan.Params,
			"expect": plan.Expect, "configRev": plan.Where.ConfigRev})
		return code, string(body)
	}
	three := 3

	plan := prepare("scale", core.ActionParams{Count: &three})
	require.NotNil(t, plan.Current)
	assert.Equal(t, 2, *plan.Current)
	code, body := run(plan)
	require.Equal(t, http.StatusOK, code, body)
	assert.Contains(t, body, "scale of web to 3 requested")

	code, _ = post("/api/_test/synthetic/controls", map[string]any{"rights": "denied"})
	require.Equal(t, http.StatusNoContent, code)
	plan = prepare("restart", core.ActionParams{})
	assert.Equal(t, core.RightsDenied, plan.Rights.State)
	code, body = run(plan)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, body, `"code":"forbidden"`)

	code, _ = post("/api/_test/synthetic/reset", nil)
	require.Equal(t, http.StatusNoContent, code)
	plan = prepare("scale", core.ActionParams{Count: &three})
	assert.Equal(t, 2, *plan.Current, "reset brings the count back")
	code, _ = post("/api/_test/synthetic/mutate", map[string]any{"object": "web", "replicas": 5})
	require.Equal(t, http.StatusNoContent, code)
	code, body = run(plan)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, body, `"code":"conflict"`)
	code, _ = post("/api/_test/synthetic/mutate", map[string]any{"object": "nope"})
	assert.Equal(t, http.StatusNotFound, code)
}
