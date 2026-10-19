package kubernetes

import (
	"strconv"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/spk/spk-ocular/internal/core"
)

// podChoiceDetails reuses the already ownership-checked ReplicaSet listing.
// A current revision matches the desired template; age and revision maximum
// alone cannot identify it during rollback or before the controller catches up.
func podChoiceDetails(p, deployment, rs *unstructured.Unstructured) []core.Message {
	var out []core.Message
	if deployment != nil && rs != nil {
		revision := rs.GetAnnotations()[revisionKey]
		if n, err := strconv.ParseInt(revision, 10, 64); err == nil && n > 0 {
			desired, _, _ := unstructured.NestedMap(deployment.Object, "spec", "template")
			actual, _, _ := unstructured.NestedMap(rs.Object, "spec", "template")
			switch {
			case len(desired) == 0 || len(actual) == 0:
				out = append(out, msg("instance.revision", "revision", revision))
			case sameTemplate(withoutHash(desired), withoutHash(actual)):
				out = append(out, msg("instance.current", "revision", revision))
			default:
				out = append(out, msg("instance.previous", "revision", revision))
			}
		}
	}
	if podReady(p) {
		out = append(out, msg("instance.ready"))
	} else {
		out = append(out, msg("instance.notReady"))
	}
	// Count the declared application containers, not debug containers or a
	// stale status entry left behind for a different container name.
	ready := 0
	statuses := map[string]bool{}
	for _, cs := range slice(p.Object, "status", "containerStatuses") {
		v, _ := cs["ready"].(bool)
		statuses[strOf(cs, "name")] = v
	}
	for _, c := range slice(p.Object, "spec", "containers") {
		if statuses[strOf(c, "name")] {
			ready++
		}
	}
	out = append(out, msg("instance.containers", "ready", strconv.Itoa(ready), "total", strconv.Itoa(len(slice(p.Object, "spec", "containers")))))
	state := podStatusText(p)
	if state == "" {
		state = "Unknown"
	}
	out = append(out, msg("instance.state", "state", state))
	return out
}
