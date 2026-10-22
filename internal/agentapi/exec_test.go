package agentapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/agentgrant"
	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func execRequest() PrepareExecRequest {
	r := ref("a", "pods", "web-1")
	return PrepareExecRequest{Ref: r, Instance: r.Name + "/" + r.UID, Channel: "main", Command: []string{"printf", "argument; $(not-a-shell)"}}
}
func allowExec(e *env, noConfirm bool) {
	g := one("a", agentgrant.VerbExec)
	g.NoConfirm = noConfirm
	e.grant(g)
}
func execResult(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	require.Equal(t, "done", out["state"])
	r, ok := out["exec"].(map[string]any)
	require.True(t, ok, "%v", out)
	return r
}

func TestAgentExecSeparatePermissionAndSelection(t *testing.T) {
	e := newEnv(t)
	req := execRequest()
	e.grant(one("a", agentgrant.VerbRead), one("a", agentgrant.VerbLogs), one("a", "action:restart", "pods"))
	e.refused("GetExecInfo", GetExecInfoRequest{Ref: req.Ref})
	e.refused("PrepareExec", req)
	allowExec(e, false)
	info := e.ok("GetExecInfo", GetExecInfoRequest{Ref: req.Ref})
	require.Len(t, info["instances"], 1)
	e.refused("GetObject", map[string]any{"ref": req.Ref})
	e.refused("GetExecInfo", GetExecInfoRequest{Ref: ref("b", "pods", "api-1")})
	for _, change := range []func(*PrepareExecRequest){func(r *PrepareExecRequest) { r.Instance = "" }, func(r *PrepareExecRequest) { r.Instance = "api-1/uid-api-1" }, func(r *PrepareExecRequest) { r.Channel = "" }, func(r *PrepareExecRequest) { r.Ref.UID = "" }, func(r *PrepareExecRequest) { r.Command = nil }, func(r *PrepareExecRequest) { r.Command = []string{"sh\x00"} }, func(r *PrepareExecRequest) { r.Command = []string{strings.Repeat("x", 16385)} }, func(r *PrepareExecRequest) { r.Stdin = strings.Repeat("x", 65537) }, func(r *PrepareExecRequest) { r.MaxSeconds = 1801 }, func(r *PrepareExecRequest) { r.MaxBytes = 4095 }} {
		r := execRequest()
		change(&r)
		code, _ := e.call("PrepareExec", r)
		assert.Equal(t, http.StatusBadRequest, code)
	}
	e.f.mu.Lock()
	assert.Zero(t, e.f.execStarted)
	e.f.mu.Unlock()
	require.NoError(t, e.srv.SaveGrants(context.Background(), api.SaveAgentGrantsRequest{Provider: "k", Target: "t", Grants: []agentgrant.Grant{one("a", agentgrant.VerbExec)}}))
}

func TestAgentExecConfirmOnceBinaryOutputAndNoJournalPayload(t *testing.T) {
	e := newEnv(t)
	allowExec(e, false)
	req := execRequest()
	req.Stdin = "private-input"
	e.f.execRun = func(_ context.Context, r provider.ExecRequest, term provider.Terminal) (provider.ExitStatus, error) {
		assert.True(t, term.Raw)
		assert.Equal(t, req.Command, r.Command)
		b, err := io.ReadAll(term.Stdin)
		require.NoError(t, err)
		assert.Equal(t, req.Stdin, string(b))
		_, _ = term.Stdout.Write([]byte("secret-output\x00\xff"))
		_, _ = term.Stderr.Write([]byte("private-stderr"))
		return provider.ExitStatus{Known: true, Code: 7}, nil
	}
	prep := e.ok("PrepareExec", req)
	assert.Equal(t, "user", prep["confirmation"])
	e.f.mu.Lock()
	assert.Zero(t, e.f.execStarted)
	e.f.mu.Unlock()
	run := e.ok("RunExec", RunRequest{PlanID: prep["planId"].(string)})
	assert.Equal(t, stateAwaiting, run["state"])
	pending, err := e.srv.Pending(context.Background())
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, req.Command, pending[0].Exec.Command)
	assert.Equal(t, req.Stdin, pending[0].Exec.Stdin)
	require.NoError(t, e.srv.Decide(context.Background(), api.DecideAgentPendingRequest{ID: pending[0].ID, Approve: true}))
	var out map[string]any
	require.Eventually(t, func() bool {
		out = e.ok("GetRun", GetRunRequest{RunID: pending[0].ID})
		return out["state"] == stateDone
	}, time.Second*3, time.Millisecond*10)
	result := execResult(t, out)
	assert.Equal(t, float64(7), result["exitCode"])
	assert.Equal(t, true, result["complete"])
	b, err := base64.StdEncoding.DecodeString(result["stdoutBase64"].(string))
	require.NoError(t, err)
	assert.Equal(t, []byte("secret-output\x00\xff"), b)
	code, _ := e.call("RunExec", RunRequest{PlanID: prep["planId"].(string)})
	assert.Equal(t, http.StatusNotFound, code)
	entries, err := e.st.ListAudit(context.Background(), store.AuditFilter{})
	require.NoError(t, err)
	data, err := json.Marshal(entries)
	require.NoError(t, err)
	for _, secret := range []string{req.Command[0], req.Command[1], req.Stdin, "secret-output", "private-stderr"} {
		assert.NotContains(t, string(data), secret)
	}
	e.grant(one("a", agentgrant.VerbRead))
	e.refused("GetRun", GetRunRequest{RunID: pending[0].ID})
}

