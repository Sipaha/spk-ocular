package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

var (
	stsGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}
	dsGVR  = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}
	hpaGVR = schema.GroupVersionResource{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"}
)

func actionClient(objs ...kruntime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(kruntime.NewScheme(), map[schema.GroupVersionResource]string{
		podGVR: "PodList", deployGVR: "DeploymentList", rsGVR: "ReplicaSetList", stsGVR: "StatefulSetList", dsGVR: "DaemonSetList",
		svcGVR: "ServiceList", hpaGVR: "HorizontalPodAutoscalerList",
	}, objs...)
}

// workload is a Deployment/StatefulSet/DaemonSet in ns with spec (merged).
func workload(kind, name, uid, rv string, spec map[string]any) *unstructured.Unstructured {
	s := map[string]any{"template": map[string]any{"metadata": map[string]any{"annotations": map[string]any{"team": "a"}}}}
	for k, v := range spec {
		s[k] = v
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1", "kind": kind,
		"metadata": map[string]any{"name": name, "namespace": "ns", "uid": uid, "resourceVersion": rv},
		"spec":     s,
	}}
}

var deployWebRef = core.Ref{Provider: ProviderID, Target: "ctx", Scope: "ns", Kind: "apps/deployments", Name: "web", UID: "uid-web"}

func actionSession(t *testing.T, objs ...kruntime.Object) (*session, *dynamicfake.FakeDynamicClient) {
	t.Helper()
	c := actionClient(objs...)
	s := newSession("ctx", "h", c, false)
	t.Cleanup(s.Close)
	return s, c
}

// expectNow is the Expect a plan made now would carry.
func expectNow(t *testing.T, s *session, ref core.Ref, action string, p core.ActionParams) string {
	t.Helper()
	u, err := s.getObject(context.Background(), ref)
	require.NoError(t, err)
	return actionExpect(s.kinds.byID[ref.Kind], action, p, u)
}

func writes(c *dynamicfake.FakeDynamicClient) []k8stesting.Action {
	var out []k8stesting.Action
	for _, a := range c.Actions() {
		if a.GetResource().Resource == "selfsubjectaccessreviews" { // a question, not a change
			continue
		}
		if v := a.GetVerb(); v == "patch" || v == "delete" || v == "update" || v == "create" {
			out = append(out, a)
		}
	}
	return out
}

func count(n int) core.ActionParams { return core.ActionParams{Count: &n} }

func TestRestartPatchesTheTemplateOfThatObjectOnly(t *testing.T) {
	s, c := actionSession(t, workload("Deployment", "web", "uid-web", "7", map[string]any{"replicas": int64(2)}))
	before := time.Now()
	res, err := s.RunAction(context.Background(), provider.ActionRun{Ref: deployWebRef, Action: "restart", Expect: expectNow(t, s, deployWebRef, "restart", core.ActionParams{})})
	require.NoError(t, err)
	assert.Contains(t, res.Message, "restart requested")
	w := writes(c)
	require.Len(t, w, 1)
	p := w[0].(k8stesting.PatchAction)
	assert.Equal(t, types.MergePatchType, p.GetPatchType())
	assert.Equal(t, "web", p.GetName())
	var body struct {
		Metadata struct{ UID, ResourceVersion string } `json:"metadata"`
		Spec     struct {
			Template struct {
				Metadata struct{ Annotations map[string]string } `json:"metadata"`
			} `json:"template"`
		} `json:"spec"`
	}
	require.NoError(t, json.Unmarshal(p.GetPatch(), &body))
	assert.Equal(t, "uid-web", body.Metadata.UID, "the object's identity is a precondition")
	assert.Equal(t, "7", body.Metadata.ResourceVersion, "and the version it was checked at")
	at, err := time.Parse(time.RFC3339Nano, body.Spec.Template.Metadata.Annotations[restartedAtKey])
	require.NoError(t, err)
	assert.False(t, at.Before(before.Truncate(time.Second)))
	assert.Len(t, body.Spec.Template.Metadata.Annotations, 1, "a merge patch: other annotations are left alone")
}

