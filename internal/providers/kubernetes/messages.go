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
	"scope.singular":       "Namespace",
	"scope.plural":         "namespaces",
	"scope.all":            "All namespaces",
	"logs.allContainers":   "All containers",
	"exec.noPods":          "No running pods",
	"level.pod":            "Pod",
	"level.container":      "Container",
	"restart.noPods":       "It runs no pods: only the pod template changes.",
	"restart.recreate":     "All pods stop, then new ones start (strategy Recreate).",
	"restart.rolling":      "Pods are replaced gradually (rolling update: max unavailable {maxUnavailable}, max surge {maxSurge}).",
	"restart.onDelete":     "Existing pods keep running until they are deleted (update strategy OnDelete).",
	"restart.partition":    "Only pods with ordinal {partition} and above are replaced, one at a time (partition {partition}).",
	"restart.ordered":      "Pods are replaced one at a time, from the highest ordinal.",
	"restart.byNode":       "Pods are replaced node by node (max unavailable {maxUnavailable}).",
	"restart.requested":    "A restart is requested.",
	"scale.now":            "It has {count} replicas now.",
	"scale.same":           "{from} → {to}: the count does not change.",
	"scale.zero":           "{from} → 0: all pods stop.",
	"scale.downOne":        "{from} → {to}: 1 pod is removed.",
	"scale.down":           "{from} → {to}: {count} pods are removed.",
	"scale.upOne":          "{from} → {to}: 1 pod is added.",
	"scale.up":             "{from} → {to}: {count} pods are added.",
	"claims.deletedOne":    "The PersistentVolumeClaims of pod {first} are deleted (what happens to the data follows the volumes' reclaim policy).",
	"claims.deleted":       "The PersistentVolumeClaims of pods {first}–{last} are deleted (what happens to the data follows the volumes' reclaim policy).",
	"claims.keptUntil":     "The PersistentVolumeClaims of removed pods are kept until the StatefulSet is deleted: then they are deleted with it (what happens to the data follows the volumes' reclaim policy).",
	"claims.keptScaled":    "The PersistentVolumeClaims of removed pods are kept.",
	"claims.deletedWith":   "The PersistentVolumeClaims of its pods are deleted (what happens to the data follows the volumes' reclaim policy).",
	"claims.kept":          "Its PersistentVolumeClaims are kept.",
	"delete.recreatedBy":   "{ownerKind} {owner} may create it again.",
	"delete.notEviction":   "This is not an eviction: PodDisruptionBudgets are not consulted.",
	"delete.requested":     "Deletion is requested: finalizers and grace periods may keep it for a while.",
	"pod.noController":     "It has no controller: nothing recreates it.",
	"pod.job":              "Job {owner} may create a new pod if it has not completed.",
	"pod.otherController":  "It belongs to {ownerKind} {owner}: whether it is recreated depends on that controller.",
	"pod.ownerGone":        "Its {ownerKind} {owner} no longer exists: nothing recreates it.",
	"pod.ownerUnreadable":  "It belongs to {ownerKind} {owner}, which could not be read: it may create a replacement.",
	"pod.ownerDeleting":    "Its {ownerKind} {owner} is being deleted: a replacement is unlikely.",
	"pod.ownerWantsNone":   "Its {ownerKind} {owner} wants 0 pods: no replacement.",
	"pod.ownerRecreates":   "{ownerKind} {owner} normally creates a replacement.",
	"pods.deleted":         "Its pods are deleted too.",
	"pods.deletedAtLeast":  "Its pods are deleted too (at least {count} now).",
	"pods.none":            "It has no pods now.",
	"pods.deletedCount":    "Its pods are deleted too ({count} now).",
	"hpa.checkFailed":      "The count could not be checked against autoscalers (could not check autoscalers: {error}).",
	"hpa.checkLate":        "The count could not be checked against autoscalers (could not check autoscalers in time).",
	"hpa.overrides":        "HorizontalPodAutoscaler {name} may override the count ({min}–{max}).",
	"hpa.tooMany":          "Not every autoscaler could be checked (too many).",
	"unavailable.deleting": "{kind} {name} is being deleted",
	"unavailable.paused":   "deployment {name} is paused: resume its rollout first",
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