func TestAgentExecFreshGrantsIdentityAndConfig(t *testing.T) {
	for _, test := range []string{"revoke", "disable", "identity", "config"} {
		t.Run(test, func(t *testing.T) {
			e := newEnv(t)
			allowExec(e, true)
			prep := e.ok("PrepareExec", execRequest())
			switch test {
			case "revoke":
				e.grant(one("a", agentgrant.VerbRead))
			case "disable":
				require.NoError(t, e.srv.SaveGrants(context.Background(), api.SaveAgentGrantsRequest{Provider: "k", Target: "t", Grants: []agentgrant.Grant{one("a", agentgrant.VerbExec, "pods")}, DisabledScopes: []agentgrant.Scope{{Mode: agentgrant.ScopeOne, Name: "a"}}}))
			case "identity":
				e.f.setIdentity("different-target")
			case "config":
				e.f.mu.Lock()
				e.f.hash = "h2"
				e.f.mu.Unlock()
				call, err := e.svc.AgentCall(context.Background(), "k", "t")
				require.NoError(t, err)
				call.Done()
			}
			code, _ := e.call("RunExec", RunRequest{PlanID: prep["planId"].(string)})
			assert.NotEqual(t, http.StatusOK, code)
			e.f.mu.Lock()
			assert.Zero(t, e.f.execStarted)
			e.f.mu.Unlock()
		})
	}
}

func TestAgentExecBudgetsAndCancellation(t *testing.T) {
	for _, test := range []string{"bytes", "timeout", "revoke", "config", "disconnect", "unknown", "transport"} {
		t.Run(test, func(t *testing.T) {
			e := newEnv(t)
			allowExec(e, true)
			req := execRequest()
			req.MaxSeconds = 1
			req.MaxBytes = 4096
			started := make(chan struct{})
			e.f.execRun = func(ctx context.Context, _ provider.ExecRequest, term provider.Terminal) (provider.ExitStatus, error) {
				close(started)
				switch test {
				case "bytes":
					_, err := term.Stdout.Write([]byte(strings.Repeat("x", 3000)))
					require.NoError(t, err)
					_, err = term.Stderr.Write([]byte(strings.Repeat("y", 3000)))
					return provider.ExitStatus{}, err
				case "unknown":
					return provider.ExitStatus{}, nil
				case "transport":
					return provider.ExitStatus{}, &provider.Error{Class: provider.ClassUnavailable, Message: "dropped"}
				}
				_, err := term.Stdout.Write([]byte("sensitive-before-cancel"))
				require.NoError(t, err)
				<-ctx.Done()
				return provider.ExitStatus{}, ctx.Err()
			}
			prep := e.ok("PrepareExec", req)
			b, _ := json.Marshal(RunRequest{PlanID: prep["planId"].(string)})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			request := httptest.NewRequest("POST", "/v1/RunExec", strings.NewReader(string(b))).WithContext(ctx)
			response := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { testOperationHandler(e.srv, "RunExec").ServeHTTP(response, request); close(done) }()
			<-started
			switch test {
			case "revoke":
				e.grant(one("a", agentgrant.VerbRead))
			case "config":
				e.f.mu.Lock()
				e.f.hash = "h2"
				e.f.mu.Unlock()
				call, err := e.svc.AgentCall(context.Background(), "k", "t")
				require.NoError(t, err)
				call.Done()
			case "disconnect":
				cancel()
			}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("exec did not cancel")
			}
			var out map[string]any
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &out))
			result := execResult(t, out)
			want := map[string]string{"bytes": "byte_limit", "timeout": "time_limit", "revoke": "cancelled", "config": "connection_changed", "disconnect": "cancelled", "unknown": "unknown", "transport": "error"}[test]
			assert.Equal(t, want, result["stopReason"])
			assert.Equal(t, false, result["complete"])
			assert.Equal(t, false, result["exitKnown"])
			assert.NotContains(t, result, "exitCode")
			if test == "revoke" || test == "config" || test == "disconnect" {
				assert.Equal(t, "", result["stdoutBase64"], "cancelled admission must discard buffered output")
			}
			if test == "timeout" {
				output, err := base64.StdEncoding.DecodeString(result["stdoutBase64"].(string))
				require.NoError(t, err)
				assert.Equal(t, "sensitive-before-cancel", string(output), "a permitted timeout may return explicitly incomplete output")
			}
			if test == "bytes" {
				outBytes, _ := base64.StdEncoding.DecodeString(result["stdoutBase64"].(string))
				errBytes, _ := base64.StdEncoding.DecodeString(result["stderrBase64"].(string))
				assert.Len(t, append(outBytes, errBytes...), 4096)
				assert.Equal(t, true, result["truncated"])
			}
		})
	}
}

