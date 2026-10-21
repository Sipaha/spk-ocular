package kubernetes

import (
	"fmt"
	"github.com/spk/spk-ocular/internal/core"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"
	"strconv"
	"strings"
)

func projectReliability(out *core.Reliability, u *unstructured.Unstructured) {
	specPath := []string{"spec"}
	prefix := "spec"
	if u.GetKind() != "Pod" {
		specPath = []string{"spec", "template", "spec"}
		prefix = "spec.template.spec"
	}
	spec, found, err := unstructured.NestedMap(u.Object, specPath...)
	if err != nil || !found {
		out.Partial = true
		reliabilityAdd(out, u, "probes", prefix, "", "unknown", "invalid", "")
		return
	}
	restart, rf, re := unstructured.NestedString(spec, "restartPolicy")
	batch := re == nil && rf && (restart == "Never" || restart == "OnFailure")
	if u.GetKind() == "Pod" {
		for _, o := range u.GetOwnerReferences() {
			if o.APIVersion == "batch/v1" && (o.Kind == "Job" || o.Kind == "CronJob") {
				batch = true
			}
		}
	}
	if re != nil || rf && (restart != "Always" && restart != "Never" && restart != "OnFailure") {
		out.Partial = true
		batch = true
	}
	count := 0
	seen := map[string]bool{}
	for _, group := range []string{"containers", "initContainers"} {
		containers, f, e := unstructured.NestedSlice(spec, group)
		if e != nil || group == "containers" && (!f || len(containers) == 0) {
			out.Partial = true
		}
		for i, v := range containers {
			count++
			if count > 64 {
				out.Partial = true
				break
			}
			c, ok := v.(map[string]any)
			if !ok {
				out.Partial = true
				continue
			}
			name, nf, ne := unstructured.NestedString(c, "name")
			if ne != nil || !nf || len(validation.IsDNS1123Label(name)) != 0 || seen[name] {
				out.Partial = true
				continue
			}
			seen[name] = true
			field := fmt.Sprintf("%s.%s[%d]", prefix, group, i)
			sidecar := str(c, "restartPolicy") == "Always"
			for _, probe := range []string{"readinessProbe", "livenessProbe", "startupProbe"} {
				_, present, e := unstructured.NestedMap(c, probe)
				outcome, code, value := "observed", "probe", "present"
				if !present {
					outcome = "review"
					value = "missing"
				}
				if e != nil {
					outcome = "unknown"
					code = "invalid"
					value = ""
				}
				if batch || group == "initContainers" && !sidecar {
					outcome = "not-applicable"
					code = "probeExcluded"
					value = ""
				}
				reliabilityAdd(out, u, "probes", field+"."+probe, name, outcome, code, value)
			}
			for _, typ := range []string{"requests", "limits"} {
				for _, key := range []string{"cpu", "memory"} {
					value, f, e := unstructured.NestedString(c, "resources", typ, key)
					outcome, code := "observed", "resource"
					if e != nil {
						outcome = "unknown"
						code = "invalid"
						value = ""
					} else if !f {
						outcome = "review"
						code = "resourceMissing"
						value = ""
					} else {
						q, e := resource.ParseQuantity(value)
						if e != nil || q.Sign() < 0 || len(value) > 128 {
							outcome = "unknown"
							code = "invalid"
							value = ""
						} else {
							value = q.String()
						}
					}
					if typ == "limits" && key == "cpu" && outcome == "review" {
						code = "cpuPolicy"
					}
					reliabilityAdd(out, u, "resources", field+".resources."+typ+"."+key, name, outcome, code, value)
				}
			}
		}
	}
	reliabilityAdd(out, u, "resources", prefix+".resources", "", "observed", "allocationCaveat", "")
	reliabilityAdd(out, u, "probes", prefix+".ephemeralContainers", "", "not-applicable", "probeExcluded", "")
	reliabilityPlacement(out, u, spec, prefix)
	reliabilityRollout(out, u)
}
func reliabilityPlacement(out *core.Reliability, u *unstructured.Unstructured, spec map[string]any, prefix string) {
	add := func(field string, n int, valid bool) {
		outcome, code := "observed", "placement"
		if !valid || n > 32 {
			outcome = "unknown"
			code = "invalid"
			out.Partial = true
		}
		value := ""
		if valid && n <= 32 {
			value = strconv.Itoa(n)
		}
		reliabilityAdd(out, u, "placement", prefix+"."+field, "", outcome, code, value)
	}
	m, _, e := unstructured.NestedStringMap(spec, "nodeSelector")
	add("nodeSelector", len(m), e == nil)
	for _, p := range [][]string{{"affinity", "nodeAffinity", "requiredDuringSchedulingIgnoredDuringExecution", "nodeSelectorTerms"}, {"affinity", "podAffinity", "requiredDuringSchedulingIgnoredDuringExecution"}, {"affinity", "podAntiAffinity", "requiredDuringSchedulingIgnoredDuringExecution"}} {
		v, _, e := unstructured.NestedSlice(spec, p...)
		add(strings.Join(p, "."), len(v), e == nil)
	}
	spread, _, e := unstructured.NestedSlice(spec, "topologySpreadConstraints")
	n := 0
	valid := e == nil && len(spread) <= 32
	if valid {
		for _, v := range spread {
			m, ok := v.(map[string]any)
			if !ok {
				valid = false
				continue
			}
			if str(m, "whenUnsatisfiable") == "DoNotSchedule" {
				n++
			}
		}
	}
	add("topologySpreadConstraints[DoNotSchedule]", n, valid)
}
func reliabilityRollout(out *core.Reliability, u *unstructured.Unstructured) {
	if u.GetKind() == "Pod" {
		return
	}
	add := func(field, outcome, code, value string) {
		reliabilityAdd(out, u, "rollout", field, "", outcome, code, value)
	}
	replicas, found, e := unstructured.NestedInt64(u.Object, "spec", "replicas")
	if !found && e == nil {
		replicas = 1
	}
	if u.GetKind() != "DaemonSet" {
		if e != nil || replicas < 0 || replicas > 2147483647 {
			add("spec.replicas", "unknown", "invalid", "")
			return
		}
		add("spec.replicas", "observed", "replicas", strconv.FormatInt(replicas, 10))
	}
	if u.GetKind() == "Deployment" {
		paused, _, pe := unstructured.NestedBool(u.Object, "spec", "paused")
		if pe != nil {
			add("spec.paused", "unknown", "invalid", "")
		} else {
			add("spec.paused", "observed", "rollout", strconv.FormatBool(paused))
		}
		strategy, f, e := unstructured.NestedString(u.Object, "spec", "strategy", "type")
		if !f && e == nil {
			strategy = "RollingUpdate"
		}
		if e != nil || (strategy != "RollingUpdate" && strategy != "Recreate") {
			add("spec.strategy.type", "unknown", "invalid", "")
			return
		}
		if strategy == "Recreate" {
			add("spec.strategy.type", "review", "recreate", strategy)
			return
		}
		add("spec.strategy.type", "observed", "rollout", strategy)
		for _, key := range []string{"maxUnavailable", "maxSurge"} {
			v, f, e := unstructured.NestedFieldNoCopy(u.Object, "spec", "strategy", "rollingUpdate", key)
			if !f && e == nil {
				v = "25%"
			}
			var ios intstr.IntOrString
			valid := e == nil
			switch x := v.(type) {
			case int64:
				if x < 0 || x > 2147483647 {
					valid = false
				} else {
					ios = intstr.FromInt32(int32(x))
				}
			case string:
				if len(x) > 16 || !strings.HasSuffix(x, "%") {
					valid = false
				} else {
					n, err := strconv.Atoi(strings.TrimSuffix(x, "%"))
					valid = valid && err == nil && n >= 0 && n <= 2147483647 && (key == "maxSurge" || n <= 100)
					ios = intstr.FromString(x)
				}
			default:
				valid = false
			}
			value := ""
			if valid {
				n, err := intstr.GetScaledValueFromIntOrPercent(&ios, int(replicas), key == "maxSurge")
				valid = err == nil
				if key == "maxUnavailable" && n > int(replicas) {
					n = int(replicas)
				}
				value = ios.String() + " → " + strconv.Itoa(n)
			}
			if valid {
				add("spec.strategy.rollingUpdate."+key, "observed", "rollout", value)
			} else {
				add("spec.strategy.rollingUpdate."+key, "unknown", "invalid", "")
			}
		}
		return
	}
	typ, f, e := unstructured.NestedString(u.Object, "spec", "updateStrategy", "type")
	if !f && e == nil {
		typ = "RollingUpdate"
	}
	if e != nil || (typ != "RollingUpdate" && typ != "OnDelete") {
		add("spec.updateStrategy.type", "unknown", "invalid", "")
	} else {
		add("spec.updateStrategy.type", "observed", "rollout", typ)
	}
	keys := []string{"partition", "maxUnavailable"}
	if u.GetKind() == "DaemonSet" {
		keys = []string{"maxUnavailable", "maxSurge"}
	}
	for _, key := range keys {
		v, f, e := unstructured.NestedFieldNoCopy(u.Object, "spec", "updateStrategy", "rollingUpdate", key)
		if !f && e == nil {
			continue
		}
		value := ""
		valid := e == nil
		switch x := v.(type) {
		case int64:
			valid = valid && x >= 0 && x <= 2147483647
			value = strconv.FormatInt(x, 10)
		case string:
			n, err := strconv.Atoi(strings.TrimSuffix(x, "%"))
			valid = valid && key != "partition" && strings.HasSuffix(x, "%") && err == nil && n >= 0 && n <= 2147483647 && (key == "maxSurge" || n <= 100)
			value = x
		default:
			valid = false
		}
		if valid {
			add("spec.updateStrategy.rollingUpdate."+key, "observed", "rollout", value)
		} else {
			add("spec.updateStrategy.rollingUpdate."+key, "unknown", "invalid", "")
		}
	}
}

