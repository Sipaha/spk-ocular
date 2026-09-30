package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// probeTimeout bounds finding a view's schema (the probe and the CRD read).
var probeTimeout = 15 * time.Second

// schemaEntry is a discovered resource's current schema epoch.
type schemaEntry struct {
	cur   *tableSchema // nil: none (not probed yet, or the last one changed)
	probe chan struct{}
	// gen grows with every invalidation (the epoch ended, a CRD changed,
	// F5, the resource no longer served): a probe begun before publishes
	// nothing.
	gen uint64
	// checking: a revalidation runs (one at a time); again — asked for
	// while it ran (one more follows, however many asked).
	checking, again bool
}

type schemaStore struct {
	mu     sync.Mutex
	by     map[schema.GroupVersionResource]*schemaEntry
	epochs uint64 // epochs given so far (never reused within the session)
	// plain: resources that showed they do not serve Tables (406, a plain
	// list) — the plain format for the rest of the session.
	plain map[schema.GroupVersionResource]bool
	// crdPrints: per CRD, what its resource's columns are made of (scope,
	// versions, printer columns): a CRD event that keeps it checks nothing.
	crdPrints map[string]string
}

func (st *schemaStore) entry(gvr schema.GroupVersionResource) *schemaEntry {
	if st.by == nil {
		st.by = map[schema.GroupVersionResource]*schemaEntry{}
	}
	e := st.by[gvr]
	if e == nil {
		e = &schemaEntry{}
		st.by[gvr] = e
	}
	return e
}

var _ provider.ViewDescriber = (*session)(nil)

// DescribeView gives a view of a discovered kind the columns of the
// resource's current schema epoch (probing the server once when there is
// none) and binds the query to it. Described kinds keep their columns.
func (s *session) DescribeView(ctx context.Context, q provider.Query) (core.KindDescriptor, provider.Query, error) {
	def := s.kind(q.Kind)
	if def == nil {
		if s.KindRemoved(q.Kind) {
			return core.KindDescriptor{}, q, &provider.Error{Class: provider.ClassRemoved, Message: fmt.Sprintf("%s is no longer served", q.Kind)}
		}
		return core.KindDescriptor{}, q, &provider.Error{Class: provider.ClassUnsupported, Message: fmt.Sprintf("unknown kind %q", q.Kind)}
	}
	desc := s.descriptor(def)
	if !def.discovered {
		return desc, q, nil
	}
	sch, err := s.schemaFor(ctx, def, q)
	if err != nil {
		return core.KindDescriptor{}, q, err
	}
	desc.Columns = sch.cols
	q.Schema = sch.epoch
	return desc, q, nil
}

// descriptor: the kind as Kinds() describes it.
func (s *session) descriptor(def *kindDef) core.KindDescriptor {
	for _, d := range newKindRegistry(def).descriptors() {
		return d
	}
	return def.desc
}

// schemaFor returns def's current schema; without one it probes the
// server — one probe per resource at a time, others wait for it. q's scope
// and name bound the probe (it is never wider than the view, for RBAC).
func (s *session) schemaFor(ctx context.Context, def *kindDef, q provider.Query) (*tableSchema, error) {
	for {
		s.schemas.mu.Lock()
		e := s.schemas.entry(def.gvr)
		if e.cur != nil {
			cur := e.cur
			s.schemas.mu.Unlock()
			return cur, nil
		}
		if s.tables == nil { // no server to ask (tests): the plain format
			s.schemas.epochs++
			e.cur = newTableSchema(def, s.schemas.epochs, false, nil, nil)
			cur := e.cur
			s.schemas.mu.Unlock()
			return cur, nil
		}
		if ch := e.probe; ch != nil {
			s.schemas.mu.Unlock()
			select {
			case <-ch:
				continue // its answer, or our own probe if it failed
			case <-ctx.Done():
				return nil, &provider.Error{Class: provider.ClassUnavailable, Message: ctx.Err().Error()}
			}
		}
		ch := make(chan struct{})
		e.probe = ch
		gen := e.gen
		s.schemas.mu.Unlock()
		sch, err := s.probeSchema(ctx, def, q)
		s.schemas.mu.Lock()
		e.probe = nil
		close(ch)
		switch {
		case s.ctx.Err() != nil:
			s.schemas.mu.Unlock()
			return nil, &provider.Error{Class: provider.ClassUnavailable, Message: "the connection to the cluster was closed"}
		case err != nil:
			s.schemas.mu.Unlock()
			class, msg := classify(err)
			return nil, &provider.Error{Class: class, Message: msg}
		case e.gen != gen:
			// Invalidated while probing (a CRD changed, F5): the answer may
			// be the old one's. Probe again — on the route still served.
			s.schemas.mu.Unlock()
			if d := s.kind(def.desc.ID); d == nil || d.gvr != def.gvr {
				return nil, &provider.Error{Class: provider.ClassSchemaChanged, Message: def.desc.Title + ": the API resource changed; opening again"}
			}
			continue
		case sch.table && s.schemas.plain[def.gvr]:
			// No Tables was shown meanwhile (another probe, a stream): this
			// answer is not published; the next round reads plain.
			s.schemas.mu.Unlock()
			continue
		case e.cur == nil:
			s.schemas.epochs++
			sch.epoch = s.schemas.epochs
			e.cur = sch
		}
		cur := e.cur
		s.schemas.mu.Unlock()
		return cur, nil
	}
}

