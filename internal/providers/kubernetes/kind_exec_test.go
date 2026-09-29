package kubernetes

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/streams"
)

// kindTerm drives a terminal: input through a pipe, output collected,
// sizes pushed.
type kindTerm struct {
	inR   *io.PipeReader
	inW   *io.PipeWriter
	mu    sync.Mutex
	out   strings.Builder
	sizes chan provider.TermSize
	done  chan struct{}
}

func newKindTerm(initial provider.TermSize) *kindTerm {
	r, w := io.Pipe()
	k := &kindTerm{inR: r, inW: w, sizes: make(chan provider.TermSize, 8), done: make(chan struct{})}
	k.sizes <- initial
	return k
}

func (k *kindTerm) Write(p []byte) (int, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.out.Write(p)
}

func (k *kindTerm) Next() *provider.TermSize {
	select {
	case s := <-k.sizes:
		return &s
	case <-k.done:
		return nil
	}
}

func (k *kindTerm) output() string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.out.String()
}

func (k *kindTerm) terminal() provider.Terminal {
	return provider.Terminal{Stdin: k.inR, Stdout: k, Sizes: k}
}

func (k *kindTerm) waitFor(t *testing.T, re string) {
	t.Helper()
	rx := regexp.MustCompile(re)
	require.Eventually(t, func() bool { return rx.MatchString(k.output()) }, 30*time.Second, 50*time.Millisecond,
		"waiting for %q in %q", re, k.output())
}

func (k *kindTerm) run(ctx context.Context, t *testing.T, h provider.ExecHandle) chan execResult {
	t.Helper()
	res := make(chan execResult, 1)
	go func() {
		st, err := h.Run(ctx, k.terminal())
		close(k.done)
		res <- execResult{st, err}
	}()
	return res
}

type execResult struct {
	st  provider.ExitStatus
	err error
}

func waitExec(t *testing.T, res chan execResult) execResult {
	t.Helper()
	select {
	case r := <-res:
		return r
	case <-time.After(60 * time.Second):
		t.Fatal("the command did not end")
		return execResult{}
	}
}

// kindWebPod: the ref of a running pod of deployment web (nginx:alpine).
func kindWebPod(t *testing.T, s *session) core.Ref {
	t.Helper()
	l, err := s.dyn.Resource(podsKind.gvr).Namespace("ocular-demo").List(context.Background(), metav1.ListOptions{LabelSelector: "app=web"})
	require.NoError(t, err)
	for _, p := range l.Items {
		if runnable(&p) {
			return s.ref(podsKind, "ocular-demo", p.GetName(), string(p.GetUID()))
		}
	}
	t.Fatal("no running web pod")
	return core.Ref{}
}

func kindSession(t *testing.T) *session {
	t.Helper()
	p, target := kindProvider(t)
	sess, err := p.Open(context.Background(), target)
	require.NoError(t, err)
	t.Cleanup(sess.Close)
	return sess.(*session)
}

func TestKindExecRunsACommandOverBothTransports(t *testing.T) {
	s := kindSession(t)
	ref := kindWebPod(t, s)
	for name, factory := range map[string]func(*conn) func(*url.URL) (remotecommand.Executor, error){
		"websocket": func(c *conn) func(*url.URL) (remotecommand.Executor, error) { return c.wsExecutor },
		"spdy":      func(c *conn) func(*url.URL) (remotecommand.Executor, error) { return c.spdyExecutor },
		"fallback":  func(c *conn) func(*url.URL) (remotecommand.Executor, error) { return c.fallbackExecutor },
	} {
		t.Run(name, func(t *testing.T) {
			h, err := s.PrepareExec(context.Background(), ref, provider.ExecRequest{Command: []string{"sh", "-c", "echo hello-$((6*7)); sleep 0.5; stty size; exit 3"}})
			require.NoError(t, err)
			eh := h.(*execHandle)
			c := *eh.conn
			c.newExecutor = factory(&c)
			eh.conn = &c
			k := newKindTerm(provider.TermSize{Cols: 100, Rows: 30})
			r := waitExec(t, k.run(context.Background(), t, h))
			require.NoError(t, r.err)
			assert.Equal(t, provider.ExitStatus{Code: 3, Known: true}, r.st)
			assert.Contains(t, k.output(), "hello-42")
			assert.Contains(t, k.output(), "30 100", "the initial size is the TTY's")
		})
	}
}

