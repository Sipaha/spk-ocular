package kubernetes

import (
	"context"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
)

var vpaGVR = schema.GroupVersionResource{Group: "autoscaling.k8s.io", Version: "v1", Resource: "verticalpodautoscalers"}

func vpaSupported(d *kindDef) bool {
	return reliabilitySupported(d) && d.gvr != podsKind.gvr
}

func (s *session) ResourceVPA(parent context.Context, ref core.Ref) (core.VPAInspection, error) {
	out := core.VPAInspection{Ref: ref, State: "not-discovered", HPAState: "not-checked", Sources: []core.VPASource{}, Declarations: []core.VPADeclaration{}, HPAMetrics: []core.VPAHPAMetric{}}
	d := s.kind(ref.Kind)
	if ref.Provider != ProviderID || ref.Target != s.target || len(validation.IsDNS1123Label(ref.Scope)) != 0 || !certificateName(ref.Name) || ref.UID == "" || len(ref.UID) > 256 {
		return out, &provider.Error{Class: provider.ClassInvalid, Message: "VPA inspection requires a namespaced UID-pinned controller"}
	}
	if !vpaSupported(d) {
		return out, &provider.Error{Class: provider.ClassUnsupported, Message: "unsupported VPA inspection controller"}
	}
	bounded, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	ctx, release, err := s.inventoryContext(bounded)
	if err != nil {
		return out, err
	}
	defer release()
	ctx = newScopeBudget().context(ctx)
	r := vpaReader{s: s, ctx: ctx, ref: ref}
	read := func() (*unstructured.Unstructured, error) {
		u, e := s.dyn.Resource(d.gvr).Namespace(ref.Scope).Get(ctx, ref.Name, metav1.GetOptions{})
		if e != nil {
			return nil, r.failure(e)
		}
		if !r.admit(u) {
			return nil, &provider.Error{Class: provider.ClassScopeLimit, Message: "VPA source exceeds snapshot budget"}
		}
		if !certificateIdentity(u, d.gvr, kindOf(d), ref.Scope) || u.GetName() != ref.Name || string(u.GetUID()) != ref.UID {
			return nil, &provider.Error{Class: provider.ClassGone, Message: "VPA target identity changed"}
		}
		return u, nil
	}
	root, err := read()
	if err != nil {
		return core.VPAInspection{}, err
	}
	out.ResourceVersion = root.GetResourceVersion()
	out.CapturedAt = s.now().UnixMilli()
	containers, found, e := unstructured.NestedSlice(root.Object, "spec", "template", "spec", "containers")
	if e != nil || !found || len(containers) == 0 || len(containers) > 16 {
		return core.VPAInspection{}, &provider.Error{Class: provider.ClassScopeLimit, Message: "VPA inspection requires up to sixteen app containers"}
	}
	names := map[string]bool{}
	for _, item := range containers {
		c, ok := item.(map[string]any)
		name := str(c, "name")
		if !ok || len(validation.IsDNS1123Label(name)) != 0 || names[name] {
			return core.VPAInspection{}, &provider.Error{Class: provider.ClassUnavailable, Message: "invalid VPA target container identity"}
		}
		names[name] = true
		out.Declarations = append(out.Declarations, core.VPADeclaration{Container: name, Requests: vpaResources(c, "resources", "requests"), Limits: vpaResources(c, "resources", "limits")})
	}
	vd := s.kind("autoscaling.k8s.io/verticalpodautoscalers")
	if vd != nil {
		out.State = "unsupported-version"
		if vd.namespaced && vd.gvr == vpaGVR && kindOf(vd) == "VerticalPodAutoscaler" {
			out.State = r.collection(vpaGVR, "VerticalPodAutoscaler", func(u *unstructured.Unstructured) bool {
				match, valid := vpaTarget(u, root, "targetRef")
				if !valid || !match {
					return valid
				}
				if !r.verify(vpaGVR, u) {
					return false
				}
				out.Sources = append(out.Sources, projectVPA(u, ref, names))
				return true
			})
			if out.State == "complete" && len(out.Sources) == 0 {
				out.State = "no-matches"
			}
			if out.State == "complete" && len(out.Sources) > 1 {
				out.State = "ambiguous"
			}
		}
	}
	if len(out.Sources) > 0 {
		out.HPAState = r.collection(reliabilityHPAGVR, "HorizontalPodAutoscaler", func(u *unstructured.Unstructured) bool {
			match, valid := vpaTarget(u, root, "scaleTargetRef")
			if !valid || !match {
				return valid
			}
			if !r.verify(reliabilityHPAGVR, u) {
				return false
			}
			metrics, found, e := unstructured.NestedSlice(u.Object, "spec", "metrics")
			if e != nil || !found || len(metrics) == 0 || len(metrics) > 32 {
				return false
			}
			valid = true
			for _, item := range metrics {
				m, ok := item.(map[string]any)
				if !ok {
					valid = false
					continue
				}
				key := "resource"
				switch str(m, "type") {
				case "ContainerResource":
					key = "containerResource"
				case "Resource":
				case "External", "Pods", "Object":
					continue
				default:
					valid = false
					continue
				}
				resource := str(m, key, "name")
				if resource != "cpu" && resource != "memory" {
					continue
				}
				typ := str(m, key, "target", "type")
				if typ != "Utilization" && typ != "AverageValue" {
					valid = false
					continue
				}
				container := ""
				if key == "containerResource" {
					container = str(m, key, "container")
					if !names[container] {
						valid = false
						continue
					}
				}
				if len(out.HPAMetrics) >= 64 {
					valid = false
					continue
				}
				out.HPAMetrics = append(out.HPAMetrics, core.VPAHPAMetric{Ref: vpaRef(ref, u, "autoscaling/horizontalpodautoscalers"), ResourceVersion: u.GetResourceVersion(), Resource: resource, Container: container, TargetType: typ})
			}
			return valid
		})
	}
	fresh, err := read()
	if err != nil {
		return core.VPAInspection{}, err
	}
	if fresh.GetResourceVersion() != out.ResourceVersion {
		return core.VPAInspection{}, &provider.Error{Class: provider.ClassConflict, Message: "VPA target changed; load again"}
	}
	if ctx.Err() != nil {
		return core.VPAInspection{}, ctx.Err()
	}
	return out, nil
}

