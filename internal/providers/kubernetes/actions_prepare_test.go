package kubernetes

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stesting "k8s.io/client-go/testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

func refOf(kind, name, uid string) core.Ref {
	return core.Ref{Provider: ProviderID, Target: "ctx", Scope: "ns", Kind: kind, Name: name, UID: uid}
}

// text is the plan's effects and warnings, lowercased (the tests match
// phrases).
func text(plan core.ActionPlan) string {
	return strings.ToLower(strings.Join(plan.Effects, "\n") + "\n--\n" + strings.Join(plan.Warnings, "\n"))
}

func prepare(t *testing.T, s *session, ref core.Ref, action string, p core.ActionParams) core.ActionPlan {
	t.Helper()
	plan, err := s.PrepareAction(context.Background(), ref, action, p)
	require.NoError(t, err)
	return plan
}

func TestRestartEffectsFollowTheUpdateStrategy(t *testing.T) {
	cases := []struct {
		name, kind string
		obj        *unstructured.Unstructured
		want       string
	}{
		{"deployment rolling", "apps/deployments", workload("Deployment", "w", "u", "1", map[string]any{"replicas": int64(3),
			"strategy": map[string]any{"type": "RollingUpdate", "rollingUpdate": map[string]any{"maxUnavailable": "25%", "maxSurge": int64(1)}}}), "replaced gradually"},
		{"deployment recreate", "apps/deployments", workload("Deployment", "w", "u", "1", map[string]any{"replicas": int64(3), "strategy": map[string]any{"type": "Recreate"}}), "all pods stop"},
		{"deployment at zero", "apps/deployments", workload("Deployment", "w", "u", "1", map[string]any{"replicas": int64(0)}), "only the pod template changes"},
		{"statefulset rolling", "apps/statefulsets", workload("StatefulSet", "w", "u", "1", map[string]any{"replicas": int64(3)}), "one at a time"},
		{"statefulset partition", "apps/statefulsets", workload("StatefulSet", "w", "u", "1", map[string]any{"replicas": int64(3),
			"updateStrategy": map[string]any{"type": "RollingUpdate", "rollingUpdate": map[string]any{"partition": int64(2)}}}), "ordinal 2 and above"},
		{"statefulset on delete", "apps/statefulsets", workload("StatefulSet", "w", "u", "1", map[string]any{"replicas": int64(3), "updateStrategy": map[string]any{"type": "OnDelete"}}), "keep running until they are deleted"},
		{"daemonset on delete", "apps/daemonsets", workload("DaemonSet", "w", "u", "1", map[string]any{"updateStrategy": map[string]any{"type": "OnDelete"}}), "keep running until they are deleted"},
		{"daemonset rolling", "apps/daemonsets", workload("DaemonSet", "w", "u", "1", nil), "node by node"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, _ := actionSession(t, c.obj)
			plan := prepare(t, s, refOf(c.kind, "w", "u"), "restart", core.ActionParams{})
			assert.Contains(t, text(plan), strings.ToLower(c.want))
			assert.False(t, plan.Destructive)
		})
	}
}

func claims(whenDeleted, whenScaled string) map[string]any {
	spec := map[string]any{"replicas": int64(3), "volumeClaimTemplates": []any{map[string]any{"metadata": map[string]any{"name": "data"}}}}
	if whenDeleted != "" {
		spec["persistentVolumeClaimRetentionPolicy"] = map[string]any{"whenDeleted": whenDeleted, "whenScaled": whenScaled}
	}
	return spec
}

func TestStatefulSetClaimsFollowTheRetentionPolicy(t *testing.T) {
	sts := func(spec map[string]any) *unstructured.Unstructured {
		return workload("StatefulSet", "db", "u", "1", spec)
	}
	ref := refOf("apps/statefulsets", "db", "u")
	cases := []struct {
		name        string
		obj         *unstructured.Unstructured
		action      string
		p           core.ActionParams
		want        string
		destructive bool
	}{
		{"delete, no policy", sts(claims("", "")), "delete", core.ActionParams{}, "claims are kept", true},
		{"delete, retain", sts(claims("Retain", "Delete")), "delete", core.ActionParams{}, "claims are kept", true},
		{"delete, delete", sts(claims("Delete", "Retain")), "delete", core.ActionParams{}, "claims of its pods are deleted", true},
		{"delete, no templates", sts(map[string]any{"replicas": int64(3)}), "delete", core.ActionParams{}, "", true},
		{"scale down, delete", sts(claims("Retain", "Delete")), "scale", count(1), "claims of pods 1–2 are deleted", true},
		{"scale down by one, delete", sts(claims("Retain", "Delete")), "scale", count(2), "claims of pod 2 are deleted", true},
		{"scale down, retain", sts(claims("Retain", "Retain")), "scale", count(1), "claims of removed pods are kept.", false},
		{"scale down, retain, deleted with it", sts(claims("Delete", "Retain")), "scale", count(1), "kept until the StatefulSet is deleted", false},
		{"scale up, delete", sts(claims("Retain", "Delete")), "scale", count(5), "3 → 5", false},
		{"scale to zero", sts(claims("", "")), "scale", count(0), "all pods stop", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, _ := actionSession(t, c.obj)
			plan := prepare(t, s, ref, c.action, c.p)
			if c.want == "" {
				assert.NotContains(t, text(plan), "claim")
			} else {
				assert.Contains(t, text(plan), strings.ToLower(c.want))
			}
			assert.Equal(t, c.destructive, plan.Destructive)
			if strings.Contains(c.want, "deleted") {
				assert.Contains(t, text(plan), "reclaim policy", "the data's fate is the volumes', not promised")
			}
		})
	}
}

