package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// widgetsAPI serves widgets as Tables (all namespaces and one); tests
// change its columns and objects. A watch holds its stream open, sending
// what push gives it.
type widgetsAPI struct {
	mu      sync.Mutex
	cols    []metav1.TableColumnDefinition
	objs    []map[string]any
	status  int // non-zero: every request fails with it
	plain   bool
	probes  atomic.Int32 // limit=1 requests
	queries []url.Values
	streams []chan map[string]any
	// watch406: watches are refused 406 (lists still answer Tables).
	watch406 bool
	// probeBody: a 200 body limit=1 requests get instead of the table.
	probeBody any
	// probeDelay holds limit=1 requests (inflight/maxInflight count them);
	// probeHold holds them until closed or the request is cancelled
	// (probeCancelled counts the latter).
	probeDelay     time.Duration
	probeHold      chan struct{}
	inflight       atomic.Int32
	maxInflight    atomic.Int32
	probeCancelled atomic.Int32
}

func (a *widgetsAPI) setCols(c []metav1.TableColumnDefinition) {
	a.mu.Lock()
	a.cols = c
	a.mu.Unlock()
}

// push sends an event to every open watch.
func (a *widgetsAPI) push(ev map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range a.streams {
		s <- ev
	}
}

func (a *widgetsAPI) serve(w http.ResponseWriter, r *http.Request) {
	if i := strings.Index(r.URL.Path, "/widgets/"); i >= 0 {
		a.serveOne(w, r, r.URL.Path[i+len("/widgets/"):])
		return
	}
	if !strings.HasSuffix(r.URL.Path, "/widgets") {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	a.mu.Lock()
	a.queries = append(a.queries, q)
	status, plain, cols, objs, w406, probeBody, delay, hold := a.status, a.plain, a.cols, a.objs, a.watch406, a.probeBody, a.probeDelay, a.probeHold
	a.mu.Unlock()
	if q.Get("limit") == "1" {
		a.probes.Add(1)
		n := a.inflight.Add(1)
		for m := a.maxInflight.Load(); n > m; m = a.maxInflight.Load() {
			if a.maxInflight.CompareAndSwap(m, n) {
				break
			}
		}
		if delay > 0 {
			time.Sleep(delay)
		}
		if hold != nil {
			select {
			case <-hold:
			case <-r.Context().Done():
				a.probeCancelled.Add(1)
			}
		}
		a.inflight.Add(-1)
		if probeBody != nil {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(probeBody)
			return
		}
	}
	switch {
	case w406 && q.Get("watch") == "true":
		w.WriteHeader(http.StatusNotAcceptable)
		return
	case status != 0:
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"kind": "Status", "apiVersion": "v1", "status": "Failure", "code": status, "reason": http.StatusText(status)})
		return
	case plain && strings.Contains(r.Header.Get("Accept"), "application/json,") || plain && !strings.Contains(r.Header.Get("Accept"), "as=Table"):
		_ = json.NewEncoder(w).Encode(map[string]any{"kind": "WidgetList", "apiVersion": "ocular.dev/v1", "metadata": map[string]any{"resourceVersion": "10"}, "items": objs})
		return
	case plain:
		w.WriteHeader(http.StatusNotAcceptable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if q.Get("watch") != "true" {
		_ = json.NewEncoder(w).Encode(tableJSON(cols, "10", "", nil, objs...))
		return
	}
	ch := make(chan map[string]any, 8)
	a.mu.Lock()
	a.streams = append(a.streams, ch)
	a.mu.Unlock()
	f := w.(http.Flusher)
	f.Flush()
	for {
		select {
		case ev := <-ch:
			b, _ := json.Marshal(ev)
			_, _ = w.Write(append(b, '\n'))
			f.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// serveOne answers a GET of one widget: a one-row Table (406 when plain).
func (a *widgetsAPI) serveOne(w http.ResponseWriter, _ *http.Request, name string) {
	a.mu.Lock()
	plain, cols, objs := a.plain, a.cols, a.objs
	a.mu.Unlock()
	for _, o := range objs {
		if o["metadata"].(map[string]any)["name"] != name {
			continue
		}
		if plain {
			w.WriteHeader(http.StatusNotAcceptable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"kind": "Table", "apiVersion": "meta.k8s.io/v1", "metadata": map[string]any{}, "columnDefinitions": cols,
			"rows": []any{map[string]any{"cells": []any{name, 5, "2m", "wide text"}, "object": o}}})
		return
	}
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(map[string]any{"kind": "Status", "apiVersion": "v1", "status": "Failure", "reason": "NotFound", "code": 404})
}

// tableSession: a session whose discovered widgets are read from api as
// Tables; dyn serves the rest (CRDs for provenance).
func tableSession(t *testing.T, api *widgetsAPI, objs ...runtime.Object) (*session, *scriptedAPI) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(api.serve))
	t.Cleanup(srv.Close)
	tc, err := newTableClient(&rest.Config{Host: srv.URL})
	require.NoError(t, err)
	disc := &scriptedAPI{}
	disc.set("/api", coreDoc)
	disc.set("/apis", apisDoc(map[string][]v2ver{"ocular.dev": {widgets}}, "ocular.dev"))
	s := newSession("ctx", "h", catalogClient(objs...), false)
	s.tables, s.caches.tables = tc, tc
	t.Cleanup(s.Close)
	s.startCatalog(disc.get)
	waitRev(t, s.cat, 2)
	return s, disc
}

var allNS = core.ScopeSel{Mode: core.ScopeAll}

func newWidgetsAPI() *widgetsAPI {
	return &widgetsAPI{cols: []metav1.TableColumnDefinition{
		{Name: "Name", Type: "string", Format: "name"}, {Name: "Size", Type: "integer"}, {Name: "Since", Type: "date"}, {Name: "Detail", Type: "string", Priority: 1},
	}, objs: []map[string]any{widgetObj("alpha", "7")}}
}

func colIDs(cs []core.Column) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return out
}

// OpenView's descriptor comes from one probe (one object, the view's own
// scope), shared by concurrent opens; the view is bound to its epoch.
func TestDescribeViewProbesOnceWithTheViewsScope(t *testing.T) {
	api := newWidgetsAPI()
	s, _ := tableSession(t, api)
	q := provider.Query{Kind: "ocular.dev/widgets", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "ns"}, Name: "alpha"}
	var wg sync.WaitGroup
	descs := make([]core.KindDescriptor, 5)
	qs := make([]provider.Query, 5)
	for i := range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var err error
			descs[i], qs[i], err = s.DescribeView(context.Background(), q)
			assert.NoError(t, err)
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(1), api.probes.Load(), "one probe for all")
	assert.Equal(t, []string{"name", "namespace", "size", "since"}, colIDs(descs[0].Columns))
	assert.Equal(t, core.ColText, descs[0].Columns[3].Type, "the CRD not readable: Since is the server's text")
	assert.Equal(t, "Widget", descs[0].Singular)
	assert.Equal(t, []core.ActionDescriptor{actDelete}, descs[0].Actions)
	assert.NotZero(t, qs[0].Schema)
	for _, x := range qs {
		assert.Equal(t, qs[0].Schema, x.Schema)
	}
	api.mu.Lock()
	probe := api.queries[0]
	api.mu.Unlock()
	assert.Equal(t, "metadata.name=alpha", probe.Get("fieldSelector"))

	// described kinds keep their columns and are not bound
	d, pq, err := s.DescribeView(context.Background(), provider.Query{Kind: "pods", Scope: allNS})
	require.NoError(t, err)
	assert.Equal(t, podsKind.desc.Columns, d.Columns)
	assert.Zero(t, pq.Schema)
}

