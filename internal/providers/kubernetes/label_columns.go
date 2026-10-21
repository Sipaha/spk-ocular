package kubernetes

import (
	"unicode/utf8"

	"github.com/spk/spk-ocular/internal/core"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Only chosen keys cross the row boundary. Values are never shortened and
// presented as exact: malformed/over-budget evidence stays explicitly unknown.
func projectLabels(u *unstructured.Unstructured, keys []string) map[string]core.LabelValue {
	if len(keys) == 0 {
		return nil
	}
	labels, _, err := unstructured.NestedStringMap(u.Object, "metadata", "labels")
	out := make(map[string]core.LabelValue, len(keys))
	for _, key := range keys {
		v := core.LabelValue{State: "missing"}
		if err != nil {
			v.State = "unavailable"
		} else if value, ok := labels[key]; ok {
			if !utf8.ValidString(value) {
				v.State = "unavailable"
			} else if len(value) > core.MaxLabelValueBytes {
				v.State = "limit"
			} else {
				v.State, v.Value = "value", value
			}
		}
		out[key] = v
	}
	return out
}