// The retention policy changes while the dialog is open: the run is
// refused (the shown promise about the claims no longer holds).
func TestARetentionPolicyChangeAfterThePlanIsAConflict(t *testing.T) {
	s, c := actionSession(t, workload("StatefulSet", "db", "u", "1", claims("Retain", "Retain")))
	ref := refOf("apps/statefulsets", "db", "u")
	plan := prepare(t, s, ref, "delete", core.ActionParams{})
	bumpVersion(t, c, stsGVR, "db", "2", func(u *unstructured.Unstructured) {
		_ = unstructured.SetNestedField(u.Object, "Delete", "spec", "persistentVolumeClaimRetentionPolicy", "whenDeleted")
	})()
	_, err := s.RunAction(context.Background(), provider.ActionRun{Ref: plan.Where.Ref, Action: "delete", Expect: plan.Expect})
	assertClass(t, err, provider.ClassConflict)
	assert.Empty(t, writes(c))
}

func owned(p *unstructured.Unstructured, kind, name, uid string) *unstructured.Unstructured {
	p.SetOwnerReferences(nil)
	_ = unstructured.SetNestedSlice(p.Object, []any{map[string]any{"apiVersion": "apps/v1", "kind": kind, "name": name, "uid": uid, "controller": true}}, "metadata", "ownerReferences")
	return p
}

func TestDeletingAPodSaysWhatMayRecreateIt(t *testing.T) {
	rs := func(replicas int64, deleting bool) *unstructured.Unstructured {
		u := workload("ReplicaSet", "web-1", "rs-uid", "1", map[string]any{"replicas": replicas})
		if deleting {
			u.Object["metadata"].(map[string]any)["deletionTimestamp"] = "2026-09-30T00:00:00Z"
		}
		return u
	}
	cases := []struct {
		name string
		objs []kruntime.Object
		want string
	}{
		{"replicaset", []kruntime.Object{owned(pod("ns", "p", "pu"), "ReplicaSet", "web-1", "rs-uid"), rs(2, false)}, "ReplicaSet web-1 normally creates a replacement"},
		{"replicaset at zero", []kruntime.Object{owned(pod("ns", "p", "pu"), "ReplicaSet", "web-1", "rs-uid"), rs(0, false)}, "wants 0 pods"},
		{"replicaset being deleted", []kruntime.Object{owned(pod("ns", "p", "pu"), "ReplicaSet", "web-1", "rs-uid"), rs(2, true)}, "is being deleted"},
		{"replicaset gone", []kruntime.Object{owned(pod("ns", "p", "pu"), "ReplicaSet", "web-1", "rs-uid")}, "no longer exists"},
		{"job", []kruntime.Object{owned(pod("ns", "p", "pu"), "Job", "j", "ju")}, "Job j may create a new pod"},
		{"unknown owner", []kruntime.Object{owned(pod("ns", "p", "pu"), "Rollout", "r", "ru")}, "depends on that controller"},
		{"bare", []kruntime.Object{pod("ns", "p", "pu")}, "nothing recreates it"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, _ := actionSession(t, c.objs...)
			plan := prepare(t, s, refOf("pods", "p", "pu"), "delete", core.ActionParams{})
			assert.Contains(t, text(plan), strings.ToLower(c.want))
			assert.Contains(t, text(plan), "not an eviction")
			assert.True(t, plan.Destructive)
		})
	}
}

func TestDeletingAWorkloadCountsItsPodsAsSeenNow(t *testing.T) {
	d := workload("Deployment", "web", "uid-web", "1", map[string]any{"replicas": int64(2), "selector": map[string]any{"matchLabels": map[string]any{"app": "web"}}})
	sel := labelled(map[string]any{"app": "web"})
	s, _ := actionSession(t, d, pod("ns", "a", "a", sel), pod("ns", "b", "b", sel), pod("ns", "other", "o"))
	plan := prepare(t, s, deployWebRef, "delete", core.ActionParams{})
	assert.Contains(t, text(plan), "its pods are deleted too (2 now)")
}

func hpa(name string, target map[string]any, minR, maxR int64) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "autoscaling/v2", "kind": "HorizontalPodAutoscaler",
		"metadata": map[string]any{"name": name, "namespace": "ns", "uid": "h-" + name},
		"spec":     map[string]any{"scaleTargetRef": target, "minReplicas": minR, "maxReplicas": maxR}}}
}

