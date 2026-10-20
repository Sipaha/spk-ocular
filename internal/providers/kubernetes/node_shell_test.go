package kubernetes

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

func nodeShellSession(t *testing.T) (*session, *dynamicfake.FakeDynamicClient) {
	t.Helper()
	n := node("worker", "node-uid", "1", false)
	n.SetLabels(map[string]string{"kubernetes.io/os": "linux"})
	ns := nsObject(nil)
	ns.SetName("kube-system")
	ns.SetUID("ns-uid")
	c := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(kruntime.NewScheme(), map[schema.GroupVersionResource]string{podGVR: "PodList", nodeGVR: "NodeList", namespaceGV: "NamespaceList"}, n, ns)
	s := newSession("ctx", "h", c, false)
	t.Cleanup(s.Close)
	clock := newHelperTestClock()
	s.conn.helpers.now = clock.Now
	s.conn.helpers.after = clock.After
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		s.conn.helpers.close(ctx)
	})
	ssarAnswer(c, true, nil)
	s.conn.startWait = 50 * time.Millisecond
	return s, c
}
func nodeShellPlan(t *testing.T, s *session) core.ActionPlan {
	t.Helper()
	p, err := s.PrepareAction(t.Context(), nodeRef("worker", "node-uid"), "node-shell", core.ActionParams{})
	require.NoError(t, err)
	require.Nil(t, p.Unavailable)
	return p
}
func nodeShellRun(p core.ActionPlan) provider.ActionRun {
	return provider.ActionRun{Ref: p.Where.Ref, Action: p.Action.ID, Params: p.Params, Expect: p.Expect}
}
func shellHandle(t *testing.T, s *session) *nodeShellHandle {
	t.Helper()
	// This helper targets the single Pod owner's startup/cleanup primitives.
	h, err := s.prepareNodeHelper(t.Context(), nodeShellRun(nodeShellPlan(t, s)))
	require.NoError(t, err)
	return h
}
func readyShells(c *dynamicfake.FakeDynamicClient) {
	c.PrependReactor("create", "pods", func(a k8stesting.Action) (bool, kruntime.Object, error) {
		p := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).DeepCopy()
		p.SetUID("helper-uid")
		p.SetResourceVersion("1")
		p.Object["status"] = map[string]any{"phase": "Running", "containerStatuses": []any{map[string]any{"name": "shell", "state": map[string]any{"running": map[string]any{}}}}}
		return true, p, c.Tracker().Create(podGVR, p, p.GetNamespace())
	})
}
func countShellWrites(c *dynamicfake.FakeDynamicClient, verb string) int {
	n := 0
	for _, a := range c.Actions() {
		if a.GetResource() == podGVR && a.GetVerb() == verb {
			n++
		}
	}
	return n
}

