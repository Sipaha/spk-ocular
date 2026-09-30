package kubernetes

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// podTemplate: one container "app" with env V=v.
func podTemplate(v string) map[string]any {
	return map[string]any{
		"metadata": map[string]any{"labels": map[string]any{"app": "web"}},
		"spec": map[string]any{"containers": []any{map[string]any{
			"name": "app", "image": "busybox:1.36", "env": []any{map[string]any{"name": "V", "value": v}},
		}}},
	}
}

// rolloutDeployment: web, selector app=web, the template of podTemplate(v).
func rolloutDeployment(rv, v string, spec map[string]any) *unstructured.Unstructured {
	s := map[string]any{"replicas": int64(2), "selector": map[string]any{"matchLabels": map[string]any{"app": "web"}}, "template": podTemplate(v)}
	for k, x := range spec {
		s[k] = x
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"name": "web", "namespace": "ns", "uid": "uid-web", "resourceVersion": rv},
		"spec":     s,
	}}
}

// revisionRS: a ReplicaSet of web (owner uid) at revision rev, its
// template podTemplate(v) with a pod-template-hash label.
func revisionRS(name, uid, owner, rev, v string, annotations map[string]any) *unstructured.Unstructured {
	t := podTemplate(v)
	t["metadata"].(map[string]any)["labels"].(map[string]any)["pod-template-hash"] = name[len(name)-4:]
	ann := map[string]any{"deployment.kubernetes.io/revision": rev}
	for k, x := range annotations {
		ann[k] = x
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1", "kind": "ReplicaSet",
		"metadata": map[string]any{
			"name": name, "namespace": "ns", "uid": uid, "resourceVersion": "1", "creationTimestamp": "2026-09-30T10:00:00Z",
			"labels": map[string]any{"app": "web", "pod-template-hash": name[len(name)-4:]}, "annotations": ann,
			"ownerReferences": []any{map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "web", "uid": owner, "controller": true}},
		},
		"spec":   map[string]any{"replicas": int64(0), "template": t},
		"status": map[string]any{"replicas": int64(0)},
	}}
}

// history: web at v3 (rev 3 current), revisions 1 (v1, a change-cause) and
// 2 (v2) scaled to zero, and a ReplicaSet of another owner.
func history(depSpec map[string]any) []kruntime.Object {
	cur := revisionRS("web-ccc3", "uid-rs3", "uid-web", "3", "v3", nil)
	_ = unstructured.SetNestedField(cur.Object, int64(2), "spec", "replicas")
	_ = unstructured.SetNestedField(cur.Object, int64(2), "status", "replicas")
	_ = unstructured.SetNestedField(cur.Object, int64(1), "status", "readyReplicas")
	return []kruntime.Object{
		rolloutDeployment("7", "v3", depSpec),
		revisionRS("web-aaa1", "uid-rs1", "uid-web", "1", "v1", map[string]any{changeCauseKey: "deploy v1"}),
		revisionRS("web-bbb2", "uid-rs2", "uid-web", "2", "v2", nil),
		cur,
		revisionRS("api-ddd4", "uid-rs4", "uid-api", "9", "v9", nil),
	}
}

func choice(v string) core.ActionParams { return core.ActionParams{Choice: &v} }

func TestUndoOffersTheRevisionsNewestFirst(t *testing.T) {
	s, _ := actionSession(t, history(nil)...)
	plan := prepare(t, s, deployWebRef, "undo", core.ActionParams{})
	require.Nil(t, plan.Unavailable)
	var values []string
	for _, c := range plan.Choices {
		values = append(values, c.Value)
	}
	assert.Equal(t, []string{"web-ccc3", "web-bbb2", "web-aaa1"}, values, "its own ReplicaSets, scaled to zero too, by revision")
	cur, old := plan.Choices[0], plan.Choices[2]
	assert.True(t, cur.Current)
	require.NotNil(t, cur.Unavailable, "the current template is not a choice")
	assert.Equal(t, ProviderID+".undo.isCurrent", cur.Unavailable.Key)
	assert.Equal(t, core.Message{Key: ProviderID + ".undo.revision", Params: map[string]string{"revision": "1"}, Text: "Revision 1"}, old.Title)
	assert.False(t, old.Current)
	assert.Nil(t, old.Unavailable)
	assert.Equal(t, int64(1790762400000), old.At)
	assert.Contains(t, core.Texts(old.Details), "deploy v1")
	assert.Contains(t, core.Texts(old.Details), "Images: busybox:1.36")
	assert.Contains(t, core.Texts(cur.Details), "Pods: 1 of 2 ready")
	assert.Len(t, old.Details, 2, "no pods: not said")
	assert.Empty(t, plan.Changes, "nothing chosen: nothing changes yet")

	_, err := s.RunAction(context.Background(), provider.ActionRun{Ref: deployWebRef, Action: "undo", Expect: plan.Expect})
	assertSaid(t, err, provider.ClassInvalid, "")
}

