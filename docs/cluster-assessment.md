# Cluster configuration, reliability and efficiency assessment

Market and source review: 2026-10-15 (Asia/Novosibirsk). Ocular baseline:
`20891bb9883f85edb87944b945bd993ef842049b`, VERSION 1.6.2.
The owner explicitly includes overall configuration effectiveness, not only
Security. Recommendations below are product proposals, not implementation or
cluster-change authorization. Operational records live in
`spk-ocular-cluster-assessment-research` and the linked task cards.

## Product conclusion

Build a local assessment workflow that combines a small set of explainable native
checks with results from existing specialist tools. Configuration quality,
reliability, measured resource efficiency, financial costs, security and upgrade
compatibility are separate dimensions. A single cluster score would conceal
coverage gaps and conflicting policies.

The strongest references for the owner's clarified request are **Polaris and
Popeye** for configuration/reliability, **KRR and Goldilocks/VPA** for rightsizing,
and **OpenCost/Kubecost** for financial context. Kubescape, Trivy and kube-bench
remain useful security-specific components rather than the complete answer.

## What each evidence layer can establish

| Evidence | Useful conclusions | Cannot establish alone |
| --- | --- | --- |
| Current manifests and resource relationships | Missing probes/requests, selector/port errors, rollout risks, declared policy conflicts | Actual peak demand, measured latency, realized savings |
| Current metrics.k8s.io sample | CPU/RAM observed at the sample time | Weekly demand, transient peaks, safe long-term downsizing |
| Historical Prometheus/cAdvisor/KSM data | Usage distribution, throttling, trends and workload attribution when labels/UIDs suffice | Application SLO compliance without application signals; unobserved demand |
| Existing VPA recommendations | A recommender's target/bounds and policy, with current conditions | Guaranteed safe values or a newly performed Ocular assessment |
| Node inventory and scheduling constraints | Requests/allocatable ledger, declared placement restrictions and known disruption blockers | Exact rescheduling/bin-packing or the ability to remove a particular node |
| OpenCost/cloud billing data | Cost attribution and modeled idle/cost opportunities | Guaranteed cash savings after changing a request |
| Security/policy reports | Evaluated controls, source verdicts and exceptions | Complete compliance when checks were skipped, denied or absent |

Requests released, capacity potentially consolidatable and actual money saved
must be separate quantities. A fixed on-prem cluster may gain headroom without
reducing its bill; a cloud cluster may retain nodes because of pool minima,
PDBs, topology, reservations, local storage or disruption budgets.

## Tool landscape

