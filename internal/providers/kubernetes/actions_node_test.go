package kubernetes

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

var nodeGVR = schema.GroupVersionResource{Version: "v1", Resource: "nodes"}

// node is a Node, cordoned or not.
func node(name, uid, rv string, unschedulable bool) *unstructured.Unstructured {
	spec := map[string]any{}
	if unschedulable {
		spec["unschedulable"] = true
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Node",
		"metadata": map[string]any{"name": name, "uid": uid, "resourceVersion": rv},
		"spec":     spec,
	}}
}

func nodeRef(name, uid string) core.Ref {
	return core.Ref{Provider: ProviderID, Target: "ctx", Kind: "nodes", Name: name, UID: uid}
}

func nodeSession(t *testing.T, objs ...kruntime.Object) (*session, *dynamicfake.FakeDynamicClient) {
	t.Helper()
	c := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(kruntime.NewScheme(), map[schema.GroupVersionResource]string{
		podGVR: "PodList", nodeGVR: "NodeList",
	}, objs...)
	s := newSession("ctx", "h", c, false)
	t.Cleanup(s.Close)
	return s, c
}

func TestNodesOfferTheirActions(t *testing.T) {
	var ids []string
	for _, a := range kindActions[nodesKind] {
		ids = append(ids, a.ID)
	}
	assert.Equal(t, []string{"cordon", "uncordon"}, ids)
}

func TestCordonAndUncordonPlans(t *testing.T) {
	s, _ := nodeSession(t, node("w1", "uid-w1", "5", false), node("w2", "uid-w2", "6", true))

	plan := prepare(t, s, nodeRef("w1", ""), "cordon", core.ActionParams{})
	assert.Nil(t, plan.Unavailable)
	assert.False(t, plan.Destructive)
	assert.Equal(t, "uid-w1", plan.Where.Ref.UID)
	assert.Contains(t, text(plan), "no new pods are scheduled on node w1")
	assert.Contains(t, text(plan), "nodename", "cordon does not hold back pods that name the node")

	plan = prepare(t, s, nodeRef("w1", ""), "uncordon", core.ActionParams{})
	require.NotNil(t, plan.Unavailable, "an open node cannot be opened")
	assert.Equal(t, "kubernetes.unavailable.schedulable", plan.Unavailable.Key)

	plan = prepare(t, s, nodeRef("w2", ""), "cordon", core.ActionParams{})
	require.NotNil(t, plan.Unavailable, "a cordoned node cannot be cordoned")
	assert.Equal(t, "kubernetes.unavailable.cordoned", plan.Unavailable.Key)

	plan = prepare(t, s, nodeRef("w2", ""), "uncordon", core.ActionParams{})
	assert.Nil(t, plan.Unavailable)
	assert.Contains(t, text(plan), "pods may be scheduled on node w2 again")
}

func TestCordonRightsAskForTheNodeByName(t *testing.T) {
	s, c := nodeSession(t, node("w1", "uid-w1", "5", false))
	asked := ssarAnswer(c, false, nil)
	plan := prepare(t, s, nodeRef("w1", ""), "cordon", core.ActionParams{})
	assert.Equal(t, map[string]any{"verb": "patch", "group": "", "resource": "nodes", "namespace": "", "name": "w1"}, *asked)
	assert.Equal(t, core.RightsDenied, plan.Rights.State)
	assert.Equal(t, "you may not patch nodes (RBAC: no rule)", plan.Rights.Reason, "a cluster-scoped object has no namespace to name")
}

func TestCordonIsAMergePatchWithIdentityAndVersion(t *testing.T) {
	for _, tc := range []struct {
		action string
		was    bool
		body   string
	}{
		{"cordon", false, `{"metadata":{"uid":"uid-w1","resourceVersion":"5"},"spec":{"unschedulable":true}}`},
		{"uncordon", true, `{"metadata":{"uid":"uid-w1","resourceVersion":"5"},"spec":{"unschedulable":false}}`},
	} {
		t.Run(tc.action, func(t *testing.T) {
			s, c := nodeSession(t, node("w1", "uid-w1", "5", tc.was))
			ref := nodeRef("w1", "uid-w1")
			res, err := s.RunAction(context.Background(), provider.ActionRun{Ref: ref, Action: tc.action, Expect: expectNow(t, s, ref, tc.action, core.ActionParams{})})
			require.NoError(t, err)
			assert.Contains(t, res.Message, "node w1: "+tc.action+" requested")
			w := writes(c)
			require.Len(t, w, 1)
			p := w[0].(k8stesting.PatchAction)
			assert.Equal(t, "", p.GetSubresource())
			assert.JSONEq(t, tc.body, string(p.GetPatch()))
		})
	}
}

// Cordoned by another hand after the plan: the plan's promise no longer
// holds — a conflict, nothing written.
func TestACordonAfterThePlanIsAConflict(t *testing.T) {
	s, c := nodeSession(t, node("w1", "uid-w1", "5", false))
	ref := nodeRef("w1", "uid-w1")
	exp := expectNow(t, s, ref, "cordon", core.ActionParams{})
	require.NoError(t, c.Tracker().Update(nodeGVR, node("w1", "uid-w1", "6", true), ""))
	_, err := s.RunAction(context.Background(), provider.ActionRun{Ref: ref, Action: "cordon", Expect: exp})
	var pe *provider.Error
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, provider.ClassConflict, pe.Class)
	assert.Empty(t, writes(c))
}

// A version-only change (status churn) between the read and the write is
// retried with the new version, as for the other patches.
func TestACordonRacingStatusChurnIsRetried(t *testing.T) {
	s, c := nodeSession(t, node("w1", "uid-w1", "5", false))
	ref := nodeRef("w1", "uid-w1")
	exp := expectNow(t, s, ref, "cordon", core.ActionParams{})
	bump := func() { require.NoError(t, c.Tracker().Update(nodeGVR, node("w1", "uid-w1", "9", false), "")) }
	failWrite(c, "patch", 1, bump, apierrors.NewConflict(schema.GroupResource{Resource: "nodes"}, "w1", assert.AnError))
	_, err := s.RunAction(context.Background(), provider.ActionRun{Ref: ref, Action: "cordon", Expect: exp})
	require.NoError(t, err)
	w := writes(c)
	require.Len(t, w, 2)
	assert.JSONEq(t, `{"metadata":{"uid":"uid-w1","resourceVersion":"9"},"spec":{"unschedulable":true}}`, string(w[1].(k8stesting.PatchAction).GetPatch()))
}