func TestUndoPlanSaysWhatChanges(t *testing.T) {
	s, _ := actionSession(t, history(map[string]any{"strategy": map[string]any{"type": "Recreate"}})...)
	plan := prepare(t, s, deployWebRef, "undo", choice("web-aaa1"))
	require.Nil(t, plan.Unavailable)
	assert.False(t, plan.Destructive)
	require.Len(t, plan.Effects, 2)
	assert.Equal(t, map[string]string{"revision": "1", "next": "4"}, plan.Effects[0].Params)
	assert.Equal(t, ProviderID+".restart.recreate", plan.Effects[1].Key)
	assert.Equal(t, []string{"containers[app].env[V]: v3 → v1"}, core.Texts(plan.Changes))
	assert.Empty(t, plan.Lists, "changes are not objects: an agent's grants judge lists")
	assert.Len(t, plan.Choices, 3, "the choices stay with a choice made")
}

func TestUndoIsUnavailable(t *testing.T) {
	for name, c := range map[string]struct {
		spec   map[string]any
		choice string
		key    string
	}{
		"paused":      {map[string]any{"paused": true}, "web-aaa1", "unavailable.paused"},
		"the current": {nil, "web-ccc3", "undo.isCurrent"},
		"not offered": {nil, "api-ddd4", "undo.gone"},
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := actionSession(t, history(c.spec)...)
			plan := prepare(t, s, deployWebRef, "undo", choice(c.choice))
			require.NotNil(t, plan.Unavailable)
			assert.Equal(t, ProviderID+"."+c.key, plan.Unavailable.Key)
			assert.Empty(t, plan.Changes)
		})
	}
}

// jsonPatchOps decodes the one JSON Patch written.
func jsonPatchOps(t *testing.T, c *dynamicfake.FakeDynamicClient) []map[string]any {
	t.Helper()
	w := writes(c)
	require.Len(t, w, 1)
	p := w[0].(k8stesting.PatchAction)
	require.Equal(t, types.JSONPatchType, p.GetPatchType())
	require.Equal(t, "web", p.GetName())
	var ops []map[string]any
	require.NoError(t, json.Unmarshal(p.GetPatch(), &ops))
	return ops
}

func runUndoOf(t *testing.T, s *session, rs string) (core.ActionResult, error) {
	t.Helper()
	plan := prepare(t, s, deployWebRef, "undo", choice(rs))
	require.Nil(t, plan.Unavailable)
	return s.RunAction(context.Background(), provider.ActionRun{Ref: deployWebRef, Action: "undo", Params: choice(rs), Expect: plan.Expect})
}

// The revision's template without its pod-template-hash, the identity
// tested, the version as an update precondition (a stale one is a 409; a
// failed test op is a bare 422), the change-cause as the revision's.
func TestUndoWritesTheRevisionsTemplateInOneJSONPatch(t *testing.T) {
	s, c := actionSession(t, history(nil)...)
	res, err := runUndoOf(t, s, "web-aaa1")
	require.NoError(t, err)
	assertDone(t, res, "done.undo", map[string]string{"kind": "deployment", "name": "web", "revision": "1"})
	assert.Equal(t, []map[string]any{
		{"op": "test", "path": "/metadata/uid", "value": "uid-web"},
		{"op": "replace", "path": "/metadata/resourceVersion", "value": "7"},
		{"op": "replace", "path": "/spec/template", "value": jsonOf(t, podTemplate("v1"))},
		{"op": "add", "path": "/metadata/annotations", "value": map[string]any{changeCauseKey: "deploy v1"}},
	}, jsonPatchOps(t, c))
}