func TestNodeShellReviewAndAdmissionNeverWrite(t *testing.T) {
	s, c := nodeShellSession(t)
	p := nodeShellPlan(t, s)
	assert.False(t, p.Destructive)
	assert.True(t, p.Action.NoAgents)
	assert.True(t, p.Action.Single)
	assert.True(t, p.Action.Terminal)
	assert.Contains(t, text(p), "root access")
	assert.Contains(t, text(p), "1 hour")
	assert.Contains(t, text(p), "kube-system")
	assert.Equal(t, core.RightsAllowed, p.Rights.State)
	h, err := s.PrepareTerminalAction(t.Context(), nodeShellRun(p))
	require.NoError(t, err)
	h.Close()
	assert.Zero(t, countShellWrites(c, "create"))
	assert.Zero(t, countShellWrites(c, "delete"))
	_, err = s.PrepareTerminalAction(t.Context(), nodeShellRun(p))
	require.Error(t, err, "review is one-shot")
}
func TestNodeShellGuards(t *testing.T) {
	for _, mode := range []string{"image", "namespace", "uid", "token", "session", "expiry", "node-replaced", "namespace-replaced", "policy", "windows"} {
		t.Run(mode, func(t *testing.T) {
			s, c := nodeShellSession(t)
			r := nodeShellRun(nodeShellPlan(t, s))
			switch mode {
			case "image":
				r.Params.Text = strp("other:1")
			case "namespace":
				r.Params.Choice = strp("default")
			case "uid":
				r.Ref.UID = "other"
			case "token":
				r.Expect += "x"
			case "session":
				s.incarnation = "another"
			case "expiry":
				s.now = func() time.Time { return time.Now().Add(time.Hour) }
			case "node-replaced", "windows":
				n, _ := c.Resource(nodeGVR).Get(t.Context(), "worker", metav1.GetOptions{})
				if mode == "windows" {
					n.SetLabels(map[string]string{"kubernetes.io/os": "windows"})
				} else {
					n.SetUID("other")
				}
				_, err := c.Resource(nodeGVR).Update(t.Context(), n, metav1.UpdateOptions{})
				require.NoError(t, err)
			case "namespace-replaced", "policy":
				n, _ := c.Resource(namespaceGV).Get(t.Context(), "kube-system", metav1.GetOptions{})
				if mode == "policy" {
					n.SetLabels(map[string]string{enforceKey: "restricted"})
				} else {
					n.SetUID("other")
				}
				_, err := c.Resource(namespaceGV).Update(t.Context(), n, metav1.UpdateOptions{})
				require.NoError(t, err)
			}
			_, err := s.PrepareTerminalAction(t.Context(), r)
			require.Error(t, err)
			assert.Zero(t, countShellWrites(c, "create"))
		})
	}
}
func TestNodeShellExecAndCleanup(t *testing.T) {
	s, c := nodeShellSession(t)
	readyShells(c)
	h := shellHandle(t, s)
	var out bytes.Buffer
	s.conn.newExecutor = func(u *url.URL) (remotecommand.Executor, error) {
		assert.Equal(t, "/api/v1/namespaces/kube-system/pods/"+h.g.Name+"/exec", u.Path)
		assert.Equal(t, []string{"nsenter", "-t", "1", "-m", "-u", "-i", "-n", "-p", "-r", "-w", "--", "/bin/sh", "-l"}, u.Query()["command"])
		return &fakeExecutor{play: func(_ context.Context, o remotecommand.StreamOptions) error {
			_, err := io.Copy(o.Stdout, o.Stdin)
			return err
		}}, nil
	}
	// Connection snapshot lives independently of the original resource session.
	s.Close()
	st, err := h.Run(t.Context(), provider.Terminal{Stdin: strings.NewReader("host echo"), Stdout: &out})
	require.NoError(t, err)
	assert.True(t, st.Known)
	assert.Equal(t, "host echo", out.String())
	assert.Equal(t, 1, countShellWrites(c, "create"))
	assert.Equal(t, 1, countShellWrites(c, "delete"))
	_, err = c.Resource(podGVR).Namespace("kube-system").Get(t.Context(), h.g.Name, metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err))
	for _, a := range c.Actions() {
		if a.GetVerb() == "delete" && a.GetResource() == podGVR {
			opts := a.(k8stesting.DeleteAction).GetDeleteOptions()
			require.NotNil(t, opts.Preconditions)
			assert.EqualValues(t, "helper-uid", *opts.Preconditions.UID)
		}
	}
	_, err = h.Again()
	require.Error(t, err)
	_, err = h.Run(t.Context(), provider.Terminal{})
	require.Error(t, err)
	assert.Equal(t, 1, countShellWrites(c, "create"))
}
func TestNodeShellStartupFailureAndCancellationCleanup(t *testing.T) {
	for _, mode := range []string{"pull", "timeout", "cancel", "exec", "delete-denied", "create-unknown", "collision", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			s, c := nodeShellSession(t)
			h := shellHandle(t, s)
			readyShells(c)
			switch mode {
			case "pull", "timeout", "cancel":
				c.PrependReactor("create", "pods", func(a k8stesting.Action) (bool, kruntime.Object, error) {
					p := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).DeepCopy()
					p.SetUID("helper-uid")
					p.Object["status"] = map[string]any{"phase": "Pending"}
					if mode == "pull" {
						p.Object["status"].(map[string]any)["containerStatuses"] = []any{map[string]any{"name": "shell", "state": map[string]any{"waiting": map[string]any{"reason": "ErrImagePull"}}}}
					}
					return true, p, c.Tracker().Create(podGVR, p, p.GetNamespace())
				})
			case "create-unknown":
				c.PrependReactor("create", "pods", func(a k8stesting.Action) (bool, kruntime.Object, error) {
					p := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).DeepCopy()
					p.SetUID("helper-uid")
					require.NoError(t, c.Tracker().Create(podGVR, p, p.GetNamespace()))
					return true, nil, errors.New("lost create response")
				})
			case "collision":
				p := h.g.pod()
				p.SetUID("foreign") // Even copied review labels cannot authorize cleanup after a 409.
				require.NoError(t, c.Tracker().Create(podGVR, p, p.GetNamespace()))
				c.PrependReactor("create", "pods", func(k8stesting.Action) (bool, kruntime.Object, error) {
					return true, nil, apierrors.NewAlreadyExists(podGVR.GroupResource(), h.g.Name)
				})
			case "replacement":
				c.PrependReactor("create", "pods", func(_ k8stesting.Action) (bool, kruntime.Object, error) {
					p := h.g.pod()
					p.SetUID("foreign")
					p.SetLabels(nil)
					require.NoError(t, c.Tracker().Create(podGVR, p, p.GetNamespace()))
					return true, p, nil
				})
			case "delete-denied":
				c.PrependReactor("delete", "pods", func(k8stesting.Action) (bool, kruntime.Object, error) {
					return true, nil, apierrors.NewForbidden(podGVR.GroupResource(), h.g.Name, errors.New("denied"))
				})
			}
			s.conn.newExecutor = func(_ *url.URL) (remotecommand.Executor, error) {
				return &fakeExecutor{play: func(context.Context, remotecommand.StreamOptions) error { return errors.New("exec failed") }}, nil
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var notices []core.Message
			_, err := h.Run(ctx, provider.Terminal{Notice: func(m core.Message) {
				notices = append(notices, m)
				if mode == "cancel" {
					cancel()
				}
			}})
			require.Error(t, err)
			assert.Equal(t, 1, countShellWrites(c, "create"), "never retry a create")
			if mode == "collision" || mode == "replacement" {
				assert.Zero(t, countShellWrites(c, "delete"), "never delete a foreign Pod")
			} else {
				assert.Equal(t, 1, countShellWrites(c, "delete"))
			}
			if mode == "delete-denied" {
				assert.Contains(t, err.Error(), "cleanup")
				assert.Contains(t, strings.Join(core.Texts(notices), " "), h.g.Name)
			}
			if mode == "create-unknown" {
				assert.Contains(t, err.Error(), "unknown outcome")
			}
		})
	}
}
func TestNodeShellRevalidatesBeforeCreate(t *testing.T) {
	s, c := nodeShellSession(t)
	h := shellHandle(t, s)
	n, _ := c.Resource(nodeGVR).Get(t.Context(), "worker", metav1.GetOptions{})
	n.SetUID("new-node")
	_, err := c.Resource(nodeGVR).Update(t.Context(), n, metav1.UpdateOptions{})
	require.NoError(t, err)
	_, err = h.Run(t.Context(), provider.Terminal{})
	require.Error(t, err)
	assert.Zero(t, countShellWrites(c, "create"))
}

