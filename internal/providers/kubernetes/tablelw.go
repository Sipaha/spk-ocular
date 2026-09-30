package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utiljson "k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

// Server-side Tables below the Reflector (P8): a discovered kind is read
// with the server's own columns (built-in printers, CRD printer columns)
// and every object of the answer. The Reflector never sees a Table: lists
// become lists of the resource's objects (versions, continue and remaining
// count kept), watch events the resource's objects (BOOKMARK with its
// annotations as is), ERROR stays a Status. The row's cells ride along in
// the object (cellsField); columns are checked against the view's schema on
// every answer that carries them.

const (
	// acceptTable asks for a Table only: a server that cannot answer one
	// says 406 (the probe decides the mode; lists and watches never switch).
	acceptTable = "application/json;as=Table;v=v1;g=meta.k8s.io"
	// acceptTableOrPlain: the probe's ask — a Table if possible.
	acceptTableOrPlain = acceptTable + ",application/json"
	// cellsField holds a row's cells in the cached object.
	cellsField = "ocularCells"
	// tableErrorBody bounds the body of a failed answer we read.
	tableErrorBody = 64 << 10
)

// errSchemaChanged: an answer's columns are not the view's schema; the
// answer is not applied (the session starts a new schema epoch).
var errSchemaChanged = errors.New("the columns of this resource changed")

// errNotTable: a Table was asked for and something else came.
var errNotTable = errors.New("the server did not answer with a table")

// errPlainList: a Table was asked for and the resource's plain list came —
// evidence that the resource does not serve Tables (like a 406).
var errPlainList = fmt.Errorf("%w: a plain list came", errNotTable)

// servesNoTables: err is evidence that the resource cannot answer Tables
// (not a failure that says nothing of the format: 410, 5xx, a Status).
func servesNoTables(err error) bool {
	return apierrors.IsNotAcceptable(err) || errors.Is(err, errPlainList)
}

// plainList: a body is a list of the resource's objects in the plain
// format (not a Table, not a Status, not garbage).
func plainList(kind string, items json.RawMessage) bool {
	return kind != "Status" && strings.HasSuffix(kind, "List") && len(items) > 0 && items[0] == '['
}

// tableClient reads resources as Tables over cfg's transport.
type tableClient struct {
	hc   *http.Client
	base *url.URL
	ua   string
}

func newTableClient(cfg *rest.Config) (*tableClient, error) {
	hc, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return nil, err
	}
	base, err := serverBase(cfg)
	if err != nil {
		return nil, err
	}
	return &tableClient{hc: hc, base: base, ua: cfg.UserAgent}, nil
}

// tableDoc is a Table as it comes (rows' objects decoded later).
type tableDoc struct {
	Kind              string                         `json:"kind"`
	Metadata          metav1.ListMeta                `json:"metadata"`
	ColumnDefinitions []metav1.TableColumnDefinition `json:"columnDefinitions"`
	Rows              []struct {
		Cells  []any           `json:"cells"`
		Object json.RawMessage `json:"object"`
	} `json:"rows"`
	// Items: a plain list's (a server answering without a Table).
	Items json.RawMessage `json:"items"`
}

// tablePage is one list answer.
type tablePage struct {
	cols      []metav1.TableColumnDefinition
	items     []unstructured.Unstructured
	rv, cont  string
	remaining *int64
}

func (c *tableClient) url(gvr schema.GroupVersionResource, ns, name string, q url.Values) string {
	u := *c.base
	p := "/api/" + gvr.Version
	if gvr.Group != "" {
		p = "/apis/" + gvr.Group + "/" + gvr.Version
	}
	if ns != "" {
		p += "/namespaces/" + url.PathEscape(ns)
	}
	p += "/" + gvr.Resource
	if name != "" {
		p += "/" + url.PathEscape(name)
	}
	u.Path = strings.TrimRight(u.Path, "/") + p
	u.RawQuery = q.Encode()
	return u.String()
}

func listQuery(o metav1.ListOptions) url.Values {
	q := url.Values{}
	if o.ResourceVersion != "" {
		q.Set("resourceVersion", o.ResourceVersion)
	}
	if o.ResourceVersionMatch != "" {
		q.Set("resourceVersionMatch", string(o.ResourceVersionMatch))
	}
	if o.Limit > 0 {
		q.Set("limit", fmt.Sprint(o.Limit))
	}
	if o.Continue != "" {
		q.Set("continue", o.Continue)
	}
	if o.FieldSelector != "" {
		q.Set("fieldSelector", o.FieldSelector)
	}
	if o.LabelSelector != "" {
		q.Set("labelSelector", o.LabelSelector)
	}
	if o.TimeoutSeconds != nil {
		q.Set("timeoutSeconds", fmt.Sprint(*o.TimeoutSeconds))
	}
	if o.AllowWatchBookmarks {
		q.Set("allowWatchBookmarks", "true")
	}
	q.Set("includeObject", "Object")
	return q
}

