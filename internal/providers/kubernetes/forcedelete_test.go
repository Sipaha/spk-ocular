package kubernetes

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

var stuckRef = core.Ref{Provider: ProviderID, Target: "ctx", Scope: "ns", Kind: "pods", Name: "db-0", UID: "uid-db-0"}

// stuckPod is pod db-0 on node w1, owned by StatefulSet db when owner is
// set; deleting and finalizers as given.
func stuckPod(uid, rv string, deleting bool, finalizers []any, owner string) *unstructured.Unstructured {
	md := map[string]any{"name": "db-0", "namespace": "ns", "uid": uid, "resourceVersion": rv}
	if deleting {
		md["deletionTimestamp"] = "2026-10-01T00:00:00Z"
		md["deletionGracePeriodSeconds"] = int64(30)
	}
	if finalizers != nil {
		md["finalizers"] = finalizers
	}
	if owner != "" {
		md["ownerReferences"] = []any{map[string]any{"apiVersion": "apps/v1", "kind": owner, "name": "db", "uid": "uid-sts", "controller": true}}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Pod", "metadata": md,
		"spec":   map[string]any{"nodeName": "w1", "containers": []any{map[string]any{"name": "db", "image": "db:1"}}},
		"status": map[string]any{"phase": "Running"},
	}}
}

// readyNode is node w1 with its Ready condition.
func readyNode(status string) *unstructured.Unstructured {
	n := node("w1", "uid-w1", "3", false)
	n.Object["status"] = map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": status}}}
	return n
}

func forceSession(t *testing.T, objs ...kruntime.Object) (*session, *dynamicfake.FakeDynamicClient) {
	t.Helper()
	c := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(kruntime.NewScheme(), map[schema.GroupVersionResource]string{
		podGVR: "PodList", nodeGVR: "NodeList", stsGVR: "StatefulSetList",
	}, objs...)
	s := newSession("ctx", "h", c, false)
	t.Cleanup(s.Close)
	return s, c
}

func forceRun(s *session, plan core.ActionPlan) (core.ActionResult, error) {
	return s.RunAction(context.Background(), provider.ActionRun{Ref: plan.Where.Ref, Action: "forceDelete", Expect: plan.Expect})
}

func TestPodsOfferForceDeleteAfterDelete(t *testing.T) {
	var ids []string
	for _, a := range kindActions[podsKind] {
		ids = append(ids, a.ID)
	}
	assert.Equal(t, []string{"debug", "delete", "forceDelete"}, ids)
	assert.True(t, actForceDelete.Destructive)
	assert.False(t, actForceDelete.Single, "stuck pods on a gone node come in batches")
}

// kubectl delete --force --grace-period=0: no wait for the kubelet; the UID
// pins the object, not its version (a deleting pod's version moves while
// the kubelet stops it).
func TestForceDeleteSendsGraceZeroWithTheUIDOnly(t *testing.T) {
	s, c := forceSession(t, stuckPod("uid-db-0", "7", true, nil, ""), readyNode("False"))
	plan := prepare(t, s, stuckRef, "forceDelete", core.ActionParams{})
	require.Nil(t, plan.Unavailable, "a deleting pod is force delete's main case")
	res, err := forceRun(s, plan)
	require.NoError(t, err)
	assertDone(t, res, "done.forceDelete", map[string]string{"name": "db-0"})
	w := writes(c)
	require.Len(t, w, 1)
	o := w[0].(k8stesting.DeleteAction).GetDeleteOptions()
	require.NotNil(t, o.GracePeriodSeconds)
	assert.Equal(t, int64(0), *o.GracePeriodSeconds)
	require.NotNil(t, o.Preconditions)
	assert.Equal(t, "uid-db-0", string(*o.Preconditions.UID))
	assert.Nil(t, o.Preconditions.ResourceVersion)
	assert.Equal(t, metav1.DeletePropagationBackground, *o.PropagationPolicy)
}

func TestForceDeleteOfAStaticPodIsUnavailable(t *testing.T) {
	p := stuckPod("uid-db-0", "7", false, nil, "")
	p.SetAnnotations(map[string]string{mirrorKey: "abc"})
	s, _ := forceSession(t, p, readyNode("True"))
	plan := prepare(t, s, stuckRef, "forceDelete", core.ActionParams{})
	require.NotNil(t, plan.Unavailable)
	assert.Equal(t, ProviderID+".forceDelete.mirror", plan.Unavailable.Key)
}

