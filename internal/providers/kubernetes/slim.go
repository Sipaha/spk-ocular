package kubernetes

import (
	"encoding/json"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

// slimObject is how list caches hold objects (measured, docs/specs): the
// identity metadata the informer needs (keys, deletions, ownership) as a
// typed ObjectMeta, and the whitelisted rest as compact JSON. A trimmed
// unstructured pod costs ~6 KB in map overhead; this costs ~1 KB. Rows are
// projected from expand() — once per change, not per render.
type slimObject struct {
	metav1.TypeMeta
	metav1.ObjectMeta
	body []byte // JSON of the trimmed object without metadata
}

var _ runtime.Object = (*slimObject)(nil)

// DeepCopyObject: slim objects are immutable once stored; a shallow copy is a copy.
func (o *slimObject) DeepCopyObject() runtime.Object {
	c := *o
	return &c
}

// slim converts a trimmed unstructured object.
func slim(u *unstructured.Unstructured) (*slimObject, error) {
	o := &slimObject{
		TypeMeta: metav1.TypeMeta{APIVersion: u.GetAPIVersion(), Kind: u.GetKind()},
		ObjectMeta: metav1.ObjectMeta{
			Name: u.GetName(), Namespace: u.GetNamespace(), UID: u.GetUID(),
			ResourceVersion: u.GetResourceVersion(), Generation: u.GetGeneration(),
			CreationTimestamp: u.GetCreationTimestamp(), DeletionTimestamp: u.GetDeletionTimestamp(),
			Labels: u.GetLabels(), OwnerReferences: u.GetOwnerReferences(),
		},
	}
	body := make(map[string]any, len(u.Object))
	for k, v := range u.Object {
		if k != "metadata" && k != "apiVersion" && k != "kind" {
			body[k] = v
		}
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	o.body = b
	return o, nil
}

// expand rebuilds the trimmed unstructured object for projection.
func (o *slimObject) expand() *unstructured.Unstructured {
	m := map[string]any{}
	_ = json.Unmarshal(o.body, &m) // our own encoding: cannot fail
	u := &unstructured.Unstructured{Object: m}
	u.SetAPIVersion(o.APIVersion)
	u.SetKind(o.Kind)
	u.SetName(o.Name)
	u.SetNamespace(o.Namespace)
	u.SetUID(o.UID)
	u.SetResourceVersion(o.ResourceVersion)
	u.SetGeneration(o.Generation)
	u.SetCreationTimestamp(o.CreationTimestamp)
	u.SetDeletionTimestamp(o.DeletionTimestamp)
	if o.Labels != nil {
		u.SetLabels(o.Labels)
	}
	if o.OwnerReferences != nil {
		u.SetOwnerReferences(o.OwnerReferences)
	}
	return u
}

// asUnstructured accepts what informer handlers deliver.
func asUnstructured(obj any) (*unstructured.Unstructured, bool) {
	switch t := obj.(type) {
	case *slimObject:
		return t.expand(), true
	case *unstructured.Unstructured:
		return t, true
	}
	return nil, false
}
