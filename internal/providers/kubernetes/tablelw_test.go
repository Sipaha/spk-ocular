package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

var widgetCols = []metav1.TableColumnDefinition{
	{Name: "Name", Type: "string", Format: "name"},
	{Name: "Size", Type: "integer"},
	{Name: "Detail", Type: "string", Priority: 1},
}

func widgetObj(name, rv string) map[string]any {
	return map[string]any{"apiVersion": "ocular.dev/v1", "kind": "Widget",
		"metadata": map[string]any{"name": name, "namespace": "ns", "uid": "uid-" + name, "resourceVersion": rv, "creationTimestamp": "2026-09-30T10:00:00Z"},
		"spec":     map[string]any{"size": 5}}
}

func tableJSON(cols []metav1.TableColumnDefinition, rv, cont string, remaining *int64, rows ...map[string]any) map[string]any {
	var rs []any
	for _, o := range rows {
		rs = append(rs, map[string]any{"cells": []any{o["metadata"].(map[string]any)["name"], 5, nil}, "object": o})
	}
	md := map[string]any{"resourceVersion": rv}
	if cont != "" {
		md["continue"] = cont
	}
	if remaining != nil {
		md["remainingItemCount"] = *remaining
	}
	t := map[string]any{"kind": "Table", "apiVersion": "meta.k8s.io/v1", "metadata": md, "rows": rs}
	if cols != nil {
		t["columnDefinitions"] = cols
	}
	return t
}

// tableServer serves the widgets collection: list answers per the handler,
// watch streams the given events and holds the stream open.
type tableServer struct {
	mu      sync.Mutex
	queries []url.Values
	accepts []string
	list    func(q url.Values) (int, any)
	events  []map[string]any
	hold    time.Duration
}

func (ts *tableServer) start(t *testing.T) *tableClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/ocular.dev/v1/namespaces/ns/widgets" {
			http.NotFound(w, r)
			return
		}
		ts.mu.Lock()
		ts.queries = append(ts.queries, r.URL.Query())
		ts.accepts = append(ts.accepts, r.Header.Get("Accept"))
		ts.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("watch") == "true" {
			f := w.(http.Flusher)
			for _, ev := range ts.events {
				b, _ := json.Marshal(ev)
				_, _ = w.Write(append(b, '\n'))
				f.Flush()
			}
			select {
			case <-time.After(ts.hold):
			case <-r.Context().Done():
			}
			return
		}
		code, body := ts.list(r.URL.Query())
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	c, err := newTableClient(&rest.Config{Host: srv.URL})
	require.NoError(t, err)
	return c
}

func (ts *tableServer) seen() ([]url.Values, []string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return append([]url.Values{}, ts.queries...), append([]string{}, ts.accepts...)
}

func widgetsLW(c *tableClient, check func([]metav1.TableColumnDefinition) error, report func(error)) *tableListWatch {
	if check == nil {
		check = func([]metav1.TableColumnDefinition) error { return nil }
	}
	if report == nil {
		report = func(error) {}
	}
	return &tableListWatch{c: c, gvr: widgetsGVR, namespace: "ns", check: check, report: report, counts: &requestCounts{}}
}

// A Table list becomes the resource's objects with their cells; versions,
// continue and the remaining count are kept.
func TestTableListIsTheResourcesObjects(t *testing.T) {
	two := int64(1)
	ts := &tableServer{list: func(q url.Values) (int, any) {
		if q.Get("continue") == "" {
			return 200, tableJSON(widgetCols, "10", "page2", &two, widgetObj("alpha", "7"))
		}
		return 200, tableJSON(widgetCols, "10", "", nil, widgetObj("beta", "8"))
	}}
	c := ts.start(t)
	var checked [][]metav1.TableColumnDefinition
	lw := widgetsLW(c, func(cols []metav1.TableColumnDefinition) error { checked = append(checked, cols); return nil }, nil)
	obj, err := lw.List(metav1.ListOptions{Limit: 1, ResourceVersion: "0"})
	require.NoError(t, err)
	l := obj.(*unstructured.UnstructuredList)
	assert.Equal(t, "10", l.GetResourceVersion())
	assert.Equal(t, "page2", l.GetContinue())
	require.NotNil(t, l.GetRemainingItemCount())
	assert.Equal(t, int64(1), *l.GetRemainingItemCount())
	require.Len(t, l.Items, 1)
	a := l.Items[0]
	assert.Equal(t, "Widget", a.GetKind())
	assert.Equal(t, "ocular.dev/v1", a.GetAPIVersion())
	assert.Equal(t, "7", a.GetResourceVersion())
	assert.Equal(t, []any{"alpha", float64(5), nil}, a.Object[cellsField])
	assert.Equal(t, widgetCols, checked[0], "the page's columns are checked")

	obj, err = lw.List(metav1.ListOptions{Limit: 1, Continue: "page2"})
	require.NoError(t, err)
	assert.Equal(t, "beta", obj.(*unstructured.UnstructuredList).Items[0].GetName())

	qs, accepts := ts.seen()
	assert.Equal(t, "Object", qs[0].Get("includeObject"))
	assert.Equal(t, "1", qs[0].Get("limit"))
	assert.Equal(t, "0", qs[0].Get("resourceVersion"))
	assert.Equal(t, "page2", qs[1].Get("continue"))
	assert.Equal(t, acceptTable, accepts[0], "a Table only: no silent plain answer")
}