// The view's rows are the server's cells, typed; a view bound to another
// epoch is told to open again.
func TestTableViewRowsAndEpochBinding(t *testing.T) {
	api := newWidgetsAPI()
	s, _ := tableSession(t, api)
	_, q, err := s.DescribeView(context.Background(), provider.Query{Kind: "ocular.dev/widgets", Scope: allNS})
	require.NoError(t, err)
	sink := &deltaSink{}
	stop, err := s.Watch(q, sink)
	require.NoError(t, err)
	defer stop()
	require.Eventually(t, func() bool { st := sink.status(); return st != nil && st.State == provider.StatusReady }, 5*time.Second, 5*time.Millisecond)
	row := sink.rows()["uid-alpha"]
	require.Len(t, row.Cells, 4)
	assert.Equal(t, "alpha", row.Cells[0].Text)
	assert.Equal(t, 5.0, *row.Cells[2].Num)

	stale := q
	stale.Schema++
	_, err = s.Watch(stale, &deltaSink{})
	var perr *provider.Error
	require.True(t, errors.As(err, &perr))
	assert.Equal(t, provider.ClassSchemaChanged, perr.Class)
	unbound := q
	unbound.Schema = 0
	_, err = s.Watch(unbound, &deltaSink{})
	require.True(t, errors.As(err, &perr), "a view of a Table kind must be bound")
}