func TestAgentExecAdmissionAndPendingRecheck(t *testing.T) {
	e := newEnv(t)
	allowExec(e, false)
	prep := e.ok("PrepareExec", execRequest())
	run := e.ok("RunExec", RunRequest{PlanID: prep["planId"].(string)})
	e.grant(one("a", agentgrant.VerbRead))
	require.NoError(t, e.srv.Decide(context.Background(), api.DecideAgentPendingRequest{ID: run["runId"].(string), Approve: true}))
	require.Eventually(t, func() bool {
		e.srv.pend.mu.Lock()
		defer e.srv.pend.mu.Unlock()
		r := e.srv.pend.runs[run["runId"].(string)]
		return r.view.State == stateFailed && r.view.Error != nil && r.view.Error.Code == "forbidden"
	}, time.Second*3, time.Millisecond*10)
	e.f.mu.Lock()
	assert.Zero(t, e.f.execStarted)
	e.f.mu.Unlock()
	allowExec(e, true)
	var dones []func()
	for i := 0; i < maxExecs; i++ {
		_, done, err := e.srv.beginExec(context.Background(), execRequest().Ref)
		require.NoError(t, err)
		dones = append(dones, done)
	}
	_, _, err := e.srv.beginExec(context.Background(), execRequest().Ref)
	require.Error(t, err)
	assert.Equal(t, api.CodeLimit, codeOf(err))
	for _, done := range dones {
		done()
	}
	ctx, done, err := e.srv.beginExec(context.Background(), execRequest().Ref)
	require.NoError(t, err)
	defer done()
	e.grant(one("a", agentgrant.VerbRead))
	assert.Error(t, context.Cause(ctx))
	// Registration precedes admission: a revocation between registration and
	// open cannot authorize the command using a previously captured grant.
	_, err = e.srv.openExec(ctx, caller{agent: "test"}, "RunExec", execRequest().Ref)
	require.Error(t, err)
}

func TestAgentExecPlanExpiryAndRetainedResultBound(t *testing.T) {
	e := newEnv(t)
	allowExec(e, true)
	prep := e.ok("PrepareExec", execRequest())
	e.srv.plans.now = func() time.Time { return time.Now().Add(planTTL + time.Second) }
	code, _ := e.call("RunExec", RunRequest{PlanID: prep["planId"].(string)})
	assert.Equal(t, http.StatusNotFound, code)
	e.srv.plans.now = time.Now
	allowExec(e, false)
	for i := 0; i < maxPending; i++ {
		prep := e.ok("PrepareExec", execRequest())
		run := e.ok("RunExec", RunRequest{PlanID: prep["planId"].(string)})
		require.NoError(t, e.srv.Decide(context.Background(), api.DecideAgentPendingRequest{ID: run["runId"].(string), Approve: false}))
	}
	prep = e.ok("PrepareExec", execRequest())
	code, _ = e.call("RunExec", RunRequest{PlanID: prep["planId"].(string)})
	assert.Equal(t, http.StatusTooManyRequests, code)
	e.srv.now = func() time.Time { return time.Now().Add(keptTTL + time.Second) }
	prep = e.ok("PrepareExec", execRequest())
	run := e.ok("RunExec", RunRequest{PlanID: prep["planId"].(string)})
	assert.Equal(t, stateAwaiting, run["state"])
}

