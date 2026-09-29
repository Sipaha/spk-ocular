package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/util/exec"
	streamhttp "k8s.io/streaming/pkg/httpstream"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

var podRef1 = core.Ref{Provider: ProviderID, Target: "ctx", Scope: "ns", Kind: "pods", Name: "p", UID: "uid-1"}

// fakeExecutor records the exec request and plays a scripted session.
type fakeExecutor struct {
	url  *url.URL
	play func(ctx context.Context, o remotecommand.StreamOptions) error
}

func (f *fakeExecutor) Stream(remotecommand.StreamOptions) error { panic("not used") }
func (f *fakeExecutor) StreamWithContext(ctx context.Context, o remotecommand.StreamOptions) error {
	return f.play(ctx, o)
}

func execSessionFor(t *testing.T, play func(ctx context.Context, o remotecommand.StreamOptions) error, objs ...*unstructured.Unstructured) (*session, *[]*url.URL) {
	t.Helper()
	var ro []kruntime.Object
	for _, o := range objs {
		ro = append(ro, o)
	}
	s := newSession("ctx", "h", groupClient(ro...), false)
	t.Cleanup(s.Close)
	var urls []*url.URL
	s.conn.newExecutor = func(u *url.URL) (remotecommand.Executor, error) {
		urls = append(urls, u)
		return &fakeExecutor{url: u, play: play}, nil
	}
	return s, &urls
}

type sizes []provider.TermSize

func (s *sizes) Next() *provider.TermSize {
	if len(*s) == 0 {
		return nil
	}
	v := (*s)[0]
	*s = (*s)[1:]
	return &v
}

func TestExecInfoOfAPod(t *testing.T) {
	p := pod("ns", "p", "uid-1", withLogContainers, func(o map[string]any) {
		st := o["status"].(map[string]any)
		st["initContainerStatuses"] = []any{
			map[string]any{"name": "migrate", "state": map[string]any{"terminated": map[string]any{"reason": "Completed"}}},
			map[string]any{"name": "mesh", "state": map[string]any{"running": map[string]any{}}},
		}
		st["containerStatuses"] = []any{
			map[string]any{"name": "app", "state": map[string]any{"running": map[string]any{}}},
			map[string]any{"name": "proxy", "state": map[string]any{"waiting": map[string]any{"reason": "CrashLoopBackOff"}}},
		}
	})
	s, _ := execSessionFor(t, nil, p)
	info, err := s.ExecInfo(context.Background(), podRef1)
	require.NoError(t, err)
	require.Len(t, info.Instances, 1)
	in := info.Instances[0]
	assert.Equal(t, "p/uid-1", info.DefaultInstance)
	assert.Equal(t, "proxy", in.DefaultChannel, "kubectl's default-container annotation")
	assert.Equal(t, []core.ExecChannel{
		{ID: "app", Title: "app", Running: true, State: "running"},
		{ID: "proxy", Title: "proxy", Running: false, State: "waiting: CrashLoopBackOff"},
		{ID: "mesh", Title: "mesh", Note: ctrSidecar, Running: true, State: "running"},
	}, in.Channels, "finished init and absent ephemeral containers are not offered")

	_, err = s.ExecInfo(context.Background(), core.Ref{Kind: "pods", Scope: "ns", Name: "p", UID: "other"})
	var pe *provider.Error
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, provider.ClassGone, pe.Class)
}

// webPod is a running pod of the web deployment's ReplicaSet rsUID.
func webPod(name, uid, rsUID string, sec int, mutate ...func(map[string]any)) *unstructured.Unstructured {
	p := memberOf(name, uid, rsUID, sec, "app")
	p.SetLabels(map[string]string{"app": "web"})
	for _, m := range mutate {
		m(p.Object)
	}
	return p
}

func notReady(o map[string]any) {
	o["status"].(map[string]any)["conditions"] = []any{map[string]any{"type": "Ready", "status": "False"}}
}

