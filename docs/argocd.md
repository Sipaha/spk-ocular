# Argo CD

Argo CD is a free standalone navigation section beside **Helm**, offered when
the connected Kubernetes API catalog contains `argoproj.io/applications`. It uses
the current explicit connection and namespace selection. No Argo CLI, paid
account or separate credential store is required. Selecting a target or opening
navigation never connects it. **Ask AI is outside the selected feature.**

## Navigation and dashboard

The section has independent **Dashboard**, **Applications**, **Application Sets**
and **Projects** pages, plus **Settings → Repositories / Clusters**. They share
the main namespace selector; resource types are not tabs inside a Cluster page.
The original `ocular.argocd` page ID now identifies Dashboard, preserving saved
navigation/favorites. The section and its Settings subgroup use ordinary global
navigation folding and favorites; scope remains per target.

Dashboard summarizes observed Application sync and health independently, with
counts for synced, healthy, out of sync, degraded, missing, progressing and
unknown. Resource cards navigate to their own pages. The Application card offers
bulk Sync/Refresh selection: Sync initially selects out-of-sync Applications and
Refresh initially selects all returned Applications. The operator can change the
selection and choose pruning/refresh type before opening the shared bulk review.
Each object retains its own review, result and conflict/unknown outcome.

**Needs attention** lists problem or unknown Applications and their reason, with
health problems prioritized. Its links open the typed Application page. **Activity**
shows associated Kubernetes Events, newest first, with free-text and category
filters. Failure/progress/success/information categories summarize Event type and
reason; they are not independent verification of a deployment. Events are neither
an Argo audit history nor a durable incident recording.

## Typed controller observations

Applications show name, namespace, sync, health, project, destination, compared
target revision and age. Application Sets show generators and age. Projects show
description, source-repository count and age. Select an object for source metadata,
requested/compared revisions, conditions, operation messages and reported
resources. Source-repository links open explicitly; known GitHub Git commit
revisions also have a commit-tree link. Unknown hosts, moving branches, chart
versions and truncated/credential-bearing URLs do not become guessed commit links.

Missing health stays unknown. `status.sync.revision(s)` describes the compared
**target**, which is not necessarily the deployed revision. Ocular does not fetch
Git or calculate a Git-to-cluster diff. Counts include only returned objects.
Expand **Read coverage** to distinguish API absence, missing namespaces, permission
failures and partial reads. A failed refresh retains the earlier snapshot with an
error. Snapshots are finite reads, not live watches; refresh to observe subsequent
controller activity. Cancel and retry use the shared workspace controls.

The controller projection contains repository/path/chart/revision metadata, not
inline Helm values, plugin environment or generator credentials. It does not read
repository/cluster Secrets. Repository URL userinfo, query and fragment are removed
before truncation. Controller messages remain untrusted data and can contain
administrator-supplied text; this is not universal secret redaction.

## Registered repositories and clusters

Repositories and Clusters are separate Settings pages even though Argo represents
the records as labelled Secrets rather than CRDs. Opening either page performs no
settings read. **Load registered settings** explicitly requests that page's kind
in the selected namespaces: Repositories selects
`argocd.argoproj.io/secret-type=repository`; Clusters selects `...=cluster`.
Credential templates (`repo-creds`) and unrelated Secrets are not included.
Changing scope, connection or settings page requires an explicit new load.

Kubernetes returns whole Secret records for these list calls, so list rights to
Secrets are required. The provider projects only connection descriptors:
repository name/type/URL/project, cluster name/server, namespace, record identity
and creation time. It never decodes credential fields, private keys, certificates
or cluster `config`. Only these descriptor fields cross the UI API boundary.
Malformed, invalid UTF-8 or overlarge fields are omitted/truncated visibly;
repository/server URL userinfo, query and fragment are removed. Secret refusal
messages are reduced to coverage classes, never passed through with raw values.

Selecting a descriptor shows only that projection. It does not open generic
Secret properties, YAML, values or an editor. Settings do not register, edit or
connect to repositories/clusters, save credentials, test remote connectivity or
claim connection health. Descriptor snapshots remain only in the active page;
they are excluded from the shared controller-evidence cache and from persistence.

## Reviewed operations

