package kubernetes

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation"
	helpers "k8s.io/component-helpers/resource"
)

const capacityObjectBytes = 1 << 20
const capacityTotalBytes = 8 << 20
const capacityPageSize = 32
const capacityPages = 8

func capacityError(class provider.ErrorClass) error {
	return &provider.Error{Class: class, Message: "node declaration snapshot could not be verified"}
}
func (s *session) NodeCapacity(parent context.Context, req core.NodeCapacityRequest) (core.NodeCapacity, error) {
	out := core.NodeCapacity{Ref: req.Ref, Namespace: req.Namespace, Pods: []core.CapacityPod{}, Coverage: "unknown"}
	ref := req.Ref
	if ref.Provider != ProviderID || ref.Target != s.target || ref.Kind != "nodes" || ref.Scope != "" || !certificateName(ref.Name) || ref.UID == "" || len(ref.UID) > 256 || len(validation.IsDNS1123Label(req.Namespace)) != 0 {
		return out, capacityError(provider.ClassInvalid)
	}
	bounded, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	ctx, release, err := s.inventoryContext(bounded)
	if err != nil {
		return out, err
	}
	defer release()
	ctx = newScopeBudget().context(ctx)
	bytes := 0
	admit := func(u *unstructured.Unstructured) bool {
		n, e := encodedJSONSize(u.Object)
		if e != nil || n > capacityObjectBytes || bytes+n > capacityTotalBytes {
			return false
		}
		bytes += n
		return true
	}
	read := func() (*unstructured.Unstructured, error) {
		u, e := s.dyn.Resource(nodesKind.gvr).Get(ctx, ref.Name, metav1.GetOptions{})
		if e != nil {
			c, _ := classify(e)
			return nil, capacityError(c)
		}
		if !admit(u) {
			return nil, capacityError(provider.ClassScopeLimit)
		}
		if !certificateIdentity(u, nodesKind.gvr, "Node", "") || u.GetName() != ref.Name {
			return nil, capacityError(provider.ClassUnavailable)
		}
		if string(u.GetUID()) != ref.UID {
			return nil, capacityError(provider.ClassGone)
		}
		return u, nil
	}
	root, err := read()
	if err != nil {
		return out, err
	}
	out.ResourceVersion = root.GetResourceVersion()
	out.Allocatable = capacityNodeResources(root)
	out.Coverage = "complete"
	cursor := inventoryCursor{}
	seen := map[string]bool{}
	rv := ""
	for page := 0; page < capacityPages; page++ {
		list, e := s.dyn.Resource(podsKind.gvr).Namespace(req.Namespace).List(ctx, metav1.ListOptions{Limit: capacityPageSize, Continue: cursor.token, FieldSelector: "spec.nodeName=" + ref.Name})
		if e != nil {
			c, _ := classify(e)
			out.Coverage = string(c)
			break
		}
		if len(list.Items) > capacityPageSize {
			out.Coverage = "partial"
			break
		}
		if list.GetResourceVersion() == "" || (page > 0 && rv != list.GetResourceVersion()) {
			out.Coverage = "unknown"
		} else {
			rv = list.GetResourceVersion()
		}
		for i := range list.Items {
			u := &list.Items[i]
			if !admit(u) {
				out.Coverage = "partial"
				out.Unverified++
				break
			}
			if !certificateIdentity(u, podsKind.gvr, "Pod", req.Namespace) || seen[u.GetName()] || str(u.Object, "spec", "nodeName") != ref.Name {
				out.Unverified++
				out.Coverage = "unknown"
				continue
			}
			seen[u.GetName()] = true
			phase := str(u.Object, "status", "phase")
			if phase == "Succeeded" || phase == "Failed" {
				out.ExcludedTerminal++
				continue
			}
			if phase != "Running" && phase != "Pending" && phase != "Unknown" {
				out.Unverified++
				out.Coverage = "unknown"
				continue
			}
			out.Pods = append(out.Pods, core.CapacityPod{Ref: s.ref(podsKind, req.Namespace, u.GetName(), string(u.GetUID())), ResourceVersion: u.GetResourceVersion(), Requests: capacityPodResources(u)})
		}
		if out.Coverage == "partial" {
			break
		}
		more, repeated := cursor.advance(list.GetContinue())
		if repeated {
			out.Coverage = "partial"
			break
		}
		if !more {
			break
		}
		if page == capacityPages-1 {
			out.Coverage = "partial"
		}
	}
	fresh, err := read()
	if err != nil {
		return core.NodeCapacity{}, err
	}
	if fresh.GetResourceVersion() != out.ResourceVersion {
		return core.NodeCapacity{}, capacityError(provider.ClassConflict)
	}
	if err = ctx.Err(); err != nil {
		return core.NodeCapacity{}, err
	}
	sort.Slice(out.Pods, func(i, j int) bool { return out.Pods[i].Ref.Name < out.Pods[j].Ref.Name })
	out.Requests = capacityTotals(out)
	out.CapturedAt = s.now().UnixMilli()
	return out, nil
}
func capacityNodeResources(u *unstructured.Unstructured) core.CapacityResources {
	return core.CapacityResources{CPU: capacityNodeQuantity(u, "cpu"), Memory: capacityNodeQuantity(u, "memory")}
}
func capacityNodeQuantity(u *unstructured.Unstructured, key string) core.CapacityQuantity {
	value, found, err := unstructured.NestedString(u.Object, "status", "allocatable", key)
	if err != nil {
		return capacityUnknown()
	}
	if !found {
		return core.CapacityQuantity{State: "unspecified"}
	}
	q, valid := capacityParse(value)
	if !valid {
		return capacityUnknown()
	}
	return core.CapacityQuantity{State: "declared", Value: q.String()}
}
func capacityUnknown() core.CapacityQuantity { return core.CapacityQuantity{State: "unknown"} }
func capacityPodResources(u *unstructured.Unstructured) core.CapacityResources {
	unknown := core.CapacityResources{CPU: capacityUnknown(), Memory: capacityUnknown()}
	// Decode only a typed projection. Raw env, metadata and configuration values
	// neither enter the typed object nor survive in a cache/DTO/store.
	spec, ok := u.Object["spec"].(map[string]any)
	if !ok {
		return unknown
	}
	slim := map[string]any{}
	if claims, present := spec["resourceClaims"]; present {
		xs, ok := claims.([]any)
		if !ok || len(xs) > 0 {
			return unknown
		}
	}
	if raw, present := spec["overhead"]; present {
		values, ok := capacityResourceList(raw)
		if !ok {
			return unknown
		}
		slim["overhead"] = values
	}
	if raw, present := spec["resources"]; present {
		values, ok := capacityResourceRequirements(raw)
		if !ok {
			return unknown
		}
		slim["resources"] = values
	}
	for _, key := range []string{"containers", "initContainers"} {
		xs, exists := spec[key]
		if !exists {
			if key == "containers" {
				return unknown
			}
			continue
		}
		items, ok := xs.([]any)
		if !ok || len(items) > 128 {
			return unknown
		}
		projected := []any{}
		for _, item := range items {
			c, ok := item.(map[string]any)
			if !ok {
				return unknown
			}
			v := map[string]any{}
			if raw, present := c["resources"]; present {
				resources, ok := capacityResourceRequirements(raw)
				if !ok {
					return unknown
				}
				v["resources"] = resources
			}
			if raw, present := c["restartPolicy"]; present {
				if raw != "Always" {
					return unknown
				}
				v["restartPolicy"] = raw
			}
			projected = append(projected, v)
		}
		slim[key] = projected
	}
	data, e := json.Marshal(slim)
	if e != nil {
		return unknown
	}
	var pod v1.Pod
	if json.Unmarshal(data, &pod.Spec) != nil || len(pod.Spec.Containers) == 0 || len(pod.Spec.ResourceClaims) > 0 {
		return unknown
	}
	for _, c := range append(append([]v1.Container{}, pod.Spec.Containers...), pod.Spec.InitContainers...) {
		if len(c.Resources.Claims) > 0 {
			return unknown
		}
		if c.RestartPolicy != nil && *c.RestartPolicy != v1.ContainerRestartPolicyAlways {
			return unknown
		}
		for _, q := range c.Resources.Requests {
			if q.Sign() < 0 || q.Cmp(resource.MustParse("1Ei")) > 0 {
				return unknown
			}
		}
	}
	for _, q := range pod.Spec.Overhead {
		if q.Sign() < 0 || q.Cmp(resource.MustParse("1Ei")) > 0 {
			return unknown
		}
	}
	if pod.Spec.Resources != nil {
		for _, q := range pod.Spec.Resources.Requests {
			if q.Sign() < 0 || q.Cmp(resource.MustParse("1Ei")) > 0 {
				return unknown
			}
		}
	}
	requests := helpers.PodRequests(&pod, helpers.PodResourcesOptions{})
	project := func(name v1.ResourceName) core.CapacityQuantity {
		if pod.Spec.Resources == nil || !hasCapacityRequest(pod.Spec.Resources.Requests, name) {
			anyMissing, anyPresent := false, false
			for _, c := range append(append([]v1.Container{}, pod.Spec.Containers...), pod.Spec.InitContainers...) {
				if hasCapacityRequest(c.Resources.Requests, name) {
					anyPresent = true
				} else {
					anyMissing = true
				}
			}
			if anyMissing && anyPresent {
				return capacityUnknown()
			}
			if anyMissing && !anyPresent {
				return core.CapacityQuantity{State: "unspecified"}
			}
		}
		q, present := requests[name]
		if !present {
			return core.CapacityQuantity{State: "unspecified"}
		}
		if q.Sign() < 0 || q.Cmp(resource.MustParse("1Ei")) > 0 {
			return capacityUnknown()
		}
		return core.CapacityQuantity{State: "declared", Value: q.String()}
	}
	return core.CapacityResources{CPU: project(v1.ResourceCPU), Memory: project(v1.ResourceMemory)}
}
func capacityTotals(out core.NodeCapacity) core.CapacityResources {
	sum := func(cpu bool) core.CapacityQuantity {
		if out.Coverage != "complete" {
			return capacityUnknown()
		}
		total := resource.MustParse("0")
		for _, p := range out.Pods {
			q := p.Requests.Memory
			if cpu {
				q = p.Requests.CPU
			}
			if q.State != "declared" {
				return capacityUnknown()
			}
			value, e := resource.ParseQuantity(q.Value)
			if e != nil {
				return capacityUnknown()
			}
			total.Add(value)
		}
		return core.CapacityQuantity{State: "declared", Value: total.String()}
	}
	return core.CapacityResources{CPU: sum(true), Memory: sum(false)}
}