// probeSchema asks for one object as a Table (with the view's scope) and
// builds the schema its answer shows; the answer is not cached rows.
func (s *session) probeSchema(ctx context.Context, def *kindDef, q provider.Query) (*tableSchema, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	defer context.AfterFunc(s.ctx, cancel)() // it ends with the session too
	ns := ""
	if def.namespaced && q.Scope.Mode == core.ScopeOne {
		ns = q.Scope.Name
	}
	o := metav1.ListOptions{Limit: 1}
	if q.Name != "" {
		o.FieldSelector = "metadata.name=" + q.Name
	}
	return s.probeRoute(ctx, def, ns, o)
}

func (s *session) probeRoute(ctx context.Context, def *kindDef, ns string, o metav1.ListOptions) (*tableSchema, error) {
	s.schemas.mu.Lock()
	plain := s.schemas.plain[def.gvr]
	s.schemas.mu.Unlock()
	if plain {
		return newTableSchema(def, 0, false, nil, nil), nil
	}
	cols, table, err := s.tables.probe(ctx, def.gvr, ns, o)
	if err != nil {
		return nil, err
	}
	if !table { // evidence: the plain format for the rest of the session
		s.schemas.mu.Lock()
		s.setPlainLocked(def.gvr)
		s.schemas.mu.Unlock()
		return newTableSchema(def, 0, false, nil, nil), nil
	}
	var crd crdColumns
	if gr := def.gvr.GroupResource(); !builtinAge[gr] {
		crd = s.crdColumns(ctx, def.gvr)
	}
	return newTableSchema(def, 0, true, cols, ageColumns(def.gvr.GroupResource(), cols, crd)), nil
}

