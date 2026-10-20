# Kubernetes learning and resource help

Open **Kubernetes learning** from the application menu without connecting to a
target. On Kubernetes resource lists, the **What is this?** button beside the
kind in the status bar opens its explanation; the same button appears beside
the kind in resource properties. Help opens over the workspace and does not
navigate away from held work. Escape closes it and returns focus.

## Offline resource reference

The application ships explanations in all eight supported languages. Purpose,
related concepts, key field paths and common misconceptions are local content;
opening or searching the reference makes no provider, schema or documentation
request. Related-concept links navigate the reference, not live objects. Official
documentation opens only after an explicit link click and requires a browser
and network access. Technical resource names and field paths are not translated.
The language control uses the normal application preference and preserves the
open concept and lesson state.

Coverage is the 37 actual API resource types in the described Kubernetes catalog
and its well-known placed-kind inventory, including the normally hidden
ReplicaSet. `web/src/learning/catalog.ts` is the explicit inventory; a test compares
it with the provider's current source definitions. This is not coverage of every
upstream Kubernetes API. Other discovered types receive an explicit missing-help
message. The reference does not fetch or render CRD schema descriptions; an
operator's own documentation remains the authority for its custom behavior.
Problems is an Ocular aggregate rather than a Kubernetes resource type and has
no invented Kubernetes explanation.

The source links are official Kubernetes concept and API documentation. The
reference distinguishes desired state from readiness, container restart from Pod
replacement, and declared policy from measured enforcement. It never reads Secret
values or interprets missing live evidence as success.

## First lesson: why a Pod returns

The introductory laboratory is a deterministic, browser-local authored scenario,
not a connection or a provider registered with the backend. It requires no
kubeconfig, Docker, account or infrastructure. The persistent simulated-data label
and lesson notice identify its boundaries. It reuses the normal resource grid
and read-only YAML viewer, with local properties, owner navigation, events and
application logs. These are synthetic observations, not a recording of a cluster.

Inspect a Deployment, its ReplicaSet and two Pods. Select a Pod and delete it in
the lesson: the observed count falls to one while desired replicas remain two.
An explicit simulated controller step creates a replacement with a new UID. Follow
owner relationships and inspect the events, logs and YAML. Scheduling, networking,
controller timing and arbitrary Kubernetes changes are not simulated.

The check uses new names and identities and hides hints. Completing it requires
following a Pod's actual local owner references through ReplicaSet to Deployment
and choosing the explanation of desired-state reconciliation. Selecting a
Deployment directly or choosing container restart/Service creation does not pass.
This is a bounded local comprehension check, not evidence of an independent
learner study, production competence or market demand.

Reset clears the lesson's observations, hints and check progress and creates fresh
identities. Switching between reference and lesson retains the in-dialog state;
language changes retain it too. Closing the library ends the in-memory lesson;
there is no saved progress, command replay, provider grant or cluster mutation.
The learning path overview reflects introductory completion while the library
remains open; resetting the introduction clears that completion.


## Task-based learning path

Choose **Learning path** in the library. Its overview contains the introduction
and five further modules. Each module has two guided tasks, followed by two
independent checks with new identities and changed scenario values. Modules can
be opened in any order; their current evidence, answers and progress survive
module/reference/language switching in this open library. Closing the library
clears them. There is no persisted certificate or learner account.

| Module | Authored situations |
| --- | --- |
| Service traffic | A selector that misses the application labels; a targetPort that differs from the listener despite ready endpoints. |
| Probes | A failed readiness path without container restarts; liveness interrupting a documented slow start, addressed with a startup budget. |
| Startup failures | A missing image tag with no container logs; invalid launch arguments identified through previous-container logs and events. |
| Configuration | A ConfigMap update not reflected in an existing environment; a required Secret key reference that does not exist. Only key names are included, never Secret values. |
| Resources | A CPU request that exceeds the remaining request budget despite low observed use; an explicit OOMKilled observation against a supplied working set and approved memory budget. |

Use the ordinary resource table and Properties, YAML, Events and Logs tabs to
inspect the listed local evidence. Choose both an explanation and a reviewed,
bounded field change. A correct answer alone cannot pass: applying a repair
requires the specified evidence views, correct diagnosis and appropriate change.
Then inspect the resulting Pod or endpoint properties before advancing. The
module is complete only after both independent checks pass. Hints and in-task
resource-help controls are absent in checks. Reading the general reference is
still available; completion is not proof of unaided learning in a study.

YAML remains readable as plain read-only text if the optional editor/highlighting
asset is unavailable; losing that asset cannot discard the lesson or its data.

All changes act on deterministic renderer-only snapshots. Workload repairs modify
a Deployment template and show a new local Pod identity; they do not teach edits
to immutable live Pod fields. Selectors/ports update the local Service snapshot.
The displayed HTTP response and scheduling outcome are authored observations,
not network requests, scheduler runs or guarantees about an actual cluster.
Supplied application measurements and derived budgets are shown separately
from Kubernetes API fields, under explicitly labeled scenario observations.
CPU and memory values are specific to these scenarios, not universal rightsizing
advice. Reset clears a module and replaces its identities; delayed
actions from previous tasks are rejected by their task token.

Content and feedback ship in all eight application languages. Kubernetes field
paths, object names, YAML, recorded status reasons and authored container logs
retain their technical spelling. The reference still covers the explicit
37-kind inventory; adding a lesson does not expand that coverage to arbitrary
APIs or CRDs. Native-speaker review and studies with independent beginners remain
unverified and separate from software checks.

The scenarios follow official Kubernetes documentation for
[Services](https://kubernetes.io/docs/concepts/services-networking/service/),
[probes](https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/),
[container lifecycle](https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/),
[ConfigMaps](https://kubernetes.io/docs/concepts/configuration/configmap/),
[Secrets](https://kubernetes.io/docs/concepts/configuration/secret/) and
[resource management](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/).