func TestKindExecInteractiveShellFollowsResizeAndOutlivesTheSession(t *testing.T) {
	s := kindSession(t)
	ref := kindWebPod(t, s)
	h, err := s.PrepareExec(context.Background(), ref, provider.ExecRequest{})
	require.NoError(t, err)
	s.Close() // another target selected: the handle keeps working

	k := newKindTerm(provider.TermSize{Cols: 80, Rows: 24})
	res := k.run(context.Background(), t, h)
	_, _ = k.inW.Write([]byte("stty size\n"))
	k.waitFor(t, `24 80`)
	k.sizes <- provider.TermSize{Cols: 120, Rows: 40}
	time.Sleep(300 * time.Millisecond) // the resize travels on its own stream
	_, _ = k.inW.Write([]byte("stty size\n"))
	k.waitFor(t, `40 120`)
	_, _ = k.inW.Write([]byte("exit 5\n"))
	r := waitExec(t, res)
	require.NoError(t, r.err)
	assert.Equal(t, provider.ExitStatus{Code: 5, Known: true}, r.st)
}

// psOf lists the processes of ref's pod (by a separate exec).
func psOf(t *testing.T, s *session, ref core.Ref) string {
	t.Helper()
	ph, err := s.PrepareExec(context.Background(), ref, provider.ExecRequest{Command: []string{"ps", "-o", "pid,args"}})
	require.NoError(t, err)
	pk := newKindTerm(provider.TermSize{Cols: 200, Rows: 50})
	r := waitExec(t, pk.run(context.Background(), t, ph))
	require.NoError(t, r.err)
	return pk.output()
}

// Cancelling an exec alone leaves the shell and its child running on kind
// (containerd keeps exec'd processes after the client is gone): that is why
// the terminal bridge hangs up in-band (^C ^D) before cancelling.
func TestKindExecCancelAloneLeavesTheShell(t *testing.T) {
	s := kindSession(t)
	ref := kindWebPod(t, s)
	marker := "sleep 4241"
	h, err := s.PrepareExec(context.Background(), ref, provider.ExecRequest{})
	require.NoError(t, err)
	k := newKindTerm(provider.TermSize{Cols: 80, Rows: 24})
	ctx, cancel := context.WithCancel(context.Background())
	res := k.run(ctx, t, h)
	_, _ = k.inW.Write([]byte("echo ready; " + marker + "\n"))
	k.waitFor(t, `(?m)^ready`)
	cancel()
	r := waitExec(t, res)
	assert.ErrorIs(t, r.err, context.Canceled)
	assert.False(t, r.st.Known)
	time.Sleep(2 * time.Second)
	if !strings.Contains(psOf(t, s, ref), marker) {
		t.Log("this runtime ended the process by itself")
		return
	}
	// Clean up what the test left (its own shell and sleep; not pkill -f,
	// which would match this cleanup's command line too).
	kh, err := s.PrepareExec(context.Background(), ref, provider.ExecRequest{Command: []string{"sh", "-c",
		`for p in $(ps -o pid,args | awk '$2=="ash" || ($2=="sleep" && $3=="4241") {print $1}'); do kill -9 $p; done`}})
	require.NoError(t, err)
	_ = waitExec(t, newKindTerm(provider.TermSize{Cols: 80, Rows: 24}).run(context.Background(), t, kh))
}

