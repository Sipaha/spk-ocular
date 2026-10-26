package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// A credential-free engine input, collected with the connected user's rights.
// No original manifests, environment, annotations or credentials leave Ocular.
const krrSnapshotLimit = 64 << 10

type krrSnapshot struct {
	API  map[string]any      `json:"api"`
	Pods map[string][]string `json:"pods"`
}

func (s *session) krrSnapshot(ctx context.Context, ns string) (string, error) {
	out := krrSnapshot{API: map[string]any{}, Pods: map[string][]string{}}
	total, wireBytes := 0, 0
	read := func(g schema.GroupVersionResource) ([]unstructured.Unstructured, error) {
		var items []unstructured.Unstructured
		seen := map[string]bool{}
		next := ""
		for page := 0; page < 4; page++ {
			list, e := s.dyn.Resource(g).Namespace(ns).List(ctx, metav1.ListOptions{Limit: 100, Continue: next})
			if e != nil {
				return nil, errKRRJob
			}
			if len(list.Items) > 100 {
				return nil, errKRRJob
			}
			total += len(list.Items)
			if total > 256 {
				return nil, errKRRJob
			}
			for _, item := range list.Items {
				if seen[item.GetName()] {
					return nil, errKRRJob
				}
				seen[item.GetName()] = true
				raw, e := json.Marshal(item.Object)
				wireBytes += len(raw)
				if e != nil || len(raw) > 1<<20 || wireBytes > 4<<20 || item.GetName() == "" || item.GetNamespace() != ns || item.GetUID() == "" || item.GetDeletionTimestamp() != nil {
					return nil, errKRRJob
				}
			}
			items = append(items, list.Items...)
			more := list.GetContinue()
			if more == "" {
				return items, nil
			}
			if more == next {
				return nil, errKRRJob
			}
			next = more
		}
		return nil, errKRRJob
	}
	pods, e := read(podsKind.gvr)
	if e != nil {
		return "", e
	}
	for _, r := range []struct{ resource, kind string }{{"deployments", "Deployment"}, {"statefulsets", "StatefulSet"}, {"daemonsets", "DaemonSet"}} {
		items, e := read(schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: r.resource})
		if e != nil {
			return "", e
		}
		projected := []any{}
		for _, item := range items {
			selector, ok, e := unstructured.NestedMap(item.Object, "spec", "selector")
			if e != nil || !ok {
				return "", errKRRJob
			}
			var parsed metav1.LabelSelector
			if runtime.DefaultUnstructuredConverter.FromUnstructured(selector, &parsed) != nil {
				return "", errKRRJob
			}
			selector, e = runtime.DefaultUnstructuredConverter.ToUnstructured(&parsed)
			if e != nil {
				return "", errKRRJob
			}
			match, e := metav1.LabelSelectorAsSelector(&parsed)
			if e != nil || match.Empty() {
				return "", errKRRJob
			}
			names := []string{}
			for _, pod := range pods {
				if match.Matches(labels.Set(pod.GetLabels())) {
					names = append(names, pod.GetName())
				}
			}
			out.Pods[r.kind+"/"+item.GetName()] = names
			containers, ok, e := unstructured.NestedSlice(item.Object, "spec", "template", "spec", "containers")
			if e != nil || !ok || len(containers) == 0 {
				return "", errKRRJob
			}
			clean := []any{}
			for _, raw := range containers {
				c, ok := raw.(map[string]any)
				if !ok {
					return "", errKRRJob
				}
				name, ok := c["name"].(string)
				if !ok || name == "" {
					return "", errKRRJob
				}
				resources := map[string]any{}
				for _, category := range []string{"requests", "limits"} {
					quantities := map[string]any{}
					for _, quantity := range []string{"cpu", "memory"} {
						v, found, e := unstructured.NestedString(c, "resources", category, quantity)
						if e != nil {
							return "", errKRRJob
						}
						if found {
							parsed, err := resource.ParseQuantity(v)
							if err != nil || parsed.Sign() < 0 {
								return "", errKRRJob
							}
							quantities[quantity] = v
						}
					}
					resources[category] = quantities
				}
				clean = append(clean, map[string]any{"name": name, "resources": resources})
			}
			projected = append(projected, map[string]any{"apiVersion": "apps/v1", "kind": r.kind, "metadata": map[string]any{"name": item.GetName(), "namespace": ns}, "spec": map[string]any{"selector": selector, "template": map[string]any{"spec": map[string]any{"containers": clean}}}})
		}
		out.API[fmt.Sprintf("/apis/apps/v1/namespaces/{namespace}/%s", r.resource)] = map[string]any{"apiVersion": "apps/v1", "kind": r.kind + "List", "items": projected}
	}
	hpas, e := read(schema.GroupVersionResource{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"})
	if e != nil {
		return "", e
	}
	projected := []any{}
	for _, hpa := range hpas {
		// Only fields read by the pinned engine's HPA guard are admitted.
		p := map[string]any{"apiVersion": "autoscaling/v2", "kind": "HorizontalPodAutoscaler", "metadata": map[string]any{"name": hpa.GetName(), "namespace": ns}}
		for _, field := range []string{"apiVersion", "kind", "name"} {
			v, found, e := unstructured.NestedString(hpa.Object, "spec", "scaleTargetRef", field)
			if e != nil || !found || v == "" {
				return "", errKRRJob
			}
			if unstructured.SetNestedField(p, v, "spec", "scaleTargetRef", field) != nil {
				return "", errKRRJob
			}
		}
		for _, path := range [][]string{{"spec", "minReplicas"}, {"spec", "maxReplicas"}, {"status", "currentReplicas"}, {"status", "desiredReplicas"}} {
			v, found, e := unstructured.NestedInt64(hpa.Object, path...)
			if e != nil || (!found && path[1] == "maxReplicas") {
				return "", errKRRJob
			}
			if found {
				if unstructured.SetNestedField(p, v, path...) != nil {
					return "", errKRRJob
				}
			}
		}
		metrics := []any{}
		rawMetrics, found, e := unstructured.NestedSlice(hpa.Object, "spec", "metrics")
		if e != nil || !found {
			return "", errKRRJob
		}
		for _, rawMetric := range rawMetrics {
			m, ok := rawMetric.(map[string]any)
			if !ok {
				return "", errKRRJob
			}
			metricType := str(m, "type")
			if metricType != "Resource" {
				switch metricType {
				case "Pods", "Object", "External", "ContainerResource":
					continue
				default:
					return "", errKRRJob
				}
			}
			name := str(m, "resource", "name")
			if name != "cpu" && name != "memory" {
				continue
			}
			targetType := str(m, "resource", "target", "type")
			if targetType != "Utilization" && targetType != "AverageValue" {
				return "", errKRRJob
			}
			target := map[string]any{"type": targetType}
			v, found, e := unstructured.NestedInt64(m, "resource", "target", "averageUtilization")
			if e != nil || (targetType == "Utilization" && !found) {
				return "", errKRRJob
			}
			if found {
				target["averageUtilization"] = v
			}
			metrics = append(metrics, map[string]any{"type": "Resource", "resource": map[string]any{"name": name, "target": target}})
		}
		if unstructured.SetNestedSlice(p, metrics, "spec", "metrics") != nil {
			return "", errKRRJob
		}
		if _, ok := p["status"]; !ok {
			p["status"] = map[string]any{}
		}
		projected = append(projected, p)
	}
	out.API["/apis/autoscaling/v2/namespaces/{namespace}/horizontalpodautoscalers"] = map[string]any{"apiVersion": "autoscaling/v2", "kind": "HorizontalPodAutoscalerList", "items": projected}
	b, e := json.Marshal(out)
	if e != nil || len(b) > krrSnapshotLimit {
		return "", errKRRJob
	}
	return string(b), nil
}
