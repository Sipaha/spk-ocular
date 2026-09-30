package agentapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	"github.com/spk/spk-ocular/internal/store"
)

type env struct {
	t   *testing.T
	f   *fakeProv
	svc *api.Service
	st  *store.Store
	srv *Server
	em  *events.Emitter
}

func newEnv(t *testing.T) *env {
	t.Helper()
	f := newFake()
	reg, err := provider.NewRegistry(f)
	require.NoError(t, err)
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	em := events.NewEmitter()
	svc := api.NewService(reg, st, em, api.Options{Version: "test", Mode: "browser", Getenv: func(string) string { return "" }})
	t.Cleanup(svc.Close)
	srv := New(Options{Service: svc, Store: st, Socket: "/nowhere/agent.sock", Lock: "/nowhere/agent.sock.lock", Version: "test"})
	t.Cleanup(srv.Close)
	svc.SetAgentControl(srv)
	return &env{t: t, f: f, svc: svc, st: st, srv: srv, em: em}
}

func one(ns, verb string, kinds ...string) agentgrant.Grant {
	g := agentgrant.Grant{Scope: agentgrant.Scope{Mode: agentgrant.ScopeOne, Name: ns}, Verb: verb}
	if len(kinds) > 0 {
		g.Kinds = kinds
	}
	return g
}

func (e *env) grant(gs ...agentgrant.Grant) {
	e.t.Helper()
	require.NoError(e.t, e.svc.SaveAgentGrants(context.Background(), api.SaveAgentGrantsRequest{Provider: "k", Target: "t", Grants: gs}))
}

// call POSTs a method as agent "claude"; it returns the status and the
// decoded body.
func (e *env) call(method string, body any) (int, map[string]any) {
	e.t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "/v1/"+method, bytes.NewReader(b))
	r.Header.Set(agentHeader, "claude")
	w := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(w, r)
	var out map[string]any
	require.NoError(e.t, json.Unmarshal(w.Body.Bytes(), &out), w.Body.String())
	return w.Code, out
}

func (e *env) ok(method string, body any) map[string]any {
	e.t.Helper()
	code, out := e.call(method, body)
	require.Equal(e.t, http.StatusOK, code, "%s: %v", method, out)
	return out
}

func (e *env) refused(method string, body any) string {
	e.t.Helper()
	code, out := e.call(method, body)
	require.Equal(e.t, http.StatusForbidden, code, "%s: %v", method, out)
	assert.Equal(e.t, "forbidden", out["code"])
	return out["detail"].(string)
}

var tgt = map[string]any{"provider": "k", "target": "t"}

func with(m map[string]any, kv ...any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i].(string)] = kv[i+1]
	}
	return out
}

func names(out map[string]any) []string {
	var ns []string
	for _, r := range out["rows"].([]any) {
		ref := r.(map[string]any)["ref"].(map[string]any)
		scope, _ := ref["scope"].(string)
		ns = append(ns, scope+"/"+ref["name"].(string))
	}
	return ns
}

func TestNothingIsGrantedByDefault(t *testing.T) {
	e := newEnv(t)
	out := e.ok("Access", nil)
	assert.Empty(t, out["targets"])
	for method, body := range map[string]any{
		"ListKinds":     tgt,
		"ListObjects":   with(tgt, "kind", "pods", "scope", "a"),
		"GetObject":     map[string]any{"ref": ref("a", "pods", "web-1")},
		"Problems":      tgt,
		"GetMetrics":    map[string]any{"refs": []core.Ref{ref("a", "pods", "web-1")}},
		"GetLogs":       map[string]any{"ref": ref("a", "pods", "web-1")},
		"PrepareAction": map[string]any{"ref": ref("a", "apps/deployments", "web"), "action": "restart"},
		"GetEditSource": map[string]any{"ref": ref("a", "apps/deployments", "web")},
	} {
		assert.Contains(t, e.refused(method, body), "nothing is granted", method)
	}
}

