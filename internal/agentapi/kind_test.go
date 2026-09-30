package agentapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/agentgrant"
	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/kubernetes"
	"github.com/spk/spk-ocular/internal/store"
)

const kindTarget = "kubeconfig:kind-ocular-dev"

// kindAgent is the whole stack on the kind test cluster: the Kubernetes
// provider reading only its kubeconfig, the service, the agent socket
// served for real, and an agent's HTTP client on that socket.
type kindAgent struct {
	t      *testing.T
	kc     string
	svc    *api.Service
	client *http.Client
}

func newKindAgent(t *testing.T) *kindAgent {
	t.Helper()
	kc := os.Getenv("OCULAR_KIND_KUBECONFIG")
	if kc == "" {
		t.Skip("OCULAR_KIND_KUBECONFIG not set (make test-kind)")
	}
	kc, _ = filepath.Abs(kc)
	a := &kindAgent{t: t, kc: kc}
	server := a.kubectl("config", "view", "--minify", "-o", "jsonpath={.clusters[0].cluster.server}")
	u, err := url.Parse(server)
	require.NoError(t, err)
	require.Contains(t, []string{"127.0.0.1", "localhost", "::1"}, u.Hostname(), "the kind API server is on loopback")

	p := kubernetes.NewWith(func(k string) string {
		if k == "KUBECONFIG" {
			return kc
		}
		return ""
	}, t.TempDir())
	reg, err := provider.NewRegistry(p)
	require.NoError(t, err)
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	svc := api.NewService(reg, st, events.NewEmitter(), api.Options{Version: "test", Mode: "browser", Getenv: func(string) string { return "" }})
	ctx, cancel := context.WithCancel(context.Background())
	svc.Start(ctx)
	t.Cleanup(func() { cancel(); svc.Close() })
	d := sockDir(t)
	srv := New(Options{Service: svc, Store: st, Socket: filepath.Join(d, "agent.sock"), Lock: filepath.Join(d, "agent.sock.lock"), Version: "test"})
	require.NoError(t, srv.Start())
	t.Cleanup(srv.Close)
	svc.SetAgentControl(srv)
	a.svc = svc
	a.client = &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var dl net.Dialer
		return dl.DialContext(ctx, "unix", filepath.Join(d, "agent.sock"))
	}}}
	return a
}

func (a *kindAgent) kubectl(args ...string) string {
	a.t.Helper()
	out, err := exec.Command("kubectl", append([]string{"--kubeconfig", a.kc, "--context", "kind-ocular-dev"}, args...)...).CombinedOutput()
	require.NoError(a.t, err, string(out))
	return strings.TrimSpace(string(out))
}

// namespace creates a namespace of the test's own, deleted after it.
func (a *kindAgent) namespace(prefix string) string {
	a.t.Helper()
	ns := fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano()%1_000_000)
	a.kubectl("create", "namespace", ns)
	a.t.Cleanup(func() {
		_, _ = exec.Command("kubectl", "--kubeconfig", a.kc, "--context", "kind-ocular-dev", "delete", "namespace", ns, "--wait=false").CombinedOutput()
	})
	return ns
}