// A page of another schema is not applied.
func TestTableListOfAnotherSchemaIsNotApplied(t *testing.T) {
	ts := &tableServer{list: func(url.Values) (int, any) { return 200, tableJSON(widgetCols, "10", "", nil, widgetObj("alpha", "7")) }}
	var reported []error
	lw := widgetsLW(ts.start(t), func([]metav1.TableColumnDefinition) error { return errSchemaChanged },
		func(err error) { reported = append(reported, err) })
	_, err := lw.List(metav1.ListOptions{})
	assert.ErrorIs(t, err, errSchemaChanged)
	assert.Equal(t, []error{errSchemaChanged}, reported)
}

// Failures are API errors (classified like the dynamic client's); a plain
// answer to a Table ask is not a list.
func TestTableFailuresAreAPIErrors(t *testing.T) {
	answers := []struct {
		code int
		body any
		is   func(error) bool
	}{
		{403, map[string]any{"kind": "Status", "apiVersion": "v1", "status": "Failure", "reason": "Forbidden", "code": 403, "message": "rbac"}, apierrors.IsForbidden},
		{406, "not acceptable", apierrors.IsNotAcceptable},
		{410, map[string]any{"kind": "Status", "apiVersion": "v1", "status": "Failure", "reason": "Expired", "code": 410}, apierrors.IsResourceExpired},
		{200, map[string]any{"kind": "WidgetList", "apiVersion": "ocular.dev/v1", "items": []any{}}, func(err error) bool { return errors.Is(err, errNotTable) }},
	}
	for _, a := range answers {
		ts := &tableServer{list: func(url.Values) (int, any) { return a.code, a.body }}
		_, err := widgetsLW(ts.start(t), nil, nil).List(metav1.ListOptions{})
		assert.True(t, a.is(err), "%d: %v", a.code, err)
	}
}

// Watch: the stream's first event carries the columns (checked), later ones
// do not; BOOKMARK keeps its annotations; ERROR stays a Status.
func TestTableWatchEvents(t *testing.T) {
	bm := map[string]any{"apiVersion": "ocular.dev/v1", "kind": "Widget", "metadata": map[string]any{"resourceVersion": "20", "annotations": map[string]any{"k8s.io/initial-events-end": "true"}}}
	ts := &tableServer{hold: 2 * time.Second, events: []map[string]any{
		{"type": "ADDED", "object": tableJSON(widgetCols, "", "", nil, widgetObj("alpha", "11"))},
		{"type": "MODIFIED", "object": tableJSON(nil, "", "", nil, widgetObj("alpha", "12"))},
		{"type": "BOOKMARK", "object": map[string]any{"kind": "Table", "apiVersion": "meta.k8s.io/v1", "metadata": map[string]any{"resourceVersion": "20"}, "columnDefinitions": nil,
			"rows": []any{map[string]any{"cells": []any{"", nil, nil}, "object": bm}}}},
		{"type": "ERROR", "object": map[string]any{"kind": "Status", "apiVersion": "v1", "status": "Failure", "reason": "Expired", "code": 410, "message": "too old"}},
	}}
	c := ts.start(t)
	var checks int
	w, err := widgetsLW(c, func([]metav1.TableColumnDefinition) error { checks++; return nil }, nil).Watch(metav1.ListOptions{ResourceVersion: "10", AllowWatchBookmarks: true})
	require.NoError(t, err)
	defer w.Stop()
	var evs []watch.Event
	for len(evs) < 4 {
		select {
		case ev := <-w.ResultChan():
			evs = append(evs, ev)
		case <-time.After(5 * time.Second):
			t.Fatalf("got %d events", len(evs))
		}
	}
	assert.Equal(t, 1, checks, "only the first event carries columns")
	assert.Equal(t, watch.Added, evs[0].Type)
	assert.Equal(t, "11", evs[0].Object.(*unstructured.Unstructured).GetResourceVersion())
	assert.Equal(t, "12", evs[1].Object.(*unstructured.Unstructured).GetResourceVersion())
	b := evs[2].Object.(*unstructured.Unstructured)
	assert.Equal(t, watch.Bookmark, evs[2].Type)
	assert.Equal(t, "Widget", b.GetKind())
	assert.Equal(t, "20", b.GetResourceVersion())
	assert.Equal(t, map[string]string{"k8s.io/initial-events-end": "true"}, b.GetAnnotations())
	assert.NotContains(t, b.Object, cellsField)
	st := evs[3].Object.(*metav1.Status)
	assert.Equal(t, watch.Error, evs[3].Type)
	assert.Equal(t, int32(410), st.Code)
	qs, _ := ts.seen()
	assert.Equal(t, "true", qs[0].Get("watch"))
	assert.Equal(t, "true", qs[0].Get("allowWatchBookmarks"))
	assert.Empty(t, qs[0].Get("sendInitialEvents"))
}

