package kubernetes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	kt "k8s.io/client-go/testing"
)

var gatekeeperGVR = schema.GroupVersionResource{Group: "constraints.gatekeeper.sh", Version: "v1beta1", Resource: "k8srequiredlabels"}

func gatekeeperRef() core.Ref {
	return core.Ref{Provider: ProviderID, Target: "t", Kind: "constraints.gatekeeper.sh/k8srequiredlabels", Name: "required-labels", UID: "constraint-uid"}
}
func gatekeeperObject() *unstructured.Unstructured {
	u := mk(gatekeeperGVR.GroupVersion().String(), "K8sRequiredLabels", "", "required-labels", "constraint-uid", map[string]any{"spec": map[string]any{"parameters": map[string]any{"password": "PRIVATE-PARAMETER"}}, "status": map[string]any{"auditTimestamp": "2026-10-22T00:00:00Z", "totalViolations": int64(5), "details": "PRIVATE-DETAILS", "violations": []any{map[string]any{"group": "", "version": "v1", "kind": "Secret", "namespace": "foreign", "name": "descriptive-secret", "message": "literal <script>not executed</script>", "enforcementAction": "scoped", "enforcementActions": []any{"warn", "dryrun"}, "uid": "UNVERIFIED-UID", "severity": "PRIVATE-SEVERITY", "extra": "PRIVATE-EXTRA"}}}})
	u.SetResourceVersion("7")
	return u
}
func gatekeeperCatalog(s *session, version string, namespaced bool, verbs []string) {
	d := discoveredDef(apiResource{Group: gatekeeperGVR.Group, Version: version, Resource: gatekeeperGVR.Resource, Kind: "K8sRequiredLabels", Namespaced: namespaced, Verbs: verbs})
	s.cat.cur.Store(&catalogSnap{reg: newKindRegistry(d), state: catalogReady})
}
func gatekeeperSession(t *testing.T, u *unstructured.Unstructured) (*session, *fake.FakeDynamicClient) {
	c := fake.NewSimpleDynamicClient(runtime.NewScheme())
	require.NoError(t, c.Tracker().Create(gatekeeperGVR, u, ""))
	s := newSession("t", "h", c, false)
	t.Cleanup(s.Close)
	gatekeeperCatalog(s, "v1beta1", false, []string{"get"})
	return s, c
}

