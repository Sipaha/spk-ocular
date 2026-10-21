package kubernetes

import (
	"github.com/spk/spk-ocular/internal/core"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"strings"
)

// Controller labels are hints, not proof. The UI additionally checks a pinned
// local controller and reported inventory (or an exact ownerReference UID).
func fluxTracking(u *unstructured.Unstructured) *core.FluxTracking {
	gvk := u.GroupVersionKind()
	var candidates []core.FluxManagerHint
	labels, owners := u.GetLabels(), u.GetOwnerReferences()
	valid := func(s string) bool { return s != "" && len(s) <= 253 && !strings.ContainsAny(s, "/ :\r\n\t") }
	for _, kind := range []string{"Kustomization", "HelmRelease"} {
		group := "kustomize.toolkit.fluxcd.io"
		if kind == "HelmRelease" {
			group = "helm.toolkit.fluxcd.io"
		}
		name, ns := labels[group+"/name"], labels[group+"/namespace"]
		if valid(name) && valid(ns) {
			candidates = append(candidates, core.FluxManagerHint{Kind: kind, Name: name, Namespace: ns})
		}
		for _, owner := range owners {
			if owner.Kind == kind && strings.HasPrefix(owner.APIVersion, group+"/") && owner.UID != "" && valid(owner.Name) && valid(u.GetNamespace()) {
				candidates = append(candidates, core.FluxManagerHint{Kind: kind, Name: owner.Name, Namespace: u.GetNamespace(), UID: string(owner.UID)})
			}
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	return &core.FluxTracking{Group: gvk.Group, Kind: gvk.Kind, Candidates: candidates}
}
