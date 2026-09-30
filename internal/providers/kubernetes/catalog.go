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

// Catalog states (core.KindCatalog.State).
const (
	catalogDiscovering = core.CatalogDiscovering
	catalogReady       = core.CatalogReady
	catalogPartial     = core.CatalogPartial // some groups unconfirmed
	catalogFailed      = core.CatalogFailed  // no discovery answer yet
)

// discoveredGroup is the navigation group of discovered kinds (neutral:
// discovery does not say whether a resource is a CRD or an aggregated API).
const discoveredGroup = "API groups"

type catalogSnap struct {
	reg         *kindRegistry
	rev         uint64
	state       string
	unconfirmed []string // groups ("core" for the core group)
	// removed: kinds this session offered that are not served any more
	// (a successful discovery without them).
	removed map[string]bool
}

type catalog struct {
	static *kindRegistry
	cur    atomic.Pointer[catalogSnap]
	get    getter // nil: no discovery (sessions without a server)
	ctx    context.Context
	wg     sync.WaitGroup

	mu       sync.Mutex
	known    map[string]bool // every discovered kind ever offered
	last     discovered
	have     bool // a discovery answered at least once
	running  bool
	again    bool
	onChange func(rev uint64)
}

func newCatalog(ctx context.Context, static *kindRegistry, get getter) *catalog {
	c := &catalog{static: static, get: get, ctx: ctx, known: map[string]bool{}}
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

// refresh runs a discovery in the background; one at a time. A refresh
// asked meanwhile supersedes the running discovery: its answer may have
// been read before the change the refresh is about, so it only adds kinds
// (never removes one or moves one to another version — that waits for an
// answer not superseded); the discovery runs again and the latest answer
// wins. A stream of refreshes thus still shows new kinds without ever
// certifying an absence it did not confirm.
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

// run discovers until no refresh is pending. It stays the only runner
// while it tells a revision, so revisions are told one at a time, in
// order (a listener asking for a refresh only marks another round).
func (c *catalog) run() {
	defer c.wg.Done()
	for {
		c.mu.Lock()
		c.again = false
		c.mu.Unlock()
		d := discoverAPI(c.ctx, c.get)
		if c.ctx.Err() != nil {
			c.mu.Lock()
			c.running = false
			c.mu.Unlock()
			return
		}
		c.mu.Lock()
		var rev uint64
		if c.again && c.have {
			if added := additions(c.last, d); len(added) > 0 {
				c.last.resources = sortedResources(append(append([]apiResource{}, c.last.resources...), added...))
				rev = c.publish(discovered{unconfirmed: c.last.unconfirmed}, true)
			}
		} else {
			if c.have {
				d = merge(c.last, d)
			}
			answered := len(d.resources) > 0 || !d.unconfirmed[""] || !d.unconfirmed["*"]
			if answered {
				c.last, c.have = d, true
			}
			rev = c.publish(d, answered)
		}
		onChange := c.onChange
		c.mu.Unlock()
		// Told outside the lock (the listener may ask for a refresh).
		if rev != 0 && onChange != nil {
			onChange(rev)
		}
		c.mu.Lock()
		if !c.again {
			c.running = false
			c.mu.Unlock()
			return
		}
		c.mu.Unlock()
	}
}

// additions: resources of cur whose group+resource last does not have.
func additions(last, cur discovered) []apiResource {
	have := map[schema.GroupResource]bool{}
	for _, r := range last.resources {
		have[schema.GroupResource{Group: r.Group, Resource: r.Resource}] = true
	}
	var out []apiResource
	for _, r := range cur.resources {
		if !have[schema.GroupResource{Group: r.Group, Resource: r.Resource}] {
			out = append(out, r)
		}
	}
	return out
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
	reg := newKindRegistry(navOrder(c.static.list, defs)...)
	removed := map[string]bool{}
	for _, d := range defs {
		c.known[d.desc.ID] = true
	}
	for id := range c.known {
		if reg.byID[id] == nil {
			removed[id] = true
		}
	}
	if old.state == state && strings.Join(old.unconfirmed, ",") == strings.Join(unconfirmed, ",") && sameKinds(old.reg, reg) {
		return 0
	}
	n := &catalogSnap{reg: reg, rev: old.rev + 1, state: state, unconfirmed: unconfirmed, removed: removed}
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

// placedKinds: well-known built-ins without a described projection, shown
// in the section of the navigation a user looks for them in (as Lens does),
// in this order after the section's described kinds; new sections follow
// the described ones in the order they first appear here. Everything else
// served (custom resources, rare built-ins) stays under API groups.
var placedKinds = []struct {
	gr      schema.GroupResource
	section string
}{
	{schema.GroupResource{Group: "batch", Resource: "jobs"}, "Workloads"},
	{schema.GroupResource{Group: "batch", Resource: "cronjobs"}, "Workloads"},
	{schema.GroupResource{Resource: "replicationcontrollers"}, "Workloads"},
	{schema.GroupResource{Resource: "endpoints"}, "Network"},
	{schema.GroupResource{Group: "discovery.k8s.io", Resource: "endpointslices"}, "Network"},
	{schema.GroupResource{Group: "networking.k8s.io", Resource: "networkpolicies"}, "Network"},
	{schema.GroupResource{Group: "networking.k8s.io", Resource: "ingressclasses"}, "Network"},
	{schema.GroupResource{Group: "autoscaling", Resource: "horizontalpodautoscalers"}, "Config"},
	{schema.GroupResource{Resource: "resourcequotas"}, "Config"},
	{schema.GroupResource{Resource: "limitranges"}, "Config"},
	{schema.GroupResource{Group: "policy", Resource: "poddisruptionbudgets"}, "Config"},
	{schema.GroupResource{Group: "scheduling.k8s.io", Resource: "priorityclasses"}, "Config"},
	{schema.GroupResource{Group: "node.k8s.io", Resource: "runtimeclasses"}, "Config"},
	{schema.GroupResource{Group: "coordination.k8s.io", Resource: "leases"}, "Config"},
	{schema.GroupResource{Group: "admissionregistration.k8s.io", Resource: "mutatingwebhookconfigurations"}, "Config"},
	{schema.GroupResource{Group: "admissionregistration.k8s.io", Resource: "validatingwebhookconfigurations"}, "Config"},
	{schema.GroupResource{Group: "apiextensions.k8s.io", Resource: "customresourcedefinitions"}, "Cluster"},
	{schema.GroupResource{Resource: "persistentvolumeclaims"}, "Storage"},
	{schema.GroupResource{Resource: "persistentvolumes"}, "Storage"},
	{schema.GroupResource{Group: "storage.k8s.io", Resource: "storageclasses"}, "Storage"},
	{schema.GroupResource{Resource: "serviceaccounts"}, "Access Control"},
	{schema.GroupResource{Group: "rbac.authorization.k8s.io", Resource: "roles"}, "Access Control"},
	{schema.GroupResource{Group: "rbac.authorization.k8s.io", Resource: "rolebindings"}, "Access Control"},
	{schema.GroupResource{Group: "rbac.authorization.k8s.io", Resource: "clusterroles"}, "Access Control"},
	{schema.GroupResource{Group: "rbac.authorization.k8s.io", Resource: "clusterrolebindings"}, "Access Control"},
}

// navOrder lays the kinds out for the navigation: the described ones, each
// section's placed kinds right after its described ones, then the new
// sections (placedKinds order), then API groups in discovery order.
func navOrder(static, defs []*kindDef) []*kindDef {
	placed := map[string][]*kindDef{}
	var rest []*kindDef
	for _, d := range defs {
		if d.place > 0 {
			placed[d.desc.Group] = append(placed[d.desc.Group], d)
		} else {
			rest = append(rest, d)
		}
	}
	for _, l := range placed {
		sort.SliceStable(l, func(i, j int) bool { return l[i].place < l[j].place })
	}
	last := map[string]int{}
	for i, d := range static {
		last[d.desc.Group] = i
	}
	out := make([]*kindDef, 0, len(static)+len(defs))
	for i, d := range static {
		out = append(out, d)
		if last[d.desc.Group] == i {
			out = append(out, placed[d.desc.Group]...)
			delete(placed, d.desc.Group)
		}
	}
	for _, pk := range placedKinds {
		out = append(out, placed[pk.section]...)
		delete(placed, pk.section)
	}
	return append(out, rest...)
}

// sensitiveResources: editing them grants access beyond themselves
// (core.KindDescriptor.Sensitive).
var sensitiveResources = map[schema.GroupResource]bool{
	{Resource: "secrets"}:                                                 true,
	{Resource: "serviceaccounts"}:                                         true,
	{Group: "rbac.authorization.k8s.io", Resource: "roles"}:               true,
	{Group: "rbac.authorization.k8s.io", Resource: "rolebindings"}:        true,
	{Group: "rbac.authorization.k8s.io", Resource: "clusterroles"}:        true,
	{Group: "rbac.authorization.k8s.io", Resource: "clusterrolebindings"}: true,
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
	group := discoveredGroup
	place := 0
	for i, pk := range placedKinds {
		if pk.gr == (schema.GroupResource{Group: r.Group, Resource: r.Resource}) {
			group, sub, place = pk.section, "", i+1
			break
		}
	}
	cols := []core.Column{colName}
	if r.Namespaced {
		cols = append(cols, colNS)
	}
	cols = append(cols, colAge)
	d := &kindDef{
		desc: core.KindDescriptor{
			ID: id, Title: pluralTitle(r.Kind, r.Resource), Singular: singular, Group: group, Subgroup: sub,
			Columns: cols, Scoped: r.Namespaced, Aliases: aliases,
			Sensitive: sensitiveResources[schema.GroupResource{Group: r.Group, Resource: r.Resource}],
		},
		place:      place,
		gvr:        schema.GroupVersionResource{Group: r.Group, Version: r.Version, Resource: r.Resource},
		namespaced: r.Namespaced,
		kind:       r.Kind,
		verbs:      append([]string{}, r.Verbs...),
		discovered: true,
		project:    projectGeneric(r.Namespaced),
	}
	d.actions = discoveredActions(r)
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
