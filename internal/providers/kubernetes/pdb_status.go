package kubernetes

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

// Only a typed, generation-qualified controller report is current. Missing or
// stale status must not become zero through an unstructured numeric default.
func currentPDBDisruptions(u *unstructured.Unstructured) (int64, bool) {
	generation, gf, ge := unstructured.NestedInt64(u.Object, "metadata", "generation")
	observed, of, oe := unstructured.NestedInt64(u.Object, "status", "observedGeneration")
	allowed, af, ae := unstructured.NestedInt64(u.Object, "status", "disruptionsAllowed")
	return allowed, gf && ge == nil && generation > 0 && of && oe == nil && observed == generation && af && ae == nil && allowed >= 0
}