func TestScaleTestsIdentityVersionAndTheCountItWasShown(t *testing.T) {
	s, c := actionSession(t, workload("Deployment", "web", "uid-web", "7", map[string]any{"replicas": int64(2)}))
	_, err := s.RunAction(context.Background(), provider.ActionRun{Ref: deployWebRef, Action: "scale", Params: count(5), Expect: expectNow(t, s, deployWebRef, "scale", count(5))})
	require.NoError(t, err)
	w := writes(c)
	require.Len(t, w, 1)
	p := w[0].(k8stesting.PatchAction)
	assert.Equal(t, types.JSONPatchType, p.GetPatchType())
	assert.Equal(t, "scale", p.GetSubresource())
	assert.JSONEq(t, `[{"op":"test","path":"/metadata/uid","value":"uid-web"},{"op":"test","path":"/metadata/resourceVersion","value":"7"},
		{"op":"test","path":"/spec/replicas","value":2},{"op":"replace","path":"/spec/replicas","value":5}]`, string(p.GetPatch()))
}

func TestDeleteHasUIDAndVersionPreconditionsAndCascadesInTheBackground(t *testing.T) {
	s, c := actionSession(t, workload("Deployment", "web", "uid-web", "7", nil))
	res, err := s.RunAction(context.Background(), provider.ActionRun{Ref: deployWebRef, Action: "delete", Expect: expectNow(t, s, deployWebRef, "delete", core.ActionParams{})})
	require.NoError(t, err)
	assert.Contains(t, res.Message, "deletion requested")
	w := writes(c)
	require.Len(t, w, 1)
	d := w[0].(k8stesting.DeleteAction)
	o := d.GetDeleteOptions()
	require.NotNil(t, o.Preconditions)
	assert.Equal(t, types.UID("uid-web"), *o.Preconditions.UID)
	assert.Equal(t, "7", *o.Preconditions.ResourceVersion)
	assert.Equal(t, metav1.DeletePropagationBackground, *o.PropagationPolicy)
}

// A plan binds its action, parameters and the state its effects depend
// on: anything else is refused before a write.
func TestARunNotMatchingItsPlanWritesNothing(t *testing.T) {
	s, c := actionSession(t, workload("Deployment", "web", "uid-web", "7", map[string]any{"replicas": int64(2)}))
	ctx := context.Background()
	scale3 := expectNow(t, s, deployWebRef, "scale", count(3))
	for name, run := range map[string]provider.ActionRun{
		"another count":  {Ref: deployWebRef, Action: "scale", Params: count(0), Expect: scale3},
		"another action": {Ref: deployWebRef, Action: "delete", Expect: scale3},
		"no uid":         {Ref: core.Ref{Provider: ProviderID, Target: "ctx", Scope: "ns", Kind: "apps/deployments", Name: "web"}, Action: "scale", Params: count(3), Expect: scale3},
		"unknown action": {Ref: deployWebRef, Action: "explode", Expect: scale3},
		"extra params":   {Ref: deployWebRef, Action: "restart", Params: count(3), Expect: scale3},
	} {
		_, err := s.RunAction(ctx, run)
		assert.Error(t, err, name)
	}
	// the replicas change after the plan: the shown "2 → 3" no longer holds
	u, err := c.Resource(deployGVR).Namespace("ns").Get(ctx, "web", metav1.GetOptions{})
	require.NoError(t, err)
	require.NoError(t, unstructured.SetNestedField(u.Object, int64(9), "spec", "replicas"))
	_, err = c.Resource(deployGVR).Namespace("ns").Update(ctx, u, metav1.UpdateOptions{})
	require.NoError(t, err)
	_, err = s.RunAction(ctx, provider.ActionRun{Ref: deployWebRef, Action: "scale", Params: count(3), Expect: scale3})
	assertClass(t, err, provider.ClassConflict)
	assert.Len(t, writes(c), 1, "only the test's own update")
}

