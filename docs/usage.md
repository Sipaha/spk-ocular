# Using SPK Ocular

## Start and connect

The desktop application opens one window and exits when that window closes.
It has no tray process. Choose a Kubernetes context or Docker connection in the
left sidebar, then press **Connect** in the resource-list area. Selecting a target
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

`spk-ocular --browser --port 5190` serves the same interface on loopback HTTP.
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
for namespace boundaries, limits, identity and review semantics. Available in
development builds, not published v1.1.2.

## Events timeline

On a connected Kubernetes target, open **Cluster → Events timeline**. Inspect
warning and normal event series on a time axis, filter by resource kind/time,
and search workloads, reasons or messages. Select an event and use **Open
resource** to access the standard properties and logs; known ownership ancestors
can also be opened. **Refresh** reads a new snapshot and preserves open resource
edits. Older Kubernetes events may already have expired, and incomplete access
or size caps are disclosed. Bars show first/last observations of cumulative
series, not continuous failure duration. See [Events timeline](events-timeline.md)
for coverage, limits and evidence semantics. Available in development builds;
not included in published v1.1.2.

## Cluster graph

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

**Add kubeconfig** offers local file selection or a YAML editor. Local imports
are links, not copies: external edits are observed, new unselected files remain
excluded, and originals are never modified. Selected KUBECONFIG files retain
first-wins merge order; standalone files stay independent. Context IDs preserve
the existing primary/file identity domains so saved selections and grants remain
associated with the same targets. The `current` badge is no longer displayed.

Before creating a custom configuration, create a non-empty master password
and confirm it. Its length and strength are your choice. Ocular encrypts the YAML with AES-256-GCM and a key
derived using Argon2id. It does not store the password. After restarting, click
**Connect** on a protected connection to enter the master password. Selecting
a row never asks for it. One successful entry unlocks all encrypted configurations
and continues that connection; linked files remain available without unlocking. The key is kept in memory until Ocular closes. There is no password
recovery or OS-keychain integration. Cancel closes the password dialog without
connecting or unlocking. **Reset master password** is available in that dialog
and the Add menu; its separate review lists all encrypted configurations and
warns that they will be permanently deleted. Cancel preserves them. Confirming
removes the encrypted records and master key, closes their sessions, preserves
linked external files, and allows creating a new master password. Keep an independent copy of your configs:
losing the password makes the stored YAML inaccessible.

Custom configs require a name and valid kubeconfig YAML with at least one context.
Use inline certificate/key data or absolute external paths; relative file paths
are rejected because pasted YAML has no source directory. Credential-helper
commands must be on PATH or use absolute paths, and execute only on Connect.
External files and helper-managed caches are outside Ocular's encryption.

**Add kubeconfig** opens a two-item menu: **From system files** scans import
candidates; **New configuration** opens master-password setup/unlock and then
the YAML editor. There is no separate configuration-management list.

Right-click a Kubernetes connection, including the selected one, to inspect or
edit its source YAML, rename the configuration or remove it. With the targets
list focused, Shift+F10 opens the keyboard cursor's menu. Linked files also offer
**Open in file manager**: Linux opens the parent folder; macOS and Windows select
the source file. This action runs on the computer hosting Ocular, including
browser mode, and reports launcher failures beside the add action.

Inspection is read-only with syntax highlighting and search. The inspector and
source editor use nearly the full window; a compact header shows the source
name, path and mode, leaving the remaining area to YAML. Editing starts
from that same source; saving requires explicit confirmation naming the external
file or encrypted configuration. Changes affect all contexts using the source.
External file updates are atomic, preserve the file's permissions and symlink,
and reject changed content. Encrypted updates keep the entry ID and use a fresh
nonce. Stale source or registry revisions refuse the write. Credential helpers
are not run by inspection or editing. YAML is held only in the open dialog's
memory, never in preferences or SQLite. Cancelling an unchanged editor closes it;
cancelling a changed draft asks before discarding it. A save review can be
cancelled without losing the draft.

Rename opens with the current name focused and selected. Password and creation
dialogs focus their first input; the source inspector/editor focuses YAML.
Rename changes the exact visible name of that connection, preserving context
IDs, source file names, YAML context keys and grants. Other connections from the
same source retain their names. Display names persist across app restarts. Remove requires confirmation; it unlinks
an external file without deleting or editing it, or deletes the encrypted copy.
Affected sessions close after source edits or removal, and resource edits must
be saved or discarded before these operations. Encrypted connections remain visible in the sidebar with a lock until unlocked
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

