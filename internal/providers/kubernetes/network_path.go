package kubernetes

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var networkSlices = schema.GroupVersionResource{Group: "discovery.k8s.io", Version: "v1", Resource: "endpointslices"}

type ingressBackend struct {
	route   core.IngressRoute
	field   string
	backend map[string]any
}

// ingressBackends retains repeated paths and the distinct default backend. Both
// relations and path diagnosis consume this same declaration, not graph edges
// which deliberately collapse repeated source/target pairs.
func ingressBackends(u *unstructured.Unstructured) []ingressBackend {
	var out []ingressBackend
	if b, ok := fieldAt(u.Object, "spec", "defaultBackend").(map[string]any); ok {
		out = append(out, ingressBackend{core.IngressRoute{DefaultBackend: true}, "spec.defaultBackend", b})
	}
	for i, r := range slice(u.Object, "spec", "rules") {
		for j, p := range slice(r, "http", "paths") {
			b, _ := p["backend"].(map[string]any)
			out = append(out, ingressBackend{core.IngressRoute{Host: str(r, "host"), Path: str(p, "path"), PathType: str(p, "pathType")}, fmt.Sprintf("spec.rules[%d].http.paths[%d].backend", i, j), b})
		}
	}
	return out
}

// ResourceNetworkPath never starts a watch, tunnel, probe, controller or Job.
// The finite namespace inventory reuses graphKey identity indices; graph Load
// admission remains independent and no cluster-wide graph is fetched here.
func (s *session) ResourceNetworkPath(parent context.Context, ref core.Ref) (core.NetworkPath, error) {
	out := core.NetworkPath{Ref: ref, Routes: []core.NetworkRoute{}, Coverage: []core.NetworkCoverage{}, Discovery: s.cat.snap().state}
	def := s.kind(ref.Kind)
	if ref.Provider != ProviderID || ref.Target != s.target || ref.Scope == "" || ref.Name == "" || ref.UID == "" {
		return out, &provider.Error{Class: provider.ClassInvalid, Message: "network path requires a namespaced UID-pinned reference"}
	}
	if def != servicesKind && def != ingressesKind {
		return out, &provider.Error{Class: provider.ClassUnsupported, Message: "network path supports only Services and Ingresses"}
	}
	bounded, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	ctx, release, err := s.inventoryContext(bounded)
	if err != nil {
		return out, err
	}
	defer release()
	ctx = newScopeBudget().context(ctx)
	r := networkReader{s: s, ctx: ctx, out: &out, namespace: ref.Scope, services: map[string]*unstructured.Unstructured{}, serviceStates: map[string]string{}}
	u, state := r.get(def.gvr, kindOf(def), ref.Name)
	if state != "ready" {
		return out, r.readError(state, "network subject could not be read")
	}
	if string(u.GetUID()) != ref.UID {
		return out, &provider.Error{Class: provider.ClassGone, Message: "network subject was replaced"}
	}
	if def == servicesKind {
		r.services[ref.Name] = u
		r.serviceStates[ref.Name] = "ready"
	}
	// Once per namespace, independently bounded. Failed and truncated inventories
	// cannot support an absence claim. Nothing but the projection escapes.
	r.pods, r.podState = r.list(podsKind.gvr, "Pod")
	r.slices, r.sliceState = r.list(networkSlices, "EndpointSlice")
	r.cover("pods", r.podState)
	r.cover("endpointslices", r.sliceState)
	if def == servicesKind {
		ports := slice(u.Object, "spec", "ports")
		if len(ports) == 0 {
			r.route(u, nil, "spec.ports", nil)
		}
		for i, p := range ports {
			if len(out.Routes) >= 32 {
				out.Truncated = true
				break
			}
			r.route(u, nil, fmt.Sprintf("spec.ports[%d]", i), p)
		}
	} else {
		for _, b := range ingressBackends(u) {
			if len(out.Routes) >= 32 {
				out.Truncated = true
				break
			}
			name := str(b.backend, "service", "name")
			svc, st := r.service(name)
			if name == "" {
				st = "unsupported"
			}
			if st != "ready" {
				code := "backend_unknown"
				health := core.HealthUnknown
				if st == "not_found" {
					code = "backend_missing"
					health = core.HealthWarning
				}
				if st == "unsupported" {
					code = "backend_unsupported"
				}
				out.Routes = append(out.Routes, core.NetworkRoute{Route: &b.route, Field: b.field, Service: s.ref(servicesKind, ref.Scope, name, ""), Checks: []core.NetworkCheck{{Code: code, State: health, Field: b.field}}, Endpoints: []core.NetworkEndpoint{}})
				continue
			}
			var matches []map[string]any
			for _, p := range slice(svc.Object, "spec", "ports") {
				if (str(b.backend, "service", "port", "name") != "" && str(p, "name") == str(b.backend, "service", "port", "name")) || (i64(b.backend, "service", "port", "number") > 0 && i64(p, "port") == i64(b.backend, "service", "port", "number")) {
					matches = append(matches, p)
				}
			}
			if len(matches) == 1 {
				r.route(svc, &b.route, b.field, matches[0])
			} else {
				r.route(svc, &b.route, b.field, nil)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return core.NetworkPath{}, err
	}
	// Backend identity/config changes invalidate their observations. The subject
	// is re-read last so replacement racing with any dependency read is fenced.
	for name, svc := range r.services {
		if def == servicesKind && name == ref.Name {
			continue
		}
		fresh, st := r.get(servicesKind.gvr, "Service", name)
		if st != "ready" {
			return core.NetworkPath{}, r.readError(st, "network backend identity could not be verified")
		}
		if fresh.GetUID() != svc.GetUID() || fresh.GetResourceVersion() != svc.GetResourceVersion() {
			return core.NetworkPath{}, &provider.Error{Class: provider.ClassConflict, Message: "network backend changed; load a new snapshot"}
		}
	}
	fresh, st := r.get(def.gvr, kindOf(def), ref.Name)
	if st != "ready" {
		return core.NetworkPath{}, r.readError(st, "network subject identity could not be verified")
	}
	if fresh.GetUID() != u.GetUID() || fresh.GetResourceVersion() != u.GetResourceVersion() {
		return core.NetworkPath{}, &provider.Error{Class: provider.ClassGone, Message: "network subject changed; load a new snapshot"}
	}
	if err := ctx.Err(); err != nil {
		return core.NetworkPath{}, err
	}
	r.boundOutput()
	out.CapturedAt = s.now().UnixMilli()
	return out, nil
}

type networkReader struct {
	s                         *session
	ctx                       context.Context
	out                       *core.NetworkPath
	namespace                 string
	bytes, objects, endpoints int
	services                  map[string]*unstructured.Unstructured
	serviceStates             map[string]string
	pods, slices              []*unstructured.Unstructured
	podState, sliceState      string
}

func (r *networkReader) cover(source, state string) {
	r.out.Coverage = append(r.out.Coverage, core.NetworkCoverage{Source: source, State: state})
	if state == "partial" {
		r.out.Truncated = true
	}
}
func (r *networkReader) admit(u *unstructured.Unstructured) bool {
	n, err := encodedJSONSize(u.Object)
	if err != nil || n > 1<<20 || r.bytes+n > 4<<20 || r.objects >= 512 {
		r.out.Truncated = true
		return false
	}
	r.bytes += n
	r.objects++
	return true
}
func (r *networkReader) valid(u *unstructured.Unstructured, gvr schema.GroupVersionResource, kind string) bool {
	return u.GetAPIVersion() == gvr.GroupVersion().String() && u.GetKind() == kind && u.GetNamespace() == r.namespace && u.GetUID() != "" && len(u.GetUID()) <= 256 && u.GetName() != "" && len(u.GetName()) <= 253
}
func (r *networkReader) get(gvr schema.GroupVersionResource, kind, name string) (*unstructured.Unstructured, string) {
	u, err := r.s.dyn.Resource(gvr).Namespace(r.namespace).Get(r.ctx, name, metav1.GetOptions{})
	if err != nil {
		c, _ := classify(err)
		return nil, string(c)
	}
	if !r.admit(u) {
		return nil, "partial"
	}
	if !r.valid(u, gvr, kind) || u.GetName() != name {
		return nil, "unknown"
	}
	return u, "ready"
}
func (r *networkReader) service(name string) (*unstructured.Unstructured, string) {
	if name == "" {
		return nil, "unsupported"
	}
	if st, ok := r.serviceStates[name]; ok {
		return r.services[name], st
	}
	u, st := r.get(servicesKind.gvr, "Service", name)
	r.serviceStates[name] = st
	if st == "ready" {
		r.services[name] = u
	}
	coverage := st
	if st == "not_found" {
		coverage = "absent"
	}
	r.cover("services/"+name, coverage)
	return u, st
}
func (r *networkReader) list(gvr schema.GroupVersionResource, kind string) ([]*unstructured.Unstructured, string) {
	out := []*unstructured.Unstructured{}
	cursor := inventoryCursor{}
	state := "ready"
	seen := map[string]bool{}
	for range 4 {
		items, err := r.s.dyn.Resource(gvr).Namespace(r.namespace).List(r.ctx, metav1.ListOptions{Limit: 32, Continue: cursor.token})
		if err != nil {
			c, _ := classify(err)
			return out, string(c)
		}
		for i := range items.Items {
			u := &items.Items[i]
			if len(out) >= 128 || !r.admit(u) {
				return out, "partial"
			}
			if !r.valid(u, gvr, kind) {
				state = "unknown"
				continue
			}
			key := graphKey(gvr.String(), u.GetNamespace(), u.GetName())
			if seen[key] {
				return out, "partial"
			}
			seen[key] = true
			out = append(out, u)
		}
		more, repeated := cursor.advance(items.GetContinue())
		if repeated {
			return out, "partial"
		}
		if !more {
			sort.Slice(out, func(i, j int) bool { return out[i].GetName() < out[j].GetName() })
			return out, state
		}
	}
	return out, "partial"
}
func check(code, field string, state core.HealthState, refs ...core.Ref) core.NetworkCheck {
	c := core.NetworkCheck{Code: code, State: state, Field: field}
	if len(refs) > 0 {
		c.Ref = &refs[0]
	}
	return c
}
func (r *networkReader) route(svc *unstructured.Unstructured, route *core.IngressRoute, field string, p map[string]any) {
	sref := r.s.ref(servicesKind, r.namespace, svc.GetName(), string(svc.GetUID()))
	v := core.NetworkRoute{Route: route, Field: field, Service: sref, Mode: "selector", Checks: []core.NetworkCheck{}, Endpoints: []core.NetworkEndpoint{}}
	defer func() { r.out.Routes = append(r.out.Routes, v) }()
	sel, _, _ := unstructured.NestedStringMap(svc.Object, "spec", "selector")
	v.Selector = labels.SelectorFromSet(sel).String()
	if len(sel) == 0 {
		v.Mode = "selectorless"
	}
	if str(svc.Object, "spec", "clusterIP") == "None" {
		v.Mode = "headless"
	}
	if str(svc.Object, "spec", "type") == "ExternalName" {
		v.Mode = "externalName"
		v.ExternalName = str(svc.Object, "spec", "externalName")
		v.Checks = append(v.Checks, check("external_name", "spec.externalName", core.HealthUnknown))
		return
	}
	portless := p == nil && v.Mode == "headless" && route == nil && len(slice(svc.Object, "spec", "ports")) == 0
	if p == nil && !portless {
		v.Checks = append(v.Checks, check("service_port_missing", field, core.HealthWarning))
		return
	}
	v.PortName = str(p, "name")
	v.Port = i64(p, "port")
	v.Protocol = nonEmpty(str(p, "protocol"), "TCP")
	v.TargetPort = str(p, "targetPort")
	target := i64(p, "targetPort")
	if v.TargetPort == "" {
		if target == 0 {
			target = v.Port
		}
		v.TargetPort = strconv.FormatInt(target, 10)
	}
	if portless {
		v.Protocol = ""
		v.TargetPort = ""
	}
	if v.Protocol != "TCP" && !portless {
		v.Checks = append(v.Checks, check("protocol_unsupported", field, core.HealthUnknown))
		return
	}
	if len(sel) > 0 {
		selected := 0
		missing := 0
		var firstMissing core.Ref
		for _, pod := range r.pods {
			if labels.SelectorFromSet(sel).Matches(labels.Set(pod.GetLabels())) {
				selected++
				if !portless && target == 0 && namedPodPort(pod, v.TargetPort, v.Protocol) == 0 {
					missing++
					firstMissing = r.s.ref(podsKind, r.namespace, pod.GetName(), string(pod.GetUID()))
				}
			}
		}
		if r.podState == "ready" && selected == 0 {
			v.Checks = append(v.Checks, check("selector_empty", "spec.selector", core.HealthWarning))
		}
		if missing > 0 {
			v.Checks = append(v.Checks, check("named_port_missing", "spec.containers[].ports", core.HealthWarning, firstMissing))
		}
	}
	podIndex := map[string]*unstructured.Unstructured{}
	for _, pod := range r.pods {
		podIndex[graphKey("pods", r.namespace, pod.GetName())] = pod
	}
	matchedSlices := 0
	for _, sl := range r.slices {
		if sl.GetLabels()["kubernetes.io/service-name"] != svc.GetName() {
			continue
		}
		sliceRef := core.Ref{Provider: ProviderID, Target: r.s.target, Scope: r.namespace, Kind: "discovery.k8s.io/endpointslices", Name: sl.GetName(), UID: string(sl.GetUID())}
		validOwner := true
		for _, o := range sl.GetOwnerReferences() {
			if o.Kind == "Service" && (o.APIVersion != "v1" || o.Name != svc.GetName() || o.UID != svc.GetUID()) {
				validOwner = false
			}
		}
		if !validOwner {
			v.Checks = append(v.Checks, check("slice_owner_mismatch", "metadata.ownerReferences", core.HealthUnknown, sliceRef))
			continue
		}
		matchedSlices++
		var ports []map[string]any
		for _, port := range slice(sl.Object, "ports") {
			if str(port, "name") == v.PortName && nonEmpty(str(port, "protocol"), "TCP") == v.Protocol {
				ports = append(ports, port)
			}
		}
		endpointPort := int64(0)
		if !portless && (len(ports) != 1 || i64(ports[0], "port") < 1 || i64(ports[0], "port") > 65535) {
			v.Checks = append(v.Checks, check("slice_port_missing", "ports", core.HealthUnknown, sliceRef))
			continue
		}
		if !portless {
			endpointPort = i64(ports[0], "port")
		}
		for _, ep := range slice(sl.Object, "endpoints") {
			if r.endpoints >= 256 {
				r.out.Truncated = true
				return
			}
			r.endpoints++
			e := core.NetworkEndpoint{Slice: sliceRef, Addresses: []string{}, Port: endpointPort, Owners: []core.Ref{}, Checks: []core.NetworkCheck{}, Ready: networkBool(ep, "conditions", "ready"), Serving: networkBool(ep, "conditions", "serving"), Terminating: networkBool(ep, "conditions", "terminating")}
			for _, address := range strs(ep, "addresses") {
				if len(e.Addresses) >= 4 || len(address) > 253 {
					r.out.Truncated = true
					break
				}
				e.Addresses = append(e.Addresses, address)
			}
			if str(sl.Object, "addressType") != "IPv4" && str(sl.Object, "addressType") != "IPv6" {
				e.Checks = append(e.Checks, check("address_unsupported", "addressType", core.HealthUnknown))
			}
			if e.Ready == nil {
				e.Checks = append(e.Checks, check("readiness_unknown", "endpoints[].conditions.ready", core.HealthUnknown))
			} else if !*e.Ready {
				e.Checks = append(e.Checks, check("endpoint_not_ready", "endpoints[].conditions.ready", core.HealthWarning))
			}
			publish, _, _ := unstructured.NestedBool(svc.Object, "spec", "publishNotReadyAddresses")
			if publish {
				e.Checks = append(e.Checks, check("publish_not_ready", "spec.publishNotReadyAddresses", core.HealthUnknown))
			}
			if target > 0 && target != e.Port {
				e.Checks = append(e.Checks, check("target_port_mismatch", "ports[].port", core.HealthWarning))
			}
			tr, _ := ep["targetRef"].(map[string]any)
			if len(tr) > 0 {
				pod := podIndex[graphKey("pods", r.namespace, str(tr, "name"))]
				if str(tr, "kind") != "Pod" || (str(tr, "apiVersion") != "" && str(tr, "apiVersion") != "v1") || str(tr, "namespace") != "" && str(tr, "namespace") != r.namespace || str(tr, "uid") == "" || pod == nil || string(pod.GetUID()) != str(tr, "uid") {
					e.Checks = append(e.Checks, check("pod_unverified", "endpoints[].targetRef", core.HealthUnknown))
				} else {
					pr := r.s.ref(podsKind, r.namespace, pod.GetName(), string(pod.GetUID()))
					e.Pod = &pr
					if len(sel) > 0 && !labels.SelectorFromSet(sel).Matches(labels.Set(pod.GetLabels())) {
						e.Checks = append(e.Checks, check("selector_mismatch", "spec.selector", core.HealthWarning))
					}
					if !portless && target == 0 && namedPodPort(pod, v.TargetPort, v.Protocol) != e.Port {
						e.Checks = append(e.Checks, check("target_port_mismatch", "spec.containers[].ports", core.HealthWarning))
					}
					for _, o := range pod.GetOwnerReferences() {
						if len(e.Owners) >= 8 || len(o.UID) > 256 || len(o.Name) > 253 {
							r.out.Truncated = true
							continue
						}
						kind := r.s.kindFor(o.APIVersion, o.Kind)
						if kind != "" && o.Name != "" && len(o.Name) <= 253 && o.UID != "" && len(o.UID) <= 256 && len(e.Owners) < 8 {
							e.Owners = append(e.Owners, core.Ref{Provider: ProviderID, Target: r.s.target, Kind: kind, Scope: r.namespace, Name: o.Name, UID: string(o.UID)})
						}
					}
				}
			}
			v.Endpoints = append(v.Endpoints, e)
		}
	}
	if r.sliceState == "ready" && matchedSlices == 0 {
		v.Checks = append(v.Checks, check("slices_empty", "metadata.labels[kubernetes.io/service-name]", core.HealthWarning))
	}
	if r.sliceState == "ready" && matchedSlices > 0 && len(v.Endpoints) == 0 {
		v.Checks = append(v.Checks, check("endpoints_empty", "endpoints", core.HealthUnknown))
	}
}
func networkBool(o map[string]any, path ...string) *bool {
	v, ok, _ := unstructured.NestedBool(o, path...)
	if !ok {
		return nil
	}
	return &v
}
func namedPodPort(pod *unstructured.Unstructured, name, protocol string) int64 {
	for _, c := range slice(pod.Object, "spec", "containers") {
		for _, p := range slice(c, "ports") {
			if str(p, "name") == name && nonEmpty(str(p, "protocol"), "TCP") == protocol {
				return i64(p, "containerPort")
			}
		}
	}
	// Restartable init containers are sidecars and may declare Service ports.
	for _, c := range slice(pod.Object, "spec", "initContainers") {
		if str(c, "restartPolicy") != "Always" {
			continue
		}
		for _, p := range slice(c, "ports") {
			if str(p, "name") == name && nonEmpty(str(p, "protocol"), "TCP") == protocol {
				return i64(p, "containerPort")
			}
		}
	}
	return 0
}

// Repeated route references must not amplify a single oversized declaration
// into an unbounded response. Display fields may be shortened with explicit
// truncation; immutable resource identities are never shortened into links.
func (r *networkReader) boundOutput() {
	text := func(s string) string {
		if len(s) > 2048 {
			r.out.Truncated = true
			end := 2048
			for end > 0 && !utf8.RuneStart(s[end]) {
				end--
			}
			return s[:end] + "…"
		}
		return s
	}
	for i := range r.out.Routes {
		v := &r.out.Routes[i]
		v.PortName = text(v.PortName)
		v.TargetPort = text(v.TargetPort)
		v.Protocol = text(v.Protocol)
		v.Selector = text(v.Selector)
		v.ExternalName = text(v.ExternalName)
		if v.Route != nil {
			copyRoute := *v.Route
			copyRoute.Path = text(copyRoute.Path)
			copyRoute.Host = text(copyRoute.Host)
			copyRoute.PathType = text(copyRoute.PathType)
			v.Route = &copyRoute
		}
	}
}

// Failed verification is not evidence that an immutable identity changed.
func (r *networkReader) readError(state, message string) error {
	if err := r.ctx.Err(); err != nil {
		return err
	}
	class := provider.ErrorClass(state)
	switch state {
	case "partial":
		class = provider.ClassScopeLimit
	case "unknown":
		class = provider.ClassUnavailable
	}
	return &provider.Error{Class: class, Message: message}
}
