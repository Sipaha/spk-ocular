# Planned work

This is the remaining product backlog, not authorization to start a task.
Implementation begins only when requested. Current behavior is documented in
[usage](usage.md) and [architecture](architecture.md).

[Product and website direction](product-plan.md) compares competitors, paid
capabilities and proposed validation priorities. It does not authorize additional
implementation or publication. [Website direction](site-plan.md) tracks remaining
site and download-journey work separately from application features.

## Investigation workbench

Events Timeline, RBAC explanation and resource comparison are implemented in
development builds. The remaining items follow the agreed feature shortlist; the list does not authorize publication.
Implementation scope for each subsequent feature is selected separately.

- [x] **[Events Timeline](events-timeline.md):** explicit bounded Events API
  snapshots/Refresh, UID-based workload ownership groups, warning/kind/time
  filters and standard resource properties/log navigation. API retention, partial
  coverage and cumulative series semantics are disclosed. Not yet released.
- [ ] **Persistent observed timeline:** bounded local observation history with
  visible gaps, UID replacement and retention, without retrospective audit claims.
- [x] **[RBAC explanation](rbac-explanation.md):** ServiceAccount declaration
  provenance, current-connection server access checks, guarded source navigation
  and Forbidden resource-read inspection. Partial coverage and authorization
  boundaries are explicit. Implemented in development builds; not yet released.
- [ ] **Security and NetworkPolicy inspection:** show policy selection and
  declared connectivity alongside risky security contexts and RBAC. Declarative
  analysis must not claim proven packet reachability or exploitability.
- [x] **[Resource/environment comparison](resource-comparison.md):** explicitly
  selected UID-pinned resources across connected targets, normalized saved YAML
  diff and optional service fields/status. Read-only, with Secret payloads
  excluded. Implemented in development builds; not yet released.
- [ ] **GitOps awareness:** show known Argo CD ownership/source/revision/sync
  context and explain reconciliation before edits.
- [ ] **Historical metrics:** connect to an existing Prometheus and link existing
  Grafana dashboards; no implicit collector installation.
- [ ] **Investigation workspace continuity:** pinned properties/logs and restored
  layouts, without replaying commands or silently retaining sensitive drafts.
- [ ] **Optional integrated AI assistant:** reuse explicit agent grants and change
  review; provider/context privacy and local models need a separate design.

Paid-tier boundaries are documented in [product direction](product-plan.md).
Ocular's features remain free; competitor packaging does not change that policy.

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
- Background credential-helper support for desktop keyring-backed caches.

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