// A stream whose columns are not the schema's ends at once: nothing of it
// is applied.
func TestTableWatchOfAnotherSchemaEnds(t *testing.T) {
	ts := &tableServer{hold: 2 * time.Second, events: []map[string]any{
		{"type": "ADDED", "object": tableJSON(widgetCols, "", "", nil, widgetObj("alpha", "11"))},
	}}
	var reported []error
	var mu sync.Mutex
	lw := widgetsLW(ts.start(t), func([]metav1.TableColumnDefinition) error { return errSchemaChanged },
		func(err error) { mu.Lock(); reported = append(reported, err); mu.Unlock() })
	w, err := lw.Watch(metav1.ListOptions{ResourceVersion: "10"})
	require.NoError(t, err)
	defer w.Stop()
	select {
	case ev, ok := <-w.ResultChan():
		assert.False(t, ok, "no event of another schema: %v", ev)
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not end")
	}
	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, reported, errSchemaChanged)
}

// End to end under an informer: List + Watch (never WatchList), changes and
// deletions reach the store.
func TestTableUnderAnInformer(t *testing.T) {
	ts := &tableServer{
		list: func(url.Values) (int, any) {
			return 200, tableJSON(widgetCols, "10", "", nil, widgetObj("alpha", "7"), widgetObj("beta", "8"))
		},
		hold: 3 * time.Second,
		events: []map[string]any{
			{"type": "MODIFIED", "object": tableJSON(widgetCols, "", "", nil, widgetObj("alpha", "11"))},
			{"type": "DELETED", "object": tableJSON(nil, "", "", nil, widgetObj("beta", "12"))},
		},
	}
	lw := widgetsLW(ts.start(t), nil, nil)
	inf := cache.NewSharedIndexInformerWithOptions(lw, &unstructured.Unstructured{}, cache.SharedIndexInformerOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go inf.RunWithContext(ctx)
	require.Eventually(t, func() bool {
		keys := inf.GetStore().ListKeys()
		if len(keys) != 1 {
			return false
		}
		o, _, _ := inf.GetStore().GetByKey("ns/alpha")
		return o != nil && o.(*unstructured.Unstructured).GetResourceVersion() == "11"
	}, 10*time.Second, 10*time.Millisecond)
	qs, _ := ts.seen()
	for _, q := range qs {
		assert.Empty(t, q.Get("sendInitialEvents"), "no WatchList for a Table kind")
	}
}

// A row's object reads like the dynamic client's: whole numbers are int64
// (metadata.generation through the accessors).
func TestTableRowObjectNumbers(t *testing.T) {
	u, err := rowObject([]byte(`{"apiVersion":"ocular.dev/v1","kind":"Widget","metadata":{"name":"a","generation":2},"spec":{"size":5,"ratio":0.5}}`), false)
	require.NoError(t, err)
	assert.Equal(t, int64(2), u.GetGeneration())
	assert.Equal(t, int64(5), u.Object["spec"].(map[string]any)["size"])
	assert.Equal(t, 0.5, u.Object["spec"].(map[string]any)["ratio"])
	assert.Equal(t, 5.0, *tableCell("integer", int64(5)).Num)
}

// A row without its object (null, not an object, no metadata or no name)
// is a failed answer, never a panic or a row without identity (Codex P2-1
// of c932672).
func TestTableRowWithoutItsObjectIsAnError(t *testing.T) {
	bad := []string{`null`, `"x"`, `{"apiVersion":"ocular.dev/v1","kind":"Widget"}`, `{"metadata":null}`, `{"metadata":{"namespace":"ns"}}`}
	for _, raw := range bad {
		_, err := rowObject([]byte(raw), false)
		assert.Error(t, err, raw)
	}
	ts := &tableServer{list: func(url.Values) (int, any) {
		return 200, map[string]any{"kind": "Table", "apiVersion": "meta.k8s.io/v1", "metadata": map[string]any{"resourceVersion": "10"},
			"columnDefinitions": widgetCols, "rows": []any{map[string]any{"cells": []any{"a", 5, nil}, "object": nil}}}
	}}
	_, err := widgetsLW(ts.start(t), nil, nil).List(metav1.ListOptions{})
	assert.Error(t, err)

	ws := &tableServer{hold: 2 * time.Second, events: []map[string]any{
		{"type": "ADDED", "object": map[string]any{"kind": "Table", "apiVersion": "meta.k8s.io/v1", "metadata": map[string]any{}, "columnDefinitions": widgetCols,
			"rows": []any{map[string]any{"cells": []any{"a", 5, nil}, "object": nil}}}},
	}}
	var mu sync.Mutex
	var reported []error
	w, err := widgetsLW(ws.start(t), nil, func(err error) { mu.Lock(); reported = append(reported, err); mu.Unlock() }).Watch(metav1.ListOptions{ResourceVersion: "10"})
	require.NoError(t, err)
	defer w.Stop()
	select {
	case ev, ok := <-w.ResultChan():
		assert.False(t, ok, "no event without an object: %v", ev)
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not end")
	}
	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, reported)
	assert.Error(t, reported[len(reported)-1])
}

