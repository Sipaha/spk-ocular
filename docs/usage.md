# Using SPK Ocular


## Connection switcher and appearance

The header starts with the selected connection's icon, name and connection-state
indicator. The connections button opens a searchable list grouped by provider;
there is no permanent connections column. Search receives focus on opening;
arrows move the cursor, Enter selects, and Escape first clears a query, then
closes the picker and returns focus. Selection closes the picker before any
unsaved-edit question. It never connects an unopened target. The selected row uses a filled background
and left accent independently of its green/yellow/gray 10 px connection-state indicator.
Source/discovery problems are available in the picker and flagged on its trigger;
pending agent requests also remain visible in the header. If the original search
has no matches, the picker also tries the existing Russian/Ukrainian physical-key
recovery to English and shows the corrected query. Real original-layout matches
take priority; searching does not connect or fetch resource data. A nonempty
query immediately highlights the Enter candidate, without requiring an arrow
key; the actual selected connection retains its left accent. There is no
redundant check mark. Provider
headings use muted text rather than competing with connection names.

Click a connection icon in the header or picker to change its appearance without
selecting or connecting it. Clicking a row icon keeps the picker open beneath the
appearance dialog, preserving its search and scroll. Save or Cancel returns focus
to that icon. The background picker is inert while the dialog is open. The picker
uses 36 px icons beside its two text lines.
Each row's **⋯** menu retains appearance, source and connection actions. Counts
sit beside provider names. The **Kubernetes** heading has two direct buttons on
the right: a file with an arrow for **Import kubeconfig files**, and a file with a plus
for **New configuration**. Both remain reachable by Tab in long lists. Disconnect
precedes the final, separately grouped Remove action. Context-menu pointer and
keyboard navigation share one active highlight. Connection
information remains available through the separate header info button. A neutral
application menu on the right contains language, About and keyboard help; the
workspace header does not display the application logo or name. Native window
and taskbar identity are unchanged.

**Connection appearance** offers 32 vector icons and sixteen curated icon/background
pairs. There are no arbitrary colour inputs. Each preset defines readable light
and dark variants; the current application uses the dark variant. New and existing
connections without valid saved appearance receive a random initial combination,
avoiding combinations already loaded in the same profile where possible. The
choice is saved once in local per-target UI state, independently of kubeconfig,
credentials and connection sessions. Renaming a connection does not change its
identity or appearance. Closing without Save discards the appearance draft;
persistence errors are shown with a retry action. The appearance dialog waits for
the stored choice before enabling Save, so a slow read cannot overwrite it with
a placeholder. Arrow keys move within each
appearance choice grid, Home/End choose its first/last option, and Tab moves
between the selected icon, selected color and dialog actions. The Color grid has
two rows of eight presets; up/down arrows move one row. Icon choices fill their
tiles while retaining the narrow-window layout.

The connection picker has hover hints only for icon actions, not connection text
or section titles. Header controls retain hints, rendered
with the application panel colors rather than platform tooltip colors, using
14 px text and a shared 800 ms delay for text hints and icon actions. Resource
table cell values have a hint only when the hovered cell actually clips its text.
Health reasons belong to the status dot, never the whole row, so native inherited
row hints cannot overlap cell hints (including Message in object Events).
The resource header shows the provider’s singular kind label beside the object
name on the same text baseline, retaining the technical kind ID in its hover hint.
Overflow is measured only for that cell before showing the hint; resizing does
not scan the table or install per-cell observers. The resource toolbar omits the
repeated section title and count; the selected navigation entry identifies the
section. Its heading and count remain available to assistive technology.
Resource navigation disclosure chevrons sit 1 px left of their layout position;
labels and click targets retain their alignment.

## Start and connect

The desktop application opens one window and exits when that window closes.
It has no tray process. Choose a Kubernetes context or Docker connection in the
header connection switcher, then press **Connect** in the resource-list area. Selecting a target
or restoring the previous selection never starts a connection. Discovery reads
local configuration without contacting servers. Explicit connections remain
open while switching targets, reading Helm charts, editing values, or minimizing
the window. Use **Disconnect** in the target context menu to close a connection.
Idle timers do not disconnect explicitly opened targets. Temporary watch/feed
failures reconnect automatically while retaining cached rows. Configuration changes automatically check a replacement session for the
same target. Connect is needed again after failed recovery, a restart, or an
explicit Disconnect. Background authentication that cannot proceed without a
person is a recovery failure, not an idle timeout.

During connection, the page shows the actual phase, elapsed time, attempt number,
last error, and time until a retry. Temporary availability failures get at most
**three total attempts**. Authentication and configuration errors stop immediately.
**Cancel** stops both an active attempt and the wait before a retry, including
session-owned credential helpers. After cancellation or failure, Connect starts
a new sequence. Configuration changes retire the old pending check or connected
session, then automatically check the replacement configuration for the same
target.

`spk-ocular --browser --port 5190` uses the ordinary desktop executable to serve
the same interface on loopback HTTP instead of opening a window. No separate
browser binary is required. Native runtime libraries remain required; browser
mode does not need an X/Wayland display on Linux.
Use the local URL printed at startup. The browser interface requires its
per-process page token and does not accept remote Host values.

Connection information is available through the information button next to the
connection name in the header. Closing that dialog returns focus without
resetting the resource table, its filter, or an unsaved editor. When switching
objects, the detail layout stays visible under a loading overlay until the new
response arrives; controls for the previous object cannot run during that wait.

## RBAC explanation

On a connected Kubernetes target, open **Access Control → RBAC explanation**.
Inspect the current connection identity or a ServiceAccount, follow binding/role
provenance and filter declared permissions. **Find declared grant** searches the
snapshot; **Check my access with the server** asks for the current connection's
actual authorization, without impersonating the selected account or executing
an operation. Missing sources never imply missing permissions.

