package kubernetes

import (
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/spk/spk-ocular/internal/core"
)

// kindDef describes one Kubernetes kind for the generic view machinery.
type kindDef struct {
	desc       core.KindDescriptor
	gvr        schema.GroupVersionResource
	namespaced bool
	// keep is the field whitelist for list caches (see trim): everything
	// else — managedFields, annotations, big specs, Secret values — is
	// dropped before the object is stored.
	keep fields
	// pre runs before the whitelist on the full object (e.g. replace
	// ConfigMap/Secret values by their key names). Must be idempotent.
	pre func(u *unstructured.Unstructured)
	// project turns a (trimmed) object into a row. now is for time-based
	// health; next is when the row must be re-projected even without an
	// API change (zero: never).
	project func(u *unstructured.Unstructured, now time.Time) (cells []core.Cell, h core.Health, next time.Time)
	// virtual: a view made of other kinds (Problems), not an API resource.
	virtual bool
}

// fields is a whitelist tree: true keeps a value whole; a nested fields
// keeps only those sub-fields (applied to each element of a list of objects).
type fields map[string]any

// metaKeep is what every kind keeps from metadata.
var metaKeep = fields{
	"name": true, "namespace": true, "uid": true, "resourceVersion": true,
	"creationTimestamp": true, "deletionTimestamp": true, "generation": true,
	"labels": true, "ownerReferences": true,
}

// trim applies a whitelist in place and returns the object. Idempotent.
func trim(u *unstructured.Unstructured, keep fields) *unstructured.Unstructured {
	full := fields{"apiVersion": true, "kind": true, "metadata": metaKeep, keysField: true}
	for k, v := range keep {
		full[k] = v
	}
	u.Object = keepMap(u.Object, full)
	return u
}

func keepMap(m map[string]any, keep fields) map[string]any {
	out := make(map[string]any, len(keep))
	for k, spec := range keep {
		v, ok := m[k]
		if !ok {
			continue
		}
		switch s := spec.(type) {
		case bool:
			out[k] = v
		case fields:
			out[k] = keepValue(v, s)
		}
	}
	return out
}

func keepValue(v any, keep fields) any {
	switch t := v.(type) {
	case map[string]any:
		return keepMap(t, keep)
	case []any:
		out := make([]any, 0, len(t))
		for _, e := range t {
			out = append(out, keepValue(e, keep))
		}
		return out
	}
	return v
}

// keysField holds the key names of a ConfigMap/Secret in list caches (the
// values themselves are never cached).
const keysField = "ocularKeys"

// keysOnly replaces data maps by the sorted list of their keys.
func keysOnly(maps ...string) func(u *unstructured.Unstructured) {
	return func(u *unstructured.Unstructured) {
		if _, done := u.Object[keysField]; done {
			return
		}
		var keys []any
		for _, m := range maps {
			if d, ok := u.Object[m].(map[string]any); ok {
				for k := range d {
					keys = append(keys, k)
				}
			}
		}
		sortAny(keys)
		u.Object[keysField] = keys
	}
}

func sortAny(v []any) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j].(string) < v[j-1].(string); j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}

// registry of kinds by id, in navigation order.
type kindRegistry struct {
	list []*kindDef
	byID map[string]*kindDef
}

func newKindRegistry(defs ...*kindDef) *kindRegistry {
	r := &kindRegistry{byID: map[string]*kindDef{}}
	for _, d := range defs {
		r.list = append(r.list, d)
		r.byID[d.desc.ID] = d
	}
	return r
}

func (r *kindRegistry) descriptors() []core.KindDescriptor {
	out := make([]core.KindDescriptor, 0, len(r.list))
	for _, d := range r.list {
		desc := d.desc
		desc.Actions = kindActions[d]
		desc.Aliases = kindAliases[d]
		desc.Singular = kindSingular[d]
		desc.Default = d == podsKind
		if d != eventsKind {
			desc.EventsKind = eventsKind.desc.ID
		}
		out = append(out, desc)
	}
	return out
}

func strs(u map[string]any, path ...string) []string {
	v, _, _ := unstructured.NestedStringSlice(u, path...)
	return v
}

// Common helpers for projections.

func str(u map[string]any, path ...string) string {
	v, _, _ := unstructured.NestedString(u, path...)
	return v
}

func i64(u map[string]any, path ...string) int64 {
	v, ok, _ := unstructured.NestedFieldNoCopy(u, path...)
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	case int:
		return int64(n)
	}
	return 0
}

func slice(u map[string]any, path ...string) []map[string]any {
	v, ok, _ := unstructured.NestedFieldNoCopy(u, path...)
	if !ok {
		return nil
	}
	list, _ := v.([]any)
	out := make([]map[string]any, 0, len(list))
	for _, e := range list {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func timeAt(u map[string]any, path ...string) time.Time {
	s := str(u, path...)
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func createdCell(u *unstructured.Unstructured) core.Cell {
	return core.TimeCell(u.GetCreationTimestamp().UnixMilli())
}

// earliest returns the earlier non-zero time.
func earliest(a, b time.Time) time.Time {
	switch {
	case a.IsZero():
		return b
	case b.IsZero():
		return a
	case b.Before(a):
		return b
	}
	return a
}