func TestGatekeeperSelectedOnlyExactDiscoveryAndIdentity(t *testing.T) {
	s, c := gatekeeperSession(t, gatekeeperObject())
	out, err := s.GatekeeperAudit(t.Context(), gatekeeperRef())
	require.NoError(t, err)
	require.Equal(t, "7", out.ResourceVersion)
	require.Equal(t, "producer-capped", out.Coverage)
	require.EqualValues(t, 5, *out.TotalViolations)
	require.Len(t, out.Violations, 1)
	require.Equal(t, "scoped", out.Violations[0].EnforcementAction)
	require.Equal(t, []string{"warn", "dryrun"}, out.Violations[0].EnforcementActions)
	b, err := json.Marshal(out)
	require.NoError(t, err)
	require.NotContains(t, string(b), "PRIVATE")
	require.NotContains(t, string(b), "UNVERIFIED-UID")
	require.Contains(t, string(b), "descriptive-secret")
	require.Len(t, c.Actions(), 2)
	for _, a := range c.Actions() {
		require.Equal(t, "get", a.GetVerb())
		require.Equal(t, gatekeeperGVR, a.GetResource())
		require.Empty(t, a.GetNamespace())
		require.Equal(t, "required-labels", a.(kt.GetAction).GetName())
	}
	for _, tc := range []struct {
		version string
		ns      bool
		verbs   []string
	}{{"v1alpha1", false, []string{"get"}}, {"v9", false, []string{"get"}}, {"v1beta1", true, []string{"get"}}, {"v1beta1", false, []string{"list", "watch"}}} {
		gatekeeperCatalog(s, tc.version, tc.ns, tc.verbs)
		_, err = s.GatekeeperAudit(t.Context(), gatekeeperRef())
		require.Error(t, err)
		require.Len(t, c.Actions(), 2)
	}
	gatekeeperCatalog(s, "v1beta1", false, []string{"get"})
	snap := s.cat.snap()
	s.cat.cur.Store(&catalogSnap{reg: snap.reg, state: catalogReady, unconfirmed: []string{gatekeeperGVR.Group}})
	_, err = s.GatekeeperAudit(t.Context(), gatekeeperRef())
	require.Error(t, err)
	require.Len(t, c.Actions(), 2)
}
func TestGatekeeperReplacementRevisionAndSafeReadErrors(t *testing.T) {
	for _, mode := range []string{"replacement", "revision", "denied", "missing", "wrong-api", "wrong-kind", "namespace", "no-rv", "too-large"} {
		t.Run(mode, func(t *testing.T) {
			s, c := gatekeeperSession(t, gatekeeperObject())
			calls := 0
			c.PrependReactor("get", "*", func(kt.Action) (bool, runtime.Object, error) {
				calls++
				u := gatekeeperObject()
				if calls == 2 {
					switch mode {
					case "replacement":
						u.SetUID("new")
					case "revision":
						u.SetResourceVersion("8")
					case "denied":
						return true, nil, apierrors.NewForbidden(gatekeeperGVR.GroupResource(), u.GetName(), context.DeadlineExceeded)
					case "missing":
						return true, nil, apierrors.NewNotFound(gatekeeperGVR.GroupResource(), "PRIVATE-SERVER")
					case "wrong-api":
						u.SetAPIVersion("foreign/v1")
					case "wrong-kind":
						u.SetKind("Other")
					case "namespace":
						u.SetNamespace("foreign")
					case "no-rv":
						u.SetResourceVersion("")
					case "too-large":
						u.Object["private"] = strings.Repeat("x", 1<<20)
					}
				}
				return true, u, nil
			})
			out, err := s.GatekeeperAudit(t.Context(), gatekeeperRef())
			require.Error(t, err)
			require.NotContains(t, err.Error(), "PRIVATE")
			require.Empty(t, out.Violations)
			require.Equal(t, 2, calls)
		})
	}
}
func TestGatekeeperReportedZeroMissingInvalidAndProjectionCaps(t *testing.T) {
	for _, tc := range []struct {
		name     string
		total    any
		rows     any
		coverage string
	}{{"zero", int64(0), nil, "reported"}, {"capped-no-list", int64(5), nil, "producer-capped"}, {"unknown", nil, nil, "unknown"}, {"negative", int64(-1), nil, "unknown"}, {"rounded", int64(1 << 53), nil, "unknown"}, {"float", float64(1), nil, "unknown"}, {"bad-list", int64(0), "bad", "inconsistent"}, {"too-small", int64(0), []any{map[string]any{}}, "inconsistent"}} {
		t.Run(tc.name, func(t *testing.T) {
			u := gatekeeperObject()
			status := map[string]any{"auditTimestamp": "invalid"}
			if tc.total != nil {
				status["totalViolations"] = tc.total
			}
			if tc.rows != nil {
				status["violations"] = tc.rows
			}
			u.Object["status"] = status
			out := core.GatekeeperAudit{Violations: []core.GatekeeperViolation{}}
			projectGatekeeper(&out, u)
			require.Equal(t, tc.coverage, out.Coverage)
			require.Equal(t, "invalid", out.AuditTimeState)
			require.True(t, out.Partial)
			require.Empty(t, out.AuditTime)
		})
	}
	u := gatekeeperObject()
	row := u.Object["status"].(map[string]any)["violations"].([]any)[0].(map[string]any)
	row["message"] = strings.Repeat("界", 600)
	row["enforcementActions"] = make([]any, 20)
	rows := make([]any, 140)
	for i := range rows {
		rows[i] = row
	}
	u.Object["status"].(map[string]any)["violations"] = rows
	out := core.GatekeeperAudit{}
	projectGatekeeper(&out, u)
	require.True(t, out.Partial)
	require.Len(t, out.Violations, 128)
	require.Equal(t, 140, out.ReturnedViolations)
	require.LessOrEqual(t, len(out.Violations[0].Message), 512)
}
func TestGatekeeperScopeAndCancelledGateWait(t *testing.T) {
	s, c := gatekeeperSession(t, gatekeeperObject())
	for _, field := range []string{"uid", "scope", "target", "provider", "name"} {
		r := gatekeeperRef()
		switch field {
		case "uid":
			r.UID = ""
		case "scope":
			r.Scope = "blue"
		case "target":
			r.Target = "other"
		case "provider":
			r.Provider = "other"
		case "name":
			r.Name = "../foreign"
		}
		_, err := s.GatekeeperAudit(t.Context(), r)
		require.Error(t, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := s.GatekeeperAudit(ctx, gatekeeperRef())
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, c.Actions())
	_, release, err := s.inventoryContext(t.Context())
	require.NoError(t, err)
	defer release()
	ctx, cancel = context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, err = s.GatekeeperAudit(ctx, gatekeeperRef())
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Empty(t, c.Actions())
	var e *provider.Error
	_, err = s.GatekeeperAudit(t.Context(), core.Ref{})
	require.ErrorAs(t, err, &e)
	require.Equal(t, provider.ClassInvalid, e.Class)
}

func TestGatekeeperActualHTTPBudgetCancellationAndSafeFailures(t *testing.T) {
	for _, mode := range []string{"normal", "oversized-object", "oversized-body", "denied", "cancel", "retry", "retry-success"} {
		t.Run(mode, func(t *testing.T) {
			var count atomic.Int32
			started := make(chan struct{}, 1)
			closed := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				if r.Method != "GET" || r.URL.Path != "/apis/constraints.gatekeeper.sh/v1beta1/k8srequiredlabels/required-labels" || r.URL.RawQuery != "" {
					t.Errorf("unexpected acquisition %s %s", r.Method, r.URL)
					http.Error(w, "refused", http.StatusMethodNotAllowed)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if mode == "cancel" {
					started <- struct{}{}
					<-r.Context().Done()
					closed <- struct{}{}
					return
				}
				if mode == "retry" || (mode == "retry-success" && count.Load() == 1) {
					w.Header().Set("Retry-After", "1")
					w.WriteHeader(http.StatusTooManyRequests)
					_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "Status", "reason": "TooManyRequests", "code": http.StatusTooManyRequests, "message": "PRIVATE-RETRY"})
					return
				}
				if mode == "denied" {
					w.WriteHeader(403)
					_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "Status", "reason": "Forbidden", "code": 403, "message": "PRIVATE-SERVER"})
					return
				}
				u := gatekeeperObject()
				if mode == "oversized-object" {
					u.Object["private"] = strings.Repeat("x", 1<<20)
				}
				if mode == "oversized-body" {
					u.Object["private"] = strings.Repeat("x", 9<<20)
				}
				_ = json.NewEncoder(w).Encode(u.Object)
			}))
			defer server.Close()
			client, err := dynamic.NewForConfig(budgetConfig(&rest.Config{Host: server.URL}))
			require.NoError(t, err)
			s := newSession("t", "h", client, false)
			defer s.Close()
			gatekeeperCatalog(s, "v1beta1", false, []string{"get"})
			if mode == "cancel" {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				done := make(chan error, 1)
				go func() { _, e := s.GatekeeperAudit(ctx, gatekeeperRef()); done <- e }()
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Fatal("selected GET not started")
				}
				cancel()
				select {
				case err = <-done:
					require.ErrorIs(t, err, context.Canceled)
				case <-time.After(time.Second):
					t.Fatal("cancelled observation retained")
				}
				select {
				case <-closed:
				case <-time.After(time.Second):
					t.Fatal("HTTP context not cancelled")
				}
				require.EqualValues(t, 1, count.Load())
				return
			}
			out, err := s.GatekeeperAudit(t.Context(), gatekeeperRef())
			if mode == "normal" {
				require.NoError(t, err)
				require.EqualValues(t, 2, count.Load())
				require.Equal(t, "7", out.ResourceVersion)
			} else {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "PRIVATE")
				require.Empty(t, out.Violations)
				if mode == "retry" || mode == "retry-success" {
					require.EqualValues(t, 2, count.Load())
				} else {
					require.EqualValues(t, 1, count.Load())
				}
			}
		})
	}
}
