package kubernetes

import (
	"context"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/spk/spk-ocular/internal/core"
)

// The catalog: the kinds a session offers — the described ones at once,
// the other served resources after discovery, which runs in the background
// (never under the API's locks: Kinds reads a snapshot). A failed or stale
// group keeps what it had (merge): only a successful answer without a
// resource removes its kind.

// Catalog states.
const (
	catalogDiscovering = "discovering"
	catalogReady       = "ready"
	catalogPartial     = "partial" // some groups unconfirmed
	catalogFailed      = "failed"  // no discovery answer yet
)

// discoveredGroup is the navigation group of discovered kinds (neutral:
// discovery does not say whether a resource is a CRD or an aggregated API).
const discoveredGroup = "API groups"

type catalogSnap struct {
	reg         *kindRegistry
	rev         uint64
	state       string
	unconfirmed []string // groups ("core" for the core group)
}

type catalog struct {
	static *kindRegistry
	cur    atomic.Pointer[catalogSnap]
	get    getter // nil: no discovery (sessions without a server)
	ctx    context.Context
	wg     sync.WaitGroup

	mu       sync.Mutex
	last     discovered
	have     bool // a discovery answered at least once
	running  bool
	again    bool
	onChange func(rev uint64)
}

func newCatalog(ctx context.Context, static *kindRegistry, get getter) *catalog {
	c := &catalog{static: static, get: get, ctx: ctx}
	state := catalogReady
	if get != nil {
		state = catalogDiscovering
	}
	c.cur.Store(&catalogSnap{reg: static, rev: 1, state: state})
	return c
}

func (c *catalog) snap() *catalogSnap { return c.cur.Load() }

// setOnChange sets what is told of a new revision (the API's hub).
func (c *catalog) setOnChange(f func(rev uint64)) {
	c.mu.Lock()
	c.onChange = f
	c.mu.Unlock()
}

// refresh runs a discovery in the background; one at a time — a refresh
// asked meanwhile runs once more after it (the latest answer wins).
func (c *catalog) refresh() {
	if c.get == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running {
		c.again = true
		return
	}
	if c.ctx.Err() != nil {
		return
	}
	c.running = true
	c.wg.Add(1)
	go c.run()
}

func (c *catalog) run() {
	defer c.wg.Done()
	for {
		d := discoverAPI(c.ctx, c.get)
		if c.ctx.Err() != nil {
			c.mu.Lock()
			c.running = false
			c.mu.Unlock()
			return
		}
		c.mu.Lock()
		if c.have {
			d = merge(c.last, d)
		}
		answered := len(d.resources) > 0 || !d.unconfirmed[""] || !d.unconfirmed["*"]
		if answered {
			c.last, c.have = d, true
		}
		rev := c.publish(d, answered)
		onChange, again := c.onChange, c.again
		c.again = false
		if !again {
			c.running = false
		}
		c.mu.Unlock()
		// Told outside the lock (the listener may ask for a refresh). Runs
		// are one at a time, so revisions are told in order.
		if rev != 0 && onChange != nil {
			onChange(rev)
		}
		if !again {
			return
		}
	}
}

// publish stores a new snapshot when the kinds or the state changed and
// returns its revision (0: no change). Called with mu held.
func (c *catalog) publish(d discovered, answered bool) uint64 {
	old := c.snap()
	defs := discoveredDefs(c.static, c.last.resources)
	state := catalogReady
	var unconfirmed []string
	for g := range d.unconfirmed {
		switch g {
		case "":
			g = "core"
		case "*":
			g = "*"
		}
		unconfirmed = append(unconfirmed, g)
	}
	sort.Strings(unconfirmed)
	switch {
	case !c.have:
		state, defs = catalogFailed, nil
	case !answered || len(unconfirmed) > 0:
		state = catalogPartial
	}
	reg := newKindRegistry(append(append([]*kindDef{}, c.static.list...), defs...)...)
	if old.state == state && strings.Join(old.unconfirmed, ",") == strings.Join(unconfirmed, ",") && sameKinds(old.reg, reg) {
		return 0
	}
	n := &catalogSnap{reg: reg, rev: old.rev + 1, state: state, unconfirmed: unconfirmed}
	c.cur.Store(n)
	return n.rev
}

// wait ends with the running discovery (Close).
func (c *catalog) wait() { c.wg.Wait() }

