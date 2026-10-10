# RBAC explanation

On an explicitly connected Kubernetes target, open **Access Control → RBAC
explanation**. Inspect the current connection identity or select a ServiceAccount
from the selected namespaces. A named-account form also supports accounts that
can be read individually when listing is unavailable. The account must belong to
the selected namespace set. **Refresh** obtains a new bounded snapshot.

ServiceAccount properties offer **Permissions** without leaving the current
editor. A Forbidden response in Kubernetes resource properties offers **Check this
Forbidden**; its resource-specific read check resolves the resource's real API
group, plural resource and namespace through the provider catalog.

## Declaration provenance

The page shows matching User, Group and ServiceAccount subjects, their
RoleBindings/ClusterRoleBindings and the referenced role rules. Source links open
the existing properties with exact UID identities and normal edit guards. A
ServiceAccount's authenticated name and standard groups are used to resolve
bindings; token Secrets are not read. The current connection identity is obtained
through SelfSubjectReview rather than inferred from kubeconfig entry names.

RoleBindings contribute only within their own namespace, including when they
reference ClusterRoles. ClusterRoleBindings are read once globally. Only the
selected namespaces are searched for RoleBindings. Missing roles, refused sources,
incomplete discovery and limits remain visible. Missing declarations never imply
that the subject has no permissions.

The declaration matcher accounts for exact names, API groups, verbs, resources
and subresources, including Kubernetes' supported wildcard forms. Named
list/watch needs a matching `metadata.name` selector in the real request.
Top-level create/deletecollection cannot be limited by resource name. Non-resource
URL rules are displayed separately and cannot match a resource request; they do
not confer access through a namespaced RoleBinding. Aggregated ClusterRoles show
the server's currently resolved rules; Ocular does not recompute aggregation.

**Find declared grant** explains matches in the loaded snapshot. It is not a full
authorization decision. RBAC is additive, other authorizers may participate,
identity or policy may change, and a snapshot can be incomplete or non-atomic.

## Server access checks

**Check my access with the server** submits a SelfSubjectAccessReview for the
current configured connection. It never impersonates the selected ServiceAccount,
adds a grant or performs the operation selected in the form. The request's verb,
API group, resource, subresource, namespace and optional name are shown with the
result and timestamp. An empty namespace requests all namespaces/cluster scope.

These review APIs use POST but do not persist cluster objects. Server-provided
reasons and evaluation errors are presented as untrusted text. A failed or
malformed review is an error/unknown result, not a denied verdict. An allowed
result accompanied by an evaluation error still shows that error.

A current authorization result is not proof that an object exists, its UID is
unchanged, admission would accept a write, or an earlier Forbidden had the same
cause. Editing fields or changing subject cancels/invalidates an obsolete check;
a late response cannot produce a verdict for the new request. Opening permission
inspection, filtering, refreshing and changing language preserve resource drafts.
Source navigation closes the dialog before the standard discard question.

## Boundaries and implementation

`RBACSnapshot` and `CheckAccess` are UI-only APIs implemented by optional provider
`RBACSource`. They require an admitted connection and do not extend agent grants
or change Roles, RoleBindings, users or ServiceAccounts. Connection credentials and
identity extras do not enter DTOs, SQLite or journals. Snapshot and review state is
kept only in the current UI opening. Existing kubeconfig impersonation settings,
if configured by the user, remain part of the current connection identity.

ServiceAccount inventory and identity pinning use PartialObjectMetadata. Binding
lists are paginated at 100 items, with 6,000 inventory accounts, 6,000 examined
bindings, 2,000 matching bindings and 128 selected namespace reads. Referenced
roles are fetched once per snapshot, with ten-second read deadlines. Rules are
capped at 12,000 entries, 100,000 field values and 8 MiB of rule text; oversized
rules are omitted with explicit incomplete coverage. The request deadline is 75
seconds and session shutdown/cancellation stops active reads. Review requests have
a 15-second provider deadline. Graph, Timeline and RBAC inventory snapshots share
a per-session gate. The frontend virtualizes individual rule rows, including
roles containing many rules.

All eight UI languages include the workflow and its own error messages. This
feature is implemented in development builds and is not in published v1.1.2.

Primary semantics: [Kubernetes RBAC](https://kubernetes.io/docs/reference/access-authn-authz/rbac/),
[authorization reviews](https://kubernetes.io/docs/reference/access-authn-authz/authorization/)
and [identity reviews](https://kubernetes.io/docs/reference/access-authn-authz/authentication/#http-access-to-authentication-information).