func (r *deltaSink) rows() map[string]core.Row {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]core.Row{}
	for _, d := range r.d {
		for _, row := range d.Upserts {
			out[row.ID] = row
		}
	}
	return out
}

// Other columns in any answer end the epoch: the view ends "schema
// changed", its cache stops, the next open probes the new columns.
func TestOtherColumnsEndTheEpoch(t *testing.T) {
	for _, via := range []string{"watch", "refresh"} {
		t.Run(via, func(t *testing.T) {
			api := newWidgetsAPI()
			s, _ := tableSession(t, api)
			_, q, err := s.DescribeView(context.Background(), provider.Query{Kind: "ocular.dev/widgets", Scope: allNS})
			require.NoError(t, err)
			sink := &deltaSink{}
			stop, err := s.Watch(q, sink)
			require.NoError(t, err)
			defer stop()
			require.Eventually(t, func() bool { st := sink.status(); return st != nil && st.State == provider.StatusReady }, 5*time.Second, 5*time.Millisecond)
			require.Eventually(t, func() bool { api.mu.Lock(); defer api.mu.Unlock(); return len(api.streams) > 0 }, 5*time.Second, 5*time.Millisecond)

			newCols := []metav1.TableColumnDefinition{{Name: "Name", Type: "string", Format: "name"}, {Name: "Phase", Type: "string"}}
			api.setCols(newCols)
			if via == "watch" {
				api.push(map[string]any{"type": "MODIFIED", "object": tableJSON(newCols, "", "", nil, widgetObj("alpha", "11"))})
			} else {
				s.RefreshKinds() // F5: the columns are checked again
			}
			require.Eventually(t, func() bool { st := sink.status(); return st != nil && st.Class == provider.ClassSchemaChanged }, 5*time.Second, 5*time.Millisecond)
			n := sink.count()
			rows := sink.rows()
			assert.Equal(t, "7", rows["uid-alpha"].Rev, "nothing of the other schema was applied")
			active, _ := s.caches.stats()
			assert.Zero(t, active)
			d, q2, err := s.DescribeView(context.Background(), provider.Query{Kind: "ocular.dev/widgets", Scope: allNS})
			require.NoError(t, err)
			assert.Greater(t, q2.Schema, q.Schema)
			assert.Equal(t, []string{"name", "namespace", "phase"}, colIDs(d.Columns))
			time.Sleep(50 * time.Millisecond)
			assert.Equal(t, n, sink.count(), "the ended view gets nothing more")
		})
	}
}

