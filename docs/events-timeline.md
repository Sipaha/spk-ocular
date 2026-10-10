# Events timeline

On an explicitly connected Kubernetes target, **Cluster → Events timeline** shows
an Events API snapshot. **Refresh** reads a new snapshot; the page does not install
a collector or keep a persistent observation history. Kubernetes may already have
expired older Events, and permissions or discovery may restrict coverage. An empty
result is not evidence that the workload has never failed.

## Investigation

Each event-series row has a time interval and a workload/resource label. Known
controller ownership groups Deployment → ReplicaSet → Pod and CronJob → Job → Pod
by immutable UID. Groups are ordered by namespace/resource, then last observation
time. Search includes known ancestor names, resource names, reasons and messages.
Filters select resource kind, warnings and a time range relative to the snapshot.
The density strip counts event-series records at their last observation.

Select a row to inspect its reason, message, first/last observation and cumulative
count. **Open resource** opens the existing resource properties, YAML, logs,
terminal and other supported tools. Known ancestors and the Event object can also
be opened. UID guards preserve historical identity: an event about a deleted Pod
never opens a same-named replacement. Unknown kinds or subjects without a UID are
named but not openable. Missing ownership is labeled rather than guessed.

Opening a different resource and leaving the workspace use the standard unsaved
edit guard. Selecting an event, filtering, changing language or refreshing does
not discard a resource draft. A failed refresh keeps the previous snapshot with a
visible error. No action is automatically applied from an event or diagnosis.

## Evidence semantics

Bars connect first and last observations of an aggregated Event. They do not
prove continuous failure duration, individual occurrence times or causality.
Counts use `series.count`, otherwise `count`, otherwise one; these are cumulative,
not occurrences within a selected time window. The time filter selects series by
last observation; an interval may begin before the filter window.

Last observation uses `series.lastObservedTime`, `lastTimestamp`, `eventTime`, then
creation time. First observation uses `firstTimestamp`, then `eventTime`, then the
last observation. Missing or reversed endpoints are disclosed. Unknown timestamps
remain available in the unbounded filter. API reason/message text is shown as
untrusted text, without translation or execution; it may contain workload details.

## Coverage and bounds

The UI-only `ClusterTimeline` API uses optional provider `TimelineSource`. Agent
wildcard grants do not expose it, and reads cannot implicitly connect a target.
An explicit namespace set issues per-namespace requests; an empty set stays empty.
All namespaces is a separate explicit choice.

Events use core/v1 for compatibility, including converted newer Event series.
Ownership inventory requests PartialObjectMetadata for discovered Pods,
Deployments, ReplicaSets, StatefulSets, DaemonSets, Jobs and CronJobs. No workload
specifications, Secret/ConfigMap inventory, logs or configuration values are
requested by Timeline. Other resource kinds can appear as event subjects without
a complete ownership inventory.

Snapshots are capped at 10,000 event series, 30,000 ownership resources and 4,096
kind/namespace reads, with 500-item API pages, a 20-second source timeout and a
75-second request timeout. Messages are capped at 2,048 UTF-8 bytes and disclose
shortening. Source errors are summarized by kind/namespace/class; partial discovery
and caps remain visible. Graph and Timeline snapshot requests share a per-session
serialized gate. Disconnect, cancellation and unmount stop in-flight work; late
responses cannot update a replacement page.

The frontend virtualizes event rows and bounds ownership traversal, including
cycles. All eight UI languages use local catalogs. This development feature has
not been included in the published v1.1.2 release.