If permission to list namespaces is unavailable, type names separated by commas.
Submitting an empty input explicitly selects All. Namespace selections belong
to each connection and survive switching connections and restarting the app.
Resource reads for an explicit set stay within those namespaces; failures in
one source are visible while accessible sources remain usable.

While the initial resource results are loading, the content area shows one
loading state beneath the toolbar. The table and coverage summary appear with data
or a settled result. Already displayed rows and source failures remain visible
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
tabs survive switching connections, but do not survive restarting the process.

## Search and keyboard

Ctrl+K opens the command palette. It searches kinds, connections, scope names,
current table rows, and recently opened objects. Object results are restricted
to selected namespaces, including recent results and results shown while the
current table is unscoped. Global objects such as Nodes remain available.

Opening an object selects its kind, opens details, and selects/reveals its exact
row when available. The namespace selection remains unchanged. A missing kind
can still be attempted through its recent object's details in the current view.
A different UID is a different object, even if its name is identical.

Commands use aliases supplied by the provider, for example `:po`, `:deploy web`,
`:ns team-a`, `:ns *`, and `:ctx staging`. Ambiguous commands require an explicit
selection. Namespace-switching commands remain available outside the current
selection. The palette searches already loaded data and history; it does not
query every kind in the cluster.

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

Related resources appear as compact grouped rows with a kind badge, truncated
name, full-name tooltip and navigation arrow. Unresolvable references remain
plain non-clickable rows; cross-namespace rows show their scope.

Deployment details include **Deployment revisions**: retained ReplicaSets ordered
by revision number, with the current template marked, creation time, ready/total
replicas, container images and change cause. **View YAML** reads the selected
revision's Pod template; **Compare** compares it with the current Deployment or
another retained revision. Comparisons omit the controller's pod-template-hash
label. These views are read-only and UID-pinned; permission failures and truncated
history are explicit. Kubernetes may already have removed older revisions under
revisionHistoryLimit. This section adds no rollback control.

## Logs, terminals, and tunnels

Logs show all levels without severity toggle buttons or a default DEBUG filter.
Use text filters and search for any log format. Logs support multiple sources, bounded history, search, filters, copying, and
follow mode. Scrolling up pauses following; Follow resumes it. Gaps, truncated
history, and unavailable sources are shown explicitly. The icon at the right edge
of the toolbar detaches logs into a window or returns them to the main panel;
its tooltip and accessible label describe the action.

Left-click **Logs**, **Terminal**, or **Files** on a workload to choose a Pod
when several are available; one Pod opens directly with its default container.
On a concrete Pod, several containers produce a container dropdown; one opens
directly. Logs also offers **All Pods** or **All containers** where applicable.
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
resize controls as the main sidebar, resource navigation, resource details and
bottom dock. Escape during a drag restores the previous size; panel widths
survive navigation within the running application.

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

**Download** opens the system file chooser in desktop mode, including detached
log windows. Choose a destination and filename; cancelling writes nothing. The
UTF-8 export contains the displayed filtered lines and their enabled prefixes;
ANSI colors, emphasis and terminal hyperlinks are removed.
Exports are staged in a private sibling file before replacing the chosen file.
Browser mode uses the browser save picker where supported, with its ordinary
download manager as fallback. Ctrl+S/Command+S invokes the same action.

The log toolbar's **Open in window** opens a separate OS window in desktop mode
(or a popup in browser mode). It uses the existing tab's log stream and buffered
lines. Sources, history options, search and display preferences stay synchronized.
Use **Return to bottom panel**, or close the secondary window, to restore the
original tab. Closing its main tab also closes the secondary window; closing the
main application exits all its windows. Up to eight log windows can be detached.

While dragging a layout separator, incoming log lines stay in the existing
bounded pending buffer and are applied on release. This avoids rebuilding a large
log window on every resize frame. Width-only changes do not remeasure fixed-height
unwrapped rows. Wheel input releases a stale selection drag if WebKit missed
mouseup, and stops caret correction from pulling the scroll position back.