// Every method needs its verb, in its scope, for its kind.
func TestMethodsNeedTheirGrant(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", agentgrant.VerbRead, "pods"))
	web := ref("a", "pods", "web-1")
	e.ok("GetObject", map[string]any{"ref": web})
	e.ok("ListObjects", with(tgt, "kind", "pods", "scope", "a"))
	e.ok("GetMetrics", map[string]any{"refs": []core.Ref{web}})
	e.refused("GetLogs", map[string]any{"ref": web})
	e.refused("GetObject", map[string]any{"ref": ref("b", "pods", "api-1")})
	e.refused("GetObject", map[string]any{"ref": ref("a", "apps/deployments", "web")})
	e.refused("ListObjects", with(tgt, "kind", "pods", "scope", "b"))
	e.refused("PrepareAction", map[string]any{"ref": ref("a", "apps/deployments", "web"), "action": "restart"})
	e.refused("GetEditSource", map[string]any{"ref": ref("a", "apps/deployments", "web")})

	e.grant(one("a", agentgrant.VerbLogs, "pods"))
	out := e.ok("GetLogs", map[string]any{"ref": web})
	assert.Equal(t, "hello from web-1", out["lines"].([]any)[0].(map[string]any)["text"])
}

// A namespaced kind is never read without a namespace nor through an
// all-scope selector the agent could send.
func TestScopedKindsNeedTheirScope(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", agentgrant.VerbRead))
	e.refused("GetObject", map[string]any{"ref": ref("", "pods", "web-1")})
	out := e.ok("ListObjects", with(tgt, "kind", "pods"))
	assert.Equal(t, []string{"a/web-1"}, names(out), "without scope: the namespaces granted")
	code, _ := e.call("ListObjects", with(tgt, "kind", "nope", "scope", "a"))
	assert.Equal(t, http.StatusNotImplemented, code)
	e.refused("ListObjects", with(tgt, "kind", "nodes"))
	code, _ = e.call("ListObjects", with(tgt, "kind", "nodes", "scope", "a"))
	assert.Equal(t, http.StatusBadRequest, code)
}

func TestFanOutOverTheNamespacesGranted(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", agentgrant.VerbRead), one("b", agentgrant.VerbRead, "pods"), one("c", agentgrant.VerbLogs))
	out := e.ok("ListObjects", with(tgt, "kind", "pods"))
	assert.Equal(t, []string{"a/web-1", "b/api-1"}, names(out), "c: logs only")
	scopes := out["scopes"].([]any)
	require.Len(t, scopes, 2)
	assert.Equal(t, "ready", scopes[0].(map[string]any)["state"])
	cols := out["columns"].([]any)
	assert.Len(t, cols, 2, "metric columns left out")
	assert.Len(t, out["rows"].([]any)[0].(map[string]any)["cells"], 2)

	out = e.ok("ListObjects", with(tgt, "kind", "pods", "limit", 1))
	assert.Equal(t, []string{"a/web-1"}, names(out))
	assert.Equal(t, true, out["truncated"])
	out = e.ok("ListObjects", with(tgt, "kind", "pods", "name", "API"))
	assert.Equal(t, []string{"b/api-1"}, names(out))
	out = e.ok("ListObjects", with(tgt, "kind", "apps/deployments"))
	assert.Equal(t, []string{"a/web"}, names(out), "b grants pods only")
}

func TestAllNamespaces(t *testing.T) {
	e := newEnv(t)
	e.grant(agentgrant.Grant{Scope: agentgrant.Scope{Mode: agentgrant.ScopeAll}, Verb: agentgrant.VerbRead, Kinds: []string{"pods"}})
	out := e.ok("ListObjects", with(tgt, "kind", "pods"))
	assert.Equal(t, []string{"a/web-1", "b/api-1", "c/x-1"}, names(out))
	e.refused("ListObjects", with(tgt, "kind", "nodes"))

	e.f.mu.Lock()
	e.f.allDenied = true
	e.f.mu.Unlock()
	assert.Contains(t, e.refused("ListObjects", with(tgt, "kind", "pods")), "grant the namespaces by name")
}