func TestForceDeletePlanSaysWhatHappens(t *testing.T) {
	// Running, no finalizers, a StatefulSet's, its node Ready.
	s, _ := forceSession(t, stuckPod("uid-db-0", "7", false, nil, "StatefulSet"), readyNode("True"))
	plan := prepare(t, s, stuckRef, "forceDelete", core.ActionParams{})
	assert.True(t, plan.Destructive)
	assert.Contains(t, msgKeys(plan.Effects), ProviderID+".forceDelete.now")
	assert.Equal(t, []string{ProviderID + ".forceDelete.notStuck", ProviderID + ".forceDelete.statefulSet"}, msgKeys(plan.Warnings))

	// Deleting and held by finalizers: it stays, said as an effect and a warning.
	s, _ = forceSession(t, stuckPod("uid-db-0", "7", true, []any{"example.com/b", "example.com/a"}, ""), readyNode("True"))
	plan = prepare(t, s, stuckRef, "forceDelete", core.ActionParams{})
	assert.NotContains(t, msgKeys(plan.Effects), ProviderID+".forceDelete.now")
	held := plan.Effects[0]
	assert.Equal(t, ProviderID+".forceDelete.held", held.Key)
	assert.Equal(t, "example.com/a, example.com/b", held.Params["finalizers"])
	assert.Equal(t, []string{ProviderID + ".forceDelete.heldWarn"}, msgKeys(plan.Warnings))

	// Its node: not Ready, gone.
	for status, key := range map[string]string{"False": "nodeNotReady", "Unknown": "nodeNotReady", "": "nodeGone"} {
		objs := []kruntime.Object{stuckPod("uid-db-0", "7", true, nil, "")}
		if status != "" {
			objs = append(objs, readyNode(status))
		}
		s, _ = forceSession(t, objs...)
		plan = prepare(t, s, stuckRef, "forceDelete", core.ActionParams{})
		assert.Equal(t, []string{ProviderID + ".forceDelete." + key}, msgKeys(plan.Warnings), status)
		assert.Equal(t, "w1", plan.Warnings[0].Params["node"])
	}
}

// The finalizers are bound (the effect reads them); the node's readiness is
// advice, not bound: a flapping node never turns runs into conflicts.
func TestForceDeleteBindsTheFinalizersNotTheNode(t *testing.T) {
	s, c := forceSession(t, stuckPod("uid-db-0", "7", true, []any{"example.com/a"}, ""), readyNode("False"))
	plan := prepare(t, s, stuckRef, "forceDelete", core.ActionParams{})
	require.NoError(t, c.Tracker().Update(nodeGVR, readyNode("True"), ""))
	_, err := forceRun(s, plan)
	require.NoError(t, err, "the node became Ready: not a conflict")

	s, c = forceSession(t, stuckPod("uid-db-0", "7", true, []any{"example.com/a"}, ""), readyNode("True"))
	plan = prepare(t, s, stuckRef, "forceDelete", core.ActionParams{})
	require.NoError(t, c.Tracker().Update(podGVR, stuckPod("uid-db-0", "8", true, []any{}, ""), "ns"))
	_, err = forceRun(s, plan)
	assertSaid(t, err, provider.ClassConflict, "error.changed")
	assert.Empty(t, writes(c))

	// Another pod took its name: nothing is written.
	s, c = forceSession(t, stuckPod("uid-db-0", "7", true, nil, ""), readyNode("True"))
	plan = prepare(t, s, stuckRef, "forceDelete", core.ActionParams{})
	require.NoError(t, c.Tracker().Update(podGVR, stuckPod("uid-db-0b", "9", false, nil, ""), "ns"))
	_, err = forceRun(s, plan)
	assertSaid(t, err, provider.ClassGone, "error.replaced")
	assert.Empty(t, writes(c))
}

func TestForceDeleteRightsAreThoseOfADelete(t *testing.T) {
	s, c := forceSession(t, stuckPod("uid-db-0", "7", true, nil, ""), readyNode("True"))
	var asked []map[string]any
	c.PrependReactor("create", "selfsubjectaccessreviews", func(a k8stesting.Action) (bool, kruntime.Object, error) {
		u := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).DeepCopy()
		ra, _, _ := unstructured.NestedMap(u.Object, "spec", "resourceAttributes")
		asked = append(asked, ra)
		u.Object["status"] = map[string]any{"allowed": true}
		return true, u, nil
	})
	plan := prepare(t, s, stuckRef, "forceDelete", core.ActionParams{})
	assert.Equal(t, core.RightsAllowed, plan.Rights.State)
	require.Len(t, asked, 1)
	assert.Equal(t, map[string]any{"verb": "delete", "group": "", "resource": "pods", "namespace": "ns", "name": "db-0"}, asked[0])
}
