# Argo CD

Argo CD is a free native workspace under **Cluster**, offered when the connected
Kubernetes API catalog contains `argoproj.io/applications`. It uses the current
explicit connection and namespace selection. No Argo CLI, paid account or separate
Argo credential store is required. Select a connection and **Connect** first;
selecting a target or opening navigation never connects it.

## Controller observations

**Applications**, **ApplicationSets** and **Projects** show bounded controller
observations. Select an object to inspect source repository/path/chart metadata,
requested and compared target revisions, destination, conditions, operation
messages and reported resources. **Activity** shows associated Kubernetes Events;
it is neither an Argo audit history nor a durable incident recording.

Sync and health are separate axes. Missing health stays unknown. “Needs attention”
includes unknown sync/health, nonhealthy or out-of-sync applications, conditions
and running/failed operations. ApplicationSet and AppProject do not inherit an
invented application health. `status.sync.revision(s)` describes the compared
**target**, which is not necessarily the deployed revision. Ocular does not fetch
Git or calculate a Git-to-cluster diff.

Counts include only returned objects. Expand **Read coverage** to distinguish API
absence, missing namespaces, permission failures and partial reads. Discovery
may still be in progress. A failed refresh retains the previous snapshot with an
error; refresh/cancel use the normal workspace loading controls. Snapshots are
finite reads, not live watches; refresh after controller activity to observe its
result. No application/source content is saved in page preferences.

Only source metadata is projected: repository, path, chart and revision. Inline
Helm values, plugin environment and generator credentials are excluded, and the
workspace never reads repository or cluster Secrets. Repository URL userinfo,
query and fragment are removed. Controller messages remain untrusted data and
can contain administrator-supplied text; this is not universal secret redaction.

## Reviewed operations

Select an Application to use **Sync**, **Sync with prune**, **Refresh** or **Hard
refresh**. Mark multiple Applications to use the shared bulk review with separate
outcomes and the ability to stop starting new work. These actions are UI-only;
agent grants cannot authorize them.

The shared review shows the actual connection, object, namespace, destination,
pinned revision(s), pruning, sync options and controller/hook consequences.
Authorization checks Kubernetes get/patch rights, **not Argo server user RBAC**.
Operators must account for the authority of the Argo controller at its destination.
Normal refresh asks the controller to compare again; hard refresh also invalidates
its manifest/target cache.

Sync requires resolved Git commits or chart versions and a comparison matching
the current source(s), destination and ignore-differences configuration. Busy,
deleting, skipped, pending-refresh, unresolved or stale applications are refused.
Known typed omitted defaults are compared canonically; opaque Helm/plugin values
and unknown fields still matter. Review binds the original spec and operation
state, UID, resource version, connection route and configuration revision. Run
performs one guarded write. Conflict or an ambiguous transport/server result is
never automatically retried; inspect current state before a new explicit review.
Request acceptance does not mean that reconciliation or deployment succeeded.

## Related resources and Argo UI

Argo's reported resources carry no UID. Clicking an available resource resolves
its current identity and pins its returned UID before opening the normal drawer.
Links require a proven local destination, a discovered group/kind and an allowed
namespace. Name-only destinations such as `in-cluster` do not prove locality;
remote destinations are not connected or followed. Empty namespace selection does
not broaden reads. Historical event links require the current full object
identity, including UID; same-name replacements are not treated as that event's
original object. The normal drawer's edit guard preserves unsaved work.

**Argo CD UI service** lists Services labeled exactly
`app.kubernetes.io/name=argocd-server` in the selected namespaces. Choose the
intended Service explicitly, inspect its ports, and use the existing **Forward**
dialog. Open the loopback URL from **Tunnels**. Ocular does not select an arbitrary
instance, start a forward automatically, log in or bypass TLS. The Argo server's
authentication and certificate requirements still apply. Missing Service access
does not turn successful Application reads into empty results.

## Bounds and verification

The provider bounds namespaces (128), objects (2000), total input (8 MiB), each
object (1 MiB), pagination (2048 pages overall, 256 per source, 50 objects per page)
and per-source reads (15 seconds), within a 75-second service deadline. Nested
projection limits and collection truncation remain visible. Explicit namespaces
use per-namespace reads, including Events and UI Services.

Fake Kubernetes client and owned HTTP tests cover projection, privacy, scope,
coverage, pagination, revision pinning, state changes and single-write conflicts
or failures. Synthetic browser fixtures exercise the workspace, shared operation
reviews, partial bulk results, UID resolution, draft preservation, late answers,
multiple Services and all eight locales. These checks do not run an Argo
controller or establish successful reconciliation in a real cluster. Flux,
credential-backed Argo settings, Git writeback and controller installation are
outside this workspace.