// A server that cannot answer Tables: the plain format, through dyn.
func TestNoTablesMeansThePlainFormat(t *testing.T) {
	api := newWidgetsAPI()
	api.plain = true
	s, _ := tableSession(t, api)
	d, q, err := s.DescribeView(context.Background(), provider.Query{Kind: "ocular.dev/widgets", Scope: allNS})
	require.NoError(t, err)
	assert.Equal(t, []core.Column{colName, colNS, colAge}, d.Columns)
	stop, err := s.Watch(q, &deltaSink{})
	require.NoError(t, err)
	stop()
}

// A denied probe is the view's failure (forbidden), not a schema.
func TestADeniedProbeIsForbidden(t *testing.T) {
	api := newWidgetsAPI()
	api.status = http.StatusForbidden
	s, _ := tableSession(t, api)
	_, _, err := s.DescribeView(context.Background(), provider.Query{Kind: "ocular.dev/widgets", Scope: allNS})
	var perr *provider.Error
	require.True(t, errors.As(err, &perr))
	assert.Equal(t, provider.ClassForbidden, perr.Class)
	api.mu.Lock()
	api.status = 0
	api.mu.Unlock()
	_, q, err := s.DescribeView(context.Background(), provider.Query{Kind: "ocular.dev/widgets", Scope: allNS})
	require.NoError(t, err, "a later open probes again")
	assert.NotZero(t, q.Schema)
}

// Provenance from the CRD: its column path makes Since a live age; without
// the right to read CRDs it stays the server's text.
func TestCRDColumnPathMakesALiveAge(t *testing.T) {
	crdObj := crd("widgets.ocular.dev")
	crdObj.Object["spec"] = map[string]any{"group": "ocular.dev", "names": map[string]any{"plural": "widgets"}, "versions": []any{
		map[string]any{"name": "v1", "additionalPrinterColumns": []any{
			map[string]any{"name": "Size", "type": "integer", "jsonPath": ".spec.size"}, map[string]any{"name": "Since", "type": "date", "jsonPath": ".metadata.creationTimestamp"},
			map[string]any{"name": "Detail", "type": "string", "priority": int64(1), "jsonPath": ".spec.detail"}}}}}
	api := newWidgetsAPI()
	s, _ := tableSession(t, api, crdObj)
	d, _, err := s.DescribeView(context.Background(), provider.Query{Kind: "ocular.dev/widgets", Scope: allNS})
	require.NoError(t, err)
	assert.Equal(t, core.ColAge, d.Columns[3].Type)

	api2 := newWidgetsAPI()
	s2, _ := tableSession(t, api2)
	s2.dyn.(interface {
		PrependReactor(string, string, k8stesting.ReactionFunc)
	}).PrependReactor("get", "customresourcedefinitions", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errForbiddenCRD
	})
	d, _, err = s2.DescribeView(context.Background(), provider.Query{Kind: "ocular.dev/widgets", Scope: allNS})
	require.NoError(t, err)
	assert.Equal(t, core.ColText, d.Columns[3].Type)
	assert.True(t, s2.crdDenied.Load())
}

var errForbiddenCRD = apierrors.NewForbidden(crdGVR.GroupResource(), "widgets.ocular.dev", errors.New("rbac"))

// A resource whose lists answer Tables but whose watch is refused 406
// cannot be kept live as Tables: the epoch ends once, the next open reads
// the plain format and stays there (Codex P2-2 of c932672).
func TestAWatchRefusingTablesMovesTheResourceToThePlainFormat(t *testing.T) {
	api := newWidgetsAPI()
	api.watch406 = true
	s, _ := tableSession(t, api)
	_, q, err := s.DescribeView(context.Background(), provider.Query{Kind: "ocular.dev/widgets", Scope: allNS})
	require.NoError(t, err)
	sink := &deltaSink{}
	stop, err := s.Watch(q, sink)
	require.NoError(t, err)
	defer stop()
	require.Eventually(t, func() bool { st := sink.status(); return st != nil && st.Class == provider.ClassSchemaChanged }, 5*time.Second, 5*time.Millisecond)
	probes := api.probes.Load()
	d, q2, err := s.DescribeView(context.Background(), provider.Query{Kind: "ocular.dev/widgets", Scope: allNS})
	require.NoError(t, err)
	assert.Equal(t, []core.Column{colName, colNS, colAge}, d.Columns)
	assert.Greater(t, q2.Schema, q.Schema)
	assert.Equal(t, probes, api.probes.Load(), "the plain format sticks: no probe asks for a Table again")
	stop2, err := s.Watch(q2, &deltaSink{})
	require.NoError(t, err)
	stop2()
}

