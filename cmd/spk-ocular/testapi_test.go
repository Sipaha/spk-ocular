package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	t.Setenv(paths.EnvHome, shortHome(t))
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
	assert.Contains(t, body, "workload web: scale 2 → 3 requested")

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

// The ui-state reset gives the next e2e spec a clean page snapshot (P19):
// the shared e2e instance keeps target_state, and without the reset a
// previous spec's restored page (filter, sort, open details) would leak
// into the next one. Only the page memo key goes.
func TestUIStateResetRoute(t *testing.T) {
	post := synthServer(t)
	set := func(key, value string) {
		code, body := post("/api/SetTargetState", map[string]any{"provider": synthetic.ID, "target": synthetic.Target, "key": key, "value": value})
		require.Equal(t, http.StatusOK, code, string(body))
	}
	set("pageMemo", `{"v":1}`)
	set("kind", `"services"`)

	code, _ := post("/api/_test/ui-state/reset", nil)
	require.Equal(t, http.StatusNoContent, code)

	code, body := post("/api/GetTargetState", map[string]any{"provider": synthetic.ID, "target": synthetic.Target})
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, string(body), "pageMemo")
	assert.Contains(t, string(body), `"kind"`)
}

// The desktop's test routes: loopback, a token, where to find both in the
// data directory (owner-only), gone when stopped.
func TestDesktopTestAPI(t *testing.T) {
	t.Setenv(paths.EnvHome, shortHome(t))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KUBECONFIG", "")
	c, err := newCore(context.Background(), "desktop", false)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	stop, err := startTestAPI(c)
	require.NoError(t, err)
	file := filepath.Join(c.Paths.DataDir, testAPIFile)
	fi, err := os.Stat(file)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	var info struct{ URL, Token string }
	b, _ := os.ReadFile(file)
	require.NoError(t, json.Unmarshal(b, &info))
	assert.True(t, strings.HasPrefix(info.URL, "http://127.0.0.1:"), info.URL)

	resp, err := http.Get(info.URL + "/api/_test/stats")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	req, _ := http.NewRequest(http.MethodGet, info.URL+"/api/_test/stats", nil)
	req.Header.Set("Authorization", "Bearer "+info.Token)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	var st map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&st))
	_ = resp.Body.Close()
	assert.Contains(t, st, "goroutines")

	stop()
	_, err = os.Stat(file)
	assert.True(t, os.IsNotExist(err))
	_, err = http.Get(info.URL + "/api/_test/stats")
	assert.Error(t, err, "the listener is closed")
}

func TestNativeSmokeSelectsReadySyntheticServices(t *testing.T) {
	post := synthServer(t)
	code, body := post("/api/_test/synthetic/select", nil)
	require.Equal(t, http.StatusNoContent, code, string(body))
	code, body = post("/api/GetTargetState", map[string]string{"provider": synthetic.ID, "target": synthetic.Target})
	require.Equal(t, http.StatusOK, code, string(body))
	var state map[string]string
	require.NoError(t, json.Unmarshal(body, &state))
	require.Equal(t, `"services"`, state["kind"])
}
