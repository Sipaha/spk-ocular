# Planned work

This is the remaining product backlog, not authorization to start a task.
Implementation begins only when requested. Current behavior is documented in
[usage](usage.md) and [architecture](architecture.md).

[Product and website direction](product-plan.md) compares competitors, paid
capabilities and proposed validation priorities. It does not authorize additional
implementation or publication. [Website direction](site-plan.md) tracks remaining
site and download-journey work separately from application features.

## Distribution and platforms

- Developer ID signing and notarization for macOS, and publisher signing for
  Windows (current macOS bundles are ad-hoc signed; Windows packages unsigned).
- AppImage distribution and a deliberately designed in-app update mechanism.

## Providers and discovery

- Read compose files for desired replicas, services without containers and
  scale/create/up/down operations.
- Docker SSH endpoints, Compose Problems, pause/unpause,
  selectable kill signals and image operations.
- CPU/memory metrics for Windows Docker daemons; full aggregation above 20 replicas.
- SSH/custom providers and simultaneous workspaces or multiple windows.
- Discovered-kind scale subresources, additional actions and child relationships;
  richer CRD column provenance and Problems coverage for custom resources.
- Search objects of unopened kinds without unbounded background listing.
- Configurable recent-session retention and background credential-helper support
  for desktop keyring-backed caches.

## Editing and actions

- Object creation, multi-document apply, Compose editing and three-way YAML merge.
- ConfigMap/binaryData value tools, file import/export and decoded certificate views.
- Namespace deletion with a typed name and a persistent action journal.
- Bulk actions with text/choice parameters; reviewed finalizer removal.
- StatefulSet/DaemonSet rollback, revision browsing and richer YAML comparisons.
- Configurable drain timing, multiple-node drain and progress until pods leave.
- Navigation to newly created CronJob Jobs and reviewed template changes.
- Pod copies, node debugging, debugger profiles/custom commands and attachment to
  suitable existing containers.

## Workspace and streams

- Persisted column order and restored log/terminal tabs across application restarts.
- Additional rollout-container log sources and selection/highlighting refinements.
- Hidden/minimized WebKit stream behavior and high-volume terminal memory analysis.
- Session recording and cleanup of abandoned reconnect prototypes.
- Earlier visibility of revoked RBAC and measured reuse of broader informer caches.
- Complete localization of remaining provider/transport diagnostics.

## Agent access

- An explicit access model for standalone Docker containers without widening
  existing project grants.

- Optional per-agent/project grants and expiry, agent-initiated access requests,
  paginated/label-filtered snapshots and a CLI wrapper.
- Separately reviewed support for cluster mutations, values, terminals, tunnels,
  and streaming events. No expansion of grants is implicit in UI feature work.

## Detection and performance

- Restart-rate detection from a single authoritative per-pod observation stream,
  with coverage epochs, no delta across observation gaps, stale-delivery rejection,
  bounded session-wide retention and tests across overlapping views.
- Memory reductions that preserve immediate filtering/sorting and responsiveness.

UDP and non-loopback forwarding are intentionally outside the product scope.