func TestClusterGrantReadsObjectsOutsideNamespaces(t *testing.T) {
	e := newEnv(t)
	e.grant(agentgrant.Grant{Scope: agentgrant.Scope{Mode: agentgrant.ScopeCluster}, Verb: agentgrant.VerbRead})
	out := e.ok("ListObjects", with(tgt, "kind", "nodes"))
	assert.Equal(t, []string{"/n1"}, names(out))
	e.refused("ListObjects", with(tgt, "kind", "pods"))
	e.refused("PrepareAction", map[string]any{"ref": ref("", "nodes", "n1"), "action": "cordon"})
}

func TestListKindsShowsWhatIsGranted(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", agentgrant.VerbRead, "pods"), one("a", "action:restart", "apps/deployments"), one("a", agentgrant.VerbEdit),
		agentgrant.Grant{Scope: agentgrant.Scope{Mode: agentgrant.ScopeAll}, Verb: "action:cordon"})
	out := e.ok("ListKinds", tgt)
	got := map[string]map[string]any{}
	for _, k := range out["kinds"].([]any) {
		km := k.(map[string]any)
		got[km["id"].(string)] = km
	}
	assert.ElementsMatch(t, []string{"pods", "apps/deployments"}, keys(got), "edit of all kinds: not Secrets (nor kinds that cannot be edited); cordon: no kind in namespaces has it; nodes: no cluster read")
	assert.Equal(t, []any{"read"}, got["pods"]["verbs"])
	acts := got["apps/deployments"]["actions"].([]any)
	require.Len(t, acts, 1)
	assert.Equal(t, "restart", acts[0].(map[string]any)["id"])
}