// A probe answered 200 with something that is neither a Table nor a list
// (a Status, garbage) is no evidence of the plain format.
func TestAProbeAnsweredWithAStatusIsAnError(t *testing.T) {
	api := newWidgetsAPI()
	api.probeBody = map[string]any{"kind": "Status", "apiVersion": "v1", "status": "Failure", "code": 500}
	s, _ := tableSession(t, api)
	_, _, err := s.DescribeView(context.Background(), provider.Query{Kind: "ocular.dev/widgets", Scope: allNS})
	require.Error(t, err)
	api.mu.Lock()
	api.probeBody = nil
	api.mu.Unlock()
	d, _, err := s.DescribeView(context.Background(), provider.Query{Kind: "ocular.dev/widgets", Scope: allNS})
	require.NoError(t, err)
	assert.Equal(t, []string{"name", "namespace", "size", "since"}, colIDs(d.Columns), "Tables after all")
}

// A probe in flight when the CRD changes answers for the old CRD: its
// result is not published, the schema is probed again (Codex P2-3 of
// c932672).
func TestARevalidationFencesAProbeInFlight(t *testing.T) {
	crdObj := crd("widgets.ocular.dev")
	crdObj.Object["spec"] = map[string]any{"group": "ocular.dev", "names": map[string]any{"plural": "widgets"}, "versions": []any{
		map[string]any{"name": "v1", "additionalPrinterColumns": []any{
			map[string]any{"name": "Size", "type": "integer", "jsonPath": ".spec.size"}, map[string]any{"name": "Since", "type": "date", "jsonPath": ".metadata.creationTimestamp"},
			map[string]any{"name": "Detail", "type": "string", "priority": int64(1), "jsonPath": ".spec.detail"}}}}}
	api := newWidgetsAPI()
	s, _ := tableSession(t, api, crdObj.DeepCopy())
	entered, release := make(chan struct{}), make(chan struct{})
	var reads atomic.Int32
	s.dyn.(interface {
		PrependReactor(string, string, k8stesting.ReactionFunc)
	}).PrependReactor("get", "customresourcedefinitions", func(k8stesting.Action) (bool, runtime.Object, error) {
		if reads.Add(1) == 1 {
			close(entered)
			<-release
			return true, crdObj.DeepCopy(), nil // the CRD as it was
		}
		return false, nil, nil
	})
	type res struct {
		d   core.KindDescriptor
		err error
	}
	done := make(chan res, 1)
	go func() {
		d, _, err := s.DescribeView(context.Background(), provider.Query{Kind: "ocular.dev/widgets", Scope: allNS})
		done <- res{d, err}
	}()
	<-entered
	// The CRD's Since is no longer the creation time.
	changed := crdObj.DeepCopy()
	cols := changed.Object["spec"].(map[string]any)["versions"].([]any)[0].(map[string]any)["additionalPrinterColumns"].([]any)
	cols[1].(map[string]any)["jsonPath"] = ".status.since"
	// Through the tracker: the client's lock is held by the reactor.
	require.NoError(t, s.dyn.(*dynamicfake.FakeDynamicClient).Tracker().Update(crdGVR, changed, "", metav1.UpdateOptions{}))
	s.crdChanged(changed)
	close(release)
	r := <-done
	require.NoError(t, r.err)
	assert.Equal(t, core.ColText, r.d.Columns[3].Type, "the old CRD's provenance was published")
	assert.Equal(t, int32(2), reads.Load())
}

