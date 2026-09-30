package kubernetes

import (
	"context"
	"errors"
	"io"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// withDebugger adds ephemeral container "debugger" in state st (nil: no
// status yet).
func withDebugger(st map[string]any) func(map[string]any) {
	return func(o map[string]any) {
		o["spec"].(map[string]any)["ephemeralContainers"] = []any{map[string]any{"name": "debugger", "image": "busybox:1.36", "stdin": true, "stdinOnce": true, "tty": true}}
		if st != nil {
			o["status"].(map[string]any)["ephemeralContainerStatuses"] = []any{map[string]any{"name": "debugger", "state": st}}
		}
	}
}

func waitingState(reason, message string) map[string]any {
	return map[string]any{"waiting": map[string]any{"reason": reason, "message": message}}
}

func terminatedState(code int64) map[string]any {
	return map[string]any{"terminated": map[string]any{"exitCode": code, "reason": "Completed"}}
}

var runningState = map[string]any{"running": map[string]any{"startedAt": "2026-10-01T10:00:00Z"}}

// setDebugger sets the debugger's state in the stored pod.
func setDebugger(t *testing.T, s *session, st map[string]any) {
	t.Helper()
	ctx := context.Background()
	pods := s.conn.dyn.Resource(podGVR).Namespace("ns")
	u, err := pods.Get(ctx, "p", metav1.GetOptions{})
	require.NoError(t, err)
	withDebugger(st)(u.Object)
	_, err = pods.Update(ctx, u, metav1.UpdateOptions{})
	require.NoError(t, err)
}

// watchStarts signals each watch of pods the handle starts.
func watchStarts(s *session) <-chan struct{} {
	started := make(chan struct{}, 16)
	s.conn.dyn.(*dynamicfake.FakeDynamicClient).PrependWatchReactor("pods", func(clienttesting.Action) (bool, watch.Interface, error) {
		started <- struct{}{}
		return false, nil, nil
	})
	return started
}

func awaitWatch(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("no watch of the pod started")
	}
}

type notices struct {
	mu   sync.Mutex
	list []core.Message
}

func (n *notices) add(m core.Message) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.list = append(n.list, m)
}

func (n *notices) keys() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return msgKeys(n.list)
}

func attachTerm(in string, out io.Writer, n *notices) provider.Terminal {
	t := provider.Terminal{Stdin: strings.NewReader(in), Stdout: out, Sizes: &sizes{{Cols: 80, Rows: 24}}}
	if n != nil {
		t.Notice = n.add
	}
	return t
}

func TestAttachIsPreparedOnlyForADebugContainer(t *testing.T) {
	ctx := context.Background()
	s, _ := execSessionFor(t, nil, pod("ns", "p", "uid-1", withDebugger(nil)))

	h, err := s.PrepareExec(ctx, podRef1, provider.ExecRequest{Channel: "debugger", Attach: true})
	require.NoError(t, err, "a debugger not started yet can be attached to (Run waits for it)")
	assert.Equal(t, "debugger", h.Describe().Channel)
	assert.Empty(t, h.Describe().Command)

	var pe *provider.Error
	for name, req := range map[string]provider.ExecRequest{
		"a regular container": {Channel: "app", Attach: true},
		"the default one":     {Attach: true},
		"no such container":   {Channel: "nope", Attach: true},
		"with a command":      {Channel: "debugger", Attach: true, Command: []string{"sh"}},
	} {
		_, err := s.PrepareExec(ctx, podRef1, req)
		require.ErrorAs(t, err, &pe, name)
		assert.Equal(t, provider.ClassInvalid, pe.Class, name)
	}
	_, err = s.PrepareExec(ctx, podRef1, provider.ExecRequest{Channel: "debugger"})
	require.ErrorAs(t, err, &pe, "exec into a debugger that is not running is still refused")
}