func keys(m map[string]map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestRelationsAndProblemsAreFiltered(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", agentgrant.VerbRead, "pods", "apps/deployments"))
	out := e.ok("GetObject", map[string]any{"ref": ref("a", "apps/deployments", "web")})
	rels := out["relations"].([]any)
	require.Len(t, rels, 1)
	assert.Equal(t, "web-1", rels[0].(map[string]any)["ref"].(map[string]any)["name"])
	assert.Equal(t, float64(2), out["hiddenRelations"], "a pod of b, the node")

	out = e.ok("Problems", tgt)
	assert.Equal(t, []string{"a/web-1"}, names(out), "not b's pod, not a's event (kind not granted), not the node")

	e.grant(one("a", agentgrant.VerbRead), one("b", agentgrant.VerbRead), agentgrant.Grant{Scope: agentgrant.Scope{Mode: agentgrant.ScopeCluster}, Verb: agentgrant.VerbRead})
	out = e.ok("Problems", tgt)
	assert.Equal(t, []string{"/n1", "a/web-1", "a/web-1.1", "b/api-1"}, names(out), "the node once, though every namespace read shows it")
	e.refused("Problems", with(tgt, "scope", "c"))
}

func TestMetricsByRef(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", agentgrant.VerbRead))
	out := e.ok("GetMetrics", map[string]any{"refs": []core.Ref{ref("a", "pods", "web-1"), ref("a", "pods", "gone")}})
	items := out["items"].([]any)
	require.Len(t, items, 1)
	assert.Equal(t, 0.5, items[0].(map[string]any)["usage"].(map[string]any)["cpu"])
	assert.Len(t, out["missing"], 1)
	code, _ := e.call("GetMetrics", map[string]any{"refs": []core.Ref{}})
	assert.Equal(t, http.StatusBadRequest, code)
}

func (e *env) runs() []provider.ActionRun {
	e.f.mu.Lock()
	defer e.f.mu.Unlock()
	return append([]provider.ActionRun(nil), e.f.runs...)
}

func prepare(e *env, name, action string, count *int) map[string]any {
	e.t.Helper()
	body := map[string]any{"ref": ref("a", "apps/deployments", name), "action": action}
	if count != nil {
		body["params"] = map[string]any{"count": *count}
	}
	return e.ok("PrepareAction", body)
}

func ptr(n int) *int { return &n }

func TestANonDestructiveActionRunsAtOnce(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", "action:restart"), one("a", "action:scale"))
	p := prepare(e, "web", "restart", nil)
	assert.Equal(t, "none", p["confirmation"])
	assert.Equal(t, false, p["plan"].(map[string]any)["destructive"])
	assert.NotContains(t, p["plan"], "expect", "no UI internals")
	out := e.ok("RunAction", map[string]any{"planId": p["planId"]})
	assert.Equal(t, "done", out["state"])
	assert.Equal(t, "restart web done", out["result"].(map[string]any)["message"])
	require.Len(t, e.runs(), 1)
	assert.Equal(t, "e-restart-web", e.runs()[0].Expect, "the plan's own expectation")

	code, out := e.call("RunAction", map[string]any{"planId": p["planId"]})
	assert.Equal(t, http.StatusNotFound, code, "a plan runs once: %v", out)
	code, _ = e.call("RunAction", map[string]any{"planId": "plan-forged"})
	assert.Equal(t, http.StatusNotFound, code)

	p = prepare(e, "web", "scale", ptr(3))
	e.ok("RunAction", map[string]any{"planId": p["planId"]})
	// Scaling to zero is destructive: "all kinds" does not cover it.
	assert.Contains(t, e.refused("PrepareAction", map[string]any{"ref": ref("a", "apps/deployments", "web"), "action": "scale", "params": map[string]any{"count": 0}}), "only with the kind named")
	e.refused("PrepareAction", map[string]any{"ref": ref("a", "apps/deployments", "web"), "action": "delete"})
}

func TestADestructivePlanWaitsForTheUser(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", "action:delete", "apps/deployments"))
	sub, unsub := e.em.Subscribe()
	defer unsub()
	p := prepare(e, "web", "delete", nil)
	assert.Equal(t, "user", p["confirmation"])
	out := e.ok("RunAction", map[string]any{"planId": p["planId"]})
	assert.Equal(t, stateAwaiting, out["state"])
	runID := out["runId"].(string)
	assert.Empty(t, e.runs(), "nothing written before the user says yes")
	assert.True(t, gotEvent(sub, api.EventAgentPendingChanged))

	pending, err := e.svc.ListAgentPending(context.Background())
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, "claude", pending[0].Agent)
	assert.Equal(t, "Target T", pending[0].TargetTitle)
	require.NotNil(t, pending[0].Action)
	assert.True(t, pending[0].Action.Destructive)
	st, _ := e.svc.AgentAccessStatus(context.Background())
	assert.Equal(t, 1, st.Pending)

	old := getRunWait
	getRunWait = 50 * time.Millisecond
	t.Cleanup(func() { getRunWait = old })
	out = e.ok("GetRun", map[string]any{"runId": runID})
	assert.Equal(t, stateAwaiting, out["state"], "no decision yet")

	require.NoError(t, e.svc.DecideAgentPending(context.Background(), api.DecideAgentPendingRequest{ID: runID, Approve: true}))
	getRunWait = 5 * time.Second
	out = waitRun(e, runID)
	assert.Equal(t, stateDone, out["state"], "%v", out)
	require.Len(t, e.runs(), 1)
	assert.Equal(t, "delete", e.runs()[0].Action)
	assert.True(t, IsGone(e.svc.DecideAgentPending(context.Background(), api.DecideAgentPendingRequest{ID: runID, Approve: true})), "decided once")

	p = prepare(e, "web", "delete", nil)
	out = e.ok("RunAction", map[string]any{"planId": p["planId"]})
	runID = out["runId"].(string)
	require.NoError(t, e.svc.DecideAgentPending(context.Background(), api.DecideAgentPendingRequest{ID: runID, Approve: false}))
	assert.Equal(t, stateRejected, e.ok("GetRun", map[string]any{"runId": runID})["state"])
	assert.Len(t, e.runs(), 1)
	assert.Equal(t, stateGone, e.ok("GetRun", map[string]any{"runId": "run-unknown"})["state"])
}

