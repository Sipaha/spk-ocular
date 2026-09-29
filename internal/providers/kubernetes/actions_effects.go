package kubernetes

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/spk/spk-ocular/internal/core"
)

// What an action would do — only what is known, worded as requested and
// "may": a restart or a deletion is requested, the controllers act.

type actionEffects struct {
	effects, warnings []string
	destructive       bool
}

const reclaimNote = " (what happens to the data follows the volumes' reclaim policy)"

func effects(def *kindDef, action string, p core.ActionParams, u *unstructured.Unstructured) actionEffects {
	var fx actionEffects
	o := u.Object
	switch action {
	case actRestart.ID:
		fx.effects = append(fx.effects, restartEffect(def, o))
	case actScale.ID:
		n := replicas(o)
		if p.Count == nil {
			fx.effects = append(fx.effects, fmt.Sprintf("It has %d replicas now.", n))
			break
		}
		m := *p.Count
		switch {
		case m == n:
			fx.effects = append(fx.effects, fmt.Sprintf("%d → %d: the count does not change.", n, m))
		case m == 0:
			fx.effects = append(fx.effects, fmt.Sprintf("%d → 0: all pods stop.", n))
			fx.destructive = true
		case m < n:
			fx.effects = append(fx.effects, fmt.Sprintf("%d → %d: %s removed.", n, m, pods(n-m)))
		default:
			fx.effects = append(fx.effects, fmt.Sprintf("%d → %d: %s added.", n, m, pods(m-n)))
		}
		if def == statefulSetsKind && m < n && len(slice(o, "spec", "volumeClaimTemplates")) > 0 {
			start := int(i64(o, "spec", "ordinals", "start")) // pods are named from it
			if str(o, "spec", "persistentVolumeClaimRetentionPolicy", "whenScaled") == "Delete" {
				fx.effects = append(fx.effects, fmt.Sprintf("The PersistentVolumeClaims of %s are deleted%s.", ordinals(start+m, start+n-1), reclaimNote))
				fx.destructive = true
			} else if str(o, "spec", "persistentVolumeClaimRetentionPolicy", "whenDeleted") == "Delete" {
				fx.effects = append(fx.effects, "The PersistentVolumeClaims of removed pods are kept until the StatefulSet is deleted: then they are deleted with it"+reclaimNote+".")
			} else {
				fx.effects = append(fx.effects, "The PersistentVolumeClaims of removed pods are kept.")
			}
		}
	case actDelete.ID:
		if def == statefulSetsKind && len(slice(o, "spec", "volumeClaimTemplates")) > 0 {
			if str(o, "spec", "persistentVolumeClaimRetentionPolicy", "whenDeleted") == "Delete" {
				fx.effects = append(fx.effects, "The PersistentVolumeClaims of its pods are deleted"+reclaimNote+".")
			} else {
				fx.effects = append(fx.effects, "Its PersistentVolumeClaims are kept.")
			}
		}
		if def == replicaSetsKind {
			if c := metav1.GetControllerOf(u); c != nil {
				fx.effects = append(fx.effects, fmt.Sprintf("%s %s may create it again.", c.Kind, c.Name))
			}
		}
		if def == podsKind {
			fx.warnings = append(fx.warnings, "This is not an eviction: PodDisruptionBudgets are not consulted.")
		}
		fx.effects = append(fx.effects, "Deletion is requested: finalizers and grace periods may keep it for a while.")
	}
	return fx
}

// ordinals names the pods from..to of a StatefulSet.
func ordinals(from, to int) string {
	if from == to {
		return fmt.Sprintf("pod %d", from)
	}
	return fmt.Sprintf("pods %d–%d", from, to)
}

func pods(n int) string {
	if n == 1 {
		return "1 pod is"
	}
	return fmt.Sprintf("%d pods are", n)
}

