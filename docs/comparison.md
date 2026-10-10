# Competitive comparison

Competitor evidence checked 2026-10-05; the Ocular column describes current
repository behavior, including explicitly marked unreleased development work. This compares documented products, not measured
performance. “Not verified” means no reliable entitlement/capability conclusion
was established; it does not mean the feature is absent. Prices and plans can
change. Ocular entries describe the current source, not the availability of published release assets.

## Direct and adjacent products

| Product | Free commercial use | Primary scope | Documented differentiators | Paid value / funding | Audience and site emphasis |
| --- | --- | --- | --- | --- | --- |
| Ocular | All features free for individuals and companies, without revenue/funding limits | Local Kubernetes and Docker operations, including Compose projects | Explicit connection; live resources/logs/terminal/forwarding; reviewed changes; scoped local agent grants | Voluntary cryptocurrency donations; no feature paywall | Developers and small platform/SRE teams; published site emphasizes daily workflows, unrestricted free use and visible review |
| [Lens](https://lenshq.io/pricing/) | Personal eligibility restricted by organizational revenue/funding | Desktop Kubernetes IDE; separate Lens Agents platform | Broad Kubernetes/Helm; paid GitOps/cloud/security; IDE MCP documented read-only | Plus/Pro $25/user/month annual equivalent or $30 monthly; Enterprise higher | Individual engineers and enterprise teams; ecosystem, adoption proof, AI/governance and enterprise trust |
| [Aptakube](https://aptakube.com/pricing) | Trial, then paid | Desktop Kubernetes | Simultaneous cluster aggregation, resource comparison, merged logs | Personal 79/year, team 59/seat/year in observed localized currency; 15-day trial | Hands-on engineers/consultants; polished daily work, multi-cluster/log/diff demos and alternative/task pages |
| [K8Studio](https://k8studio.io/pricing/) | Trial, then paid | Desktop Kubernetes | Topology, timeline, docking, integrations; MCP and reviewed mutations, local model support | Basic $9/month, Professional $17/month in observed monthly view; Airtight $187/year | DevOps/SRE and architects; visual investigation, local access, AI, trial-led funnel |
| [Freelens](https://github.com/freelensapp/freelens) | Free MIT project | Desktop Kubernetes | Maintained community desktop IDE with built-in Helm lifecycle, extensions and broad packaging | Donations/community model | Engineers seeking a free IDE; GitHub/releases/community and migration from old OpenLens builds |
| [Headlamp](https://headlamp.dev/) | Open-source; no paid tier found in reviewed official pages | Desktop or deployed Kubernetes UI | RBAC-aware controls; official App Catalog Helm plugin bundled with desktop; extensible views | Community/ecosystem support | Operators/platform teams; extensibility and many installation channels |
| [K9s](https://k9scli.io/) | Free Apache-2.0 project | Terminal Kubernetes | Keyboard workflows, XRay/resource relations, configurable views and reverse RBAC | Sponsorship | Experienced terminal operators; efficiency, documentation and community |
| [Kubernetic](https://www.kubernetic.com/pricing) | Paid products | Desktop and on-prem Kubernetes | Private Helm repositories, desktop operations, team integrations | Desktop €60/year; team €8/user/month | Individual users and private team deployments; straightforward desktop→team path |
| [Docker Desktop](https://www.docker.com/pricing/) | Personal eligibility restrictions; paid company plans | Container runtime/toolchain, Compose, local Kubernetes | Build/runtime integration, images, debugging and organizational administration | Pro/Team/Business; annual equivalents $9/$15/$24 per user/month | Developers and enterprise IT; complete development environment and standardization |
| [Portainer](https://www.portainer.io/pricing) | BE free entry for three nodes; OSS CE also exists | Deployed Docker/Swarm/Kubernetes management | RBAC/SSO, Git-driven deployment, drift/audit/change windows | BE Starter from $1,045/year, Scale from $2,095/year with plan-specific node/CPU conditions | Platform/IT teams; fleet management, support, education, partners and sales |
| [Radar](https://radarhq.io/) | Open-source local/in-cluster entry; hosted offer separate | Kubernetes investigation | MCP, topology, timeline and audit-oriented checks | Hosted tiers separate; exact paid boundary not fully verified | Engineers/platform teams; screenshots, local setup, docs and package ecosystems |
| [Podman Desktop](https://podman-desktop.io/features) | Community/open-source project | Local containers, pods, images and Kubernetes extensions | Image/registry workflows, pod creation and Kubernetes YAML flow | No paid desktop tier verified | Developers; open container workflow and ecosystem integrations |
| [OrbStack](https://orbstack.dev/pricing) | Noncommercial personal use free; commercial use paid | macOS container/Linux runtime | Runtime/networking convenience and debug shell | Pro $8/user/month billed annually | macOS developers; runtime experience and low setup friction |

Docker Desktop, OrbStack and runtime distributions are not interchangeable with
Ocular's operations UI. Portainer/Devtron/Komodor/Rancher are deployed platforms;
compare focused workflows without claiming full platform replacement.

## Feature comparison with explicit evidence boundaries

| Capability | Ocular working tree | Lens IDE | Aptakube | K8Studio | Relevant free alternatives |
| --- | --- | --- | --- | --- | --- |
| Free use regardless of company turnover | Confirmed product policy, all features | Personal has eligibility limits | Paid after trial | Paid after trial | Freelens, Headlamp, K9s have open-source models |
| Kubernetes and Docker in one operations workspace | Implemented standalone containers plus Compose grouping; desired-state Compose lifecycle and image mutations remain outside current coverage | Kubernetes focus; Compose parity not verified | Kubernetes focus | Kubernetes focus | Podman/Rancher Desktop cover local container environments with different scope |
| Full Helm lifecycle | Implemented; SDK/API/UI fixtures plus disposable kind/PostgreSQL integration cover lifecycle and failure paths | Documented baseline including charts/install/upgrade/rollback/uninstall | Release listing/values, upgrade, rollback and uninstall; local Helm binary required. New-chart install/catalog not established; tracked repository/OCI gaps | Documented in Professional | Freelens: built-in lifecycle (v1.10.3 source). Headlamp: official App Catalog (v0.9.1), bundled desktop; deployment/backend configuration matters |
| Merged/related logs | Implemented with bounds and explicit gaps/coverage | Logs available; exact aggregation limits not compared | Prominent documented workflow | Investigation workflow documented; exact limits not compared | K9s/GUI alternatives cover logs; quotas and semantics differ |
| Simultaneous cross-cluster aggregation | Not implemented; existing targets switch within a workspace | Views from several clusters remain open in tabs; combined-table semantics not established | Core differentiator: combined resource views | Simultaneous cluster tabs/docked panels; combined-table semantics not established | No blanket parity claim |
| Resource/environment comparison | Implemented in development builds: explicitly selected resources across connected targets, normalized read-only saved YAML diff and optional service fields; Secret payloads excluded | Paid editor compares current unsaved edits with the loaded/saved baseline; arbitrary two-resource diff not established | Core differentiator | YAML Compare explicitly diffs two selected objects; cross-cluster selection for this action is not established | No blanket parity claim |
| Agent inspection | Local agent API with scoped grants | Built-in MCP documented read-only | Not verified | MCP documented | Extensions/plugins vary |
| Reviewed agent mutations | Implemented for documented operations; same-OS-user trust boundary | Not in the documented read-only IDE MCP; separate Lens Agents is different | Not verified | Documented confirmation, RBAC and server dry-run | Plugin-dependent; do not assume absent |
| Topology / observed timeline | Relations/Problems, cluster graph and Events API snapshot timeline implemented in development builds; persistent observation history remains proposed | Exact paid workflow boundaries not fully compared | Not established by this study | Prominent documented paid workflow | Radar provides relevant topology/timeline examples |
| RBAC explanation / own authorization checks | Implemented in development builds: ServiceAccount binding/role provenance, self-only server checks and visible partial coverage; no dedicated role-management workflow | Exact workflow not reviewed here | Exact workflow not reviewed here | Professional documents RBAC management and user permissions | Inspection and management scopes differ; no blanket parity claim |
| Enterprise identity/governance | No authenticated per-agent identities or central organization service | Higher paid plans / separate products | Team licensing/SSO terms; not equivalent to cluster RBAC | Product/tier-specific | Open-source RBAC-aware UIs are not automatically identity platforms |

Sources for the detailed rows: [Lens Helm charts](https://docs.lenshq.io/k8slens/using-lens/helm/charts/),
[Lens releases](https://docs.lenshq.io/k8slens/using-lens/helm/releases/),
[Lens IDE MCP](https://docs.lenshq.io/k8slens/mcp-server/how-it-works/),
[Aptakube product](https://aptakube.com/),
[K8Studio AI](https://k8studio.io/ai-assistant/),
[K8Studio product](https://k8studio.io/),
[Ocular usage](usage.md), [Ocular agent permissions](agent-api.md).

Additional primary evidence: [Aptakube Helm release](https://aptakube.com/blog/aptakube-1.6),
[maintainer-confirmed uninstall/binary lookup](https://github.com/aptakube/aptakube/issues/387),
[repository management tracking](https://github.com/aptakube/aptakube/issues/381),
[OCI upgrade/source tracking](https://github.com/aptakube/aptakube/issues/313),
[Freelens published release](https://github.com/freelensapp/freelens/releases/tag/v1.10.3),
[Freelens release menu](https://github.com/freelensapp/freelens/blob/v1.10.3/packages/core/src/renderer/components/helm-releases/release-menu.tsx),
[Headlamp App Catalog release API](https://github.com/headlamp-k8s/plugins/blob/app-catalog-0.9.1/app-catalog/src/api/releases.tsx),
[Headlamp plugin/deployment details](https://github.com/headlamp-k8s/plugins/tree/main/app-catalog),
[Lens cluster tabs](https://docs.lenshq.io/k8slens/using-lens/layout/),
[Lens editor diff contract](https://docs.lenshq.io/k8slens/using-lens/advanced-editor/),
[K8Studio simultaneous panels](https://k8studio.io/features/manage-multiple-kubernetes-clusters/),
[K8Studio two-object diff](https://k8studio.io/features/yaml-compare/).
An open issue is a tracked unresolved workflow, not proof that every related
operation is absent. Source-confirmed competitor behavior was not executed in
this research. Concurrent tabs/panels, an aggregated table and cross-environment
diff are distinct capabilities, not interchangeable checkmarks.

## Wider landscape and status

The research also covers Rancher Desktop, Devtron, Komodor, OpenShift Console,
Rancher/Prime, Seabird and kubenav. Their form factors, public offers and product
lessons are in [product direction](product-plan.md). OpenLens binary builds,
Kubernetes Dashboard, Octant and Monokle Cloud must be labeled historical or
retired rather than presented as equivalent maintained commercial offerings.

## What to borrow, without copying paywalls

Full Helm lifecycle and named agent permission groups are already implemented;
they are part of the current baseline, not new feature proposals. The opt-in
kind/PostgreSQL integration target and its bounds are documented in
[development](development.md#real-helm-and-postgresql-integration). The ideas below
extend that baseline.

| Priority | Idea | Why it can matter | First validation |
| --- | --- | --- | --- |
| P1 | Explain a failure with linked evidence | Reduce time assembling pod/events/log context | Seed OOM, image-pull, readiness and scheduling failures |
| P1 | Compare working and failing environments | Find concrete image/config/probe/limit differences | Paired-resource test with planted differences |
| P1 | Incident evidence bundle | Hand off a problem without repeated copying and questions | Recipient diagnosis plus redaction/coverage tests |
| P1 | Change receipt and health follow-up | Understand what changed and whether retry is safe | Reconstruct an earlier failed operation |
| P1 | Expiring agent task permissions | Reduce broad grants left enabled after a task | Users explain granted scope and remaining access after expiry |
| P2 | Explain Service reachability | Reveal broken selectors, ports and ready endpoints | Controlled connectivity-failure fixtures |
| P1 | Custom columns / investigation presets | Make CRDs and team-specific fields usable | Reuse a saved view in a second session |

These are proposed improvements, not authorization to implement them. They remain
free under the confirmed Ocular model. The [site plan](site-plan.md) describes the
published `pages` website and its remaining improvements.