func IsGone(err error) bool { return api.IsCoded(err, api.CodeGone) }

func waitRun(e *env, id string) map[string]any {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		out := e.ok("GetRun", map[string]any{"runId": id})
		if out["state"] != stateRunning && out["state"] != stateAwaiting || time.Now().After(deadline) {
			return out
		}
	}
}

func gotEvent(sub *events.Subscription, typ string) bool {
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-sub.Wake():
			for _, ev := range sub.Drain() {
				if ev.Type == typ {
					return true
				}
			}
		case <-deadline:
			return false
		}
	}
}

func TestNoConfirmRunsADestructivePlanAtOnce(t *testing.T) {
	e := newEnv(t)
	g := one("a", "action:delete", "apps/deployments")
	g.NoConfirm = true
	e.grant(g)
	p := prepare(e, "web", "delete", nil)
	assert.Equal(t, "none", p["confirmation"])
	out := e.ok("RunAction", map[string]any{"planId": p["planId"]})
	assert.Equal(t, stateDone, out["state"])
	assert.Len(t, e.runs(), 1)
}

func TestGrantsAreCheckedAgainAtEveryStep(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", "action:restart"), one("a", "action:delete", "apps/deployments"))
	p := prepare(e, "web", "restart", nil)
	e.grant(one("a", "action:delete", "apps/deployments")) // restart revoked
	e.refused("RunAction", map[string]any{"planId": p["planId"]})
	assert.Empty(t, e.runs())

	// Revoked while waiting: the user's yes writes nothing.
	p = prepare(e, "web", "delete", nil)
	out := e.ok("RunAction", map[string]any{"planId": p["planId"]})
	runID := out["runId"].(string)
	require.NoError(t, e.svc.RevokeAllAgentGrants(context.Background()))
	require.NoError(t, e.svc.DecideAgentPending(context.Background(), api.DecideAgentPendingRequest{ID: runID, Approve: true}))
	out = waitRun(e, runID)
	assert.Equal(t, stateFailed, out["state"])
	assert.Equal(t, "forbidden", out["error"].(map[string]any)["code"])
	assert.Empty(t, e.runs())
	j, _ := e.st.ListAudit(context.Background(), store.AuditFilter{Limit: 50})
	assert.Equal(t, store.AuditRefused, j[0].Phase, "the refusal is journaled: %+v", j[0])
}

func TestAPlanNamingObjectsOutsideTheGrantIsRefused(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", "action:restart"))
	assert.Contains(t, e.refused("PrepareAction", map[string]any{"ref": ref("a", "apps/deployments", "listy"), "action": "restart"}), "b/api-1")
	e.grant(one("a", "action:restart"), one("b", "action:restart"))
	e.ok("PrepareAction", map[string]any{"ref": ref("a", "apps/deployments", "listy"), "action": "restart"})
}

