# Architecture and behavior contracts

## Application boundary

The application is Go + Wails v3 + React/Vite/TypeScript. Linux uses GTK 3 and
WebKit2GTK 4.1 through the `wails gtk3` build tags. `production` disables DevTools.
Windows uses WebView2 and macOS uses WKWebView. Desktop stream requests allow
`http://wails.localhost` on Windows and `wails://localhost` on Linux/macOS.
Credential plugins run in a separate process group on Unix and a kill-on-close
job object on Windows; timeout cleanup includes their descendants. Windows
children start without a console and are assigned to the job before they run.
The Wails Go module and `@wailsio/runtime` versions must match.

`cmd/spk-ocular` creates the shared core. Desktop uses Wails bindings; browser
mode exposes POST `/api/<Method>` and SSE `/api/events` on loopback. Both use
`internal/api.API` and the same frontend `Client`. API additions require the Go
interface, service, both transports, and TypeScript client. The UI transport's
Host, Origin, and token guards run before side effects.

The core types (`Ref`, `Row`, `KindDescriptor`, `ScopeSel`, capabilities) are
provider-independent. Kubernetes and Docker implement that boundary;
the synthetic provider exists only for testing. No frontend generic component
should infer Kubernetes behavior from a kind name.

| Area | Implementation |
| --- | --- |
| Shared service and DTOs | `internal/api`, `internal/api/transport` |
| Invalidation and delta views | `internal/events`, `internal/views`, `web/src/views` |
| Provider contracts and scope union | `internal/core`, `internal/provider` |
| Kubernetes | `internal/providers/kubernetes` |
| Docker/Compose provider and Engine client | `internal/providers/compose` |
| Logs and terminal streams | `internal/streams`, `web/src/logs`, `web/src/term` |
| Port forwarding | `internal/forwards`, `web/src/tunnels` |
| Guarded mutations | `web/src/actions`, `web/src/edit`, `web/src/values` |
| Agent permissions and API | `internal/agentgrant`, `internal/agentapi`, `web/src/agents` |
| Persistence | `internal/store`, `web/src/components/pageMemo*` |
| Window and process integration | `internal/desktop`, `internal/execshim`, `internal/paths` |

## Connections and sessions

Production Kubernetes discovery reads only explicitly imported file links and
encrypted configuration metadata (including locked rows), without network I/O. Candidate discovery
scans KUBECONFIG and ~/.kube separately from target discovery. Selected primary
files preserve first-wins order, and selected standalone files remain independent.
Target IDs remain stable when other configuration files change. App-owned targets
use a separate stored-config ID domain. Stored YAML resolves directly in memory,
never through a plaintext temporary file. Locked rows contain only stored context
names and display names; session creation rejects locked records. Connect on a
protected target opens the unlock prompt and resumes only that explicit connection. New configuration can also unlock the local vault directly,
then continue to its YAML editor without creating a session, even with no stored
records. A successful master entry unlocks every encrypted record for the process
lifetime. Reset uses a reviewed registry revision, deletes only
encrypted records and their aliases/key material, and revalidates sessions.

The version-1 configuration envelope uses Argon2id (64 MiB, 3 passes, 4 lanes,
16-byte random salt, 32-byte key) and AES-256-GCM with fresh nonces and
record-bound authenticated data. Writes use owner-only temporary files, sync,
atomic replacement and an OS file lock; stale process snapshots refuse to
replace a newer registry. Keys clear when the service closes. The UI-only API
supports status/scan/import/dismiss/setup/unlock/create/resolve/inspect/update/reveal/rename/rename-target/remove.
Only explicit inspection returns plaintext YAML; other commands return metadata.
Rename/remove requests bind to the exact entry revision; aliases do not alter
context identity or credentials. Per-target display aliases bind to both target
ID and registered source; rename-target checks its own metadata revision and
never reseals credentials or renames YAML context keys. Removal synchronously revalidates sessions.
The existing source-loader unit tests can inject unmanaged sources; production
wiring always enables the explicit registry.
The UI configuration DTOs and optional `configuration.Source` capability live in
`internal/configuration`, without importing the concrete Kubernetes adapter.
Provider decorators retain this capability explicitly; an installed Kubernetes
provider without it returns Unsupported instead of a successful empty status.
Credentials never enter target DTOs, events, logs, or SQLite. The UI-only
`Configurations` inspect command explicitly returns bounded YAML with no-store
HTTP headers; the frontend keeps it outside configuration metadata and destroys
the editor on close. Content revisions for source edits are process-keyed HMACs bound to the entry;
update verifies both metadata and the inspected bytes. Stored updates reseal the
same ID; external updates lock, check the source, and atomically replace its
resolved path without replacing a symlink. Update revalidates live sessions.
The reveal command resolves only a registered,
revision-checked external file and launches a fixed platform file manager without
a shell; encrypted records cannot be revealed as files. Configuration revisions
are process-keyed HMACs, not raw configuration hashes.

UI selection and connection are separate. UI transports use `UIContext`; resource
reads require an admitted connection and never implicitly create a session.
`ConnectTarget` checks a provisional provider session, then admits it only if its
attempt ID and configuration revision are still current. `CancelConnectTarget`
addresses that attempt ID and prevents late success from publishing a session.
Granted agent calls keep their independent, permission-checked session path.

Connection progress reports backend phases and timestamps. Availability failures
have at most three total attempts, with 500 ms and 1 s backoff and a 90-second
budget per attempt. Authentication/configuration failures stop immediately.
A forbidden Kubernetes namespace probe permits connection with typed scopes.
Cancellation interrupts the check and backoff; removing the private session
lifetime marker terminates credential helpers and rejects late helper starts.

Explicit UI connections remain open until Disconnect or application shutdown,
including in Helm, while the window is minimized, and after switching targets.
They do not depend on frontend heartbeat timers or resource-view leases.
Configuration changes retire the old incarnation and automatically check a new
one for the same existing target, retaining its mounted workspace while checks
run. Previously reviewed writes still fail their old configuration preconditions;
recovery never repeats or authorizes mutations. Failed recovery or background
authentication that cannot proceed without a person can still require Connect
again. Busy implicit sessions remain alive while used by views, streams or agents. The two most recently left implicit sessions expire after 10 minutes;
other unused implicit sessions expire after 60 seconds.
Closing a non-selected connection ends its owned views and streams. Terminal
and tunnel handles own their original connection snapshots independently.

Background Kubernetes exec plugins run headlessly through `execshim`, with a
15-second limit. Authentication requiring a person closes the background session
and asks the user to select that connection and press Connect again. Foreground plugins are also
bounded and terminate with their parent. Configuration changes invalidate the
session incarnation; old responses cannot update the replacement session.

## Download history and native file actions

The native desktop runtime owns one download-history manager shared by the main
window, detached log windows and container transfers. Native destination dialogs
remain the authority for paths. Transfers register only after a destination is
chosen; their existing staged writers publish only on successful completion.
Download-history IDs, not renderer-supplied paths, authorize Open/Reveal actions.
OS dispatch uses fixed executables and separate arguments, never a shell, and
requires an existing completed regular file or directory. The Downloads Wails
service is UI-only and does not expand HTTP or agent grants.
Copy-path requests use the existing serialized, generation-checked clipboard
queue, so a later path copy supersedes pending Secret-value reads without letting
an older clipboard operation overwrite it.