func TestUndoKeepsTheChangeCauseOfTheRevision(t *testing.T) {
	withCause := func(objs []kruntime.Object, cause string) []kruntime.Object {
		objs[0].(*unstructured.Unstructured).SetAnnotations(map[string]string{changeCauseKey: cause, "team": "a"})
		return objs
	}
	t.Run("replaced", func(t *testing.T) {
		s, c := actionSession(t, withCause(history(nil), "deploy v3")...)
		_, err := runUndoOf(t, s, "web-aaa1")
		require.NoError(t, err)
		ops := jsonPatchOps(t, c)
		assert.Equal(t, map[string]any{"op": "add", "path": "/metadata/annotations/kubernetes.io~1change-cause", "value": "deploy v1"}, ops[3])
	})
	t.Run("removed", func(t *testing.T) {
		s, c := actionSession(t, withCause(history(nil), "deploy v3")...)
		_, err := runUndoOf(t, s, "web-bbb2")
		require.NoError(t, err)
		ops := jsonPatchOps(t, c)
		assert.Equal(t, map[string]any{"op": "remove", "path": "/metadata/annotations/kubernetes.io~1change-cause"}, ops[3])
	})
	t.Run("none either", func(t *testing.T) {
		s, c := actionSession(t, history(nil)...)
		_, err := runUndoOf(t, s, "web-bbb2")
		require.NoError(t, err)
		assert.Len(t, jsonPatchOps(t, c), 3)
	})
}

func jsonOf(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	var out any
	require.NoError(t, json.Unmarshal(b, &out))
	return out
}

// What the plan showed is checked against the ReplicaSets read at the run.
func TestUndoRefusesWhatChangedSinceThePlan(t *testing.T) {
	for name, c := range map[string]struct {
		change func(t *testing.T, c *dynamicfake.FakeDynamicClient)
		key    string
	}{
		"the revision is gone": {func(t *testing.T, c *dynamicfake.FakeDynamicClient) {
			require.NoError(t, c.Tracker().Delete(rsGVR, "ns", "web-aaa1"))
		}, "undo.gone"},
		"the revision was replaced": {func(t *testing.T, c *dynamicfake.FakeDynamicClient) {
			require.NoError(t, c.Tracker().Delete(rsGVR, "ns", "web-aaa1"))
			require.NoError(t, c.Tracker().Create(rsGVR, revisionRS("web-aaa1", "uid-rs1b", "uid-web", "1", "v1", nil), "ns"))
		}, "error.changed"},
		"its template changed": {func(t *testing.T, c *dynamicfake.FakeDynamicClient) {
			bumpVersion(t, c, rsGVR, "web-aaa1", "2", func(u *unstructured.Unstructured) {
				_ = unstructured.SetNestedField(u.Object, "busybox:1.37", "spec", "template", "spec", "containers", "0", "image")
				u.Object["spec"].(map[string]any)["template"] = revisionRS("web-aaa1", "uid-rs1", "uid-web", "1", "v0", nil).Object["spec"].(map[string]any)["template"]
			})()
		}, "error.changed"},
		"the deployment's template changed": {func(t *testing.T, c *dynamicfake.FakeDynamicClient) {
			bumpVersion(t, c, deployGVR, "web", "8", func(u *unstructured.Unstructured) {
				u.Object["spec"].(map[string]any)["template"] = podTemplate("v4")
			})()
		}, "error.changed"},
		"paused": {func(t *testing.T, c *dynamicfake.FakeDynamicClient) {
			bumpVersion(t, c, deployGVR, "web", "8", func(u *unstructured.Unstructured) {
				_ = unstructured.SetNestedField(u.Object, true, "spec", "paused")
			})()
		}, "unavailable.paused"},
	} {
		t.Run(name, func(t *testing.T) {
			s, cl := actionSession(t, history(nil)...)
			plan := prepare(t, s, deployWebRef, "undo", choice("web-aaa1"))
			c.change(t, cl)
			_, err := s.RunAction(context.Background(), provider.ActionRun{Ref: deployWebRef, Action: "undo", Params: choice("web-aaa1"), Expect: plan.Expect})
			assertSaid(t, err, provider.ClassConflict, c.key)
			for _, w := range writes(cl) {
				assert.NotEqual(t, "patch", w.GetVerb(), "nothing written")
			}
		})
	}
}

