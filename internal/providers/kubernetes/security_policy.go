package kubernetes

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/spk/spk-ocular/internal/core"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

var securityPolicyResults = []string{"pass", "fail", "warn", "error", "skip"}

// Counts leave Go as JSON numbers consumed by JavaScript. Larger integers would
// lose their source value at that boundary and must stay unknown/incomplete.
const securityPolicyMaxCount = 1<<53 - 1

// Only a discovered, supported namespaced route is admitted. Choosing one API
// never probes the other group or guesses equivalence between report objects.
func (s *session) securityPolicySource(source securitySource) (securitySource, string) {
	snap := s.cat.snap()
	if snap.state == catalogDiscovering || snap.state == catalogFailed || slices.Contains(snap.unconfirmed, source.gvr.Group) || slices.Contains(snap.unconfirmed, "*") {
		return source, "partial"
	}
	for _, d := range snap.reg.list {
		if d.gvr.Group != source.gvr.Group || d.gvr.Resource != source.gvr.Resource || !d.discovered {
			continue
		}
		source.gvr = d.gvr
		supported := d.gvr.Group == "wgpolicyk8s.io" && (d.gvr.Version == "v1alpha2" || d.gvr.Version == "v1beta1") || d.gvr.Group == "openreports.io" && d.gvr.Version == "v1alpha1"
		if !supported || !d.namespaced || d.kind != source.kind || !slices.Contains(d.verbs, "list") {
			return source, "unsupported"
		}
		return source, ""
	}
	return source, "missing"
}