func TestEdits(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", agentgrant.VerbEdit))
	e.refused("GetEditSource", map[string]any{"ref": ref("a", "secrets", "db")})
	src := e.ok("GetEditSource", map[string]any{"ref": ref("a", "apps/deployments", "web")})
	assert.NotContains(t, src, "base", "the signed base stays here")
	p := e.ok("PrepareEdit", map[string]any{"sourceId": src["sourceId"], "edited": "name: web\nreplicas: 3\n"})
	assert.Equal(t, "none", p["confirmation"])
	out := e.ok("RunEdit", map[string]any{"planId": p["planId"]})
	assert.Equal(t, stateDone, out["state"])
	assert.Equal(t, "2", out["result"].(map[string]any)["version"])

	unchanged := e.ok("PrepareEdit", map[string]any{"sourceId": src["sourceId"], "edited": src["text"]})
	assert.Empty(t, unchanged["planId"])
	assert.Contains(t, e.refused("PrepareEdit", map[string]any{"sourceId": src["sourceId"], "edited": "DESTROY"}), "only with the kind named")

	e.grant(one("a", agentgrant.VerbEdit, "secrets", "apps/deployments"))
	src = e.ok("GetEditSource", map[string]any{"ref": ref("a", "secrets", "db")})
	p = e.ok("PrepareEdit", map[string]any{"sourceId": src["sourceId"], "edited": "DESTROY"})
	assert.Equal(t, "user", p["confirmation"])
	out = e.ok("RunEdit", map[string]any{"planId": p["planId"]})
	assert.Equal(t, stateAwaiting, out["state"])
	pending, _ := e.svc.ListAgentPending(context.Background())
	require.Len(t, pending, 1)
	require.NotNil(t, pending[0].Edit)
	assert.Empty(t, pending[0].Edit.Token, "no token for the UI")

	// The journal keeps no text of an edit.
	j, _ := e.st.ListAudit(context.Background(), store.AuditFilter{Limit: 100})
	for _, en := range j {
		b, _ := json.Marshal(en)
		assert.NotContains(t, string(b), "SECRET-TEXT")
		assert.NotContains(t, string(b), "replicas")
		assert.NotContains(t, string(b), "DESTROY")
	}
}

// Another cluster behind the context suspends its grants; the user's
// confirmation (or the context pointed back) resumes them.
func TestATargetPointedElsewhereSuspendsItsGrants(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", agentgrant.VerbRead))
	sub, unsub := e.em.Subscribe()
	defer unsub()
	e.f.setIdentity("https://b | ca:2 | user:u")
	assert.Contains(t, e.refused("ListObjects", with(tgt, "kind", "pods", "scope", "a")), "points elsewhere")
	assert.True(t, gotEvent(sub, api.EventAgentGrantsChanged))
	out := e.ok("Access", nil)
	assert.Equal(t, "suspended", out["targets"].([]any)[0].(map[string]any)["state"])

	e.f.setIdentity("https://a | ca:1 | user:u")
	e.ok("ListObjects", with(tgt, "kind", "pods", "scope", "a"))
	assert.Equal(t, "active", e.ok("Access", nil)["targets"].([]any)[0].(map[string]any)["state"])

	e.f.setIdentity("https://b | ca:2 | user:u")
	e.refused("ListObjects", with(tgt, "kind", "pods", "scope", "a"))
	require.NoError(t, e.svc.ReconfirmAgentTarget(context.Background(), "k", "t"))
	e.ok("ListObjects", with(tgt, "kind", "pods", "scope", "a"))
}

func TestTheJournal(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", agentgrant.VerbRead), one("a", "action:restart"))
	e.ok("ListObjects", with(tgt, "kind", "pods", "scope", "a"))
	e.ok("ListObjects", with(tgt, "kind", "pods", "scope", "a"))
	e.refused("ListObjects", with(tgt, "kind", "pods", "scope", "b"))
	p := prepare(e, "web", "restart", nil)
	e.ok("RunAction", map[string]any{"planId": p["planId"]})
	j, err := e.svc.ListAgentAudit(context.Background(), store.AuditFilter{})
	require.NoError(t, err)
	var phases []string
	reads := 0
	for _, en := range j {
		assert.Equal(t, "claude", en.Agent)
		switch en.Phase {
		case store.AuditRead:
			reads += en.Count
		default:
			phases = append(phases, en.Phase+":"+en.Method+":"+en.Outcome)
		}
	}
	assert.Equal(t, []string{"outcome:RunAction:done", "intent:RunAction:", "refused:ListObjects:forbidden"}, phases)
	assert.Equal(t, 3, reads, "two lists and the prepare, folded")
	for _, en := range j {
		if en.Phase == store.AuditIntent {
			assert.Equal(t, "action:restart", en.Verb)
			assert.Equal(t, "apps/deployments/a/web", en.Object)
			assert.NotEmpty(t, en.ExpectHash)
			assert.NotContains(t, en.ExpectHash, "e-restart")
		}
	}
}