Select an Application or mark several to use **Sync**, **Sync with prune**,
**Refresh** or **Hard refresh** through the standard reviews. Bulk execution has
independent outcomes and can stop starting new work. All four actions are UI-only;
agent grants cannot authorize controller reconciliation.

The review shows the actual connection, UID, namespace, destination, pinned
revision(s), pruning, sync options and controller/hook consequences. Authorization
checks Kubernetes get/patch rights, **not Argo server user RBAC**. Operators must
account for the Argo controller's authority at its destination. Normal refresh
requests another comparison; hard refresh also invalidates the manifest/target
cache. Acceptance does not mean reconciliation or deployment succeeded.

Sync requires resolved Git commits/chart versions and a comparison matching current
source(s), destination and ignore-differences configuration. Busy, deleting,
skipped, pending-refresh, unresolved or stale applications are refused. Known
omitted typed defaults are canonicalized; opaque Helm/plugin values and unknown
fields remain significant. Review binds original spec/operation state, UID,
resource version, route and configuration revision. Run performs one guarded write;
conflict or an ambiguous result is never automatically retried.

## Bidirectional resource investigation

Argo's reported resources carry no UID. Clicking an available resource resolves
its current identity and pins its returned UID before opening the normal drawer.
Links require a proven local destination, an unambiguous discovered group/kind and
an allowed namespace. Name-only destinations such as `in-cluster` do not prove
locality; remote destinations are not connected or followed. An empty namespace
selection never broadens reads. Event links require current full object identity,
including UID; same-name replacements are not the event's original object.

Properties expose **Argo CD tracking** for current tracking metadata. A
self-referencing `argocd.argoproj.io/tracking-id` must match the actual group/kind,
name and namespace; copied non-self-referencing annotations are rejected.
Argo's namespace-qualified `<namespace>_<Application>` instance names are supported.
A generic `app.kubernetes.io/instance` label alone never becomes ownership.
**Managed by Argo CD** links require matching Application identity, a proven local
destination and that Application's reported resource membership. The current
resource UID and matching Application UID are pinned. Ambiguous matches remain
unresolved; nonempty installation IDs remain unresolved when the controlling
instance identity cannot be independently verified.

Already loaded controller evidence can supply the reverse link without another
inventory request, for exactly the same connection incarnation and namespace
selection. This memory-only cache holds at most eight projections per Client;
Settings never enter it. Otherwise **Find managing Application** requests a
bounded snapshot explicitly within the current selection. Properties never widen
namespaces or connect another cluster. The link opens the typed Application page.
Held YAML/value edits survive refresh and automatic connection replacement.
Navigation checks edit guards again when an asynchronous resource answer arrives,
because a new draft may have appeared while its UID lookup was pending.

## Open the Argo UI

Dashboard's **Open Argo CD UI** uses Services labelled exactly
`app.kubernetes.io/name=argocd-server` within the selected namespaces. A single
candidate with complete coverage can be used directly; multiple/partial candidates
require explicit Service selection. It chooses a supported HTTPS port443, or an
unambiguous HTTP(S) port. Nonstandard ambiguous ports retain the ordinary port
controls and Forward dialog.

An exact Service UID, remote port and current configuration revision allow reuse
of an existing ready/idle forward. Otherwise one loopback forward is started on a
free local port, then its URL is opened explicitly. No failed/ambiguous start is
retried automatically. A late port-resolution answer cannot start a forward or
open a browser after its context has been abandoned. Tunnels retain their normal
explicit lifetime controls. Ocular does not log in or bypass TLS; the Argo server's
authentication and certificate requirements still apply. Missing Service access
is coverage, not empty Application success.

## Bounds and verification

Controller and explicit settings reads bound namespaces128, objects2000, total
input8MiB, each object1MiB, pagination2048pages overall/256per source/50objects per
page and per-source reads15seconds, within the75-second service deadline. Nested
projection limits and truncation remain visible. Explicit namespaces use
per-namespace reads.

Kubernetes fake clients and owned HTTP tests cover projection/privacy, selectors,
scope, coverage, pagination, tracking identity, revision pinning and single-write
conflicts/failures. Headless browser fixtures cover actual independent navigation,
registered descriptors, operations, bulk selection, forward reuse, edit guards,
late answers and eight locales. These checks do not run a real Argo controller or
prove reconciliation in a user cluster. Flux, Ask AI, credential editing, Git
writeback and controller installation remain outside this task.
