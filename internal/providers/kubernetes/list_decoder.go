package kubernetes

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utiljson "k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// inventoryClient retains the dynamic client's transports, negotiation and
// request behavior. Only JSON LIST decoding changes: the upstream list custom
// unmarshaler parses the full envelope and then parses every item again.
func inventoryClient(original *rest.Config) (*dynamic.DynamicClient, error) {
	cfg := dynamic.ConfigFor(original)
	cfg.GroupVersion = nil
	cfg.APIPath = "/if-you-see-this-search-for-the-break"
	cfg.NegotiatedSerializer = inventoryNegotiation{cfg.NegotiatedSerializer}
	httpClient, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return nil, err
	}
	client, err := rest.UnversionedRESTClientForConfigAndClient(cfg, httpClient)
	if err != nil {
		return nil, err
	}
	return dynamic.New(client), nil
}

type inventoryNegotiation struct{ runtime.NegotiatedSerializer }

func (n inventoryNegotiation) SupportedMediaTypes() []runtime.SerializerInfo {
	infos := append([]runtime.SerializerInfo(nil), n.NegotiatedSerializer.SupportedMediaTypes()...)
	for i := range infos {
		if infos[i].MediaType == "application/json" {
			infos[i].Serializer = inventoryJSON{infos[i].Serializer}
		}
	}
	return infos
}

type inventoryJSON struct{ runtime.Serializer }

func (s inventoryJSON) Decode(data []byte, defaults *schema.GroupVersionKind, into runtime.Object) (runtime.Object, *schema.GroupVersionKind, error) {
	list, ok := into.(*unstructured.UnstructuredList)
	if !ok || list == nil {
		return s.Serializer.Decode(data, defaults, into)
	}
	var object map[string]any
	// This is the same case-sensitive, integer-preserving decoder used by
	// Kubernetes unstructured objects, without their repeated LIST passes.
	if err := utiljson.Unmarshal(data, &object); err != nil {
		return nil, nil, err
	}
	for key, value := range object {
		if key != "apiVersion" && key != "kind" {
			if strings.EqualFold(key, "apiVersion") || strings.EqualFold(key, "kind") {
				// The upstream metadata probe is case-insensitive. Preserve
				// its handling of noncanonical envelopes, including aliases.
				return s.Serializer.Decode(data, defaults, into)
			}
			continue
		}
		if _, valid := value.(string); value != nil && !valid {
			return nil, nil, fmt.Errorf("resource list %s must be a string", key)
		}
	}
	if version, _ := object["apiVersion"].(string); version != "" {
		if _, err := schema.ParseGroupVersion(version); err != nil {
			return nil, nil, err
		}
	}
	var items []any
	if raw := object["items"]; raw != nil {
		var valid bool
		items, valid = raw.([]any)
		if !valid {
			return nil, nil, fmt.Errorf("resource list items must be an array")
		}
	}
	delete(object, "items")
	out := &unstructured.UnstructuredList{Object: object, Items: make([]unstructured.Unstructured, 0, len(items))}
	gvk := out.GroupVersionKind()
	if gvk.Kind == "" {
		return nil, &gvk, runtime.NewMissingKindErr(string(data))
	}
	for _, item := range items {
		fields, valid := item.(map[string]any)
		if item != nil && !valid {
			return nil, &gvk, fmt.Errorf("resource list item must be an object")
		}
		u := unstructured.Unstructured{Object: fields}
		if u.GetKind() == "" && u.GetAPIVersion() == "" {
			u.SetKind(strings.TrimSuffix(gvk.Kind, "List"))
			u.SetAPIVersion(out.GetAPIVersion())
		}
		out.Items = append(out.Items, u)
	}
	*list = *out
	return list, &gvk, nil
}