// sameKinds: the same kinds with the same routes.
func sameKinds(a, b *kindRegistry) bool {
	if len(a.list) != len(b.list) {
		return false
	}
	for i, d := range a.list {
		e := b.list[i]
		if d != e && (d.desc.ID != e.desc.ID || d.gvr != e.gvr || d.namespaced != e.namespaced || !sameStrings(d.verbs, e.verbs) ||
			d.desc.Title != e.desc.Title || d.desc.Singular != e.desc.Singular || !sameStrings(d.desc.Aliases, e.desc.Aliases)) {
			return false
		}
	}
	return true
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// discoveredDefs: a kind for each listable, watchable resource the
// described kinds do not cover.
func discoveredDefs(static *kindRegistry, rs []apiResource) []*kindDef {
	covered := map[schema.GroupResource]bool{
		{Group: "events.k8s.io", Resource: "events"}: true, // the events kind shows core/v1 events
	}
	// One alias names one kind: the described kinds' names first, then the
	// discovered ones in discovery order.
	taken := map[string]bool{}
	for _, d := range static.list {
		if !d.virtual {
			covered[d.gvr.GroupResource()] = true
		}
		taken[d.desc.ID] = true
		for _, a := range kindAliases[d] {
			taken[a] = true
		}
	}
	var out []*kindDef
	for _, r := range rs {
		gr := schema.GroupResource{Group: r.Group, Resource: r.Resource}
		if covered[gr] || !r.has("list") || !r.has("watch") {
			continue
		}
		covered[gr] = true
		d := discoveredDef(r)
		free := d.desc.Aliases[:0]
		for _, a := range d.desc.Aliases {
			if !taken[a] {
				taken[a] = true
				free = append(free, a)
			}
		}
		d.desc.Aliases = free
		out = append(out, d)
	}
	return out
}

// discoveredDef describes a served resource generically: name, scope, age
// (the table's columns come with its first answer — Table, P8 Task 2).
func discoveredDef(r apiResource) *kindDef {
	id := r.Resource
	if r.Group != "" {
		id = r.Group + "/" + r.Resource
	}
	sub := r.Group
	if sub == "" {
		sub = "core"
	}
	singular := r.Kind
	if singular == "" {
		singular = r.Singular
	}
	aliases := append([]string{}, r.ShortNames...)
	if r.Singular != "" {
		aliases = append(aliases, r.Singular)
	}
	aliases = append(aliases, r.Resource)
	cols := []core.Column{colName}
	if r.Namespaced {
		cols = append(cols, colNS)
	}
	cols = append(cols, colAge)
	d := &kindDef{
		desc: core.KindDescriptor{
			ID: id, Title: pluralTitle(r.Kind, r.Resource), Singular: singular, Group: discoveredGroup, Subgroup: sub,
			Columns: cols, Scoped: r.Namespaced, Aliases: aliases,
		},
		gvr:        schema.GroupVersionResource{Group: r.Group, Version: r.Version, Resource: r.Resource},
		namespaced: r.Namespaced,
		kind:       r.Kind,
		verbs:      append([]string{}, r.Verbs...),
		discovered: true,
		project:    projectGeneric(r.Namespaced),
	}
	if r.has("delete") {
		d.actions = []core.ActionDescriptor{actDelete}
	}
	return d
}

// pluralTitle: the Kind in the plural as the resource spells it
// ("CertificateRequest" + "certificaterequests" → "CertificateRequests").
func pluralTitle(kind, resource string) string {
	lk := strings.ToLower(kind)
	switch {
	case kind == "":
		return resource
	case lk+"s" == resource:
		return kind + "s"
	case lk+"es" == resource:
		return kind + "es"
	case strings.HasSuffix(lk, "y") && strings.TrimSuffix(lk, "y")+"ies" == resource:
		return kind[:len(kind)-1] + "ies"
	case lk == resource:
		return kind
	}
	return resource
}

func projectGeneric(namespaced bool) func(u *unstructured.Unstructured, now time.Time) ([]core.Cell, core.Health, time.Time) {
	return func(u *unstructured.Unstructured, _ time.Time) ([]core.Cell, core.Health, time.Time) {
		cells := []core.Cell{core.TextCell(u.GetName())}
		if namespaced {
			cells = append(cells, core.TextCell(u.GetNamespace()))
		}
		cells = append(cells, createdCell(u))
		h := core.Health{State: core.HealthUnknown}
		if u.GetDeletionTimestamp() != nil {
			h = core.HealthFrom([]core.Issue{{State: core.HealthTerminating, Reason: "Terminating"}})
		}
		return cells, h, time.Time{}
	}
}