ServiceAccount properties offer **Permissions**, and a Forbidden resource read
offers **Check this Forbidden**. Permission inspection preserves open YAML drafts;
source navigation uses the standard edit guard. See [RBAC explanation](rbac-explanation.md)
for namespace boundaries, limits, identity and review semantics. Available from 1.2.0.

## Security

On a connected Kubernetes target, open **Cluster → Security**. Review read-only
configuration risks and existing Trivy Operator CVE reports, filter results and
open their resource or report in the standard inspector. Each finding explains
its observed fields and recommendation. Missing reports show what to install
and which read permissions are needed; Ocular installs nothing automatically.
Configuration findings are declarations, not an exploit or compliance verdict.
See [Security](security.md) for checks, setup, coverage and limits. Available from 1.3.0.

## Events timeline

On a connected Kubernetes target, open **Cluster → Events timeline**. Inspect
warning and normal event series on a time axis, filter by resource kind/time,
and search workloads, reasons or messages. Select an event and use **Open
resource** to access the standard properties and logs; known ownership ancestors
can also be opened. **Refresh** reads a new snapshot and preserves open resource
edits. Older Kubernetes events may already have expired, and incomplete access
or size caps are disclosed. Bars show first/last observations of cumulative
series, not continuous failure duration. See [Events timeline](events-timeline.md)
for coverage, limits and evidence semantics. Available from 1.2.0.

## Cluster graph

Graph inventory loading requires an explicit Load graph click once per backend
connection session. Opening the graph before that click creates neither an inventory
request nor a layout worker. Navigation and warm target switching retain this choice;
Disconnect/Connect, a replacement session and application restart require a fresh click.
The choice is memory-only, never a saved target preference. Automatic replacement retains
a guarded unsaved editor until its edits are released, then requests fresh graph admission.

On a connected Kubernetes target, open **Cluster → Cluster graph**. The same
namespace picker can select one, several or all namespaces. The canvas includes
discovered readable resources (including custom resources and retained objects);
cluster-scoped objects form a separate region. Empty explicit namespace sets do
not widen to all namespaces.

Drag the background to pan, use the wheel or zoom buttons to zoom, and choose
**Fit graph** to return to the overview. Search finds resources across the chosen
namespaces. Arrow keys move through resources; Enter opens the focused object.
Clicking a node opens the standard right-hand details/YAML panel with its usual
tools. Unsaved edits remain protected when selecting another object or leaving.

**Route layer** overlays directed, animated declared routes on the same graph,
without changing layout, camera or selection. It represents Ingress/Gateway
routes to Services and Service selectors to Pods, not observed network traffic.
No collector is needed; no packet counts, throughput or latency are inferred.

The graph is a snapshot: **Refresh** rereads it while retaining the camera and
selected UID. It does not open permanent watches for every resource type.
Denied/error sources and unfinished discovery are shown as partial coverage.
A 30,000-node / 120,000-edge cap is explicitly reported; narrow the namespace
selection if the cap is reached. See [graph architecture](cluster-graph.md).

## About the application

Click the **About** button next to the keyboard help in the top bar. It shows the running
build version, Apache 2.0 license, product website, source repository and author
name and profile link. Employment and biography are kept on the author website.
Product and author links use language-neutral website roots; each website
selects its language from its saved choice and browser preferences. Desktop links
open in the system browser; browser-mode links open in a separate tab. Escape,
the close button or backdrop closes the dialog and restores focus. Opening it
preserves workspaces, connections and unsaved edits.

## Language

The header language button shows a globe and the current native language name.
Its menu offers the eight supported languages.
The saved choice works in desktop and browser mode, including when browser
storage is blocked. Before a manual choice is saved, desktop mode follows a
supported system locale and browser mode follows the first supported browser
preference. English is the
fallback. Changing language preserves open workspaces, editors and connections.
External data and errors may remain in their original language; see
[localization coverage](localization.md).

## Kubernetes configurations

First use of the configuration registry opens an import dialog, including after
upgrading an older installation. Candidates come from the `KUBECONFIG` file list
and regular kubeconfig files directly in `~/.kube`. `KUBECONFIG` is a list of
file paths, separated with the OS path-list delimiter; it is not a directory
setting. No candidate is selected automatically. Dialog actions share one footer: cancel/skip
on the left and the primary action on the right; narrow windows stack actions. Skip dismisses onboarding
persistently. Neither scanning nor importing contacts clusters or runs credential
helpers. Docker discovery is unchanged.

**Import kubeconfig files** imports local files; **New configuration** opens a YAML editor. Local imports
are links, not copies: external edits are observed, new unselected files remain
excluded, and originals are never modified. Selected KUBECONFIG files retain
first-wins merge order; standalone files stay independent. Context IDs preserve
the existing primary/file identity domains so saved selections and grants remain
associated with the same targets. The `current` badge is no longer displayed.

Before creating a custom configuration, create a non-empty master password
and confirm it. Its length and strength are your choice. Ocular encrypts the YAML with AES-256-GCM and a key
derived using Argon2id. It does not store the password. After restarting, click
**Connect** on a protected connection to enter the master password. When adding
custom YAML, **New configuration** asks for that password directly and continues
to the editor without connecting. This also works when a master password exists
but no encrypted configuration has been saved yet, or the last one was removed.
Cancelling password entry leaves the registry unchanged. Selecting a row never
asks for it. One successful password entry unlocks all encrypted configurations.
Connect then continues its selected connection; New configuration only opens its
editor. Linked files remain available without unlocking. The key is kept in
memory until Ocular closes. There is no password
recovery or OS-keychain integration. Cancel closes the password dialog without
connecting or unlocking. **Reset master password** is available in that dialog
and the Add menu; its separate review lists all encrypted configurations and
warns that they will be permanently deleted. Cancel receives initial focus;
Enter on Cancel closes the review and preserves the encrypted configurations.
The dialog owns keyboard input without selecting a target behind it. Confirming
removes the encrypted records and master key, closes their sessions, preserves
linked external files, and allows creating a new master password. Keep an independent copy of your configs:
losing the password makes the stored YAML inaccessible.

