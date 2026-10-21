# Radar comparison and reuse direction

Source review: 2026-10-14. Radar commit
`e9b97277fed92147f3380b85ca7269c98cb3c4f0`; Ocular commit
`20891bb9883f85edb87944b945bd993ef842049b` (VERSION 1.6.2).
Radar's latest published release observed was v1.16.0 at
`500cae5d2e90256fc69eb54f928ebe08ca99b01c`; the reviewed HEAD is 108 commits
ahead. This document compares source capabilities, not verified release parity.
No real clusters, runtime performance or installation compatibility were tested.
The GitOps baseline below predates the implemented [Argo CD](argocd.md) and
[Flux CD](fluxcd.md) workspaces; it is not a list of their remaining features.
The [consolidated roadmap](backlog.md#comparative-priorities) incorporates newer
Sextant/Rubick/OpsDeck evidence without discarding this study's source boundaries.
Upstream screenshots were inspected as design evidence, not as a live test.

The operational analysis, priorities, acceptance criteria and proposed work cards
live in the work task service under `spk-ocular-radar-analysis` and its linked
cards. Their status is authoritative; this document is a product comparison,
not a second task registry or authorization to implement.

## Main conclusion

Radar's strongest lesson is a connected investigation workflow: a symptom links
to an affected workload, supporting conditions/events, dependency failures,
recent changes and the next useful inspection. A longer inventory of pages is
less valuable than this connection between existing data.

Radar was already present in [product direction](product-plan.md) and
[the comparison matrix](comparison.md). The gap was shallow coverage of its
implementation, not a completely absent competitor. The available evidence
does not establish why the earlier research stopped there.

Ocular already has a native/local UI, discovery, logs/exec, forwarding, Helm,
resource comparison, RBAC explanation, security checks, a graph and Events
timeline. These are not new feature proposals. Ocular also has Docker/Compose
and explicit local agent grants; Radar's documented focus is Kubernetes.
These distinctions do not establish a universal winner or measured demand.

## Capability comparison

| Area | Radar source evidence | Ocular baseline | Useful delta |
| --- | --- | --- | --- |
| Problems and diagnosis | `internal/issues`, `internal/k8s/detect_*.go`, `pkg/hpadiag`, `pkg/configrefs`: grouped problems, dependency checks and explanations | `internal/providers/kubernetes/problems.go` merges Pod/controller/Service/Ingress/Node health and recent warnings; explicitly excludes Jobs, CronJobs, PVCs and CRs | Expand coverage, preserve current-vs-historical distinction, correlate evidence in the drawer |
| GitOps | Argo/Flux inventory, source/revision, controller conditions, dependency trees and operations; `pkg/gitops/insights` | Generic discovered CRDs; `edit.go:managedBy` already recognizes a managed-by label, Helm annotation and Argo tracking ID | Explain the reconciler and durable source, add typed health and dependency links; start read-only |
| Network diagnosis | `docs/reachability.md`, `internal/reachability`, `pkg/topology`: declared path plus optional probes with explicit vantage | [Graph](cluster-graph.md) contains declared routes and relationships, not observed traffic | Focused path with selector/port/readiness errors; no graph rewrite or automatic network tests |
| Table customization | `packages/k8s-ui/src/utils/custom-columns.ts`: label/annotation columns, stable IDs and persisted definitions | Built-in columns and server/CRD printer columns; widths persist | User-selected label columns and reusable layouts; arbitrary JSONPath is a separate extension |
| TLS | `pkg/certs`, `internal/server/certificate.go`: cert-manager and TLS Secret inventory, expiry, deduplication | Generic resources and value inspection; decoded certificates remain planned | Expiry/issuer/SAN/renewal status with links to consumers, explicit Secret read boundary |
| Helm diagnosis | `pkg/helmhistory`, operation insight and bounded live hook evidence | Full Helm lifecycle, hooks/manifests/history and guarded operations; declared resources are not live health | Explain failed/pending upgrades, compare useful revisions, open the actual failed Job/Pod/logs |
| Change history | `pkg/timeline`, `internal/k8s/history.go`, meaningful-change ranking; memory/SQLite/PostgreSQL | [Timeline](events-timeline.md) is an explicit Events API snapshot; no durable observed changes | Opt-in bounded local history with gaps, ownership/UID and selected field changes |
| Historical metrics | `internal/prometheus`, `docs/workload-metrics.md`: cAdvisor/KSM/Beyla/mesh attribution and coverage | `metrics.go` reads current metrics.k8s.io CPU/memory with UID attribution | Existing Prometheus CPU/memory/throttling charts with reliable cluster/workload identity |
| Agent integration | HTTP MCP, enriched diagnosis and minified context | Schema-described local HTTP over Unix socket/named pipe, explicit grants and reviewed writes | Thin opt-in MCP adapter over existing methods, then compact evidence tools |
| Audit/readiness | `pkg/audit`, `pkg/upgradereadiness`: security, resilience, storage, API/dependency checks | [Security](security.md) covers selected hardening/RBAC/policy risks and Trivy reports | Add a small set of resilience checks before undertaking versioned upgrade readiness |
| Image inspection | `internal/images`: registry layers and filesystem, including images of non-running containers | Container filesystem inspection through exec, including editing/downloads | Useful for distroless/crashed containers, but registry credentials, layer semantics and cache cost make it later work |
| Live traffic and costs | Hubble/Caretta/Istio/Beyla adapters; OpenCost integration | Declared routes and current usage only | Optional integrations after demand is established; Radar does not make telemetry appear without its sources |
| Shared team deployment | In-cluster Helm, OIDC/proxy auth, impersonation; Cloud-related code | Local desktop/loopback application | Different deployment and permission product; not an incremental MVP feature |

## Priority rationale

The subsequent [cluster assessment study](cluster-assessment.md) broadens the
owner's requested scope beyond Security to efficiency and promotes the native
reliability and historical-metrics proposals. Use its linked task records for
the current sequencing; the order below describes the Radar-specific comparison.

The proposed order targets hands-on developers and small SRE/platform teams
using Ocular daily. There is no measured usage funnel or customer survey behind
the ordering. Value is estimated from frequency, incident impact, audience reach,
reuse of existing Ocular machinery and ongoing maintenance cost. Effort includes
bounded reads, eight locales, fake-server/browser/native checks and documentation;
it is an estimate for one engineer familiar with the code, not a schedule.

The task service records these recommendations in order:

1. **Evidence-backed workload diagnosis**: broaden Problems, then correlate
   conditions, events, dependency checks and existing logs. Initial slice:
   Jobs/PVC/HPA plus Pod failure evidence; CronJob scheduling logic follows.
2. **Read-only Argo/Flux context**: controller/source/revision/health and ownership
   provenance. Preserve the existing edit warning and make it more useful.
3. **Declared network path diagnosis**: extend existing route resolution with
   explicit failed or unknown hops, starting with Service and Ingress.
4. **Label columns and saved view presets**: a smaller daily productivity win;
   start with labels, review annotation exposure separately.
5. **TLS inventory and expiry**: prevent a specific expensive failure with a
   bounded feature. cert-manager first, explicitly selected Secret reads second.
6. **Helm failure investigation**: live hook evidence and meaningful revision
   comparison on top of the existing lifecycle.
7. **Persistent observed changes**: strong incident value, but a new persistence,
   coverage and data-retention contract. Do not block current-state diagnosis on it.
8. **Existing Prometheus history**: high value but substantial identity, source,
   credential and query-budget complexity. Start with an explicit backend.
9. **MCP compatibility**: useful adoption work without building an embedded AI
   chat. Move up if agent users become the primary acquisition channel.
10. **Resilience checks, then upgrade readiness**: small deterministic rules first;
    maintain a separately versioned evidence catalog before making upgrade claims.

GitOps moves below columns/TLS when target users do not use Argo or Flux.
Prometheus moves up for incident-heavy teams that already have a query backend.
Package-manager distribution (Krew/Homebrew/Scoop) is a separate acquisition
experiment, not a substitute for installation/signing work already planned.

## Code reuse

Both projects use Go, client-go, React and TypeScript. Radar's extracted
`github.com/skyhook-io/radar/pkg` module currently uses Kubernetes 0.37.1,
matching Ocular's Kubernetes dependencies. This makes selected pure rules and
fixtures promising candidates, but does not prove drop-in compatibility.

| Candidate | Recommended treatment | Integration cost |
| --- | --- | --- |
| `pkg/certs/certs.go` | Adapt certificate DTO aggregation, thresholds and tests; keep Ocular-specific source coverage/UIDs | Low for pure rules; Secret acquisition needs its own design |
| `pkg/hpadiag`, `pkg/rolloutdiag`, selected `pkg/configrefs` rules | Adapt deterministic rules and negative fixtures behind the Ocular provider | Medium; bind evidence to current identities and translate messages |
| `pkg/helmhistory` | Reuse history classification ideas/tests via an adapter | Medium; Radar uses Helm v3 while Ocular embeds Helm v4.3.0 |
| GitOps insight/tree types | Reuse diagnosis vocabulary, cases and source resolution selectively | Medium/high; avoid global cache and request-handler dependencies |
| Label-column utility | Reimplement the small mapping in Ocular's grid; preserve exact key semantics | Low frontend logic, additional backend projection/persistence work |
| `pkg/timeline` and meaningful changes | Borrow event/schema/query patterns, not the storage subsystem wholesale | High; Ocular's session/scope/Secret invariants are different |
| Prometheus attribution | Use queries and coverage tests as references | High; prove cluster and historical ownership attribution independently |
| Shared React package or entire server/cache | Do not adopt wholesale | Different navigation, design, providers, lifecycle and editor stack |

Radar's desktop uses Wails v2, React Flow/ELK topology and Monaco; Ocular uses
Wails v3, a worker/canvas graph and CodeMirror. Replacing Ocular's graph/editor
to gain a feature would import a much larger change than the feature requires.
Radar's `internal/*` packages are not importable from Ocular under Go's internal
visibility rules. Extract/adapt small code with provenance or use public packages
behind an adapter; pin a commit and inspect the reachable dependency graph.

The inspected root [Radar license](https://github.com/skyhook-io/radar/blob/e9b97277fed92147f3380b85ca7269c98cb3c4f0/LICENSE)
is Apache-2.0, as is Ocular. Redistribution must retain applicable notices and
license text, and identify modified files; include upstream NOTICE attribution
when applicable. The inspected source archive contained no separate NOTICE file.
Check the selected files and their dependencies again when importing, record the
commit and modifications, and integrate notices with Ocular's existing
[license collection](releases.md#third-party-licenses). The license does not grant
branding rights. See the [license terms](https://www.apache.org/licenses/LICENSE-2.0).

## Important limits when borrowing behavior

- **GitOps diff is not necessarily Git diff.** `pkg/gitops/insights/drift.go`
  compares live state with last-applied for that path, explicitly lacks that
  evidence for SSA/Helm and uses selected hard-coded defaults. Show the actual
  baseline/source; no annotation means unavailable, not no drift.
- **Secret field parsing is not field-limited API access.** Kubernetes GET/LIST
  returns the Secret object. Reading only `tls.crt` in Go does not mean `tls.key`
  never entered backend memory. Start from cert-manager status and explicit reads;
  return only public certificate metadata and do not retain values.
- **History is observed evidence.** Disconnection, late start, retention and UID
  replacement matter. Do not store arbitrary ConfigMap/env/log contents and rely
  on pattern redaction to make them safe. Start with an allowlist of field changes.
- **Missing is not denied or unobserved.** Radar's missing-ref code already gates
  authoritative absence on cache coverage. Ocular must retain visible unknowns,
  rather than silently skipping every inaccessible dependency.
- **Network diagrams are not packet evidence.** Radar differentiates probe
  vantage; its automatic proxy probe on opening a tab is not proposed for Ocular.
  Active probing or creating Jobs requires a separate reviewed action design.
- **Metrics names alone are not identity.** Shared Prometheus backends can carry
  same-named Pods from multiple clusters or old UIDs. Missing counters and coverage
  must not become zeroes or fabricated historical ownership.
- **MCP is a transport, not an authority.** Do not replace Ocular's grant and
  Prepare/Run controls with client confirmation hints, or expose UI-only APIs.

## Verification and follow-up boundary

This is a source/documentation analysis. Source paths above and upstream tests
were inspected; the tests were not executed and no upstream performance claim
was benchmarked. Actual deployed user clusters were not accessed. Latest public
release metadata was checked separately from HEAD. Implementation cards require
their own behavioral tests, clean builds and actual UI captures before delivery.
No production code, dependencies or application behavior changed in this review.

Primary upstream references:
[source tree](https://github.com/skyhook-io/radar/tree/e9b97277fed92147f3380b85ca7269c98cb3c4f0),
[GitOps](https://github.com/skyhook-io/radar/blob/e9b97277fed92147f3380b85ca7269c98cb3c4f0/docs/gitops.md),
[reachability](https://github.com/skyhook-io/radar/blob/e9b97277fed92147f3380b85ca7269c98cb3c4f0/docs/reachability.md),
[metrics](https://github.com/skyhook-io/radar/blob/e9b97277fed92147f3380b85ca7269c98cb3c4f0/docs/workload-metrics.md),
[MCP](https://github.com/skyhook-io/radar/blob/e9b97277fed92147f3380b85ca7269c98cb3c4f0/docs/mcp.md).
