package kubernetes

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spk/spk-ocular/internal/core"
)

// The provider's English sentences for the UI (what actions would do, the
// terminal's levels), by key; the UI says the keys it knows in its language
// (web/src/i18n.ts, providerTexts) with the same parameters, the rest in
// this English.
var messageTexts = map[string]string{
	"scope.singular":             "Namespace",
	"scope.plural":               "namespaces",
	"scope.all":                  "All namespaces",
	"logs.allContainers":         "All containers",
	"exec.noPods":                "No running pods",
	"level.pod":                  "Pod",
	"level.container":            "Container",
	"restart.noPods":             "It runs no pods: only the pod template changes.",
	"restart.recreate":           "All pods stop, then new ones start (strategy Recreate).",
	"restart.rolling":            "Pods are replaced gradually (rolling update: max unavailable {maxUnavailable}, max surge {maxSurge}).",
	"restart.onDelete":           "Existing pods keep running until they are deleted (update strategy OnDelete).",
	"restart.partition":          "Only pods with ordinal {partition} and above are replaced, one at a time (partition {partition}).",
	"restart.ordered":            "Pods are replaced one at a time, from the highest ordinal.",
	"restart.byNode":             "Pods are replaced node by node (max unavailable {maxUnavailable}).",
	"restart.requested":          "A restart is requested.",
	"scale.now":                  "It has {count} replicas now.",
	"scale.same":                 "{from} → {to}: the count does not change.",
	"scale.zero":                 "{from} → 0: all pods stop.",
	"scale.downOne":              "{from} → {to}: 1 pod is removed.",
	"scale.down":                 "{from} → {to}: {count} pods are removed.",
	"scale.upOne":                "{from} → {to}: 1 pod is added.",
	"scale.up":                   "{from} → {to}: {count} pods are added.",
	"claims.deletedOne":          "The PersistentVolumeClaims of pod {first} are deleted (what happens to the data follows the volumes' reclaim policy).",
	"claims.deleted":             "The PersistentVolumeClaims of pods {first}–{last} are deleted (what happens to the data follows the volumes' reclaim policy).",
	"claims.keptUntil":           "The PersistentVolumeClaims of removed pods are kept until the StatefulSet is deleted: then they are deleted with it (what happens to the data follows the volumes' reclaim policy).",
	"claims.keptScaled":          "The PersistentVolumeClaims of removed pods are kept.",
	"claims.deletedWith":         "The PersistentVolumeClaims of its pods are deleted (what happens to the data follows the volumes' reclaim policy).",
	"claims.kept":                "Its PersistentVolumeClaims are kept.",
	"delete.recreatedBy":         "{ownerKind} {owner} may create it again.",
	"delete.notEviction":         "This is not an eviction: PodDisruptionBudgets are not consulted.",
	"delete.requested":           "Deletion is requested: finalizers and grace periods may keep it for a while.",
	"delete.finalizers":          "Deletion waits for its finalizers: {finalizers}.",
	"pod.noController":           "It has no controller: nothing recreates it.",
	"pod.job":                    "Job {owner} may create a new pod if it has not completed.",
	"pod.otherController":        "It belongs to {ownerKind} {owner}: whether it is recreated depends on that controller.",
	"pod.ownerGone":              "Its {ownerKind} {owner} no longer exists: nothing recreates it.",
	"pod.ownerUnreadable":        "It belongs to {ownerKind} {owner}, which could not be read: it may create a replacement.",
	"pod.ownerDeleting":          "Its {ownerKind} {owner} is being deleted: a replacement is unlikely.",
	"pod.ownerWantsNone":         "Its {ownerKind} {owner} wants 0 pods: no replacement.",
	"pod.ownerRecreates":         "{ownerKind} {owner} normally creates a replacement.",
	"pods.deleted":               "Its pods are deleted too.",
	"pods.deletedAtLeast":        "Its pods are deleted too (at least {count} now).",
	"pods.none":                  "It has no pods now.",
	"pods.deletedCount":          "Its pods are deleted too ({count} now).",
	"hpa.checkFailed":            "The count could not be checked against autoscalers (could not check autoscalers: {error}).",
	"hpa.checkLate":              "The count could not be checked against autoscalers (could not check autoscalers in time).",
	"hpa.overrides":              "HorizontalPodAutoscaler {name} may override the count ({min}–{max}).",
	"hpa.tooMany":                "Not every autoscaler could be checked (too many).",
	"unavailable.deleting":       "{kind} {name} is being deleted",
	"unavailable.paused":         "deployment {name} is paused: resume its rollout first",
	"unavailable.cordoned":       "node {name} is already cordoned: no new pods are scheduled on it",
	"unavailable.schedulable":    "node {name} is not cordoned: pods may be scheduled on it",
	"node.cordon":                "No new pods are scheduled on node {name}; the pods running there stay.",
	"node.cordonBypass":          "Pods that name the node (nodeName) or tolerate its being unschedulable may still be placed there.",
	"node.uncordon":              "Pods may be scheduled on node {name} again.",
	"drain.podsUnreadable":       "The node's pods could not be read ({reason}): a drain needs to know them all",
	"drain.tooMany":              "The node runs more than {max} pods: too many for one drain",
	"drain.nothing":              "node {name} is cordoned and has no pod to evict",
	"drain.evictOne":             "Eviction is requested for 1 pod, one request per pod; a PodDisruptionBudget may refuse it.",
	"drain.evict":                "Eviction is requested for {count} pods, one request per pod; PodDisruptionBudgets may refuse some.",
	"drain.recreate":             "A controller may create an evicted pod again; where is up to the scheduler.",
	"drain.noWait":               "Nothing waits for the pods to go: they end by their grace periods (the tables show it).",
	"drain.onlyCordon":           "No pod needs evicting: only the cordon is written.",
	"drain.bareOne":              "1 pod has no controller: it stays on the node (nothing would recreate it). Delete it yourself if it is not needed.",
	"drain.bare":                 "{count} pods have no controller: they stay on the node (nothing would recreate them). Delete them yourself if they are not needed.",
	"drain.list.evict":           "Evicted",
	"drain.list.emptyDir":        "emptyDir data is lost",
	"drain.list.bare":            "Stay: no controller",
	"drain.list.left":            "Left alone",
	"drain.list.denied":          "Eviction not allowed",
	"drain.item.owner":           "{ownerKind} {owner}",
	"drain.item.volumes":         "volumes {volumes}",
	"drain.skip.daemon":          "a DaemonSet's (it would come back at once)",
	"drain.skip.mirror":          "a static pod (the API cannot evict it)",
	"drain.skip.finished":        "finished",
	"drain.skip.deleting":        "being deleted",
	"drain.pdbBlocksOne":         "PodDisruptionBudget {pdb} allows no disruption now: the eviction of 1 pod may be refused (a forecast: the server decides at each eviction).",
	"drain.pdbBlocks":            "PodDisruptionBudget {pdb} allows no disruption now: evictions of {count} pods may be refused (a forecast: the server decides at each eviction).",
	"drain.pdbMany":              "Pod {pod} is under more than one PodDisruptionBudget: the server refuses to evict it.",
	"drain.pdbUnchecked":         "PodDisruptionBudgets were not checked (not all could be read in time); the server still consults them at each eviction.",
	"edit.local":                 "Not checked by the server (a dry run of this resource is not proven to be safe): the result is computed locally.",
	"edit.noDryRunWebhook":       "The server could not check the edit without writing it (an admission webhook does not support dry run): the result is computed locally.",
	"edit.rebased":               "The object changed since the editor opened it: your edit lies over its current version — check the difference.",
	"edit.collisions":            "Your edit overwrites changes made since the editor opened it: {paths}.",
	"edit.controller":            "It is managed by {ownerKind} {owner}: the controller may revert the change.",
	"edit.managedBy":             "It is managed by {manager}: the next deployment may overwrite the change.",
	"edit.rollout":               "The pod template changes: a rollout starts.",
	"edit.replicasZero":          "Replicas go to 0: all pods stop.",
	"edit.finalizers":            "Finalizers of an object being deleted are removed ({finalizers}): its deletion may finish at once.",
	"edit.dataKeys":              "Data keys are removed: {keys}.",
	"edit.volumes":               "Volumes or volume claim templates are removed: {names}.",
	"edit.removes":               "Removed: {paths}.",
	"edit.hiddenInvalid":         "The server found the edit invalid; its message is hidden: it may contain Secret values.",
	"edit.hiddenInvalidFields":   "The server found {count} fields invalid; its message is hidden: it may contain Secret values.",
	"edit.hiddenForbidden":       "The server refused the edit; its message is hidden: it may contain Secret values.",
	"edit.hiddenConflict":        "The Secret changed meanwhile; the server's message is hidden: it may contain Secret values.",
	"edit.hiddenGone":            "The Secret no longer exists; the server's message is hidden: it may contain Secret values.",
	"edit.hiddenOther":           "The server did not accept the edit; its message is hidden: it may contain Secret values.",
	"values.immutable":           "The Secret is immutable: its values cannot change (create a new Secret instead).",
	"values.rebased":             "The Secret changed since its keys were listed; the edit lies over its current version.",
	"values.collision":           "Key {key} changed since the keys were listed: the edit overwrites that change.",
	"values.serverChanges":       "The server's review keeps these keys otherwise than asked: {keys}.",
	"values.typed":               "Secrets of type {type} have a required format: the server checks it.",
	"values.delete":              "A deleted key cannot be restored here; readers outside the pods (controllers, other systems) cannot be seen.",
	"values.deleteUsed":          "Pods read key {key} without marking it optional: {pods}.",
	"values.reload":              "Pods reading the Secret as environment variables see a change only after a restart; mounted volumes update after a delay (subPath mounts never).",
	"secret.hiddenReadForbidden": "The server refused to show the Secret; its message is hidden: it may contain Secret values.",
	"secret.hiddenReadOther":     "The Secret could not be read; the server's message is hidden: it may contain Secret values.",
}

// msg builds the message key with params given as name, value pairs.
func msg(key string, kv ...any) core.Message {
	tmpl, ok := messageTexts[key]
	if !ok {
		panic("kubernetes: no text for " + key)
	}
	m := core.Message{Key: ProviderID + "." + key, Text: tmpl}
	if len(kv) > 0 {
		m.Params = make(map[string]string, len(kv)/2)
		for i := 0; i+1 < len(kv); i += 2 {
			m.Params[fmt.Sprint(kv[i])] = fmt.Sprint(kv[i+1])
		}
		// Longest names first: {count} must not eat a prefix of another.
		names := make([]string, 0, len(m.Params))
		for k := range m.Params {
			names = append(names, k)
		}
		sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
		for _, k := range names {
			m.Text = strings.ReplaceAll(m.Text, "{"+k+"}", m.Params[k])
		}
	}
	return m
}
