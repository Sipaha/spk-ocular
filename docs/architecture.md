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
names and display names; session creation rejects locked records. Connect alone
opens the unlock prompt; a successful master entry unlocks every encrypted record
for the process lifetime. Reset uses a reviewed registry revision, deletes only
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

## Live tables and discovery

Events invalidate state; they are not an unbounded event journal. Each subscriber
keeps the latest event per key. Overflow and reconnection cause `resync`.
`OpenView` returns a unique, never-reused view ID. `GetRows(since)` returns an
atomic versioned delta or a complete reset, including an explicitly empty reset.
The frontend advances its cursor after applying data, keeps one pull in flight
per view, and ignores responses from closed or replaced views.

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

The cache stores reduced objects, omits Secret/ConfigMap values from list caches,
and attributes metrics only to matching live UIDs. Background caches use bounded
retention. View lifecycles are keyed by immutable requests and session ownership.

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

UI logs and terminal traffic use a token-protected loopback server in both app
modes. They never stream through the `wails://` asset transport. Stream IDs are
one-shot and bound to session ownership. Guards run before consuming an ID.

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
Column resizing uses inherited CSS variables, one DOM update per animation
frame, and commits React/persistent state on release.

The palette filters current/recent objects by the target's selected scopes,
including on unscoped tables. It opens the object's kind and selects a row by
full Ref + UID once that row arrives. A one-shot request cannot reselect a row
later after an unrelated manual selection.

## Visual and performance constraints

Sidebar provider headings use 14 px semibold text, 16 px icons and a 10 px bottom
inset before their rows; the subordinate
Add kubeconfig action uses muted 12 px text.
Use graphite surfaces, system fonts, 14 px text, a 17 px root rem, 32 px table
rows, 40 px headers/toolbars, 14 px semibold resource-page titles, and mostly
2/4 px radii. The SVG app icon embeds as
256×256 PNG. Desktop and installer icons derive from the website’s optical-eye design,
with heavier aligned outlines and fewer details for small taskbar sizes. The
SVG is rasterized at four-times each output size and downsampled with
Catmull-Rom for smooth edges. GTK supplies exact
16/24/32/48/64/128 px representations as one icon family so panels choose their
own size. The combined ARGB property stays below the X11 request limit; simply
embedding a 512 px PNG can leave the GTK window without a taskbar icon. The resource list reserves 24 px for GTK overlay-scrollbar hit areas.
Connection details remain a header-button modal; the old Overview page is absent.

`@theme static` preserves runtime ANSI/xterm colors. CodeMirror and xterm are
lazy-loaded. Each entry JS chunk has a 300 KiB gzip ceiling enforced by the build.
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
