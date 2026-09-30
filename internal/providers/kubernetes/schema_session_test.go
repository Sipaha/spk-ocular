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
	if !strings.HasSuffix(r.URL.Path, "/widgets") {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	a.mu.Lock()
	a.queries = append(a.queries, q)
	status, plain, cols, objs := a.status, a.plain, a.cols, a.objs
	a.mu.Unlock()
	if q.Get("limit") == "1" {
		a.probes.Add(1)
	}
	switch {
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
			map[string]any{"name": "Size", "jsonPath": ".spec.size"}, map[string]any{"name": "Since", "jsonPath": ".metadata.creationTimestamp"}}}}}
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
