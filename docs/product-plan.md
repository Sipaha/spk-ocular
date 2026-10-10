# Product and website direction

Competitor evidence reviewed: 2026-10-05. Priorities below are recommendations for validation and sequencing, not authorization to implement or publish them. Public official pages, documentation and repositories support competitor facts; advertised performance and customer outcomes were not independently benchmarked. Prices can change and vary by billing period, currency and region.

## Direction

Make Ocular an effective daily operations workspace for engineers moving between existing Docker and Kubernetes environments: find a problem, inspect evidence, review a change and verify the outcome. Keep the Helm lifecycle and existing operations reliable before starting speculative platform work.

The proposed primary audience is hands-on developers and small platform/SRE teams; consultants managing several environments and engineers using local coding agents are secondary audience hypotheses. Ocular should initially earn repeat use for specific tasks. Enterprise fleet governance, a container runtime and a CI/CD platform are different products.

The published website currently presents **Kubernetes and Docker in one workspace**, with real development-build screens and dedicated investigation, logs, Helm and agent-access sections. A recorded agent grant/review demonstration remains a possible addition. Local operation, open licensing, native presentation and AI integration are useful attributes, but none is established as unique.

The application now includes standalone Docker containers as well as Compose
projects. The confirmed terminology direction is **Kubernetes and Docker**, with
Compose described as a grouping/workflow capability. The website reflects this coverage with localized copy and real Docker screens; the application
change does not imply a new container runtime, full image administration or an
expansion of agent project grants.

## Funding model and website promise

Ocular remains fully free for individuals and companies of any size, revenue or
funding. All product features are available without a paid tier, seat charge,
trial expiry or revenue-based eligibility threshold. Development is supported
through voluntary cryptocurrency donations. Donations do not unlock features
or imply investment returns, ownership, priority support or an SLA.

The homepage states this alongside the primary download action and in its free-use section:

- English hero: **All features free. For you and your company.**
- Both languages state that all features are free for individuals and companies.
- Supporting eligibility statement: free for companies regardless of revenue or funding.
- Primary action: download; secondary action: support development.

The support section already explains optional cryptocurrency donations. It does
not contain payment details. Adding networks, copyable addresses or QR codes
requires owner-supplied, verified wallet information; never invent addresses. Avoid forced prompts, donation gates and implying that
payment guarantees a requested feature. Publishing payment details is a
separate authorized step.

Competitors' paid capabilities remain useful product research. Their paywalls,
revenue thresholds and seat licensing are not the proposed Ocular business model.

## Baseline and claim boundaries

Current repository documentation describes live resources/discovery, namespace selection, problems and metrics, relationship navigation, logs, terminals, local forwarding, reviewed Kubernetes changes and a local agent API. It describes Linux/macOS/Windows delivery, a native system webview, a loopback browser mode, Apache-2.0 licensing and no required product account, hosted backend or telemetry. Verify the released version before marketing any capability.

The agent API provides explicit grants and review controls. Names supplied by agents are **not authenticated identities**; grants apply to local agents under the same OS user. This is an error-prevention and audit boundary, not isolation from malicious same-user processes. Secret-value access, terminal access or additional write capabilities must not be inferred from the existence of an API.

The Helm lifecycle is implemented: chart/version browsing, repositories, values, install, upgrade, history, resources, rollback and uninstall. Named additive agent permission groups and scope/group switches are also implemented. Behavior and verification boundaries are documented in [usage](usage.md), [agent access](agent-api.md) and [development](development.md). Distinguish development-build demonstrations from functionality available in a published release. Helm has SDK/fake-API tests and an explicit disposable kind/PostgreSQL integration target covering lifecycle, failures, cancellation and upstream SQL compatibility. Validation is bounded by those fixtures, not every cluster policy, database deployment or chart. Read-only [resource comparison](resource-comparison.md) is implemented in development builds: two explicitly selected resources across connected targets, normalized saved YAML and optional service fields. It is not yet released. Other relevant gaps include generic creation/multi-document apply, Compose desired-state lifecycle, simultaneous workspaces, persistent user action history and distribution/update improvements. Current documentation discloses unsigned Windows installers, ad-hoc/non-notarized macOS bundles and the Linux runtime baseline.

