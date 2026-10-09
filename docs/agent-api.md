# Local agent API

On Linux and macOS, the agent API is HTTP/JSON over the Unix socket
`$SPK_OCULAR_HOME/agent.sock`, defaulting to `~/.spk/ocular/agent.sock`.
It is available while either desktop or browser mode is running.

```sh
curl -s --unix-socket ~/.spk/ocular/agent.sock http://ocular/v1
curl -s --unix-socket ~/.spk/ocular/agent.sock http://ocular/v1/methods
```

On Windows the same HTTP/1.1 protocol uses a local named pipe
`\\.\pipe\spk-ocular-<profile-hash>`. Copy the exact endpoint from the Agents
panel; it is derived from the absolute data directory. Use a named-pipe-capable
HTTP client (for example Go `winio.DialPipeContext` as an HTTP transport), or send
a normal HTTP request over a Windows named pipe. `curl --unix-socket` applies
only to Linux/macOS. A minimal Python discovery request is:

```python
import http.client

# Replace PIPE with the endpoint shown in the Agents panel.
with open(PIPE, "r+b", buffering=0) as pipe:
    pipe.write(b"GET /v1 HTTP/1.1\r\nHost: ocular\r\nConnection: close\r\n\r\n")
    class Connection:
        def makefile(self, *args):
            return pipe
    response = http.client.HTTPResponse(Connection())
    response.begin()
    print(response.read().decode())
```

`GET /v1` explains the current protocol and `GET /v1/methods` provides the
method catalog, request/response JSON schemas, required rights, and mutation
flags. Call methods with JSON bodies at `POST /v1/<Method>`. The catalog is the
source of truth for method fields; schemas are generated from the handler DTOs.

## Permissions

No rights are granted by default. A person grants and revokes access in the
Agents dialog. Grants cover a provider, identified target, explicit scopes or
All, verbs, and kinds. The global-object scope is read-only. Rights are common
to local agents; a caller's agent name is self-reported, not authenticated.

Target identity includes its configured endpoint and identity material; Docker
also includes the daemon identity. Pointing a context at another target suspends
its grants until identity is confirmed. Rights and target identity are checked
on every call and again immediately before a pending mutation executes.
The resource header's **Agent permissions** button opens the same editor for
the current target and selected namespaces/projects. An explicit set opens one
card per name; All opens a separate all-scopes card, including future scopes.
An empty selection disables the button. Context cards start without permissions
unless that scope already has grants; opening the editor never writes grants.
Other saved scopes remain visible and are preserved when saving.

Each namespace/project has a persisted master switch and any number of named
access groups, each with its own persisted switch. Switches and names are saved
with **Save** alongside the permissions; opening or editing a draft has no
effect on active access. Disabled groups retain all their permissions.
An enabled group's permissions add to the union; no group is a deny rule.
Overlapping grants keep their kind and confirmation boundaries: a narrow group
cannot restrict a broader one, and no-confirm on Pods does not waive confirmation
for Services granted by another group.

A disabled namespace master blocks access even through an enabled All group.
The All master pauses every namespace/project; the cluster master independently
pauses non-namespaced objects. Switching a master back on restores the previously
enabled groups and leaves individually disabled groups off. Reads, object
relations, logs, metrics, plan preparation and execution honor these switches;
saving access changes cancels active log reads. Prepared and pending mutations
recheck the current switches before writing.

The database retains legacy grant rows unchanged during migration. The editor
converts them to named groups on save, preserving repeated verbs as distinct
groups rather than overwriting or combining their kind/no-confirm settings.
The UI's SaveAgentGrants payload accepts legacy `grants`, named `groups`
(id, name, scope, disabled, grants), and `disabledScopes`, replaced atomically.
Only an empty configuration revokes the target; empty or disabled groups remain
stored. Group names contain 1–128 characters.

Within each access group, permissions are categorized as **Viewing** (resources and logs), **Changes**
(YAML), and **Execution** (individual resource actions). Execution details start
collapsed when no actions are granted. Expanding a category does not grant permissions.
Kind restrictions, sensitive-kind selection, destructive-action validation and
confirmation settings remain per permission. The editor uses the workspace's
compact headers, navigation and rows; its footer stays visible while scopes
scroll. Connection instructions are collapsed by default, while API errors
remain visible. Tab stays within the dialog and closing restores focus.

The grant card shows connection identity details only when the target changes
and requires confirmation; its ordinary heading is the target name.

