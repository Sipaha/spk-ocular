package kubernetes

import (
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// Problems: what is wrong in the scope now, by the ordinary kinds' own
// projections (a row here and the object's row in its table agree by
// construction), plus recent Warning events as evidence — not proof that
// something is broken now.
//
// A Problems view is one ordinary viewWatch per source (its own lease,
// order gate, deadlines and status) whose rows pass through a filter into
// one view; nothing is shared between sources, so no scheduler spans them.

var problemsKind = &kindDef{
	desc: core.KindDescriptor{
		ID: "problems", Title: "Problems", Group: "Health", Scoped: true,
		// The worst first; among equals the most recent first.
		Sort: &core.SortSpec{Column: "severity", Desc: true, Then: "since"},
		// Not observed (the sources are problemSources).
		NotCovered: []string{"Jobs", "CronJobs", "PersistentVolumeClaims", "custom resources"},
		Columns: []core.Column{
			{ID: "severity", Title: "Severity", Type: core.ColStatus, Width: 90},
			{ID: "kind", Title: "Kind", Type: core.ColText, Width: 110},
			colNS,
			colName,
			{ID: "reason", Title: "Reason", Type: core.ColText, Width: 160},
			{ID: "message", Title: "Message", Type: core.ColText},
			{ID: "since", Title: "Since", Type: core.ColAge, Width: 80},
		},
	},
	virtual: true,
}

// problemSource is one kind a Problems view observes.
type problemSource struct {
	def *kindDef // as observed for Problems (see problemDef)
	// title names the source in the coverage.
	title string
	// selector narrows its cache (Warning events only).
	selector string
}

// problemSources, in coverage order. Jobs, CronJobs, PVCs and custom
// resources are not observed (problemsKind.NotCovered says so).
var problemSources = []problemSource{
	{def: problemDef(podsKind, false), title: "Pods"},
	{def: problemDef(deploymentsKind, false), title: "Deployments"},
	{def: problemDef(statefulSetsKind, false), title: "StatefulSets"},
	{def: problemDef(daemonSetsKind, false), title: "DaemonSets"},
	{def: problemDef(servicesKind, false), title: "Services"},
	{def: problemDef(ingressesKind, false), title: "Ingresses"},
	{def: problemDef(nodesKind, false), title: "Nodes"},
	{def: problemDef(eventsKind, true), title: "Warning events", selector: "type=Warning"},
}

// Severity ranks for sorting (Cell.Num): worse is higher.
var severityRank = map[string]float64{"error": 4, "warning": 3, "unknown": 2, "recent": 1}

// problemDef observes kind d for Problems: the same cache (kind ID, gvr,
// whitelist) and its own health; a row is in Problems' columns. Events
// count as a problem only while they are recent (with a deadline for the
// end of it), unlike the Events table, which is their inventory.
func problemDef(d *kindDef, recent bool) *kindDef {
	pd := *d
	singular := kindOf(d)
	pd.project = func(u *unstructured.Unstructured, now time.Time) ([]core.Cell, core.Health, time.Time) {
		_, h, next := d.project(u, now)
		name := u.GetName()
		severity := string(h.State)
		if recent {
			h = core.Health{State: core.HealthOK}
			is, n, ok := recentWarning(u, now)
			if ok {
				h = core.HealthFrom([]core.Issue{is})
			}
			next = n
			severity = "recent"
			name = strings.ToLower(str(u.Object, "involvedObject", "kind")) + "/" + str(u.Object, "involvedObject", "name")
		}
		since := core.Cell{}
		if len(h.Issues) > 0 && h.Issues[0].Since != 0 {
			since = core.TimeCell(h.Issues[0].Since)
		}
		sev := core.NumCell(severityRank[severity], severity)
		sev.Muted = recent // evidence, not a current state
		return []core.Cell{
			sev, core.TextCell(singular), core.TextCell(u.GetNamespace()), core.TextCell(name),
			core.TextCell(h.Reason), core.TextCell(h.Message), since,
		}, h, next
	}
	return &pd
}

// isProblem: what Problems shows. Progressing and terminating are not
// (a rollout runs, a pod waits for its grace period) until a time rule has
// made them a warning.
func isProblem(h core.Health) bool {
	switch h.State {
	case core.HealthError, core.HealthWarning, core.HealthUnknown:
		return true
	}
	return false
}

// problemsView merges its sources into one sink. Sources deliver
// concurrently (each from its own informer, and statuses outside their
// data order), so the merge is under one lock.
type problemsView struct {
	mu      sync.Mutex
	sink    provider.Sink
	stopped bool
	cov     []provider.SourceCoverage
	emitted []map[string]bool // per source: the problem rows it has in the view
	last    provider.ViewStatus
}

// problemsFeed is one source's sink.
type problemsFeed struct {
	v      *problemsView
	i      int
	prefix string
}

// Apply filters a source's delta: problems in, the rest out. A source's
// Reset replaces only that source's rows — passed on as a Reset it would
// erase every other source's rows.
func (f *problemsFeed) Apply(d provider.Delta) {
	v := f.v
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.stopped {
		return // a late delivery after close
	}
	mine := v.emitted[f.i]
	var out provider.Delta
	var kept map[string]bool
	if d.Reset {
		kept = map[string]bool{}
	}
	for _, r := range d.Upserts {
		id := f.prefix + r.ID
		switch {
		case isProblem(r.Health):
			r.ID = id
			out.Upserts = append(out.Upserts, r)
			mine[id] = true
			if kept != nil {
				kept[id] = true
			}
		case mine[id]:
			delete(mine, id)
			out.Deletes = append(out.Deletes, id)
		}
	}
	if d.Reset {
		for id := range mine {
			if !kept[id] {
				delete(mine, id)
				out.Deletes = append(out.Deletes, id)
			}
		}
	}
	for _, id := range d.Deletes {
		if id = f.prefix + id; mine[id] {
			delete(mine, id)
			out.Deletes = append(out.Deletes, id)
		}
	}
	if d.Status != nil {
		v.cov[f.i] = coverageOf(v.cov[f.i].Source, *d.Status)
		if st := aggregateStatus(v.cov); !st.Equal(v.last) {
			v.last = st
			c := st.Clone()
			out.Status = &c
		}
	}
	if len(out.Upserts) > 0 || len(out.Deletes) > 0 || out.Status != nil {
		v.sink.Apply(out)
	}
}

func coverageOf(source string, st provider.ViewStatus) provider.SourceCoverage {
	c := provider.SourceCoverage{Source: source, Class: st.Class, Message: st.Message}
	switch st.State {
	case provider.StatusLoading:
		c.State = provider.CoverageLoading
	case provider.StatusReady:
		c.State = provider.CoverageReady
	case provider.StatusStale:
		c.State = provider.CoverageStale
	default:
		c.State = provider.CoverageError
		if st.Class == provider.ClassForbidden || st.Class == provider.ClassUnauthorized {
			c.State = provider.CoverageDenied
		}
	}
	return c
}

// aggregateStatus: loading while any source loads; ready once every
// source settled and one is ready (the coverage says what is missing or
// stale); stale when none is ready but some are stale (rows are the last
// known); error when none can be observed.
func aggregateStatus(cov []provider.SourceCoverage) provider.ViewStatus {
	n := map[provider.CoverageState]int{}
	for _, c := range cov {
		n[c.State]++
	}
	st := provider.ViewStatus{Coverage: append([]provider.SourceCoverage(nil), cov...)}
	switch {
	case n[provider.CoverageLoading] > 0:
		st.State = provider.StatusLoading
	case n[provider.CoverageReady] > 0:
		st.State = provider.StatusReady
	case n[provider.CoverageStale] > 0:
		st.State = provider.StatusStale
	default:
		st.State = provider.StatusError
		st.Class, st.Message = provider.ClassForbidden, "no source of problems could be observed"
		for _, c := range cov {
			if c.State == provider.CoverageError {
				st.Class, st.Message = c.Class, c.Message
				break
			}
		}
	}
	return st
}

// watchProblems opens the Problems view of scope: every source is an
// ordinary watch; a failure halfway releases the ones started.
func (s *session) watchProblems(q provider.Query, sink provider.Sink) (func(), error) {
	if q.Name != "" || q.Subject != nil {
		return nil, &provider.Error{Class: provider.ClassUnsupported, Message: "problems cannot be narrowed to one object"}
	}
	srcs := s.problemSources
	v := &problemsView{sink: sink, cov: make([]provider.SourceCoverage, len(srcs)), emitted: make([]map[string]bool, len(srcs))}
	for i, src := range srcs {
		title := src.title
		if !src.def.namespaced && q.Scope.Mode == core.ScopeOne {
			title += " (cluster-wide)"
		}
		v.cov[i] = provider.SourceCoverage{Source: title, State: provider.CoverageLoading}
		v.emitted[i] = map[string]bool{}
	}
	v.last = aggregateStatus(v.cov)
	st := v.last.Clone()
	sink.Apply(provider.Delta{Status: &st})

	stops := make([]func(), 0, len(srcs))
	stopAll := func() {
		v.mu.Lock()
		v.stopped = true
		v.mu.Unlock()
		for i := len(stops) - 1; i >= 0; i-- {
			stops[i]()
		}
	}
	for i, src := range srcs {
		stop, err := s.watchDef(src.def, provider.Query{Kind: src.def.desc.ID, Scope: q.Scope}, src.selector,
			&problemsFeed{v: v, i: i, prefix: src.def.desc.ID + "#"})
		if err != nil {
			stopAll()
			return nil, err
		}
		stops = append(stops, stop)
	}
	return stopAll, nil
}
