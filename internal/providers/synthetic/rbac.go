package synthetic

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"time"
)

func (s *session) RBAC(ctx context.Context, scope core.ScopeSel, subject *core.Ref) (core.RBACSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return core.RBACSnapshot{}, err
	}
	out := core.RBACSnapshot{Principal: "demo-operator", Groups: []string{"team:developers", "system:authenticated"}, Accounts: []core.Ref{}, Grants: []core.RBACGrant{}, Problems: []core.GraphProblem{}, Discovery: "ready", CapturedAt: time.Now().UnixMilli()}
	for _, c := range allCrates {
		if scope.Contains(c.zone) {
			out.Accounts = append(out.Accounts, crateRef(c))
		}
	}
	if subject != nil {
		r, err := s.getCrate(*subject)
		if err != nil {
			return out, err
		}
		if subject.UID != "" && subject.UID != r.Ref.UID {
			return out, &provider.Error{Class: provider.ClassGone, Message: "ServiceAccount was replaced"}
		}
		out.Subject = &r.Ref
		out.Principal = "system:serviceaccount:" + r.Ref.Scope + ":" + r.Ref.Name
		out.Groups = []string{"system:serviceaccounts", "system:serviceaccounts:" + r.Ref.Scope, "system:authenticated"}
	}
	namespace := "blue"
	if scope.Mode == core.ScopeOne {
		namespace = scope.Name
	}
	if scope.Contains(namespace) {
		out.Grants = append(out.Grants, core.RBACGrant{Binding: core.Ref{Name: "pod-reader", Scope: namespace}, BindingKind: "RoleBinding", Role: core.Ref{Name: "pod-reader", Scope: namespace}, RoleKind: "Role", SubjectKind: "Group", SubjectName: out.Groups[0], Namespace: namespace, Rules: []core.RBACRule{{Verbs: []string{"get", "list", "watch"}, APIGroups: []string{""}, Resources: []string{"pods", "pods/log"}, ResourceNames: []string{}, NonResourceURLs: []string{}}}})
	}
	out.Grants = append(out.Grants, core.RBACGrant{Binding: core.Ref{Name: "named-config"}, BindingKind: "ClusterRoleBinding", Role: core.Ref{Name: "named-config"}, RoleKind: "ClusterRole", SubjectKind: "Group", SubjectName: "system:authenticated", ClusterWide: true, Rules: []core.RBACRule{{Verbs: []string{"get"}, APIGroups: []string{""}, Resources: []string{"configmaps"}, ResourceNames: []string{"settings"}, NonResourceURLs: []string{}}, {Verbs: []string{"get"}, APIGroups: []string{}, Resources: []string{}, ResourceNames: []string{}, NonResourceURLs: []string{"/healthz", "/version"}}}})
	return out, nil
}
func (s *session) CheckAccess(ctx context.Context, a core.AccessAttributes) (core.AccessReview, error) {
	if err := ctx.Err(); err != nil {
		return core.AccessReview{}, err
	}
	state, reason := "denied", "No configured authorizer allowed this request"
	if a.Verb == "get" && a.Resource == "pods" && a.Namespace == "blue" {
		state, reason = "allowed", "RBAC: allowed by RoleBinding pod-reader"
	}
	return core.AccessReview{Attributes: a, State: state, Reason: reason, CheckedAt: time.Now().UnixMilli()}, nil
}
func (s *session) ResolveAccess(ref core.Ref, verb, subresource string) (core.AccessAttributes, error) {
	return core.AccessAttributes{Verb: verb, Resource: ref.Kind, Subresource: subresource, Namespace: ref.Scope, Name: ref.Name}, nil
}
