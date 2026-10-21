# Planned work

This is the remaining product backlog, not authorization to start a task.
Implementation begins only when requested. Current behavior is documented in
[usage](usage.md) and [architecture](architecture.md).

[Product and website direction](product-plan.md) compares competitors, paid
capabilities and proposed validation priorities. It does not authorize additional
implementation or publication. [Website direction](site-plan.md) tracks remaining
site and download-journey work separately from application features.

## Learning and resource help

See [learning direction](product-plan.md#kubernetes-learning-and-contextual-help)
for the native-language help and interactive learning direction.
Current behavior is documented in [learning](learning.md).

- Implemented: offline explanations for the 37 described/placed built-in
  API kinds in all eight languages, with purpose, relationships, key fields and
  common misconceptions.
- Implemented: one isolated Deployment/ReplicaSet/Pod lesson without a
  cluster, with reset, owner navigation and a bounded comprehension check.
- Implemented: five additional local modules with guided practice and independent
  checks for Service routing, probes, startup failures, configuration and resources.
- Remaining validation: independent beginner studies and native-speaker review;
  local task completion does not establish real-world competence.
- P2: a bounded form-based resource builder and manifest export; create/apply is
  not the leading priority merely because competitors offer it.

Detailed scope and acceptance live in `spk-ocular-learning-resource-help`,
`spk-ocular-learning-first-lesson`, `spk-ocular-learning-path` and
`spk-ocular-competitive-resource-create`. Help and the first lesson have been
implemented. Independent learner validation remains separate from software
delivery; do not treat a completed local check as a learner study.

## Backup and recovery integrations

- [ ] **Native Velero support:** add a dedicated section when Velero APIs are
  discovered in the connected cluster, following the Argo CD/Flux navigation
  model. Plan backup/restore and schedule inspection, controller status, related
  events and guarded resource navigation. Detailed supported kinds, operations
  and acceptance criteria must be selected and verified before implementation.
  This is a roadmap item only; installation, backup/restore execution, credential
  access and changes to real clusters are not authorized by its inclusion.
  Planning task: `spk-ocular-velero` in the work task service.

## Investigation workbench

Events Timeline, RBAC explanation, resource comparison and workspace continuity
are implemented from 1.2.0. The remaining items follow the agreed
feature shortlist; the list does not authorize publication.
Implementation scope for each subsequent feature is selected separately.

- [x] **[Events Timeline](events-timeline.md):** explicit bounded Events API
  snapshots/Refresh, UID-based workload ownership groups, warning/kind/time
  filters and standard resource properties/log navigation. API retention, partial
  coverage and cumulative series semantics are disclosed. Included from 1.2.0.
- [ ] **Persistent observed timeline:** bounded local observation history with
  visible gaps, UID replacement and retention, without retrospective audit claims.
- [x] **[RBAC explanation](rbac-explanation.md):** ServiceAccount declaration
  provenance, current-connection server access checks, guarded source navigation
  and Forbidden resource-read inspection. Partial coverage and authorization
  boundaries are explicit. Included from 1.2.0.
- [x] **[Security and NetworkPolicy inspection](security.md):** read-only workload
  hardening, declared role risks, ingress/egress policy selection and existing
  Trivy Operator CVE reports with setup guidance. Included from 1.3.0.
  No exploitability, CNI enforcement or connectivity claim.
- [ ] **Extended security investigation:** declared permitted peers, binding-based
  workload permission provenance and further report adapters; scope separately.
- [x] **[Resource/environment comparison](resource-comparison.md):** explicitly
  selected UID-pinned resources across connected targets, normalized saved YAML
  diff and optional service fields/status. Read-only, with Secret payloads
  excluded. Included from 1.2.0.
- [x] **[Native Argo CD workspace](argocd.md):** scoped Applications, ApplicationSets,
  Projects, source/revision/conditions/resources and Events; reviewed UI-only
  Sync/Prune and Refresh/Hard refresh with revision/identity guards. Explicit
  local-resource resolution and Argo UI Service forwarding; no real-controller
  reconciliation claim. Current source; release inclusion follows release notes.
- [ ] **Broader GitOps ownership awareness:** explain known controller/source
  context before ordinary edits; Flux and further adapters remain separate.
- [ ] **Historical metrics:** connect to an existing Prometheus and link existing
  Grafana dashboards; no implicit collector installation.
- [x] **[Investigation workspace continuity](workspace-continuity.md):** pinned
  resource/log references, ended terminal slots and restored layouts. Explicit
  connection/resume, UID guards, no command replay or persisted contents/drafts.
  Included from 1.2.0.
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
- Broader object search beyond the explicit single-kind Ctrl+K type/name query,
  without unbounded background listing.
- Background credential-helper support for desktop keyring-backed caches.

## Editing and actions

- Object creation, multi-document apply, Compose editing and three-way YAML merge.
- File import/export and decoded certificate views.
- Namespace deletion with a typed name and a persistent action journal.
- Bulk actions with text/choice parameters; reviewed finalizer removal.
- StatefulSet/DaemonSet rollback, revision browsing and richer YAML comparisons.
- Configurable drain timing, multiple-node drain and progress until pods leave.
- Navigation to newly created CronJob Jobs and reviewed template changes.
- Pod copies, additional node-debugging profiles/custom commands and attachment
  to suitable existing containers. Reviewed Linux Node Shell is implemented; see
  [Node Shell](usage.md#node-shell).

## Workspace and streams

- Persisted column order; pinned log references and ended terminal slots already
  have [workspace continuity](workspace-continuity.md), without session replay.
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