| Tool | Role and inputs | Integration lesson and limits |
| --- | --- | --- |
| [Polaris](https://polaris.docs.fairwinds.com/) | Live/resource-file configuration policy checks; dashboard, CLI and webhook modes. Reliability, security and resource-setting policies; custom JSON Schema rules | Strong native-check vocabulary and structured audit input. Use audit/read-only mode; remediation/webhooks are separate actions. A policy finding is not measured inefficiency |
| [Popeye](https://github.com/derailed/popeye) | Live cluster linter: references, endpoints/ports, probes, resource settings, potentially unused objects, current utilization | Particularly relevant to a local client. Borrow rules carefully: incomplete inventory cannot prove unused, and snapshots cannot justify rightsizing |
| [KRR](https://github.com/robusta-dev/krr) | Local Python CLI querying existing Prometheus-compatible history, cAdvisor and KSM; structured JSON/CSV/Markdown recommendations | Best first rightsizing research reference. Import reports before embedding a Python runtime or invoking kubeconfig helpers. Core analysis is separate from its optional Enforcer and SaaS integrations |
| [Goldilocks](https://goldilocks.docs.fairwinds.com/) / [VPA](https://kubernetes.io/docs/concepts/workloads/autoscaling/vertical-pod-autoscale/) | Goldilocks creates VPA objects and displays the VPA recommender's results; VPA Off computes recommendations without applying them | Read existing VPA CR status first. Do not silently create VPAs or install controllers. Prometheus is optional for VPA/Goldilocks, unlike KRR's historical query workflow |
| [OpenCost](https://opencost.io/docs/integrations/api/) | Cost allocation API using metrics and pricing; workload/idle/shared costs | Optional existing-backend adapter. Preserve currency, time window, allocation method and price provenance; no invented universal node price |
| [Kubecost](https://www.ibm.com/docs/en/kubecost/self-hosted/3.x?topic=apis-container-request-right-sizing-recommendation-api-v2) | Cost visibility and request-rightsizing recommendations with configurable parameters | Learn the recommendation-to-cost workflow. Do not assume its APIs/features are included in OpenCost or identical across editions |
| [KubeLinter](https://github.com/stackrox/kube-linter) | Static YAML/Helm checks for security and production readiness, with configurable checks | Good future preflight of rendered/proposed manifests. Not a live historical performance analyzer |
| [kube-score](https://github.com/zegl/kube-score) | Static object-set checks: probes, resource limits, policy selection, disruption/placement | Relationships need the appropriate object set. It can process exported live objects, but remains a static checker; no measured sizing |
| [kubeconform](https://github.com/yannh/kubeconform) | Schema validation against selected Kubernetes/CRD schemas | Useful editing gate; a schema-valid manifest can still be unreliable or wasteful. Missing CRD schemas must remain unknown/skipped |
| [Kubescape](https://github.com/kubescape/kubescape) | Broad posture/framework evaluation of files or clusters; CLI and optional operator/runtime components | Prefer versioned report import/adapter. Node evidence and runtime deployment are distinct from API-only scanning; evaluate permissions and data transmission by mode |
| [Trivy Operator](https://aquasecurity.github.io/trivy-operator/latest/docs/crds/configaudit-report/) | Persisted configuration, RBAC, infrastructure, compliance and vulnerability report CRs | Lowest-friction extension of Ocular's existing VulnerabilityReport adapter: ConfigAuditReport and RbacAssessmentReport first. Reading reports is not running a scan |
| [kube-bench](https://github.com/aquasecurity/kube-bench) | CIS checks of Kubernetes component/node configuration, processes and files | Needs host-level evidence for relevant checks; kubeconfig alone is insufficient. Managed control-plane visibility may be unavailable. Import results, do not deploy privileged collectors from Refresh |
| [Kyverno](https://kyverno.io/docs/guides/reports/) | Policy enforcement and background evaluation, PolicyReport/ClusterPolicyReport; OpenReports alternative | Read existing results. Preserve pass/fail/warn/error/skip, engine/rule identity and exceptions. Reports represent current resources, not full admission or historical audit logs |
| [Gatekeeper](https://open-policy-agent.github.io/gatekeeper/website/docs/audit/) | Rego/constraint enforcement and periodic audit | Its constraint status is a distinct adapter, not automatically PolicyReport. Violation lists are capped; compare reported list with totalViolations and audit timestamp |
| [Policy Reporter](https://kyverno.io/docs/subprojects/policy-reporter/) | Aggregation/UI/notifications for policy reports, not an independent scanner | Study result navigation, filters and producer identity; Ocular need not install another dashboard to read supported CRs |
| [Pluto](https://pluto.docs.fairwinds.com/quickstart/) / [kubent](https://github.com/doitintl/kube-no-trouble) | Deprecated/removed API discovery from manifests and supported in-cluster/Helm evidence | Use explicit target versions and distinguish source manifests from converted API responses. Neither proves full upgrade readiness |
| [Karpenter](https://karpenter.sh/v1.15/concepts/disruption/), [Cluster Autoscaler](https://github.com/kubernetes/autoscaler), [Descheduler](https://github.com/kubernetes-sigs/descheduler) | Provisioning/consolidation/scaling/eviction controllers | Inspect their conditions and blockers. They are actuators, not passive scanners to start as part of assessment |

For specialized RBAC analysis, KubiScan and rbac-tool are references, but they do
not assess workload efficiency. Ocular already has binding provenance and
current-connection authorization checks. Do not conflate a risky declared rule,
effective authorization and a demonstrated privilege-escalation path.

## Commercial references

- [Fairwinds Insights](https://www.fairwinds.com/insights) combines configuration
  policies, reliability, rightsizing, costs and developer remediation workflows.
  It is the closest broad product reference for this request.
- [CAST AI](https://docs.cast.ai/docs/about-the-read-only-agent) separates read-only
  inventory/cost recommendations from optimization authority. Its platform agent
  sends selected cluster data; this is not Ocular's local-only deployment model.
- [ScaleOps](https://scaleops.com/) emphasizes automatic resource allocation and
  rightsizing; [StormForge](https://docs.stormforge.io/v2/about/) describes historical
  recommendations, configurable cost/reliability goals and optional application.
  These are reference workflows, not independently benchmarked superiority claims.
- [ARMO](https://hub.armosec.io/docs/attack-path) and
  [Sysdig](https://docs.sysdig.com/en/sysdig-secure/posture/) represent posture/risk
  prioritization platforms. Their broader security workflows should not displace
  resource-efficiency work in Ocular's clarified scope.

Prices, procurement availability in Russia, private API contracts and savings
claims were not validated. No SaaS account or cluster connection was created.

## Findings from source inspection

KRR `simple` at `8b4e9e338739e1dfd7ca2e1dcc9f62008e73f2e0` defaults to CPU
percentile 95, memory maximum plus 15%, a 100-point minimum and an HPA guard.
The CPU proposal takes the maximum of the returned per-Pod percentile estimates;
it is not a single pooled cluster percentile. CPU limit is unset by this strategy.
Memory request/limit use the proposed maximum-based value. Insufficient samples
or an applicable HPA guard produce an undefined recommendation. Optional OOM
handling is explicitly experimental in source. These defaults are a versioned
strategy, not universal best practice or an Ocular default.

This exposes a real policy conflict: Polaris has a `cpuLimitsMissing` check and
kube-score can require CPU limits, while KRR's simple strategy proposes no CPU
limit. Preserve both the policy requirement and recommendation rationale; do not
average their scores or automatically "fix" one with the other. HPA utilization
uses requests as a denominator, so rightsizing can also alter replica decisions.
VPA/HPA compatibility depends on which metrics each controls; a blanket claim
that any VPA and HPA can never coexist is wrong.

Polaris `ca9a859482ee6c3d324124f1e52b551d0bc45b3b` includes explicit rule files
for CPU/memory requests/limits, readiness/liveness, replica count, priority class
and `pdbDisruptionsIsZero`, alongside security rules. Existing tests include
success/failure fixtures. Reuse small rule ideas with applicability and tests,
not the assumption that every singleton or batch workload must follow identical
availability policy.

Trivy Operator `5171971e35c7b1d8a0e47a94a2326da4d97ff417` ConfigAuditReport
schema carries checkID, success, severity, title, messages, remediation,
container/JSONPath scope, scanner identity and updateTimestamp. Not all optional
fields are guaranteed. This is richer than a count of critical findings and fits
a resource-linked investigation view. Current upstream also defines InfraAssessment
reports; old articles naming CISKubeBenchReport are not a reliable current API list.

Kyverno currently documents `wgpolicyk8s.io/v1alpha2` reports and an optional
`openreports.io/v1alpha1` API. Detect served group/version, including per-resource
scope, rather than hard-coding a historical report shape or counting dual APIs
twice. Gatekeeper's capped constraint violations need separate completeness logic.

## Fit with Ocular and proposed sequence

Current [Security](security.md) has selected hardening/RBAC/NetworkPolicy checks
and Trivy VulnerabilityReport reading. It does not yet read ConfigAuditReport,
PolicyReport or VPA recommendations. Current CPU/RAM metrics are snapshots.
The earlier Radar list includes resilience and historical metrics, but only
partially covers this broader assessment; it did not specify rightsizing/cost
adapters or a consolidated assessment evidence model.

1. **Native configuration/reliability checks.** Promote the existing resilience
   proposal; use Polaris/Popeye as references. Keep resource-linked findings in
   existing investigation views, not a generic Overview page. Separate risks
   from currently observed incidents in Problems.
2. **Existing VPA recommendations.** A small read-only adapter can deliver
   sizing context without a new recommender or mandatory Prometheus installation.
   Show absent/denied/warming-up and current update mode explicitly.
3. **Historical metrics and explainable rightsizing.** Promote the existing
   Prometheus work as a prerequisite; prototype with versioned KRR report import.
   The report producer, algorithm, interval, coverage and HPA caveat stay visible.
4. **Existing configuration/policy reports.** Trivy config/RBAC first, then
   PolicyReport/OpenReports; reuse existing Security UI and bounded source handling.
5. **Capacity, placement and cost context.** Requests vs allocatable, measured
   usage, disruption blockers and potentially idle objects; OpenCost is optional.
   No automatic evictions/deletions or guaranteed savings claims.
6. **Offline audit imports/preflight.** Polaris/Kubescape structured reports,
   then preflight of rendered manifests if demand justifies it. Do not install
   every scanner or make a broad shell/plugin runner in the first iteration.

Detailed acceptance and evolving status belong to the task service. The updated
resilience/metrics tasks and new assessment cards supersede their earlier generic
P2 placement for this clarified product direction, while all implementation
remains unstarted.

## Common finding and recommendation contract

Keep source-native rule IDs and verdicts plus a normalized category. A record
needs target identity, resource group/kind/namespace/name/UID when available,
producer/version, evidence time/window, rule/strategy version, field or container,
observed values, recommendation, confidence/coverage, prerequisites and caveats.
Missing UID makes navigation unverified until explicitly resolved; a report is
not permission to open a same-named replacement. Imported results may refer to
an older configuration and must not silently become current verdicts.

Use statuses such as evaluated, failed, skipped, unavailable, stale and truncated.
Do not treat an empty report or inaccessible namespace as success. Deduplicate
display groups using known equivalence while retaining producer evidence;
different rule IDs/severities and exceptions must remain inspectable.
Local suppressions need scope, reason and expiry without writing cluster policy.

For efficiency, keep current requests/limits, observed distributions, algorithm
recommendations, reclaimable requests and modeled cost separate. Display the
window and representative sample coverage, account for restart/rollout epochs,
batch/seasonal peaks, throttled CPU and memory peaks censored by OOM. Storage
capacity needs actual usage data; a provisioned PVC size is not consumed bytes.
"No known consumer" is a review candidate, not proof that a PVC or Secret is safe
to delete: operators, external consumers and scheduled jobs may use it.

Changes stay in Ocular's existing reviewed mutation paths, with GitOps source
awareness. Initial integrations only read reports/recommendations; they never
install scanners, grant rights, run node collectors or enable auto-apply.

## Maintenance and reuse evidence

GitHub metadata and selected pinned source were checked separately from docs.
Observed latest releases: Polaris v10.2.5 (2026-09-18), Goldilocks v4.16.2
(2026-09-15), Kubescape v4.0.16 (2026-10-14), Trivy Operator v0.35.0
(2026-10-05), kube-bench v0.16.0 (2026-08-05), KubeLinter v0.8.3
(2026-03-10), Pluto v5.24.4 (2026-09-15), Policy Reporter chart 3.11.1
(2026-10-14). Chart and app versions are not assumed identical.

Popeye is not archived, but observed HEAD was dated 2025-04-14 and release
v0.22.1 dated 2025-01-28. kubent HEAD was 2025-01-11 and release 0.7.3
2024-08-30. Treat these as maintenance signals, not proof of abandonment.
kubeaudit is archived and should not become a new foundation. kube-hunter's
observed release was from 2022; it is an active security-testing tool, not an
efficiency analyzer. Current kube-score repository is `zegl/kube-score`.

Most core candidates identify Apache-2.0 licensing; KRR and kube-score identify
MIT. Popeye's GitHub license classifier returned NOASSERTION, but its inspected
LICENSE explicitly refers to Apache-2.0. KubiScan identifies GPL-3.0: direct code
reuse needs separate compatibility analysis. Favor report boundaries over copying
whole engines; preserve notices and review each dependency when importing code.
No code was imported by this research.

Some unauthenticated GitHub API requests hit rate limits; exact KRR/autoscaler/
Karpenter HEADs were obtained through git ls-remote instead. KRR's strategy was
then fetched at the pinned commit. Unavailable release lookups are unknown, not
evidence of no release. IBM's full Kubecost page returned 403; its indexed official
API description supports only the high-level capability comparison here.

This is primary-source research, not a benchmark or live cluster audit. No
scanner, operator, auto-remediator or hosted integration was run. No claim of
coverage, recommendation accuracy or savings on the user's clusters is made.