// The server writes the columns with the stream's first event whatever its
// type: a first BOOKMARK's columns are checked too (Codex P2-7 of c932672).
func TestTableWatchChecksTheColumnsOfAFirstBookmark(t *testing.T) {
	bm := map[string]any{"apiVersion": "ocular.dev/v1", "kind": "Widget", "metadata": map[string]any{"resourceVersion": "20"}}
	ts := &tableServer{hold: 2 * time.Second, events: []map[string]any{
		{"type": "BOOKMARK", "object": map[string]any{"kind": "Table", "apiVersion": "meta.k8s.io/v1", "metadata": map[string]any{"resourceVersion": "20"},
			"columnDefinitions": widgetCols, "rows": []any{map[string]any{"cells": []any{"", nil, nil}, "object": bm}}}},
		{"type": "MODIFIED", "object": tableJSON(nil, "", "", nil, widgetObj("alpha", "21"))},
	}}
	var mu sync.Mutex
	var reported []error
	lw := widgetsLW(ts.start(t), func([]metav1.TableColumnDefinition) error { return errSchemaChanged },
		func(err error) { mu.Lock(); reported = append(reported, err); mu.Unlock() })
	w, err := lw.Watch(metav1.ListOptions{ResourceVersion: "10", AllowWatchBookmarks: true})
	require.NoError(t, err)
	defer w.Stop()
	select {
	case ev, ok := <-w.ResultChan():
		assert.False(t, ok, "nothing after headers of another schema: %v", ev)
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not end")
	}
	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, reported, errSchemaChanged)
}

// Evidence that the resource cannot answer Tables (406, a plain list) is
// told once to notTable; failures that say nothing of the format are not.
func TestTableListWatchTellsWhenTablesAreNotServed(t *testing.T) {
	answers := []struct {
		code int
		body any
		not  bool
	}{
		{406, "not acceptable", true},
		{200, map[string]any{"kind": "WidgetList", "apiVersion": "ocular.dev/v1", "items": []any{}}, true},
		{200, map[string]any{"kind": "Status", "apiVersion": "v1", "status": "Failure", "code": 500}, false},
		{500, map[string]any{"kind": "Status", "apiVersion": "v1", "status": "Failure", "code": 500}, false},
		{410, map[string]any{"kind": "Status", "apiVersion": "v1", "status": "Failure", "reason": "Expired", "code": 410}, false},
	}
	for _, a := range answers {
		ts := &tableServer{list: func(url.Values) (int, any) { return a.code, a.body }}
		lw := widgetsLW(ts.start(t), nil, nil)
		var told int
		lw.notTable = func() { told++ }
		_, err := lw.List(metav1.ListOptions{})
		assert.Error(t, err)
		assert.Equal(t, a.not, told == 1, "%d %v: told %d", a.code, a.body, told)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotAcceptable) }))
	t.Cleanup(srv.Close)
	c, err := newTableClient(&rest.Config{Host: srv.URL})
	require.NoError(t, err)
	lw := widgetsLW(c, nil, nil)
	var told int
	lw.notTable = func() { told++ }
	_, err = lw.Watch(metav1.ListOptions{ResourceVersion: "10"})
	assert.True(t, apierrors.IsNotAcceptable(err), "%v", err)
	assert.Equal(t, 1, told, "a watch refused 406")
}
