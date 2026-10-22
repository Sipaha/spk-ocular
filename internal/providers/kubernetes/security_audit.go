package kubernetes

import (
	"strings"

	"github.com/spk/spk-ocular/internal/core"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func (s securitySource) audit() bool {
	return s.kind == "ConfigAuditReport" || s.kind == "RbacAssessmentReport" || s.kind == "ClusterConfigAuditReport" || s.kind == "ClusterRbacAssessmentReport"
}

func (s securitySource) policy() bool {
	return s.kind == "PolicyReport" || s.kind == "Report" || s.kind == "ClusterPolicyReport" || s.kind == "ClusterReport"
}
func (s securitySource) external() bool { return s.audit() || s.policy() }

type securityAuditCoverage struct {
	ok, incomplete bool
	reports        int
	errors         map[string]bool
}

func newSecurityAuditCoverage() *securityAuditCoverage {
	return &securityAuditCoverage{errors: map[string]bool{}}
}
func (c *securityAuditCoverage) readError(err error, missing bool) {
	state := "error"
	if missing {
		state = "missing"
	}
	if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
		state = "denied"
	}
	c.errors[state] = true
}
func (c *securityAuditCoverage) finish(source securitySource, out *core.SecuritySnapshot) {
	if !source.external() {
		return
	}
	state := "empty"
	if c.reports > 0 {
		state = "available"
	}
	if c.incomplete {
		state = "partial"
	}
	if len(c.errors) > 0 && !c.ok {
		state = "error"
		if c.errors["denied"] {
			state = "denied"
		} else if len(c.errors) == 1 && c.errors["missing"] {
			state = "missing"
		}
	}
	for i := range out.AuditSources {
		if out.AuditSources[i].Kind == source.kind {
			out.AuditSources[i].State = state
			out.AuditSources[i].Reports = c.reports
		}
	}
}

// Only this typed allowlist leaves provider memory. Text is untrusted scanner
// evidence, rendered literally; it is not a universal secret-redaction boundary.
func (s *session) securityAuditReport(ref core.Ref, source securitySource, u *unstructured.Unstructured, emit func(core.SecurityFinding), truncated *bool) bool {
	complete := true
	text := func(v string) string {
		local := false
		value := securityText(v, &local)
		if local {
			complete = false
			*truncated = true
		}
		return value
	}
	if u.GetAPIVersion() != source.gvr.GroupVersion().String() || u.GetKind() != source.kind || u.GetResourceVersion() == "" || len(u.GetResourceVersion()) > 256 || u.GetName() == "" || len(u.GetName()) > 253 || u.GetUID() == "" || len(u.GetUID()) > 256 || (!source.global && (u.GetNamespace() == "" || len(u.GetNamespace()) > 253)) || (source.global && u.GetNamespace() != "") {
		return false
	}
	report := securityMap(u.Object, "report")
	checks, found, err := unstructured.NestedSlice(report, "checks")
	if !found || err != nil {
		complete = false
	}
	if len(checks) > 4096 {
		checks = checks[:4096]
		complete = false
		*truncated = true
	}
	scannerName := text(str(report, "scanner", "name"))
	scannerVersion := text(str(report, "scanner", "version"))
	vendor := text(str(report, "scanner", "vendor"))
	workload, ownerUID := "", ""
	if owner := metav1.GetControllerOfNoCopy(u); owner != nil {
		workload = text(owner.Kind + "/" + owner.Name)
		ownerUID = text(string(owner.UID))
	} else if owners := u.GetOwnerReferences(); len(owners) > 0 {
		workload = text(owners[0].Kind + "/" + owners[0].Name)
		ownerUID = text(string(owners[0].UID))
	}
	failed := map[string]int64{}
	for _, item := range checks {
		check, ok := item.(map[string]any)
		if !ok {
			complete = false
			continue
		}
		id := str(check, "checkID")
		if id == "" {
			complete = false
			continue
		}
		nativeSeverity := text(str(check, "severity"))
		severity := strings.ToLower(nativeSeverity)
		if severity != "critical" && severity != "high" && severity != "medium" && severity != "low" {
			severity = "unknown"
		}
		result := "unknown"
		if value, present := securityBool(check, "success"); present {
			if value {
				result = "pass"
			} else {
				result = "fail"
				failed[severity]++
			}
		} else {
			complete = false
		}
		messages := []string{}
		if raw, present, e := unstructured.NestedSlice(check, "messages"); e != nil {
			complete = false
		} else if present {
			if len(raw) > 16 {
				raw = raw[:16]
				complete = false
				*truncated = true
			}
			for _, v := range raw {
				if value, ok := v.(string); ok {
					messages = append(messages, text(value))
				} else {
					complete = false
				}
			}
		}
		scopeType, scopeValue := text(str(check, "scope", "type")), text(str(check, "scope", "value"))
		container := ""
		if scopeType == "Container" {
			container = scopeValue
		}
		detail := &core.SecurityAuditCheck{Result: result, NativeSeverity: nativeSeverity, Category: text(str(check, "category")), Description: text(str(check, "description")), Messages: messages, Remediation: text(str(check, "remediation")), ScopeType: scopeType, ScopeValue: scopeValue, ScannerName: scannerName, ScannerVersion: scannerVersion, ScannerVendor: vendor, APIVersion: source.gvr.GroupVersion().String(), ResourceVersion: text(u.GetResourceVersion()), OwnerUID: ownerUID}
		emit(core.SecurityFinding{Ref: ref, KindTitle: source.kind, Category: "audit", Severity: severity, Rule: text(id), Container: container, Evidence: source.kind, Title: text(str(check, "title")), Scanner: strings.TrimSpace(scannerName + " " + scannerVersion), ScannedAt: unixMs(timeAt(report, "updateTimestamp")), Workload: workload, Audit: detail})
	}
	// Summary describes failed checks, not all checks. Missing summary is unknown;
	// inconsistent/malformed declared counts disclose incomplete evidence.
	if summary, present, e := unstructured.NestedMap(report, "summary"); e != nil {
		complete = false
	} else if present {
		for _, level := range []string{"critical", "high", "medium", "low"} {
			if n, present, e := unstructured.NestedInt64(summary, level+"Count"); e != nil || (present && (n < 0 || n != failed[level])) {
				complete = false
			}
		}
	}
	return complete
}
