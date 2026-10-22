package kubernetes

import (
	"context"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation"
)

func (s *session) gatekeeperAvailable(d *kindDef) bool {
	snap := s.cat.snap()
	return snap.state == catalogReady && !slices.Contains(snap.unconfirmed, "*") && !slices.Contains(snap.unconfirmed, "constraints.gatekeeper.sh") && d != nil && d.discovered && !d.namespaced && d.gvr.Group == "constraints.gatekeeper.sh" && d.gvr.Version == "v1beta1" && d.kind != "" && len(d.kind) <= 253 && d.gvr.Resource == strings.ToLower(d.kind) && len(validation.IsDNS1035Label(d.gvr.Resource)) == 0 && slices.Contains(d.verbs, "get")
}

// No kind inventory, template read, subject resolution or version fallback.
func (s *session) GatekeeperAudit(parent context.Context, ref core.Ref) (core.GatekeeperAudit, error) {
	out := core.GatekeeperAudit{Ref: ref, Violations: []core.GatekeeperViolation{}}
	d := s.kind(ref.Kind)
	if ref.Provider != ProviderID || ref.Target != s.target || ref.Scope != "" || ref.UID == "" || len(ref.UID) > 256 || ref.Name == "" || len(validation.IsDNS1123Subdomain(ref.Name)) != 0 {
		return out, &provider.Error{Class: provider.ClassInvalid, Message: "constraint audit requires one cluster-scoped UID-pinned reference"}
	}
	if !s.gatekeeperAvailable(d) {
		return out, &provider.Error{Class: provider.ClassUnsupported, Message: "constraint audit requires confirmed discovered cluster-scoped v1beta1 constraint GET support"}
	}
	bounded, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	ctx, release, err := s.inventoryContext(bounded)
	if err != nil {
		return out, err
	}
	defer release()
	ctx = newScopeBudget().context(ctx)
	remaining := &atomic.Int32{}
	remaining.Store(2)
	ctx = context.WithValue(ctx, finiteRequestLimitKey{}, remaining)
	read := func() (*unstructured.Unstructured, error) {
		u, e := s.dyn.Resource(d.gvr).Get(ctx, ref.Name, metav1.GetOptions{})
		if e != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			c, _ := classify(e)
			return nil, &provider.Error{Class: c, Message: "constraint audit source could not be read"}
		}
		n, e := encodedJSONSize(u.Object)
		if e != nil || n > 1<<20 {
			return nil, &provider.Error{Class: provider.ClassScopeLimit, Message: "constraint audit source exceeds observation budget"}
		}
		if u.GetAPIVersion() != d.gvr.GroupVersion().String() || u.GetKind() != d.kind || u.GetNamespace() != "" || u.GetName() != ref.Name || u.GetResourceVersion() == "" || len(u.GetResourceVersion()) > 256 {
			return nil, &provider.Error{Class: provider.ClassUnavailable, Message: "constraint audit source identity is invalid"}
		}
		if string(u.GetUID()) != ref.UID {
			return nil, &provider.Error{Class: provider.ClassGone, Message: "constraint was replaced"}
		}
		return u, nil
	}
	u, err := read()
	if err != nil {
		return out, err
	}
	projectGatekeeper(&out, u)
	fresh, err := read()
	if err != nil {
		return core.GatekeeperAudit{}, err
	}
	if fresh.GetResourceVersion() != u.GetResourceVersion() {
		return core.GatekeeperAudit{}, &provider.Error{Class: provider.ClassConflict, Message: "constraint changed; load a new observation"}
	}
	if err := ctx.Err(); err != nil {
		return core.GatekeeperAudit{}, err
	}
	out.APIVersion = u.GetAPIVersion()
	out.ResourceVersion = u.GetResourceVersion()
	out.CapturedAt = s.now().UnixMilli()
	return out, nil
}

func projectGatekeeper(out *core.GatekeeperAudit, u *unstructured.Unstructured) {
	out.Coverage = "unknown"
	out.AuditTimeState = "missing"
	status, ok := u.Object["status"].(map[string]any)
	if !ok {
		out.Partial = true
		return
	}
	if raw, present := status["auditTimestamp"]; present {
		out.AuditTimeState = "invalid"
		if value, ok := raw.(string); ok && len(value) <= 64 {
			if t, e := time.Parse(time.RFC3339Nano, value); e == nil && t.Year() >= 1 && t.Year() <= 9999 {
				out.AuditTime = t.UTC().Format(time.RFC3339Nano)
				out.AuditTimeState = "reported"
			}
		}
	}
	if value, present, err := unstructured.NestedInt64(status, "totalViolations"); err == nil && present && value >= 0 && value <= securityPolicyMaxCount {
		out.TotalViolations = &value
	} else {
		out.Partial = true
	}
	if out.AuditTimeState != "reported" {
		out.Partial = true
	}
	raw, present := status["violations"]
	rows, ok := raw.([]any)
	if present && !ok {
		out.Partial = true
		out.Coverage = "inconsistent"
		return
	}
	out.ReturnedViolations = len(rows)
	if out.TotalViolations != nil {
		switch {
		case *out.TotalViolations < int64(len(rows)):
			out.Coverage = "inconsistent"
			out.Partial = true
		case *out.TotalViolations > int64(len(rows)):
			out.Coverage = "producer-capped"
			out.Partial = true
		default:
			out.Coverage = "reported"
		}
	}
	if len(rows) > 128 {
		rows = rows[:128]
		out.Partial = true
	}
	text := func(m map[string]any, key string, required bool) string {
		raw, exists := m[key]
		v, ok := raw.(string)
		if (!exists && required) || (exists && !ok) || (required && v == "") {
			out.Partial = true
		}
		return securityText(v, &out.Partial)
	}
	for _, raw := range rows {
		m, ok := raw.(map[string]any)
		if !ok {
			out.Partial = true
			continue
		}
		v := core.GatekeeperViolation{Group: text(m, "group", false), Version: text(m, "version", true), Kind: text(m, "kind", true), Namespace: text(m, "namespace", false), Name: text(m, "name", true), Message: text(m, "message", true), EnforcementAction: text(m, "enforcementAction", true), EnforcementActions: []string{}}
		if raw, exists := m["enforcementActions"]; exists {
			actions, ok := raw.([]any)
			if !ok {
				out.Partial = true
			} else {
				if len(actions) > 16 {
					actions = actions[:16]
					out.Partial = true
				}
				for _, raw := range actions {
					if action, ok := raw.(string); ok {
						v.EnforcementActions = append(v.EnforcementActions, securityText(action, &out.Partial))
					} else {
						out.Partial = true
					}
				}
			}
		}
		out.Violations = append(out.Violations, v)
	}
}