func TestExecInfoOfAWorkloadOffersItsRunningPodsReadyAndNewestFirst(t *testing.T) {
	deleting := webPod("deleting", "u-del", "rs1", 50, func(o map[string]any) {
		o["metadata"].(map[string]any)["deletionTimestamp"] = at(60).Format(time.RFC3339)
	})
	pending := webPod("pending", "u-pend", "rs1", 40, func(o map[string]any) { o["status"].(map[string]any)["phase"] = "Pending" })
	stranger := webPod("stranger", "u-str", "rs-other", 45) // same labels, another controller
	s, _ := execSessionFor(t, nil,
		deployment("web", "d1", "app"), replicaSet("web-rs", "rs1", "d1"), replicaSet("web-rs2", "rs2", "d1"),
		webPod("old-ready", "u-old", "rs1", 10), webPod("new-ready", "u-new", "rs2", 30),
		webPod("newest-not-ready", "u-nr", "rs2", 35, notReady), deleting, pending, stranger)
	info, err := s.ExecInfo(context.Background(), webRef())
	require.NoError(t, err)
	var titles []string
	for _, in := range info.Instances {
		titles = append(titles, in.Title)
	}
	// a Pending pod with a running container is offered, after running ones
	assert.Equal(t, []string{"new-ready", "old-ready", "newest-not-ready", "pending"}, titles)
	assert.Equal(t, "new-ready/u-new", info.DefaultInstance)
}

func TestAnExplicitInstanceMustBelongToTheWorkload(t *testing.T) {
	foreign := webPod("stranger", "u-str", "rs-x", 45)
	s, _ := execSessionFor(t, nil,
		deployment("web", "d1", "app"), replicaSet("web-rs", "rs1", "d1"), replicaSet("other-rs", "rs-x", "d-other"),
		webPod("mine", "u-mine", "rs1", 10), foreign)
	req := func(inst string) error {
		_, err := s.PrepareExec(context.Background(), webRef(), provider.ExecRequest{Instance: inst, Channel: "app"})
		return err
	}
	require.NoError(t, req("mine/u-mine"))
	var pe *provider.Error
	require.ErrorAs(t, req("stranger/u-str"), &pe, "a pod of another ReplicaSet")
	assert.Equal(t, provider.ClassGone, pe.Class)
	require.Error(t, req("mine/u-replaced"), "a stale choice")
	_, err := s.PrepareExec(context.Background(), core.Ref{Kind: "apps/deployments", Scope: "ns", Name: "missing", UID: "d9"}, provider.ExecRequest{Instance: "mine/u-mine", Channel: "app"})
	require.ErrorAs(t, err, &pe, "a workload that does not exist")
	assert.Equal(t, provider.ClassNotFound, pe.Class)
	_, err = s.PrepareExec(context.Background(), core.Ref{Kind: "apps/deployments", Scope: "ns", Name: "web", UID: "d-old"}, provider.ExecRequest{Instance: "mine/u-mine", Channel: "app"})
	require.ErrorAs(t, err, &pe, "a replaced workload")
	assert.Equal(t, provider.ClassGone, pe.Class)
}