func TestAgentExecMarksItsProviderRequestAsAgentOwned(t *testing.T) {
	e := newEnv(t)
	allowExec(e, true)
	seen := make(chan bool, 1)
	e.f.execRun = func(_ context.Context, req provider.ExecRequest, _ provider.Terminal) (provider.ExitStatus, error) {
		seen <- req.Agent
		return provider.ExitStatus{Known: true}, nil
	}
	prep := e.ok("PrepareExec", execRequest())
	e.ok("RunExec", RunRequest{PlanID: prep["planId"].(string)})
	assert.True(t, <-seen, "the UI cannot choose this internal marker and broad grants cannot bypass provider helper exclusions")
}

func TestAgentExecDurationBoundsAndEffectiveDeadline(t *testing.T) {
	for _, seconds := range []int{-1, 1801} {
		req := execRequest()
		req.MaxSeconds = seconds
		require.Error(t, validateExec(&req))
	}
	for _, seconds := range []int{1, 121, 300, 1800} {
		req := execRequest()
		req.MaxSeconds = seconds
		require.NoError(t, validateExec(&req))
		require.Equal(t, seconds, req.MaxSeconds)
	}
	require.Equal(t, 2*time.Minute, runTimeout, "ordinary action/edit limit stays unchanged")
	for _, confirm := range []bool{false, true} {
		for _, seconds := range []int{0, 1800} {
			t.Run(fmt.Sprintf("confirm=%v/seconds=%d", confirm, seconds), func(t *testing.T) {
				e := newEnv(t)
				allowExec(e, !confirm)
				req := execRequest()
				req.MaxSeconds = seconds
				want := seconds
				if want == 0 {
					want = 300
				}
				deadlines := make(chan time.Duration, 1)
				e.f.execRun = func(ctx context.Context, _ provider.ExecRequest, _ provider.Terminal) (provider.ExitStatus, error) {
					deadline, ok := ctx.Deadline()
					if !ok {
						deadlines <- 0
					} else {
						deadlines <- time.Until(deadline)
					}
					return provider.ExitStatus{Known: true}, nil
				}
				prep := e.ok("PrepareExec", req)
				require.Equal(t, float64(want), prep["plan"].(map[string]any)["maxSeconds"])
				out := e.ok("RunExec", RunRequest{PlanID: prep["planId"].(string)})
				if confirm {
					require.Equal(t, stateAwaiting, out["state"])
					id := out["runId"].(string)
					require.NoError(t, e.srv.Decide(t.Context(), api.DecideAgentPendingRequest{ID: id, Approve: true}))
					require.Eventually(t, func() bool { out = e.ok("GetRun", GetRunRequest{RunID: id}); return out["state"] == stateDone }, 3*time.Second, 10*time.Millisecond)
				}
				result := execResult(t, out)
				require.Equal(t, true, result["complete"])
				remaining := <-deadlines
				require.Greater(t, remaining, time.Duration(want-30)*time.Second, "exec must not inherit the two-minute write cap")
				require.LessOrEqual(t, remaining, time.Duration(want)*time.Second)
			})
		}
	}
}

func TestConfirmedAgentExecSurvivesConfirmationWaitExpiryWhileRunning(t *testing.T) {
	e := newEnv(t)
	allowExec(e, false)
	var advance atomic.Int64
	e.srv.now = func() time.Time { return time.Now().Add(time.Duration(advance.Load())) }
	started, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	e.f.execRun = func(ctx context.Context, _ provider.ExecRequest, _ provider.Terminal) (provider.ExitStatus, error) {
		close(started)
		select {
		case <-release:
			return provider.ExitStatus{Known: true}, nil
		case <-ctx.Done():
			return provider.ExitStatus{}, ctx.Err()
		}
	}
	req := execRequest()
	req.MaxSeconds = 1800
	prep := e.ok("PrepareExec", req)
	run := e.ok("RunExec", RunRequest{PlanID: prep["planId"].(string)})
	id := run["runId"].(string)
	require.NoError(t, e.srv.Decide(t.Context(), api.DecideAgentPendingRequest{ID: id, Approve: true}))
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("confirmed exec did not start")
	}
	advance.Store(int64(11 * time.Minute))
	e.srv.pend.mu.Lock()
	e.srv.pend.sweepLocked()
	pending := e.srv.pend.runs[id]
	if assert.NotNil(t, pending) {
		assert.Equal(t, stateRunning, pending.view.State)
	}
	e.srv.pend.mu.Unlock()
	close(release)
	out := e.ok("GetRun", GetRunRequest{RunID: id})
	result := execResult(t, out)
	require.Equal(t, true, result["complete"])
}
