# Ocular and Sextant

Sextant is a serious direct competitor for local Kubernetes work. At the inspected
versions it covers more Kubernetes operations and diagnostic domains than Ocular.
Ocular has a different provider breadth, embedded Helm, a browser mode and a local
learning environment. Neither feature counts, implementation language nor test
counts establish overall product quality or a performance winner.

## Comparison boundary

This assessment separates shipped artifacts, current source, reported experience,
static inspection and a small isolated function probe. It is not a live product
benchmark, security certification or independent usability study.

- Sextant: published [v0.21.0](https://github.com/FriendsOfKubernetes/sextant/releases/tag/v0.21.0),
  source `be00e44ed19f7c2f0c17de40ebdca31e3a84c1a2`; release and CI for that commit
  reported success through the GitHub API. The tag resolved to the inspected
  source. The repository was created on October 11, 2026.
- Ocular: current source `0d3429581ad59926df2b3eb96d11ccde8a48ac70`, including
  Argo CD and Flux work; latest published release inspected was
  [v1.9.0](https://github.com/Sipaha/spk-ocular/releases/tag/v1.9.0).
  Features added after that tag must not be attributed to its downloadable binaries.
- Evidence was retrieved on October 21, 2026. Published articles are historical
  snapshots: the [first article](https://habr.com/ru/articles/1095984/) discusses
  the early project and 0.11.1 measurements; the
  [second](https://habr.com/ru/articles/1097014/) discusses progress through 0.19.
  Their future 0.20/0.21 work was already released at inspection.
- All 226 entries from the paginated GitHub issues endpoint were retrieved:
  224 issues and two pull requests. The issues comprised 205 closed and 19 open;
  222 were opened by `mxp100`, two by other accounts. All 44 comments returned
  for issues with discussions were read. These are retrieval-time counts,
  not adoption metrics or an independent security audit.
- Habr's public comments API returned 21 comments for the first article and
  zero for the second. Author replies and reader reports are distinguished below.
- Source snapshots and API evidence are attached to the operational research
  task. This document contains the assessment; progress and acceptance history
  belong to that task rather than a second task registry.

No competitor installer or third-party build script was run. No real cluster,
container, host namespace or user application was accessed for this analysis.

## Product comparison

“Broader” below refers to the verified presence of workflows, not a measured
advantage in correctness, ease of use or customer outcomes.

| Area | Ocular current source | Sextant inspected source/release | Assessment |
| --- | --- | --- | --- |
| Infrastructure | Kubernetes, Docker and Compose | Kubernetes-focused client | Ocular addresses the local-development-to-cluster workflow more broadly. |
| Delivery | Native desktop and loopback browser mode; six OS/architecture pairs in the documented release contract | Native Tauri desktop; Linux amd64/arm64, universal macOS, Windows x64 | Ocular has a browser mode and Windows arm64 coverage; Sextant has more convenient installation/update channels. |
| Core Kubernetes operations | Live tables, scoped discovery, YAML/schema tools, reviewed changes, logs, terminals, forwards, node tools | Corresponding core workflows, plus explicit generic apply/create, debug containers and Set image | Considerable overlap; generic creation and certain convenience actions are Ocular gaps. |
| Helm | Embedded Helm; Secret, ConfigMap and PostgreSQL storage; reviewed lifecycle | Native Secret-based release reads; installed Helm binary for changes, repositories and search | Ocular has a concrete deployment/dependency and storage advantage. |
| Argo CD | Dashboard, Applications, ApplicationSets, Projects, repository/cluster projections; reviewed Sync/Prune and Refresh | Native resources/actions, controller installation, API-backed desired/live diff and saved login | Sextant has additional operations; Ocular deliberately avoids a second Argo credential/session system for its projections. |
| Flux | Dashboard, 14 supported types, generation-aware states, scoped links, conservative ownership evidence | 13 specialized types in the inspected registry, reconcile/with-source, suspend/resume, controller installation | Sextant is more operational; Ocular has a more cautious status/identity model in inspected paths. ExternalArtifact is present in Ocular's specialized list. |
| Metrics and efficiency | Current CPU/RAM inspection; historical metrics/right-sizing are planned separately | Prometheus/VictoriaMetrics history, recommendations, cost adapters and request metrics | Substantial Sextant advantage when the required telemetry exists. |
| Networking | Resource relationships, declared-route graph, NetworkPolicy selection/risk inspection | Gateway API request matching, Service/Ingress diagnosis, policy reasoning, Istio/Linkerd, traffic metrics/graph | Sextant offers a much more complete troubleshooting workflow. |
| Reliability | Problems, Events Timeline, resource investigation | Problems plus opt-in practice checks, autoscaling analysis and cleanup candidates | Sextant connects more observations to operator questions. |
| Security/RBAC | Selected hardening checks, existing Trivy reports, declared-role provenance, self access review | PSS-oriented inspection, Trivy, RBAC, broader network-policy model | Both have meaningful tools. Ocular's selected checks must not be described as full PSS conformance. |
| Backup and storage | Ordinary storage resources; Velero is roadmap-only | PVC usage, snapshots, Velero backup/restore object creation | Clear specialized-workflow gap for Ocular. |
| Ecosystem | Generic Custom Resources plus selected specialized workspaces | Numerous operator adapters, including Argo Rollouts/Workflows, Crossplane, Kyverno, Knative, databases, ingress and secret controllers | Sextant's ecosystem breadth is materially larger. Generic CRD visibility is not equivalent to semantic adapters. |
| Investigation context | Persistent layouts, pins and ended-stream references; pairwise resource comparison | Saved views/workspaces with export/import, namespace comparison, Helm/last-applied drift, observed change timeline | Different strengths; Sextant covers more team handoff and fleet comparison scenarios. |
| Extensibility | No equivalent public sandboxed UI extension system found in current implementation map | Sandboxed iframe extensions, SDK, consent and backend guard | Sextant has an extension platform; existence of an SDK does not establish a third-party ecosystem. |
| Agent integration | Local HTTP/JSON over same-user socket/pipe, discovery schemas, grants, plans, bounded logs/exec | MCP HTTP and stdio bridge, guarded operations and six diagnostic tools | Sextant is easier to attach to common MCP clients and provides richer ready-made diagnosis. |
| Learning | Offline explanations for 37 types and six authored local modules, eight languages | Operational explanations, schema Explain and diagnostics; no corresponding offline task laboratory found | Ocular has a defensible learning distinction, with learner effectiveness still unverified. |
| Languages/accessibility | Eight languages and keyboard/focus behavior | Fourteen listed languages and a dedicated accessibility effort | Sextant has broader listed language coverage; neither translation count proves native-speaker quality. |

Ocular evidence: [usage](usage.md), [architecture](architecture.md),
[Argo CD](argocd.md), [Flux](fluxcd.md), [security](security.md),
[RBAC](rbac-explanation.md), [comparison](resource-comparison.md),
[workspace continuity](workspace-continuity.md), [learning](learning.md),
[agent API](agent-api.md), [release contract](releases.md) and
[remaining work](backlog.md). Current unreleased features remain explicitly
separate from the published release.

Sextant's concrete implementation references are
[Helm ADR](https://github.com/FriendsOfKubernetes/sextant/blob/be00e44ed19f7c2f0c17de40ebdca31e3a84c1a2/docs/adr/0005-helm-native-read-cli-write.md),
[Flux model](https://github.com/FriendsOfKubernetes/sextant/blob/be00e44ed19f7c2f0c17de40ebdca31e3a84c1a2/src/features/flux/model.ts),
[Flux actions](https://github.com/FriendsOfKubernetes/sextant/blob/be00e44ed19f7c2f0c17de40ebdca31e3a84c1a2/src/features/flux/actions.tsx),
[storage design](https://github.com/FriendsOfKubernetes/sextant/blob/be00e44ed19f7c2f0c17de40ebdca31e3a84c1a2/docs/adr/0086-volume-usage-is-read-from-kubelet-stats-or-prometheus-and-storage-actions-only-create-new-objects.md),
[workspace design](https://github.com/FriendsOfKubernetes/sextant/blob/be00e44ed19f7c2f0c17de40ebdca31e3a84c1a2/docs/adr/0083-saved-views-and-workspaces-live-in-the-settings-and-an-export-names-clusters-by-reference-only.md),
[comparison design](https://github.com/FriendsOfKubernetes/sextant/blob/be00e44ed19f7c2f0c17de40ebdca31e3a84c1a2/docs/adr/0090-comparison-and-drift-are-read-only-normalized-in-the-window-and-secrets-are-compared-by-hashes-that-never-leave-the-core.md),
[extension guard](https://github.com/FriendsOfKubernetes/sextant/blob/be00e44ed19f7c2f0c17de40ebdca31e3a84c1a2/docs/adr/0051-extension-calls-are-checked-in-rust-by-one-guard-shared-with-the-mcp-server.md)
and [release assets](https://github.com/FriendsOfKubernetes/sextant/releases/tag/v0.21.0).

## Where the difference matters in real work

### A broken external request

Ocular can expose objects, relationships, Events, logs and declared policy
selection. The engineer still assembles much of the explanation. Sextant offers
one path from Ingress/Gateway through route, Service and endpoints, with policy
reasoning and missing inputs represented. The benefit is fewer transitions and
less reconstruction of Kubernetes semantics, not another table in the sidebar.

Its answer is computed from configuration, not a network probe. CNI enforcement,
DNS state, application listeners and the actual proxy configuration can differ.
The [connectivity design](https://github.com/FriendsOfKubernetes/sextant/blob/be00e44ed19f7c2f0c17de40ebdca31e3a84c1a2/docs/adr/0082-connectivity-is-reasoned-from-policy-objects-and-answers-unknown-rather-than-allowed.md)
explicitly uses unknown for incomplete or unmodelled policy evidence. Copying a
boolean “reachable” indicator would lose the important part of that design.
[Traffic diagnosis requirements](https://github.com/FriendsOfKubernetes/sextant/issues/208)
make the intended workflow concrete.

### An oversized or throttled workload

Sextant reads historical CPU/memory series and supplies resource suggestions.
Its rules are fixed heuristics: CPU p95 with headroom, peak memory with headroom,
additional handling of OOM and throttling. Known history shorter than six hours
produces no recommendation; unknown history length can still produce one,
labelled unknown coverage. Current metrics-server usage alone does not suffice.
These are proposals, not proof of an optimal resource allocation or savings.

Ocular's Resources learning module teaches a bounded synthetic example, not
live workload sizing. It must not be credited with this production feature.
A future adapter should preserve sampling/coverage, workload identity through
rollouts, and the distinction between a patch and the Git/Helm source of truth.
[Recommendation implementation](https://github.com/FriendsOfKubernetes/sextant/blob/be00e44ed19f7c2f0c17de40ebdca31e3a84c1a2/src/features/rightsizing/recommend.ts),
[metrics/history design](https://github.com/FriendsOfKubernetes/sextant/blob/be00e44ed19f7c2f0c17de40ebdca31e3a84c1a2/docs/adr/0070-right-sizing-reads-fixed-promql-and-recommends-by-fixed-rules-without-changing-the-cluster.md).

### GitOps inspection and action

Sextant offers more buttons and installation workflows. Ocular's Flux API is a
bounded, typed, UI-only projection, rather than another raw-object consumer;
its status considers current generation and its local inventory navigation
resolves current identity explicitly. Helm labels alone do not establish a manager.

A reproducible isolated source probe of Sextant's unchanged `readyStatus` and
`readyTone` returned `True`/`success` for a Kustomization at generation 2 whose
Ready condition observed generation 1. The Flux column calls these functions
directly. The finding is narrow: this path renders the reported condition
without generation qualification. It is not proof that the whole application
calls that resource reconciled, and the application itself was not run.
Ocular explicitly tests that case as Unknown. This is a useful example of why
feature depth cannot be measured by the number of types or buttons.
[Model](https://github.com/FriendsOfKubernetes/sextant/blob/be00e44ed19f7c2f0c17de40ebdca31e3a84c1a2/src/features/flux/model.ts),
[column rendering](https://github.com/FriendsOfKubernetes/sextant/blob/be00e44ed19f7c2f0c17de40ebdca31e3a84c1a2/src/features/flux/columns.tsx),
[Ocular tests](../internal/providers/kubernetes/fluxcd_test.go).

Sextant's Argo desired/live diff goes through an Argo API session and loopback
forward, with saved login support. That supplies information Ocular's Kubernetes
projection does not promise, at the cost of another authentication and lifetime
boundary. TLS verification is disabled for that internal loopback client; plain
HTTP requires explicit server configuration rather than a TLS-error fallback.
This is a documented tradeoff, not evidence of a demonstrated exploit.
[Argo transport ADR](https://github.com/FriendsOfKubernetes/sextant/blob/be00e44ed19f7c2f0c17de40ebdca31e3a84c1a2/docs/adr/0027-argocd-api-via-port-forward-without-tls-check.md).

### A local application and its cluster deployment

Ocular combines Docker/Compose and Kubernetes in the same workspace and carries
resource, logs, files and reviewed-operation concepts across providers. Embedded
Helm avoids requiring another installed executable; alternative storage drivers
matter for users whose release storage is not Secret-based. These distinctions
are more concrete than “another free Lens”. They still need external users to
validate whether they cause adoption or reduce investigation time.

### A newcomer learning Kubernetes

Ocular supplies deterministic local practice without a kubeconfig, account or
cluster. The six modules reuse its normal inspection surfaces and require
observations and repairs rather than only a correct multiple-choice answer.
Sextant's explanations assist live operations, but the inspected feature map has
no equivalent task laboratory. This is a distinction in intended learning flow;
it is not proof of learning transfer, and no beginner study is claimed.

## Architecture, safety and performance

Both products use a native host with React and a system webview. Rust/Tauri
versus Go/Wails does not establish memory, responsiveness or safety by itself.
Sextant has generated Rust/TypeScript boundary types, feature registries and a
public extension SDK. Ocular has provider-neutral API/stream/view contracts,
multiple transports and typed specialized projections. Those choices support
different expansion paths and impose different maintenance costs.

Sextant's large-list implementation progressively delivers pages, caps displayed
collections, pushes supported selectors to the server and reuses watches.
Its main table cap is 20,000 objects and its broad-list warning threshold is
10,000; failed counts do not permanently block the capped list. Those choices
improve first rows while requiring visible completeness limits. Ocular's
inventory gate, compact selected collections and complete-list readiness use
a different policy. “First rows” must not be compared with “all selected rows”.
[Scale design](https://github.com/FriendsOfKubernetes/sextant/blob/be00e44ed19f7c2f0c17de40ebdca31e3a84c1a2/docs/adr/0091-large-clusters-get-progressive-capped-lists-a-count-guard-server-side-selectors-and-one-shared-watch-per-list.md),
[Ocular measurements](performance.md).

There is no fair Ocular/Sextant speed or RAM ranking yet. Sextant's published
0.11.1 result of 326 to 376 MB PSS for 44 to 544 pods plus 150 CRDs belongs to
that release and setup. Ocular's documented 10,000-Pod run included a browser
process tree, with a 557.947 MiB sampled maximum across valid sessions; it was
not the same native WebKit workload. Ocular's original stability criterion was
NOT ACCEPTED; the owner-selected reassessment is INCOMPLETE because a separate
plateau/control was not established. Neither permits a leak-free claim.
[Corrected Sextant measurements](https://github.com/FriendsOfKubernetes/sextant/issues/170),
[Ocular methodology and limits](performance.md).

A useful paired benchmark would hold object bytes, namespace selection,
permissions, controllers, runtime mode, window, hardware and build mode equal.
Measure startup, first rows and complete readiness separately; aggregate all app
processes; record PSS, private dirty, peak and sustained memory, API requests and
bytes. Include quiet watch, churn, reconnect, many details, terminal flood and
return-to-idle. This is proposed validation, not a run performed here.

Agent safety is also a comparison of tradeoffs. Ocular's same-user socket/pipe
avoids a TCP bearer endpoint and its plans bind object/configuration identity.
Caller names are self-reported and grants are shared among local agents; this
is not authenticated per-agent isolation. Exec can access mounted credentials.
Sextant provides easier MCP integration and more operations, including port
forwarding and separately permissioned Secret values, with fresh guards,
confirmations and an audit trail. Broader capability is useful and increases
what its permissions must constrain. Neither is a sandbox against an arbitrary
host process or proof of safety for every granted operation.
[Ocular protocol](agent-api.md),
[Sextant guard/confirmation design](https://github.com/FriendsOfKubernetes/sextant/blob/be00e44ed19f7c2f0c17de40ebdca31e3a84c1a2/docs/adr/0042-agent-confirmations-in-the-app-window-and-activity-log.md).

Sextant's diagnostic tools reuse window-side engines, then validate and filter
results in Rust. The design includes bounded requests, cancellation, scope-aware
inputs and recomputed summaries. This is more useful to an agent than returning
raw objects alone, but dependent on the live webview and careful provenance of
every input. The [diagnostic bridge ADR](https://github.com/FriendsOfKubernetes/sextant/blob/be00e44ed19f7c2f0c17de40ebdca31e3a84c1a2/docs/adr/0094-agents-get-the-windows-diagnostic-answers-through-a-checked-request-to-the-window.md)
records that evolution. A copied summary can disclose hidden existence or policy
even when the visible object list was filtered.

## What the feedback actually says

### Habr readers and author replies

The first article's comments contain distinct, useful signals:

- A reader contrasts a slow/paid GUI with familiar `kubectl` and asks whether
  agents will replace much of the interface. This is a positioning hypothesis,
  not a measured user migration. A GUI should justify itself through evidence,
  review and investigation context, while exposing bounded automation.
  [Discussion](https://habr.com/ru/articles/1095984/#comment_30542170).
- The Radar thread asks for concrete feature comparisons and corrects a broad
  characterization of a competitor. The author's safety and superiority claims
  are advocacy, not independent evidence. For Ocular, show reproducible workflows
  and avoid asserting that browser UI or native UI determines security.
  [Reader response](https://habr.com/ru/articles/1095984/#comment_30547046).
- Readers introduce Radar, Rubick and OpsDeck. Rubick and OpsDeck are assessed
  separately in the [extended comparison](competitor-landscape.md); this thread
  itself is not independent evidence of their capabilities.
  [Links](https://habr.com/ru/articles/1095984/#comment_30549538).
- The name thread shows that discoverability and associations can distract from
  product value. It does not establish demand for a name change in Ocular.
  [Thread](https://habr.com/ru/articles/1095984/#comment_30542296).

The sample is small and self-selected. Positive responses are not retention,
production adoption or validation of every feature. Zero comments on the second
article is a retrieval observation, not evidence of disinterest.

### External bug reports

The two issues opened by other accounts are practical rather than exotic:

| Evidence | What was reported | Implication for Ocular |
| --- | --- | --- |
| [#175](https://github.com/FriendsOfKubernetes/sextant/issues/175), closed | `proxy-url` failed because the required kube HTTP proxy feature was disabled | Real connection environments, corporate proxies and authentication recovery deserve validation before another operator adapter. Closure is not proof that every proxy topology works. |
| [#176](https://github.com/FriendsOfKubernetes/sextant/issues/176), closed | Windows row menu clipped at the right edge with Russian labels | Native edge positioning, long translations, scaling and resize matter more than screenshots of a central wide-window happy path. Private user screenshots need not be published to preserve a useful report. |

These reports identify a category of acceptance work. They do not demonstrate
that the same bugs occur in Ocular; no such reproduction is claimed.

### Maintainer measurements and security reviews

The remaining issue corpus is mainly implementation planning and self-review.
That is useful engineering evidence, not 222 independent user votes.

| Evidence and current qualification | Reusable lesson |
| --- | --- |
| [#170](https://github.com/FriendsOfKubernetes/sextant/issues/170): original swap-pressured result corrected; partial optimizations shipped; compact JSON [PR #203](https://github.com/FriendsOfKubernetes/sextant/pull/203) was still open | Quote corrected release measurements, distinguish allocator high-water, transient peak and retained growth; do not count an unmerged PR as delivered. |
| [#124](https://github.com/FriendsOfKubernetes/sextant/issues/124): PTY shutdown issue fixed in comments; memory observations remain | Read the latest discussion before calling every item in an open issue an active defect. |
| [#125](https://github.com/FriendsOfKubernetes/sextant/issues/125): historical debug-build flood/detail peaks; fixture cleanup fixed; profiling remains | Sustained terminal/inspection workloads and full process trees belong in performance validation. Old peaks do not describe current release behavior without remeasurement. |
| [#173](https://github.com/FriendsOfKubernetes/sextant/issues/173): log-growth suspicion closed as a non-reproduced false alarm | Switching screens can masquerade as a stream leak. Hands-off soak and controlled attribution matter. |
| [#221](https://github.com/FriendsOfKubernetes/sextant/issues/221), closed: policy lists read outside agent scope before output filtering | Admission must constrain reads, not merely sanitize a final answer. The issue explicitly says no data leaked to the agent; do not inflate it into a demonstrated exfiltration. |
| [#169](https://github.com/FriendsOfKubernetes/sextant/issues/169), closed: guessed Secret values distinguishable through write outcomes | A read restriction is insufficient if conflict/immutable responses form an oracle. Check every write and error path that can reveal a protected value. |
| [#91](https://github.com/FriendsOfKubernetes/sextant/issues/91), open: CSP changes shipped; live webview checks remain | A unit-tested sandbox policy still needs validation in each actual runtime. Do not report already-fixed CSP items as current exploits. |
| [#20](https://github.com/FriendsOfKubernetes/sextant/issues/20), [#21](https://github.com/FriendsOfKubernetes/sextant/issues/21), [#82](https://github.com/FriendsOfKubernetes/sextant/issues/82), [#83](https://github.com/FriendsOfKubernetes/sextant/issues/83), open | Installer trust, hands-on macOS/Windows, real cloud authentication and live MCP clients are separate from successful CI builds and mocks. |
| [#217](https://github.com/FriendsOfKubernetes/sextant/issues/217), open with partial delivery | Homebrew/Scoop/rpm/arm64 are present; winget's first publication remains pending. A partially open umbrella issue is not a missing entire feature. |

Sextant has signed updater artifacts, while Apple/Microsoft installer signing
is still absent. These are different trust mechanisms. Ocular also lacks that
vendor signing and currently has no in-app update mechanism. Sextant's update
and package-manager experience is therefore a substantive delivery advantage,
not a contradiction in its signing claims.

## General assessment and priorities

For an experienced Kubernetes engineer seeking a wide local operations console,
Sextant currently offers the stronger feature proposition. For a developer moving
between Compose and Kubernetes, or a newcomer learning through local tasks,
Ocular offers a distinct workflow. Neither segment preference is a demonstrated
usability-study outcome.

The highest competitive risk is that “free, local, native, no account, controlled
AI access” is now shared positioning. Both products can implement another CRD
view quickly. A sustainable distinction needs measurable task outcomes: successful
connection in difficult environments, time to a supported diagnosis, confidence
in a reviewed write, continuity after reconnect, and learning transfer.

Recommended order for evaluating further Ocular work, without authorizing its
implementation:

1. **Operational trust and onboarding.** Demonstrate supported authentication and
   proxy paths, long-session behavior, native platform usability and honest
   release contents. Improve installation/update friction as a selected product
   decision. Do not advertise a proven RAM plateau before obtaining one.
2. **Connected diagnosis.** A Service/Ingress/Gateway-to-endpoints explanation,
   linked to existing Events/logs/RBAC/policy evidence, would close a more valuable
   gap than a long list of standalone CRD tables. Preserve unknown and coverage.
3. **Reliability and efficiency.** Evaluate opt-in best-practice checks and
   history-backed resource sizing. Observed current usage, recommended capacity
   and actual cost must remain distinct.
4. **Investigation handoff and agent usability.** Assess saved investigation
   context/export and a standard MCP adapter over existing scoped plans/readers,
   rather than a second grant engine. Preserve current narrower permissions
   unless their expansion is explicitly selected.
5. **Choose ecosystem adapters from actual use.** Velero is already a separate
   roadmap item; add semantic adapters only when the user's environments and
   tasks justify them. Generic CRDs remain useful alongside specialized views.

Continue the Docker/Compose-to-Kubernetes and offline-learning strengths in
parallel with validation of whether users value them. Neither a mobile client,
a full extension marketplace nor parity with every newly listed operator should
become an automatic response to this comparison.

No weighted numerical overall score is assigned: there is no common benchmark,
independent user study or production support dataset that could justify one.
The supported conclusion is an ordinal one: Sextant leads in Kubernetes feature
breadth, diagnostic integration and update convenience; Ocular differs in provider
coverage, embedded Helm, browser availability, cautious selected projections and
local learning. Both remain young products with important real-world validation
and maintenance risks. A larger test count and more frequent releases do not
resolve those risks by themselves.