// A debugger still starting: Run waits by watching the pod (saying why),
// attaches — no command — once it runs, sends one newline so the prompt
// shows, and takes the exit code from the container's status.
func TestAttachWaitsForTheDebuggerThenShowsThePromptAndItsExitCode(t *testing.T) {
	ctx := context.Background()
	var s *session
	var stdin []byte
	s, urls := execSessionFor(t, func(_ context.Context, o remotecommand.StreamOptions) error {
		assert.True(t, o.Tty)
		stdin, _ = io.ReadAll(o.Stdin)
		_, _ = o.Stdout.Write([]byte("/ # "))
		setDebugger(t, s, terminatedState(3)) // the shell exits: the attach ends
		return nil
	}, pod("ns", "p", "uid-1", withDebugger(waitingState("ContainerCreating", ""))))
	started := watchStarts(s)
	h, err := s.PrepareExec(ctx, podRef1, provider.ExecRequest{Channel: "debugger", Attach: true})
	require.NoError(t, err)

	var n notices
	var out strings.Builder
	type result struct {
		st  provider.ExitStatus
		err error
	}
	done := make(chan result, 1)
	go func() {
		st, err := h.Run(ctx, attachTerm("ls\n", &out, &n))
		done <- result{st, err}
	}()
	awaitWatch(t, started)
	setDebugger(t, s, waitingState("PodInitializing", ""))
	require.Eventually(t, func() bool { return len(n.keys()) == 2 }, 5*time.Second, 10*time.Millisecond)
	assert.Empty(t, *urls, "nothing attaches before the debugger runs")
	setDebugger(t, s, runningState)

	var r result
	select {
	case r = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not end")
	}
	require.NoError(t, r.err)
	assert.Equal(t, provider.ExitStatus{Code: 3, Known: true}, r.st)
	assert.Equal(t, "\nls\n", string(stdin), "one newline first: the shell printed its prompt before the attach")
	assert.Equal(t, "/ # ", out.String())
	n.mu.Lock()
	assert.Equal(t, []string{"kubernetes.debug.starting", "kubernetes.debug.starting"}, msgKeys(n.list))
	assert.Equal(t, "ContainerCreating", n.list[0].Params["reason"])
	assert.Equal(t, "PodInitializing", n.list[1].Params["reason"])
	n.mu.Unlock()
	require.Len(t, *urls, 1)
	u := (*urls)[0]
	assert.Equal(t, "/api/v1/namespaces/ns/pods/p/attach", u.Path)
	assert.Equal(t, url.Values{"container": {"debugger"}, "stdin": {"true"}, "stdout": {"true"}, "tty": {"true"}}, u.Query())
}

func TestAttachFailsWhenTheDebuggerCannotRun(t *testing.T) {
	for name, tc := range map[string]struct {
		state map[string]any
		pod   func(map[string]any)
		class provider.ErrorClass
		key   string
		text  string
	}{
		"image not pulled": {state: waitingState("ErrImagePull", `pull "nope:1": not found`), class: provider.ClassUnavailable,
			key: "kubernetes.debug.cannotStart", text: `ErrImagePull: pull "nope:1": not found`},
		"pull backoff": {state: waitingState("ImagePullBackOff", "Back-off pulling image"), class: provider.ClassUnavailable,
			key: "kubernetes.debug.cannotStart", text: "ImagePullBackOff"},
		"bad image": {state: waitingState("InvalidImageName", ""), class: provider.ClassUnavailable, key: "kubernetes.debug.cannotStart"},
		"ended":     {state: terminatedState(0), class: provider.ClassGone, key: "kubernetes.debug.ended"},
		"pod done": {state: waitingState("ContainerCreating", ""), pod: func(o map[string]any) { o["status"].(map[string]any)["phase"] = "Failed" },
			class: provider.ClassGone, key: "kubernetes.debug.podEnded"},
	} {
		t.Run(name, func(t *testing.T) {
			ran := false
			mut := []func(map[string]any){withDebugger(nil)}
			s, _ := execSessionFor(t, func(context.Context, remotecommand.StreamOptions) error { ran = true; return nil }, pod("ns", "p", "uid-1", mut...))
			s.conn.startWait = 2 * time.Second // a missed failure says slowStart, not a hang
			h, err := s.PrepareExec(context.Background(), podRef1, provider.ExecRequest{Channel: "debugger", Attach: true})
			require.NoError(t, err)
			setDebugger(t, s, tc.state)
			if tc.pod != nil {
				pods := s.conn.dyn.Resource(podGVR).Namespace("ns")
				u, err := pods.Get(context.Background(), "p", metav1.GetOptions{})
				require.NoError(t, err)
				tc.pod(u.Object)
				_, err = pods.Update(context.Background(), u, metav1.UpdateOptions{})
				require.NoError(t, err)
			}
			st, err := h.Run(context.Background(), attachTerm("", io.Discard, nil))
			assert.False(t, st.Known)
			var pe *provider.Error
			require.ErrorAs(t, err, &pe)
			assert.Equal(t, tc.class, pe.Class)
			require.NotNil(t, pe.Why)
			assert.Equal(t, tc.key, pe.Why.Key)
			assert.Contains(t, pe.Message, tc.text)
			assert.False(t, ran)
		})
	}
}

