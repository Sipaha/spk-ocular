package kubernetes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/views"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
)

func TestMultipleNamespacesNeverListOrWatchAllAndKeepLiveRows(t *testing.T) {
	c := fakeClient(pod("a", "same", "a1"), pod("b", "same", "b1"), pod("outside", "secret", "no"))
	c.PrependReactor("list", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetNamespace() != "a" && a.GetNamespace() != "b" {
			return true, nil, apierrors.NewForbidden(podGVR.GroupResource(), "", nil)
		}
		return false, nil, nil
	})
	h := newHarness(t, c)
	id := h.open("pods", core.ScopeSel{Mode: core.ScopeSome, Names: []string{"b", "a", "b"}})
	p := h.until(id, isReady)
	require.Len(t, p.Upserts, 2)
	assert.ElementsMatch(t, []string{"a1", "b1"}, []string{p.Upserts[0].ID, p.Upserts[1].ID})
	require.NoError(t, c.Resource(podGVR).Namespace("a").Delete(context.Background(), "same", metav1.DeleteOptions{}))
	_, err := c.Resource(podGVR).Namespace("b").Create(context.Background(), pod("b", "new", "b2"), metav1.CreateOptions{})
	require.NoError(t, err)
	h.until(id, func(p views.Page) bool {
		if len(p.Upserts) != 2 {
			return false
		}
		ids := map[string]bool{}
		for _, row := range p.Upserts {
			ids[row.ID] = true
		}
		return ids["b1"] && ids["b2"]
	})
	for _, a := range c.Actions() {
		if a.GetVerb() == "list" || a.GetVerb() == "watch" {
			assert.Contains(t, []string{"a", "b"}, a.GetNamespace())
		}
	}
}

func TestMultipleNamespacesReportDeniedAndEmptySetMakesNoRequests(t *testing.T) {
	c := fakeClient(pod("a", "visible", "a1"))
	c.PrependReactor("list", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetNamespace() != "a" {
			return true, nil, apierrors.NewForbidden(podGVR.GroupResource(), "", nil)
		}
		return false, nil, nil
	})
	h := newHarness(t, c)
	id := h.open("pods", core.ScopeSel{Mode: core.ScopeSome})
	p := h.until(id, isReady)
	assert.Empty(t, p.Upserts)
	assert.Empty(t, c.Actions())
	id = h.open("pods", core.ScopeSel{Mode: core.ScopeSome, Names: []string{"a", "denied"}})
	p = h.until(id, isReady)
	assert.Equal(t, []string{"visible"}, names(p))
	require.Len(t, p.Status.Coverage, 2)
	assert.Equal(t, provider.CoverageDenied, p.Status.Coverage[1].State)
}

func TestMultipleNamespaceMetricsUseEachNamespaceAndCache(t *testing.T) {
	c := fullFake(pod("a", "same", "a1"), pod("b", "same", "b1"))
	c.PrependReactor("list", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetResource() != podMetricsGVR {
			return false, nil, nil
		}
		require.Contains(t, []string{"a", "b"}, a.GetNamespace())
		return true, podMetricsList(podMetric(a.GetNamespace(), "same", time.Now().UTC().Format(time.RFC3339))), nil
	})
	h := newHarness(t, c)
	q := provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeSome, Names: []string{"a", "b"}}}
	h.until(h.open(q.Kind, q.Scope), isReady)
	m, err := h.sess.Metrics(context.Background(), q, nil)
	require.NoError(t, err)
	assert.Contains(t, m.Values, "a1")
	assert.Contains(t, m.Values, "b1")
	_, err = h.sess.Metrics(context.Background(), q, nil)
	require.NoError(t, err)
	calls := 0
	for _, a := range c.Actions() {
		if a.GetResource() == podMetricsGVR {
			calls++
		}
	}
	assert.Equal(t, 2, calls)
}

func TestMultipleNamespacesProbeOnlySelectedAndTryAfterDenied(t *testing.T) {
	api := newWidgetsAPI()
	s, _ := tableSession(t, api)
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if strings.Contains(r.URL.Path, "/namespaces/a/") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !strings.Contains(r.URL.Path, "/namespaces/b/") {
			http.Error(w, "must stay scoped", http.StatusForbidden)
			return
		}
		api.serve(w, r)
	}))
	defer srv.Close()
	tc, err := newTableClient(&rest.Config{Host: srv.URL})
	require.NoError(t, err)
	s.tables, s.caches.tables = tc, tc
	q := provider.Query{Kind: "ocular.dev/widgets", Scope: core.ScopeSel{Mode: core.ScopeSome, Names: []string{"a", "b"}}}
	desc, bound, err := s.DescribeView(context.Background(), q)
	require.NoError(t, err)
	assert.NotZero(t, bound.Schema)
	assert.Contains(t, colIDs(desc.Columns), "size")
	require.Len(t, paths, 2)
	assert.Contains(t, paths[0], "/namespaces/a/")
	assert.Contains(t, paths[1], "/namespaces/b/")
}