// A version moved by status churn (a rollout in progress) is retried, the
// patch rebuilt from the new read.
func TestUndoRetriesAVersionOnlyChange(t *testing.T) {
	s, c := actionSession(t, history(nil)...)
	plan := prepare(t, s, deployWebRef, "undo", choice("web-aaa1"))
	var bodies [][]map[string]any
	c.PrependReactor("patch", "deployments", func(a k8stesting.Action) (bool, kruntime.Object, error) {
		var ops []map[string]any
		require.NoError(t, json.Unmarshal(a.(k8stesting.PatchAction).GetPatch(), &ops))
		bodies = append(bodies, ops)
		if len(bodies) > 1 {
			return false, nil, nil
		}
		bumpVersion(t, c, deployGVR, "web", "8", func(u *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(u.Object, int64(2), "status", "readyReplicas")
		})()
		bumpVersion(t, c, rsGVR, "web-aaa1", "5", nil)() // the ReplicaSet's own churn
		return true, nil, conflict409
	})
	_, err := s.RunAction(context.Background(), provider.ActionRun{Ref: deployWebRef, Action: "undo", Params: choice("web-aaa1"), Expect: plan.Expect})
	require.NoError(t, err)
	require.Len(t, bodies, 2)
	assert.Equal(t, "7", bodies[0][1]["value"])
	assert.Equal(t, "8", bodies[1][1]["value"], "the version of this attempt's read")
}

// A failed uid test (422) of an object replaced meanwhile is said as such;
// a 422 of an unchanged object stays a refusal.
func TestUndoA422IsClassifiedByLookingAgain(t *testing.T) {
	t.Run("replaced", func(t *testing.T) {
		s, c := actionSession(t, history(nil)...)
		calls := failWrite(c, "patch", 5, func() {
			require.NoError(t, c.Tracker().Delete(deployGVR, "ns", "web"))
			u := rolloutDeployment("1", "v3", nil)
			u.SetUID("uid-new")
			require.NoError(t, c.Tracker().Create(deployGVR, u, "ns"))
		}, invalid422)
		_, err := runUndoOf(t, s, "web-aaa1")
		assertSaid(t, err, provider.ClassGone, "error.replacedMeanwhile")
		assert.Equal(t, 1, *calls)
	})
	t.Run("refused", func(t *testing.T) {
		s, c := actionSession(t, history(nil)...)
		calls := failWrite(c, "patch", 5, nil, invalid422)
		_, err := runUndoOf(t, s, "web-aaa1")
		assertSaid(t, err, provider.ClassInvalid, "")
		assert.Equal(t, 1, *calls)
	})
	t.Run("the revision went meanwhile", func(t *testing.T) {
		s, c := actionSession(t, history(nil)...)
		calls := failWrite(c, "patch", 5, func() {
			require.NoError(t, c.Tracker().Delete(rsGVR, "ns", "web-aaa1"))
		}, conflict409)
		_, err := runUndoOf(t, s, "web-aaa1")
		assertSaid(t, err, provider.ClassConflict, "error.changed")
		assert.Equal(t, 1, *calls)
	})
}