The socket is mode 0600 in a mode 0700 directory. Linux/macOS peer credentials must
match the application UID. A process holds `agent.sock.lock` with flock; another
instance cannot remove or take over a live owner's socket. A too-long Unix socket
path disables agent access with a visible status while the main app remains usable.
On Windows a protected pipe DACL grants only the current user access; remote
clients are rejected and the first pipe instance reserves its name until close.
The data directory also has a protected inheritable user DACL.

This is an error-prevention and audit boundary, not a sandbox against another
process running as the same OS user. Docker access and combinations of workload
editing and logs can confer broader infrastructure access. The grant UI explains
these implications. Secret values are never directly exposed by this API.

## Read operations

`Access` returns effective grants and explicit `disabledScopes` master exclusions.
Its state is active, paused (no effective grants), or suspended (identity changed).
Disabled group contents remain available only to the user-facing editor.
`ListKinds` includes only available, permitted kinds and actions.
`ListObjects` returns a bounded snapshot (up to 500 rows), optional name-substring
filtering, truncation, and source status/coverage; it does not expose UI view IDs.
Label filtering and pagination cursors are not implemented.

`GetObject`, `Problems`, and `GetMetrics` enforce the same scope boundary.
Relations to disallowed objects are omitted and their hidden count is reported.
A namespaced request uses only granted ScopeOne reads unless the grant explicitly
covers all namespaces. Global sources are opened once and require global read
permission. Providers must honor the scope of a Ref, even for same-name objects.

Reads and metrics use object references, not IDs borrowed from UI sessions.

## Logs

One `logs` grant covers channel discovery, history, time intervals, grep, bounded
live reading and export to a local file. It does not require an additional
`read` grant. Target, namespace/project and kind restrictions apply to every
method. Log contents are never added to the audit journal.

- `GetLogInfo` lists channels, the default channel, aggregation and support for
  the previous container instance. Aggregate Kubernetes workloads also return
  concrete owned Pod refs (including stopped Pods); every subsequent request
  remains subject to its own namespace and kind grant checks.
- `GetLogs` returns a bounded JSON snapshot. Without time bounds, `limit` defaults
  to 200 matching lines. **Either time bound makes `limit` mandatory.** Its range
  is 1–5000. `maxBytes` bounds the complete encoded JSON response, including
  escaping and source metadata: default 64 KiB, allowed 4 KiB–1 MiB.
- `StreamLogs` returns bounded NDJSON frames. **`limit` is always required.**
  It has the same line/byte ceilings, plus `maxSeconds` (default 20, maximum 60),
  even with `follow: true`. Metadata and heartbeat frames count toward its byte
  budget. It cannot produce an indefinite response.
- `ExportLogs` writes retained history to a new file in Downloads and returns
  only its path, format, byte/line/source counts, completeness and bounded
  warnings. Log text does not enter the API response.

Shared selection options:

| Field | Meaning |
| --- | --- |
| `ref` | Object reference, including namespace/project and preferably UID |
| `channel` | Default when omitted; `*` for all; IDs from `GetLogInfo` |
| `sinceTime` | Inclusive start, RFC 3339 with optional fractional seconds |
| `untilTime` | Exclusive end, later than the start |
| `previous` | Previous container instance where supported; cannot be followed |
| `grep` | Object with `pattern`, optional `regex`, `ignoreCase` and `invert` |

Grep matches the log text before counting returned lines against `limit`.
The default is literal text; `regex: true` uses Go/RE2 expressions. Patterns must
contain 1–4096 bytes. Inversion selects nonmatching lines. Invalid expressions
are rejected before reading or creating an export file.

`GetLogs` and `StreamLogs` also accept `tailLines`: 1–5000 per source, or `-1` for
retained history. Without a time bound or grep, its default is 200. With a time
bound or grep, the default is `-1`, so an old interval is not accidentally
excluded by taking today's last 200 lines first. `limit` independently bounds
the matching output across all sources. Previous-container history supports time
bounds too. Untimestamped lines cannot be assigned to an interval: they are
omitted with an explicit gap state.

For example, POST this to `/v1/GetLogs` or `/v1/StreamLogs`:

```json
{
  "ref": {
    "provider": "kubernetes",
    "target": "kubeconfig:dev",
    "scope": "team-a",
    "kind": "pods",
    "name": "web-123"
  },
  "channel": "*",
  "sinceTime": "2026-01-01T10:00:00Z",
  "untilTime": "2026-01-01T11:00:00Z",
  "grep": { "pattern": "error|timeout", "regex": true, "ignoreCase": true },
  "limit": 100,
  "maxBytes": 32768
}
```

