# Cluster graph

The cluster graph workspace is an opt-in inventory canvas
for an explicitly connected target. It shares the existing namespace selection
and resource details drawer. Multiple selected namespaces form separate regions;
cluster-scoped resources remain a separate region. Missing permissions and
incomplete discovery must remain visible rather than implying complete coverage.

The additional route layer represents declared Service/Ingress (and available
Gateway API) routes, not measured traffic. It requires no collector, in-cluster
agent, telemetry endpoint or infrastructure changes. It must not invent packet
counts, throughput, latency or observed connections. Enabling the layer preserves
the layout, viewport and selected resource.

The implementation uses compact value-free inventory snapshots, indexed
relationship resolution, worker-based layout and a canvas with semantic zoom.
Pan/zoom changes only the camera, never recomputes the graph or React node trees.
Selection uses Ref + UID and the standard drawer, including unsaved-edit guards.

## Inventory and rendering

`ClusterGraph` is UI-only in both browser and native transports. It requires an
explicit admitted connection and is absent from the agent method registry and
agent kind catalog. Session shutdown cancels inventory reads. Each session
serializes graph requests; at most six source lists run concurrently. Explicit
namespace sets use only per-namespace lists, with unscoped sources read once.
Pagination is followed; repeated continuation tokens and inventory limits are
reported. Secret and ConfigMap inventories use PartialObjectMetadata lists, so their
contents are not requested. Responses contain only identity, kind, health and relationships, never
YAML, annotations, environment values or Secret/ConfigMap contents.

Ownership resolves immutable UIDs rather than names. Selectors use namespace
label indices; references use kind/namespace/name indices and pin known UIDs.
Known Pod/template configuration/storage references, RBAC, HPA, events and
Gateway API links supplement generic ownership. Custom-resource ownership is
supported; arbitrary custom spec fields are not guessed to be references.

Snapshots are explicit, not permanent watches of every API resource. Refresh
preserves the camera and selected UID; an unavailable selected object remains
in the standard drawer instead of selecting its replacement. Inventory caps are
30,000 nodes, 120,000 edges and 4,096 kind/namespace sources, with visible
truncation. The catalog's incomplete discovery and per-source errors remain
visible.

Layout runs in a Worker with stable namespace regions and dependency columns.
Cycles remain finite; nodes keep their full identity. Canvas2D batches geometry,
culls out-of-viewport objects, uses semantic zoom for text and has indexed
hit-testing. Camera updates use requestAnimationFrame with no React node tree
or force-layout recalculation. The animated route layer respects reduced motion.
Keyboard traversal (including F6 workspace entry), resource search and the ordinary details panel complement
pointer pan/zoom. Canvas resizing retains the viewed world center when the
details panel opens or resizes.

## Reference research

[K8Studio CloudMaps](https://k8studio.io/features/cloudmaps/) describes a cluster
map grouped into namespaces with relationship navigation and progressive detail.
[Headlamp Map](https://headlamp.dev/docs/latest/development/plugins/functionality/extending-the-map/)
models resources as nodes with stable IDs and relations as edges.
[K8Studio Object Topology](https://k8studio.io/features/object-topology/) offers a
focused relationship diagram in resource details. These are design references;
Ocular's graph and declared routes do not claim observed network telemetry.