Custom configs require a name and valid kubeconfig YAML with at least one context.
Use inline certificate/key data or absolute external paths; relative file paths
are rejected because pasted YAML has no source directory. Credential-helper
commands must be on PATH or use absolute paths, and execute only on Connect.
External files and helper-managed caches are outside Ocular's encryption.

The two heading actions open dialogs directly. **Import kubeconfig files** scans import
candidates; **New configuration** opens master-password setup/unlock and then
the editor. **Reset master password** belongs only to the locked-vault unlock
dialog, next to password entry; it is absent from file import. If a fresh
status contains no stored configurations, Reset skips the deletion review and
resets the empty vault directly. The exact fresh registry revision still guards
against concurrent additions; failures are shown without retry. When stored
configurations exist, their names and deletion confirmation remain required. There is no
separate configuration-management list. Tab moves between the direct actions;
Enter or Space activates one without selecting a connection behind the dialog.

Right-click a Kubernetes connection, including the selected one, to **Edit kubeconfig**, rename the configuration or remove it. With a connection row focused, Shift+F10 opens its menu. The header
switcher also offers Shift+F10 for the selected connection. Linked files also offer
**Open in file manager**: Linux opens the parent folder; macOS and Windows select
the source file. This action runs on the computer hosting Ocular, including
browser mode, and reports launcher failures beside the add action.

The source editor provides syntax highlighting and search and uses nearly the full window; a compact header shows the source
name, path and mode, leaving the remaining area to YAML. Editing starts
from the selected source; saving requires explicit confirmation naming the external
file or encrypted configuration. Changes affect all contexts using the source.
External file updates are atomic, preserve the file's permissions and symlink,
and reject changed content. Encrypted updates keep the entry ID and use a fresh
nonce. Stale source or registry revisions refuse the write. Credential helpers
are not run by opening or editing a source. YAML is held only in the open dialog's
memory, never in preferences or SQLite. Cancelling an unchanged editor closes it;
cancelling a changed draft asks before discarding it. A save review can be
cancelled without losing the draft.

Rename opens with the current name focused and selected. Password and creation
dialogs focus their first input; the source editor focuses YAML.
Rename changes the exact visible name of that connection, preserving context
IDs, source file names, YAML context keys and grants. Other connections from the
same source retain their names. Display names persist across app restarts. Remove requires confirmation; it unlinks
an external file without deleting or editing it, or deletes the encrypted copy.
Affected sessions close after source edits or removal, and resource edits must
be saved or discarded before these operations. Encrypted connections remain visible in the connection picker with a lock until unlocked
through **Connect**. Inspection/editing becomes available after unlocking.

**Disconnect** is offered in a connected target’s menu, including the selected
one; it is absent for disconnected targets. Disconnect keeps the selection and
checks unsaved resource edits before closing. In-flight agent calls finish
independently; their held session closes afterward.

An active connection has a green dot; connecting/cancelling uses yellow and
disconnected targets use gray. Cancel updates the attempt
state without waiting for provider cleanup; cleanup remains tracked by the app
and cannot admit a late connection or start another retry.

The registry is an owner-only `configurations/kubeconfigs.json` file in the
Ocular data directory. It contains linked paths and names, encrypted custom YAML,
context names for locked rows, and versioned encryption metadata. Configuration
and context names and linked paths are not secret. Older records initially show
a source placeholder; their context names are populated on the first successful
unlock. With multiple contexts, select the desired connection after that first unlock. Passwords and YAML are never stored in UI preferences or SQLite; the
configuration-management API is UI-only. Encryption protects files at rest,
not against another program running as the same OS user or reading unlocked
process memory. Existing agent grants are not expanded by an import.

## Resources and namespaces

The resource navigation groups built-in Kubernetes kinds and discovered API
resources. A resource search matches names, aliases, groups, and kind IDs.
Enter opens the only matching kind. This search is independent of the row filter.

Click a namespace row or its text, or press Enter, to select exactly one
namespace and close the picker. Use checkboxes to add or remove names from a
set. Picker checkboxes use the same dark outline and selected mark as resource
rows. Space toggles a checkbox when the list has focus; it types a space in the
search field. Removing the last checkbox selects an empty set, not all namespaces.
Choose **All** explicitly to view all namespaces.

A table keeps the complete inventory of the requested kind and chosen namespace
set, so row filtering and sorting by inventory fields cover the whole list. Loading is
automatic; there are no user-facing pages or Load more controls. Opening a table
does not open the full description of every Kubernetes row. Resource details are read when
an object is opened. CPU/RAM are separately sampled metrics: their existing
sort uses the samples available when sorting is selected, with unknown values
last; it is not a complete cluster-wide CPU/RAM ranking.

