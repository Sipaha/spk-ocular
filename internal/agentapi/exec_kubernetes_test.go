//go:build !windows

package agentapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/spk/spk-ocular/internal/agentgrant"
	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/providers/kubernetes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Production Kubernetes discovery/dynamic client/exec WebSocket behind the
// real agent MCP endpoint. Only this test's loopback fake API receives requests.
func TestAgentExecKubernetesOverSocket(t *testing.T) {
	var replaced atomic.Bool
	var starts atomic.Int32
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api":
			_, _ = fmt.Fprint(w, `{"kind":"APIVersions","versions":["v1"]}`)
		case "/apis":
			_, _ = fmt.Fprint(w, `{"kind":"APIGroupList","groups":[]}`)
		case "/api/v1":
			_, _ = fmt.Fprint(w, `{"kind":"APIResourceList","groupVersion":"v1","resources":[{"name":"pods","singularName":"pod","namespaced":true,"kind":"Pod","verbs":["get","list","watch"]}]}`)
		case "/version":
			_, _ = fmt.Fprint(w, `{"gitVersion":"v1.36.0"}`)
		case "/api/v1/namespaces/a/pods/web":
			uid := "pod-uid"
			if replaced.Load() {
				uid = "new-uid"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "web", "namespace": "a", "uid": uid}, "spec": map[string]any{"containers": []any{map[string]any{"name": "app", "image": "fixture"}}}, "status": map[string]any{"phase": "Running", "containerStatuses": []any{map[string]any{"name": "app", "state": map[string]any{"running": map[string]any{}}}}}})
		case "/api/v1/namespaces/a/pods/web/exec":
			starts.Add(1)
			assert.Equal(t, []string{"program", "literal; $(not-a-shell)"}, r.URL.Query()["command"])
			assert.Equal(t, "false", r.URL.Query().Get("tty"))
			assert.Equal(t, "true", r.URL.Query().Get("stderr"))
			c, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"v5.channel.k8s.io"}})
			if err != nil {
				t.Errorf("accept exec: %v", err)
				return
			}
			defer func() { _ = c.CloseNow() }()
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			var input []byte
			for {
				_, b, err := c.Read(ctx)
				if err != nil {
					t.Errorf("read stdin: %v", err)
					return
				}
				if len(b) > 0 && b[0] == 0 {
					input = append(input, b[1:]...)
				}
				if len(b) == 2 && b[0] == 255 && b[1] == 0 {
					break
				}
			}
			assert.Equal(t, "finite stdin", string(input))
			for _, b := range [][]byte{append([]byte{1}, []byte("kube-out\x00\xff")...), append([]byte{2}, []byte("kube-err\n")...), append([]byte{3}, []byte(`{"status":"Failure","reason":"NonZeroExitCode","details":{"causes":[{"reason":"ExitCode","message":"7"}]}}`)...)} {
				if err := c.Write(ctx, websocket.MessageBinary, b); err != nil {
					t.Errorf("write exec: %v", err)
					return
				}
			}
			_ = c.Close(websocket.StatusNormalClosure, "")
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`)
		}
	}))
	t.Cleanup(fake.Close)
	home := t.TempDir()
	kc := filepath.Join(home, "config")
	require.NoError(t, os.WriteFile(kc, []byte(fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: fixture\n  cluster:\n    server: %s\nusers:\n- name: fixture\n  user: {}\ncontexts:\n- name: fixture\n  context:\n    cluster: fixture\n    user: fixture\ncurrent-context: fixture\n", fake.URL)), 0600))
	p := kubernetes.NewWith(func(k string) string {
		if k == "KUBECONFIG" {
			return kc
		}
		return ""
	}, home)
	a := serveAgent(t, p)
	grant := one("a", agentgrant.VerbExec, "pods")
	grant.NoConfirm = true
	require.NoError(t, a.svc.SaveAgentGrants(context.Background(), api.SaveAgentGrantsRequest{Provider: "kubernetes", Target: "kubeconfig:fixture", Grants: []agentgrant.Grant{grant}}))
	ref := core.Ref{Provider: "kubernetes", Target: "kubeconfig:fixture", Kind: "pods", Scope: "a", Name: "web", UID: "pod-uid"}
	var info core.ExecInfo
	require.Equal(t, http.StatusOK, a.call("GetExecInfo", GetExecInfoRequest{Ref: ref}, &info))
	require.Len(t, info.Instances, 1)
	req := PrepareExecRequest{Ref: ref, Instance: info.Instances[0].ID, Channel: "app", Command: []string{"program", "literal; $(not-a-shell)"}, Stdin: "finite stdin"}
	var prep PrepareExecView
	require.Equal(t, http.StatusOK, a.call("PrepareExec", req, &prep))
	assert.Zero(t, starts.Load())
	var run RunView
	require.Equal(t, http.StatusOK, a.call("RunExec", RunRequest{PlanID: prep.PlanID}, &run))
	require.NotNil(t, run.Exec)
	assert.Equal(t, []byte("kube-out\x00\xff"), run.Exec.Stdout)
	assert.Equal(t, []byte("kube-err\n"), run.Exec.Stderr)
	require.NotNil(t, run.Exec.ExitCode)
	assert.Equal(t, 7, *run.Exec.ExitCode)
	assert.True(t, run.Exec.Complete)
	assert.Equal(t, int32(1), starts.Load())
	require.Equal(t, http.StatusOK, a.call("PrepareExec", req, &prep))
	replaced.Store(true)
	assert.Equal(t, http.StatusGone, a.call("RunExec", RunRequest{PlanID: prep.PlanID}, nil))
	assert.Equal(t, int32(1), starts.Load())
}