**Website baseline:** the redesigned Astro site is published at
[the product homepage](https://sipaha.github.io/spk-ocular/), from the independent
`pages` branch. It includes localized real app screens, themes, a gallery, mobile
navigation, free-commercial-use messaging, agent trust boundaries, installation
and signature guidance, SEO/social metadata and no-JavaScript download fallbacks.
Its source workflow builds, checks and deploys the website; publishing the site
does not create an application release or verify native package installation.
See [website direction and remaining work](site-plan.md).

See the consolidated [comparison tables](comparison.md) for capability, funding
and positioning matrices with explicit unknowns.

## Competitive landscape

| Category | Products and relevant expectations | Implication |
| --- | --- | --- |
| Direct graphical Kubernetes clients | [Lens](https://lenshq.io/), [Aptakube](https://aptakube.com/), [K8Studio](https://k8studio.io/), [Kubernetic](https://www.kubernetic.com/), [Freelens](https://github.com/freelensapp/freelens), [Headlamp](https://headlamp.dev/), [Seabird](https://github.com/getseabird/seabird), [Radar](https://radarhq.io/). Repeated themes: complete operations, logs, relationships, workspace continuity and quick setup. | Compare complete workflows. Free/open alternatives mean pricing alone cannot establish a durable advantage. |
| Terminal substitutes | [K9s](https://k9scli.io/) and [Lazydocker](https://github.com/jesseduffield/lazydocker). Keyboard navigation and compact task execution matter. | Keep the GUI efficient for experienced operators and make shortcuts discoverable. |
| Local runtime/container tools | [Docker Desktop](https://www.docker.com/products/docker-desktop/), [Rancher Desktop](https://rancherdesktop.io/), [Podman Desktop](https://podman-desktop.io/), [OrbStack](https://orbstack.dev/); [Dockge](https://github.com/louislam/dockge) focuses on Compose stacks. | Test interoperability; do not advertise replacing VM/runtime/build functions Ocular does not provide. |
| Deployed organizational platforms | [Portainer](https://portainer.io/), [Devtron](https://devtron.ai/), [Komodor](https://komodor.com/), [Rancher](https://www.rancher.com/) and [OpenShift Console](https://github.com/openshift/console). Governance, delivery, support and fleet outcomes dominate. | Borrow focused workflows without importing an entire platform roadmap. |
| Other form factors | [kubenav](https://github.com/kubenav/kubenav) targets mobile operations. | Useful audience evidence; mobile is not a proposed immediate scope expansion. |
| Historical/migration references | [OpenLens binary builds](https://github.com/MuhammedKalkan/OpenLens) say not to expect updates; [Kubernetes Dashboard](https://github.com/kubernetes-retired/dashboard) and [Octant](https://github.com/vmware-archive/octant) are archived. [Monokle](https://github.com/kubeshop/monokle) states maintenance/evolution is unavailable; its [site](https://monokle.io/) announces commercial Cloud EOL. | Label status explicitly. Use relationship, validation and migration lessons without presenting retired tools as equal-status maintained competitors. |

K8Studio and Radar are particularly relevant additional comparisons: local operation and AI/MCP coexist with visual investigation workflows. Lens now positions both a Kubernetes IDE and a separate governed-agent platform. Therefore a generic AI headline would obscure Ocular's more concrete value. [K8Studio](https://k8studio.io/), [Radar](https://radarhq.io/), [Lens](https://lenshq.io/).

## Paid capabilities and lessons

| Product | Public offer observed | What to learn |
| --- | --- | --- |
| Lens | Helm is in eligible free Personal. Plus/Pro: $25/user/month annual equivalent ($300/year), $30 monthly; Enterprise $50 annual equivalent, $60 monthly. Paid capabilities include AI/MCP, GitOps, cloud integrations and security; organization tiers add collaboration/administration, with offline activation and identity/provisioning in Enterprise. Personal eligibility has an organizational revenue/funding threshold. [Plans](https://lenshq.io/pricing/) | Complete Helm is baseline. Paid value combines daily productivity with organizational adoption requirements. |
| Aptakube | 15-day trial; personal 79/year and team 59/seat/year in observed localized currency. Independent page fetches showed USD and EUR; use a specific locale/currency when publishing. Both include all app features; team terms add licensing/support/SSO. [Pricing](https://aptakube.com/pricing) | A cohesive daily tool can be sold without gating every advanced workflow. |
| K8Studio | Basic $9/month, Professional $17/month in the observed monthly view; Airtight $187/year; 15-day trial. Advanced visual investigation, AI, security/RBAC and Helm support its higher tier. [Pricing](https://k8studio.io/pricing/), [AI workflow](https://k8studio.io/ai-assistant/) | Reviewed AI operations and local models already exist in direct competitors. Compare scope, revocation and stale-plan behavior instead of claiming uniqueness. |
| Kubernetic | Desktop €60/year; on-prem team app €8/user/month. Team includes organizational/integration features. Its pricing/docs differ on Tekton availability; do not claim confirmed parity there. [Pricing](https://www.kubernetic.com/pricing), [docs](https://docs.kubernetic.com/) | Private chart repositories and a clear path from personal tool to team workflow matter. |
| Docker / OrbStack | Docker annual equivalents: Pro $9, Team $15, Business $24/user/month; runtime/service bundles make direct GUI price comparisons misleading. OrbStack Pro $8/user/month billed annually, with commercial use and support/debugging value. [Docker](https://www.docker.com/pricing/), [OrbStack](https://orbstack.dev/pricing) | Installation, runtime compatibility and daily convenience deserve investment. Do not infer an unverified paid boundary for Docker Debug. |
| Portainer | Three-node free Business entry; Starter displayed from $1,045/year and Scale $2,095/year, with node/CPU/support conditions; custom Enterprise. [Pricing](https://portainer.io/pricing) | Buyers also pay for support, scale and procurement. Do not divide entry prices into a universal per-node price. |
| Devtron | OSS plus enterprise offers; free enterprise tier limited to one cluster/50 vCPUs/10 users; Starter $999/month and Growth $2,000/month. [Pricing](https://devtron.ai/pricing) | Commercial value belongs to organizational scale and standardized delivery, not just a richer table. |
| Komodor | Current plans use custom pricing with a platform fee plus AI token usage. Human approval, agent controls and audit are explicit value. [Pricing](https://komodor.com/platform/pricing-and-plans/) | Sell verified operational outcomes; avoid reusing older per-node pricing or treating testimonials as benchmarks. |

Lens IDE's documented MCP is read-only, distinct from Lens Agents; K8Studio documents reviewed mutations. Ocular's review controls are therefore a meaningful specific comparison, not a category-wide exclusive. [Lens MCP](https://docs.lenshq.io/k8slens/mcp-server/how-it-works/), [K8Studio AI](https://k8studio.io/ai-assistant/).

The agreed investigation feature shortlist is maintained in [the backlog](backlog.md#investigation-workbench). The Events API snapshot timeline and RBAC explanation are implemented in development builds; persistent local observation history remains separate.

## Product priorities and concrete ideas

P0 means a baseline or adoption prerequisite. P1/P2 are proposed validation order, not dates or delivery promises. Additional implementation starts only when requested.

| Priority / idea | User value and concrete behavior | How to validate / important boundary |
| --- | --- | --- |
| **P0 — First successful connection** | Clear requirements, reliable packages, existing context discovery and actionable credential-helper/RBAC errors. | Fresh isolated OS profiles; measure installation failures and time to first useful inspection. Signing/notarization/updater changes need their own release scope. |
| **P1 — Explain workload failure** | A focused trail from rollout to pod condition, events, previous exit and relevant bounded logs. | Seed image-pull, probe, OOM and scheduling failures; measure correct diagnosis and false confidence. Show missing evidence; extend existing views instead of adding a generic Overview. |
| **P1 — Incident evidence bundle** | Preview and locally export selected status/events/manifests/log excerpts with timestamps and coverage for handoff. | Recipient diagnoses a fixture with fewer follow-up requests. Test secret fixtures; excluding Secret objects alone cannot sanitize logs/config. |
| **P1 — Compare environments** | Explicitly pair staging/production resources; compare images, probes, limits and normalized YAML with unmistakable target identity. | Find planted differences faster than context switching. Distinguish absent from forbidden/unavailable; no automatic cross-target writes. |
| **P1 — Custom columns and view presets** | Preview bounded JSONPath-derived fields and save common investigation layouts, especially for CRDs. | Observe reuse in a second session; measure detail-panel openings avoided. Cap expression cost and preserve masking. |
| **P1 — GitOps ownership awareness** | Show controller/source/revision clues before an edit that may be reconciled away; link to the durable source when known. | Users choose appropriate edit paths for managed/unmanaged fixtures. Inference is labeled; start with inspection rather than automatic Git actions. |
| **P1 — Change receipt** | Record the requested action, exact target/version, known result and subsequent observed health for the operator. | Reconstruct an earlier failed operation and decide whether retry is safe. Exclude secrets; rollback is operation-specific, not universal Undo. |
| **P1 — Expiring task access** | Grant a bounded investigation scope for a visible duration; pause/revoke from one place and inspect pending changes. | Measure overgranting and users' ability to explain remaining access. Expiry can extend current grants; authenticated per-agent credentials require a separate design. |
| **P2 — Preserve investigation context** | Pinned details, log filters and restorable layouts reduce navigation repetition. | Three-workload task-switch exercise. Restore disconnected; do not replay terminal commands or silently persist sensitive drafts. |
| **P2 — Explain reachability** | Trace Ingress/Gateway → Service → EndpointSlice → ready Pods and show broken selectors/ports. | Seed selector, port and readiness faults. API relationships do not prove packet delivery or every network-policy effect. |
| **P2 — Compose-to-cluster comparison** | Explicitly pair a local service and deployed workload; compare image, environment names, mounts, ports and checks. | Interview engineers using both, then test a narrow prototype. Avoid exposing environment values or promising automatic equivalent conversion. |
| **P2 — Observed change timeline** | Bounded local history explains a transient failure after it disappears. | Reproduce short-lived changes and reconstruct only recorded evidence. Mark observation gaps, UID replacement and retention limits; no retrospective audit claim. |
| **P2 — Evidence-based resource tuning** | Show requests/limits against measured usage with source and sample duration. | Test whether users know when evidence is insufficient to change limits. Instant samples cannot justify long-term rightsizing or dollar savings. |
| **P2 — Safe shared runbooks** | Declarative view/filter/action templates transfer a team's troubleshooting knowledge. | A newcomer solves a fixture with less help. Imports never grant rights or execute commands; defer arbitrary plugin/shell execution. |

Narrow public demand signals support testing manifest review, configurable columns and workload logs, but do not establish market-wide demand or today's unfixed state: [Freelens manifest-review request](https://github.com/freelensapp/freelens-ai-extension/issues/87), [custom columns](https://github.com/freelensapp/freelens/issues/2244), [aggregated logs](https://github.com/freelensapp/freelens/issues/687). Ocular already has relevant logs/review primitives; demonstrate and improve them before adding another generic chat interface.

## Remaining website priorities

The redesign and initial publication are complete. Preserve the implemented
RU/EN pages, themes, task sections, localized screenshots, unrestricted free-use
promise, same-user agent boundary, mobile navigation and robust download fallback.
The remaining work concerns the complete installation journey and deeper guides.

| Priority | Remaining change | Verification |
| --- | --- | --- |
| P0 | Keep validating the download/install journey for each published release | Stable packages are available; preserve anonymous downloads, checksum verification and native runner installation checks |
| P1 | Extend the existing FAQ and installation guidance into quickstart, trust and task pages | Explain actual credential/helper/Secret/network behavior and reproducible troubleshooting tasks |
| P1 | Add a Compose workflow and agent grant/review demonstration | Actual app on disposable fixtures; version labels and accurate scope/revocation behavior |
| Conditional | Add donation network/address/QR details when supplied and authorized by the owner | Verify all representations against the owner's source; retain optional support with no feature gates |
| P2 | Add sourced comparisons, routes/sitemap and reproducible performance evidence | Dated facts, versions/hardware/fixture methodology; no invented social proof or speed claims |

The [site plan](site-plan.md) describes current presentation and the remaining
work. Use the website's existing checks for future changes and verify the actual
public pages after an authorized deployment.

## Promotion and measurable experiments

Observed competitor surfaces include downloadable desktop trials and alternative/task pages at [Aptakube](https://aptakube.com/); package-manager/ecosystem installation at [Headlamp](https://headlamp.dev/); community/docs at [K9s](https://k9scli.io/); and education, case studies, partners and technical sales at [Portainer](https://portainer.io/). These establish channel existence, not traffic or effectiveness.

| Experiment | Delivery proposal | Measurement and decision |
| --- | --- | --- |
| First-use cohort | Recruit 8–12 consenting engineers across development/SRE/consulting; observe a disposable diagnosis with their normal tool and Ocular | Installation success, correct diagnosis, time to first useful finding, wrong-target attempts and repeated confusion. Qualitative cohort, not population statistics. |
| Homepage comprehension | Test two message/demo orders with recruited participants | Can they explain the product, limits and next step without coaching? Improve repeated misunderstandings before traffic acquisition. |
| Useful technical content | Prepare three task guides with reproducible fixtures and short actual recordings | Task completion, qualified download intent and explicit feedback. Page visits alone do not establish product adoption. |
| Community demonstration | Prepare transparent maintainer posts for relevant developer/SRE communities, respecting rules | Number and quality of task attempts, reproducible reports and repeat volunteers. Publication/contact requires separate authorization. |
| Distribution pilot | Evaluate Homebrew/WinGet/Linux channels after package quality and publisher requirements are settled | Fresh-install success and support burden per platform; advertise only channels actually released. |
| Voluntary return-use study | Ask participants to keep a brief one-week task diary | Which normal workflow was replaced and repeated, and why users returned or stopped. Do not infer retention from release downloads. |
| Team adoption test | Give interested teams an architecture/permissions brief and one shared runbook prototype | Can a second engineer adopt it without the maintainer? Capture concrete policy/support blockers before enterprise development. |

A future funnel can separate qualified visit, download, successful installation, first target inspection and repeat useful task. Initially use moderated sessions and opt-in self-reports; application telemetry and website analytics are separate privacy/product decisions. No collection is implied by this plan.

## Sequence and decision gates

1. Keep existing Helm, Agents and resource workflows regression-tested. Market shipped capabilities accurately; publication and release creation require their own authorization.
2. Remove first-use friction and demonstrate already available workflows with reliable installation/docs/screenshots.
3. Validate failure explanation, incident handoff and environment comparison; choose one complete workflow with repeated observed benefit before expanding breadth.
4. Extend the published website with the remaining installation/task guides and targeted distribution experiments; verify real visitor workflows before promotion.
5. Validate organizational identity, collaboration and support needs after repeat adoption, keeping product features free. Evaluate voluntary donation support without turning feature access or company eligibility into paid packaging.