func reliabilitySelector(u *unstructured.Unstructured, set map[string]string) (bool, bool) {
	raw, f, e := unstructured.NestedFieldNoCopy(u.Object, "spec", "selector")
	if e != nil {
		return false, false
	}
	if !f || raw == nil {
		return false, true
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return false, false
	}
	if m == nil {
		return false, true
	}
	// Bound selector grammar before typed conversion; reject unknown fields.
	for k := range m {
		if k != "matchLabels" && k != "matchExpressions" {
			return false, false
		}
	}
	ml, _, e := unstructured.NestedStringMap(m, "matchLabels")
	if e != nil || len(ml) > 32 {
		return false, false
	}
	for k, v := range ml {
		if len(validation.IsQualifiedName(k)) != 0 || len(validation.IsValidLabelValue(v)) != 0 {
			return false, false
		}
	}
	ex, _, e := unstructured.NestedSlice(m, "matchExpressions")
	if e != nil || len(ex) > 32 {
		return false, false
	}
	for _, v := range ex {
		x, ok := v.(map[string]any)
		if !ok {
			return false, false
		}
		for k := range x {
			if k != "key" && k != "operator" && k != "values" {
				return false, false
			}
		}
		values, _, e := unstructured.NestedStringSlice(x, "values")
		if e != nil || len(values) > 32 {
			return false, false
		}
		for _, s := range values {
			if len(validation.IsValidLabelValue(s)) != 0 {
				return false, false
			}
		}
	}
	var ls metav1.LabelSelector
	if runtimeFromMap(m, &ls) != nil {
		return false, false
	}
	selector, e := metav1.LabelSelectorAsSelector(&ls)
	if e != nil {
		return false, false
	}
	return selector.Matches(labels.Set(set)), true
}
func (r *reliabilityReader) pdbs(root *unstructured.Unstructured) {
	path := []string{"metadata", "labels"}
	if root.GetKind() != "Pod" {
		path = []string{"spec", "template", "metadata", "labels"}
	}
	set, _, e := unstructured.NestedStringMap(root.Object, path...)
	if e != nil || len(set) > 128 {
		r.out.PDBState = "unknown"
		return
	}
	matches := 0
	r.out.PDBState = r.collection(pdbGVR, "PodDisruptionBudget", func(u *unstructured.Unstructured) bool {
		match, valid := reliabilitySelector(u, set)
		if !valid {
			return false
		}
		if !match {
			return true
		}
		matches++
		if !r.verify(pdbGVR, u) {
			return false
		}
		// Controller labels establish candidates, never live Pod protection.
		code := "pdbCandidate"
		if root.GetKind() == "Pod" {
			code = "pdbSelected"
		}
		reliabilityAdd(r.out, u, "pdb", "spec.selector", "", "observed", code, "")
		allowed, current := currentPDBDisruptions(u)
		outcome, value := "unknown", ""
		if current {
			outcome = "observed"
			value = strconv.FormatInt(allowed, 10)
		}
		reliabilityAdd(r.out, u, "pdb", "status.disruptionsAllowed", "", outcome, "pdbStatus", value)
		return true
	})
	if r.out.PDBState == "complete" && matches == 0 {
		reliabilityAdd(r.out, root, "pdb", strings.Join(path, "."), "", "observed", "pdbNone", "")
	}
	if matches > 1 {
		reliabilityAdd(r.out, root, "pdb", strings.Join(path, "."), "", "review", "pdbMultiple", strconv.Itoa(matches))
	}
}
func (r *reliabilityReader) hpas(root *unstructured.Unstructured) {
	matches := 0
	r.out.HPAState = r.collection(reliabilityHPAGVR, "HorizontalPodAutoscaler", func(u *unstructured.Unstructured) bool {
		v, vf, e := unstructured.NestedString(u.Object, "spec", "scaleTargetRef", "apiVersion")
		k, kf, ke := unstructured.NestedString(u.Object, "spec", "scaleTargetRef", "kind")
		n, nf, ne := unstructured.NestedString(u.Object, "spec", "scaleTargetRef", "name")
		if e != nil || ke != nil || ne != nil || !vf || !kf || !nf || v == "" || k == "" || !certificateName(n) {
			return false
		}
		if v != "apps/v1" || k != root.GetKind() || n != root.GetName() {
			return true
		}
		matches++
		if !r.verify(reliabilityHPAGVR, u) {
			return false
		}
		add := func(field, outcome, code, value string) {
			reliabilityAdd(r.out, u, "hpa", field, "", outcome, code, value)
		}
		add("spec.scaleTargetRef", "observed", "hpaAssociation", "")
		observed, f, e := unstructured.NestedInt64(u.Object, "status", "observedGeneration")
		current := e == nil && f && observed > 0 && observed == u.GetGeneration()
		for _, p := range [][]string{{"spec", "minReplicas"}, {"spec", "maxReplicas"}, {"status", "currentReplicas"}, {"status", "desiredReplicas"}} {
			value, f, e := unstructured.NestedInt64(u.Object, p...)
			outcome, text := "unknown", ""
			if e == nil && f && value >= 0 && value <= 2147483647 && (p[0] == "spec" || current) {
				outcome = "observed"
				text = strconv.FormatInt(value, 10)
			}
			add(strings.Join(p, "."), outcome, "hpaStatus", text)
		}
		conditions, _, ce := unstructured.NestedSlice(u.Object, "status", "conditions")
		if ce != nil || len(conditions) > 32 {
			r.out.Partial = true
			add("status.conditions", "unknown", "invalid", "")
		} else {
			seen := map[string]bool{}
			duplicate := false
			for _, v := range conditions {
				c, ok := v.(map[string]any)
				if !ok {
					duplicate = true
					continue
				}
				typ := str(c, "type")
				if seen[typ] {
					duplicate = true
				}
				seen[typ] = true
			}
			if duplicate {
				add("status.conditions", "unknown", "invalid", "")
			} else {
				for _, v := range conditions {
					c, ok := v.(map[string]any)
					if !ok {
						continue
					}
					typ := str(c, "type")
					if typ != "AbleToScale" && typ != "ScalingActive" && typ != "ScalingLimited" {
						continue
					}
					status := str(c, "status")
					cg, cf, ce := unstructured.NestedInt64(c, "observedGeneration")
					fresh := current && ce == nil && (!cf || cg == u.GetGeneration())
					if !fresh || (status != "True" && status != "False") {
						status = "Unknown"
					}
					outcome := "observed"
					if status == "Unknown" {
						outcome = "unknown"
					}
					add("status.conditions["+typ+"]", outcome, "hpaStatus", status)
				}
			}
		}
		metrics, _, me := unstructured.NestedSlice(u.Object, "spec", "metrics")
		if me != nil || len(metrics) > 32 {
			r.out.Partial = true
			add("spec.metrics", "unknown", "invalid", "")
		} else {
			for i, v := range metrics {
				m, ok := v.(map[string]any)
				if !ok {
					add("spec.metrics", "unknown", "invalid", "")
					continue
				}
				typ := str(m, "type")
				key := "resource"
				if typ == "ContainerResource" {
					key = "containerResource"
				} else if typ != "Resource" {
					continue
				}
				if str(m, key, "name") == "cpu" && str(m, key, "target", "type") == "Utilization" {
					add(fmt.Sprintf("spec.metrics[%d]", i), "review", "hpaCPU", "")
				}
			}
		}
		return true
	})
	if matches > 1 && r.out.HPAState == "complete" {
		r.out.HPAState = "ambiguous"
	}
}
