package kubernetes

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var serviceAccountGVR = schema.GroupVersionResource{Version: "v1", Resource: "serviceaccounts"}
var roleBindingGVR = schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings"}
var clusterRoleBindingGVR = schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"}
var selfSubjectGVR = schema.GroupVersionResource{Group: "authentication.k8s.io", Version: "v1", Resource: "selfsubjectreviews"}
var selfAccessGVR = schema.GroupVersionResource{Group: "authorization.k8s.io", Version: "v1", Resource: "selfsubjectaccessreviews"}

const rbacInventoryLimit = 6000
const rbacGrantLimit = 2000
const rbacRuleLimit = 12000
const rbacValueLimit = 100000
const rbacTextLimit = 8 << 20

func (s *session) rbacRef(group, kind, ns, name, uid string) core.Ref {
	version := "v1"
	if group != "" {
		version = group + "/v1"
	}
	return core.Ref{Provider: ProviderID, Target: s.target, Kind: s.kindFor(version, kind), Scope: ns, Name: name, UID: uid}
}

// RBAC reads declarations only. SelfSubjectReview identifies the connection;
// it never guesses identity from a kubeconfig alias or impersonates a subject.
func (s *session) RBAC(parent context.Context, scope core.ScopeSel, subject *core.Ref) (core.RBACSnapshot, error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	select {
	case s.graphGate <- struct{}{}:
		defer func() { <-s.graphGate }()
	case <-ctx.Done():
		return core.RBACSnapshot{}, ctx.Err()
	}
	snap := s.cat.snap()
	out := core.RBACSnapshot{Groups: []string{}, Accounts: []core.Ref{}, Grants: []core.RBACGrant{}, Problems: []core.GraphProblem{}, Discovery: snap.state}
	problem := func(kind, ns string, err error) {
		class, _ := classify(err)
		out.Problems = append(out.Problems, core.GraphProblem{Kind: kind, Scope: ns, Class: string(class)})
	}
	if subject != nil {
		// Metadata only: never request the ServiceAccount's token Secret references.
		var uid string
		if s.graphMetadata != nil {
			u, err := s.graphMetadata.Resource(serviceAccountGVR).Namespace(subject.Scope).Get(ctx, subject.Name, metav1.GetOptions{})
			if err != nil {
				return out, rbacSafeError(err)
			}
			uid = string(u.GetUID())
		} else {
			u, err := s.dyn.Resource(serviceAccountGVR).Namespace(subject.Scope).Get(ctx, subject.Name, metav1.GetOptions{})
			if err != nil {
				return out, rbacSafeError(err)
			}
			uid = string(u.GetUID())
		}
		if subject.UID != "" && subject.UID != uid {
			return out, provider.Said(provider.ClassGone, msg("rbac.replaced"))
		}
		ref := s.rbacRef("", "ServiceAccount", subject.Scope, subject.Name, uid)
		out.Subject = &ref
		out.Principal = "system:serviceaccount:" + subject.Scope + ":" + subject.Name
		out.Groups = []string{"system:serviceaccounts", "system:serviceaccounts:" + subject.Scope, "system:authenticated"}
	} else {
		review, err := s.dyn.Resource(selfSubjectGVR).Create(ctx, &unstructured.Unstructured{Object: map[string]any{"apiVersion": "authentication.k8s.io/v1", "kind": "SelfSubjectReview"}}, metav1.CreateOptions{})
		if err != nil {
			problem("selfsubjectreviews", "", err)
		} else {
			out.Principal = str(review.Object, "status", "userInfo", "username")
			out.Groups, _, _ = unstructured.NestedStringSlice(review.Object, "status", "userInfo", "groups")
			if len(out.Principal) > 1024 {
				out.Principal = ""
				out.Truncated = true
			}
			if out.Groups == nil {
				out.Groups = []string{}
			}
			validGroups := []string{}
			for _, group := range out.Groups {
				if len(group) > 1024 {
					out.Truncated = true
					continue
				}
				validGroups = append(validGroups, group)
			}
			out.Groups = validGroups
			if len(out.Groups) > 256 {
				out.Groups = out.Groups[:256]
				out.Truncated = true
			}
		}
	}
	namespaces := []string{""}
	if scope.Mode != core.ScopeAll {
		namespaces = scope.SelectedNames()
	}
	if len(namespaces) > 128 {
		namespaces = namespaces[:128]
		out.Truncated = true
	}
	// Account inventory is metadata-only and bounded, independent of whether the
	// current principal could be identified. Unknown identity can still use SSAR.
	for _, ns := range namespaces {
		token := ""
		seen := map[string]bool{}
		for {
			if len(out.Accounts) >= rbacInventoryLimit {
				out.Truncated = true
				break
			}
			var next string
			var err error
			if s.graphMetadata != nil {
				var list *metav1.PartialObjectMetadataList
				list, err = s.graphMetadata.Resource(serviceAccountGVR).Namespace(ns).List(ctx, metav1.ListOptions{Limit: 100, Continue: token})
				if err == nil {
					next = list.GetContinue()
					for _, u := range list.Items {
						if !scope.Contains(u.GetNamespace()) || u.GetUID() == "" {
							continue
						}
						if len(out.Accounts) >= rbacInventoryLimit {
							out.Truncated = true
							break
						}
						out.Accounts = append(out.Accounts, s.rbacRef("", "ServiceAccount", u.GetNamespace(), u.GetName(), string(u.GetUID())))
					}
				}
			} else {
				var list *unstructured.UnstructuredList
				list, err = s.dyn.Resource(serviceAccountGVR).Namespace(ns).List(ctx, metav1.ListOptions{Limit: 100, Continue: token})
				if err == nil {
					next = list.GetContinue()
					for _, u := range list.Items {
						if !scope.Contains(u.GetNamespace()) || u.GetUID() == "" {
							continue
						}
						if len(out.Accounts) >= rbacInventoryLimit {
							out.Truncated = true
							break
						}
						out.Accounts = append(out.Accounts, s.rbacRef("", "ServiceAccount", u.GetNamespace(), u.GetName(), string(u.GetUID())))
					}
				}
			}
			if err != nil {
				problem("serviceaccounts", ns, err)
				break
			}
			if next == "" {
				break
			}
			if seen[next] {
				out.Truncated = true
				break
			}
			seen[next] = true
			token = next
		}
	}
	type roleResult struct {
		object *unstructured.Unstructured
		class  string
	}
	roles := map[string]roleResult{}
	rules := 0
	valuesUsed, textUsed := 0, 0
	bindings := 0
	readBindings := func(gvr schema.GroupVersionResource, ns string, cluster bool) {
		token := ""
		seen := map[string]bool{}
		for {
			if bindings >= rbacInventoryLimit || len(out.Grants) >= rbacGrantLimit {
				out.Truncated = true
				return
			}
			list, err := s.dyn.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{Limit: 100, Continue: token})
			if err != nil {
				problem(gvr.Resource, ns, err)
				return
			}
			for _, binding := range list.Items {
				if !cluster && !scope.Contains(binding.GetNamespace()) {
					continue
				}
				if bindings >= rbacInventoryLimit || len(out.Grants) >= rbacGrantLimit {
					out.Truncated = true
					return
				}
				bindings++
				kind, name, matched := rbacBindingMatch(&binding, out.Principal, out.Groups)
				if !matched {
					continue
				}
				grant := core.RBACGrant{Binding: s.rbacRef(gvr.Group, "RoleBinding", binding.GetNamespace(), binding.GetName(), string(binding.GetUID())), BindingKind: "RoleBinding", SubjectKind: kind, SubjectName: name, Namespace: binding.GetNamespace(), ClusterWide: cluster, Rules: []core.RBACRule{}}
				if cluster {
					grant.BindingKind = "ClusterRoleBinding"
					grant.Binding = s.rbacRef(gvr.Group, grant.BindingKind, "", binding.GetName(), string(binding.GetUID()))
				}
				roleKind, roleName := str(binding.Object, "roleRef", "kind"), str(binding.Object, "roleRef", "name")
				roleNS := binding.GetNamespace()
				roleResource := "roles"
				if roleKind == "ClusterRole" {
					roleNS = ""
					roleResource = "clusterroles"
				}
				grant.RoleKind = roleKind
				grant.Role = s.rbacRef(gvr.Group, roleKind, roleNS, roleName, "")
				if str(binding.Object, "roleRef", "apiGroup") != gvr.Group || roleName == "" || (roleKind != "Role" && roleKind != "ClusterRole") || (cluster && roleKind != "ClusterRole") {
					grant.Error = "invalid_role_ref"
					out.Grants = append(out.Grants, grant)
					continue
				}
				key := roleResource + "/" + roleNS + "/" + roleName
				result, ok := roles[key]
				if !ok && (rules >= rbacRuleLimit || valuesUsed >= rbacValueLimit || textUsed >= rbacTextLimit) {
					out.Truncated = true
					grant.Error = "limit"
					out.Grants = append(out.Grants, grant)
					continue
				}
				if !ok {
					roleCtx, done := context.WithTimeout(ctx, 10*time.Second)
					object, err := s.dyn.Resource(schema.GroupVersionResource{Group: gvr.Group, Version: "v1", Resource: roleResource}).Namespace(roleNS).Get(roleCtx, roleName, metav1.GetOptions{})
					done()
					if object != nil {
						object = trim(object, fields{"rules": true, "aggregationRule": true})
					}
					result.object = object
					if err != nil {
						class, _ := classify(err)
						result.class = string(class)
					}
					roles[key] = result
				}
				if result.class != "" {
					grant.Error = result.class
				} else {
					grant.Role.UID = string(result.object.GetUID())
					_, grant.Aggregated, _ = unstructured.NestedMap(result.object.Object, "aggregationRule")
					for _, rule := range slice(result.object.Object, "rules") {
						if rules >= rbacRuleLimit {
							out.Truncated = true
							grant.Error = "limit"
							break
						}
						rules++
						decoded := core.RBACRule{Verbs: rbacStrings(rule, "verbs"), APIGroups: rbacStrings(rule, "apiGroups"), Resources: rbacStrings(rule, "resources"), ResourceNames: rbacStrings(rule, "resourceNames"), NonResourceURLs: rbacStrings(rule, "nonResourceURLs")}
						// Bound each declaration without silently turning a restricted rule into
						// a wider one. Oversized rules are omitted and coverage is incomplete.
						tooLarge := false
						valueCount, textCount := 0, 0
						for _, values := range [][]string{decoded.Verbs, decoded.APIGroups, decoded.Resources, decoded.ResourceNames, decoded.NonResourceURLs} {
							valueCount += len(values)
							if len(values) > 512 {
								tooLarge = true
							}
							for _, value := range values {
								textCount += len(value)
								if len(value) > 2048 {
									tooLarge = true
								}
							}
						}
						if valuesUsed+valueCount > rbacValueLimit || textUsed+textCount > rbacTextLimit {
							out.Truncated = true
							grant.Error = "limit"
							break
						}
						if tooLarge {
							out.Truncated = true
							grant.Error = "limit"
							continue
						}
						valuesUsed += valueCount
						textUsed += textCount
						grant.Rules = append(grant.Rules, decoded)
					}
				}
				out.Grants = append(out.Grants, grant)
			}
			next := list.GetContinue()
			if next == "" {
				return
			}
			if seen[next] {
				out.Truncated = true
				return
			}
			seen[next] = true
			token = next
		}
	}
	if out.Principal != "" {
		readBindings(clusterRoleBindingGVR, "", true)
		for _, ns := range namespaces {
			readBindings(roleBindingGVR, ns, false)
		}
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	sort.Slice(out.Accounts, func(i, j int) bool {
		a, b := out.Accounts[i], out.Accounts[j]
		return a.Scope+"/"+a.Name < b.Scope+"/"+b.Name
	})
	sort.Slice(out.Grants, func(i, j int) bool {
		a, b := out.Grants[i], out.Grants[j]
		return a.Namespace+"/"+a.BindingKind+"/"+a.Binding.Name < b.Namespace+"/"+b.BindingKind+"/"+b.Binding.Name
	})
	out.CapturedAt = s.now().UnixMilli()
	return out, nil
}
func rbacStrings(o map[string]any, key string) []string {
	values, _, _ := unstructured.NestedStringSlice(o, key)
	if values == nil {
		return []string{}
	}
	return values
}
func rbacBindingMatch(binding *unstructured.Unstructured, principal string, groups []string) (string, string, bool) {
	if principal == "" {
		return "", "", false
	}
	for _, subject := range slice(binding.Object, "subjects") {
		kind, name, group := str(subject, "kind"), str(subject, "name"), str(subject, "apiGroup")
		switch kind {
		case "User":
			if group == "rbac.authorization.k8s.io" && name == principal {
				return kind, name, true
			}
		case "Group":
			if group == "rbac.authorization.k8s.io" {
				for _, g := range groups {
					if g == name {
						return kind, name, true
					}
				}
			}
		case "ServiceAccount":
			ns := str(subject, "namespace")
			if ns == "" {
				ns = binding.GetNamespace()
			}
			if group == "" && ns != "" && principal == "system:serviceaccount:"+ns+":"+name {
				return kind, ns + "/" + name, true
			}
		}
	}
	return "", "", false
}
func rbacSafeError(err error) error {
	class, _ := classify(err)
	return provider.Said(class, msg("rbac.source", "class", string(class)))
}