// Closing the tab (the page's WebSocket) goes through the bridge: it hangs
// the shell up in-band, so neither the shell nor its foreground child is
// left in the container.
func TestKindTerminalClosingTheTabEndsTheShellAndItsChild(t *testing.T) {
	s := kindSession(t)
	ref := kindWebPod(t, s)
	marker := "sleep 4242"
	reg := streams.NewRegistry()
	defer reg.Close()
	h := streams.NewHandler(reg, streams.HandlerOptions{AllowOrigin: "wails://localhost"})
	srv := httptest.NewServer(h)
	defer srv.Close()

	eh, err := s.PrepareExec(context.Background(), ref, provider.ExecRequest{})
	require.NoError(t, err)
	id, err := reg.AddTerm(eh, provider.TermSize{Cols: 80, Rows: 24})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/"+h.Token()+"/term/"+id,
		&websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"wails://localhost"}}})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	require.NoError(t, err)
	defer func() { _ = c.CloseNow() }()
	require.NoError(t, c.Write(ctx, websocket.MessageBinary, []byte("echo rea''dy; "+marker+"\n")))
	var out strings.Builder
	for !regexp.MustCompile(`(?m)^ready`).MatchString(out.String()) {
		typ, data, err := c.Read(ctx)
		require.NoError(t, err, out.String())
		if typ == websocket.MessageBinary {
			out.Write(data)
		} else if strings.Contains(string(data), `"ack"`) {
			continue
		}
	}
	require.Contains(t, psOf(t, s, ref), marker)

	_ = c.Close(websocket.StatusNormalClosure, "tab closed")
	require.Eventually(t, func() bool { return reg.Count(streams.KindTerm) == 0 }, 10*time.Second, 50*time.Millisecond)
	ps := psOf(t, s, ref)
	assert.NotContains(t, ps, marker, "the foreground child")
	assert.NotRegexp(t, `(?m)^\s*\d+\s+(ash|bash|sh)\s*$`, ps, "the interactive shell")
}

func TestKindExecWithoutPermissionIsForbidden(t *testing.T) {
	rbac := os.Getenv("OCULAR_KIND_RBAC_DIR")
	if rbac == "" {
		t.Skip("OCULAR_KIND_RBAC_DIR not set (make test-kind)")
	}
	cfg, _ := filepath.Abs(filepath.Join(rbac, "viewer.kubeconfig"))
	p := NewWith(func(k string) string {
		if k == "KUBECONFIG" {
			return cfg
		}
		return ""
	}, t.TempDir())
	d, err := p.Discover(context.Background())
	require.NoError(t, err)
	sess, err := p.Open(context.Background(), d.Targets[0].ID)
	require.NoError(t, err)
	defer sess.Close()
	s := sess.(*session)
	ref := kindWebPod(t, s)
	h, err := s.PrepareExec(context.Background(), ref, provider.ExecRequest{})
	require.NoError(t, err, "reading the pod is allowed")
	r := waitExec(t, newKindTerm(provider.TermSize{Cols: 80, Rows: 24}).run(context.Background(), t, h))
	var pe *provider.Error
	require.ErrorAs(t, r.err, &pe)
	assert.Equal(t, provider.ClassForbidden, pe.Class, pe.Message)
	assert.Contains(t, pe.Message, "pods/exec")
}

func TestKindExecInAnImageWithoutAShellSaysSo(t *testing.T) {
	s := kindSession(t)
	u, err := s.dyn.Resource(podsKind.gvr).Namespace("ocular-demo").Get(context.Background(), "noshell", metav1.GetOptions{})
	require.NoError(t, err)
	ref := s.ref(podsKind, "ocular-demo", "noshell", string(u.GetUID()))
	h, err := s.PrepareExec(context.Background(), ref, provider.ExecRequest{})
	require.NoError(t, err)
	r := waitExec(t, newKindTerm(provider.TermSize{Cols: 80, Rows: 24}).run(context.Background(), t, h))
	var pe *provider.Error
	require.ErrorAs(t, r.err, &pe)
	assert.Equal(t, provider.ClassInvalid, pe.Class, pe.Message)
	assert.Contains(t, pe.Message, "no shell")
}

func TestKindExecInfoOfTheWebDeployment(t *testing.T) {
	s := kindSession(t)
	u, err := s.dyn.Resource(deploymentsKind.gvr).Namespace("ocular-demo").Get(context.Background(), "web", metav1.GetOptions{})
	require.NoError(t, err)
	info, err := s.ExecInfo(context.Background(), s.ref(deploymentsKind, "ocular-demo", "web", string(u.GetUID())))
	require.NoError(t, err)
	require.NotEmpty(t, info.Instances)
	assert.Equal(t, info.Instances[0].ID, info.DefaultInstance)
	for _, in := range info.Instances {
		assert.True(t, strings.HasPrefix(in.Title, "web-"), in.Title)
		assert.Equal(t, "nginx", in.DefaultChannel)
	}
}
