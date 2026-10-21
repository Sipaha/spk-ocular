# Flux CD

The free **Flux** navigator section appears beside Helm/Argo CD only when the
connected API catalog discovers a supported Flux resource. API discovery selects
the served version; it does not prove that a controller is running or healthy.
Failed/partial discovery preserves previously confirmed kinds until absence is
confirmed. Opening navigation or selecting an unconnected target never connects it.

## Navigation and observations

Dashboard summarizes returned controller observations in the selected namespaces,
with ready totals per discovered kind, Needs attention filters and related
Kubernetes Events. Counts cover returned data, not an independently verified fleet
inventory. Event retention and API permissions limit the history.

Each discovered kind has an independent list and details page:

| Section | Supported kinds |
| --- | --- |
| Sources | GitRepository, OCIRepository, HelmRepository, HelmChart, Bucket, ExternalArtifact |
| Workloads | Kustomization, HelmRelease |
| Image automation | ImageRepository, ImagePolicy, ImageUpdateAutomation |
| Notifications | Alert, Provider, Receiver |

Only served supported kinds appear. Lists show name, namespace, reconciliation
state, status message, observed revision and age. Search and state filters narrow
returned data. Details expose conditions, observed/current generation, public
source/path/revision, typed controller references and reported Kustomization
inventory. **Open resource tools** uses the ordinary resource Properties/YAML/Events
and existing reviewed operations according to current API permissions. No new
Flux reconcile/suspend/resume endpoint or Git mutation is introduced.

Suspended comes from `spec.suspend`. Ready/Failed/Reconciling require a condition
observing the current object generation (with `status.observedGeneration` as the
fallback when the condition omits it). Missing/stale conditions remain Unknown;
truncated conditions cannot certify readiness. A current Stalled condition is
Failed. Suspension does not prove an already running reconciliation has stopped.

## References and managing resources

Source/dependency/provider/resource links use exact group, kind, namespace and
name, resolved only against uniquely loaded controller objects in the current
selection. A missing source, cross-selection reference or unknown API remains
inert. Dependent links reverse these explicit references, not inferred names.
Only recognized safe HTTP(S) repository URLs are clickable. GitHub commit links
require a GitRepository and an exact resolved SHA-1/SHA-256 revision; arbitrary
hosts, branches and chart versions do not become guessed commit links.

Kustomization inventory records do not contain resource UIDs. Opening one requires
a known served kind, selected namespace and local destination, then explicitly
resolves the current UID on this connection. A configured `spec.kubeConfig`
disables local destination links; Ocular does not follow it to another cluster.
Late answers and navigation recheck the existing unsaved-edit guard.

Current resource metadata may expose a **Flux tracking** hint. Properties can
explicitly look up managing controllers within the selected namespaces. A
Kustomization match requires current resource UID, pinned controller UID, local
destination, matching tracking metadata and exact reported inventory membership.
An exact ownerReference UID can establish a direct controller reference. Labels
alone do not prove HelmRelease resource membership; ambiguous or unverified
managers remain Unknown. This conservative limit can leave a real Helm-managed
workload unresolved.

Deployments, StatefulSets and DaemonSets gain a Flux column when Flux is detected.
It uses already loaded controller evidence, with no automatic inventory read.
Properties lookup or visiting Flux supplies evidence in memory for the exact
client, target, namespace selection and connection incarnation. The cache keeps
at most eight snapshots per client and never persists them.

## Read and access boundaries

`FluxCD` is an authenticated, Origin-checked, no-store UI endpoint shared by HTTP
and Wails. Virtual navigator kinds are UI-only; no additional agent grant or
controller mutation is implied. Ordinary resource grants remain independent.

The provider lists only discovered supported namespaced controller resources and
Events in the explicit namespace selection. An empty explicit set makes no source
calls. One API call is bounded by 75 seconds; individual source reads by 15 seconds,
128 selected namespaces, 2,000 input objects, 8 MiB raw input, 1 MiB per object,
2,048 total pages, 256 pages per source and 50 requested objects per page. Conditions,
references, inventory, text and Events have additional projection bounds. Repeated
pagination, malformed inventory, limits and denied reads retain visible coverage;
raw server-error details do not enter the projection.

The projection never follows Secrets, decryption/config references, chart values,
substitution values, notification credentials or webhook addresses/tokens. URL
userinfo, queries and fragments are removed from projected URL fields. Controller
messages are bounded observations, not a universal secret-redaction guarantee.
No Git/Flux CLI, remote destination, controller installation or credential store
is required or created. Ask AI is outside this feature.

## Custom Resources

The navigator labels the existing `API groups` section **Custom Resources** in all
eight languages. Its internal folding/favorites keys stay unchanged. The section
may also contain discovered aggregated APIs and uncommon built-ins: the label is
not proof that every entry is a CRD.

All third-party API resources, including the Argo CD and Flux CRDs, remain in
Custom Resources even when a dedicated section exists. Specialized pages are an
additional controller-focused presentation. Generic resource pages, schemas,
resource tools, relationship resolution and grants retain their full descriptors.
Generic and dedicated kinds have independent selections and favorites; neither
silently replaces the other or deletes saved entries.

## Verification boundaries

Tests use fake Kubernetes clients and owned HTTP/synthetic browser fixtures.
Controller-condition semantics and resource formats follow the official
[Flux controller documentation](https://fluxcd.io/flux/components/) and
[Kustomization inventory types](https://github.com/fluxcd/kustomize-controller/blob/main/api/v1/inventory_types.go).
The selected non-AI workflow is compared with the
[Lens Flux views](https://docs.lenshq.io/k8slens/flux-cd/using-flux-cd/).
These checks do not certify reconciliation on a real Flux installation, remote
clusters or every controller version. Release availability follows release notes.