For a large namespaced collection, **All** can stop with a request to select
specific namespaces. The table then shows no partial successful inventory.
Choose one or more namespaces above to load their complete lists. This protects
against accidentally loading a very large cluster-wide collection; it does not
automatically change your selection. The current admission budgets are described
in [Architecture](architecture.md#live-tables-and-discovery).

Live updates normally keep lists current. Use the circular-arrow icon to force
an update; it spins until that update finishes. F5 refreshes the resource view;
F5 in navigation refreshes the resource-kind catalog. The namespace popup has
the same action beside its search field and preserves the query and selected
names. During a live-table refresh, old rows remain until the new snapshot is
ready. A failed read keeps them visibly stale; an All admission failure clears
them and asks for a narrower scope. Refresh preserves filters, sorting and held
editor drafts.

While a resource list or an object's Events list loads, **Cancel** stops that
observation. The loader shows total elapsed time and consumed inventory response
bytes when the provider can measure them; an em dash means unavailable, not zero.
After cancellation, **Request cancelled** and **Retry** replace the list. The
placeholder survives navigation, warm target switching and frontend reloads
until an explicit Retry or a new connection. Cancelling one observation does not
stop other consumers of the same cache. When none remain, an unfinished inventory
and its HTTP reads stop immediately. A pending discovered-type schema probe can
also be cancelled. Retry preserves the page's filters and held editor drafts.

If permission to list namespaces is unavailable, type names separated by commas.
Submitting an empty input explicitly selects All. Namespace selections belong
to each connection and survive switching connections and restarting the app.
Resource reads for an explicit set stay within those namespaces; failures in
one source are visible while accessible sources remain usable.

While initial resource results are loading, the table remains mounted with its
known column headers and one loading state in the row area. Server-defined
columns appear as soon as their schema is available; unknown columns are not
invented. Coverage summaries appear with observed data or a settled result. Already displayed rows and source failures remain visible
while other sources load or the view refreshes; progress moves to a small toolbar
indicator. Sources still loading are not warned as unavailable during that read.

All resource sections, including Favorites and API subgroups, can be collapsed.
Click a heading or use Enter/Space and Left/Right. Their expansion state is
shared globally and saved in the database. With no saved choice, only Workloads
is expanded; Favorites and all other sections start collapsed. Saved choices
continue to take precedence. The current kind remains visible
inside a collapsed section. Search temporarily expands matching sections and
restores the saved state when cleared.
Resource navigation rows use the standard arrow cursor, including draggable favorites.
Section chevrons are centered SVGs rather than font glyphs; direct child labels
align with their section title, and nested children align with the subgroup title.

## Favorites and saved layout

Favorites are global across connections and identified by provider and kind.
Add or remove them with the star or context menu. A favorite appears only in
Favorites; removing it returns it to its original catalog position. Unavailable
kinds remain remembered and reappear when a connection serves them again.
Drag items to reorder them, or use Move up/Move down in their keyboard menu.

Resize columns by dragging their boundary. Arrow keys change width, Shift uses
larger steps, Home/End select the limits, and double-click resets a width.
Primary table widths are stored per connection, kind, and column ID. Nested
detail tables retain widths only while mounted.

The last kind, scope, row filter, sort, cursor, and open details are restored per
connection. Row checkmarks are intentionally not restored. Log and terminal
tabs survive switching connections. From 1.2.0, explicitly pinned
references and ended terminal slots also survive restart; actual contents and
commands do not. See [workspace continuity](workspace-continuity.md).

## Search and keyboard

Ctrl+K opens the command palette. It searches kinds, connections, scope names,
and current table rows. History only raises the priority of matching live results;
it never introduces absent, deleted, replaced or out-of-context resources. Object
results normally remain in the selected namespaces, even while the current table is
unscoped. Global objects such as Nodes remain available. An explicit three-part
query, `namespace type name` (for example `kube-system pods coredns`), searches
only that one namespace and resource type. Other providers use their own scope
names, for example `green cr gamma`. The selected namespace changes only after
choosing a result, atomically with its view/object and behind the unsaved-edit
guard. Existing type-first queries and colon commands keep their meaning; the
name part can contain several words. Keyboard-layout recovery preserves the
explicit namespace/type prefix. Ambiguous types offer a choice without fan-out.

Opening an object selects its kind, opens details, and selects/reveals its exact
row when available. The revealed row's table receives keyboard focus, so `L`
opens its logs immediately for kinds supporting logs. Ordinary queries preserve
the namespace selection; an explicit namespace prefix applies that selection
when the result is chosen. A different UID is a different object, even if its name is identical.

Commands use aliases supplied by the provider, for example `:po`, `:deploy web`,
`:ns team-a`, `:ns *`, and `:ctx staging`. Ambiguous commands require an explicit
selection. Namespace-switching commands remain available outside the current
selection. A query such as `dep model` or `pod model` resolves the first word
from provider aliases, IDs, singular names or an unambiguous prefix, then matches
the remaining name text within that resource kind. When another kind is needed,
the open palette temporarily reads only that one view with the existing namespace
selection. Loading and incomplete/error sources are visible; Enter waits for a
result, and ambiguous kinds require a choice. The temporary view closes on query
change, target/scope change or palette close; it does not connect another target
or search every kind in the cluster. Its open search field uses the text placeholder
without a redundant magnifier icon.

If the original query has no matching results, Ctrl+K tries one
English QWERTY recovery of Russian or compatible Ukrainian physical letter keys.
For example, the Russian-layout keys for `pod model` recover that query. The input stays unchanged and
an eight-language hint names the recovered query. Real original-layout matches
win; fuzzy subsequence matching remains unchanged. Explicit `:` commands are not
rewritten. A recognized kind keeps its prefix and waits for its original read
before recovering the name; loading/read errors are not treated as no matches.
The same single scoped temporary view and UID/edit guards apply. This recovery
is not transliteration, does not guess Latin/IME layouts from the UI language,
and is available from 1.4.0.

Blue focus outlines appear after Tab/Shift+Tab navigation and hide on mouse or
touch input. Selection and hover highlights remain independent of focus.

F6 cycles interface areas. Arrow keys navigate resource kinds and table rows.
`?` opens the current keyboard reference. Ctrl+K inside a terminal belongs to
the shell; use the header search button to open the palette from there.
Shortcuts follow physical keys so they work with different keyboard layouts,
including punctuation keys and CodeMirror history, selection, comments and search
panel commands. Plain text, IME composition and AltGr retain their input behavior.

## Details and changes

The detail header shows the object name before its less prominent kind.
Details include facts, YAML, relationships, and the tools supported by that kind.
Kubernetes supports reviewed edits and actions including restart, scale, delete,
Deployment rollout actions, CronJob actions, node operations, and pod debugging.
Availability depends on kind, permissions, and current state.

Changes use a preview and a version/identity guard. A conflict requires a new
preview. An unknown result means the server may have performed the operation;
check the resource before trying again. Navigation that would discard unsaved
YAML or value edits asks first; cancellation preserves the editor.
Values typed while the initial action preview loads are preserved, including
the caret position in the replica-count field.

Secret YAML uses `<N bytes>` placeholders. Its editor permits metadata changes
only; placeholders cannot be written into Secret data. Real values are read or
changed explicitly in **Details → Values → Change**. Value reads are not stored
in SQLite or included in recent-object records.

Checkboxes mark rows for bulk actions; the cursor controls details and keyboard
shortcuts. Ctrl-click toggles marks without opening details, Shift selects a
range, Space toggles a mark, and Ctrl+A marks visible rows. Filtering a marked
row out removes its mark. Bulk actions prepare and run one guarded operation
per resource, and report each result independently.

The inspector close button uses a centered proportional SVG in a 28 px square,
with the same focus style as its action buttons. Closing retains the normal
unsaved-edit guard.

Resource-inspector actions use compact icon buttons, with descriptive tooltips
on hover and keyboard focus. Accessible names and shortcuts are unchanged.
Tooltips include the right-click setup options for logs, terminals and files.
Only one action tooltip is shown at a time; Escape dismisses it without closing
the underlying panel. The ellipsis opens the object-action menu; a bookmarked
document selects the comparison baseline.

Related resources appear in compact grouped Type / Name / Open tables. Fixed
type and action columns align names across resource kinds; long names have
full-name tooltips. The Open arrow supports mouse and keyboard navigation.
Unresolvable references remain non-clickable rows; cross-namespace rows show
their scope. All three columns fit ordinary narrow inspectors; exceptionally
narrow panels can scroll each table horizontally.

Deployment details include **Deployment revisions**: retained ReplicaSets ordered
by revision number in a compact table, with the current template marked,
creation date (full timestamp in a tooltip), ready/total replicas, container images
and change cause. Long images and causes are truncated with full-value tooltips;
narrow inspectors scroll the table horizontally. The row icon buttons retain
accessible labels and tooltips for YAML and comparison. **View YAML** reads the selected
revision's Pod template. **Compare** defaults to the nearest retained lower
revision number, independent of display order. If no earlier revision remains,
it uses the current Deployment template. The baseline selector still offers the
current Deployment and other retained revisions; YAML-only mode does not fetch
a comparison baseline until Compare is requested. Comparisons omit the controller's pod-template-hash
label. These views are read-only and UID-pinned; permission failures and truncated
history are explicit. Kubernetes may already have removed older revisions under
revisionHistoryLimit. This section adds no rollback control.

## Logs, terminals, and tunnels

Logs show all levels without severity toggle buttons or a default DEBUG filter.
Opening or reactivating logs focuses the visible log viewport. `End` in that
viewport jumps to the newest line and resumes Follow; text fields retain their
usual End behavior. The keyboard reference and Follow tooltip show the shortcut.
Log display toggles and actions use icon buttons with descriptive hover/focus
tooltips; active toggles stay highlighted. Channel, line-limit and time-range
selectors and search/filter inputs retain text. Paired circular arrows reopen a
log stream; the history-clock icon selects logs from the previous instance.
Use text filters and search for any log format. Logs support multiple sources, bounded history, search, filters, copying, and
follow mode. Scrolling up pauses following; Follow resumes it. Gaps, truncated
history, and unavailable sources are shown explicitly. The icon at the right edge
of the toolbar detaches logs into a window or returns them to the main panel;
its tooltip and accessible label describe the action.

Left-click **Logs**, **Terminal**, or **Files** on a workload to choose a Pod
when several are available; one Pod opens directly with its default container.
This explicit selection also applies to shortcuts: workload replicas are peer
instances with potentially different logs and state, so quick tools do not
automatically choose a Running Pod. Container defaults within a concrete Pod
are a separate choice from selecting among workload replicas.
On a concrete Pod, left-click and the tool's shortcut open directly with the
annotated default container if running, otherwise the first running container.
Logs retain the ordinary default for completed Pods with no running container.
The setup dialog offers **All Pods** or **All containers** where applicable.
The log list includes stopped Pods, while Terminal and Files offer running
instances/containers. Variant metadata is prefetched when details load and cached
for five seconds; clicking never inserts a Loading row in the dropdown.

Right-click any of these three buttons for a full setup dialog. Pod and container
selectors stay visible even with one option. Terminal alone includes a Command
field; Shift+S also opens its setup. The former terminal split-button arrow is
removed. Cancelling either picker or setup starts nothing. Files keeps the chosen
Pod and container pinned for the lifetime of the inspector.

Terminal layout dragging fits and sends the final geometry on release, with
identical resize messages suppressed; focus returns from the mouse separator.
BusyBox ash can itself emit a new prompt line after a real SIGWINCH: this is shell
output, not an Enter sent by the UI, and is preserved. Plain physical Cyrillic
keys are sent once; IME composition and AltGr remain handled by xterm.

Terminal tabs keep their connection when you change the selected target. Closing
a Kubernetes terminal sends a hang-up sequence before disconnecting. A connection
configuration change is marked on existing tabs; it does not silently reconnect
a running session to another target.

Port forwarding binds loopback addresses only and uses the actual allocated port.
Tunnels have their own lifecycle and remain open across connection changes.
UDP and non-loopback forwarding are not supported.

## Agents

The Agents dialog manages local API grants, pending confirmations, and an audit
journal. **Agent permissions** beside the namespace/project selector opens this
editor for the current selection. Multiple names have separate cards; All
explicitly includes future namespaces/projects. Opening a card grants nothing:
select permissions and press **Save**. Existing rights in other scopes remain
visible.

The switch beside a namespace/project pauses all agent access to it, including
permissions inherited from All. **Add group** creates another independent set
of permissions: give it a name and configure its permissions. Each group has a
separate switch. Enabled groups add their rights together; one group cannot
restrict rights granted by another. A disabled group keeps its name and settings.
The All master pauses every namespace/project; the cluster switch is independent.
Press **Save** to persist names, switches and permissions in the database.
Re-enabling a namespace restores its enabled groups without enabling groups
that were individually switched off.

Each access group contains Viewing, Changes and expandable Execution categories,
with resource-kind restrictions and confirmation settings per permission.
**Container commands (exec)** grants finite argv commands separately from
resource actions. Select its kinds explicitly; every command requires confirmation
unless that exec permission allows **Without confirmation**. The confirmation
shows the chosen instance/container, exact argv, finite stdin and output/time
limits. Exec can read mounted secrets and change container or host data with
container permissions. Revocation cancels transport; it cannot guarantee that
an already started remote command ends. Existing grants never acquire exec.
The connection instruction is under **Connect an agent**; the API status remains
visible when it is collapsed. The editor scrolls independently from its Save
and Discard controls. Permission and group hints are available on hover.
No access is granted by default. Destructive plans require confirmation
unless the particular grant explicitly allows execution without it. See
[the agent API](agent-api.md) for the transport and permission model.

## Docker containers and Compose projects

Docker connections use your existing Docker contexts and Engine configuration.
Containers is the default view for a new workspace and includes both standalone
containers and Compose containers. Saved workspace selections are preserved.
Select **All resources** to include containers without a project; choosing one or
more Compose projects excludes them. Deselecting every project yields an empty
selection. Standalone containers have no Project/Service value and are not shown
as a fabricated Compose project. Containers with only a service label also do not
create a service without a project label.

Containers support live state, details/inspect YAML, logs, terminals, Linux
CPU/memory metrics and reviewed start, stop, restart and removal. Identity and
stale-review checks also apply to standalone containers. Compose Projects and
Services retain grouping and service-wide operations. Compose files are not read
to infer desired state or create missing services.

The all-resources Networks and Volumes views include unused Engine objects;
project selections show the project's objects and those used by its containers.
Images is an unscoped inventory, including unused images. These inventories do
not add image/network/volume mutation operations. Ocular connects to an existing
Docker Engine; it does not provide a container runtime or virtual machine.

Standalone-container access is currently available through the UI. Existing
agent project grants do not cover it; see [agent access](agent-api.md).

## Helm

The divider between the release/chart list and its details can be dragged or
resized with arrow keys. The target list in agent permissions uses the same
resize controls as the resource navigation, resource details and
bottom dock. Escape during a drag restores the previous size; panel widths
survive navigation and, from 1.2.0, application restarts through the
[workspace metadata](workspace-continuity.md).

Connected Kubernetes targets offer Helm Releases and Charts using the same
resource table and detail-panel components as other sections. The tables support
filtering, sorting and remembered column widths. Ocular embeds Helm
4.3.0; no system `helm` executable is required. Releases respect the existing
namespace selection, including an explicitly empty set. Successful namespace
reads remain visible when another namespace fails; the failed scope is reported.
Refresh is explicit.
The table includes namespace, chart/app versions, revision, status and update time.
Details show user/computed values, declared resources, manifests, hooks, notes and
revision history. Declared resources are not a live health assertion. Resources
recognized by the current Kubernetes catalogue open their standard Ocular
details, where a fresh read establishes existence and the current object UID.

Charts are searchable across configured repositories or within one repository.
The catalogue shows the latest matching version per chart; details let you choose
an exact version before installation. HTTP(S) repositories support username/token,
custom CA and client certificate/key paths. OCI entries use a full chart path,
such as `oci://registry.example/team/chart`; versions come from registry tags.
OCI does not define a universal chart catalogue/search endpoint. TLS verification
is enabled unless explicitly disabled for that repository.

Repositories and the release storage driver are configured in Helm settings.
Secret storage is the default; ConfigMap and PostgreSQL SQL storage are available.
These settings apply across connections. Passwords and SQL connection strings
are kept in an owner-only `helm/settings.json` under Ocular's data directory, not
SQLite; reads return only presence flags. An unchanged saved password is retained
only for the same repository name, URL and username. Explicitly editing the
password field replaces or clears it. Ocular does not edit the user's Helm or
Docker credential configuration.

Install, upgrade, rollback and uninstall first prepare a review. The review binds
the selected connection/configuration, storage, namespace, release content and
chart/values. Execution rechecks the current release; rollback also rechecks the
selected historical revision. Plans are one-shot, expire after ten minutes and
are held only in memory. No automatic mutation retry is performed. A cancelled
or unsuccessful operation can have already changed the cluster: inspect release
history and resources before preparing a new operation.

Installation previews for charts that introduce CRDs render locally with observed
cluster API versions plus the chart's declared types. This mode is explicitly
labeled; server validation is deferred until execution, and lookup results may
differ. CRDs are included in that preview. Other server-validation failures are
not silently downgraded to a local preview.

The editor accepts YAML values, timeout and readiness waiting, hook suppression,
namespace creation for install and history retention for uninstall. Upgrade uses
the explicitly edited values; opening Upgrade starts with the displayed release's
user values. Historical details can therefore be used intentionally as a starting
point. Helm templates with lookup, time or randomness may render differently at
execution even though the chart and values are pinned. Hook/CRD side effects are
not fully represented by the preview. External Helm clients are not locked out
atomically; avoid concurrent operations on the same release.

Values, manifests, chart defaults and hooks may contain secrets. They remain in
the UI/short-lived operation state and are not written into resource caches,
recent-object history, agent grants or the agent journal. Helm API calls are
UI-only: existing wildcard agent permissions do not expose Helm data or actions.

## Container files

Use **Files** in resource details for a resource that supports terminals. The
selected instance/container is pinned for the inspector and displayed
as read-only context in its header, ordered as namespace/scope, source object,
instance/Pod, and container (without repeating the source when it is the instance). Browse the compact filesystem tree, including
hidden entries, or enter an absolute directory path. Expand folders with their
arrow or the keyboard; a single click selects a file and a double click or Enter
opens it for editing. Right-click a folder and choose **Refresh** to reload that
branch without discarding the open editor. A folder's first load displays an
animated child placeholder; subsequent openings reuse its cached list, including
after collapse. Empty folders replace their loading child with **No elements**,
retaining the same row height and aligning with child-entry icons. Loaded folders show their cached direct-entry count
even while collapsed. Only explicit Refresh reloads the list and shows an animated
indicator on the folder row while retaining its children. Opening a file shows
an animated loading state in the editor pane. Double-clicking names selects the
row, never the name text. The tree uses the editor's monospace font.
The compact header bars use explicit 4 px top and bottom padding around their
26 px controls; their total 35 px height includes the 1 px bottom border.
Errors appear in a dismissible overlay above the footer without moving or resizing
the tree and editor. Dismissing an error preserves the open draft.
The inspector occupies the window with 12 px edge margins, up to 1800 px wide.
The status footer is 27 px high, with centered text and a 22 px file-type picker.
Drag the boundary between the tree and editor to resize the tree; the width is
kept while the inspector remains open. The focused separator also supports arrow
keys and Home/End; Escape cancels an active drag. Its hit area sits on the editor
side of the boundary so the tree scrollbar remains usable at the tree edge.
Symlinks have a distinct 14 px SVG arrow; their tooltips show the stored destination. **Go to target** resolves the link
inside the pinned container and reveals its actual file or folder, including
relative links and chains. Unsaved edits are protected before opening a different
file. Broken or inaccessible links report an error. Symlinks are otherwise marked
and followed using the container's normal permissions. Select a regular UTF-8 text file to edit; binary files and
files larger than 2 MiB are refused. Syntax highlighting is selected by filename
and can be overridden with the file-type picker in the footer (or Plain text). Common formats
include YAML, JSON, XML/HTML, CSS, JavaScript/TypeScript, Python, Go, Rust, Java,
C/C++, SQL, Markdown, shell, Dockerfile, TOML and configuration formats.

Save writes back to the selected container; Ctrl+S/Command+S also saves. Unsaved
edits are protected when closing or opening another file. Browsing, expanding
folders and refreshing the tree keep the open draft.
In the desktop app, right-click either a file or a folder and choose
**Download**. A native directory chooser selects the local destination. The
selected file or entire folder is copied with its original name, including
binary files, hidden files and empty subfolders; the editor's 2 MiB limit does
not apply. Existing destination names are refused, never overwritten or merged.
Cancelling the chooser starts no transfer. Downloads read the container's saved
contents, not an unsaved editor draft. The browser UI explains that this native
operation requires the desktop app.

Downloads require `sh` and `tar` in the container. They stream without a PTY and
stage privately on the destination filesystem before publishing the complete
result. A failed transfer removes the staging tree. Regular files and directories
are copied with owner-only local permissions; safe relative symlinks and regular
file hard links within the selected folder are retained. Selecting a symlink,
links outside the downloaded folder, special files and names unsafe on supported
platforms produce an error. Transfers have a 30-minute timeout and a 100,000-entry
limit, but no editor-size or UTF-8 content restriction.

The connection revision and original SHA-256 content must still match. A changed
file requires reopening; transport failures are not automatically retried.
Writes preserve an existing file's inode and permissions and stage input in a
private temporary sibling before checking the original contents. This is not an
atomic transaction with other container processes: an external writer can race
the final comparison/write, and a failed write can leave partial contents.

This currently requires a running Linux container with `sh`, `head`, `mktemp`,
`sha256sum`, `wc`, `tr`, `cat` and `rm` (GNU or BusyBox tools).
Displaying symlink destinations and resolving links additionally requires `readlink`. Minimal/distroless
images without these tools show an error. Access uses Docker exec or Kubernetes
pods/exec permissions; it does not grant additional OS permissions. Files are
available only to the application UI, not agent grants, and contents are not
persisted in Ocular's settings or resource caches.

### Log windows, downloads and resizing

**Download** reads all currently retained history of the selected log source in a
separate finite archive. It uses the selected channel and previous-instance option,
but ignores the viewer's line limit, time range, search and text filter. It does
not export only the viewport or the bounded viewer buffer. **Copy** continues to
copy the filtered loaded lines. Each exported line includes its original provider
timestamp, when available, and source label; ANSI formatting is removed.

In desktop mode, including detached log windows, choose the destination before
history reads start. Native and supported browser save pickers write chunks to a
staged file and replace the destination only after a complete stream. Progress
shows lines/bytes, and **Cancel log download** stops the export. Closing its tab or
changing source/channel cancels it. Cancelling the chooser starts no history read.
Source failures, gaps, truncated lines, limits or an interrupted stream abort the
staged download instead of silently saving a partial history as complete.

The archive read is bounded to 1 GiB, 10000000 lines, 1000 sources and 5 minutes.
Browsers without a streaming save picker use a 64 MiB in-memory download-manager
fallback and report an error beyond that size; use the desktop app or a streaming
save picker for larger exports. Ctrl+S/Command+S invokes the same action. History
is limited to what Kubernetes/Docker still retain: expired/rotated logs and deleted
Pods are not recovered, and several sources are read separately, not atomically.

Log drag selection is confined to its viewport, including native selection
reanchoring and dragging outside its edges. Clicking log text explicitly focuses
the viewport so Ctrl+A selects logs rather than the surrounding interface.
Releasing/cancelling the drag, losing the window or closing the tab removes the
temporary selection guard; input fields retain their own selection behavior.

The log toolbar's **Open in window** opens a separate OS window in desktop mode
(or a popup in browser mode). It uses the existing tab's log stream and buffered
lines. Sources, history options, search and display preferences stay synchronized.
Use **Return to bottom panel**, or close the secondary window, to restore the
original tab. Closing its main tab also closes the secondary window; closing the
main application exits all its windows. Up to eight log windows can be detached.
Detached windows have no Downloads history button or extra header; their log
export action remains available and records exports in the main window history.

While dragging a layout separator, incoming log lines stay in the existing
bounded pending buffer and are applied on release. This avoids rebuilding a large
log window on every resize frame. Width-only changes do not remeasure fixed-height
unwrapped rows. Resizing the bottom panel preserves the screen position of log
rows before paint: Follow stays at the tail, including when toggling line
wrapping, while reading history keeps its position without enabling Follow. At the start/end of available history the
scroll offset is clamped. Hidden tabs retain their history position when restored.
Changing Wrap while reading history keeps the first visible logical log entry
and its position within the line, clamped when the line becomes shorter. It does
not resume Follow. New incoming lines remain below that reading position.
Fractional panel sizes retain the precise scroll offset and its subpixel
remainder, so repeated rounding cannot accumulate into a visible history drift.
Wheel input releases a stale selection drag if WebKit missed
mouseup, and stops caret correction from pulling the scroll position back.

## Downloads

The download button in the application header opens the Downloads popup, with
a distinct outline and floating-window shadow separating it from the workspace.
Detached log windows have the same button and share the list. Downloads stay visible while
switching targets; opening the popup never connects a target or changes grants.
While a download is active, a thin track beneath the stationary button icon
shows a runner moving left to right in a loop, without implying a percentage.
Reduced-motion preferences keep the runner static. Successful results not yet
viewed tint the button and add a small dot. Opening Downloads acknowledges successful entries
shown in the list, including results arriving while it is open. Active, failed,
cancelled and interrupted jobs do not count as new successful downloads. Viewing
acknowledgements are retained in origin-local UI storage when available and shared
with other windows through storage events/BroadcastChannel.

The list covers container file/folder copies and log exports started through the
Ocular UI. Compact rows show a file-type icon, filename, human-readable transferred
size and localized relative time measured against the moment the popup opens. Relative
time is omitted for entries younger than one minute and stays fixed until the
next opening; transfer status, progress and new entries continue updating. Hover
the size or visible relative time for the exact date. Full paths are available through Copy path rather than repeated in every row. Active,
failed, cancelled, interrupted and browser-handoff states remain explicit.
A cancelled destination chooser starts no download.

Failed entries include a localized reason beneath their compact metadata: for
example permission denied, insufficient destination space/quota, an existing
or unavailable destination, an unavailable source, interrupted connection,
timeout, incomplete log history, a download limit or rejected archive. Only a
safe reason code is retained, never raw exception text, remote stderr or log
contents. The first reported failure survives later cleanup. Old entries and
unclassified errors explicitly say that the reason is unavailable; Ocular does
not reconstruct missing causes. Native staged-export expiry is a failed timeout,
while explicit cancellation and application/window closure keep their distinct
states. No partial output is presented as a completed download.

In desktop mode, completed entries offer **Open downloaded item**, **Show in
folder** and **Copy path**. File opening uses the OS default application; folder
opening selects the item on Windows/macOS and opens its parent folder on Linux.
Moved or deleted items report an error. Nothing opens automatically. Interrupted
or failed downloads do not expose an actionable destination as a complete file.

The native app retains the latest 200 entries across restarts, keeping active
transfers while trimming old terminal records. It stores metadata only in its
private data directory, never downloaded contents, log text or credentials.
Unfinished native transfers become Interrupted after application exit/restart;
they are never retried or resumed automatically. **Clear history** removes only
terminal metadata, leaves active transfers visible and never deletes files. It
is disabled when there is nothing to clear and stays muted on hover.
The row's **Remove from history** cross removes only that terminal entry and
never deletes its file. The **Delete downloaded item** trash button is desktop-only:
it previews the exact path, starts confirmation on Cancel, and permanently deletes
the file or the folder and its contents after confirmation, then removes the row.
Active transfers cannot be dismissed or deleted. Missing/replaced items or failed
deletions remain in history with an error. Existing log-source cancellation and
staging/overwrite rules remain unchanged.

Browser mode also lists UI log exports and shares metadata with detached windows.
Successful save-picker exports are Completed; a Blob download is **Sent to browser
downloads**, because Ocular cannot verify the browser's final save. Browsers do
not provide the full destination path or OS folder controls, so these native file
actions are unavailable. Terminal metadata is retained in origin-local browser
storage when available. Storage failures remain visible and do not delete files.
Native history read/lock failures preserve the existing metadata file and use a
visible in-memory history for the current process instead of overwriting it.

## Resource comparison

In resource properties, use **Use as comparison baseline**, then open another
resource (also on another connected target) and choose **Compare with baseline**.
The read-only dialog compares normalized saved YAML, preserving meaningful fields
and array order. **Show service fields and status** includes otherwise hidden
Kubernetes metadata and status. Secret payloads remain excluded. **Refresh**
reads both UID-pinned objects again; editor drafts remain untouched. See
[resource comparison](resource-comparison.md) for identity, normalization and
retention boundaries. This feature is implemented from 1.2.0 and is
included from 1.2.0.
