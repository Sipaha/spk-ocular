package kubernetes

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// actionCluster is the kind test cluster — checked before anything is
// created or deleted — with a namespace of the test's own.
type actionCluster struct {
	t    *testing.T
	sess *session
	ns   string
}

func kindActionCluster(t *testing.T) *actionCluster {
	t.Helper()
	p, target := kindProvider(t)
	require.Equal(t, "kubeconfig:kind-ocular-dev", target, "actions are tested on the kind cluster ocular-dev only")
	ps, err := p.Open(context.Background(), target)
	require.NoError(t, err)
	t.Cleanup(ps.Close)
	s := ps.(*session)
	host, _, err := net.SplitHostPort(s.conn.endpoint)
	require.NoError(t, err)
	require.Contains(t, []string{"127.0.0.1", "localhost", "::1"}, host, "the kind API server is on loopback")
	c := &actionCluster{t: t, sess: s, ns: fmt.Sprintf("ocular-act-%d", time.Now().UnixNano()%1_000_000)}
	c.kubectl("create", "namespace", c.ns)
	t.Cleanup(func() { c.kubectlNS("delete", "namespace", c.ns, "--wait=false") })
	return c
}

func (c *actionCluster) run(args ...string) (string, error) {
	out, err := exec.Command("kubectl", append([]string{"--kubeconfig", kindKubeconfig(c.t), "--context", "kind-ocular-dev"}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (c *actionCluster) kubectl(args ...string) string {
	c.t.Helper()
	out, err := c.run(args...)
	require.NoError(c.t, err, out)
	return out
}

// kubectlNS runs in the test namespace.
func (c *actionCluster) kubectlNS(args ...string) string {
	c.t.Helper()
	return c.kubectl(append([]string{"-n", c.ns}, args...)...)
}

func (c *actionCluster) deployment(name string, replicas int) core.Ref {
	c.t.Helper()
	c.kubectlNS("create", "deployment", name, "--image=nginx:1.27-alpine", fmt.Sprintf("--replicas=%d", replicas))
	c.kubectlNS("patch", "deployment", name, "--type=merge", "-p", `{"spec":{"template":{"metadata":{"annotations":{"team":"a"}}}}}`)
	c.kubectlNS("rollout", "status", "deployment/"+name, "--timeout=120s")
	return c.ref("apps/deployments", name)
}

func (c *actionCluster) ref(kind, name string) core.Ref {
	uid := c.kubectlNS("get", strings.TrimPrefix(strings.TrimPrefix(kind, "apps/"), "networking.k8s.io/"), name, "-o", "jsonpath={.metadata.uid}")
	return core.Ref{Provider: ProviderID, Target: c.sess.target, Scope: c.ns, Kind: kind, Name: name, UID: uid}
}

// act prepares and runs action like the UI does (the plan's ref and
// Expect go back unchanged).
func (c *actionCluster) act(ref core.Ref, action string, p core.ActionParams) (core.ActionResult, error) {
	c.t.Helper()
	plan, err := c.sess.PrepareAction(context.Background(), ref, action, p)
	require.NoError(c.t, err)
	return c.sess.RunAction(context.Background(), provider.ActionRun{Ref: plan.Where.Ref, Action: action, Params: p, Expect: plan.Expect})
}

func (c *actionCluster) jsonpath(kind, name, path string) string {
	c.t.Helper()
	out, _ := c.run("-n", c.ns, "get", kind, name, "-o", "jsonpath="+path)
	return out
}

func TestKindActionRestartKeepsOtherAnnotationsAndTwiceIsTwoRollouts(t *testing.T) {
	c := kindActionCluster(t)
	ref := c.deployment("web", 1)
	res, err := c.act(ref, "restart", core.ActionParams{})
	require.NoError(t, err)
	assert.Contains(t, res.Message, "restart requested")
	first := c.jsonpath("deployment", "web", `{.spec.template.metadata.annotations.kubectl\.kubernetes\.io/restartedAt}`)
	require.NotEmpty(t, first)
	assert.Equal(t, "a", c.jsonpath("deployment", "web", "{.spec.template.metadata.annotations.team}"), "a merge: other annotations stay")
	_, err = c.act(ref, "restart", core.ActionParams{}) // within the same second
	require.NoError(t, err)
	second := c.jsonpath("deployment", "web", `{.spec.template.metadata.annotations.kubectl\.kubernetes\.io/restartedAt}`)
	assert.NotEqual(t, first, second)
	c.kubectlNS("rollout", "status", "deployment/web", "--timeout=120s")
	assert.Equal(t, "4", c.jsonpath("deployment", "web", "{.metadata.generation}"), "create, annotate, then two restarts")
}

func TestKindActionScaleUpAndToZero(t *testing.T) {
	c := kindActionCluster(t)
	ref := c.deployment("web", 1)
	_, err := c.act(ref, "scale", count(3))
	require.NoError(t, err)
	assert.Equal(t, "3", c.jsonpath("deployment", "web", "{.spec.replicas}"))
	_, err = c.act(ref, "scale", count(0))
	require.NoError(t, err)
	assert.Equal(t, "0", c.jsonpath("deployment", "web", "{.spec.replicas}"))
}

func TestKindActionDeletePods(t *testing.T) {
	c := kindActionCluster(t)
	c.deployment("web", 1)
	name := c.kubectlNS("get", "pods", "-l", "app=web", "-o", "jsonpath={.items[0].metadata.name}")
	_, err := c.act(c.ref("pods", name), "delete", core.ActionParams{})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		out, _ := c.run("-n", c.ns, "get", "pods", "-l", "app=web", "-o", "jsonpath={.items[*].metadata.name}")
		return out != "" && !strings.Contains(out, name)
	}, 90*time.Second, 500*time.Millisecond, "the ReplicaSet creates a replacement")

	c.kubectlNS("run", "bare", "--image=nginx:1.27-alpine", "--restart=Never")
	_, err = c.act(c.ref("pods", "bare"), "delete", core.ActionParams{})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		_, err := c.run("-n", c.ns, "get", "pod", "bare")
		return err != nil
	}, 90*time.Second, 500*time.Millisecond, "a bare pod is gone for good")
}