func TestARunningInitContainerOfAPendingPodCanBeOpened(t *testing.T) {
	p := pod("ns", "p", "u", func(o map[string]any) {
		o["spec"].(map[string]any)["initContainers"] = []any{map[string]any{"name": "init"}, map[string]any{"name": "done"}}
		o["status"] = map[string]any{"phase": "Pending",
			"containerStatuses": []any{map[string]any{"name": "app", "state": map[string]any{"waiting": map[string]any{"reason": "PodInitializing"}}}},
			"initContainerStatuses": []any{
				map[string]any{"name": "init", "state": map[string]any{"running": map[string]any{}}},
				map[string]any{"name": "done", "state": map[string]any{"terminated": map[string]any{"reason": "Completed"}}},
			}}
	})
	chs := execChannels(p)
	require.Len(t, chs, 2, "the waiting app container and the running init; the finished init is not offered")
	assert.Equal(t, core.ExecChannel{ID: "app", Title: "app", State: "waiting: PodInitializing"}, chs[0])
	assert.Equal(t, core.ExecChannel{ID: "init", Title: "init", Note: "init", Running: true, State: "running"}, chs[1])
	s, _ := execSessionFor(t, nil, p)
	_, err := s.PrepareExec(context.Background(), core.Ref{Kind: "pods", Scope: "ns", Name: "p", UID: "u"}, provider.ExecRequest{Channel: "init"})
	require.NoError(t, err)
	_, err = s.PrepareExec(context.Background(), core.Ref{Kind: "pods", Scope: "ns", Name: "p", UID: "u"}, provider.ExecRequest{Channel: "app"})
	require.Error(t, err, "a container that is not running")
}

func TestPrepareExecPinsThePodAndChecksTheContainer(t *testing.T) {
	ctx := context.Background()
	p := pod("ns", "p", "uid-1", withLogContainers)
	s, _ := execSessionFor(t, nil, p)

	h, err := s.PrepareExec(ctx, podRef1, provider.ExecRequest{Channel: "app"})
	require.NoError(t, err)
	d := h.Describe()
	assert.Equal(t, "p", d.Instance)
	assert.Equal(t, "app", d.Channel)
	assert.Empty(t, d.Command, "the default shell is not shown as a command")
	assert.Equal(t, "cluster.invalid", d.Endpoint)
	assert.Equal(t, "h", d.ConfigHash)

	var pe *provider.Error
	_, err = s.PrepareExec(ctx, podRef1, provider.ExecRequest{}) // default "proxy" is not running
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, provider.ClassInvalid, pe.Class)
	assert.Contains(t, pe.Message, "proxy")
	_, err = s.PrepareExec(ctx, podRef1, provider.ExecRequest{Channel: "nope"})
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, provider.ClassInvalid, pe.Class)
	_, err = s.PrepareExec(ctx, podRef1, provider.ExecRequest{Instance: "other/uid-9", Channel: "app"})
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, provider.ClassInvalid, pe.Class, "a pod's only instance is itself")
}

func TestExecRunsTheCommandAndReportsTheExitCode(t *testing.T) {
	ctx := context.Background()
	var got remotecommand.StreamOptions
	var seen []remotecommand.TerminalSize
	s, urls := execSessionFor(t, func(_ context.Context, o remotecommand.StreamOptions) error {
		got = o
		for sz := o.TerminalSizeQueue.Next(); sz != nil; sz = o.TerminalSizeQueue.Next() {
			seen = append(seen, *sz)
		}
		in, _ := io.ReadAll(o.Stdin)
		_, _ = o.Stdout.Write(append([]byte("echo:"), in...))
		return exec.CodeExitError{Err: errors.New("command terminated with non-zero exit code"), Code: 3}
	}, pod("ns", "p", "uid-1"))
	h, err := s.PrepareExec(ctx, podRef1, provider.ExecRequest{Command: []string{"ls", "-la", "a b"}})
	require.NoError(t, err)
	s.Close() // the session goes away (another target selected): the handle does not care

	var out strings.Builder
	st, err := h.Run(ctx, provider.Terminal{Stdin: strings.NewReader("hi"), Stdout: &out, Sizes: &sizes{{Cols: 80, Rows: 24}, {Cols: 100, Rows: 40}}})
	require.NoError(t, err)
	assert.Equal(t, provider.ExitStatus{Code: 3, Known: true}, st)
	assert.Equal(t, "echo:hi", out.String())
	assert.True(t, got.Tty)
	assert.Equal(t, []remotecommand.TerminalSize{{Width: 80, Height: 24}, {Width: 100, Height: 40}}, seen)
	require.Len(t, *urls, 1)
	u := (*urls)[0]
	assert.Equal(t, "/api/v1/namespaces/ns/pods/p/exec", u.Path)
	assert.Equal(t, url.Values{"container": {"app"}, "command": {"ls", "-la", "a b"}, "stdin": {"true"}, "stdout": {"true"}, "tty": {"true"}}, u.Query())

	again, err := h.Again()
	require.NoError(t, err)
	assert.Equal(t, h.Describe(), again.Describe())
}