A snapshot reports `truncated` and `limitReason`; source metadata covers returned
lines and a bounded set of source problems, while `totalSources` reports how
many sources were observed. Byte-budget trimming keeps a suffix of matching
lines. This is not a cursor-based pagination API.

Stream frames are `start`, `source`, `lines`, `state`, `ready`, `ping`, `error`
and `end`. Inspect source states and the final `end.complete`, `end.truncated`
and `end.stopReason`. A reached line/byte/time budget means more data may exist;
EOF without `end` is incomplete. `ready` is a provider signal, not proof of
complete history. A finite historical tail with positive `tailLines` uses the
snapshot bounds before being emitted. Followed history uses the provider's live
window; use finite history or file export to inspect older retained records.

### File export

POST to `/v1/ExportLogs` with the selection above, `limit` and optional
`format: "text"` (default, `.log`) or `format: "ndjson"` (`.jsonl`). Omit time
bounds and grep to export all retained logs of the object. With time bounds,
`limit` remains required. Without them, the file limit defaults to 1,000,000
matching lines; it can be set to at most 10,000,000. File size defaults to
64 MiB (`maxBytes`, maximum 1 GiB); duration defaults to 120 seconds
(`maxSeconds`, maximum 300). Limits and incomplete sources are explicit in the
returned `complete`, `truncated`, `stopReason` and warnings.

The file has a generated unique name and owner-only access: mode 0600 on Unix,
a protected current-user DACL set at creation on Windows. The agent cannot supply a
path or overwrite a file. Downloads follows the application's XDG download
configuration, otherwise `~/Downloads`. Text files preserve timestamps and source
labels; NDJSON preserves structured line metadata. Records are written in source
order, without an in-memory merge. The agent can inspect the returned path with
local tools instead of asking the model to consume all log contents.

Finite retained-history reads use incremental provider reads and bypass the
screen's 50,000-line tail and merge-buffer limits. At most 1000 sources are
admitted; coverage failures and oversized-line truncation remain visible.
Rotated or deleted history that the target no longer retains cannot be recovered.

All log reads, including exports, stop on target grant changes, configuration
changes, shutdown or client disconnect. An export cancelled by revocation or
client disconnect is removed; a returned partial file is explicitly marked as
such. Four concurrent agent log operations are allowed across the application.

## Mutations and confirmation

`PrepareAction`/`RunAction` and `GetEditSource`/`PrepareEdit`/`RunEdit` use opaque
agent-owned `planId`/`sourceId`/`runId` values. UI handles, Expect payloads, edit
bases, and signing tokens remain inside the process. Prepared entries are
bounded, expire, and cannot be reused as another caller's request.

A destructive plan requires a grant naming the kind and the relevant verb.
Unless that grant permits execution without confirmation, Run returns
`awaiting_confirmation`. A person confirms in Ocular. `GetRun` can wait up to
25 seconds for a result; pending confirmation expires after ten minutes and
process exit discards pending runs. A stale plan fails rather than being
silently rebuilt after confirmation.

The confirmation dialog starts on No. Escape means Later, not rejection.
The pending count appears in the UI/window title and can produce a desktop
notification without raising the window. Non-destructive mutations within a
valid grant execute without that extra confirmation.

Cluster-scoped mutations, Secret value operations, exec, port forwarding, and
streaming events are not available to agents. Plans naming affected objects
outside the granted scopes are refused. Broad edit-kind grants exclude sensitive
identity/security kinds unless those kinds are named explicitly.

### Standalone Docker containers

The UI's Docker inventory includes containers without Compose project labels.
They retain an empty project scope. Existing `all` project grants cover only
nonempty project scopes; a `cluster` read grant does not turn a scoped container
kind into an unscoped kind. Such standalone rows are filtered from agent lists,
and direct detail/log/action requests are refused. Supplying a false project
also fails the provider's fresh membership check. A separate explicit grant
model is needed before agent access to standalone containers can be offered.

The internal provider ID remains `compose`, even though its UI title is Docker.
Existing target identities and saved grants keep their meaning.

## Sessions and audit

Agent calls retain the target session while in use, independently of UI
selection. Snapshot concurrency and waiting time are bounded. Audit records
include intent before writes and the resulting status afterward. Reads are
coalesced by method and target within a minute. Request bodies and Secret values
are not journaled. The journal is bounded to 20,000 records and can be filtered
by target and reported agent name in the UI.