// The object is replaced by a same-named one between the run's read and
// its write: the write fails as gone and the new object is untouched.
func TestKindActionOnAnObjectReplacedBetweenReadAndWrite(t *testing.T) {
	for _, a := range []struct {
		action string
		p      core.ActionParams
	}{{"restart", core.ActionParams{}}, {"scale", count(4)}, {"delete", core.ActionParams{}}} {
		t.Run(a.action, func(t *testing.T) {
			c := kindActionCluster(t)
			ref := c.deployment("web", 1)
			plan, err := c.sess.PrepareAction(context.Background(), ref, a.action, a.p)
			require.NoError(t, err)
			var once sync.Once
			c.sess.beforeWrite = func(string, *unstructured.Unstructured) {
				once.Do(func() {
					c.kubectlNS("delete", "deployment", "web", "--wait=true")
					c.kubectlNS("create", "deployment", "web", "--image=nginx:1.27-alpine", "--replicas=1")
				})
			}
			_, err = c.sess.RunAction(context.Background(), provider.ActionRun{Ref: plan.Where.Ref, Action: a.action, Params: a.p, Expect: plan.Expect})
			assertClass(t, err, provider.ClassGone)
			newUID := c.jsonpath("deployment", "web", "{.metadata.uid}")
			require.NotEmpty(t, newUID, "the replacement exists")
			assert.NotEqual(t, ref.UID, newUID)
			assert.Equal(t, "1", c.jsonpath("deployment", "web", "{.spec.replicas}"))
			assert.Empty(t, c.jsonpath("deployment", "web", `{.spec.template.metadata.annotations.kubectl\.kubernetes\.io/restartedAt}`))
		})
	}
}

// Only the status changes between the read and the write (the version
// moves): the run retries transparently.
func TestKindActionStatusChurnIsRetried(t *testing.T) {
	for _, a := range []struct {
		action string
		p      core.ActionParams
	}{{"restart", core.ActionParams{}}, {"scale", count(2)}, {"delete", core.ActionParams{}}} {
		t.Run(a.action, func(t *testing.T) {
			c := kindActionCluster(t)
			ref := c.deployment("web", 1)
			writes := 0
			c.sess.beforeWrite = func(string, *unstructured.Unstructured) {
				if writes++; writes == 1 {
					c.kubectlNS("patch", "deployment", "web", "--subresource=status", "--type=merge", "-p", `{"status":{"collisionCount":7}}`)
				}
			}
			_, err := c.act(ref, a.action, a.p)
			require.NoError(t, err)
			assert.Equal(t, 2, writes, "the first write met the new version and was retried")
		})
	}
}

func TestKindActionReplicasChangedAfterThePlanIsAConflict(t *testing.T) {
	c := kindActionCluster(t)
	ref := c.deployment("web", 1)
	plan, err := c.sess.PrepareAction(context.Background(), ref, "scale", count(2))
	require.NoError(t, err)
	c.kubectlNS("scale", "deployment/web", "--replicas=5")
	_, err = c.sess.RunAction(context.Background(), provider.ActionRun{Ref: plan.Where.Ref, Action: "scale", Params: count(2), Expect: plan.Expect})
	assertClass(t, err, provider.ClassConflict)
	assert.Equal(t, "5", c.jsonpath("deployment", "web", "{.spec.replicas}"))
}

func TestKindActionWithoutTheRightIsForbidden(t *testing.T) {
	s := rbacSession(t, "viewer").(*session)
	c := &actionCluster{t: t, ns: "ocular-demo"}
	pod := c.kubectl("-n", "ocular-demo", "get", "pods", "-l", "app=web", "-o", "jsonpath={.items[0].metadata.name}")
	ref := core.Ref{Provider: ProviderID, Target: s.target, Scope: "ocular-demo", Kind: "pods", Name: pod}
	plan, err := s.PrepareAction(context.Background(), ref, "delete", core.ActionParams{})
	require.NoError(t, err)
	_, err = s.RunAction(context.Background(), provider.ActionRun{Ref: plan.Where.Ref, Action: "delete", Expect: plan.Expect})
	assertClass(t, err, provider.ClassForbidden)
	assert.Equal(t, pod, c.kubectl("-n", "ocular-demo", "get", "pod", pod, "-o", "jsonpath={.metadata.name}"))
}
