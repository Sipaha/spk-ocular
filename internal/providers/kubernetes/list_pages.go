package kubernetes

import (
	"context"
	"fmt"

	metainternal "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

const collectionPageSize int64 = 100

type collectionPage struct {
	items     []unstructured.Unstructured
	rv, next  string
	remaining *int64
}

// collectPages builds a complete compact snapshot, not a cache of full list
// objects. Each page is reduced before the next request. The outer reflector
// sees one complete list, so its expired-token fallback cannot publish only a
// first page. Failures discard the candidate; the previous live snapshot stays.
func collectPages(ctx context.Context, options metav1.ListOptions, read func(context.Context, metav1.ListOptions) (collectionPage, error), compact func(*unstructured.Unstructured) (runtime.Object, error), guard *scopeBudget) (runtime.Object, error) {
	if guard != nil {
		if err := guard.begin(); err != nil {
			return nil, err
		}
	}
	options.Limit = collectionPageSize
	options.Continue = ""
	// RV=0 may ignore Limit in an API-server watch cache. Most-recent LIST
	// supports consistent chunks; successful completion supplies the watch RV.
	options.ResourceVersion = ""
	options.ResourceVersionMatch = ""
	out := &metainternal.List{Items: []runtime.Object{}}
	cursor := inventoryCursor{}
	first := true
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page, err := read(ctx, options)
		if err != nil {
			return nil, err
		}
		if guard != nil && page.remaining != nil {
			if err := guard.estimate(int64(len(out.Items)+len(page.items)) + *page.remaining); err != nil {
				return nil, err
			}
		}
		if first {
			out.ResourceVersion = page.rv
			first = false
		} else if out.ResourceVersion != "" && page.rv != "" && page.rv != out.ResourceVersion {
			return nil, fmt.Errorf("collection resource version changed between pages")
		}
		for i := range page.items {
			if guard != nil {
				if err := guard.observe(&page.items[i], false); err != nil {
					return nil, err
				}
			}
			obj, err := compact(&page.items[i])
			if err != nil {
				return nil, err
			}
			out.Items = append(out.Items, obj)
			page.items[i].Object = nil
		}
		more, repeated := cursor.advance(page.next)
		if repeated {
			return nil, fmt.Errorf("collection continuation token repeated")
		}
		if !more {
			return out, nil
		}
		options.Continue = cursor.token
		if guard != nil && len(out.Items) == allNamespaceObjects && len(page.items) > 0 {
			// A continuation is not proof of another matching object when selectors
			// are present. Probe one rather than fetching another full page.
			options.Limit = 1
		} else {
			options.Limit = collectionPageSize
		}
	}
}
