# Kubernetes Security workspace

Security is a read-only investigation workspace under **Cluster**. It reads an
explicit snapshot through the already connected target and the selected
namespaces. Refresh does not install a scanner, run containers, change grants,
connect another target or write cluster resources. An empty namespace selection
performs no inventory reads. Available from 1.3.0.

## Configuration findings

The table separates configuration, RBAC declaration, NetworkPolicy and image
vulnerability sources. It filters by source, severity and search terms; each
finding explains its observed fields and a recommendation. Open uses the standard
UID-pinned resource inspector and unsaved-edit guard. Refresh keeps an open editor;
failed reads retain a visibly stale previous snapshot. The shared resource grid uses 32 px virtualized rows, sortable headers and
mouse/keyboard column resizing. Widths persist per target and stable column ID,
across scopes and restarts; Escape cancels a drag and double-click restores the
default. Counts cover the filtered snapshot, not just the visible rows.

Configuration checks read Pod specifications and controller Pod templates from
Deployments, StatefulSets, DaemonSets, CronJobs, Jobs and ReplicaSets. Each resource and template is inspected independently so admission changes and
live Pod settings remain visible. Counts are resource/container findings; they
can overlap between a controller template and its Pods.
Checks cover privileged/Windows HostProcess containers, host namespaces, hostPath,
privilege escalation, non-root execution, writable root filesystem, capabilities,
seccomp, token auto-mount and default ServiceAccount use with auto-mount enabled.
Container settings override Pod-level defaults. Token auto-mount respects an
explicit Pod setting first, then the ServiceAccount setting. Missing/denied account
inventory cannot establish the default. Explicit projected token volumes need
separate review. Linux-specific hardening checks are omitted for explicitly
Windows Pods. Missing declared hardening is not proof of the process's actual
identity or node runtime defaults. These are selected risk checks, not full [Pod
Security Standards](https://kubernetes.io/docs/concepts/security/pod-security-standards/)
admission conformance or exploit tests.

Role and ClusterRole findings inspect literal API groups/resources/verbs and
resourceNames. Wildcards, Secret reads, delegation/impersonation and pods/exec
are reported as **declared rules**, including roles without any bindings. They do
not identify effective users or prove escalation. Use RBAC explanation to inspect
bindings and the current connection's server authorization.

NetworkPolicy checks use full label-selector semantics and effective policyTypes.
They report missing declared ingress/egress selection only after the relevant
policy inventory completed successfully. Denied, malformed or truncated inventories
cannot establish absence. CNI enforcement, permitted peers, host networking and
packet delivery are not inferred or tested.

Severity is a local prioritization for configuration rules, and the scanner's
severity for CVEs. No numeric cluster score, compliance certificate, attack path,
entry-point reachability or proven exploitation is claimed.

## CVE reports and setup guidance

Ocular reads `aquasecurity.github.io/v1alpha1` **VulnerabilityReport** objects
created by Trivy Operator. It projects vulnerability ID/title, severity, package,
installed/fixed versions, container, image repository/tag/digest, scanner/version
and report update timestamp. The report resource is the UID-pinned navigation
source when its kind is available in the current catalogue. Owner descriptions
are secondary plain text, never a guessed navigation target. Its owner description is not evidence that the currently running image
still matches: tags are mutable. Missing timestamps remain unknown; no freshness
is invented. Report naming conventions are not used to resolve identity.

An unavailable optional report API (404) is setup state with installation help,
not an incomplete-source error. A missing namespace, denied request or failed
read still appears in coverage diagnostics. Ocular distinguishes an unavailable
report API, empty selected namespaces,
permission denial, read failure, incomplete coverage and an empty namespace
selection. No reports never means zero vulnerabilities. If the API is absent,
the UI explains that **Trivy Operator must be installed in the cluster** and links
to [official installation](https://aquasecurity.github.io/trivy-operator/latest/getting-started/installation/helm/).
If only its CRD exists, the operator still needs to run. When no reports have
been produced, check scan jobs, watched namespaces and private registry access.
Permission denial instead requests `get/list` access to
`vulnerabilityreports.aquasecurity.github.io` in the selected namespaces and links
to [official report access guidance](https://aquasecurity.github.io/trivy-operator/latest/tutorials/manage_access_to_security_reports/).
Installation and permission changes belong to the cluster owner; Ocular performs
neither automatically. Official links open in the system browser on desktop and
a separate browser tab in browser mode. Configuration checks work without Trivy.

Reports are saved external scanner data, not a live scan initiated by Ocular.
The [official report schema](https://aquasecurity.github.io/trivy-operator/latest/docs/crds/vulnerability-report/)
explains their workload/container association and optional fields. This first
adapter does not read ExposedSecretReport or download/scan images, install a CLI,
contact an external vulnerability service or forward registry credentials.

## Bounds and coverage

The API is UI-only; there is no agent method or permission expansion. It needs an
admitted connection and cancellation follows its session incarnation. Snapshot
reads share the existing inventory gate. Limits: 75 seconds per API request,
20 seconds per source/namespace, 128 selected namespaces, 4096 pages overall,
512 pages per source, 50 objects requested per page, 6000 objects, 16 MiB of
inspected object JSON, 1 MiB per object and 12000 findings. Long projected strings
are limited to 512 UTF-8 bytes. Containers, RBAC rules and vulnerabilities per
object are bounded to 256, 1024 and 4096 respectively. Repeated pagination tokens
stop with visible truncation. Oversized or incomplete sources are disclosed.
Server page-size hints do not guarantee the server respects the requested limit;
local object, byte and finding bounds still apply after decoding a response.

Workload manifests are inspected in backend memory only. Environment values,
commands, volume paths, Secret/ConfigMap values and raw server error text are
not returned. Secret and ConfigMap inventories are never requested. Findings and
report contents are not persisted to workspace state, telemetry or agent grants.

Verification includes provider fake-client and real HTTP boundary tests, UI-only
session tests, filter/localization tests, real browser interaction and owned native
window captures. Synthetic/seeded evidence does not prove a real vulnerability,
scanner execution, CNI enforcement or compatibility with every operator version.
