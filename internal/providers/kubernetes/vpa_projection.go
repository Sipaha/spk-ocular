package kubernetes

import (
	"math"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation"
)

// Missing or invalid quantities remain absent, never an invented zero.
func vpaResources(m map[string]any, path ...string) core.VPAResources {
	read := func(name string) string {
		p := append(append([]string{}, path...), name)
		text := str(m, p...)
		if text == "" || len(text) > 64 {
			return ""
		}
		q, e := resource.ParseQuantity(text)
		if e != nil || q.Sign() < 0 {
			return ""
		}
		n := q.AsApproximateFloat64()
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return ""
		}
		return q.String()
	}
	return core.VPAResources{CPU: read("cpu"), Memory: read("memory")}
}
func vpaEnum(s string, allowed ...string) string {
	for _, v := range allowed {
		if s == v {
			return s
		}
	}
	return "unknown"
}
func vpaFreshness(m map[string]any, generation int64, path ...string) string {
	g, found, e := unstructured.NestedInt64(m, path...)
	if e != nil {
		return "stale"
	}
	if !found {
		return "unknown"
	}
	if generation <= 0 || g != generation {
		return "stale"
	}
	return "current"
}
func projectVPA(u *unstructured.Unstructured, root core.Ref, names map[string]bool) core.VPASource {
	out := core.VPASource{Ref: vpaRef(root, u, "autoscaling.k8s.io/verticalpodautoscalers"), ResourceVersion: u.GetResourceVersion(), APIVersion: u.GetAPIVersion(), UpdateMode: vpaEnum(str(u.Object, "spec", "updatePolicy", "updateMode"), "Off", "Initial", "Recreate", "Auto", "InPlaceOrRecreate", "InPlace"), State: "no-recommendation", Freshness: vpaFreshness(u.Object, u.GetGeneration(), "status", "observedGeneration"), Conditions: []core.VPACondition{}, Recommendations: []core.VPARecommendation{}, Policies: []core.VPAPolicy{}}
	recommenders, _, e := unstructured.NestedSlice(u.Object, "spec", "recommenders")
	if e != nil || len(recommenders) > 1 {
		out.Partial = true
	} else if len(recommenders) == 1 {
		m, ok := recommenders[0].(map[string]any)
		name := str(m, "name")
		if ok && certificateName(name) {
			out.Recommender = name
		} else {
			out.Partial = true
		}
	}
	conditions, _, e := unstructured.NestedSlice(u.Object, "status", "conditions")
	flags := map[string]string{}
	seen := map[string]bool{}
	if e != nil || len(conditions) > 16 {
		out.Partial = true
	} else {
		for _, item := range conditions {
			m, ok := item.(map[string]any)
			typ := str(m, "type")
			if !ok || seen[typ] {
				out.Partial = true
				out.Freshness = "stale"
				continue
			}
			seen[typ] = true
			if vpaEnum(typ, "RecommendationProvided", "LowConfidence", "FetchingHistory", "NoPodsMatched", "ConfigUnsupported", "ConfigDeprecated") == "unknown" {
				out.Partial = true
				continue
			}
			status := vpaEnum(str(m, "status"), "True", "False", "Unknown")
			fresh := vpaFreshness(m, u.GetGeneration(), "observedGeneration")
			if fresh == "stale" {
				out.Freshness = "stale"
			}
			transition := ""
			if text := str(m, "lastTransitionTime"); len(text) <= 64 {
				if t, err := time.Parse(time.RFC3339Nano, text); err == nil {
					transition = t.UTC().Format(time.RFC3339Nano)
				}
			}
			out.Conditions = append(out.Conditions, core.VPACondition{Type: typ, Status: status, Freshness: fresh, TransitionTime: transition})
			flags[typ] = status
		}
	}
	policies, _, e := unstructured.NestedSlice(u.Object, "spec", "resourcePolicy", "containerPolicies")
	seen = map[string]bool{}
	if e != nil || len(policies) > 17 {
		out.Partial = true
	} else {
		for _, item := range policies {
			m, ok := item.(map[string]any)
			name := str(m, "containerName")
			if !ok || (name != "*" && len(validation.IsDNS1123Label(name)) != 0) || seen[name] {
				out.Partial = true
				continue
			}
			seen[name] = true
			p := core.VPAPolicy{Container: name, Mode: vpaEnum(str(m, "mode"), "Off", "Auto"), ControlledValues: vpaEnum(str(m, "controlledValues"), "RequestsAndLimits", "RequestsOnly"), ControlledResources: []string{}, MinAllowed: vpaResources(m, "minAllowed"), MaxAllowed: vpaResources(m, "maxAllowed")}
			resources, found, err := unstructured.NestedStringSlice(m, "controlledResources")
			if err != nil || len(resources) > 2 {
				out.Partial = true
			} else if found {
				p.ControlledResourcesKnown = true
				used := map[string]bool{}
				for _, v := range resources {
					if (v != "cpu" && v != "memory") || used[v] {
						out.Partial = true
						continue
					}
					used[v] = true
					p.ControlledResources = append(p.ControlledResources, v)
				}
			}
			out.Policies = append(out.Policies, p)
		}
	}
	recommendations, _, e := unstructured.NestedSlice(u.Object, "status", "recommendation", "containerRecommendations")
	seen = map[string]bool{}
	if e != nil || len(recommendations) > 16 {
		out.Partial = true
		out.State = "invalid"
	} else {
		for _, item := range recommendations {
			m, ok := item.(map[string]any)
			name := str(m, "containerName")
			if !ok || !names[name] || seen[name] {
				out.Partial = true
				out.State = "invalid"
				out.Recommendations = []core.VPARecommendation{}
				break
			}
			seen[name] = true
			rec := core.VPARecommendation{Container: name, Target: vpaResources(m, "target"), LowerBound: vpaResources(m, "lowerBound"), UpperBound: vpaResources(m, "upperBound"), UncappedTarget: vpaResources(m, "uncappedTarget")}
			if rec.Target.CPU == "" && rec.Target.Memory == "" {
				out.Partial = true
				continue
			}
			out.Recommendations = append(out.Recommendations, rec)
		}
		if out.State != "invalid" && len(out.Recommendations) > 0 {
			out.State = "reported"
		}
	}
	if flags["RecommendationProvided"] == "False" {
		out.Recommendations = []core.VPARecommendation{}
		out.State = "no-recommendation"
	}
	for _, flag := range []struct{ name, state string }{{"LowConfidence", "low-confidence"}, {"FetchingHistory", "fetching-history"}, {"NoPodsMatched", "no-pods"}, {"ConfigUnsupported", "config-unsupported"}} {
		if flags[flag.name] == "True" {
			out.State = flag.state
			if flag.name == "NoPodsMatched" || flag.name == "ConfigUnsupported" {
				out.Recommendations = []core.VPARecommendation{}
			}
		}
	}
	if out.Freshness == "stale" {
		out.State = "stale"
		out.Recommendations = []core.VPARecommendation{}
	}
	return out
}