func TestExecRefusesAReplacedPod(t *testing.T) {
	ctx := context.Background()
	ran := false
	s, _ := execSessionFor(t, func(context.Context, remotecommand.StreamOptions) error { ran = true; return nil }, pod("ns", "p", "uid-1"))
	h, err := s.PrepareExec(ctx, podRef1, provider.ExecRequest{})
	require.NoError(t, err)
	client := s.conn.dyn
	require.NoError(t, client.Resource(podGVR).Namespace("ns").Delete(ctx, "p", metav1.DeleteOptions{}))
	_, err = client.Resource(podGVR).Namespace("ns").Create(ctx, pod("ns", "p", "uid-2"), metav1.CreateOptions{})
	require.NoError(t, err)

	_, err = h.Run(ctx, provider.Terminal{Stdin: strings.NewReader(""), Stdout: io.Discard, Sizes: &sizes{}})
	var pe *provider.Error
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, provider.ClassGone, pe.Class)
	assert.Contains(t, pe.Message, "replaced")
	assert.False(t, ran)

	require.NoError(t, client.Resource(podGVR).Namespace("ns").Delete(ctx, "p", metav1.DeleteOptions{}))
	_, err = h.Run(ctx, provider.Terminal{Stdin: strings.NewReader(""), Stdout: io.Discard, Sizes: &sizes{}})
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, provider.ClassGone, pe.Class)
	assert.Contains(t, pe.Message, "no longer exists")
}

func TestExecErrorsAreExplained(t *testing.T) {
	forbidden := apierrors.NewForbidden(schema.GroupResource{Resource: "pods/exec"}, "p", errors.New("user cannot create pods/exec"))
	for name, tc := range map[string]struct {
		err     error
		command []string
		class   provider.ErrorClass
		msg     string
	}{
		"forbidden": {err: forbidden, class: provider.ClassForbidden, msg: "pods/exec permission"},
		"no shell": {err: errors.New(`OCI runtime exec failed: exec: "sh": executable file not found in $PATH: unknown`),
			class: provider.ClassInvalid, msg: `"sh" was not found in the container's PATH: the image has no shell`},
		"no command": {err: errors.New(`exec: "top": executable file not found in $PATH`), command: []string{"top"},
			class: provider.ClassInvalid, msg: `"top" was not found in the container`},
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := execSessionFor(t, func(context.Context, remotecommand.StreamOptions) error { return tc.err }, pod("ns", "p", "uid-1"))
			h, err := s.PrepareExec(context.Background(), podRef1, provider.ExecRequest{Command: tc.command})
			require.NoError(t, err)
			st, err := h.Run(context.Background(), provider.Terminal{Stdin: strings.NewReader(""), Stdout: io.Discard, Sizes: &sizes{}})
			assert.False(t, st.Known)
			var pe *provider.Error
			require.ErrorAs(t, err, &pe)
			assert.Equal(t, tc.class, pe.Class)
			assert.Contains(t, pe.Message, tc.msg)
		})
	}
}

