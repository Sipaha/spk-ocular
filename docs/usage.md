# Using SPK Ocular

## Start and connect

The desktop application opens one window and exits when that window closes.
It has no tray process. Choose a Kubernetes context or Docker connection in the
left sidebar, then press **Connect** in the resource-list area. Selecting a target
or restoring the previous selection never starts a connection. Discovery reads
local configuration without contacting servers. Existing connections can stay
open while switching targets; restarting the app requires Connect again.

During connection, the page shows the actual phase, elapsed time, attempt number,
last error, and time until a retry. Temporary availability failures get at most
**three total attempts**. Authentication and configuration errors stop immediately.
**Cancel** stops both an active attempt and the wait before a retry, including
session-owned credential helpers. After cancellation or failure, Connect starts
a new sequence. Configuration changes also require a new explicit connection.

`spk-ocular --browser --port 5190` serves the same interface on loopback HTTP.
Use the local URL printed at startup. The browser interface requires its
per-process page token and does not accept remote Host values.

Connection information is available through the information button next to the
connection name in the header. Closing that dialog returns focus without
resetting the resource table, its filter, or an unsaved editor.

## Resources and namespaces

The resource navigation groups built-in Kubernetes kinds and discovered API
resources. A resource search matches names, aliases, groups, and kind IDs.
Enter opens the only matching kind. This search is independent of the row filter.

Click a namespace row or its text, or press Enter, to select exactly one
namespace and close the picker. Use checkboxes to add or remove names from a
set. Space toggles a checkbox when the list has focus; it types a space in the
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
Shortcuts follow physical keys so they work with different keyboard layouts.

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

## Logs, terminals, and tunnels

Logs support multiple sources, bounded history, search, filters, copying, and
follow mode. Scrolling up pauses following; Follow resumes it. Gaps, truncated
history, and unavailable sources are shown explicitly.

Terminal tabs keep their connection when you change the selected target. Closing
a Kubernetes terminal sends a hang-up sequence before disconnecting. A connection
configuration change is marked on existing tabs; it does not silently reconnect
a running session to another target.

Port forwarding binds loopback addresses only and uses the actual allocated port.
Tunnels have their own lifecycle and remain open across connection changes.
UDP and non-loopback forwarding are not supported.

## Agents

The Agents dialog manages local API grants, pending confirmations, and an audit
journal. No access is granted by default. Destructive plans require confirmation
unless the particular grant explicitly allows execution without it. See
[the agent API](agent-api.md) for the transport and permission model.