// Revalidations of one resource share one probe at a time, however many
// CRD events and F5s ask (Codex P2-4 of c932672).
func TestRevalidationsOfAResourceShareOneFlight(t *testing.T) {
	api := newWidgetsAPI()
	s, _ := tableSession(t, api)
	_, q, err := s.DescribeView(context.Background(), provider.Query{Kind: "ocular.dev/widgets", Scope: allNS})
	require.NoError(t, err)
	sink := &deltaSink{}
	stop, err := s.Watch(q, sink)
	require.NoError(t, err)
	defer stop()
	require.Eventually(t, func() bool { st := sink.status(); return st != nil && st.State == provider.StatusReady }, 5*time.Second, 5*time.Millisecond)
	api.mu.Lock()
	api.probeDelay = 100 * time.Millisecond
	api.mu.Unlock()
	before := api.probes.Load()
	gvr := widgetsGVR
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(2)
		go func() { defer wg.Done(); s.revalidate(&gvr) }()
		go func() { defer wg.Done(); s.revalidate(nil) }()
	}
	wg.Wait()
	assert.Equal(t, int32(1), api.maxInflight.Load(), "one probe of the resource at a time")
	assert.LessOrEqual(t, api.probes.Load()-before, int32(2), "asks while one runs coalesce into one more")
}

// A CRD event that does not touch what the columns are made of (status,
// labels) checks nothing again.
func TestACRDEventWithTheSameColumnsChecksNothing(t *testing.T) {
	crdObj := crd("widgets.ocular.dev")
	crdObj.Object["spec"] = map[string]any{"group": "ocular.dev", "names": map[string]any{"plural": "widgets"}, "versions": []any{
		map[string]any{"name": "v1", "additionalPrinterColumns": []any{map[string]any{"name": "Size", "type": "integer", "jsonPath": ".spec.size"}}}}}
	api := newWidgetsAPI()
	s, _ := tableSession(t, api, crdObj.DeepCopy())
	_, q, err := s.DescribeView(context.Background(), provider.Query{Kind: "ocular.dev/widgets", Scope: allNS})
	require.NoError(t, err)
	stop, err := s.Watch(q, &deltaSink{})
	require.NoError(t, err)
	defer stop()
	before := api.probes.Load()
	status := crdObj.DeepCopy()
	status.Object["status"] = map[string]any{"acceptedNames": map[string]any{"plural": "widgets"}}
	status.SetLabels(map[string]string{"x": "y"})
	s.crdChanged(status)
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, before, api.probes.Load(), "nothing of the columns changed")
	cols := status.Object["spec"].(map[string]any)["versions"].([]any)[0].(map[string]any)
	cols["additionalPrinterColumns"] = []any{map[string]any{"name": "Size", "type": "string", "jsonPath": ".spec.size"}}
	s.crdChanged(status)
	require.Eventually(t, func() bool { return api.probes.Load() > before }, 5*time.Second, 5*time.Millisecond, "the printer columns changed")
}

