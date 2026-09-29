package kubernetes

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/spk/spk-ocular/internal/core"
)

// What an action would do — only what is known, worded as requested and
// "may": a restart or a deletion is requested, the controllers act.

type actionEffects struct {
	effects, warnings []core.Message
	destructive       bool
}

func effects(def *kindDef, action string, p core.ActionParams, u *unstructured.Unstructured) actionEffects {
	var fx actionEffects
	o := u.Object
	switch action {
	case actRestart.ID:
		fx.effects = append(fx.effects, restartEffect(def, o))
	case actScale.ID:
		n := replicas(o)
		if p.Count == nil {
			fx.effects = append(fx.effects, msg("scale.now", "count", n))
			break
		}
		m := *p.Count
		switch {
		case m == n:
			fx.effects = append(fx.effects, msg("scale.same", "from", n, "to", m))
		case m == 0:
			fx.effects = append(fx.effects, msg("scale.zero", "from", n))
			fx.destructive = true
		case m < n:
			fx.effects = append(fx.effects, countMsg("scale.downOne", "scale.down", n-m, "from", n, "to", m))
		default:
			fx.effects = append(fx.effects, countMsg("scale.upOne", "scale.up", m-n, "from", n, "to", m))
		}
		if def == statefulSetsKind && m < n && len(slice(o, "spec", "volumeClaimTemplates")) > 0 {
			start := int(i64(o, "spec", "ordinals", "start")) // pods are named from it
			if str(o, "spec", "persistentVolumeClaimRetentionPolicy", "whenScaled") == "Delete" {
				if first, last := start+m, start+n-1; first == last {
					fx.effects = append(fx.effects, msg("claims.deletedOne", "first", first))
				} else {
					fx.effects = append(fx.effects, msg("claims.deleted", "first", first, "last", last))
				}
				fx.destructive = true
			} else if str(o, "spec", "persistentVolumeClaimRetentionPolicy", "whenDeleted") == "Delete" {
				fx.effects = append(fx.effects, msg("claims.keptUntil"))
			} else {
				fx.effects = append(fx.effects, msg("claims.keptScaled"))
			}
		}
	case actDelete.ID:
		if def == statefulSetsKind && len(slice(o, "spec", "volumeClaimTemplates")) > 0 {
			if str(o, "spec", "persistentVolumeClaimRetentionPolicy", "whenDeleted") == "Delete" {
				fx.effects = append(fx.effects, msg("claims.deletedWith"))
			} else {
				fx.effects = append(fx.effects, msg("claims.kept"))
			}
		}
		if def == replicaSetsKind {
			if c := metav1.GetControllerOf(u); c != nil {
				fx.effects = append(fx.effects, msg("delete.recreatedBy", "ownerKind", c.Kind, "owner", c.Name))
			}
		}
		if def == podsKind {
			fx.warnings = append(fx.warnings, msg("delete.notEviction"))
		}
		fx.effects = append(fx.effects, msg("delete.requested"))
	}
	return fx
}

// countMsg: one (key1) or several (keyN, with the count).
func countMsg(key1, keyN string, n int, kv ...any) core.Message {
	if n == 1 {
		return msg(key1, kv...)
	}
	return msg(keyN, append(kv, "count", n)...)
}