// CheckAccess asks the server for this connection's own authorization decision.
// Review objects are non-persistent; no selected ServiceAccount is impersonated.
func (s *session) CheckAccess(parent context.Context, a core.AccessAttributes) (core.AccessReview, error) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	object, err := s.dyn.Resource(selfAccessGVR).Create(ctx, &unstructured.Unstructured{Object: map[string]any{"apiVersion": "authorization.k8s.io/v1", "kind": "SelfSubjectAccessReview", "spec": map[string]any{"resourceAttributes": map[string]any{"verb": a.Verb, "group": a.Group, "resource": a.Resource, "subresource": a.Subresource, "namespace": a.Namespace, "name": a.Name}}}}, metav1.CreateOptions{})
	if err != nil {
		return core.AccessReview{}, rbacSafeError(err)
	}
	allowed, present, statusErr := unstructured.NestedBool(object.Object, "status", "allowed")
	if !present || statusErr != nil {
		return core.AccessReview{Attributes: a, State: "unknown", EvaluationError: "status_unavailable", CheckedAt: s.now().UnixMilli()}, nil
	}
	reason := str(object.Object, "status", "reason")
	evaluation := str(object.Object, "status", "evaluationError")
	if len(reason) > 4096 {
		reason = reason[:4096]
	}
	if len(evaluation) > 4096 {
		evaluation = evaluation[:4096]
	}
	state := "denied"
	if allowed {
		state = "allowed"
	} else if strings.TrimSpace(evaluation) != "" {
		state = "unknown"
	}
	return core.AccessReview{Attributes: a, State: state, Reason: reason, EvaluationError: evaluation, CheckedAt: s.now().UnixMilli()}, nil
}

func (s *session) ResolveAccess(ref core.Ref, verb, subresource string) (core.AccessAttributes, error) {
	def := s.kind(ref.Kind)
	if def == nil || def.virtual {
		return core.AccessAttributes{}, provider.Said(provider.ClassUnsupported, msg("rbac.unsupported"))
	}
	ns := ""
	if def.namespaced {
		ns = ref.Scope
	}
	return core.AccessAttributes{Verb: verb, Group: def.gvr.Group, Resource: def.gvr.Resource, Subresource: subresource, Namespace: ns, Name: ref.Name}, nil
}