func TestAttachStopsWaitingAtTheDeadline(t *testing.T) {
	s, urls := execSessionFor(t, nil, pod("ns", "p", "uid-1", withDebugger(waitingState("ContainerCreating", ""))))
	s.conn.startWait = 50 * time.Millisecond
	h, err := s.PrepareExec(context.Background(), podRef1, provider.ExecRequest{Channel: "debugger", Attach: true})
	require.NoError(t, err)
	_, err = h.Run(context.Background(), attachTerm("", io.Discard, nil))
	var pe *provider.Error
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, provider.ClassUnavailable, pe.Class)
	require.NotNil(t, pe.Why)
	assert.Equal(t, "kubernetes.debug.slowStart", pe.Why.Key)
	assert.Empty(t, *urls)
}

func TestAttachWaitEndsWithThePodOrTheCaller(t *testing.T) {
	ctx := context.Background()
	s, _ := execSessionFor(t, nil, pod("ns", "p", "uid-1", withDebugger(nil)))
	started := watchStarts(s)
	h, err := s.PrepareExec(ctx, podRef1, provider.ExecRequest{Channel: "debugger", Attach: true})
	require.NoError(t, err)
	errs := make(chan error, 1)
	go func() {
		_, err := h.Run(ctx, attachTerm("", io.Discard, nil))
		errs <- err
	}()
	awaitWatch(t, started)
	require.NoError(t, s.conn.dyn.Resource(podGVR).Namespace("ns").Delete(ctx, "p", metav1.DeleteOptions{}))
	var pe *provider.Error
	select {
	case err := <-errs:
		require.ErrorAs(t, err, &pe)
		assert.Equal(t, provider.ClassGone, pe.Class)
	case <-time.After(5 * time.Second):
		t.Fatal("the deleted pod did not end the wait")
	}

	s2, _ := execSessionFor(t, nil, pod("ns", "p", "uid-1", withDebugger(nil)))
	started = watchStarts(s2)
	h, err = s2.PrepareExec(ctx, podRef1, provider.ExecRequest{Channel: "debugger", Attach: true})
	require.NoError(t, err)
	cctx, cancel := context.WithCancel(ctx)
	go func() {
		_, err := h.Run(cctx, attachTerm("", io.Discard, nil))
		errs <- err
	}()
	awaitWatch(t, started)
	cancel()
	select {
	case err := <-errs:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not end the wait")
	}
}

// The attach ended but the debugger still runs (a lost connection): its end
// is not known; reconnecting attaches again (a second shell would leave
// the first one waiting).
func TestAttachEndWithoutTheDebuggerEndingIsUnknownAndReconnectAttaches(t *testing.T) {
	s, urls := execSessionFor(t, func(context.Context, remotecommand.StreamOptions) error { return nil },
		pod("ns", "p", "uid-1", withDebugger(runningState)))
	s.conn.exitWait = 50 * time.Millisecond
	h, err := s.PrepareExec(context.Background(), podRef1, provider.ExecRequest{Channel: "debugger", Attach: true})
	require.NoError(t, err)
	st, err := h.Run(context.Background(), attachTerm("", io.Discard, nil))
	require.NoError(t, err)
	assert.False(t, st.Known)

	again, err := h.Again()
	require.NoError(t, err)
	_, err = again.Run(context.Background(), attachTerm("", io.Discard, nil))
	require.NoError(t, err)
	require.Len(t, *urls, 2)
	assert.Equal(t, "/api/v1/namespaces/ns/pods/p/attach", (*urls)[1].Path)
	assert.NotContains(t, (*urls)[1].Query(), "command")
}

func TestAttachForbiddenNamesItsPermission(t *testing.T) {
	forbidden := apierrors.NewForbidden(schema.GroupResource{Resource: "pods/attach"}, "p", errors.New("user cannot create pods/attach"))
	s, _ := execSessionFor(t, func(context.Context, remotecommand.StreamOptions) error { return forbidden },
		pod("ns", "p", "uid-1", withDebugger(runningState)))
	h, err := s.PrepareExec(context.Background(), podRef1, provider.ExecRequest{Channel: "debugger", Attach: true})
	require.NoError(t, err)
	_, err = h.Run(context.Background(), attachTerm("", io.Discard, nil))
	var pe *provider.Error
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, provider.ClassForbidden, pe.Class)
	assert.Contains(t, pe.Message, "pods/attach permission")
}
