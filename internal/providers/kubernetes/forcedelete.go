package kubernetes

import (
	"context"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/spk/spk-ocular/internal/core"
)

// actForceDelete is kubectl delete --force --grace-period=0 of a pod: for
// pods stuck in Terminating (a gone node). Not single: they come in batches.
var actForceDelete = core.ActionDescriptor{ID: "forceDelete", Title: "Force delete", Destructive: true}

// forceDeleteEffects: what a forced deletion does to u — gone at once, or
// held by its finalizers (a forced deletion does not bypass them) — and
// what may interfere.
func forceDeleteEffects(u *unstructured.Unstructured) actionEffects {
	var fx actionEffects
	if fin := sortedFinalizers(u); len(fin) > 0 {
		list := strings.Join(fin, ", ")
		fx.effects = append(fx.effects, msg("forceDelete.held", "name", u.GetName(), "finalizers", list))
		fx.warnings = append(fx.warnings, msg("forceDelete.heldWarn", "name", u.GetName(), "finalizers", list))
	} else {
		fx.effects = append(fx.effects, msg("forceDelete.now", "name", u.GetName()))
	}
	if u.GetDeletionTimestamp() == nil {
		fx.warnings = append(fx.warnings, msg("forceDelete.notStuck", "name", u.GetName()))
	}
	if c := metav1.GetControllerOf(u); c != nil && c.Kind == "StatefulSet" {
		fx.warnings = append(fx.warnings, msg("forceDelete.statefulSet", "name", u.GetName(), "owner", c.Name))
	}
	return fx
}

func sortedFinalizers(u *unstructured.Unstructured) []string {
	fin := append([]string{}, u.GetFinalizers()...)
	sort.Strings(fin)
	return fin
}

// nodeWarning: advice on the pod's node — its containers may keep running
// there when it is not Ready or gone. Not bound by Expect (like the rights
// check): a flapping node must not turn runs into conflicts. nil: Ready,
// or no node.
func (s *session) nodeWarning(ctx context.Context, p *unstructured.Unstructured) *core.Message {
	name := str(p.Object, "spec", "nodeName")
	if name == "" {
		return nil
	}
	n, err := s.getObject(ctx, core.Ref{Kind: nodesKind.desc.ID, Name: name})
	var m core.Message
	switch {
	case err != nil && isNotFound(err):
		m = msg("forceDelete.nodeGone", "node", name)
	case err != nil:
		m = msg("forceDelete.nodeUnchecked", "node", name)
	case !nodeReady(n):
		m = msg("forceDelete.nodeNotReady", "node", name)
	default:
		return nil
	}
	return &m
}

func nodeReady(n *unstructured.Unstructured) bool {
	for _, c := range slice(n.Object, "status", "conditions") {
		if strOf(c, "type") == "Ready" {
			return strOf(c, "status") == "True"
		}
	}
	return false
}