// call is one request of the agent: the HTTP status and the JSON answer.
func (a *kindAgent) call(method string, body any, out any) int {
	a.t.Helper()
	b, err := json.Marshal(body)
	require.NoError(a.t, err)
	req, err := http.NewRequest(http.MethodPost, "http://ocular/v1/"+method, bytes.NewReader(b))
	require.NoError(a.t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(agentHeader, "kind-test")
	resp, err := a.client.Do(req)
	require.NoError(a.t, err)
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	require.NoError(a.t, err)
	if out != nil && resp.StatusCode == http.StatusOK {
		require.NoError(a.t, json.Unmarshal(data, out), string(data))
	}
	return resp.StatusCode
}

func (a *kindAgent) objects(req ListObjectsRequest) (ObjectsView, int) {
	var v ObjectsView
	return v, a.call("ListObjects", req, &v)
}

func rowNames(v ObjectsView) []string {
	var out []string
	for _, r := range v.Rows {
		out = append(out, r.Ref.Name)
	}
	return out
}

// TestKindAgentAccessOverTheSocket: an agent on the real socket reads the
// namespace granted and not another, a discovered custom resource with its
// server's columns, a pod's logs and metrics; restarts a Deployment by its
// grant and is refused what is not granted; its delete waits for the
// user's decision (No, then Yes).
func TestKindAgentAccessOverTheSocket(t *testing.T) {
	a := newKindAgent(t)
	ns := a.namespace("ocular-agent")
	other := a.namespace("ocular-agent-other")
	a.kubectl("-n", ns, "create", "deployment", "app", "--image=busybox:1.36", "--", "sh", "-c", "while true; do echo hello-agent; sleep 1; done")
	a.kubectl("-n", other, "create", "deployment", "hidden", "--image=busybox:1.36", "--", "sleep", "3600")
	a.kubectl("-n", ns, "rollout", "status", "deployment/app", "--timeout=120s")

	ctx := context.Background()
	deployments := []string{"apps/deployments"}
	require.NoError(t, a.svc.SaveAgentGrants(ctx, api.SaveAgentGrantsRequest{Provider: "kubernetes", Target: kindTarget, Grants: []agentgrant.Grant{
		one(ns, agentgrant.VerbRead), one(ns, agentgrant.VerbLogs),
		one(ns, agentgrant.ActionVerb("restart"), deployments...), one(ns, agentgrant.ActionVerb("delete"), deployments...),
		one("ocular-crd", agentgrant.VerbRead), one("ocular-demo", agentgrant.VerbRead),
	}}))

	var access AccessView
	require.Equal(t, http.StatusOK, a.call("Access", AccessRequest{}, &access))
	require.Len(t, access.Targets, 1)

	// The granted namespace's Deployments; another's are not there, nor asked for.
	target := TargetRef{Provider: "kubernetes", Target: kindTarget}
	v, code := a.objects(ListObjectsRequest{TargetRef: target, Kind: "apps/deployments"})
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, rowNames(v), "app")
	assert.NotContains(t, rowNames(v), "hidden")
	_, code = a.objects(ListObjectsRequest{TargetRef: target, Kind: "apps/deployments", Scope: other})
	assert.Equal(t, http.StatusForbidden, code)

	// A discovered kind (the CRD of the seed): the server's columns, one cell each.
	var widgets string
	require.Eventually(t, func() bool {
		var kinds struct {
			Kinds []KindView `json:"kinds"`
		}
		if a.call("ListKinds", ListKindsRequest{TargetRef: target}, &kinds) != http.StatusOK {
			return false
		}
		for _, k := range kinds.Kinds {
			if strings.HasSuffix(k.ID, "widgets") && strings.Contains(k.ID, "ocular.dev") {
				widgets = k.ID
			}
		}
		return widgets != ""
	}, 60*time.Second, time.Second, "the widgets CRD is discovered")
	v, code = a.objects(ListObjectsRequest{TargetRef: target, Kind: widgets, Scope: "ocular-crd"})
	require.Equal(t, http.StatusOK, code)
	assert.ElementsMatch(t, []string{"alpha", "beta", "gamma"}, rowNames(v))
	var titles []string
	for _, c := range v.Columns {
		titles = append(titles, c.Title)
	}
	assert.Subset(t, titles, []string{"Size", "Phase"})
	for _, r := range v.Rows {
		assert.Len(t, r.Cells, len(v.Columns), "a cell per column: %s", r.Ref.Name)
		if r.Ref.Name == "alpha" {
			for i, c := range v.Columns {
				if c.Title == "Size" {
					assert.Equal(t, "3", r.Cells[i])
				}
			}
		}
	}

	// Logs of the Deployment's pod.
	pods, code := a.objects(ListObjectsRequest{TargetRef: target, Kind: "pods", Scope: ns})
	require.Equal(t, http.StatusOK, code)
	require.NotEmpty(t, pods.Rows)
	var tail api.Tail
	require.Eventually(t, func() bool {
		tail = api.Tail{}
		if a.call("GetLogs", GetLogsRequest{Ref: pods.Rows[0].Ref, TailLines: 5}, &tail) != http.StatusOK {
			return false
		}
		return len(tail.Lines) > 0
	}, 30*time.Second, time.Second)
	assert.Contains(t, tail.Lines[len(tail.Lines)-1].Text, "hello-agent")

	// Metrics of a long-running pod of the seed (metrics-server needs a while for new ones).
	demo, code := a.objects(ListObjectsRequest{TargetRef: target, Kind: "pods", Scope: "ocular-demo", Name: "web-"})
	require.Equal(t, http.StatusOK, code)
	require.NotEmpty(t, demo.Rows)
	require.Eventually(t, func() bool {
		var m MetricsView
		return a.call("GetMetrics", GetMetricsRequest{Refs: []core.Ref{demo.Rows[0].Ref}}, &m) == http.StatusOK && len(m.Items) == 1 && m.Items[0].Usage.Memory != nil
	}, 90*time.Second, 3*time.Second, "the pod's usage")

	// Restart by the grant; scale is not granted.
	dv, _ := a.objects(ListObjectsRequest{TargetRef: target, Kind: "apps/deployments", Scope: ns})
	require.Len(t, dv.Rows, 1)
	deploy := dv.Rows[0].Ref
	var plan PrepareView
	require.Equal(t, http.StatusOK, a.call("PrepareAction", PrepareActionRequest{Ref: deploy, Action: "restart"}, &plan))
	var run RunView
	require.Equal(t, http.StatusOK, a.call("RunAction", RunRequest{PlanID: plan.PlanID}, &run))
	assert.Equal(t, "done", run.State, "%+v", run)
	assert.NotEmpty(t, a.kubectl("-n", ns, "get", "deployment", "app", "-o", "jsonpath={.spec.template.metadata.annotations.kubectl\\.kubernetes\\.io/restartedAt}"))
	n := 2
	assert.Equal(t, http.StatusForbidden, a.call("PrepareAction", PrepareActionRequest{Ref: deploy, Action: "scale", Params: core.ActionParams{Count: &n}}, nil))

	// Delete waits for the user: No leaves it, Yes deletes it.
	for _, approve := range []bool{false, true} {
		require.Equal(t, http.StatusOK, a.call("PrepareAction", PrepareActionRequest{Ref: deploy, Action: "delete"}, &plan))
		assert.True(t, plan.Plan.Destructive)
		run = RunView{}
		require.Equal(t, http.StatusOK, a.call("RunAction", RunRequest{PlanID: plan.PlanID}, &run))
		require.Equal(t, "awaiting_confirmation", run.State)
		waiting, err := a.svc.ListAgentPending(ctx)
		require.NoError(t, err)
		require.Len(t, waiting, 1)
		assert.Equal(t, "kind-test", waiting[0].Agent)
		require.NoError(t, a.svc.DecideAgentPending(ctx, api.DecideAgentPendingRequest{ID: waiting[0].ID, Approve: approve}))
		var outcome RunView
		require.Equal(t, http.StatusOK, a.call("GetRun", GetRunRequest{RunID: run.RunID}, &outcome))
		if !approve {
			assert.Equal(t, "rejected", outcome.State)
			assert.Equal(t, "app", a.kubectl("-n", ns, "get", "deployment", "app", "-o", "jsonpath={.metadata.name}"))
			continue
		}
		assert.Equal(t, "done", outcome.State, "%+v", outcome)
	}
	require.Eventually(t, func() bool {
		out, _ := exec.Command("kubectl", "--kubeconfig", a.kc, "--context", "kind-ocular-dev", "-n", ns, "get", "deployment", "app").CombinedOutput()
		return strings.Contains(string(out), "NotFound")
	}, 60*time.Second, time.Second)
}
