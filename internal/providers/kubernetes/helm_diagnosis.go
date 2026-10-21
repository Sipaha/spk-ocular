package kubernetes

import (
	"context"
	"errors"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/helm"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func (s *session) HelmReadContext(ctx context.Context) (context.Context, func(), error) {
	return s.inventoryContext(ctx)
}

// HelmHookCurrent has no dependency-reference or log acquisition path.
func (s *session) HelmHookCurrent(ctx context.Context, h helm.HookRecord) (core.Diagnosis, error) {
	out := core.Diagnosis{Evidence: []core.DiagnosisEvidence{}, Coverage: []core.DiagnosisCoverage{}}
	var gvr schema.GroupVersionResource
	kind := ""
	switch {
	case h.APIVersion == "batch/v1" && h.Kind == "Job":
		gvr = schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}
		kind = "batch/jobs"
	case h.APIVersion == "v1" && h.Kind == "Pod":
		gvr = podsKind.gvr
		kind = "pods"
	default:
		return out, errors.New("hook kind is not covered")
	}
	if h.Coverage != "supported" || h.Namespace == "" || h.Name == "" {
		return out, errors.New("hook identity is not covered")
	}
	ctx = newScopeBudget().context(ctx)
	read := func() (*unstructured.Unstructured, error) {
		return s.dyn.Resource(gvr).Namespace(h.Namespace).Get(ctx, h.Name, metav1.GetOptions{})
	}
	u, err := read()
	if err != nil {
		class, _ := classify(err)
		out.Coverage = append(out.Coverage, core.DiagnosisCoverage{Source: "current_hook", State: string(class)})
		out.CapturedAt = s.now().UnixMilli()
		return out, nil
	}
	if u.GetUID() == "" || u.GetResourceVersion() == "" || u.GetNamespace() != h.Namespace || u.GetName() != h.Name || u.GetKind() != h.Kind || u.GetAPIVersion() != h.APIVersion {
		return out, errors.New("current hook identity is unavailable")
	}
	ref := core.Ref{Provider: ProviderID, Target: s.target, Kind: kind, Scope: h.Namespace, Name: h.Name, UID: string(u.GetUID())}
	out.Ref = ref
	d := diagnosisReader{s: s, ctx: ctx, out: &out, currentOnly: true}
	if !d.admit(u) {
		return core.Diagnosis{}, errors.New("current hook exceeds snapshot budget")
	}
	d.object(u, ref)
	if h.Kind == "Job" {
		d.jobPods(u, ref)
	}
	d.events(ref)
	after, err := read()
	if err != nil || after.GetUID() != u.GetUID() || after.GetResourceVersion() != u.GetResourceVersion() || after.GetName() != u.GetName() || after.GetNamespace() != u.GetNamespace() || after.GetKind() != u.GetKind() || after.GetAPIVersion() != u.GetAPIVersion() {
		return core.Diagnosis{}, errors.New("current hook changed during observation")
	}
	if err = ctx.Err(); err != nil {
		return core.Diagnosis{}, err
	}
	out.CapturedAt = s.now().UnixMilli()
	return out, nil
}

// HelmFluxContext inspects only one explicit controller namespace and reported release history.
func (s *session) HelmFluxContext(ctx context.Context, ns, storage string, r helm.Release) (helm.FluxContext, error) {
	out := helm.FluxContext{Candidates: []helm.FluxAssociation{}, Coverage: "not_covered"}
	def := s.kind("helm.toolkit.fluxcd.io/helmreleases")
	if def == nil || !def.discovered || !def.namespaced || def.kind != "HelmRelease" {
		return out, nil
	}
	ctx = newScopeBudget().context(ctx)
	evidence := core.Diagnosis{Evidence: []core.DiagnosisEvidence{}, Coverage: []core.DiagnosisCoverage{}}
	d := diagnosisReader{s: s, ctx: ctx, out: &evidence, currentOnly: true}
	out.Coverage = d.list(def.gvr, ns, "", "", func(u *unstructured.Unstructured) {
		if u.GetNamespace() != ns || u.GetKind() != "HelmRelease" || u.GetAPIVersion() != def.gvr.GroupVersion().String() || u.GetUID() == "" {
			return
		}
		if str(u.Object, "status", "storageNamespace") != storage {
			return
		}
		matched := false
		for _, v := range slice(u.Object, "status", "history") {
			if str(v, "name") == r.Name && str(v, "namespace") == r.Namespace && i64(v, "version") == int64(r.Revision) {
				matched = true
				break
			}
		}
		if !matched {
			return
		}
		state := "reported_match"
		if fieldAt(u.Object, "spec", "kubeConfig") != nil {
			state = "remote_unverified"
		} else if i64(u.Object, "status", "observedGeneration") == 0 || i64(u.Object, "status", "observedGeneration") != u.GetGeneration() {
			state = "stale_unverified"
		}
		ref := core.Ref{Provider: ProviderID, Target: s.target, Kind: def.desc.ID, Scope: ns, Name: u.GetName(), UID: string(u.GetUID())}
		one := core.Diagnosis{Evidence: []core.DiagnosisEvidence{}}
		projection := diagnosisReader{s: s, ctx: ctx, out: &one}
		projection.object(u, ref)
		source := str(u.Object, "spec", "chartRef", "kind") + "/" + str(u.Object, "spec", "chartRef", "name")
		if source == "/" {
			source = str(u.Object, "spec", "chart", "spec", "sourceRef", "kind") + "/" + str(u.Object, "spec", "chart", "spec", "sourceRef", "name")
		}
		if len(source) > 2048 {
			source = ""
			state = "partial_unverified"
		}
		if one.Truncated {
			state = "partial_unverified"
		}
		out.Candidates = append(out.Candidates, helm.FluxAssociation{Ref: ref, State: state, Source: source, Conditions: one.Evidence})
	})
	if evidence.Truncated {
		out.Coverage = "partial"
	}
	if out.Coverage != "ready" {
		for i := range out.Candidates {
			if out.Candidates[i].State == "reported_match" {
				out.Candidates[i].State = "partial_unverified"
			}
		}
	}
	if len(out.Candidates) > 1 {
		for i := range out.Candidates {
			out.Candidates[i].State = "ambiguous"
		}
	}
	if err := ctx.Err(); err != nil {
		return helm.FluxContext{}, err
	}
	out.CapturedAt = s.now().UnixMilli()
	return out, nil
}