func hasCapacityRequest(r v1.ResourceList, name v1.ResourceName) bool { _, ok := r[name]; return ok }

// Admit only short nonnegative CPU/memory quantities before Kubernetes JSON
// unmarshalling. No arbitrary resource/configuration field enters the helper.
func capacityResourceList(raw any) (map[string]any, bool) {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, false
	}
	out := map[string]any{}
	for _, name := range []string{"cpu", "memory"} {
		raw, exists := m[name]
		if !exists {
			continue
		}
		value, ok := raw.(string)
		if !ok || value == "" || len(value) > 128 {
			return nil, false
		}
		q, valid := capacityParse(value)
		if !valid {
			return nil, false
		}
		out[name] = q.String()
	}
	return out, true
}
func capacityResourceRequirements(raw any) (map[string]any, bool) {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, false
	}
	if claims, exists := m["claims"]; exists {
		xs, ok := claims.([]any)
		if !ok || len(xs) > 0 {
			return nil, false
		}
	}
	out := map[string]any{}
	if raw, exists := m["requests"]; exists {
		requests, ok := capacityResourceList(raw)
		if !ok {
			return nil, false
		}
		out["requests"] = requests
	}
	return out, true
}

var capacityQuantityPattern = regexp.MustCompile(`^\+?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]{1,2}|[numkKMGTPE]i?)?$`)

func capacityParse(value string) (resource.Quantity, bool) {
	if len(value) > 128 || !capacityQuantityPattern.MatchString(value) {
		return resource.Quantity{}, false
	}
	q, err := resource.ParseQuantity(value)
	return q, err == nil && q.Sign() >= 0 && q.Cmp(resource.MustParse("1Ei")) <= 0
}
