package kubernetes

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"time"
)

var reliabilityHPAGVR = schema.GroupVersionResource{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"}

func reliabilitySupported(d *kindDef) bool {
	if d == nil || !d.namespaced {
		return false
	}
	switch d.gvr {
	case podsKind.gvr:
		return kindOf(d) == "Pod"
	case deploymentsKind.gvr:
		return kindOf(d) == "Deployment"
	case statefulSetsKind.gvr:
		return kindOf(d) == "StatefulSet"
	case daemonSetsKind.gvr:
		return kindOf(d) == "DaemonSet"
	}
	return false
}

func (s *session) ResourceReliability(parent context.Context, ref core.Ref) (core.Reliability, error) {
	out := core.Reliability{Ref: ref, Observations: []core.ReliabilityObservation{}, PDBState: "unknown", HPAState: "not-applicable"}
	d := s.kind(ref.Kind)
	if ref.Provider != ProviderID || ref.Target != s.target || len(validation.IsDNS1123Label(ref.Scope)) != 0 || !certificateName(ref.Name) || ref.UID == "" || len(ref.UID) > 256 {
		return out, &provider.Error{Class: provider.ClassInvalid, Message: "reliability requires a namespaced UID-pinned workload"}
	}
	if !reliabilitySupported(d) {
		return out, &provider.Error{Class: provider.ClassUnsupported, Message: "unsupported reliability workload"}
	}
	bounded, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	ctx, release, err := s.inventoryContext(bounded)
	if err != nil {
		return out, err
	}
	defer release()
	ctx = newScopeBudget().context(ctx)
	r := reliabilityReader{s: s, ctx: ctx, out: &out}
	read := func() (*unstructured.Unstructured, error) {
		u, e := s.dyn.Resource(d.gvr).Namespace(ref.Scope).Get(ctx, ref.Name, metav1.GetOptions{})
		if e != nil {
			return nil, r.failure(e)
		}
		if !r.admit(u) {
			return nil, &provider.Error{Class: provider.ClassScopeLimit, Message: "reliability source exceeds snapshot budget"}
		}
		if !certificateIdentity(u, d.gvr, kindOf(d), ref.Scope) || u.GetName() != ref.Name {
			return nil, &provider.Error{Class: provider.ClassUnavailable, Message: "invalid workload identity"}
		}
		if string(u.GetUID()) != ref.UID {
			return nil, &provider.Error{Class: provider.ClassGone, Message: "workload was replaced"}
		}
		return u, nil
	}
	u, err := read()
	if err != nil {
		return out, err
	}
	out.ResourceVersion = u.GetResourceVersion()
	out.CapturedAt = s.now().UnixMilli()
	projectReliability(&out, u)
	r.pdbs(u)
	if d.gvr == deploymentsKind.gvr || d.gvr == statefulSetsKind.gvr {
		r.hpas(u)
	}
	fresh, err := read()
	if err != nil {
		return core.Reliability{}, err
	}
	if fresh.GetResourceVersion() != u.GetResourceVersion() {
		return core.Reliability{}, &provider.Error{Class: provider.ClassConflict, Message: "workload changed; load a new snapshot"}
	}
	if err = ctx.Err(); err != nil {
		return core.Reliability{}, err
	}
	return out, nil
}

type reliabilityReader struct {
	s                       *session
	ctx                     context.Context
	out                     *core.Reliability
	objects, bytes, related int
}

func (r *reliabilityReader) admit(u *unstructured.Unstructured) bool {
	n, e := encodedJSONSize(u.Object)
	if e != nil || n > 1<<20 || r.bytes+n > 4<<20 || r.objects >= 256 {
		r.out.Partial = true
		return false
	}
	r.bytes += n
	r.objects++
	return true
}
func (r *reliabilityReader) failure(err error) error {
	if r.ctx.Err() != nil {
		return r.ctx.Err()
	}
	c, _ := classify(err)
	return &provider.Error{Class: c, Message: "reliability source could not be read"}
}

// Collections are never widened beyond the explicitly selected namespace.
func (r *reliabilityReader) collection(gvr schema.GroupVersionResource, kind string, visit func(*unstructured.Unstructured) bool) string {
	cursor := inventoryCursor{}
	seen := map[string]bool{}
	count := 0
	state := "complete"
	for range 4 {
		l, e := r.s.dyn.Resource(gvr).Namespace(r.out.Ref.Scope).List(r.ctx, metav1.ListOptions{Limit: 32, Continue: cursor.token})
		if e != nil {
			c, _ := classify(e)
			return string(c)
		}
		if len(l.Items) > 32 {
			r.out.Partial = true
			return "partial"
		}
		for i := range l.Items {
			u := &l.Items[i]
			count++
			if count > 128 || !r.admit(u) {
				return "partial"
			}
			if !certificateIdentity(u, gvr, kind, r.out.Ref.Scope) || seen[u.GetName()] {
				state = "unknown"
				continue
			}
			seen[u.GetName()] = true
			if !visit(u) {
				state = "unknown"
			}
		}
		more, repeated := cursor.advance(l.GetContinue())
		if repeated {
			r.out.Partial = true
			return "partial"
		}
		if !more {
			return state
		}
	}
	r.out.Partial = true
	return "partial"
}

// Related values and navigation are admitted only after an exact UID/RV recheck.
func (r *reliabilityReader) verify(gvr schema.GroupVersionResource, u *unstructured.Unstructured) bool {
	if r.related >= 16 {
		r.out.Partial = true
		return false
	}
	r.related++
	v, e := r.s.dyn.Resource(gvr).Namespace(u.GetNamespace()).Get(r.ctx, u.GetName(), metav1.GetOptions{})
	return e == nil && r.admit(v) && certificateIdentity(v, gvr, u.GetKind(), u.GetNamespace()) && v.GetName() == u.GetName() && v.GetUID() == u.GetUID() && v.GetResourceVersion() == u.GetResourceVersion()
}
func reliabilityAdd(out *core.Reliability, u *unstructured.Unstructured, rule, field, container, outcome, code, value string) {
	if len(out.Observations) >= 256 || len(field) > 512 || len(value) > 2048 || len(container) > 253 {
		out.Partial = true
		return
	}
	severity := "info"
	if outcome == "review" {
		severity = "contextual"
	}
	sourceKind := out.Ref.Kind
	switch u.GetKind() {
	case "PodDisruptionBudget":
		sourceKind = "policy/poddisruptionbudgets"
	case "HorizontalPodAutoscaler":
		sourceKind = "autoscaling/horizontalpodautoscalers"
	}
	observation := core.ReliabilityObservation{Rule: "kubernetes.reliability." + rule, Version: 1, Source: core.Ref{Provider: out.Ref.Provider, Target: out.Ref.Target, Kind: sourceKind, Scope: u.GetNamespace(), Name: u.GetName(), UID: string(u.GetUID())}, ResourceVersion: u.GetResourceVersion(), Field: field, Container: container, Outcome: outcome, Severity: severity, Code: code, Value: value, Reference: reliabilityReference(rule)}
	n, err := encodedJSONSize(observation)
	if err != nil || n > 2048 {
		out.Partial = true
		return
	}
	out.Observations = append(out.Observations, observation)
}
func reliabilityReference(rule string) string {
	switch rule {
	case "probes":
		return "https://kubernetes.io/docs/concepts/configuration/liveness-readiness-startup-probes/"
	case "resources":
		return "https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/"
	case "pdb":
		return "https://kubernetes.io/docs/concepts/workloads/pods/disruptions/"
	case "hpa":
		return "https://kubernetes.io/docs/concepts/workloads/autoscaling/horizontal-pod-autoscale/"
	case "placement":
		return "https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/"
	default:
		return "https://kubernetes.io/docs/concepts/workloads/controllers/"
	}
}