// The difference is of the templates alone: a reference is said as one,
// never as a value.
func TestTemplateChangesArePathsAndReferences(t *testing.T) {
	from := podTemplate("a")
	to := podTemplate("b")
	c := to["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
	c["image"] = "busybox:1.37"
	c["env"] = append(c["env"].([]any),
		map[string]any{"name": "TOKEN", "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": "creds", "key": "token"}}},
		map[string]any{"name": "MODE", "valueFrom": map[string]any{"configMapKeyRef": map[string]any{"name": "cfg", "key": "mode"}}})
	to["spec"].(map[string]any)["containers"] = append(to["spec"].(map[string]any)["containers"].([]any), map[string]any{"name": "side", "image": "x"})
	to["metadata"].(map[string]any)["annotations"] = map[string]any{"example.com/v": "2"}
	to["spec"].(map[string]any)["serviceAccountName"] = "robot"
	from["spec"].(map[string]any)["volumes"] = []any{map[string]any{"name": "data", "emptyDir": map[string]any{}}}
	assert.Equal(t, []string{
		`metadata.annotations["example.com/v"]: added (2)`,
		"containers[app].env[V]: a → b",
		"containers[app].env[TOKEN]: added (secretKeyRef creds/token)",
		"containers[app].env[MODE]: added (configMapKeyRef cfg/mode)",
		"containers[app].image: busybox:1.36 → busybox:1.37",
		"containers[side]: added",
		"serviceAccountName: added (robot)",
		"volumes[data]: removed",
	}, core.Texts(templateChanges(from, to)))
	assert.Empty(t, templateChanges(from, from))

	// A named list gone or come whole is said item by item.
	bare := podTemplate("a")
	delete(bare["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any), "env")
	assert.Equal(t, []string{"containers[app].env[V]: removed"}, core.Texts(templateChanges(podTemplate("a"), bare)))
	assert.Equal(t, []string{"containers[app].env[V]: added (a)"}, core.Texts(templateChanges(bare, podTemplate("a"))))
}

func TestPauseAndResumeAreMergePatchesOfPaused(t *testing.T) {
	for _, c := range []struct {
		action string
		paused bool
	}{{"pause", false}, {"resume", true}} {
		t.Run(c.action, func(t *testing.T) {
			s, cl := actionSession(t, rolloutDeployment("7", "v1", map[string]any{"paused": c.paused}))
			plan := prepare(t, s, deployWebRef, c.action, core.ActionParams{})
			require.Nil(t, plan.Unavailable)
			assert.NotEmpty(t, plan.Effects)
			res, err := s.RunAction(context.Background(), provider.ActionRun{Ref: deployWebRef, Action: c.action, Expect: plan.Expect})
			require.NoError(t, err)
			assertDone(t, res, "done."+c.action, map[string]string{"kind": "deployment", "name": "web"})
			w := writes(cl)
			require.Len(t, w, 1)
			p := w[0].(k8stesting.PatchAction)
			assert.Equal(t, types.MergePatchType, p.GetPatchType())
			assert.JSONEq(t, `{"metadata":{"uid":"uid-web","resourceVersion":"7"},"spec":{"paused":`+map[bool]string{true: "true", false: "false"}[!c.paused]+`}}`, string(p.GetPatch()))
		})
	}
}

func TestPauseAndResumeFollowThePausedState(t *testing.T) {
	for _, c := range []struct {
		action string
		paused bool
		key    string
	}{{"pause", true, "unavailable.alreadyPaused"}, {"resume", false, "unavailable.notPaused"}} {
		t.Run(c.action, func(t *testing.T) {
			s, cl := actionSession(t, rolloutDeployment("7", "v1", map[string]any{"paused": c.paused}))
			plan := prepare(t, s, deployWebRef, c.action, core.ActionParams{})
			require.NotNil(t, plan.Unavailable)
			assert.Equal(t, ProviderID+"."+c.key, plan.Unavailable.Key)
			_, err := s.RunAction(context.Background(), provider.ActionRun{Ref: deployWebRef, Action: c.action, Expect: plan.Expect})
			assertSaid(t, err, provider.ClassConflict, c.key)
			assert.Empty(t, writes(cl))
		})
	}
}

// A pause planned before the spec changed is refused (its generation).
func TestAPauseAfterASpecChangeIsAConflict(t *testing.T) {
	d := rolloutDeployment("7", "v1", nil)
	d.SetGeneration(3)
	s, c := actionSession(t, d)
	plan := prepare(t, s, deployWebRef, "pause", core.ActionParams{})
	bumpVersion(t, c, deployGVR, "web", "8", func(u *unstructured.Unstructured) { u.SetGeneration(4) })()
	_, err := s.RunAction(context.Background(), provider.ActionRun{Ref: deployWebRef, Action: "pause", Expect: plan.Expect})
	assertSaid(t, err, provider.ClassConflict, "error.changed")
}

// Undo asks for both what it writes and what it reads.
func TestUndoRightsAskForTheReplicaSetsToo(t *testing.T) {
	s, c := actionSession(t, history(nil)...)
	var asked []map[string]any
	c.PrependReactor("create", "selfsubjectaccessreviews", func(a k8stesting.Action) (bool, kruntime.Object, error) {
		u := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).DeepCopy()
		ra, _, _ := unstructured.NestedMap(u.Object, "spec", "resourceAttributes")
		asked = append(asked, ra)
		u.Object["status"] = map[string]any{"allowed": ra["resource"] == "deployments"}
		return true, u, nil
	})
	plan := prepare(t, s, deployWebRef, "undo", choice("web-aaa1"))
	assert.Equal(t, core.RightsDenied, plan.Rights.State)
	assert.Contains(t, plan.Rights.Reason, "list replicasets in ns")
	assert.Len(t, asked, 2)
}
