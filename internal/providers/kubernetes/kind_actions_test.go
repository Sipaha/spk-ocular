package kubernetes

import (
	"context"
	"fmt"
	"net"
	"os"
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
	assert.Contains(t, res.Message.Text, "restart requested")
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

// apply creates objects from a manifest in the test namespace.
func (c *actionCluster) apply(manifest string) {
	c.t.Helper()
	cmd := exec.Command("kubectl", "--kubeconfig", kindKubeconfig(c.t), "--context", "kind-ocular-dev", "-n", c.ns, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	out, err := cmd.CombinedOutput()
	require.NoError(c.t, err, string(out))
}

// statefulSet runs two pods, each with a claim data-<name>-<ordinal>, under
// the given claim retention policy.
func (c *actionCluster) statefulSet(name, whenDeleted, whenScaled string) core.Ref {
	c.t.Helper()
	c.apply(fmt.Sprintf(`apiVersion: apps/v1
kind: StatefulSet
metadata: {name: %[1]s}
spec:
  replicas: 2
  serviceName: %[1]s
  podManagementPolicy: Parallel
  persistentVolumeClaimRetentionPolicy: {whenDeleted: %[2]s, whenScaled: %[3]s}
  selector: {matchLabels: {app: %[1]s}}
  template:
    metadata: {labels: {app: %[1]s}}
    spec:
      terminationGracePeriodSeconds: 0
      containers:
      - name: web
        image: nginx:1.27-alpine
        volumeMounts: [{name: data, mountPath: /data}]
  volumeClaimTemplates:
  - metadata: {name: data}
    spec:
      accessModes: [ReadWriteOnce]
      resources: {requests: {storage: 16Mi}}
`, name, whenDeleted, whenScaled))
	c.kubectlNS("rollout", "status", "statefulset/"+name, "--timeout=180s")
	return c.ref("apps/statefulsets", name)
}

func (c *actionCluster) claims() string {
	c.t.Helper()
	return c.kubectlNS("get", "pvc", "-o", "jsonpath={.items[*].metadata.name}")
}

// prepare returns the plan as the UI would show it.
func (c *actionCluster) prepare(ref core.Ref, action string, p core.ActionParams) core.ActionPlan {
	c.t.Helper()
	plan, err := c.sess.PrepareAction(context.Background(), ref, action, p)
	require.NoError(c.t, err)
	return plan
}

func (c *actionCluster) runPlan(plan core.ActionPlan) {
	c.t.Helper()
	_, err := c.sess.RunAction(context.Background(), provider.ActionRun{Ref: plan.Where.Ref, Action: plan.Action.ID, Params: plan.Params, Expect: plan.Expect})
	require.NoError(c.t, err)
}

func TestKindActionStatefulSetClaimsFollowTheRetentionPolicy(t *testing.T) {
	t.Run("scaled Delete, deleted Retain", func(t *testing.T) {
		c := kindActionCluster(t)
		ref := c.statefulSet("db", "Retain", "Delete")
		require.Equal(t, "data-db-0 data-db-1", c.claims())

		plan := c.prepare(ref, "scale", count(1))
		assert.True(t, plan.Destructive)
		assert.Contains(t, strings.Join(core.Texts(plan.Effects), "\n"), "The PersistentVolumeClaims of pod 1 are deleted")
		c.runPlan(plan)
		require.Eventually(t, func() bool { return c.claims() == "data-db-0" }, 120*time.Second, time.Second, "the claim of the removed pod is deleted")

		plan = c.prepare(ref, "delete", core.ActionParams{})
		assert.Contains(t, core.Texts(plan.Effects), "Its PersistentVolumeClaims are kept.")
		c.runPlan(plan)
		require.Eventually(t, func() bool {
			_, err := c.run("-n", c.ns, "get", "statefulset", "db")
			return err != nil
		}, 120*time.Second, time.Second)
		require.Never(t, func() bool { return c.claims() != "data-db-0" }, 5*time.Second, time.Second, "the remaining claim is kept")
	})
	t.Run("scaled Retain, deleted Delete", func(t *testing.T) {
		c := kindActionCluster(t)
		ref := c.statefulSet("db", "Delete", "Retain")

		plan := c.prepare(ref, "scale", count(1))
		assert.False(t, plan.Destructive, "scaling down keeps the claims")
		assert.Contains(t, strings.Join(core.Texts(plan.Effects), "\n"), "The PersistentVolumeClaims of removed pods are kept until the StatefulSet is deleted")
		c.runPlan(plan)
		require.Eventually(t, func() bool {
			_, err := c.run("-n", c.ns, "get", "pod", "db-1")
			return err != nil
		}, 120*time.Second, time.Second)
		assert.Equal(t, "data-db-0 data-db-1", c.claims(), "the claim of the removed pod is kept")

		plan = c.prepare(ref, "delete", core.ActionParams{})
		assert.Contains(t, strings.Join(core.Texts(plan.Effects), "\n"), "The PersistentVolumeClaims of its pods are deleted")
		c.runPlan(plan)
		require.Eventually(t, func() bool { return c.claims() == "" }, 120*time.Second, time.Second,
			"every claim of its pods goes with it, the one kept at scale down too")
	})
}

func TestKindActionRestartOfAPausedDeploymentIsUnavailable(t *testing.T) {
	c := kindActionCluster(t)
	ref := c.deployment("web", 1)
	c.kubectlNS("rollout", "pause", "deployment/web")
	plan := c.prepare(ref, "restart", core.ActionParams{})
	require.NotNil(t, plan.Unavailable)
	assert.Equal(t, "deployment web is paused: resume its rollout first", plan.Unavailable.Text)
	_, err := c.sess.RunAction(context.Background(), provider.ActionRun{Ref: plan.Where.Ref, Action: "restart", Expect: plan.Expect})
	assertClass(t, err, provider.ClassConflict)
	assert.Empty(t, c.jsonpath("deployment", "web", `{.spec.template.metadata.annotations.kubectl\.kubernetes\.io/restartedAt}`))

	c.kubectlNS("rollout", "resume", "deployment/web")
	plan = c.prepare(ref, "restart", core.ActionParams{})
	assert.Nil(t, plan.Unavailable)
	c.runPlan(plan)
}

func TestKindActionScaleWarnsOfAnAutoscaler(t *testing.T) {
	c := kindActionCluster(t)
	ref := c.deployment("web", 1)
	c.deployment("other", 1)
	plan := c.prepare(ref, "scale", count(2))
	assert.Empty(t, plan.Warnings)
	assert.Equal(t, core.RightsAllowed, plan.Rights.State)

	c.kubectlNS("autoscale", "deployment", "other", "--min=1", "--max=3")
	c.kubectlNS("autoscale", "deployment", "web", "--min=2", "--max=5")
	plan = c.prepare(ref, "scale", count(2))
	assert.Equal(t, []string{"HorizontalPodAutoscaler web may override the count (2–5)."}, core.Texts(plan.Warnings))
}

func TestKindActionViewerRightsAreDeniedInThePlan(t *testing.T) {
	s := rbacSession(t, "viewer").(*session)
	c := &actionCluster{t: t, ns: "ocular-demo"}
	pod := c.kubectl("-n", "ocular-demo", "get", "pods", "-l", "app=web", "-o", "jsonpath={.items[0].metadata.name}")
	ref := core.Ref{Provider: ProviderID, Target: s.target, Scope: "ocular-demo", Kind: "pods", Name: pod}
	plan, err := s.PrepareAction(context.Background(), ref, "delete", core.ActionParams{})
	require.NoError(t, err)
	assert.Equal(t, core.RightsDenied, plan.Rights.State)
	assert.True(t, strings.HasPrefix(plan.Rights.Reason, "you may not delete pods in ocular-demo"), plan.Rights.Reason)
}

func TestKindActionDeleteOfADeploymentCountsThePodsItOwns(t *testing.T) {
	c := kindActionCluster(t)
	ref := c.deployment("web", 2)
	c.kubectlNS("run", "stray", "--image=nginx:1.27-alpine", "--restart=Never", "--labels=app=web")
	plan := c.prepare(ref, "delete", core.ActionParams{})
	assert.Contains(t, strings.Join(core.Texts(plan.Effects), "\n"), "Its pods are deleted too (2 now).", "the stray pod has its labels, not its owner")
}

// drainWorker is the kind worker kept for node actions (tainted: only
// drain fixtures run there); every test leaves it open for pods again.
const drainWorker = "ocular-dev-worker"

func (c *actionCluster) worker() core.Ref {
	c.t.Helper()
	require.Equal(c.t, "only", c.kubectl("get", "node", drainWorker, "-o", `jsonpath={.metadata.labels.ocular\.dev/drain}`), "the drain worker, by its label")
	c.t.Cleanup(func() { _, _ = c.run("uncordon", drainWorker) })
	uid := c.kubectl("get", "node", drainWorker, "-o", "jsonpath={.metadata.uid}")
	return core.Ref{Provider: ProviderID, Target: c.sess.target, Kind: "nodes", Name: drainWorker, UID: uid}
}

func TestKindActionCordonAndUncordon(t *testing.T) {
	c := kindActionCluster(t)
	ref := c.worker()
	unschedulable := func() string {
		return c.kubectl("get", "node", drainWorker, "-o", "jsonpath={.spec.unschedulable}")
	}
	res, err := c.act(ref, "cordon", core.ActionParams{})
	require.NoError(t, err)
	assert.Contains(t, res.Message.Text, "cordon requested")
	assert.Equal(t, "true", unschedulable())

	plan, err := c.sess.PrepareAction(context.Background(), ref, "cordon", core.ActionParams{})
	require.NoError(t, err)
	require.NotNil(t, plan.Unavailable, "already cordoned")
	assert.Equal(t, core.RightsAllowed, plan.Rights.State)

	_, err = c.act(ref, "uncordon", core.ActionParams{})
	require.NoError(t, err)
	assert.Empty(t, unschedulable())
}

// drainFixtures: on the drain worker only (nodeSelector + toleration) —
// movers (2 replicas), cache (emptyDir), guarded (a PodDisruptionBudget
// allowing no disruption) and a pod without a controller.
func drainFixtures(ns string) string {
	pod := func(app, extra string) string {
		return fmt.Sprintf(`metadata: {labels: {app: %s, ocular.test/drain: fixture}}
    spec:
      nodeSelector: {ocular.dev/drain: only}
      tolerations: [{key: ocular.dev/drain, operator: Equal, value: only, effect: NoSchedule}]
      terminationGracePeriodSeconds: 1
      containers: [{name: app, image: "registry.k8s.io/pause:3.10", imagePullPolicy: IfNotPresent%s}]`, app, extra)
	}
	deploy := func(name string, n int, extra, volumes string) string {
		return fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata: {name: %s, namespace: %s}
spec:
  replicas: %d
  selector: {matchLabels: {app: %s}}
  template:
    %s%s
---
`, name, ns, n, name, pod(name, extra), volumes)
	}
	cache := `
      volumes: [{name: scratch, emptyDir: {}}]`
	return deploy("movers", 2, "", "") + deploy("cache", 1, ", volumeMounts: [{name: scratch, mountPath: /scratch}]", cache) + deploy("guarded", 1, "", "") +
		fmt.Sprintf(`apiVersion: policy/v1
kind: PodDisruptionBudget
metadata: {name: guarded, namespace: %s}
spec: {minAvailable: 1, selector: {matchLabels: {app: guarded}}}
---
apiVersion: v1
kind: Pod
metadata: {name: bare, namespace: %s, labels: {app: bare, ocular.test/drain: fixture}}
spec:
  nodeSelector: {ocular.dev/drain: only}
  tolerations: [{key: ocular.dev/drain, operator: Equal, value: only, effect: NoSchedule}]
  terminationGracePeriodSeconds: 1
  containers: [{name: app, image: "registry.k8s.io/pause:3.10", imagePullPolicy: IfNotPresent}]
`, ns, ns)
}

// workerPods: the worker's pods as [namespace/name, uid, owner kind, drain label].
func (c *actionCluster) workerPods() [][]string {
	c.t.Helper()
	out := c.kubectl("get", "pods", "-A", "--field-selector", "spec.nodeName="+drainWorker, "-o",
		`jsonpath={range .items[*]}{.metadata.namespace}/{.metadata.name}|{.metadata.uid}|{.metadata.ownerReferences[0].kind}|{.metadata.labels.ocular\.test/drain}{"\n"}{end}`)
	var pods [][]string
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Split(l, "|"); len(f) == 4 {
			pods = append(pods, f)
		}
	}
	return pods
}

func TestKindActionDrain(t *testing.T) {
	c := kindActionCluster(t)
	ref := c.worker()
	file := t.TempDir() + "/drain.yaml"
	require.NoError(t, os.WriteFile(file, []byte(drainFixtures(c.ns)), 0o600))
	c.kubectl("apply", "-f", file)
	for _, d := range []string{"movers", "cache", "guarded"} {
		c.kubectlNS("rollout", "status", "deployment/"+d, "--timeout=120s")
	}
	c.kubectlNS("wait", "--for=condition=Ready", "pod/bare", "--timeout=120s")
	c.kubectlNS("wait", "--for=jsonpath={.status.expectedPods}=1", "pdb/guarded", "--timeout=60s")

	// Before any write: every pod a drain would touch on the worker is a
	// fixture of this test; the rest are DaemonSets' (left alone).
	daemons := map[string]bool{}
	movers := map[string]bool{}
	for _, f := range c.workerPods() {
		switch {
		case f[2] == "DaemonSet":
			daemons[f[1]] = true
		case f[3] == "fixture" && strings.HasPrefix(f[0], c.ns+"/"):
			if strings.HasPrefix(f[0], c.ns+"/movers-") {
				movers[f[1]] = true
			}
		default:
			require.Failf(t, "a pod on the drain worker that is not this test's", "%v", f)
		}
	}
	require.Len(t, movers, 2)
	require.NotEmpty(t, daemons, "kindnet and kube-proxy run there")

	plan, err := c.sess.PrepareAction(context.Background(), ref, "drain", core.ActionParams{})
	require.NoError(t, err)
	require.Nil(t, plan.Unavailable)
	assert.Equal(t, core.RightsAllowed, plan.Rights.State)
	names := func(key string) []string {
		for _, l := range plan.Lists {
			if l.Title.Key == "kubernetes."+key {
				var out []string
				for _, it := range l.Items {
					out = append(out, strings.TrimPrefix(it.Name, c.ns+"/"))
				}
				return out
			}
		}
		return nil
	}
	assert.Len(t, names("drain.list.evict"), 4, "movers ×2, cache, guarded")
	assert.Equal(t, []string{"bare"}, names("drain.list.bare"))
	require.Len(t, names("drain.list.emptyDir"), 1)
	assert.True(t, strings.HasPrefix(names("drain.list.emptyDir")[0], "cache-"))
	var blocking []string
	for _, w := range plan.Warnings {
		if w.Key == "kubernetes.drain.pdbBlocksOne" {
			blocking = append(blocking, w.Params["pdb"])
		}
	}
	assert.Equal(t, []string{c.ns + "/guarded"}, blocking)

	res, err := c.sess.RunAction(context.Background(), provider.ActionRun{Ref: plan.Where.Ref, Action: "drain", Expect: plan.Expect})
	require.NoError(t, err)
	byTitle := map[string]core.ActionPart{}
	for _, p := range res.Parts {
		byTitle[strings.TrimPrefix(p.Title, c.ns+"/")] = p
	}
	assert.Equal(t, core.OutcomeDone, byTitle[drainWorker].Outcome, "the cordon")
	for title, p := range byTitle {
		switch {
		case strings.HasPrefix(title, "movers-"), strings.HasPrefix(title, "cache-"):
			assert.Equal(t, core.OutcomeDone, p.Outcome, title)
		case strings.HasPrefix(title, "guarded-"):
			assert.Equal(t, core.OutcomeRefused, p.Outcome)
			require.NotNil(t, p.Why)
			assert.Equal(t, "kubernetes.drain.why.pdb", p.Why.Key, "the budget, told by its cause")
		case title == "bare":
			assert.Equal(t, core.OutcomeSkipped, p.Outcome)
		}
	}
	assert.Len(t, byTitle, 6)
	assert.Equal(t, core.OutcomeRefused, res.Outcome)
	assert.Equal(t, "true", c.kubectl("get", "node", drainWorker, "-o", "jsonpath={.spec.unschedulable}"))

	// The movers' pods leave; their replacements wait (the worker is the
	// only node they may run on, and it is cordoned). DaemonSets stay.
	require.Eventually(t, func() bool {
		for _, f := range c.workerPods() {
			if movers[f[1]] {
				return false
			}
		}
		return true
	}, 90*time.Second, time.Second, "the evicted pods are gone")
	require.Eventually(t, func() bool {
		out, _ := c.run("-n", c.ns, "get", "pods", "-l", "app=movers", "--field-selector", "status.phase=Pending", "-o", "name")
		return len(strings.Fields(out)) == 2
	}, 60*time.Second, time.Second, "replacements wait for a node")
	still := map[string]bool{}
	for _, f := range c.workerPods() {
		still[f[1]] = true
	}
	for uid := range daemons {
		assert.True(t, still[uid], "a DaemonSet's pod is left alone")
	}
	assert.Contains(t, c.kubectlNS("get", "pod", "bare", "-o", "jsonpath={.status.phase}"), "Running", "no controller: left")
}

// A CronJob (a discovered kind) suspended and resumed: the field on the
// server follows, a second suspend is unavailable.
func TestKindActionCronJobSuspendAndResume(t *testing.T) {
	c := kindActionCluster(t)
	require.Eventually(t, func() bool { return c.sess.Catalog().State == core.CatalogReady }, 30*time.Second, 20*time.Millisecond)
	c.apply(`apiVersion: batch/v1
kind: CronJob
metadata: {name: yearly}
spec:
  schedule: "0 0 1 1 *"
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: Never
          containers: [{name: app, image: "busybox:1.36", command: ["true"]}]
`)
	uid := c.kubectlNS("get", "cronjob", "yearly", "-o", "jsonpath={.metadata.uid}")
	ref := core.Ref{Provider: ProviderID, Target: c.sess.target, Scope: c.ns, Kind: "batch/cronjobs", Name: "yearly", UID: uid}
	suspend := func() string { return c.jsonpath("cronjob", "yearly", "{.spec.suspend}") }

	res, err := c.act(ref, "suspend", core.ActionParams{})
	require.NoError(t, err)
	assert.Contains(t, res.Message.Text, "suspend requested")
	assert.Equal(t, "true", suspend())
	plan := c.prepare(ref, "suspend", core.ActionParams{})
	require.NotNil(t, plan.Unavailable, "already suspended")
	assert.Equal(t, core.RightsAllowed, plan.Rights.State)

	_, err = c.act(ref, "resume", core.ActionParams{})
	require.NoError(t, err)
	assert.Equal(t, "false", suspend())
}

// Run now on a suspended CronJob whose history limit is 0: the Job with the
// reviewed name is made as kubectl makes it, runs, and the controller
// deletes it once finished; the same review never sends another Job.
func TestKindActionCronJobRunNow(t *testing.T) {
	c := kindActionCluster(t)
	require.Eventually(t, func() bool { return c.sess.Catalog().State == core.CatalogReady }, 30*time.Second, 20*time.Millisecond)
	c.apply(`apiVersion: batch/v1
kind: CronJob
metadata: {name: yearly}
spec:
  schedule: "0 0 1 1 *"
  suspend: true
  successfulJobsHistoryLimit: 0
  jobTemplate:
    metadata: {labels: {app: yearly}}
    spec:
      template:
        spec:
          restartPolicy: Never
          terminationGracePeriodSeconds: 1
          containers: [{name: app, image: "busybox:1.36", command: ["true"]}]
`)
	uid := c.kubectlNS("get", "cronjob", "yearly", "-o", "jsonpath={.metadata.uid}")
	ref := core.Ref{Provider: ProviderID, Target: c.sess.target, Scope: c.ns, Kind: "batch/cronjobs", Name: "yearly", UID: uid}
	plan := c.prepare(ref, "run", core.ActionParams{})
	require.Nil(t, plan.Unavailable)
	assert.Equal(t, core.RightsAllowed, plan.Rights.State)
	var job string
	for _, e := range plan.Effects {
		if e.Key == "kubernetes.cronjob.run" {
			job = e.Params["job"]
		}
	}
	require.Regexp(t, `^yearly-manual-[a-z0-9]{5}$`, job)

	res, err := c.sess.RunAction(context.Background(), provider.ActionRun{Ref: plan.Where.Ref, Action: "run", Expect: plan.Expect})
	require.NoError(t, err)
	assert.Contains(t, res.Message.Text, job)
	created := c.kubectlNS("get", "job", job, "-o", `jsonpath={.metadata.annotations.cronjob\.kubernetes\.io/instantiate} {.metadata.labels.app} {.metadata.ownerReferences[0].kind}/{.metadata.ownerReferences[0].uid}/{.metadata.ownerReferences[0].controller}`)
	assert.Equal(t, "manual yearly CronJob/"+uid+"/true", created)

	// Finished, it is deleted by the history limit 0.
	require.Eventually(t, func() bool {
		_, err := c.run("-n", c.ns, "get", "job", job)
		return err != nil
	}, 120*time.Second, time.Second, "the finished Job is deleted by the CronJob's history limit")
	_, err = c.sess.RunAction(context.Background(), provider.ActionRun{Ref: plan.Where.Ref, Action: "run", Expect: plan.Expect})
	var pe *provider.Error
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, provider.ClassConflict, pe.Class, "the review was run once")
	assert.Empty(t, c.kubectlNS("get", "jobs", "-o", "name"), "no second Job")
}

// revisionOf: the choice titled "Revision n" of an undo plan.
func revisionOf(t *testing.T, plan core.ActionPlan, n string) core.ActionChoice {
	t.Helper()
	for _, ch := range plan.Choices {
		if ch.Title.Params["revision"] == n {
			return ch
		}
	}
	require.Failf(t, "no revision", "revision %s among %v", n, plan.Choices)
	return core.ActionChoice{}
}

// setEnv makes a new revision: V=v with its change-cause.
func (c *actionCluster) setEnv(v string) {
	c.t.Helper()
	c.kubectlNS("set", "env", "deployment/web", "V="+v)
	c.kubectlNS("annotate", "deployment/web", "kubernetes.io/change-cause=V="+v, "--overwrite")
	c.kubectlNS("rollout", "status", "deployment/web", "--timeout=120s")
}

// Undo to a revision: its template and change-cause come back, it becomes
// the newest revision; an undo sent while that rollout is still going on
// passes (status churn is retried, not refused).
func TestKindActionUndoToARevision(t *testing.T) {
	c := kindActionCluster(t)
	ref := c.deployment("web", 2) // revisions 1 (created) and 2 (annotated)
	c.setEnv("a")                 // 3
	c.setEnv("b")                 // 4
	env := func() string {
		return c.jsonpath("deployment", "web", `{.spec.template.spec.containers[0].env[?(@.name=="V")].value}`)
	}

	plan := c.prepare(ref, "undo", core.ActionParams{})
	require.Nil(t, plan.Unavailable)
	require.Len(t, plan.Choices, 4)
	assert.True(t, revisionOf(t, plan, "4").Current)
	three := revisionOf(t, plan, "3")
	assert.Contains(t, core.Texts(three.Details), "V=a")
	four := revisionOf(t, plan, "4").Value

	p := core.ActionParams{Choice: &three.Value}
	plan = c.prepare(ref, "undo", p)
	require.Nil(t, plan.Unavailable)
	assert.Equal(t, []string{"containers[nginx].env[V]: b → a"}, core.Texts(plan.Changes))
	assert.Equal(t, "5", plan.Effects[0].Params["next"])
	res, err := c.sess.RunAction(context.Background(), provider.ActionRun{Ref: plan.Where.Ref, Action: "undo", Params: p, Expect: plan.Expect})
	require.NoError(t, err)
	assert.Equal(t, "deployment web: rollback to revision 3 requested", res.Message.Text)
	assert.Equal(t, "a", env())
	assert.Equal(t, "V=a", c.jsonpath("deployment", "web", `{.metadata.annotations.kubernetes\.io/change-cause}`))
	require.Eventually(t, func() bool {
		return c.jsonpath("replicaset", three.Value, `{.metadata.annotations.deployment\.kubernetes\.io/revision}`) == "5"
	}, 30*time.Second, 200*time.Millisecond, "the revision rolled back to becomes the newest")

	// Back to V=b while that rollout goes on.
	res, err = c.act(ref, "undo", core.ActionParams{Choice: &four})
	require.NoError(t, err, "an undo during a rollout")
	assert.Contains(t, res.Message.Text, "rollback to revision 4 requested")
	assert.Equal(t, "b", env())
	c.kubectlNS("rollout", "status", "deployment/web", "--timeout=120s")
	assert.Equal(t, "6", c.jsonpath("replicaset", four, `{.metadata.annotations.deployment\.kubernetes\.io/revision}`))
}

// Pause: a template change makes no ReplicaSet (and undo waits for
// resume); resume rolls it out.
func TestKindActionPauseAndResume(t *testing.T) {
	c := kindActionCluster(t)
	ref := c.deployment("web", 1)
	rss := func() int {
		return len(strings.Fields(c.kubectlNS("get", "replicasets", "-l", "app=web", "-o", "jsonpath={.items[*].metadata.name}")))
	}
	before := rss()

	res, err := c.act(ref, "pause", core.ActionParams{})
	require.NoError(t, err)
	assert.Equal(t, "deployment web: rollout pause requested", res.Message.Text)
	assert.Equal(t, "true", c.jsonpath("deployment", "web", "{.spec.paused}"))
	plan := c.prepare(ref, "pause", core.ActionParams{})
	require.NotNil(t, plan.Unavailable, "already paused")
	plan = c.prepare(ref, "undo", core.ActionParams{})
	require.NotEmpty(t, plan.Choices)
	undo := c.prepare(ref, "undo", core.ActionParams{Choice: &plan.Choices[len(plan.Choices)-1].Value})
	require.NotNil(t, undo.Unavailable)
	assert.Equal(t, ProviderID+".unavailable.paused", undo.Unavailable.Key)

	c.kubectlNS("set", "env", "deployment/web", "V=paused")
	// The controller has seen the change (observedGeneration) and made no
	// ReplicaSet for it.
	gen := c.jsonpath("deployment", "web", "{.metadata.generation}")
	require.Eventually(t, func() bool { return c.jsonpath("deployment", "web", "{.status.observedGeneration}") == gen }, 30*time.Second, 200*time.Millisecond)
	assert.Equal(t, before, rss(), "paused: no new ReplicaSet")

	_, err = c.act(ref, "resume", core.ActionParams{})
	require.NoError(t, err)
	assert.NotEqual(t, "true", c.jsonpath("deployment", "web", "{.spec.paused}"))
	require.Eventually(t, func() bool { return rss() == before+1 }, 60*time.Second, 200*time.Millisecond, "resumed: the change rolls out")
	c.kubectlNS("rollout", "status", "deployment/web", "--timeout=120s")
}

// stubbornPod: a pod that ignores TERM for 5 minutes (a plain delete leaves
// it Terminating), with the given finalizers.
func (c *actionCluster) stubbornPod(name string, finalizers ...string) {
	c.t.Helper()
	fin := ""
	if len(finalizers) > 0 {
		fin = fmt.Sprintf("  finalizers: [%s]\n", strings.Join(finalizers, ", "))
	}
	c.apply(fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
%sspec:
  terminationGracePeriodSeconds: 300
  containers:
  - name: main
    image: busybox:1.36
    imagePullPolicy: IfNotPresent
    command: [sh, -c, "trap '' TERM; sleep 3600 & wait"]
`, name, fin))
	c.kubectlNS("wait", "--for=condition=Ready", "pod/"+name, "--timeout=120s")
}

func TestKindActionForceDeleteOfAPodStuckTerminating(t *testing.T) {
	c := kindActionCluster(t)
	c.stubbornPod("stubborn")
	ref := c.ref("pods", "stubborn")
	c.kubectlNS("delete", "pod", "stubborn", "--wait=false")
	require.NotEmpty(t, c.jsonpath("pod", "stubborn", "{.metadata.deletionTimestamp}"), "a plain delete leaves it Terminating")

	plan := c.prepare(ref, "forceDelete", core.ActionParams{})
	require.Nil(t, plan.Unavailable, "a deleting pod is force delete's case")
	assert.Contains(t, msgKeys(plan.Effects), ProviderID+".forceDelete.now")
	assert.Empty(t, plan.Warnings, "deleting, no finalizers, its node Ready")
	c.runPlan(plan)
	require.Eventually(t, func() bool {
		_, err := c.run("-n", c.ns, "get", "pod", "stubborn")
		return err != nil
	}, 10*time.Second, 200*time.Millisecond, "gone from the API at once, not after 300 s")
}

// A finalizer keeps a pod even from a forced deletion: the plan says so.
func TestKindActionForceDeleteIsHeldByAFinalizer(t *testing.T) {
	c := kindActionCluster(t)
	c.stubbornPod("held", "ocular.test/hold")
	t.Cleanup(func() {
		_, _ = c.run("-n", c.ns, "patch", "pod", "held", "--type=merge", "-p", `{"metadata":{"finalizers":null}}`)
	})
	ref := c.ref("pods", "held")
	plan := c.prepare(ref, "forceDelete", core.ActionParams{})
	require.Nil(t, plan.Unavailable)
	require.NotEmpty(t, plan.Effects)
	assert.Equal(t, ProviderID+".forceDelete.held", plan.Effects[0].Key)
	assert.Equal(t, "ocular.test/hold", plan.Effects[0].Params["finalizers"])
	assert.Equal(t, []string{ProviderID + ".forceDelete.heldWarn", ProviderID + ".forceDelete.notStuck"}, msgKeys(plan.Warnings))
	c.runPlan(plan)
	time.Sleep(2 * time.Second) // what a forced deletion would have removed by now
	assert.NotEmpty(t, c.jsonpath("pod", "held", "{.metadata.deletionTimestamp}"), "still there, deleting")
	assert.Equal(t, "0", c.jsonpath("pod", "held", "{.metadata.deletionGracePeriodSeconds}"), "the forced deletion was recorded")
}
