package kubernetes

import (
	"context"
	"fmt"
	"testing"

	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternal "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestCompactPagesCompleteAndConsistent(t *testing.T) {
	calls, reduced := 0, 0
	obj, err := collectPages(context.Background(), metav1.ListOptions{ResourceVersion: "0"}, func(_ context.Context, o metav1.ListOptions) (collectionPage, error) {
		require.EqualValues(t, 100, o.Limit)
		require.Empty(t, o.ResourceVersion)
		require.Equal(t, calls*100, reduced, "reduce the previous page before fetching another")
		start := calls * 100
		calls++
		items := make([]unstructured.Unstructured, 100)
		for i := range items {
			items[i] = *realisticPod(start + i)
		}
		next := ""
		if calls < 100 {
			next = fmt.Sprint(calls)
		}
		return collectionPage{items: items, rv: "snapshot", next: next}, nil
	}, func(u *unstructured.Unstructured) (runtime.Object, error) {
		reduced++
		return slim(trim(u, podsKind.keep))
	}, nil)
	require.NoError(t, err)
	l := obj.(*metainternal.List)
	require.Len(t, l.Items, 10000)
	require.Equal(t, "snapshot", l.ResourceVersion)
	require.Empty(t, l.Continue)
	for _, o := range l.Items {
		require.IsType(t, &slimObject{}, o)
	}
}
func TestCompactPagesFailureNeverReturnsPartialSnapshot(t *testing.T) {
	for _, mode := range []string{"expired", "repeated", "version"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			obj, err := collectPages(context.Background(), metav1.ListOptions{}, func(_ context.Context, _ metav1.ListOptions) (collectionPage, error) {
				calls++
				if calls == 2 && mode == "expired" {
					return collectionPage{}, apierrors.NewResourceExpired("expired")
				}
				rv := "1"
				if calls == 2 && mode == "version" {
					rv = "2"
				}
				return collectionPage{items: []unstructured.Unstructured{*realisticPod(calls)}, rv: rv, next: "same"}, nil
			}, func(u *unstructured.Unstructured) (runtime.Object, error) { return slim(u) }, nil)
			require.Error(t, err)
			require.Nil(t, obj)
			require.Equal(t, 2, calls)
		})
	}
}
func TestBroadInventoryEstimateRejectsBeforeCompacting(t *testing.T) {
	g := newScopeBudget()
	remaining := int64(9900)
	calls := 0
	obj, err := collectPages(context.Background(), metav1.ListOptions{}, func(context.Context, metav1.ListOptions) (collectionPage, error) {
		calls++
		return collectionPage{items: make([]unstructured.Unstructured, 100), remaining: &remaining, next: "100"}, nil
	}, func(*unstructured.Unstructured) (runtime.Object, error) {
		t.Fatal("oversized candidate must not be published")
		return nil, nil
	}, g)
	require.Nil(t, obj)
	require.Error(t, err)
	class, _ := classify(err)
	require.Equal(t, provider.ClassScopeLimit, class)
	require.Equal(t, 1, calls)
	require.Error(t, g.begin(), "blocked cache cannot continuously retry broad inventory")
}
func TestBroadInventoryObservedCountAndWatchChurn(t *testing.T) {
	g := newScopeBudget()
	for i := 0; i < allNamespaceObjects; i++ {
		require.NoError(t, g.observe(realisticPod(i), false))
	}
	require.NoError(t, g.observe(realisticPod(0), true))
	require.NoError(t, g.observe(realisticPod(allNamespaceObjects), false))
	require.Error(t, g.observe(realisticPod(allNamespaceObjects+1), false))
	require.Error(t, g.observe(realisticPod(1), true), "admission failure stays latched until a new cache")
}

func TestBroadInventoryWithoutEstimateNeverPublishesPartial(t *testing.T) {
	calls := 0
	guard := newScopeBudget()
	out, err := collectPages(context.Background(), metav1.ListOptions{}, func(context.Context, metav1.ListOptions) (collectionPage, error) {
		start := calls * 100
		calls++
		items := make([]unstructured.Unstructured, 100)
		for i := range items {
			items[i] = *realisticPod(start + i)
		}
		return collectionPage{items: items, next: fmt.Sprint(calls), rv: "1"}, nil
	}, func(u *unstructured.Unstructured) (runtime.Object, error) { return slim(trim(u, podsKind.keep)) }, guard)
	require.Error(t, err)
	require.Nil(t, out)
	require.Equal(t, 21, calls)
}

func TestBroadInventoryAtLimitContinuesAcrossEmptyFilteredPages(t *testing.T) {
	calls := 0
	out, err := collectPages(context.Background(), metav1.ListOptions{}, func(_ context.Context, o metav1.ListOptions) (collectionPage, error) {
		call := calls
		calls++
		if call < 20 {
			require.EqualValues(t, 100, o.Limit)
			items := make([]unstructured.Unstructured, 100)
			for i := range items {
				items[i] = *realisticPod(call*100 + i)
			}
			return collectionPage{items: items, rv: "1", next: fmt.Sprint(calls)}, nil
		}
		if call == 20 {
			require.EqualValues(t, 1, o.Limit)
			return collectionPage{rv: "1", next: "last-empty"}, nil
		}
		require.EqualValues(t, 100, o.Limit)
		return collectionPage{rv: "1"}, nil
	}, func(u *unstructured.Unstructured) (runtime.Object, error) { return slim(trim(u, podsKind.keep)) }, newScopeBudget())
	require.NoError(t, err)
	require.Len(t, out.(*metainternal.List).Items, 2000)
	require.Equal(t, 22, calls)
}
