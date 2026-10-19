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
learner study, production competence or market demand. Broader lessons remain
separate work after validation.

Reset clears the lesson's observations, hints and check progress and creates fresh
identities. Switching between reference and lesson retains the in-dialog state;
language changes retain it too. Closing the library ends the in-memory lesson;
there is no saved progress, command replay, provider grant or cluster mutation.