func restartEffect(def *kindDef, o map[string]any) string {
	if def != daemonSetsKind && replicas(o) == 0 {
		return "It runs no pods: only the pod template changes."
	}
	switch def {
	case deploymentsKind:
		if str(o, "spec", "strategy", "type") == "Recreate" {
			return "All pods stop, then new ones start (strategy Recreate)."
		}
		return fmt.Sprintf("Pods are replaced gradually (rolling update: max unavailable %s, max surge %s).",
			intOrPercent(o, "25%", "spec", "strategy", "rollingUpdate", "maxUnavailable"), intOrPercent(o, "25%", "spec", "strategy", "rollingUpdate", "maxSurge"))
	case statefulSetsKind:
		if str(o, "spec", "updateStrategy", "type") == "OnDelete" {
			return "Existing pods keep running until they are deleted (update strategy OnDelete)."
		}
		if part := i64(o, "spec", "updateStrategy", "rollingUpdate", "partition"); part > 0 {
			return fmt.Sprintf("Only pods with ordinal %d and above are replaced, one at a time (partition %d).", part, part)
		}
		return "Pods are replaced one at a time, from the highest ordinal."
	case daemonSetsKind:
		if str(o, "spec", "updateStrategy", "type") == "OnDelete" {
			return "Existing pods keep running until they are deleted (update strategy OnDelete)."
		}
		return fmt.Sprintf("Pods are replaced node by node (max unavailable %s).", intOrPercent(o, "1", "spec", "updateStrategy", "rollingUpdate", "maxUnavailable"))
	}
	return "A restart is requested."
}

func intOrPercent(o map[string]any, def string, path ...string) string {
	switch v := fieldAt(o, path...).(type) {
	case string:
		return v
	case int64:
		return fmt.Sprint(v)
	case float64:
		return fmt.Sprint(int64(v))
	}
	return def
}

// podController says what may recreate a deleted pod.
func (s *session) podController(ctx context.Context, p *unstructured.Unstructured) string {
	c := metav1.GetControllerOf(p)
	if c == nil {
		return "It has no controller: nothing recreates it."
	}
	var def *kindDef
	switch c.Kind {
	case "ReplicaSet":
		def = replicaSetsKind
	case "StatefulSet":
		def = statefulSetsKind
	case "DaemonSet":
		def = daemonSetsKind
	case "Job":
		return fmt.Sprintf("Job %s may create a new pod if it has not completed.", c.Name)
	default:
		return fmt.Sprintf("It belongs to %s %s: whether it is recreated depends on that controller.", c.Kind, c.Name)
	}
	ctx, cancel := context.WithTimeout(ctx, prepareExtrasTimeout)
	defer cancel()
	owner, err := s.getObject(ctx, core.Ref{Kind: def.desc.ID, Scope: p.GetNamespace(), Name: c.Name, UID: string(c.UID)})
	switch {
	case err != nil && strings.Contains(err.Error(), "took its name"), err != nil && isNotFound(err):
		return fmt.Sprintf("Its %s %s no longer exists: nothing recreates it.", c.Kind, c.Name)
	case err != nil:
		return fmt.Sprintf("It belongs to %s %s, which could not be read: it may create a replacement.", c.Kind, c.Name)
	case owner.GetDeletionTimestamp() != nil:
		return fmt.Sprintf("Its %s %s is being deleted: a replacement is unlikely.", c.Kind, c.Name)
	case def != daemonSetsKind && replicas(owner.Object) == 0:
		return fmt.Sprintf("Its %s %s wants 0 pods: no replacement.", c.Kind, c.Name)
	}
	return fmt.Sprintf("%s %s normally creates a replacement.", c.Kind, c.Name)
}

func isNotFound(err error) bool { return strings.Contains(err.Error(), "not found") }

// maxCounted bounds counting a workload's pods.
const maxCounted = 500

// podsOf counts the pods a workload's selector matches now.
func (s *session) podsOf(ctx context.Context, u *unstructured.Unstructured) []string {
	sel, err := metav1.LabelSelectorAsSelector(labelSelectorOf(u.Object))
	if err != nil || sel.Empty() {
		return []string{"Its pods are deleted too."}
	}
	l, err := s.dyn.Resource(podsKind.gvr).Namespace(u.GetNamespace()).List(ctx, metav1.ListOptions{LabelSelector: sel.String(), Limit: maxCounted})
	switch {
	case err != nil:
		return []string{"Its pods are deleted too."}
	case l.GetContinue() != "":
		return []string{fmt.Sprintf("Its pods are deleted too (more than %d now).", maxCounted)}
	}
	return []string{fmt.Sprintf("Its pods are deleted too (%d now).", len(l.Items))}
}