// do sends a GET; a non-200 answer is an API error (a Status when the
// server sent one).
func (c *tableClient) do(ctx context.Context, gvr schema.GroupVersionResource, u, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	if c.ua != "" {
		req.Header.Set("User-Agent", c.ua)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusOK {
		return resp, nil
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, tableErrorBody))
	var st metav1.Status
	if json.Unmarshal(body, &st) == nil && st.Kind == "Status" && st.Code != 0 {
		return nil, apierrors.FromObject(&st)
	}
	return nil, apierrors.NewGenericServerResponse(resp.StatusCode, "get", gvr.GroupResource(), "", strings.TrimSpace(string(body)), 0, false)
}

// list reads one page as a Table.
func (c *tableClient) list(ctx context.Context, gvr schema.GroupVersionResource, ns string, o metav1.ListOptions) (*tablePage, error) {
	resp, err := c.do(ctx, gvr, c.url(gvr, ns, "", listQuery(o)), acceptTable)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var d tableDoc
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return nil, err
	}
	if d.Kind != "Table" {
		if plainList(d.Kind, d.Items) {
			return nil, errPlainList
		}
		return nil, errNotTable
	}
	p := &tablePage{cols: d.ColumnDefinitions, rv: d.Metadata.ResourceVersion, cont: d.Metadata.Continue, remaining: d.Metadata.RemainingItemCount}
	p.items = make([]unstructured.Unstructured, 0, len(d.Rows))
	for _, r := range d.Rows {
		u, err := rowObject(r.Object, false)
		if err != nil {
			return nil, err
		}
		p.items = append(p.items, *withCells(u, r.Cells))
	}
	return p, nil
}

// get reads one object as a Table: the columns, the row's cells and its
// object as the server has it (the cells apart: the object is shown).
func (c *tableClient) get(ctx context.Context, gvr schema.GroupVersionResource, ns, name string) ([]metav1.TableColumnDefinition, []any, *unstructured.Unstructured, error) {
	resp, err := c.do(ctx, gvr, c.url(gvr, ns, name, url.Values{"includeObject": {"Object"}}), acceptTable)
	if err != nil {
		return nil, nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var d tableDoc
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return nil, nil, nil, err
	}
	if d.Kind != "Table" || len(d.Rows) != 1 {
		return nil, nil, nil, errNotTable // one object in the plain format: no list to tell by
	}
	u, err := rowObject(d.Rows[0].Object, false)
	if err != nil {
		return nil, nil, nil, err
	}
	return d.ColumnDefinitions, d.Rows[0].Cells, u, nil
}

// rowObject: a row's object, decoded like the API machinery does (whole
// numbers are int64: metadata.generation and the like read through
// unstructured's accessors). An object is required, with metadata; a
// row's (not a BOOKMARK's) also with its name.
func rowObject(raw json.RawMessage, bookmark bool) (*unstructured.Unstructured, error) {
	var m map[string]any
	if err := utiljson.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("a table row without its object: %w", err)
	}
	md, _ := m["metadata"].(map[string]any)
	if md == nil {
		return nil, errors.New("a table row without its object")
	}
	if name, _ := md["name"].(string); !bookmark && name == "" {
		return nil, errors.New("a table row whose object has no name")
	}
	return &unstructured.Unstructured{Object: m}, nil
}

// withCells: u carrying a row's cells (for the cache's projection).
func withCells(u *unstructured.Unstructured, cells []any) *unstructured.Unstructured {
	if cells == nil {
		cells = []any{}
	}
	u.Object[cellsField] = cells
	return u
}

// watch opens a Table watch stream. check sees the columns of the first
// event that carries them; an error ends the stream (reported by mismatch).
func (c *tableClient) watch(ctx context.Context, gvr schema.GroupVersionResource, ns string, o metav1.ListOptions, check func([]metav1.TableColumnDefinition) error, mismatch, tell func(error)) (watch.Interface, error) {
	o.Watch = true
	q := listQuery(o)
	q.Set("watch", "true")
	resp, err := c.do(ctx, gvr, c.url(gvr, ns, "", q), acceptTable)
	if err != nil {
		return nil, err
	}
	w := &tableWatch{body: resp.Body, out: make(chan watch.Event), stop: make(chan struct{}), check: check, mismatch: mismatch, tell: tell}
	go w.run()
	return w, nil
}

type tableWatch struct {
	body     io.ReadCloser
	out      chan watch.Event
	stop     chan struct{}
	once     sync.Once
	check    func([]metav1.TableColumnDefinition) error
	mismatch func(error)
	// tell sees the stream's errors (a 406 in it is evidence of no Tables).
	tell func(error)
}

func (w *tableWatch) run() {
	defer close(w.out)
	defer func() { _ = w.body.Close() }()
	dec := json.NewDecoder(w.body)
	for {
		var ev struct {
			Type   watch.EventType `json:"type"`
			Object json.RawMessage `json:"object"`
		}
		if err := dec.Decode(&ev); err != nil {
			return // ended, dropped or stopped: the reflector watches again
		}
		out, ok := w.event(ev.Type, ev.Object)
		if !ok {
			return
		}
		select {
		case w.out <- out:
		case <-w.stop:
			return
		}
	}
}

