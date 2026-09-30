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
}

type schemaStore struct {
	mu     sync.Mutex
	by     map[schema.GroupVersionResource]*schemaEntry
	epochs uint64 // epochs given so far (never reused within the session)
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
		s.schemas.mu.Unlock()
		sch, err := s.probeSchema(ctx, def, q)
		s.schemas.mu.Lock()
		e.probe = nil
		close(ch)
		if err == nil && e.cur == nil {
			s.schemas.epochs++
			sch.epoch = s.schemas.epochs
			e.cur = sch
		}
		cur := e.cur
		s.schemas.mu.Unlock()
		if err != nil {
			class, msg := classify(err)
			return nil, &provider.Error{Class: class, Message: msg}
		}
		return cur, nil
	}
}

// probeSchema asks for one object as a Table (with the view's scope) and
// builds the schema its answer shows; the answer is not cached rows.
func (s *session) probeSchema(ctx context.Context, def *kindDef, q provider.Query) (*tableSchema, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
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
	cols, table, err := s.tables.probe(ctx, def.gvr, ns, o)
	if err != nil {
		return nil, err
	}
	if !table {
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
	}
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return nil, false, err
	}
	if d.Kind != "Table" {
		return nil, false, nil // a list of objects: no Tables here
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
	return crdColumnsOf(u, gvr.Version)
}

func crdColumnsOf(u *unstructured.Unstructured, version string) crdColumns {
	for _, v := range slice(u.Object, "spec", "versions") {
		if str(v, "name") != version {
			continue
		}
		out := crdColumns{found: true, printer: map[string]string{}}
		for _, c := range slice(v, "additionalPrinterColumns") {
			out.printer[str(c, "name")] = str(c, "jsonPath")
		}
		return out
	}
	return crdColumns{}
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

// revalidate probes the schemas in use again (a CRD changed, F5): one whose
// answer shows other columns or another provenance ends. gvr nil: all.
func (s *session) revalidate(only *schema.GroupVersionResource) {
	if s.tables == nil {
		return
	}
	s.schemas.mu.Lock()
	var cur []*tableSchema
	for gvr, e := range s.schemas.by {
		if e.cur != nil && (only == nil || *only == gvr) {
			cur = append(cur, e.cur)
		}
	}
	s.schemas.mu.Unlock()
	for _, sch := range cur {
		ns, sel, used := s.caches.routeOf(sch)
		if !used {
			// No view uses it: forget it, the next view probes afresh.
			s.schemas.mu.Lock()
			if e := s.schemas.entry(sch.gvr); e.cur == sch {
				e.cur = nil
				sch.retired.Store(true)
			}
			s.schemas.mu.Unlock()
			continue
		}
		ctx, cancel := context.WithTimeout(s.ctx, probeTimeout)
		o := metav1.ListOptions{Limit: 1, FieldSelector: sel}
		next, err := s.probeRoute(ctx, sch.def, ns, o)
		cancel()
		if err != nil {
			continue // not an answer about the columns: the views say what fails
		}
		if !next.same(sch) {
			s.schemaChanged(sch)
		}
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
		if !served[gvr] && e.probe == nil {
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