The manager exposes bounded metadata snapshots and change invalidations; progress
notifications are throttled and completion is immediate. A small polling fallback
keeps window snapshots current. Private `downloads/history.json` retains up to
200 recent records while never evicting active transfers. A process-lifetime OS
lock prevents two desktop instances from overwriting one another's history.
The startup read handle closes before any rewrite so Windows can replace the
existing file; the process-lifetime lock stays held. Writes use owner-only
temporary files, sync and atomic replacement. Read/lock
failures preserve existing metadata, use process-local state and expose a storage
warning. Application exit marks unfinished jobs Interrupted without retry or
command replay. Clear and per-entry Dismiss remove terminal metadata only, never
file contents. Native PrepareDelete issues a short-lived one-shot token for a
completed record's backend-owned path, current item identity and parent directory.
The delete review focuses enabled Cancel before paint, including when preparation
initially mounted it busy, without overriding a choice already inside the review.
DeleteFile requires that token and the same live record, rechecks item/parent
identity and metadata, and removes through os.Root; recursive folder deletion
does not follow symlink destinations. Successful deletion removes the record;
failures retain it. This is an explicit permanent local-file action, not OS Trash
or a cluster mutation; no filesystem-wide atomicity is claimed.

Download metadata contains only an allowlisted `failureReason` code for failed
entries. Native sinks classify structured OS/provider/API errors and mark the
failing stage; browser save writers classify DOM/API codes and history readers
mark protocol/limit failures separately from destination writes. Native log
exports accept only a safe reason code on failure cleanup, never renderer error
text. First terminal failure wins. Persisted/browser-shared records contain no
raw exception, API detail, remote stderr, credential or downloaded content.
Unknown/old reasons become `unknown`, and the UI translates the code using the
current language. Browser records are reconstructed from declared safe metadata
fields so unknown input properties are not retained or broadcast.

The renderer retains at most 200 viewed successful record IDs in origin-local
storage, with storage-event/BroadcastChannel synchronization where supported.
Acknowledgement happens when completed/browser-handoff entries appear in an open
Downloads panel, never when an active job is merely listed. It changes only the
button indicator and does not alter native history or authorize file actions.

Browser-mode downloads retain no full local paths or file contents. Origin-local
terminal metadata and BroadcastChannel invalidations share their history with
detached log windows; active metadata remains process/window-owned. Blob handoff
is distinguished from a verified save-picker completion. Native opening and
folder actions are not fabricated for browser-managed destinations.

## Shared workspace presentation

Resource lists, Helm, Graph, Timeline, Security and RBAC use `WorkspaceToolbar`.
Section identity remains available as an accessible heading; visible names,
counts and coverage belong in the active `LocationStatus` footer. Do not add a
second section selector or a private loading row above retained content.

Finite reads use `useSnapshotRequest` for single-flight refresh, retained
same-scope results, explicit cancellation/retry and late-response fencing.
Changing client/request/scope identity cancels the old read and drops data from
the previous scope. Resource watches retain their protocol-specific `ViewSync`;
both paths share `RefreshButton`, `ListRefreshProgress`, `ListLoading` and
`CancelledList`. Inline elapsed/progress controls appear after two seconds in a
reserved toolbar slot. Unknown byte counts remain unknown, never estimated.
`SnapshotBoundary` keeps content mounted while initial reads and cancelled
observations hide it. Cancellation is not a mutation rollback guarantee.

Graph admission remains explicit per connection and owns its request identity;
a retained editor does not authorize a read on a replacement connection. Global
Helm settings are independent of connection changes. Its graph read includes the
layout worker, which is terminated on cancellation or disposal. Helm section
navigation belongs in the normal left catalogue, including settings; operation
review, one-shot plans and unsaved-edit protection remain feature-owned.
Helm operation outcomes use the shared status-bar notice channel instead of
inserting private success banners. Comparison baseline status occupies a permanent app-header slot and a portal
popover; selecting or clearing it must not change workspace geometry.

## Live tables and discovery