type vpaReader struct {
	s                       *session
	ctx                     context.Context
	ref                     core.Ref
	objects, bytes, related int
	recheckDenied           bool
}

func (r *vpaReader) admit(u *unstructured.Unstructured) bool {
	n, e := encodedJSONSize(u.Object)
	if e != nil || n > 1<<20 || r.bytes+n > 4<<20 || r.objects >= 274 {
		return false
	}
	r.bytes += n
	r.objects++
	return true
}
func (r *vpaReader) failure(err error) error {
	if r.ctx.Err() != nil {
		return r.ctx.Err()
	}
	c, _ := classify(err)
	return &provider.Error{Class: c, Message: "VPA inspection source could not be read"}
}
func (r *vpaReader) collection(gvr schema.GroupVersionResource, kind string, visit func(*unstructured.Unstructured) bool) string {
	r.recheckDenied = false
	cursor := inventoryCursor{}
	seen := map[string]bool{}
	state := "complete"
	for range 4 {
		list, err := r.s.dyn.Resource(gvr).Namespace(r.ref.Scope).List(r.ctx, metav1.ListOptions{Limit: 32, Continue: cursor.token})
		if err != nil {
			c, _ := classify(err)
			return string(c)
		}
		if len(list.Items) > 32 {
			return "partial"
		}
		for i := range list.Items {
			u := &list.Items[i]
			if !r.admit(u) {
				return "partial"
			}
			if !certificateIdentity(u, gvr, kind, r.ref.Scope) || seen[u.GetName()] {
				if state != "forbidden" {
					state = "unknown"
				}
				continue
			}
			seen[u.GetName()] = true
			if !visit(u) {
				if r.recheckDenied {
					state = "forbidden"
				} else if state != "forbidden" {
					state = "unknown"
				}
			}
		}
		more, repeated := cursor.advance(list.GetContinue())
		if repeated {
			return "partial"
		}
		if !more {
			return state
		}
	}
	return "partial"
}
func (r *vpaReader) verify(gvr schema.GroupVersionResource, u *unstructured.Unstructured) bool {
	if r.related >= 16 {
		return false
	}
	r.related++
	v, e := r.s.dyn.Resource(gvr).Namespace(r.ref.Scope).Get(r.ctx, u.GetName(), metav1.GetOptions{})
	if e != nil {
		class, _ := classify(e)
		if class == provider.ClassForbidden {
			r.recheckDenied = true
		}
		return false
	}
	return r.admit(v) && certificateIdentity(v, gvr, u.GetKind(), r.ref.Scope) && v.GetName() == u.GetName() && v.GetUID() == u.GetUID() && v.GetResourceVersion() == u.GetResourceVersion()
}
func vpaTarget(u, root *unstructured.Unstructured, field string) (bool, bool) {
	v, vf, ve := unstructured.NestedString(u.Object, "spec", field, "apiVersion")
	k, kf, ke := unstructured.NestedString(u.Object, "spec", field, "kind")
	n, nf, ne := unstructured.NestedString(u.Object, "spec", field, "name")
	valid := ve == nil && ke == nil && ne == nil && vf && kf && nf && v != "" && k != "" && certificateName(n)
	return valid && v == root.GetAPIVersion() && k == root.GetKind() && n == root.GetName(), valid
}
func vpaRef(root core.Ref, u *unstructured.Unstructured, kind string) core.Ref {
	return core.Ref{Provider: root.Provider, Target: root.Target, Kind: kind, Scope: u.GetNamespace(), Name: u.GetName(), UID: string(u.GetUID())}
}