// Closing Ocular during a write: the journal says unknown.
func TestClosingDuringAWriteLeavesATrace(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", "action:restart"))
	e.f.mu.Lock()
	e.f.runGate = make(chan struct{})
	e.f.mu.Unlock()
	p := prepare(e, "web", "restart", nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = e.call("RunAction", map[string]any{"planId": p["planId"]})
	}()
	require.Eventually(t, func() bool {
		j, _ := e.st.ListAudit(context.Background(), store.AuditFilter{Limit: 10})
		return len(j) > 0 && j[0].Phase == store.AuditIntent
	}, 2*time.Second, 5*time.Millisecond)
	e.srv.Close()
	<-done
	j, _ := e.st.ListAudit(context.Background(), store.AuditFilter{Limit: 10})
	assert.Equal(t, store.AuditOutcome, j[0].Phase)
	assert.Equal(t, string(core.OutcomeUnknown), j[0].Outcome)
}

func TestPendingPlansExpireAndAreBounded(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", "action:delete", "apps/deployments"))
	oldTTL, oldMax := pendingTTL, maxPending
	pendingTTL, maxPending = 100*time.Millisecond, 2
	t.Cleanup(func() { pendingTTL, maxPending = oldTTL, oldMax })
	var ids []string
	for range 2 {
		p := prepare(e, "web", "delete", nil)
		ids = append(ids, e.ok("RunAction", map[string]any{"planId": p["planId"]})["runId"].(string))
	}
	p := prepare(e, "web", "delete", nil)
	code, _ := e.call("RunAction", map[string]any{"planId": p["planId"]})
	assert.Equal(t, http.StatusTooManyRequests, code)
	out := waitRun(e, ids[0])
	assert.Equal(t, stateExpired, out["state"])
	assert.Equal(t, stateExpired, waitRun(e, ids[1])["state"])
	assert.True(t, IsGone(e.svc.DecideAgentPending(context.Background(), api.DecideAgentPendingRequest{ID: ids[1], Approve: true})))
	assert.Empty(t, e.runs())
}

// The catalog is the methods served, each with its description, verb and
// schemas.
func TestTheCatalogIsTheMethods(t *testing.T) {
	e := newEnv(t)
	w := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/v1/methods", nil))
	require.Equal(t, http.StatusOK, w.Code)
	var cat struct {
		Methods []struct {
			Name, Path, Description, Verb string
			Request                       map[string]any
			Response                      map[string]any
		}
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &cat))
	var listed []string
	for _, m := range cat.Methods {
		listed = append(listed, m.Name)
		assert.NotEmpty(t, m.Description, m.Name)
		assert.NotEmpty(t, m.Verb, m.Name)
		assert.Equal(t, "object", m.Request["type"], m.Name)
		assert.Equal(t, "object", m.Response["type"], m.Name)
		// Served: a POST reaches the method, not the fallback.
		_, pattern := e.srv.mux.Handler(httptest.NewRequest("POST", m.Path, nil))
		assert.Equal(t, "POST "+m.Path, pattern)
	}
	assert.ElementsMatch(t, []string{"Access", "ListKinds", "ListObjects", "GetObject", "Problems", "GetMetrics", "GetLogs",
		"PrepareAction", "RunAction", "GetEditSource", "PrepareEdit", "RunEdit", "GetRun"}, listed)
	lo := cat.Methods[2]
	assert.Equal(t, "ListObjects", lo.Name)
	assert.Contains(t, lo.Request["required"], "kind")

	w = httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/v1", nil))
	assert.Contains(t, w.Body.String(), "/v1/methods")
	assert.Contains(t, w.Body.String(), "confirmation")
	w = httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/v1/Exec", strings.NewReader("{}")))
	assert.Equal(t, http.StatusNotFound, w.Code)
}
