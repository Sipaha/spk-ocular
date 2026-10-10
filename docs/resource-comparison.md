# Resource comparison

Resource comparison is available in development builds; it is not yet released.
It compares two explicitly selected saved objects, including resources from
separate connected targets. It performs no apply, edit or other mutation.

## Select a pair

Open a resource's standard properties and choose **Use as comparison baseline**.
A compact baseline bar remains visible while navigating to another resource or
switching targets. Open the second resource's properties and choose **Compare
with baseline**. Ordinary navigation and target changes keep their existing
unsaved-edit guards. Pinning and opening the comparison preserve the current
draft. The draft itself is not part of the comparison.

Only resources with an observed UID on an explicitly connected target can be
selected. The selection pins provider, target, kind, scope, name, UID,
configuration revision and connection attempt ID. A closed/reconfigured/reopened
connection requires selecting the resource again. A same-name replacement is
refused rather than substituted. No target is connected implicitly.

## Read and normalize

Opening the dialog reads both provider-safe Resource YAML documents separately.
The reads are snapshots, not an atomic transaction or continuous watch. Each side
shows its target, object identity and read time. **Refresh** reads both again with
the same pinned identities. Failures do not produce a partial successful diff.
Closing, refreshing or invalidating a connection rejects obsolete responses.

Mapping keys are sorted; scalar values and array order are preserved. Names,
namespaces, labels, owner references, finalizers, meaningful annotations, specs
and other configuration differences remain visible. Normalization does not
perform API defaulting, pair containers by name, ignore environment differences
or claim semantic equivalence of differently ordered arrays.

For Kubernetes, the default view excludes top-level `status` and these top-level
metadata fields: `uid`, `resourceVersion`, `generation`, `creationTimestamp`,
`deletionTimestamp`, `deletionGracePeriodSeconds`, `managedFields`, `selfLink`.
The dialog lists fields actually omitted. **Show service fields and status**
switches between two normalized representations of the same snapshots without
new reads. Other providers retain all their ordinary resource fields.

Provider exclusions remain in force in both modes. In particular, Kubernetes
Resource.Get already omits managed fields and the last-applied annotation.
Secret payloads (`data`, `stringData`, `binaryData`) and their size masks are
always excluded from comparison. Matching Secret metadata is not evidence of
matching secret values. No revealed-value API is called.

Different kinds/providers can be compared explicitly, with a schema warning.
A no-difference result applies only to the displayed comparison fields, not
object health, access, hidden data or the complete environment.

## Bounds and lifetime

The UI-only CompareResources API uses admitted sessions, validates both
configuration and connection identities before reads and revalidates session
incarnations afterward. It enforces UID and scope identity even when a provider's
ordinary Get implementation does not do so. Existing agent grants expose no new
endpoint or data.

Each input and normalized representation is bounded to 1 MiB and 20,000 newline
characters, with nesting depth 64 and 100,000 YAML nodes. YAML must be one mapping;
duplicate keys, aliases, malformed input and multiple documents are refused.
64-bit integer precision is retained during JSON/YAML normalization. The entire read
has a 35-second deadline. The existing bounded Myers diff discloses approximate
results, folds long lines and exposes further rows in pages.

Only two references remain in memory between navigation actions. YAML belongs
to the open dialog and is discarded on close. Neither references nor comparison
contents are written to settings, recent-object data, journals or browser storage.
Clearing the baseline drops both selections. The modal traps keyboard focus,
closes on Escape/backdrop and restores focus to its opener when still present.
All controls and diagnostics are available in all eight UI languages.
