# Investigation workspace continuity

Workspace continuity is implemented from 1.2.0 and is included from 1.2.0.
It preserves explicitly pinned resource/log references, ended terminal slots and
panel sizes. It does not persist investigation contents or reopen connections.

## Pin and restore

Choose **Pin properties** in the normal resource properties. A read-only copy of
the ordinary properties/YAML/relations/events surface opens in the bottom panel.
It remains while navigating between resources and connections; multiple resource
panels can coexist with logs and terminals. YAML, container-file and protected-value
editing stay in the main resource properties. Opening, navigating or closing a
pinned read-only panel preserves an existing main-editor draft.

Every bottom-panel tab has **Keep after restart**. Resource properties are pinned
by default; log/terminal tabs are retained only after this explicit choice.
Unpinning keeps the current tab open but excludes it from the saved workspace.
Closing removes it from both the current and saved workspace. Tab order and the
active retained tab are restored. The layout includes target/navigation widths,
resource and Helm detail widths, the agent target-panel width and bottom height.
Sizes are bounded and clamped to the current viewport. When no saved workspace
exists, the selected target’s existing bottom-panel height is migrated once. Existing per-target kind,
scope, row filter, cursor, sorting, details tab and table-column widths continue
to use their existing persistence. Row checkmarks are never restored.

After restarting, retained panels appear as dormant references. Select the target
and press **Connect**, then explicitly open the pinned properties or resume logs.
A reference in another target cannot open that connection implicitly. Resource
properties show their current read result, not the previous contents. Log resume
starts a new ordinary bounded read; prior lines, buffers, searches, time ranges,
regexes and display controls are not restored. The channel selected when the tab
was opened is retained. Detached windows return as ordinary bottom-panel tabs.

An ended terminal slot retains only its object reference. **Set up a new terminal**
validates the pinned object and opens the usual instance/container/command setup.
The command field starts empty; a new terminal begins only after the user submits
that setup. Original commands, attachments, instance choices, terminal IDs, screen
contents and processes are not resumed. Closing an ended slot executes nothing.

## Identity and privacy

Stored panels whitelist provider, target, kind, scope, name and UID; log channel
and the properties details/YAML choice are optional metadata. Display titles,
server hints, credentials, configuration hashes, YAML/Secret values, editor/file
drafts, log lines, searches and terminal command/screen data are excluded.
Resource names and namespaces are still local metadata, not anonymous data.

Each retained object requires a UID. A same-name replacement is refused, including
when a provider's ordinary Get does not enforce the supplied UID. Log and ended
terminal resume validate returned identity before starting the normal tool flow.
Pinned property responses also validate kind/scope/name/UID before display or
recent-object recording. Late resume responses cannot reactivate a closed tab.

Pinned properties bind their live read to the admitted connection incarnation
and configuration. Closing/reconnecting/reconfiguring makes that panel dormant
again; it requires an explicit resume. Existing running logs and terminals retain
the ordinary stream/session semantics and never acquire new privileges. Existing
UI-only inspection and reviewed operation boundaries remain unchanged; this
feature adds no agent endpoint or grant.

## Storage and failure handling

A versioned `workspacePanels` snapshot uses the existing local target_state store
under the reserved `ocular/workspace` UI preference identity. It never asks the
provider registry to create or connect an Ocular target. At most 8 retained panels
fit within the existing 4096-byte state-value limit; 512 bytes are reserved for
layout and bookkeeping. Oversized selections or references without a UID are visibly refused,
without silently truncating existing panels. Runtime unpinned tabs keep their
ordinary lifetime and are not constrained by this saved-panel count.

Strict parsing rejects invalid versions, malformed identities, duplicate resource/
log slots and invalid layout values. Unknown fields never enter restored tabs.
Multiple ended terminal slots for one object remain distinct. A failed/invalid
read pauses saving to protect the stored snapshot. Explicit retry loads it again;
**Reset saved workspace** replaces unusable stored data with the current valid
metadata. Save failures retain current panels and offer retry. Writes share the
existing per-target serialization queue across remounts, so older writes cannot
overtake newer ones. A resize or explicit tab opening during initial loading is
not overwritten by a late restore response.

All controls and diagnostics are translated in all eight application languages.