// probe reads one page asking for a Table or the plain format: whether the
// server answers Tables for the resource, and its columns.
func (c *tableClient) probe(ctx context.Context, gvr schema.GroupVersionResource, ns string, o metav1.ListOptions) ([]metav1.TableColumnDefinition, bool, error) {
	resp, err := c.do(ctx, gvr, c.url(gvr, ns, "", listQuery(o)), acceptTableOrPlain)
	if apierrors.IsNotAcceptable(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = resp.Body.Close() }()
	var d struct {
		Kind              string                         `json:"kind"`
		ColumnDefinitions []metav1.TableColumnDefinition `json:"columnDefinitions"`
		Items             json.RawMessage                `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return nil, false, err
	}
	switch {
	case d.Kind == "Table":
	case plainList(d.Kind, d.Items):
		return nil, false, nil // a list of objects: no Tables here
	default: // a Status, garbage: nothing said of the format
		return nil, false, fmt.Errorf("%s: an unexpected answer (%q) to a list", gvr.Resource, d.Kind)
	}
	if len(d.ColumnDefinitions) == 0 {
		return nil, false, fmt.Errorf("%s: a table without columns", gvr.Resource)
	}
	return d.ColumnDefinitions, true, nil
}

// crdColumns reads what the resource's CRD says of its columns in the
// served version (for the provenance of age columns). Not a CRD (an
// aggregated API), no right to read CRDs, or any failure: nothing known.
func (s *session) crdColumns(ctx context.Context, gvr schema.GroupVersionResource) crdColumns {
	if s.crdDenied.Load() {
		return crdColumns{}
	}
	crdVersion := schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	if d := s.kind(crdKindID); d != nil {
		crdVersion = d.gvr
	}
	u, err := s.dyn.Resource(crdVersion).Get(ctx, gvr.Resource+"."+gvr.Group, metav1.GetOptions{})
	switch {
	case apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err):
		s.crdDenied.Store(true)
		return crdColumns{}
	case err != nil:
		return crdColumns{}
	}
	// The baseline a CRD event is compared with (an event's is newer).
	s.schemas.mu.Lock()
	if s.schemas.crdPrints == nil {
		s.schemas.crdPrints = map[string]string{}
	}
	if _, ok := s.schemas.crdPrints[u.GetName()]; !ok {
		s.schemas.crdPrints[u.GetName()] = crdPrint(u)
	}
	s.schemas.mu.Unlock()
	return crdColumnsOf(u, gvr.Version)
}

func crdColumnsOf(u *unstructured.Unstructured, version string) crdColumns {
	for _, v := range slice(u.Object, "spec", "versions") {
		if str(v, "name") != version {
			continue
		}
		out := crdColumns{found: true}
		for _, c := range slice(v, "additionalPrinterColumns") {
			out.printer = append(out.printer, printerColumn{name: str(c, "name"), typ: str(c, "type"), jsonPath: str(c, "jsonPath"), priority: i64(c, "priority")})
		}
		return out
	}
	return crdColumns{}
}

// crdPrint: what of a CRD its resource's columns are made of — scope,
// versions served, printer columns (not status, labels, …).
func crdPrint(u *unstructured.Unstructured) string {
	type version struct {
		Name    string `json:"name"`
		Served  any    `json:"served"`
		Printer any    `json:"printer"`
	}
	var vs []version
	for _, v := range slice(u.Object, "spec", "versions") {
		vs = append(vs, version{Name: str(v, "name"), Served: v["served"], Printer: v["additionalPrinterColumns"]})
	}
	b, _ := json.Marshal(struct {
		Scope    string    `json:"scope"`
		Versions []version `json:"versions"`
	}{str(u.Object, "spec", "scope"), vs})
	return string(b)
}

// currentSchema: def's schema for a view bound to epoch (no I/O). nil: the
// epoch is not the current one (the view must be opened again).
func (s *session) currentSchema(def *kindDef, epoch uint64) *tableSchema {
	s.schemas.mu.Lock()
	defer s.schemas.mu.Unlock()
	e := s.schemas.entry(def.gvr)
	if s.tables == nil && e.cur == nil { // tests without a server: plain
		s.schemas.epochs++
		e.cur = newTableSchema(def, s.schemas.epochs, false, nil, nil)
	}
	if e.cur == nil || (epoch != 0 && e.cur.epoch != epoch) || (epoch == 0 && s.tables != nil) {
		return nil
	}
	return e.cur
}

// schemaChanged: an answer of old's resource came with other columns. The
// epoch ends: the next view probes the new one, views of old end "schema
// changed" (their UI opens them again) and old's caches stop.
func (s *session) schemaChanged(old *tableSchema) {
	s.schemas.mu.Lock()
	e := s.schemas.entry(old.gvr)
	if e.cur != old {
		s.schemas.mu.Unlock()
		return // already ended
	}
	e.cur = nil
	e.gen++
	old.retired.Store(true)
	s.schemas.mu.Unlock()
	s.endViews(func(w *viewWatch) bool { return w.def.schema == old }, provider.ClassSchemaChanged,
		old.def.desc.Title+": the columns changed; opening again")
	s.caches.retire(old)
}

// endViews ends the views that match (final status, observation released).
func (s *session) endViews(match func(*viewWatch) bool, class provider.ErrorClass, msg string) {
	for _, w := range s.caches.allWatchers() {
		if match(w) {
			w.end(class, msg)
		}
	}
}

// tablesUnsupported: old's resource showed it does not serve Tables (a
// 406 or a plain list to a Table list or watch, in the stream too).
func (s *session) tablesUnsupported(old *tableSchema) { s.markPlain(old.gvr) }

// markPlain: gvr showed it does not serve Tables. The plain format sticks
// for the session (recorded even without an epoch); a Table probe in
// flight is fenced, a Table epoch ends and its views open again plain.
func (s *session) markPlain(gvr schema.GroupVersionResource) {
	if cur := s.setPlain(gvr); cur != nil {
		s.schemaChanged(cur)
	}
}

// setPlain records gvr's plain format and fences its Table probe in
// flight; it returns the current Table epoch (to be ended), if any.
func (s *session) setPlain(gvr schema.GroupVersionResource) *tableSchema {
	s.schemas.mu.Lock()
	defer s.schemas.mu.Unlock()
	s.setPlainLocked(gvr)
	e := s.schemas.by[gvr]
	if e == nil {
		return nil
	}
	if e.probe != nil {
		e.gen++
	}
	if e.cur != nil && e.cur.table {
		return e.cur
	}
	return nil
}

func (s *session) setPlainLocked(gvr schema.GroupVersionResource) {
	if s.schemas.plain == nil {
		s.schemas.plain = map[schema.GroupVersionResource]bool{}
	}
	s.schemas.plain[gvr] = true
}

// revalidate checks the schemas in use again (a CRD changed, F5): one whose
// answer shows other columns or another provenance ends; a first probe in
// flight is fenced (its answer may be the old one's). only nil: all.
func (s *session) revalidate(only *schema.GroupVersionResource) {
	if s.tables == nil {
		return
	}
	s.schemas.mu.Lock()
	var gvrs []schema.GroupVersionResource
	for gvr, e := range s.schemas.by {
		if only != nil && *only != gvr {
			continue
		}
		switch {
		case e.cur != nil:
			gvrs = append(gvrs, gvr)
		case e.probe != nil:
			e.gen++
		}
	}
	s.schemas.mu.Unlock()
	for _, gvr := range gvrs {
		s.revalidateOne(gvr)
	}
}

// revalidateOne: one check of a resource at a time; asks while it runs
// coalesce into one more check after it.
func (s *session) revalidateOne(gvr schema.GroupVersionResource) {
	s.schemas.mu.Lock()
	e := s.schemas.by[gvr]
	if e == nil {
		s.schemas.mu.Unlock()
		return
	}
	if e.checking {
		e.again = true
		s.schemas.mu.Unlock()
		return
	}
	e.checking = true
	s.schemas.mu.Unlock()
	for {
		s.checkSchema(e)
		s.schemas.mu.Lock()
		if !e.again || s.ctx.Err() != nil {
			e.checking, e.again = false, false
			s.schemas.mu.Unlock()
			return
		}
		e.again = false
		s.schemas.mu.Unlock()
	}
}

// checkSchema probes e's current schema with the route of a view using it;
// unused, it is forgotten (the next view probes afresh).
func (s *session) checkSchema(e *schemaEntry) {
	s.schemas.mu.Lock()
	sch := e.cur
	s.schemas.mu.Unlock()
	if sch == nil {
		return
	}
	ns, sel, used := s.caches.routeOf(sch)
	if !used {
		s.schemas.mu.Lock()
		if e.cur == sch {
			e.cur = nil
			e.gen++
			sch.retired.Store(true)
		}
		s.schemas.mu.Unlock()
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, probeTimeout)
	defer cancel()
	next, err := s.probeRoute(ctx, sch.def, ns, metav1.ListOptions{Limit: 1, FieldSelector: sel})
	switch {
	case err != nil:
		return // not an answer about the columns: the views say what fails
	case !next.table:
		// Evidence of no Tables: whatever Table epoch is current now (not
		// only the one read) ends, and probes in flight are fenced.
		s.markPlain(sch.gvr)
	case !next.same(sch):
		s.schemaChanged(sch)
	}
}

// forgetSchemas drops the epochs of resources the catalog no longer serves
// at that version (a CRD deleted, a version moved): a resource served again
// is probed afresh.
func (s *session) forgetSchemas(reg *kindRegistry) {
	served := map[schema.GroupVersionResource]bool{}
	for _, d := range reg.list {
		served[d.gvr] = true
	}
	s.schemas.mu.Lock()
	var ended []*tableSchema
	for gvr, e := range s.schemas.by {
		switch {
		case served[gvr]:
		case e.probe != nil:
			e.gen++ // its answer is not published: the route is gone
		default:
			if e.cur != nil {
				e.cur.retired.Store(true)
				ended = append(ended, e.cur)
			}
			delete(s.schemas.by, gvr)
		}
	}
	s.schemas.mu.Unlock()
	// Views still on such an epoch (a version moved; removed kinds ended
	// already) open again on what is served now.
	for _, sch := range ended {
		s.endViews(func(w *viewWatch) bool { return w.def.schema == sch }, provider.ClassSchemaChanged,
			sch.def.desc.Title+": the columns changed; opening again")
		s.caches.retire(sch)
	}
}