Kubernetes LIST supports field/label selectors and continuation pages, but no
arbitrary substring search or column sort. A request limit controls chunk size;
it does not establish a globally sorted or searched result from an unseen
collection. Complete local filtering/sorting requires the relevant inventory
(or a separately built index). See the
[Kubernetes API contract](https://kubernetes.io/docs/reference/using-api/api-concepts/).


A resource table loads only its demanded kind and selected namespace routes.
The initial collection drains automatic 100-object LIST pages, reducing each
page to compact projected cache objects before fetching the next. The complete
snapshot's resourceVersion starts the watch; failed/expired/repeated continuation
candidates never become a partial successful list. WatchList-capable servers
stream an initial snapshot instead. Full local search and sorting need no
user-facing pagination. Opening a table does not fetch each object's detail;
YAML/facts use a selected-object read. Ordinary LIST can still carry full wire
objects where the required columns cannot be served as metadata; trimming is
not server-side projection.

All Namespaces for a namespaced live collection has admission limits: 2,000
objects, 16 MiB of raw encoded current objects, and 8 MiB per finite LIST response
when using the session HTTP transport. A remainingItemCount estimate can reject
on the first page; without it, actual observations enforce the same inventory
budget. Watch growth is checked too. Exceeding a limit stops that broad cache,
clears its rows and asks for explicit namespaces. It never presents a truncated
list as complete or automatically widens a namespace choice. A fresh explicit
namespace route remains a complete collection. These are admission/payload
limits, not a total process-memory bound. Explicit snapshot tools retain their
own feature budgets and coverage reporting.

All list-refresh controls share an icon action and spin until their operation
finishes. A live-view refresh keeps old rows while a replacement snapshot loads,
then replaces them atomically; old callbacks cannot mutate the new observation.
Failed refresh keeps old rows visibly stale, except a broad-scope admission
failure clears them. Another consumer can retain the old shared cache until its
last lease ends. Catalog refresh waits for discovery even when no revision
changes. Filters, sort, selection and editor guards remain owned by the UI.

Each admitted Kubernetes session shares a read transport budget across dynamic,
metadata, server-side Table and discovery clients: 10 request starts per second,
a burst of 20, and at most four finite response bodies in flight. Finite slots
are released on body completion, error or close. Watches, followed logs and
upgraded connections pay for establishment but do not hold a finite-response
slot for their stream lifetime. Cancellation applies while waiting for either
budget. Existing stricter client-go throttles and mutation behavior remain;
this budget adds no retry and does not bound watch event rate or collection size.
For a provider with a live scope-kind view and a lightweight scope checker,
the UI scope bootstrap checks access without copying the whole scope inventory.
Kubernetes checks with limit=1; its live namespace view owns the complete list.
Direct scope readers lease that same informer when retained. Known observation
errors remain errors rather than successful stale scope results.
Idle informer retention has three session-wide limits: eight caches, 25,000
objects, and 32 MiB of encoded object payload plus identity/label/owner strings.
The last measure is a payload budget, not a Go heap or process-memory guarantee.
Oldest idle caches are evicted first; active leases are preserved. The limits
apply in background sessions too. A local five-second sweep also catches caches
that grow through watch updates after their last view closes; it issues no API
read. Completed foreground idle caches retain their existing 60-second grace expiry.
An unfinished inventory loses its last lease immediately: its outstanding reads
are cancelled instead of draining an unused collection during the grace period.
A latched broad-scope admission failure remains reusable until normal eviction.


Events invalidate state; they are not an unbounded event journal. Each subscriber
keeps the latest event per key. Overflow and reconnection cause `resync`.
`OpenView` returns a unique, never-reused view ID. `GetRows(since)` returns an
atomic versioned delta or a complete reset, including an explicitly empty reset.
The frontend advances its cursor after applying data, keeps one pull in flight
per view, and ignores responses from closed or replaced views.

`CancelViewLoading` latches a query under its session owner and explicit UI
connection token, cancels its pending `OpenView` operation by unique request ID,
and closes its observation. `OpenView` refuses that query with `cancelled` until
an explicit `retry` or a different connection incarnation. Cancellation is app
memory state, not a saved target preference; it survives frontend reconstruction.
The request-to-view association covers cancellation racing with result delivery;
ordinary Close/owner cleanup and lazy lease-expiry pruning release that metadata.
Other existing consumers and pending opens have independent request IDs and keep
running. Epoch checks and frontend generations reject late deliveries. Both HTTP
and native bindings support the operation; the native OpenView forwards its context.

Kubernetes inventory contexts carry a response-body counter shared with their
cache. It measures bytes actually read (including failed attempts and initial
WatchList data), before admission wrappers, excluding headers/TLS and discovery
or schema probes. HTTP decompression may have occurred before counting, so this
is payload consumed by the client rather than on-the-wire network traffic.
Counters stop after initial cache sync; subsequent watch traffic is not included.
A warm-cache replay reports zero newly downloaded bytes rather than the cached
snapshot's historical transfer size.
Namespace/source unions sum source counters. Loading observations publish changed
status at most every 250 ms through the existing coalesced cursor protocol, without
extra upstream requests. The UI's elapsed timer covers the whole loading operation,
including waits for the budget and initial view/schema setup. Docker Engine feed epochs also count their finite initialization/list/inspect
responses through the same status contract. A local-only progress reporter keeps
bytes visible while refresh preparation waits for the complete shared snapshot;
it adds no Engine calls. An unfinished Docker feed loses its last lease immediately
instead of continuing through the warm-cache grace period. Synthetic or other
providers without body instrumentation report no value rather than a fabricated zero.

UID is row identity. A same-name replacement removes the old row and adds a new
one; a late deletion of the old UID cannot remove its replacement. A view is
ready only after the initial snapshot is delivered. Permission failures remain
errors rather than looking like an empty table.

Kubernetes discovery runs outside `Open` and updates from CRD watches, explicit
refresh, and resync. Unconfirmed groups retain their last known kinds. Removed
kinds terminate their views. Server-side Table responses determine discovered
columns; a schema change opens a new view epoch. A fallback to ordinary objects
is remembered until the session ends. Live age conversion is used only when a
column's provenance is known, not inferred from its description.

Explicit `ScopeSome` selections fan out to namespace-specific `ScopeOne` reads,
watches, schema probes, and metrics. An empty set stays empty. An independent
Reset replaces only its source; shared rows survive until their last owner is
removed. Partial failures keep successful sources and expose coverage. Global
kinds are opened once. The UI's multi-scope selection does not widen agent grants.

Graph inventory loading requires an explicit Load graph click once per backend
connection session. Opening the graph before that click creates neither an inventory
request nor a layout worker. Navigation and warm target switching retain this choice;
Disconnect/Connect, a replacement session and application restart require a fresh click.
The choice is memory-only, never a saved target preference. Automatic replacement retains
a guarded unsaved editor until its edits are released, then requests fresh graph admission.

Graph, Timeline, RBAC and Security snapshots share the session-bound inventory
gate and continuation-token cursor. Session cancellation applies before waiting
for that gate. Each adapter retains its own resource/page/byte budgets, projection
and coverage classification; metadata-only reads and optional-API absence semantics
are not generalized into full-object inventory.

Compact resource relationships, Deployment revision history and declared Helm
resources share semantic detail-table layout and compact action controls. These
bounded read-only lists retain feature-owned columns, localized labels and guarded
resource navigation; they do not inherit primary-grid selection, bulk actions or
persistence. Ingress relations retain one row per declared HTTP path with host
and pathType, including repeated Service references; defaultBackend is represented
without an invented HTTP path. This is configuration, not observed routing. Timeline keeps its time-axis grid and the agent journal keeps its
server-paginated, variable-height evidence rows.

The cache stores reduced objects, omits Secret/ConfigMap values from list caches,
and attributes metrics only to matching live UIDs. Background caches use bounded
retention. View lifecycles are keyed by immutable requests and session ownership.

## RBAC explanation

Optional provider `RBACSource` exposes UI-only `RBACSnapshot` and `CheckAccess`
through an admitted connection. Namespace-bounded bindings resolve User/Group/SA
subjects and referenced roles, with metadata-only ServiceAccount inventory and
visible incomplete coverage. SelfSubjectReview identifies the current connection;
SelfSubjectAccessReview checks its exact request attributes without adding subject
impersonation. Both are non-persistent introspection. The frontend virtualizes
rule rows and separates declared matches from server decisions, invalidating late
checks when fields change. Resource properties expose guarded inspection and
Forbidden read checks. See [RBAC explanation](rbac-explanation.md).

## Security inspection

The UI-only `ClusterSecurity` API uses optional provider `SecuritySource` and
an explicit admitted connection. Bounded dynamic reads project workload hardening,
role declarations, NetworkPolicy selection and Trivy VulnerabilityReport evidence
without returning manifests, values or server error details. Namespace coverage,
scanner availability and report timestamps remain explicit. The Security workspace
uses standard guarded ResourceDrawer navigation and the shared virtualized
resource table. It runs
no scanner and performs no writes; see [Security](security.md).

## Events timeline

The UI-only `ClusterTimeline` API uses optional provider `TimelineSource` and an
explicit admitted connection. It reads bounded Events API snapshots and
metadata-only workload ownership, with per-namespace scope, visible partial
coverage and session cancellation. The frontend groups UID-based controller
chains and virtualizes event rows; standard ResourceDrawer navigation retains
edit guards. Cumulative event series are not audit history or continuous failure
durations. See [Events timeline](events-timeline.md).

## Cluster graph

The UI-only `ClusterGraph` API and specialized `graph` workspace use compact,
bounded inventory snapshots from optional provider `GraphSource` implementations.
Kubernetes discovery supplies resource types; scoped reads remain namespace
specific. Relationships are resolved by UID and indexed selectors/references.
Worker layout and an imperative Canvas2D camera keep pan/zoom independent of
React node rendering. Consecutive overview strips share native canvas fills
by colour and opacity; individual borders/text flush each run. Every rectangle,
relationship and semantic-zoom threshold is retained. Selection reuses
ResourceDrawer and its edit guards.
The optional layer shows declared routes, not measured traffic.
See [cluster graph](cluster-graph.md) for coverage, limits and lifecycle.

## Docker and Compose

The Docker provider retains the stable internal ID `compose` for persisted
connections, selections and grants. Its container feed lists and observes all
containers, including those without Compose labels. The default Containers view
is in the Engine group; Compose Projects and Services remain separate.

All resources includes standalone containers and unused networks/volumes/images.
Explicit project selections include only project members and their related
networks/volumes; an empty selection stays empty. Images remain an unscoped Engine
inventory. Standalone containers keep an empty project in their Refs and never
form a synthetic Compose project or service. This preserves agent boundaries:
all-project and cluster read grants do not authorize these scoped-kind objects
with an empty project. Container details/logs/exec/actions recheck supplied
project and UID constraints. Metrics do not sample IDs outside selected scopes.

Compose groups observed Engine resources by their labels. It does not read
compose files to infer desired replicas or create missing services. Supported
endpoints are Unix sockets, TCP/TLS and local Windows named pipes (`npipe://`); `ssh://` endpoints are visible but cannot
be opened. HTTP mutations are not automatically retried.

Feeds are ordered by incarnation and source. Events supplement initial/repeated
lists; reconnecting reconciles state. Shared network rows use source ownership.
Status is projected from current container health and exit state. Service metrics
cover at most 20 replicas and indicate limited coverage when appropriate.

Docker log `since` is positional. Resume remembers bounded line identities and
an anchor; unfamiliar lines before the anchor become an explicit gap rather
than being silently discarded. A stopped source waits on events, not polling.

## Streams

Terminal fitting is deferred during panel pointer drags, then the final size is
applied once. The protocol suppresses equal dimensions. Native Cyrillic keydown
is forwarded once and its keypress replay suppressed; composing/AltGr events
retain xterm handling. The helper textarea clears delivered residue after xterm
input/composition finalizers, outside active composition and screen reader mode.
New key/input/composition events cancel pending cleanup; unmount removes its
listeners and timer. No output or transport-input deduplication is performed.
Shell-generated SIGWINCH redraws remain raw output.

Workload tool metadata is prefetched with details and cached for five seconds.
Selections pin concrete identities; pending clicks are cancellable and never
show a Loading dropdown row. A shared dropdown chooses an
explicit exec instance for Terminal/Files or a concrete UID-bearing Pod Ref for
Logs. Aggregate LogInfo exposes owned Pod refs, including terminated Pods, without
changing stream ownership checks; the original workload remains the All Pods
choice. Provider metadata distinguishes container choices from stdout/stderr
streams: Docker logs retain their immediate stream opening, while concrete Pods
offer container choices. Explicit setup uses provider-supplied instance labels. Right-click setup always shows
instance selectors, even for one option; only Terminal accepts a command.
Cancellation and context changes invalidate pending picker responses.

UI log queries carry a case-insensitive `*` text filter. Providers scan beyond
the raw tail, consume every raw record into their reconnect cursor, and select
matches before merging/trimming the history tail. Filtered backlog scans expand
finite tail windows sequentially per source, charging overlapping reads to a
shared 10,000 raw-line budget and the group byte/deadline budgets. The readers
enforce local byte caps, not just upstream parameters. A shared bounded sink filters
live records before transport. Filtered live views pair that finite scan with
independent EOF-only capture (explicit upstream tail zero), remap sources by
stable key, and reconcile timestamped overlaps by multiplicity. New-record
buffering is capped at 50k/16 MiB and reports overflow. A shared per-view raw
read pacer limits live decoding to 1 MiB/s across sources; an additional live
admission semaphore retains half the existing provider slots for other reads.
Atomic history-start/history-end snapshots provide bounded/coalesced previews
and a reserved UI loader while capture continues. Raw scan byte/time limits remain separate and
report incomplete history. Changing the filter opens a new fenced stream;
detached windows include the filter in query identity and reject stale
frame/snapshot/status/reset deliveries for older queries. Local search operates on
the matching buffer. Agent grep/export limits retain their separate contracts.

UI logs and terminal traffic use a token-protected loopback server in both app
modes. They never stream through the `wails://` asset transport. Stream IDs are
one-shot and bound to session ownership. Guards run before consuming an ID.

Agent exec uses its own scope-wide `exec` grant for the selected namespace/project.
Existing kind-restricted grants retain their limits; the editor does not widen them
unless the user replaces the permission. Unsupported kinds still cannot execute commands.
GetExecInfo → PrepareExec → RunExec selects an explicit member/container and
pins object UID plus opaque configuration revision. Preparation retains only
bounded request/description data in process memory, not a UI terminal handle.
Execution prepares a fresh provider handle on the grant-checked AgentCall session,
rechecks membership and runs once without PTY or replay. Default confirmation
shares the existing pending-plan UI; argv and stdin are shown but never journaled.
Commands default to 300 seconds and accept 1–1800 seconds. Exec has its own
requested command deadline plus a 30-second outer setup allowance, so the generic
two-minute action/edit deadline cannot clip a longer exec. Running confirmed
commands are not expired by the ten-minute confirmation wait TTL; retention
starts after completion.
Active operations register before authorization so grant saves cannot miss them.
Revocation and session/configuration cancellation close transport; remote process
termination is best effort. Separate stdout/stderr share a raw byte budget and
return base64 with observed exit, completion and stop metadata. Docker finite
input half-closes the write side after EOF while preserving output; interactive
terminals retain their original input lifetime. Empty input is detached. Retained
queued results are bounded, expire, and require current exec access to read.
Standalone Docker containers retain the existing empty-project exclusion.

Agent log history and live streams use the local agent transport directly, with the
same `logs` grant for channel discovery, snapshots, intervals and following.
No UI stream IDs are exposed. Active reads register before checking grants, so
a concurrent revocation cannot miss them. Grant changes cancel reads, including
quiet streams; session replacement and client disconnect also end them. NDJSON
frames have backpressure, write deadlines and an explicit completion frame.
Time intervals require a matching-line limit. Streams also require a limit and
have byte and time budgets; JSON snapshots count escaping and metadata in their
byte budget. Grep is applied before output limits.
Exports write incrementally into unique owner-only files in Downloads (0600 on Unix, protected user DACL on Windows) and return
only bounded metadata. Finite archival provider reads bypass UI tail and merge
buffers, read sources sequentially, and keep no reconnect history.
Historical time filtering happens before choosing a bounded tail. Unknown
timestamps and incomplete source coverage are reported, not treated as success.

Log limits cover individual lines, frames, total buffer bytes, and line count.
The UI retains up to 50,000 lines and a bounded character buffer. Quiet streams
send a small frame after 100 ms so WebKitGTK delivers buffered data. Slow readers
have deadlines. Search with regular expressions runs in a time-bounded Worker.

Terminal WebSocket traffic has cumulative output credits (`ack`) and input
credits (`iack`). Interrupt clears pending input and is sent before ordinary
queued bytes. Unacknowledged data is bounded. Closing a Kubernetes terminal sends
Ctrl+C then Ctrl+D before cancellation so in-container children can exit.
OSC clipboard writes, title changes, and unsolicited link opening are disabled.

Tunnels bind loopback only. A Service tunnel rechecks its backend after an
upstream failure and can choose a replacement. Handshakes, heartbeats, and reads
have cancellation/deadline handling. Neither terminal nor tunnel selection can
silently substitute another configuration after a target changes.

## Mutations and secrets

Prepare reads one session incarnation. Run verifies configuration identity, UID,
version/Expect, permissions, and the same route before one write. A known failed
precondition can be retried only after proving no write happened; ambiguous
transport failures are `unknown`. Client-go automatic mutation retries are disabled.

Kubernetes YAML edits compute a patch from the reviewed original and edited text.
Run uses the reviewed UID/resourceVersion without rebasing. Dry-run is used only
when the API route is known to support it. Otherwise preview is a local overlay
with an explicit warning. The frontend `mayLeave` guard protects every navigation
that would discard YAML or value edits; automatic page replacement also waits.

Secret YAML is metadata-only and uses length placeholders. Backend validation
rejects data/stringData/type/immutable edits and placeholders in patches. Value
operations use a separate read/review/write path for the selected key and object.
Secret error text is sanitized; real values never enter list caches, events,
recent-object history, grants, audit records, or SQLite.

The same guarded value API supports built-in ConfigMaps. `data` reads as UTF-8;
`binaryData` is decoded losslessly and edited as base64 when a text field would
change its bytes. Per-key merge patches remove the same key from the opposite
storage field while retaining all other keys. Existing binary keys retain their
storage. ConfigMap Run checks the reviewed version before reconstructing storage
and still sends UID/resourceVersion preconditions in the single patch. Both kinds
respect immutable objects and report bounded Pod consumers; key/value data never
enters list/watch caches through this feature. ConfigMap text review plans may
include only the selected key's freshly read and expected dry-run/local text for
a line diff. Binary/CRLF keys and all Secret plans omit text. Text is not included
in signed grants or persisted; the API also strips it from non-ConfigMap plans.

Action availability and parameters come from kind descriptors. Node drain,
CronJob creation, Deployment rollout, debugging, and bulk operations have their
own guarded plans. A destructive confirmation displays the actual target,
namespace, kind, and name. An unknown outcome must not be silently retried.
Bulk operations prepare and run individually and retain per-object outcomes.

## UI state and persistence

SQLite uses WAL and embedded migrations. The data directory is owner-only.

| State | Storage and ownership |
| --- | --- |
| Interface language | `ui_prefs.language`, global; empty means automatic |
| Selected connection | `ui_prefs.selected_target` |
| Ordered favorites | `ui_prefs.favorite_kinds`, provider + kind, global |
| Section expansion | `ui_prefs.nav_sections`, global stable keys |
| Kind, namespace selection, dock height | `target_state`, per target |
| Page/filter/sort/cursor/details | `target_state.pageMemo`, per target |
| Primary column widths | `target_state.columnWidths.<kind>`, per column ID |
| Recent objects | `recent_objects`, full Ref + UID; 50 per target, 500 total |
| Agent identity, grants, journal | `agent_targets`, `agent_grants`, `agent_audit` |

The target-state value limit is 4096 bytes. Oversized page snapshots remain in
memory; scope write failures are visible. A shared write queue prevents older
writes from overwriting newer state after fast target switches. Global favorites
and section updates are serialized; optimistic rollback replays remaining changes.
Unavailable kinds/groups retain their preferences. Existing per-target `navOpen`
settings migrate once from the last selected target into global section state.
Removed Overview selections map to the default resource kind with the same scope.

Resource navigation search is shared across target switches within one run and
clears after restart. Search expands groups temporarily. Favorites appear in only
one place. Namespace row/Enter selects one; checkbox/Space toggles a set.
Inspector Events uses the shared virtual resource grid with a compact detail
appearance. Its transparent surface, muted header and row separators match the
read-only detail tables; content height is bounded to seven rows. Sorting, column
resizing and Subject/UID-scoped live observation retain the shared paths.

Column resizing uses inherited CSS variables, one DOM update per animation
frame, and commits React/persistent state on release. Known table headers remain
mounted during the first row read; later schema refinements preserve sorting by
column ID. Security uses this same virtual grid, with custom evidence/action
cells and no separate pagination or table styling.

The palette normally filters eligible live rows by the target's selected scopes, including
on unscoped tables. History influences ranking only, with exact Ref/UID matches;
it cannot create candidates. Explicit type-and-name queries may attach one
temporary view to the shared ViewHub, preserving scope and normal session guards.
An explicit scope/type/name query narrows that view to the named scope and only
commits its scope when a result is selected, atomically with guarded navigation.
No parallel inventory sweep or implicit connection is performed. The view
lifetime follows the query/target/scope and palette, so late responses are
discarded and late opens closed. It opens the object's kind and selects a row by
full Ref + UID once that row arrives. A one-shot request cannot reselect a row
later after an unrelated manual selection.

## Visual and performance constraints

The header connection picker replaces the permanent targets column. Provider
headings group searchable rows with independent selected and connected states.
The configuration controller stays mounted outside the picker, so closing it
cannot destroy an unlock continuation or an open source editor. Appearance uses
stable catalogue IDs in the existing per-target UI-state queue; initialization
reads saved data before random assignment, and Save waits for initialization.
Failures are visible and retries retain the assigned value. No appearance action
opens a provider session or changes source/configuration revisions. The catalogue
contains 32 icons and 16 curated foreground/background presets. Resource section
headings and counts are screen-reader-only in the toolbar; the visible navigation
identifies the current section. Value hints measure only the hovered table cell
once before display, without table-wide resize work or per-cell observers. Shared
14 px hints wait 800 ms; pointer leave cancels pending display before measurement.
Explicit palette `scope type name` queries use one scope/type view and commit
scope together with the chosen result behind the navigation edit guard. Log
selection remains inside its owning viewport even if native range endpoints
escape to the document. Detached log windows expose log export, while Downloads
history belongs to the main window only. Resource switches keep inert toolbar
controls and section geometry until the new object settles; privileged children
are unmounted immediately and their layout space is retained.
The unopened/closed-connection scene uses two bundled transparent schematic
illustrations, anchored independently to navigation and main panels. The larger
lower-left lighthouse and its extended beam have fixed geometry and are clipped
when navigation resizes; the tower never scales with the panel. Base colors
match the connected panels; there is no shared panorama or translucent sidebar.
The illustrations are absent from mounted resource workspaces, including held
edits during recovery. Its static
provider section preview performs no discovery or resource reads. The main status
area owns the single Connect/Cancel action and unchanged attempt controls.
Navigation section icons are deterministic SVGs keyed by canonical group name,
with a neutral fallback; localization does not change icon identity.

Use graphite surfaces, system fonts, 14 px text, a 17 px root rem, 32 px table
rows, 40 px headers/toolbars, 14 px semibold titles, and mostly
2/4 px radii. Command-palette rows have a 38 px minimum height and vertically
center the section, object name and hint despite their different font sizes.
The SVG app icon embeds as
256×256 PNG. Desktop and installer icons derive from the website’s optical-eye design,
with heavier aligned outlines and fewer details for small taskbar sizes. The
SVG is rasterized at four-times each output size and downsampled with
Catmull-Rom for smooth edges. GTK supplies exact
16/24/32/48/64/128 px representations as one icon family so panels choose their
own size. The combined ARGB property stays below the X11 request limit; simply
embedding a 512 px PNG can leave the GTK window without a taskbar icon. The resource list reserves 24 px for GTK overlay-scrollbar hit areas.
Connection details remain a header-button modal; the old Overview page is absent.

Agent permission checkboxes retain native input/label/keyboard semantics, with
contrasting checked and disabled states; forced-colors mode retains native rendering.

`@theme static` preserves runtime ANSI/xterm colors. CodeMirror and xterm are
lazy-loaded. Resource YAML preloads on tab hover/focus or an explicit edit request.
While the editor chunk loads, a loading indicator replaces the former full-document
plain-text fallback, avoiding duplicate YAML DOM and an unhighlighted first paint.
The syntax parser and virtualized editor keep their existing bounded initialization.
Switching readable resources reuses the same CodeMirror view and replaces its
document before paint; a changed resource identity resets selection and scroll.
Editable drafts remain isolated and protected by the existing leave guard.
Each entry JS chunk has a 300 KiB gzip ceiling enforced by the build.
Virtualized row keys remain stable across layout changes. The application does
not bundle a browser, run telemetry, or keep a tray/background process.

Memory verification measures the application and its webview children. After
warm-up, workload phases must stay within +100 MiB and settle for at least ten
minutes after load. Avoid measurements when system memory pressure would evict
pages and make Private_Dirty look artificially small.

## Helm boundary

The Helm UI reuses `ResourceTable` and `DetailsPanel` for list navigation,
sorting, column resizing and detail-panel sizing. Only the Helm SDK operation
and repository forms remain specialized. Resource details retain their static
layout under a loading overlay while a new object is fetched; stale controls
are inert and protected value/port components unmount immediately on selection
changes. Late responses cannot replace the current object.

`internal/helm` wraps the embedded Helm 4 SDK. A Kubernetes session supplies its
already admitted REST configuration, including proxy/authentication/exec shim;
no second kubeconfig resolution can redirect a Helm operation. Transport and readiness-wait contexts
bind legacy SDK requests to the UI operation and session lifetime. Mutation
Retry-After responses do not trigger client-go retries.
The upgrade SDK cancellation listener uses a detachable context bridge. Once
RunWithContext returns, operation cleanup cannot turn a successful upgrade into
a failed release through a late SDK cancellation listener. Active cancellation,
deadlines and cancellation causes still propagate; transport and readiness waits
retain the original bounded operation context.

The UI-only `Helm` API is served through HTTP and Wails; it is absent from the
agent method registry and additionally requires the UI context marker. UI kind
metadata selects the specialized workspace without adding virtual resources to
provider or agent catalogues. Full values/manifests never enter generic views.
Prepared plans bind session incarnation and storage configuration, pin chart
content/values and fingerprint release contents. Expiration actively drops the
plan; Run consumes it before checking/writing. SDK rendering receives a fresh
chart tree so preview dependency/value processing cannot mutate the execution
input. Per-release operations are serialized within Ocular. Other Helm clients
remain external concurrency actors, not participants in an atomic distributed lock.

HTTP chart/index responses and expanded root archives are bounded to 32 MiB;
SDK nested-archive limits also apply. Repository authentication is scoped to its
origin, and redirects strip credentials across hosts. OCI uses an explicit
Ocular-owned credential provider instead of Docker credential-helper fallback.
Private configuration uses protected application-owned files.

`internal/helm/sqlstore` is an Apache-2.0 adaptation of Helm 4.3.0's PostgreSQL
driver and release codec. The upstream driver has no connection-close API and
uses non-context queries, which is unsuitable for repeated desktop calls. The
adapter preserves Helm's tables, migration names and release encoding, adds
operation contexts and pool closure, propagates transaction commit failures and
does not turn arbitrary read failures into missing releases. It must be compared
against the upstream SQL schema/codec when upgrading Helm. SQL permissions and
row-level policies remain the database administrator's responsibility.

Tunnel failure counters and their last-error details are published under the same
lock, so a snapshot cannot report a failure without its associated error.

## Container file inspector

The UI-only `Files` method is exposed through both HTTP (bounded request with
no-store) and Wails. It prepares provider exec handles with explicit instance and
channel identity. The provider Terminal raw mode disables the PTY, separates
stderr, and preserves stdout bytes; existing interactive terminals keep their
PTY behavior. Paths and content are passed as argv/stdin, never interpolated
into shell source. Output and UTF-8 text are bounded to 2 MiB, errors to 8 KiB,
and each operation to 30 seconds. Saves compare the captured configuration
revision and original content digest before writing without mutation retries.
The frontend holds unsaved edits, ignores obsolete responses, and loads
CodeMirror language parsers on demand. Directory lists stay cached for the
inspector's lifetime, even after collapse, and reload only on explicit Refresh.
First loads have a non-selectable animated child placeholder; Refresh preserves
children and animates the parent row. File reads overlay the editor with a visible
loading state. Symlink listings include their raw readlink target, including
relative paths; the UI-only resolve operation uses readlink -f within the pinned
container before revealing a target. Paths remain argv values, and NUL framing
preserves whitespace and newline characters in entry names and link targets.

Desktop `FileDownloads` owns the native local-directory grant and accepts no
renderer-provided destination path. `Service.DownloadFiles` is a separate UI-only
byte stream using the same pinned identity and connection revision. A remote
`sh`/`tar` command passes paths as argv and feeds a backpressured pipe; stderr is
bounded and the exit status must be successful before publishing. Tar entries
are confined through `os.Root`, portable path checks and exclusive file creation.
Links are installed last, and external links/special entries are refused.
An owner-only staging tree is removed on any failure. Linux/macOS publish via
exclusive rename flags; Windows uses MoveFileEx without replacement. Existing
names, including names created during transfer, remain intact. Downloads run
for at most 30 minutes and contain at most 100,000 entries. Their bytes never
pass through Wails JSON or the text-editor API.

The editor captures modified physical keys before both content and search-panel
keymaps, normalizes only shortcut keys, and runs CodeMirror's existing commands.
It does not reinterpret ordinary typing, IME composition or AltGr.

### Detached log rendering

The main window remains the single provider-stream owner when a log tab is
shown in a secondary window. The child hydrates from a bounded log-buffer snapshot
and consumes ordered NDJSON frames, maintaining the original entry IDs and
source ANSI/level carry. Query and view changes travel back to the owner. Wails
windows relay through an application-only service and direct Wails events,
bypassing the coalescing resource-event queue; browser popups use BroadcastChannel.
Frame sends are serialized, and log query equality compares fields rather than
JSON key order. No window state or log contents are persisted. Closing the main
window explicitly quits Wails even when a secondary window remains open.

Resource navigation section headings use 14 px semibold text. Search fields keep
icons at a fixed size and allow only their input to shrink with a panel.

Log history downloads use the UI-only `OpenLogHistory` API and the existing
session-owned, one-shot NDJSON transport. The provider receives a finite Archive
read with TailAll, selected channel/previous setting and no viewer time/text
filters. Backend and frontend bound bytes/lines/sources and reject incomplete
history; source retention and sequential multi-source reads still apply. This
adds no agent endpoint or grant. Closing a connection retires its export stream.

Native log downloads use the application-only Wails service and the system
Downloads directory by default. Explicit right-click location selection uses a
system chooser attached to the requesting window. Default publication atomically
claims a unique filename and never replaces an existing entry. Begin returns an opaque app-owned
export id; Append writes bounded UTF-8 chunks, and Complete publishes
the destination only after a successful sync. Explicit location selection retains
staged replacement of the selected destination. Cancel, window closure, app
shutdown and expiry remove the owner-only staged file. The renderer never sends
a destination path. The Downloads panel cancels owned native transfers by issued
history ID; log cancellation also notifies the owning renderer to abort its read.
Browser cancellation routes to the owner through bounded metadata messages,
including detached windows. New transfers open the main Downloads panel.
Explicit browser save pickers likewise stage chunks; ordinary browser downloads
use the download manager with a bounded 64 MiB buffer. A download must receive a
valid complete end frame before committing. The desktop loopback server does not
enable the old Downloads save route. Agent exports retain their separate private
Downloads contract and existing permission checks.

The file inspector renders through a body portal above resource-panel resize
handles. Its tree uses compact 24 px virtualized rows with full-path keys and
lazily loaded directory branches. The provider default pins the object, instance
and container; location controls are informational rather than selectors. Folder
refresh and expansion do not replace the editor or discard drafts.

Deployment revision metadata is attached to resource details by the Kubernetes
provider using owner UID and retained ReplicaSet revision annotations, with the
same bounded history lookup as rollout review. ReplicaSet/Deployment details
expose a normalized Pod-template YAML for lazy read-only comparison. The UI never
infers revision availability from a kind ID and never mutates resources from the
revision section; failed and truncated history is reported separately.

## Resource comparison

The UI-only `CompareResources` API reads two explicitly selected Resource YAML
objects through admitted sessions. Selections pin UID, scope, configuration and
connection incarnation. Bounded normalization sorts mapping keys, preserves array
order and 64-bit integer precision, excludes Secret payloads, and returns paired
representations with/without Kubernetes service fields. The global comparison
state retains only two references across navigation; the open modal owns the
non-persistent YAML snapshots. No new agent endpoint or mutation path is added.
See [resource comparison](resource-comparison.md).

## Investigation workspace continuity

`web/src/workspace` strictly serializes whitelisted UID-pinned references and
global panel dimensions into the existing 4096-byte target_state value. The
App owns restoration and serialized writes; no provider/session is opened by
loading metadata. The dock supports read-only standard ResourceDrawer panels,
dormant log references and ended terminal slots. Explicit resume validates UID
and admitted connection identity; properties cannot inherit main-editor drafts.
Bodies, values, logs, commands and screens never enter this snapshot. Existing
page/scope/filter/column persistence remains independent. See
[workspace continuity](workspace-continuity.md).

## Shared UI and transport boundaries

HTTP RPC bodies are limited before JSON allocation: 2 MiB for metadata, 40 MiB
for two escaped edit texts, 10 MiB for value inputs, and the existing 6 MiB Helm
and 13 MiB file budgets. Token/origin guards run first. A present body contains
exactly one JSON value; a 10-second body-read deadline is cleared before the provider call.
Decoded service limits remain authoritative for both HTTP and Wails.

Session resolution captures its exact entry under the local session mutex and
revalidates that entry before incarnation-sensitive work. Disconnect or a new
implicit session cannot redirect an admitted UI read/review to another owner.
This is local lifecycle ownership, not a transaction with the cluster server.

Read-only/setup dialogs share focus restoration, reachable-control tab wrapping
and fixed SVG close controls. Nested widgets consume their own keys; feature
owners retain initial focus and close/edit/review decisions. Mutation editors
retain their specialized keyboard and held-edit rules. Action reviews focus Cancel
while the initial plan is pending, so Escape closes the review before the provider
answers; an opener tooltip cannot take its keyboard input. Resource grids use a
localized accessible name without translating machine IDs or resetting state.

Each journal filter incarnation owns both first-page and pagination replies,
including failures. Changing a filter hides its previous rows immediately;
same-filter refresh retains older pages. Pagination is single-flight. A selected
historical target retains its exact reference when the live target list changes;
it never silently broadens to all targets. Date formatting follows UI language.

The UI is the sole owner of retained regex-search history. A fresh budgeted
worker matches incremental batches without keeping another text history; monotonic
entry IDs permit binary suffix lookup, and obsolete worker replies are rejected.
Compose reverse-network and project indexes exist only inside one immutable
projection, with ambiguous names and ordering preserved. They trade bounded map
allocations for fewer repeated scans, not a universal speed/memory guarantee.
Current view cursors skip row-history scanning; status-only frontend pages retain
their rows-array identity while still publishing status. Independent cursors,
resets, coverage and UID replacement semantics remain unchanged.

### Inventory JSON decoding

The dynamic client retains client-go request construction, authentication, rate
limits, retries, error handling and content negotiation. JSON LIST responses use
one integer-preserving, case-sensitive map decode instead of repeatedly parsing
the envelope and each item. Typed-list item kind/version inference and pagination
metadata are retained. Noncanonical envelope field aliases fall back to the
upstream serializer. GETs, mutations, watch framing and non-JSON serializers keep
the upstream path. This reduces decoding work; it does not omit object fields
from the received response or replace cache projection/admission checks.

## Local learning boundary

`web/src/learning` owns locally shipped resource explanations, the deterministic
introduction and five authored task modules. It registers no target, provider,
API or agent capability. Both lesson reducers have no transport or Client
dependency; their UID-scoped objects exist only in renderer memory. Path actions
carry a module/generation/step token; reset and task advancement reject stale
actions. Each task gates repair on inspected evidence, diagnosis and an authored
field change, then gates advancement on result inspection. Independent stages
use neutral per-module symptoms, change names/values and case order, disable hints
and require selecting decisive observations. Supporting choices separate supplied
scenario observations from API paths and log/event excerpts. Wrong-repair feedback
is keyed to the selected bounded change; all copy is local in eight languages.
Per-module state and the
introductory completion flag belong to the open library and disappear on close.
Shared ResourceTable and read-only YamlView retain familiar navigation without
exposing live actions, comparison pins or writes. Secret snapshots contain key
names only. Neither authored success nor task completion is a live verification
or a measured learner outcome.
See [learning](learning.md) for coverage, reset and validation boundaries.

## Reviewed node terminals

`ActionDescriptor.terminal` selects the optional `TerminalActioner` capability.
Only a single-object, `NoAgents` UI action can use it. `RunAction` validates the
reviewed connection revision, obtains an app-owned `ExecHandle` and returns an
opaque `TerminalOpen.preparedId`. It performs no remote write. The UI adopts
that handle through the existing `ReopenTerminal` and one-shot stream protocol;
its discarded StrictMode mount cannot consume the handle. Prepared terminal
IDs are memory-only and are never restored or replayed from workspace persistence.

Kubernetes Node Shell signs node Ref/UID, namespace/UID, image, helper name,
nonce, session incarnation and expiry. Admission consumes the nonce before
registration. A connection-snapshot pool shares one helper for a node UID across
Shell and Files consumers; only its owner performs one non-retrying CREATE.
A one-shot terminal prototype transfers its reservation to its active stream,
so evicting the prototype cannot cancel that stream and an ended tab cannot
retain an otherwise unused helper. A shared atomic
start guard still prevents replaying a terminal command. The helper has no mounted API token, uses the selected
nodeName and host namespaces, runs privileged as UID 0 and has a one-hour active
deadline. After bounded startup, the existing exec transport runs `nsenter` into
host PID 1's mount/UTS/IPC/network/PID namespaces, root and working directory,
followed by `/bin/sh -l`.
Node identity is checked again after startup; normal terminal credits, input,
resize and transport guards apply.

After the last consumer closes, a generation-guarded 5-second idle timer requests
cleanup; a new consumer cancels that timer. The one-hour helper deadline never
resets. Startup failure and normal application shutdown bypass idle grace.
Cleanup runs under an independent ten-second deadline, including after startup
failure or an ambiguous create result. The create response pins the server-assigned Pod UID; even a replacement with
copied ownership labels cannot inherit cleanup. For an ambiguous create without
a returned UID, the random ownership marker and nodeName identify a helper before
a UID-conditional DELETE. CREATE never retries; persisted cleanup may retry only
against the owned helper UID. A read confirms
disappearance, so admission-added finalizers do not become silent cleanup
success. Foreign/replaced Pods are not deleted. A failed cleanup retains a pool
tombstone until a read proves absence or a different UID; a fresh launch cannot
overlap unfinished cleanup on that connection/node. Active consumers can receive
a cleanup failure notice; cleanup after idle expiry has no remaining listener and
uses the silent durable recovery path. `ForgetTerminal` revokes its consumer
without waiting through idle grace.
Application logs retain a warning with target/helper identity, without command
output or credential details. Unavailable API/kubelet, process
crashes and revoked rights can leave a helper; the active deadline bounds its
execution when enforced, not object retention. Kubernetes exec and nodeName placement have no atomic Node UID precondition;
identity reads reject observed replacements but cannot eliminate the final
read-to-exec race. No automatic reconnect, node policy mutation, agent grant
expansion or SSH client is introduced.

Agent exec preparation carries an internal, non-JSON caller marker. The Kubernetes
provider refuses Node Shell helper Pods by their reserved name or ownership
marker, even through broad namespace-wide container exec grants. Editing helper
labels does not make a reserved helper name eligible for agent exec. UI terminals
retain their ordinary container behavior; this does not add an agent host-shell
permission.


## Reviewed node file sessions

A UI-only FilesActioner admits a signed, single-object Files review into a bounded
memory registry; RunAction performs no remote writes. Its preparedId is required
for every node file operation and download, is bound to the reviewed Ref/UID,
and is never persisted. The ordinary node Exec capability remains unavailable,
so existing agent grants do not acquire host access. The signed helper grant
includes the action ID; terminal and file reviews cannot be exchanged.

The provider shares Node Shell startup/identity/cleanup logic and its pooled
helper. The first consumer starts it lazily; individual file commands run bounded
raw exec through nsenter into the host root. Each Files session holds one lease
and has its own command cancellation. Closing it does not cancel another consumer.
It retains the reviewed connection snapshot and rechecks node, namespace, Pod UID,
ownership marker and node owner on each command. Failed helpers are not recreated
automatically. Closing or registry expiry (one hour) cancels its own commands and
releases its lease; actual deletion follows the last consumer’s idle grace. StrictMode discarded mounts do
not revoke a live file inspector. Late/unadopted action results are released.

Shared Files create uses a pinned connection revision, argv path operands and
POSIX shell noclobber; it refuses existing entries and returns a versioned empty
document. The UI protects unsaved edits and updates its directory cache after
confirmed creation. Regular reads, writes, downloads and their existing limits
remain shared across container and node file inspectors.

Rename first reads a digest of lstat-style device/inode/type/size/timestamps.
The confirmed operation compares that digest and the connection revision,
refuses an existing destination, then invokes a tiny libc-free Linux amd64/arm64 program with
`renameat2(RENAME_NOREPLACE)`. Paths stay argv operands. The readable assembler
sources and deterministic generator live beside the embedded programs; tests
check ELF architecture, source hashes and a non-executable stack. The shell
stages the helper using ordinary BusyBox/POSIX tools, opens and unlinks it,
then executes its inherited `/proc/self/fd/3`. No installed utility remains.
An unsupported syscall, architecture or noexec temporary directory fails safely;
there is no plain rename fallback. This avoids BusyBox mv -n's verified
check-to-rename destination race. Directory caches and any open
document paths are rekeyed only after success. There is no atomic remote source
inode precondition across the final stat-to-move interval; an external filesystem
change can still race it. Failures/unknown outcomes are never retried implicitly.

## Shared helper admission

A ready pool entry is probed against the actual API object before returning an
`ActionPlan.reuse` hint. The signed admission includes the helper generation;
RunAction rechecks node/namespace/Pod ownership and reserves that exact entry.
A stale reuse token never falls back to CREATE. Read-only preparation neither
retains a helper nor extends idle grace. A helper's namespace/image stay fixed
until its consumers close and cleanup is confirmed. Separate connection snapshots
and node UIDs cannot inherit one another's helpers. At most 128 helper entries
are retained by one connection, including cleanup tombstones.

## Durable helper recovery

SQLite owned_helpers stores non-secret intent before CREATE: provider/target,
namespace UID, helper name/random marker, node owner UID and creation time. The
server Pod UID is pinned before any host command. A cleanup record is removed
only after confirmed disappearance (or proof the same-named UID is a replacement).
A namespace UID mismatch retains the record for the original cluster; it never
turns a repointed kubeconfig into permission to delete elsewhere. No credentials
or configuration hashes are stored.

The app injects a HelperJournal into tracked terminal/file handles before they
are registered. All consumers of a pool entry share one durable owner lease,
which remains active during idle grace. Graceful shutdown cancels those owners
and waits for their bounded cleanup/Finish before closing storage; no late Begin
can create a helper after journal shutdown. Leases renew once per minute for five minutes; a failed renewal
cancels host access. The current process also excludes active helpers from its
reaper, including across a wall-clock jump. Recovery claims an expired lease by
an atomic SQL condition before work; stale owners cannot renew or pin another UID.
The reaper runs off the UI thread, wakes on explicit connection readiness and
otherwise scans local due metadata every 30 seconds. It uses only current explicit
sessions and cancels with their lifetime. No discovery, separate credential resolution,
implicit connection or UI notification is triggered by recovery. At most four
due helpers per connection are attempted in one pass.

On failure next_try = attempt completion + delay_ms, then delay_ms doubles to
86,400,000 ms. The initial delay is 60,000 ms. Backoff persists across restart
and reconnect, with no jitter or replay of missed intervals. UID-conditional
DELETE is followed by a read; an ambiguous result remains pending. CREATE ambiguity
without a returned UID is recovered only by the recorded namespace/marker/node
owner, and the observed UID is persisted before deletion. A crash in that CREATE
response window cannot provide a server UID that the client never received.


## Local file reveal and Files deletion

Local download and source-configuration reveal share `internal/filemanager`.
Linux sends the escaped full file URI to `FileManager1.ShowItems` on the existing
session bus and waits for acknowledgment; it never autostarts a bus or silently
falls back to an unselected parent. macOS and Windows retain item-selection
arguments. Linux/macOS selection requests carry a five-second deadline; Explorer
is dispatched and reaped without tying its lifetime to the request. No shell
interprets a path.

Shared Files deletion reads the same stat digest as rename into a review,
requires the original connection revision and source digest, rejects root, and
passes the normalized absolute path to `rm -r --`. Symlinks are unlinked without
following their targets. The source check cannot atomically exclude changes
between stat and removal or snapshot recursive contents; errors may leave partial
results. The UI permits no repeat submission after failure until a fresh review.
Cancel initially receives focus, dirty editors use mayLeave, and cached branches
and affected documents are removed only on confirmed success.


## Detachable Files and Shell views

Files uses ephemeral bottom-panel tabs for every supported target. A FilesHost
owns one reviewed capability and closes it only on logical tab removal or owner
page exit. The dock and guest window transfer a memory-only snapshot containing
container identity, captured revision, cached tree, open document/base version,
draft and CodeMirror state/history/selection/scroll. Editor callbacks are bound
to document path and generation, so an old unmount cannot overwrite a replacement
draft. The guest does not acquire
another helper consumer or reread the document on transfer. Dirty guards are
scoped to the tabs actually being closed; group closure captures exact tab IDs,
flushes detached state and refuses an in-flight write. Browser capability close
uses keepalive on pagehide. Snapshot transport is bounded to16MiB and coalesces
pending snapshots until the receiving renderer acknowledges processing; oversize transfer fails visibly without discarding the source.

A TerminalHost permanently retains its TerminalView, xterm parser and credit-based
WebSocket. A guest terminal gets a bounded xterm serialize0.14.0 snapshot, then
ordered output frames; server credit is returned only after both parsers consume
the frame. Only the visible parser may generate PTY replies: the hidden owner disables
xterm stdin while detached, so DA/DSR/OSC queries are not answered twice. A normal
return flushes the guest parser before restoring owner input. Guest closure
releases pending mirror acknowledgments. Input/interrupt
and resize route to the original owner; hidden owner geometry never overrides the
guest. The published MIT addon matches xterm6.0.0, is integrity-verified and vendored
without dependency/lockfile mutation; the license collector includes its upstream
license. Untrusted terminal clipboard/link/title behavior remains disabled.

The UI-only native LogWindows service also admits whitelisted files/shell routes,
sharing the existing eight-window cap. Shell uses normal960×600 sizing bounded
by logical work area; no maximize/fullscreen operation exists. A synchronous Wails
WindowClosing hook waits for guest state/input flush before approved closure.
Per-opening epochs reject messages and close requests from an older window.
Wails events and browser BroadcastChannel carry memory-only data; owner page exit and the browser opener lifetime end orphan guests. No tool window is exposed through agent grants.

## Native Argo CD workspace

`provider.ArgoSource` provides the UI-only `ArgoCD` API snapshot over the current
explicit session. Six virtual Argo kinds are conditional on discovered Applications;
`ocular.argocd` retains its stable ID as Dashboard in the standalone Argo CD group. See [Argo CD](argocd.md) for projection/read bounds and semantics.
The projection excludes inline values and credential objects; no Git or remote
cluster connection is made. Argo status resource refs are resolved explicitly
against the catalog and selected namespace, then pinned to a fresh UID.

The four exact v1alpha1 Application actions reuse Prepare/Run, are NoAgents,
and send one resource-version-guarded write. Sync pins comparison revisions and
binds original spec/operation state along with UID, route and configuration.
Canonical comparison permits only known omitted typed defaults; opaque values
and unknown fields remain significant. Server acceptance is asynchronous. The
frontend reuses shared snapshot/loading, grids, operation reviews, edit guards
and forwarding; snapshots are not persisted or silently widened.

`ArgoRequest.settings` is empty for ordinary controller observations, or explicitly
`repository`/`cluster` for one settings page. `ArgoSettingsSource` performs a
bounded, per-namespace, per-kind label-selected Secret list and decodes only the
connection descriptor whitelist. Secret payloads/config never cross this API or
enter persistence/controller evidence. Invalid kinds are refused before opening
or reading a session. The original controller snapshot still makes no Secret read.

Current resource DTOs can expose parsed self-tracking metadata without another
inventory request. Reverse manager links require current resource UID, local
Application destination and exact reported membership. Generic labels alone,
unknown installation identity and ambiguous matches do not authorize a link.
An eight-entry per-Client memory cache shares only ordinary controller projections
for an exact connection incarnation and namespace selection. Held editors cannot
relabel retained evidence as belonging to a replacement connection.