func TestAPausedDeploymentIsNotRestarted(t *testing.T) {
	s, c := actionSession(t, workload("Deployment", "web", "uid-web", "7", map[string]any{"paused": true}))
	_, err := s.RunAction(context.Background(), provider.ActionRun{Ref: deployWebRef, Action: "restart", Expect: expectNow(t, s, deployWebRef, "restart", core.ActionParams{})})
	assertClass(t, err, provider.ClassConflict)
	assert.Empty(t, writes(c))
}

func TestAReplacedObjectIsGone(t *testing.T) {
	s, c := actionSession(t, workload("Deployment", "web", "uid-new", "9", nil))
	_, err := s.RunAction(context.Background(), provider.ActionRun{Ref: deployWebRef, Action: "delete", Expect: "x"})
	assertClass(t, err, provider.ClassGone)
	assert.Empty(t, writes(c))
}

// failWrite makes the first n writes fail with err after running mutate
// (the cluster changing under the write).
func failWrite(c *dynamicfake.FakeDynamicClient, verb string, n int, mutate func(), err error) *int {
	calls := 0
	c.PrependReactor(verb, "*", func(k8stesting.Action) (bool, kruntime.Object, error) {
		calls++
		if calls > n {
			return false, nil, nil
		}
		if mutate != nil {
			mutate()
		}
		return true, nil, err
	})
	return &calls
}

// bumpVersion changes the stored object directly (reactors run under the
// fake client's lock: going through the client would deadlock).
func bumpVersion(t *testing.T, c *dynamicfake.FakeDynamicClient, gvr schema.GroupVersionResource, name, rv string, change func(u *unstructured.Unstructured)) func() {
	return func() {
		o, err := c.Tracker().Get(gvr, "ns", name)
		require.NoError(t, err)
		u := o.(*unstructured.Unstructured).DeepCopy()
		u.SetResourceVersion(rv)
		if change != nil {
			change(u)
		}
		require.NoError(t, c.Tracker().Update(gvr, u, "ns"))
	}
}

var (
	conflict409 = apierrors.NewConflict(schema.GroupResource{Group: "apps", Resource: "deployments"}, "web", errors.New("the object has been modified"))
	invalid422  = apierrors.NewInvalid(schema.GroupKind{Group: "apps", Kind: "Deployment"}, "web", nil)
)

// A failed version precondition is retried only when the object is proven
// unchanged but for its version (nothing was written).
func TestAVersionOnlyChangeIsRetried(t *testing.T) {
	for _, c := range []struct {
		action string
		verb   string
		fail   error
		params core.ActionParams
	}{
		{"delete", "delete", conflict409, core.ActionParams{}},
		{"restart", "patch", conflict409, core.ActionParams{}},
		{"scale", "patch", invalid422, count(4)}, // a failed JSON Patch test
	} {
		t.Run(c.action, func(t *testing.T) {
			s, cl := actionSession(t, workload("Deployment", "web", "uid-web", "7", map[string]any{"replicas": int64(2)}))
			calls := failWrite(cl, c.verb, 1, bumpVersion(t, cl, deployGVR, "web", "8", func(u *unstructured.Unstructured) {
				_ = unstructured.SetNestedField(u.Object, int64(1), "status", "readyReplicas") // status only
			}), c.fail)
			_, err := s.RunAction(context.Background(), provider.ActionRun{Ref: deployWebRef, Action: c.action, Params: c.params, Expect: expectNow(t, s, deployWebRef, c.action, c.params)})
			require.NoError(t, err)
			assert.Equal(t, 2, *calls, "retried once, at the new version")
		})
	}
}