func TestScaleWarnsOfAnAutoscaler(t *testing.T) {
	d := workload("Deployment", "web", "uid-web", "1", map[string]any{"replicas": int64(2)})
	onWeb := hpa("web", map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "web"}, 2, 10)
	onSts := hpa("sts", map[string]any{"apiVersion": "apps/v1", "kind": "StatefulSet", "name": "web"}, 1, 3)
	t.Run("targets it", func(t *testing.T) {
		s, _ := actionSession(t, d, onWeb)
		assert.Contains(t, text(prepare(t, s, deployWebRef, "scale", count(3))), "horizontalpodautoscaler web may override the count (2–10)")
	})
	t.Run("targets something else", func(t *testing.T) {
		s, _ := actionSession(t, d, onSts)
		assert.NotContains(t, text(prepare(t, s, deployWebRef, "scale", count(3))), "autoscaler")
	})
	t.Run("cannot be listed", func(t *testing.T) {
		s, c := actionSession(t, d)
		c.PrependReactor("list", "horizontalpodautoscalers", func(k8stesting.Action) (bool, kruntime.Object, error) {
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "autoscaling", Resource: "horizontalpodautoscalers"}, "", errors.New("rbac"))
		})
		plan := prepare(t, s, deployWebRef, "scale", count(3))
		assert.Contains(t, text(plan), "could not check autoscalers")
	})
}

// ssarAnswer makes SelfSubjectAccessReview answer allowed / denied, or
// fail; it records the attributes asked.
func ssarAnswer(c interface {
	PrependReactor(verb, resource string, fn k8stesting.ReactionFunc)
}, allowed bool, fail error) *map[string]any {
	asked := map[string]any{}
	c.PrependReactor("create", "selfsubjectaccessreviews", func(a k8stesting.Action) (bool, kruntime.Object, error) {
		if fail != nil {
			return true, nil, fail
		}
		u := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).DeepCopy()
		attrs, _, _ := unstructured.NestedMap(u.Object, "spec", "resourceAttributes")
		for k, v := range attrs {
			asked[k] = v
		}
		st := map[string]any{"allowed": allowed}
		if !allowed {
			st["reason"] = "RBAC: no rule"
		}
		u.Object["status"] = st
		return true, u, nil
	})
	return &asked
}

func TestRightsAreCheckedWithSeparateAttributes(t *testing.T) {
	d := workload("Deployment", "web", "uid-web", "1", map[string]any{"replicas": int64(2)})
	t.Run("scale allowed", func(t *testing.T) {
		s, c := actionSession(t, d)
		asked := ssarAnswer(c, true, nil)
		plan := prepare(t, s, deployWebRef, "scale", count(3))
		assert.Equal(t, core.RightsAllowed, plan.Rights.State)
		assert.Equal(t, map[string]any{"verb": "patch", "group": "apps", "resource": "deployments", "subresource": "scale", "namespace": "ns", "name": "web"}, *asked)
	})
	t.Run("delete denied", func(t *testing.T) {
		s, c := actionSession(t, d)
		asked := ssarAnswer(c, false, nil)
		plan := prepare(t, s, deployWebRef, "delete", core.ActionParams{})
		assert.Equal(t, core.RightsDenied, plan.Rights.State)
		assert.Contains(t, plan.Rights.Reason, "delete deployments in ns")
		assert.Equal(t, "delete", (*asked)["verb"])
		assert.Nil(t, (*asked)["subresource"])
	})
	t.Run("check failed", func(t *testing.T) {
		s, c := actionSession(t, d)
		ssarAnswer(c, false, errors.New("boom"))
		plan := prepare(t, s, deployWebRef, "restart", core.ActionParams{})
		assert.Equal(t, core.RightsUnknown, plan.Rights.State)
	})
}

func TestAPlanNamesItsTargetAndPinsTheObject(t *testing.T) {
	s, _ := actionSession(t, workload("Deployment", "web", "uid-web", "1", map[string]any{"replicas": int64(2)}))
	noUID := deployWebRef
	noUID.UID = ""
	plan := prepare(t, s, noUID, "scale", core.ActionParams{})
	assert.Equal(t, "uid-web", plan.Where.Ref.UID)
	assert.Equal(t, "h", plan.Where.ConfigHash)
	require.NotNil(t, plan.Current)
	assert.Equal(t, 2, *plan.Current)
	assert.NotEmpty(t, plan.Expect)
	_, err := s.PrepareAction(context.Background(), deployWebRef, "scale", count(maxReplicas+1))
	assertClass(t, err, provider.ClassInvalid)
	_, err = s.PrepareAction(context.Background(), refOf("namespaces", "ns", ""), "delete", core.ActionParams{})
	assertClass(t, err, provider.ClassUnsupported)
}

func TestAPausedOrDeletingObjectIsUnavailableInThePlan(t *testing.T) {
	d := workload("Deployment", "web", "uid-web", "1", map[string]any{"paused": true})
	s, _ := actionSession(t, d)
	assert.Contains(t, prepare(t, s, deployWebRef, "restart", core.ActionParams{}).Unavailable, "paused")
	assert.Empty(t, prepare(t, s, deployWebRef, "delete", core.ActionParams{}).Unavailable)
}
