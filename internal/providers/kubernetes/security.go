package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spk/spk-ocular/internal/core"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var vulnerabilityReportGVR = schema.GroupVersionResource{Group: "aquasecurity.github.io", Version: "v1alpha1", Resource: "vulnerabilityreports"}

const securityResourceLimit = 6000
const securityFindingLimit = 12000
const securityByteLimit = 16 << 20
const securityTextLimit = 512

type securitySource struct {
	gvr    schema.GroupVersionResource
	kind   string
	global bool
}
type securityPolicy struct {
	namespace       string
	selector        labels.Selector
	ingress, egress bool
}
type securityWorkload struct {
	ref    core.Ref
	kind   string
	spec   map[string]any
	labels map[string]string
}

var securitySources = []securitySource{
	{schema.GroupVersionResource{Version: "v1", Resource: "serviceaccounts"}, "ServiceAccount", false},
	{schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}, "NetworkPolicy", false},
	{schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, "Deployment", false},
	{schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}, "StatefulSet", false},
	{schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}, "DaemonSet", false},
	{schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"}, "CronJob", false},
	{schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}, "Job", false},
	{schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}, "ReplicaSet", false},
	{schema.GroupVersionResource{Version: "v1", Resource: "pods"}, "Pod", false},
	{schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"}, "Role", false},
	{schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"}, "ClusterRole", true},
	{vulnerabilityReportGVR, "VulnerabilityReport", false},
}

// Security projects only declared risk fields. No Secret/ConfigMap resources,
// token contents, image downloads, scanners or writes are requested.
func (s *session) Security(parent context.Context, scope core.ScopeSel) (core.SecuritySnapshot, error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	select {
	case s.graphGate <- struct{}{}:
		defer func() { <-s.graphGate }()
	case <-ctx.Done():
		return core.SecuritySnapshot{}, ctx.Err()
	}
	out := core.SecuritySnapshot{Findings: []core.SecurityFinding{}, Problems: []core.GraphProblem{}, Discovery: s.cat.snap().state, CVEState: "empty"}
	namespaces := []string{""}
	if scope.Mode != core.ScopeAll {
		namespaces = scope.SelectedNames()
	}
	if len(namespaces) == 0 {
		out.CVEState = "not-requested"
		out.CapturedAt = s.now().UnixMilli()
		return out, nil
	}
	if len(namespaces) > 128 {
		namespaces = namespaces[:128]
		out.Truncated = true
	}
	workloads := []securityWorkload{}
	accounts := map[string]bool{}
	policies := []securityPolicy{}
	policyComplete := map[string]bool{}
	accountComplete := map[string]bool{}
	cveErrors := map[string]bool{}
	cveOK := false
	usedBytes := 0
	pages := 0
	seenObjects := map[string]bool{}
	emit := func(f core.SecurityFinding) {
		if len(out.Findings) >= securityFindingLimit {
			out.Truncated = true
			return
		}
		f.ID = fmt.Sprintf("%s/%s/%s/%d", f.Ref.UID, f.Container, f.Rule, len(out.Findings))
		out.Findings = append(out.Findings, f)
	}
	for _, source := range securitySources {
		sourceNamespaces := namespaces
		if source.global {
			sourceNamespaces = []string{""}
		}
		for _, ns := range sourceNamespaces {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			if out.Resources >= securityResourceLimit || usedBytes >= securityByteLimit || pages >= 4096 {
				out.Truncated = true
				break
			}
			jobCtx, done := context.WithTimeout(ctx, 20*time.Second)
			token := ""
			seen := map[string]bool{}
			complete := true
			for page := 0; ; page++ {
				if out.Resources >= securityResourceLimit || usedBytes >= securityByteLimit || pages >= 4096 || page >= 512 {
					out.Truncated = true
					complete = false
					break
				}
				pages++
				list, err := s.dyn.Resource(source.gvr).Namespace(ns).List(jobCtx, metav1.ListOptions{Limit: 50, Continue: token})
				if err != nil {
					class, _ := classify(err)
					// An optional report API that is not installed is setup state,
					// not failed coverage. A missing namespace is still a read error.
					missingReportAPI := source.gvr == vulnerabilityReportGVR && apierrors.IsNotFound(err)
					if status, ok := err.(apierrors.APIStatus); ok && status.Status().Details != nil && status.Status().Details.Kind == "namespaces" {
						missingReportAPI = false
					}
					if !missingReportAPI {
						out.Problems = append(out.Problems, core.GraphProblem{Kind: source.gvr.String(), Scope: ns, Class: string(class)})
					}
					complete = false
					if source.gvr == vulnerabilityReportGVR {
						state := "error"
						if missingReportAPI {
							state = "missing"
						}
						if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
							state = "denied"
						}
						cveErrors[state] = true
					}
					break
				}
				if source.gvr == vulnerabilityReportGVR {
					cveOK = true
				}
				for i := range list.Items {
					u := &list.Items[i]
					if u.GetUID() == "" || (!source.global && !scope.Contains(u.GetNamespace())) {
						continue
					}
					raw, err := json.Marshal(u.Object)
					if err != nil {
						out.Truncated = true
						complete = false
						continue
					}
					usedBytes += len(raw)
					if len(raw) > 1<<20 || usedBytes > securityByteLimit || out.Resources >= securityResourceLimit {
						out.Truncated = true
						complete = false
						break
					}
					objectKey := source.gvr.String() + "/" + string(u.GetUID())
					if seenObjects[objectKey] {
						out.Truncated = true
						complete = false
						continue
					}
					seenObjects[objectKey] = true
					out.Resources++
					ref := core.Ref{Provider: ProviderID, Target: s.target, Kind: s.kindFor(source.gvr.GroupVersion().String(), source.kind), Name: u.GetName(), Scope: u.GetNamespace(), UID: string(u.GetUID())}
					switch source.kind {
					case "ServiceAccount":
						enabled := true
						if value, found, _ := unstructured.NestedBool(u.Object, "automountServiceAccountToken"); found {
							enabled = value
						}
						accounts[u.GetNamespace()+"/"+u.GetName()] = enabled
					case "NetworkPolicy":
						selectorMap, _, _ := unstructured.NestedMap(u.Object, "spec", "podSelector")
						var selector metav1.LabelSelector
						if err := runtime.DefaultUnstructuredConverter.FromUnstructured(selectorMap, &selector); err != nil {
							complete = false
							out.Problems = append(out.Problems, core.GraphProblem{Kind: source.gvr.String(), Scope: ns, Class: "invalid_selector"})
							continue
						}
						sel, err := metav1.LabelSelectorAsSelector(&selector)
						if err != nil {
							complete = false
							out.Problems = append(out.Problems, core.GraphProblem{Kind: source.gvr.String(), Scope: ns, Class: "invalid_selector"})
							continue
						}
						types, _, _ := unstructured.NestedStringSlice(u.Object, "spec", "policyTypes")
						ingress := true
						egress := false
						if len(types) > 0 {
							ingress = slices.Contains(types, "Ingress")
							egress = slices.Contains(types, "Egress")
						} else {
							_, found, _ := unstructured.NestedSlice(u.Object, "spec", "egress")
							egress = found
						}
						policies = append(policies, securityPolicy{u.GetNamespace(), sel, ingress, egress})
					case "Role", "ClusterRole":
						s.securityRole(ref, source.kind, u, emit, &out.Truncated)
					case "VulnerabilityReport":
						out.Reports++
						s.securityReport(ref, u, emit, &out.Truncated)
					default:
						path := []string{"spec"}
						labelPath := []string{"metadata", "labels"}
						if source.kind != "Pod" {
							path = []string{"spec", "template", "spec"}
							labelPath = []string{"spec", "template", "metadata", "labels"}
						}
						if source.kind == "CronJob" {
							path = []string{"spec", "jobTemplate", "spec", "template", "spec"}
							labelPath = []string{"spec", "jobTemplate", "spec", "template", "metadata", "labels"}
						}
						spec, found, _ := unstructured.NestedMap(u.Object, path...)
						if !found {
							continue
						}
						ls, _, _ := unstructured.NestedStringMap(u.Object, labelPath...)
						workloads = append(workloads, securityWorkload{ref: ref, kind: source.kind, spec: spec, labels: ls})
					}
				}
				next := list.GetContinue()
				if next == "" {
					break
				}
				if seen[next] {
					out.Truncated = true
					complete = false
					break
				}
				seen[next] = true
				token = next
			}
			done()
			if source.kind == "NetworkPolicy" {
				policyComplete[ns] = complete
			}
			if source.kind == "ServiceAccount" {
				accountComplete[ns] = complete
			}
		}
	}
	for _, w := range workloads {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		s.securityPod(w, accounts, accountComplete, emit, &out.Truncated)
		if policyComplete[w.ref.Scope] || policyComplete[""] {
			ingress, egress := false, false
			for _, p := range policies {
				if p.namespace == w.ref.Scope && p.selector.Matches(labels.Set(w.labels)) {
					ingress = ingress || p.ingress
					egress = egress || p.egress
				}
			}
			for _, direction := range []struct {
				name    string
				covered bool
			}{{"ingress", ingress}, {"egress", egress}} {
				if !direction.covered {
					emit(core.SecurityFinding{Ref: w.ref, KindTitle: w.kind, Category: "network", Severity: "medium", Rule: "network-" + direction.name, Evidence: "NetworkPolicy / " + direction.name})
				}
			}
		}
	}
	if len(cveErrors) > 0 {
		out.CVEState = "partial"
		if !cveOK {
			out.CVEState = "error"
			if cveErrors["denied"] {
				out.CVEState = "denied"
			} else if len(cveErrors) == 1 && cveErrors["missing"] {
				out.CVEState = "missing"
			}
		}
	} else if out.Truncated {
		out.CVEState = "partial"
	} else if out.Reports > 0 {
		out.CVEState = "available"
	}
	sort.SliceStable(out.Findings, func(i, j int) bool {
		return securitySeverity(out.Findings[i].Severity) < securitySeverity(out.Findings[j].Severity)
	})
	if err := ctx.Err(); err != nil {
		return out, err
	}
	out.CapturedAt = s.now().UnixMilli()
	return out, nil
}
func securitySeverity(s string) int {
	switch s {
	case "critical":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	case "low":
		return 3
	default:
		return 4
	}
}
func securityText(s string, truncated *bool) string {
	if len(s) > securityTextLimit {
		*truncated = true
		s = s[:securityTextLimit]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s
}
func securityBool(o map[string]any, path ...string) (bool, bool) {
	v, ok, _ := unstructured.NestedBool(o, path...)
	return v, ok
}
func securityMap(o map[string]any, path ...string) map[string]any {
	m, _, _ := unstructured.NestedMap(o, path...)
	if m == nil {
		return map[string]any{}
	}
	return m
}
func securityNumber(o map[string]any, key string) (int64, bool) {
	v, ok, _ := unstructured.NestedInt64(o, key)
	return v, ok
}
func securityList(o map[string]any, key string) []any {
	v, _, _ := unstructured.NestedSlice(o, key)
	return v
}
func (s *session) securityPod(w securityWorkload, accounts, complete map[string]bool, emit func(core.SecurityFinding), truncated *bool) {
	add := func(rule, severity, container, evidence string) {
		emit(core.SecurityFinding{Ref: w.ref, KindTitle: w.kind, Category: "configuration", Severity: severity, Rule: rule, Container: securityText(container, truncated), Evidence: evidence})
	}
	podSC := securityMap(w.spec, "securityContext")
	for _, key := range []string{"hostNetwork", "hostPID", "hostIPC"} {
		if boolAt(w.spec, key) {
			add("host-access", "high", "", key+"=true")
		}
	}
	for _, v := range securityList(w.spec, "volumes") {
		volume, ok := v.(map[string]any)
		if ok {
			if _, exists := volume["hostPath"]; exists {
				add("host-path", "high", "", "spec.volumes[].hostPath")
			}
		}
	}
	windows := str(w.spec, "os", "name") == "windows"
	for _, key := range []string{"containers", "initContainers", "ephemeralContainers"} {
		containers := securityList(w.spec, key)
		if len(containers) > 256 {
			*truncated = true
			containers = containers[:256]
		}
		for _, item := range containers {
			c, ok := item.(map[string]any)
			if !ok {
				continue
			}
			name := str(c, "name")
			sc := securityMap(c, "securityContext")
			if boolAt(sc, "privileged") || boolAt(sc, "windowsOptions", "hostProcess") || boolAt(podSC, "windowsOptions", "hostProcess") {
				add("privileged", "critical", name, "securityContext.privileged / hostProcess=true")
			}
			if windows {
				continue
			}
			if value, found := securityBool(sc, "allowPrivilegeEscalation"); !found || value {
				add("escalation", "high", name, "allowPrivilegeEscalation != false")
			}
			nonroot, found := securityBool(sc, "runAsNonRoot")
			if !found {
				nonroot, _ = securityBool(podSC, "runAsNonRoot")
			}
			uid, uidSet := securityNumber(sc, "runAsUser")
			if !uidSet {
				uid, uidSet = securityNumber(podSC, "runAsUser")
			}
			if (uidSet && uid == 0) || (!nonroot && (!uidSet || uid <= 0)) {
				add("root", "high", name, "runAsNonRoot / runAsUser")
			}
			if !boolAt(sc, "readOnlyRootFilesystem") {
				add("writable-root", "medium", name, "readOnlyRootFilesystem != true")
			}
			drops, _, _ := unstructured.NestedStringSlice(sc, "capabilities", "drop")
			adds, _, _ := unstructured.NestedStringSlice(sc, "capabilities", "add")
			unsafeAdd := false
			for _, cap := range adds {
				if cap != "NET_BIND_SERVICE" {
					unsafeAdd = true
				}
			}
			if !slices.Contains(drops, "ALL") || unsafeAdd {
				add("capabilities", "medium", name, "capabilities.drop / capabilities.add")
			}
			seccomp := str(sc, "seccompProfile", "type")
			if seccomp == "" {
				seccomp = str(podSC, "seccompProfile", "type")
			}
			if seccomp != "RuntimeDefault" && seccomp != "Localhost" {
				add("seccomp", "medium", name, "seccompProfile.type")
			}
		}
	}
	account := nonEmpty(str(w.spec, "serviceAccountName"), "default")
	enabled, explicit := securityBool(w.spec, "automountServiceAccountToken")
	if !explicit {
		var known bool
		enabled, known = accounts[w.ref.Scope+"/"+account]
		if !known {
			if !complete[w.ref.Scope] && !complete[""] {
				return
			}
			enabled = true
		}
	}
	if enabled {
		add("token-mount", "medium", "", "automountServiceAccountToken != false")
		if account == "default" {
			add("default-account", "low", "", "serviceAccountName=default")
		}
	}
}
func (s *session) securityRole(ref core.Ref, kind string, u *unstructured.Unstructured, emit func(core.SecurityFinding), truncated *bool) {
	rules := securityList(u.Object, "rules")
	if len(rules) > 1024 {
		*truncated = true
		rules = rules[:1024]
	}
	for _, item := range rules {
		r, ok := item.(map[string]any)
		if !ok {
			continue
		}
		verbs := rbacStrings(r, "verbs")
		resources := rbacStrings(r, "resources")
		groups := rbacStrings(r, "apiGroups")
		has := func(values []string, value string) bool {
			return slices.Contains(values, value) || slices.Contains(values, "*")
		}
		add := func(rule, severity string) {
			emit(core.SecurityFinding{Ref: ref, KindTitle: kind, Category: "rbac", Severity: severity, Rule: rule, Evidence: securityText("verbs="+strings.Join(verbs, ",")+"; apiGroups="+strings.Join(groups, ",")+"; resources="+strings.Join(resources, ",")+"; resourceNames="+strings.Join(rbacStrings(r, "resourceNames"), ","), truncated)})
		}
		if slices.Contains(verbs, "*") || slices.Contains(resources, "*") || slices.Contains(groups, "*") {
			add("rbac-wildcard", "high")
		}
		if has(groups, "") && has(resources, "secrets") && (has(verbs, "get") || has(verbs, "list") || has(verbs, "watch")) {
			add("rbac-secrets", "high")
		}
		// Impersonation applies to specific identity resources, not every rule
		// whose verb set contains a wildcard.
		extra := false
		for _, resource := range resources {
			if resource == "userextras" || strings.HasPrefix(resource, "userextras/") {
				extra = true
			}
		}
		impersonates := has(verbs, "impersonate") && ((has(groups, "") && (has(resources, "users") || has(resources, "groups") || has(resources, "serviceaccounts"))) || (has(groups, "authentication.k8s.io") && (has(resources, "uids") || has(resources, "userextras") || extra)))
		if (has(groups, "rbac.authorization.k8s.io") && (has(verbs, "bind") || has(verbs, "escalate")) && (has(resources, "roles") || has(resources, "clusterroles"))) || impersonates {
			add("rbac-escalation", "critical")
		}
		if has(groups, "") && has(resources, "pods/exec") && has(verbs, "create") {
			add("rbac-exec", "high")
		}
	}
}
func (s *session) securityReport(ref core.Ref, u *unstructured.Unstructured, emit func(core.SecurityFinding), truncated *bool) {
	report := securityMap(u.Object, "report")
	image := str(report, "artifact", "repository")
	if tag := str(report, "artifact", "tag"); tag != "" {
		image += ":" + tag
	}
	if digest := str(report, "artifact", "digest"); digest != "" {
		image += "@" + digest
	}
	scanner := str(report, "scanner", "name") + " " + str(report, "scanner", "version")
	scannedAt := timeAt(report, "updateTimestamp")
	workload := ""
	if owner := metav1.GetControllerOfNoCopy(u); owner != nil {
		workload = owner.Kind + "/" + owner.Name
	} else if owners := u.GetOwnerReferences(); len(owners) > 0 {
		workload = owners[0].Kind + "/" + owners[0].Name
	}
	vulnerabilities := securityList(report, "vulnerabilities")
	if str(report, "scanner", "name") == "" {
		*truncated = true
	}
	var total int64
	for _, key := range []string{"criticalCount", "highCount", "mediumCount", "lowCount", "unknownCount"} {
		count, _, _ := unstructured.NestedInt64(report, "summary", key)
		if count > 0 {
			if count > securityFindingLimit {
				*truncated = true
				break
			}
			total += count
		}
	}
	if total > int64(len(vulnerabilities)) {
		*truncated = true
	}
	if len(vulnerabilities) > 4096 {
		*truncated = true
		vulnerabilities = vulnerabilities[:4096]
	}
	for _, item := range vulnerabilities {
		v, ok := item.(map[string]any)
		if !ok {
			*truncated = true
			continue
		}
		id := str(v, "vulnerabilityID")
		if id == "" {
			*truncated = true
			continue
		}
		severity := strings.ToLower(str(v, "severity"))
		if securitySeverity(severity) == 4 {
			severity = "unknown"
		}
		text := func(value string) string { return securityText(value, truncated) }
		emit(core.SecurityFinding{Ref: ref, KindTitle: "VulnerabilityReport", Category: "cve", Severity: severity, Rule: text(id), Title: text(str(v, "title")), Container: text(u.GetLabels()["trivy-operator.container.name"]), Evidence: "VulnerabilityReport", Image: text(image), Package: text(str(v, "resource")), Installed: text(str(v, "installedVersion")), Fixed: text(str(v, "fixedVersion")), Scanner: text(scanner), ScannedAt: unixMs(scannedAt), Workload: text(workload)})
	}
}