func (s *session) securityPolicyReport(ref core.Ref, source securitySource, u *unstructured.Unstructured, emit func(core.SecurityFinding), truncated *bool) bool {
	if u.GetAPIVersion() != source.gvr.GroupVersion().String() || u.GetKind() != source.kind || u.GetNamespace() == "" || len(u.GetNamespace()) > 253 || u.GetName() == "" || len(u.GetName()) > 253 || u.GetUID() == "" || len(u.GetUID()) > 256 || u.GetResourceVersion() == "" || len(u.GetResourceVersion()) > 256 {
		return false
	}
	complete := true
	text := func(value string) string {
		local := false
		out := securityText(value, &local)
		if local {
			complete = false
			*truncated = true
		}
		return out
	}
	field := func(object map[string]any, path ...string) string {
		value, _, err := unstructured.NestedString(object, path...)
		if err != nil {
			complete = false
		}
		return text(value)
	}
	mapAt := func(object map[string]any, key string) map[string]any {
		value, _, err := unstructured.NestedMap(object, key)
		if err != nil {
			complete = false
		}
		return value
	}
	subject := func(object map[string]any) *core.SecurityPolicySubject {
		if object == nil {
			return nil
		}
		return &core.SecurityPolicySubject{APIVersion: field(object, "apiVersion"), Kind: field(object, "kind"), Namespace: field(object, "namespace"), Name: field(object, "name"), UID: field(object, "uid")}
	}
	selector := func(object map[string]any, key string) string {
		value, present, err := unstructured.NestedMap(object, key)
		if err != nil {
			complete = false
			return ""
		}
		if !present {
			return ""
		}
		raw, err := json.Marshal(value)
		if err != nil || len(raw) > 4096 {
			complete = false
			*truncated = true
			return ""
		}
		var typed metav1.LabelSelector
		if runtime.DefaultUnstructuredConverter.FromUnstructured(value, &typed) != nil {
			complete = false
			return ""
		}
		parsed, err := metav1.LabelSelectorAsSelector(&typed)
		if err != nil {
			complete = false
			return ""
		}
		if parsed.String() == "" {
			return "{}"
		}
		return text(parsed.String())
	}
	results, found, err := unstructured.NestedSlice(u.Object, "results")
	if err != nil || !found {
		complete = false
	}
	if len(results) > 4096 {
		results = results[:4096]
		complete = false
		*truncated = true
	}
	reportSource := ""
	var maxResults *int64
	statusFilter := []string{}
	if source.gvr.Group == "openreports.io" || source.gvr.Version == "v1beta1" {
		reportSource = field(u.Object, "source")
		if config := mapAt(u.Object, "configuration"); config != nil {
			limits := mapAt(config, "limits")
			if limits == nil {
				complete = false
			}
			if n, present, e := unstructured.NestedInt64(limits, "maxResults"); e != nil || n < 0 || n > securityPolicyMaxCount {
				complete = false
			} else if present {
				maxResults = &n
				if n > 0 && int64(len(results)) >= n {
					complete = false
				}
			}
			if raw, _, e := unstructured.NestedSlice(limits, "statusFilter"); e != nil {
				complete = false
			} else {
				if len(raw) > 16 {
					raw = raw[:16]
					complete = false
					*truncated = true
				}
				for _, item := range raw {
					value, ok := item.(string)
					if !ok || !slices.Contains(securityPolicyResults, value) {
						complete = false
					}
					statusFilter = append(statusFilter, text(value))
				}
				if len(raw) > 0 {
					for _, value := range securityPolicyResults {
						if !slices.Contains(statusFilter, value) {
							complete = false
						}
					}
				}
			}
		}
	}
	reportScope := subject(mapAt(u.Object, "scope"))
	scopeSelector := selector(u.Object, "scopeSelector")
	if reportScope != nil && u.Object["scopeSelector"] != nil {
		complete = false
	}
	counts := map[string]int64{}
	var summary map[string]int64
	if declared := mapAt(u.Object, "summary"); declared != nil {
		summary = map[string]int64{}
		for _, result := range securityPolicyResults {
			if n, present, e := unstructured.NestedInt64(declared, result); e != nil || n < 0 || n > securityPolicyMaxCount {
				complete = false
			} else if present {
				summary[result] = n
			}
		}
	}
	for _, raw := range results {
		row, ok := raw.(map[string]any)
		if !ok {
			complete = false
			continue
		}
		policy := field(row, "policy")
		if policy == "" {
			complete = false
			continue
		}
		nativeResult := field(row, "result")
		result := nativeResult
		if !slices.Contains(securityPolicyResults, result) {
			result = "unknown"
			complete = false
		} else {
			counts[result]++
		}
		nativeSeverity := field(row, "severity")
		severity := strings.ToLower(nativeSeverity)
		if !slices.Contains([]string{"critical", "high", "medium", "low"}, severity) {
			severity = "unknown"
		}
		engine := field(row, "source")
		if engine == "" {
			engine = reportSource
		}
		var scored *bool
		if v, present, e := unstructured.NestedBool(row, "scored"); e != nil {
			complete = false
		} else if present {
			scored = &v
		}
		at := int64(0)
		if timestamp := mapAt(row, "timestamp"); timestamp != nil {
			seconds, secPresent, secErr := unstructured.NestedInt64(timestamp, "seconds")
			nanos, nanoPresent, nanoErr := unstructured.NestedInt64(timestamp, "nanos")
			if secErr != nil || nanoErr != nil || !secPresent || !nanoPresent || seconds < 0 || seconds > 253402300799 || nanos < 0 || nanos >= 1e9 {
				complete = false
			} else if seconds > 0 {
				at = seconds*1000 + nanos/1e6
			}
		}
		subjects := []core.SecurityPolicySubject{}
		if values, _, e := unstructured.NestedSlice(row, "resources"); e != nil {
			complete = false
		} else {
			if len(values) > 16 {
				values = values[:16]
				complete = false
				*truncated = true
			}
			for _, value := range values {
				if object, ok := value.(map[string]any); ok {
					subjects = append(subjects, *subject(object))
				} else {
					complete = false
				}
			}
		}
		subjectSelector := selector(row, "resourceSelector")
		if len(subjects) > 0 && row["resourceSelector"] != nil {
			complete = false
		}
		properties := mapAt(row, "properties")
		rule := field(row, "rule")
		detail := &core.SecurityPolicyCheck{Result: result, NativeResult: nativeResult, NativeSeverity: nativeSeverity, Source: engine, Policy: policy, Rule: rule, Category: field(row, "category"), Scored: scored, Message: field(row, "message"), Process: field(properties, "process"), Exceptions: field(properties, "exceptions"), APIVersion: source.gvr.GroupVersion().String(), ResourceVersion: u.GetResourceVersion(), Scope: reportScope, Subjects: subjects, ScopeSelector: scopeSelector, SubjectSelector: subjectSelector, MaxResults: maxResults, StatusFilter: statusFilter}
		detail.Summary = summary
		emit(core.SecurityFinding{Ref: ref, KindTitle: source.kind, Category: "policy", Severity: severity, Rule: text(strings.TrimSpace(policy + " / " + rule)), Evidence: source.kind, Title: policy, Scanner: engine, ScannedAt: at, Policy: detail})
	}
	for result, n := range summary {
		if n != counts[result] {
			complete = false
		}
	}
	return complete
}