func TestAFailedWriteIsClassifiedByLookingAgain(t *testing.T) {
	cases := map[string]struct {
		fail   error
		mutate func(t *testing.T, c *dynamicfake.FakeDynamicClient) func()
		class  provider.ErrorClass
	}{
		"replaced between read and write": {invalid422, func(t *testing.T, c *dynamicfake.FakeDynamicClient) func() {
			return func() {
				require.NoError(t, c.Tracker().Delete(deployGVR, "ns", "web"))
				require.NoError(t, c.Tracker().Create(deployGVR, workload("Deployment", "web", "uid-new", "1", map[string]any{"replicas": int64(2)}), "ns"))
			}
		}, provider.ClassGone},
		"deleted between read and write": {apierrors.NewNotFound(schema.GroupResource{Group: "apps", Resource: "deployments"}, "web"), nil, provider.ClassGone},
		"replicas changed between read and write": {invalid422, func(t *testing.T, c *dynamicfake.FakeDynamicClient) func() {
			return bumpVersion(t, c, deployGVR, "web", "8", func(u *unstructured.Unstructured) {
				_ = unstructured.SetNestedField(u.Object, int64(6), "spec", "replicas")
			})
		}, provider.ClassConflict},
		"rejected, nothing changed": {invalid422, nil, provider.ClassInvalid},
		"forbidden":                 {apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, "web", errors.New("rbac")), nil, provider.ClassForbidden},
		"connection lost":           {errors.New("read tcp: connection reset by peer"), nil, provider.ClassUnknown},
		"timeout":                   {context.DeadlineExceeded, nil, provider.ClassUnknown},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s, cl := actionSession(t, workload("Deployment", "web", "uid-web", "7", map[string]any{"replicas": int64(2)}))
			var mutate func()
			if c.mutate != nil {
				mutate = c.mutate(t, cl)
			}
			calls := failWrite(cl, "patch", 5, mutate, c.fail)
			_, err := s.RunAction(context.Background(), provider.ActionRun{Ref: deployWebRef, Action: "scale", Params: count(4), Expect: expectNow(t, s, deployWebRef, "scale", count(4))})
			assertClass(t, err, c.class)
			assert.Equal(t, 1, *calls, "never retried")
		})
	}
}

func TestVersionRetriesAreBounded(t *testing.T) {
	s, cl := actionSession(t, workload("Deployment", "web", "uid-web", "7", nil))
	n := 0
	calls := failWrite(cl, "delete", 10, func() {
		n++
		bumpVersion(t, cl, deployGVR, "web", "v"+string(rune('a'+n)), nil)()
	}, conflict409)
	_, err := s.RunAction(context.Background(), provider.ActionRun{Ref: deployWebRef, Action: "delete", Expect: expectNow(t, s, deployWebRef, "delete", core.ActionParams{})})
	assertClass(t, err, provider.ClassConflict)
	assert.Equal(t, maxVersionRetries+1, *calls)
}

// Only a failed precondition may be retried: a 409, or a 422 of a scale's
// JSON Patch test. A validation refusal of a merge patch is kept even when
// the version moved meanwhile.
func TestAValidationRefusalIsNotRetriedEvenWhenTheVersionMoved(t *testing.T) {
	s, cl := actionSession(t, workload("Deployment", "web", "uid-web", "7", map[string]any{"replicas": int64(2)}))
	calls := failWrite(cl, "patch", 5, bumpVersion(t, cl, deployGVR, "web", "8", func(u *unstructured.Unstructured) {
		_ = unstructured.SetNestedField(u.Object, int64(1), "status", "readyReplicas")
	}), invalid422)
	_, err := s.RunAction(context.Background(), provider.ActionRun{Ref: deployWebRef, Action: "restart", Expect: expectNow(t, s, deployWebRef, "restart", core.ActionParams{})})
	assertClass(t, err, provider.ClassInvalid)
	assert.Equal(t, 1, *calls)
}