func restartEffect(def *kindDef, o map[string]any) core.Message {
	if def != daemonSetsKind && replicas(o) == 0 {
		return msg("restart.noPods")
	}
	switch def {
	case deploymentsKind:
		if str(o, "spec", "strategy", "type") == "Recreate" {
			return msg("restart.recreate")
		}
		return msg("restart.rolling",
			"maxUnavailable", intOrPercent(o, "25%", "spec", "strategy", "rollingUpdate", "maxUnavailable"), "maxSurge", intOrPercent(o, "25%", "spec", "strategy", "rollingUpdate", "maxSurge"))
	case statefulSetsKind:
		if str(o, "spec", "updateStrategy", "type") == "OnDelete" {
			return msg("restart.onDelete")
		}
		if part := i64(o, "spec", "updateStrategy", "rollingUpdate", "partition"); part > 0 {
			return msg("restart.partition", "partition", part)
		}
		return msg("restart.ordered")
	case daemonSetsKind:
		if str(o, "spec", "updateStrategy", "type") == "OnDelete" {
			return msg("restart.onDelete")
		}
		return msg("restart.byNode", "maxUnavailable", intOrPercent(o, "1", "spec", "updateStrategy", "rollingUpdate", "maxUnavailable"))
	}
	return msg("restart.requested")
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
func (s *session) podController(ctx context.Context, p *unstructured.Unstructured) core.Message {
	c := metav1.GetControllerOf(p)
	if c == nil {
		return msg("pod.noController")
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
		return msg("pod.job", "owner", c.Name)
	default:
		return msg("pod.otherController", "ownerKind", c.Kind, "owner", c.Name)
	}
	ctx, cancel := context.WithTimeout(ctx, prepareExtrasTimeout)
	defer cancel()
	owner, err := s.getObject(ctx, core.Ref{Kind: def.desc.ID, Scope: p.GetNamespace(), Name: c.Name, UID: string(c.UID)})
	switch {
	case err != nil && strings.Contains(err.Error(), "took its name"), err != nil && isNotFound(err):
		return msg("pod.ownerGone", "ownerKind", c.Kind, "owner", c.Name)
	case err != nil:
		return msg("pod.ownerUnreadable", "ownerKind", c.Kind, "owner", c.Name)
	case owner.GetDeletionTimestamp() != nil:
		return msg("pod.ownerDeleting", "ownerKind", c.Kind, "owner", c.Name)
	case def != daemonSetsKind && replicas(owner.Object) == 0:
		return msg("pod.ownerWantsNone", "ownerKind", c.Kind, "owner", c.Name)
	}
	return msg("pod.ownerRecreates", "ownerKind", c.Kind, "owner", c.Name)
}

func isNotFound(err error) bool { return strings.Contains(err.Error(), "not found") }

// maxCounted bounds counting a workload's pods.
const maxCounted = 500

// podsOf counts the pods a workload owns now: a Deployment's through its
// ReplicaSets, the others' directly (controller references, not labels: an
// unrelated pod with the same labels is not deleted with it); pods already
// being deleted are not counted. Counting is bounded; a partial count is
// said as "at least".
func (s *session) podsOf(ctx context.Context, def *kindDef, u *unstructured.Unstructured) []core.Message {
	unknown := []core.Message{msg("pods.deleted")}
	sel, err := metav1.LabelSelectorAsSelector(labelSelectorOf(u.Object))
	if err != nil || sel.Empty() {
		return unknown
	}
	list := func(gvr schema.GroupVersionResource) ([]unstructured.Unstructured, bool, error) {
		l, err := s.dyn.Resource(gvr).Namespace(u.GetNamespace()).List(ctx, metav1.ListOptions{LabelSelector: sel.String(), Limit: maxCounted})
		if err != nil {
			return nil, false, err
		}
		return l.Items, l.GetContinue() != "", nil
	}
	owners := map[types.UID]bool{u.GetUID(): true}
	partial := false
	if def == deploymentsKind {
		owners = map[types.UID]bool{}
		rss, more, err := list(replicaSetsKind.gvr)
		if err != nil {
			return unknown
		}
		partial = more
		for i := range rss {
			if c := metav1.GetControllerOf(&rss[i]); c != nil && c.UID == u.GetUID() {
				owners[rss[i].GetUID()] = true
			}
		}
	}
	pods, more, err := list(podsKind.gvr)
	if err != nil {
		return unknown
	}
	partial = partial || more
	n := 0
	for i := range pods {
		if c := metav1.GetControllerOf(&pods[i]); c != nil && owners[c.UID] && pods[i].GetDeletionTimestamp() == nil {
			n++
		}
	}
	switch {
	case partial && n == 0:
		return unknown
	case partial:
		return []core.Message{msg("pods.deletedAtLeast", "count", n)}
	case n == 0:
		return []core.Message{msg("pods.none")}
	}
	return []core.Message{msg("pods.deletedCount", "count", n)}
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
func (s *session) autoscalers(ctx context.Context, def *kindDef, u *unstructured.Unstructured) []core.Message {
	kind := u.GetKind()
	if kind == "" {
		kind = map[*kindDef]string{deploymentsKind: "Deployment", statefulSetsKind: "StatefulSet"}[def]
	}
	res := s.dyn.Resource(hpaGVRs).Namespace(u.GetNamespace())
	cont := ""
	var out []core.Message
	for page := 0; page < maxHPAPages; page++ {
		l, err := res.List(ctx, metav1.ListOptions{Limit: 500, Continue: cont})
		if err != nil {
			return append(out, msg("hpa.checkFailed", "error", shortErr(err)))
		}
		for _, h := range l.Items {
			t := h.Object
			gv, _ := schema.ParseGroupVersion(str(t, "spec", "scaleTargetRef", "apiVersion"))
			if gv.Group == def.gvr.Group && str(t, "spec", "scaleTargetRef", "kind") == kind && str(t, "spec", "scaleTargetRef", "name") == u.GetName() {
				out = append(out, msg("hpa.overrides", "name", h.GetName(), "min", max(i64(t, "spec", "minReplicas"), 1), "max", i64(t, "spec", "maxReplicas")))
			}
		}
		if cont = l.GetContinue(); cont == "" {
			return out
		}
	}
	return append(out, msg("hpa.tooMany"))
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
	// Neither allowed nor denied, and the authorizer failed: no answer.
	denied, _, _ := unstructured.NestedBool(out.Object, "status", "denied")
	if e := str(out.Object, "status", "evaluationError"); !denied && e != "" {
		return core.Rights{State: core.RightsUnknown, Reason: e}
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