func TestNodeShellAdmissionPolicyIsWarningAndChangedPolicyInvalidatesReview(t *testing.T) {
	s, c := nodeShellSession(t)
	ns, err := c.Resource(namespaceGV).Get(t.Context(), "kube-system", metav1.GetOptions{})
	require.NoError(t, err)
	ns.SetLabels(map[string]string{enforceKey: "restricted"})
	_, err = c.Resource(namespaceGV).Update(t.Context(), ns, metav1.UpdateOptions{})
	require.NoError(t, err)
	p := nodeShellPlan(t, s)
	assert.Contains(t, strings.Join(core.Texts(p.Warnings), " "), "unless the request is exempt")
	// An exempt identity may be admitted by the API server. A namespace label
	// alone must not be mistaken for the server's admission decision.
	h, err := s.PrepareTerminalAction(t.Context(), nodeShellRun(p))
	require.NoError(t, err)
	h.Close()
}

func TestNodeShellCleanupWaitsForActualDeletion(t *testing.T) {
	s, c := nodeShellSession(t)
	h := shellHandle(t, s)
	p := h.g.pod()
	p.SetUID("helper-uid")
	p.SetFinalizers([]string{"fixture/hold"})
	require.NoError(t, c.Tracker().Create(podGVR, p, p.GetNamespace()))
	deleted := make(chan struct{})
	c.PrependReactor("delete", "pods", func(k8stesting.Action) (bool, kruntime.Object, error) { close(deleted); return true, nil, nil })
	done := make(chan error, 1)
	go func() { done <- h.cleanup("helper-uid") }()
	<-deleted
	select {
	case err := <-done:
		t.Fatalf("DELETE success is not disappearance: %v", err)
	default:
	}
	require.NoError(t, c.Tracker().Delete(podGVR, p.GetNamespace(), p.GetName()))
	require.NoError(t, <-done)
	assert.Equal(t, 1, countShellWrites(c, "delete"))
}

