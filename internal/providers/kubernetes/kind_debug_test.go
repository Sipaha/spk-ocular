package kubernetes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/streams"
)

// debugTarget runs pod "target" (nginx, container web) in the test
// namespace.
func (c *actionCluster) debugTarget() core.Ref {
	c.t.Helper()
	c.apply(`apiVersion: v1
kind: Pod
metadata: {name: target}
spec:
  terminationGracePeriodSeconds: 0
  containers:
  - {name: web, image: "nginx:1.27-alpine"}
`)
	c.kubectlNS("wait", "--for=condition=Ready", "pod/target", "--timeout=120s")
	return c.ref("pods", "target")
}

// debug adds a debugger as the UI does (the plan's defaults reviewed) and
// prepares its terminal.
func (c *actionCluster) debug(ref core.Ref, image string) (core.TerminalOpen, provider.ExecHandle) {
	c.t.Helper()
	p := core.ActionParams{}
	if image != "" {
		p.Text = &image
	}
	plan := c.prepare(ref, "debug", p)
	require.Nil(c.t, plan.Unavailable)
	res, err := c.sess.RunAction(context.Background(), provider.ActionRun{Ref: plan.Where.Ref, Action: "debug", Params: plan.Params, Expect: plan.Expect})
	require.NoError(c.t, err)
	require.NotNil(c.t, res.Terminal)
	to := *res.Terminal
	h, err := c.sess.PrepareExec(context.Background(), to.Ref, provider.ExecRequest{Instance: to.Instance, Channel: to.Channel, Attach: to.Attach})
	require.NoError(c.t, err)
	return to, h
}

func (c *actionCluster) debuggerState(name string) string {
	c.t.Helper()
	return c.jsonpath("pod", "target", `{.status.ephemeralContainerStatuses[?(@.name=="`+name+`")].state}`)
}

func TestKindDebugShellSeesTheTargetAndEndsWithItsExitCode(t *testing.T) {
	c := kindActionCluster(t)
	ref := c.debugTarget()
	plan := c.prepare(ref, "debug", core.ActionParams{})
	require.NotNil(t, plan.Params.Text)
	assert.Equal(t, "busybox:1.36", *plan.Params.Text)
	require.NotNil(t, plan.Params.Choice)
	assert.Equal(t, "web", *plan.Params.Choice)

	to, h := c.debug(ref, "")
	assert.True(t, to.Attach)
	assert.Contains(t, c.jsonpath("pod", "target", "{.spec.ephemeralContainers[*].name}"), to.Channel)
	term := newKindTerm(provider.TermSize{Cols: 100, Rows: 30})
	res := term.run(context.Background(), t, h)
	term.waitFor(t, `/ # `) // the prompt, without a key pressed
	_, err := term.inW.Write([]byte("ps\n"))
	require.NoError(t, err)
	term.waitFor(t, `nginx: master process`)
	_, err = term.inW.Write([]byte("exit 3\n"))
	require.NoError(t, err)
	r := waitExec(t, res)
	require.NoError(t, r.err)
	assert.Equal(t, provider.ExitStatus{Code: 3, Known: true}, r.st)
	assert.Contains(t, c.debuggerState(to.Channel), `"exitCode":3`)

	// Reconnecting to an ended debugger says so (it cannot restart).
	again, err := h.Again()
	require.NoError(t, err)
	r = waitExec(t, newKindTerm(provider.TermSize{Cols: 80, Rows: 24}).run(context.Background(), t, again))
	var pe *provider.Error
	require.ErrorAs(t, r.err, &pe)
	assert.Equal(t, provider.ClassGone, pe.Class)
	require.NotNil(t, pe.Why)
	assert.Equal(t, "kubernetes.debug.ended", pe.Why.Key)
}

// A closed tab ends the debugger through the terminal's hang-up (Ctrl-C,
// Ctrl-D) even while a command runs in it — a tty passes no end of input,
// closing the connection alone would leave it running in the pod.
func TestKindDebugClosedTabEndsTheDebugger(t *testing.T) {
	c := kindActionCluster(t)
	ref := c.debugTarget()
	to, h := c.debug(ref, "")
	reg := streams.NewRegistry()
	defer reg.Close()
	sh := streams.NewHandler(reg, streams.HandlerOptions{AllowOrigin: "wails://localhost"})
	srv := httptest.NewServer(sh)
	defer srv.Close()
	id, err := reg.AddTerm("term", h, provider.TermSize{Cols: 80, Rows: 24})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ws, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/"+sh.Token()+"/term/"+id,
		&websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"wails://localhost"}}})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	require.NoError(t, err)
	defer func() { _ = ws.CloseNow() }()
	var out strings.Builder
	read := func(re string) {
		for !regexp.MustCompile(re).MatchString(out.String()) {
			typ, data, err := ws.Read(ctx)
			require.NoError(t, err, out.String())
			if typ == websocket.MessageBinary {
				out.Write(data)
			}
		}
	}
	read(`/ # $`) // the prompt, without a key pressed
	require.NoError(t, ws.Write(ctx, websocket.MessageBinary, []byte("echo rea''dy; sleep 4242\n")))
	read(`(?m)^ready`)

	_ = ws.Close(websocket.StatusNormalClosure, "tab closed")
	require.Eventually(t, func() bool { return reg.Count(streams.KindTerm) == 0 }, 15*time.Second, 50*time.Millisecond)
	require.Eventually(t, func() bool { return strings.Contains(c.debuggerState(to.Channel), "terminated") },
		30*time.Second, 500*time.Millisecond, "the debugger ends: %s", c.debuggerState(to.Channel))
}

func TestKindDebugWithAnImageThatCannotBePulledSaysWhy(t *testing.T) {
	c := kindActionCluster(t)
	ref := c.debugTarget()
	_, h := c.debug(ref, "ocular.invalid/nope:1")
	r := waitExec(t, newKindTerm(provider.TermSize{Cols: 80, Rows: 24}).run(context.Background(), t, h))
	var pe *provider.Error
	require.ErrorAs(t, r.err, &pe)
	assert.Equal(t, provider.ClassUnavailable, pe.Class, pe.Message)
	require.NotNil(t, pe.Why)
	assert.Equal(t, "kubernetes.debug.cannotStart", pe.Why.Key)
	assert.Regexp(t, `ErrImagePull|ImagePullBackOff`, pe.Message)
}

func TestKindDebugWithoutTheRightIsForbidden(t *testing.T) {
	s := rbacSession(t, "viewer").(*session)
	c := &actionCluster{t: t, ns: "ocular-demo"}
	pod := c.kubectl("-n", "ocular-demo", "get", "pods", "-l", "app=web", "-o", "jsonpath={.items[0].metadata.name}")
	before := c.kubectl("-n", "ocular-demo", "get", "pod", pod, "-o", "jsonpath={.spec.ephemeralContainers[*].name}")
	ref := core.Ref{Provider: ProviderID, Target: s.target, Scope: "ocular-demo", Kind: "pods", Name: pod}
	plan, err := s.PrepareAction(context.Background(), ref, "debug", core.ActionParams{})
	require.NoError(t, err)
	_, err = s.RunAction(context.Background(), provider.ActionRun{Ref: plan.Where.Ref, Action: "debug", Params: plan.Params, Expect: plan.Expect})
	assertClass(t, err, provider.ClassForbidden)
	assert.Equal(t, before, c.kubectl("-n", "ocular-demo", "get", "pod", pod, "-o", "jsonpath={.spec.ephemeralContainers[*].name}"))
}