// The first probe of a view ends with its session (and publishes nothing
// into a closed one) (Codex P2-6 of c932672).
func TestSessionCloseCancelsASchemaProbe(t *testing.T) {
	api := newWidgetsAPI()
	api.probeHold = make(chan struct{})
	defer close(api.probeHold)
	s, _ := tableSession(t, api)
	done := make(chan error, 1)
	go func() {
		_, _, err := s.DescribeView(context.Background(), provider.Query{Kind: "ocular.dev/widgets", Scope: allNS})
		done <- err
	}()
	require.Eventually(t, func() bool { return api.inflight.Load() == 1 }, 5*time.Second, 5*time.Millisecond)
	s.Close()
	select {
	case err := <-done:
		assert.Error(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("the probe outlived its session")
	}
	require.Eventually(t, func() bool { return api.probeCancelled.Load() == 1 }, 3*time.Second, 5*time.Millisecond)
}

// Details of a Table kind: the object exactly as the server has it (a field
// of its own named like our cells is its own), facts with the wide columns
// (Codex P2-3 of 77b7b11).
func TestTableGetKeepsTheObjectAsIs(t *testing.T) {
	api := newWidgetsAPI()
	api.objs[0]["ocularCells"] = map[string]any{"real": "payload"}
	s, _ := tableSession(t, api)
	r, err := s.Get(context.Background(), core.Ref{Kind: "ocular.dev/widgets", Scope: "ns", Name: "alpha"})
	require.NoError(t, err)
	assert.Contains(t, r.YAML, "ocularCells:\n  real: payload")
	assert.Contains(t, r.Facts, core.Detail{Key: "Detail", Value: "wide text"})
}

// Evidence that a resource serves no Tables sticks wherever it is seen:
// the first probe, a revalidation, details read before any view (Codex
// re-review of 5ab7a17: Task 2 #2).
func TestNoTablesEvidenceSticksOnEveryPath(t *testing.T) {
	serveTables := func(api *widgetsAPI, on bool) {
		api.mu.Lock()
		api.plain = !on
		api.mu.Unlock()
	}
	plainCols := []core.Column{colName, colNS, colAge}
	t.Run("probe", func(t *testing.T) {
		api := newWidgetsAPI()
		api.plain = true
		s, _ := tableSession(t, api)
		d, _, err := s.DescribeView(context.Background(), provider.Query{Kind: "ocular.dev/widgets", Scope: allNS})
		require.NoError(t, err)
		require.Equal(t, plainCols, d.Columns)
		serveTables(api, true)
		s.revalidate(nil) // no view uses it: forgotten
		probes := api.probes.Load()
		d, _, err = s.DescribeView(context.Background(), provider.Query{Kind: "ocular.dev/widgets", Scope: allNS})
		require.NoError(t, err)
		assert.Equal(t, plainCols, d.Columns)
		assert.Equal(t, probes, api.probes.Load())
	})
	t.Run("revalidation", func(t *testing.T) {
		api := newWidgetsAPI()
		s, _ := tableSession(t, api)
		_, q, err := s.DescribeView(context.Background(), provider.Query{Kind: "ocular.dev/widgets", Scope: allNS})
		require.NoError(t, err)
		sink := &deltaSink{}
		stop, err := s.Watch(q, sink)
		require.NoError(t, err)
		defer stop()
		require.Eventually(t, func() bool { st := sink.status(); return st != nil && st.State == provider.StatusReady }, 5*time.Second, 5*time.Millisecond)
		serveTables(api, false)
		s.revalidate(nil)
		require.Eventually(t, func() bool { st := sink.status(); return st != nil && st.Class == provider.ClassSchemaChanged }, 5*time.Second, 5*time.Millisecond)
		serveTables(api, true)
		d, _, err := s.DescribeView(context.Background(), provider.Query{Kind: "ocular.dev/widgets", Scope: allNS})
		require.NoError(t, err)
		assert.Equal(t, plainCols, d.Columns)
	})
	t.Run("details", func(t *testing.T) {
		api := newWidgetsAPI()
		api.plain = true
		s, _ := tableSession(t, api)
		s.dyn.(interface {
			PrependReactor(string, string, k8stesting.ReactionFunc)
		}).PrependReactor("get", "widgets", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, widget("alpha"), nil
		})
		_, err := s.Get(context.Background(), core.Ref{Kind: "ocular.dev/widgets", Scope: "ns", Name: "alpha"})
		require.NoError(t, err)
		serveTables(api, true)
		probes := api.probes.Load()
		d, _, err := s.DescribeView(context.Background(), provider.Query{Kind: "ocular.dev/widgets", Scope: allNS})
		require.NoError(t, err)
		assert.Equal(t, plainCols, d.Columns)
		assert.Equal(t, probes, api.probes.Load())
	})
}
