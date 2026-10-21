package kubernetes

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/views"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestLabelProjectionExactKeysMissingLimitsAndSlimEvidence(t *testing.T) {
	u := pod("ns", "p", "uid")
	u.SetLabels(map[string]string{"app.kubernetes.io/version": "v2", "x:y/z.a": "literal", "x": "wrong", "empty": "", "large": strings.Repeat("é", 129), "unselected": "private"})
	keys := []string{"app.kubernetes.io/version", "x:y/z.a", "empty", "missing", "large"}
	compact, err := slim(trim(u, podsKind.keep))
	require.NoError(t, err)
	got := projectLabels(compact.expand(), keys)
	require.Len(t, got, 5)
	require.Equal(t, core.LabelValue{State: "value", Value: "literal"}, got["x:y/z.a"])
	require.Equal(t, core.LabelValue{State: "value"}, got["empty"])
	require.Equal(t, core.LabelValue{State: "missing"}, got["missing"])
	require.Equal(t, core.LabelValue{State: "limit"}, got["large"])
	require.NotContains(t, got, "unselected")
	u.Object["metadata"].(map[string]any)["labels"] = map[string]any{"broken": int64(42)}
	require.Equal(t, "unavailable", projectLabels(u, []string{"x"})["x"].State)
	_, err = slim(u)
	require.Error(t, err, "malformed metadata must not become a successful missing label")
	for _, keys := range [][]string{{""}, {"a", "a"}, {strings.Repeat("é", 127)}, {"a b"}, {"a\nb"}, {"a", "b", "c", "d", "e", "f", "g", "h", "i"}} {
		require.Error(t, core.ValidLabelKeys(keys))
	}
	require.NoError(t, core.ValidLabelKeys([]string{"x:y/z.a", "__proto__"}))
}

func TestLabelViewsShareLargeInventoryWithoutPerRowReadsAndKeepUIDUpdates(t *testing.T) {
	objs := make([]runtime.Object, 2000)
	for i := range objs {
		u := pod("ns", fmt.Sprintf("p-%04d", i), fmt.Sprintf("uid-%d", i))
		u.SetLabels(map[string]string{"app/version": fmt.Sprintf("v-%d", i)})
		objs[i] = u
	}
	client := fakeClient(objs...)
	h := newHarness(t, client)
	q := provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "ns"}}
	ordinary, err := h.m.Open("s", h.sess, q)
	require.NoError(t, err)
	h.until(ordinary, isReady)
	q.LabelKeys = []string{"app/version", "absent"}
	selected, err := h.m.Open("s", h.sess, q)
	require.NoError(t, err)
	p := h.until(selected, isReady)
	require.Len(t, p.Upserts, 2000)
	require.Equal(t, "v-1999", findRow(p, "p-1999").Labels["app/version"].Value)
	for _, a := range client.Actions() {
		require.NotEqual(t, "get", a.GetVerb())
		if a.GetVerb() == "list" || a.GetVerb() == "watch" {
			require.Equal(t, "ns", a.GetNamespace())
		}
	}
	lists := 0
	for _, a := range client.Actions() {
		if a.GetVerb() == "list" {
			lists++
		}
	}
	require.Equal(t, 1, lists, "projection reuses the warm cache")
	replacement := pod("ns", "p-1999", "replacement")
	replacement.SetLabels(map[string]string{"app/version": "new"})
	_, err = client.Resource(podGVR).Namespace("ns").Update(context.Background(), replacement, metav1.UpdateOptions{})
	require.NoError(t, err)
	p = h.until(selected, func(p views.Page) bool { return findRow(p, "p-1999").ID == "replacement" })
	require.Equal(t, "new", findRow(p, "p-1999").Labels["app/version"].Value)
	require.Nil(t, findRow(h.until(ordinary, func(p views.Page) bool { return findRow(p, "p-1999").ID == "replacement" }), "p-1999").Labels, "ordinary rows do not gain selected metadata")
}

func TestLabelProjectionCopiesPerViewAndRejectsInvalidQueries(t *testing.T) {
	h := newHarness(t, fakeClient())
	_, err := h.sess.Watch(provider.Query{Kind: "pods", Scope: all, LabelKeys: []string{"a", "a"}}, &rowSink{})
	require.Error(t, err)
	_, err = h.sess.Watch(provider.Query{Kind: "problems", Scope: all, LabelKeys: []string{"a"}}, &rowSink{})
	require.Error(t, err)
	u := pod("ns", "p", "u")
	u.SetLabels(map[string]string{"a": "1"})
	w := viewWatch{def: podsKind, now: time.Now, labelKeys: []string{"a"}}
	r, _ := w.row(u)
	u.SetLabels(map[string]string{"a": "2"})
	next, _ := w.row(u)
	require.Equal(t, "1", r.Labels["a"].Value)
	require.Equal(t, "2", next.Labels["a"].Value)
}

func TestLabelProjectionUsesMetadataFromRealServerTableRows(t *testing.T) {
	api := newWidgetsAPI()
	api.objs[0]["metadata"].(map[string]any)["labels"] = map[string]any{"x:y/z.a": "exact", "unselected": "omit"}
	s, _ := tableSession(t, api)
	q := provider.Query{Kind: "ocular.dev/widgets", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "ns"}, LabelKeys: []string{"x:y/z.a", "missing"}}
	desc, bound, err := s.DescribeView(context.Background(), q)
	require.NoError(t, err)
	require.True(t, desc.LabelColumns)
	h := &harness{t: t, sess: s, m: views.NewManager(events.NewEmitter())}
	t.Cleanup(h.m.CloseAll)
	id, err := h.m.Open("s", s, bound)
	require.NoError(t, err)
	p := h.until(id, isReady)
	require.Len(t, p.Upserts, 1)
	require.Equal(t, map[string]core.LabelValue{"x:y/z.a": {State: "value", Value: "exact"}, "missing": {State: "missing"}}, p.Upserts[0].Labels)
	require.Equal(t, "alpha", p.Upserts[0].Cells[0].Text, "server columns remain aligned")
	api.mu.Lock()
	defer api.mu.Unlock()
	for _, q := range api.queries {
		require.Equal(t, "Object", q.Get("includeObject"))
	}
}