func TestPodURLKeepsTheServerPathPrefix(t *testing.T) {
	c := newConn(&rest.Config{Host: "https://rancher.example/k8s/clusters/c-1/"}, nil, "t", "t", "h")
	u, err := c.podURL("ns", "p q", "exec", url.Values{"command": {"sh"}})
	require.NoError(t, err)
	assert.Equal(t, "https://rancher.example/k8s/clusters/c-1/api/v1/namespaces/ns/pods/p%20q/exec?command=sh", u.String())
	assert.Equal(t, "rancher.example", c.endpoint)
	c = newConn(&rest.Config{Host: "10.0.0.1:6443"}, nil, "t", "t", "h")
	u, err = c.podURL("ns", "p", "exec", nil)
	require.NoError(t, err)
	assert.Equal(t, "https://10.0.0.1:6443/api/v1/namespaces/ns/pods/p/exec", u.String())
}

// A server that holds the handshake (TLS or the upgrade answer) keeps the
// WebSocket path "connecting" until the user cancels — there is no
// handshake timeout (client-go builds its dialer with none); cancelling
// ends it promptly and leaves no goroutines.
func TestExecHandshakeThatNeverAnswersEndsOnlyByCancel(t *testing.T) {
	tlsStall, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = tlsStall.Close() }()
	var heldMu sync.Mutex
	var held []net.Conn
	go func() {
		for {
			c, err := tlsStall.Accept()
			if err != nil {
				return
			}
			heldMu.Lock()
			held = append(held, c) // accept, never speak TLS
			heldMu.Unlock()
		}
	}()
	release := make(chan struct{})
	headerStall := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select { // never answer the upgrade
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer headerStall.Close()
	// Refuses the WebSocket upgrade (so the executor falls back) and holds
	// the SPDY one.
	spdyStall := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.Error(w, "no websockets here", http.StatusBadRequest)
			return
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer spdyStall.Close()
	// Refuses both, and withholds the SPDY refusal's body.
	bodyStall := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.Error(w, "no websockets here", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer bodyStall.Close()
	defer close(release)

	for name, host := range map[string]string{"tls": "https://" + tlsStall.Addr().String(), "headers": headerStall.URL, "spdy": spdyStall.URL, "refusal body": bodyStall.URL} {
		t.Run(name, func(t *testing.T) {
			before := runtime.NumGoroutine()
			cfg := &rest.Config{Host: host, TLSClientConfig: rest.TLSClientConfig{Insecure: true}}
			c := newConn(cfg, groupClient(pod("ns", "p", "uid-1")), "t", "t", "h")
			h := &execHandle{conn: c, ns: "ns", pod: "p", uid: "uid-1", container: "app", argv: defaultShell, shell: true}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				_, err := h.Run(ctx, provider.Terminal{Stdin: strings.NewReader(""), Stdout: io.Discard, Sizes: &sizes{}})
				done <- err
			}()
			select {
			case err := <-done:
				t.Fatalf("returned before cancel: %v", err)
			case <-time.After(3 * time.Second): // longer than any dial/TLS default would allow to look "done"
			}
			cancel()
			select {
			case err := <-done:
				assert.ErrorIs(t, err, context.Canceled)
			case <-time.After(5 * time.Second):
				t.Fatal("cancel did not end the handshake")
			}
			require.Eventually(t, func() bool { return runtime.NumGoroutine() <= before+3 }, 10*time.Second, 50*time.Millisecond,
				"goroutines left: %d (before %d)", runtime.NumGoroutine(), before)
		})
	}
	heldMu.Lock()
	for _, c := range held {
		_ = c.Close()
	}
	heldMu.Unlock()
}

func TestFallbackRecognisesClientGoUpgradeFailures(t *testing.T) {
	assert.True(t, shouldFallback(&streamhttp.UpgradeFailureError{Cause: errors.New("400")}), "the error client-go's websocket executor returns")
	assert.True(t, shouldFallback(fmt.Errorf("wrapped: %w", &streamhttp.UpgradeFailureError{Cause: errors.New("400")})))
	assert.True(t, shouldFallback(errors.New("proxy: unknown scheme: https")))
	assert.False(t, shouldFallback(errors.New("command terminated with non-zero exit code")))
	assert.False(t, shouldFallback(context.Canceled))
}