func labelSelectorOf(o map[string]any) *metav1.LabelSelector {
	ml, _, _ := unstructured.NestedStringMap(o, "spec", "selector", "matchLabels")
	sel := &metav1.LabelSelector{MatchLabels: ml}
	for _, e := range slice(o, "spec", "selector", "matchExpressions") {
		sel.MatchExpressions = append(sel.MatchExpressions, metav1.LabelSelectorRequirement{
			Key: strOf(e, "key"), Operator: metav1.LabelSelectorOperator(strOf(e, "operator")), Values: strs(e, "values")})
	}
	return sel
}

var hpaGVRs = schema.GroupVersionResource{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"}

// maxHPAPages bounds reading autoscalers.
const maxHPAPages = 5

// autoscalers warns when an autoscaler targets u (it may override a
// manual count); failing to check is said, not taken for "none".
func (s *session) autoscalers(ctx context.Context, def *kindDef, u *unstructured.Unstructured) []string {
	kind := u.GetKind()
	if kind == "" {
		kind = map[*kindDef]string{deploymentsKind: "Deployment", statefulSetsKind: "StatefulSet"}[def]
	}
	res := s.dyn.Resource(hpaGVRs).Namespace(u.GetNamespace())
	cont := ""
	var out []string
	for page := 0; page < maxHPAPages; page++ {
		l, err := res.List(ctx, metav1.ListOptions{Limit: 500, Continue: cont})
		if err != nil {
			return append(out, fmt.Sprintf("The count could not be checked against autoscalers (could not check autoscalers: %s).", shortErr(err)))
		}
		for _, h := range l.Items {
			t := h.Object
			gv, _ := schema.ParseGroupVersion(str(t, "spec", "scaleTargetRef", "apiVersion"))
			if gv.Group == def.gvr.Group && str(t, "spec", "scaleTargetRef", "kind") == kind && str(t, "spec", "scaleTargetRef", "name") == u.GetName() {
				out = append(out, fmt.Sprintf("HorizontalPodAutoscaler %s may override the count (%d–%d).", h.GetName(), max(i64(t, "spec", "minReplicas"), 1), i64(t, "spec", "maxReplicas")))
			}
		}
		if cont = l.GetContinue(); cont == "" {
			return out
		}
	}
	return append(out, "Not every autoscaler could be checked (too many).")
}

func shortErr(err error) string {
	_, msg := classify(err)
	return msg
}

// rights asks the API server whether the action is allowed (RBAC); it
// cannot vouch for admission or quotas, and rights may change meanwhile.
func (s *session) rights(ctx context.Context, def *kindDef, action string, u *unstructured.Unstructured) core.Rights {
	verb, sub := "patch", ""
	switch action {
	case actScale.ID:
		sub = "scale"
	case actDelete.ID:
		verb = "delete"
	}
	attrs := map[string]any{"verb": verb, "group": def.gvr.Group, "resource": def.gvr.Resource, "namespace": u.GetNamespace(), "name": u.GetName()}
	if sub != "" {
		attrs["subresource"] = sub
	}
	review := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "authorization.k8s.io/v1", "kind": "SelfSubjectAccessReview",
		"spec": map[string]any{"resourceAttributes": attrs},
	}}
	out, err := s.dyn.Resource(ssarGVR).Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return core.Rights{State: core.RightsUnknown, Reason: shortErr(err)}
	}
	allowed, found, _ := unstructured.NestedBool(out.Object, "status", "allowed")
	if !found {
		return core.Rights{State: core.RightsUnknown}
	}
	if allowed {
		return core.Rights{State: core.RightsAllowed}
	}
	what := def.gvr.Resource
	if sub != "" {
		what += "/" + sub
	}
	reason := fmt.Sprintf("you may not %s %s in %s", verb, what, u.GetNamespace())
	if r := str(out.Object, "status", "reason"); r != "" {
		reason += " (" + r + ")"
	}
	return core.Rights{State: core.RightsDenied, Reason: reason}
}

var ssarGVR = schema.GroupVersionResource{Group: "authorization.k8s.io", Version: "v1", Resource: "selfsubjectaccessreviews"}