func TestNodeShellCleanupDoesNotDeleteAReplacementWithCopiedLabels(t *testing.T) {
	s, c := nodeShellSession(t)
	readyShells(c)
	h := shellHandle(t, s)
	s.conn.newExecutor = func(_ *url.URL) (remotecommand.Executor, error) {
		return &fakeExecutor{play: func(context.Context, remotecommand.StreamOptions) error {
			p, err := c.Resource(podGVR).Namespace(h.g.Namespace).Get(t.Context(), h.g.Name, metav1.GetOptions{})
			require.NoError(t, err)
			require.NoError(t, c.Tracker().Delete(podGVR, h.g.Namespace, h.g.Name))
			p.SetUID("replacement-copied-labels")
			require.NoError(t, c.Tracker().Create(podGVR, p, h.g.Namespace))
			return nil
		}}, nil
	}
	_, err := h.Run(t.Context(), provider.Terminal{})
	require.NoError(t, err)
	assert.Zero(t, countShellWrites(c, "delete"))
	p, err := c.Resource(podGVR).Namespace(h.g.Namespace).Get(t.Context(), h.g.Name, metav1.GetOptions{})
	require.NoError(t, err)
	assert.EqualValues(t, "replacement-copied-labels", p.GetUID())
}

func TestNodeShellHelperCannotInheritAgentContainerExecGrants(t *testing.T) {
	s, c := nodeShellSession(t)
	readyShells(c)
	h := shellHandle(t, s)
	p := h.g.pod()
	p.SetUID("helper-uid")
	p.Object["status"] = map[string]any{"phase": "Running", "containerStatuses": []any{map[string]any{"name": "shell", "state": map[string]any{"running": map[string]any{}}}}}
	require.NoError(t, c.Tracker().Create(podGVR, p, p.GetNamespace()))
	ref := core.Ref{Provider: ProviderID, Target: "ctx", Kind: "pods", Scope: p.GetNamespace(), Name: p.GetName(), UID: string(p.GetUID())}
	for _, labels := range []map[string]string{p.GetLabels(), nil} {
		p.SetLabels(labels)
		require.NoError(t, c.Tracker().Update(podGVR, p, p.GetNamespace()))
		_, err := s.PrepareExec(t.Context(), ref, provider.ExecRequest{Agent: true, Channel: "shell", Command: []string{"id"}})
		var pe *provider.Error
		require.ErrorAs(t, err, &pe)
		assert.Equal(t, provider.ClassForbidden, pe.Class)
		ui, err := s.PrepareExec(t.Context(), ref, provider.ExecRequest{Channel: "shell", Command: []string{"id"}})
		require.NoError(t, err)
		ui.Close()
	}
}