// event converts one Table event; false ends the stream.
func (w *tableWatch) event(t watch.EventType, raw json.RawMessage) (watch.Event, bool) {
	if t == watch.Error {
		var st metav1.Status
		if err := json.Unmarshal(raw, &st); err != nil {
			st = metav1.Status{Status: metav1.StatusFailure, Message: "an unreadable watch error", Code: http.StatusInternalServerError}
		}
		if w.tell != nil {
			w.tell(apierrors.FromObject(&st))
		}
		return watch.Event{Type: t, Object: &st}, true
	}
	var d tableDoc
	if err := json.Unmarshal(raw, &d); err != nil || d.Kind != "Table" || len(d.Rows) != 1 {
		w.mismatch(errNotTable)
		return watch.Event{}, false
	}
	// The server writes the columns with the stream's first event, whatever
	// its type (a BOOKMARK's too).
	if len(d.ColumnDefinitions) > 0 {
		if err := w.check(d.ColumnDefinitions); err != nil {
			w.mismatch(err)
			return watch.Event{}, false
		}
	}
	u, err := rowObject(d.Rows[0].Object, t == watch.Bookmark)
	if err != nil {
		w.mismatch(err)
		return watch.Event{}, false
	}
	if t != watch.Bookmark { // a BOOKMARK: RV and annotations only, as the server sent them
		withCells(u, d.Rows[0].Cells)
	}
	return watch.Event{Type: t, Object: u}, true
}

func (w *tableWatch) ResultChan() <-chan watch.Event { return w.out }

func (w *tableWatch) Stop() {
	w.once.Do(func() {
		close(w.stop)
		_ = w.body.Close()
	})
}

// tableListWatch is a Table kind's ListerWatcher: like statusListWatch
// (outcomes reported, header-only watch deadline), with the answers' columns
// checked against the schema. WatchList is never advertised: the initial
// state comes by List.
type tableListWatch struct {
	c         *tableClient
	gvr       schema.GroupVersionResource
	namespace string
	selector  string
	check     func([]metav1.TableColumnDefinition) error
	report    transportReport
	ended     func()
	counts    *requestCounts
	// notTable is told of evidence that the resource does not serve
	// Tables (406, a plain list): the epoch moves to the plain format.
	notTable func()
}

func (lw *tableListWatch) tell(err error) {
	if lw.notTable != nil && servesNoTables(err) {
		lw.notTable()
	}
}

var (
	_ cache.ListerWatcher            = (*tableListWatch)(nil)
	_ cache.ListerWatcherWithContext = (*tableListWatch)(nil)
)

func (lw *tableListWatch) opts(o metav1.ListOptions) metav1.ListOptions {
	if lw.selector != "" {
		o.FieldSelector = lw.selector
	}
	return o
}

func (lw *tableListWatch) ListWithContext(ctx context.Context, o metav1.ListOptions) (runtime.Object, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	lw.counts.lists.Add(1)
	p, err := lw.c.list(ctx, lw.gvr, lw.namespace, lw.opts(o))
	if err == nil {
		err = lw.check(p.cols) // a page of another schema is not applied
	}
	lw.tell(err)
	lw.report(err)
	if err != nil {
		return nil, err
	}
	l := &unstructured.UnstructuredList{Object: map[string]any{}, Items: p.items}
	l.SetResourceVersion(p.rv)
	l.SetContinue(p.cont)
	l.SetRemainingItemCount(p.remaining)
	return l, nil
}

func (lw *tableListWatch) WatchWithContext(ctx context.Context, o metav1.ListOptions) (watch.Interface, error) {
	ctx, cancel := context.WithCancel(ctx)
	lw.counts.watchStarts.Add(1)
	timer := time.AfterFunc(watchEstablishTimeout, cancel)
	w, err := lw.c.watch(ctx, lw.gvr, lw.namespace, lw.opts(o), lw.check, lw.report, lw.tell)
	if !timer.Stop() && err == nil {
		w.Stop()
		err = context.DeadlineExceeded
	}
	if err != nil {
		cancel()
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			err = fmt.Errorf("watch did not start within %s: %w", watchEstablishTimeout, context.DeadlineExceeded)
		}
		lw.tell(err)
		lw.report(err)
		return nil, err
	}
	lw.report(nil)
	return newReportingWatch(w, lw.report, lw.ended, cancel), nil
}

func (lw *tableListWatch) List(o metav1.ListOptions) (runtime.Object, error) {
	return lw.ListWithContext(context.Background(), o)
}

func (lw *tableListWatch) Watch(o metav1.ListOptions) (watch.Interface, error) {
	return lw.WatchWithContext(context.Background(), o)
}

// IsWatchListSemanticsUnSupported: a Table stream is not a WatchList one.
func (lw *tableListWatch) IsWatchListSemanticsUnSupported() bool { return true }
