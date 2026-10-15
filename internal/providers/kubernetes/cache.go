package kubernetes

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/spk/spk-ocular/internal/provider"
)

// Cache retention (docs/architecture.md): a cache no
// view uses is kept for cacheGrace (switching back is instant), but at most
// maxIdleCaches such caches exist, subject to object and encoded-payload
// budgets; the oldest idle one goes first.
const (
	cacheGrace          = 60 * time.Second
	maxIdleCaches       = 8
	maxIdleObjects      = 25000
	maxIdlePayloadBytes = 32 << 20
	cacheBudgetSweep    = 5 * time.Second
)

type cacheKey struct {
	gvr       schema.GroupVersionResource
	namespace string // "" = all namespaces / cluster-scoped
	selector  string // field selector
	// sch: a discovered kind's schema epoch (its rows are that epoch's).
	sch *tableSchema
}

// transport is a cache's connection state, shared by its views.
type transport struct {
	failing bool
	class   provider.ErrorClass
	message string
}

// informerCache is one informer plus the views (watchers) that lease it.
type informerCache struct {
	key      cacheKey
	def      *kindDef
	inf      cache.SharedIndexInformer
	stop     chan struct{}
	stopOnce sync.Once
	guard    *scopeBudget
	progress loadTransfer

	mu       sync.Mutex
	leases   int
	observed bool
	idleAt   time.Time
	tr       transport
	okGen    uint64 // bumped by every successful request
	watchers map[*viewWatch]struct{}
}

// reconnectGrace: a watch stream that ended is fine if a new request
// succeeds within this; otherwise the views go stale ("reconnecting").
var reconnectGrace = 5 * time.Second

var errReconnecting = errors.New("the connection to the cluster was lost; reconnecting")

// streamEnded is called when a watch stream closes (normal renewal or a
// dropped connection). Renewals re-watch at once and bump okGen.
func (c *informerCache) streamEnded() {
	c.mu.Lock()
	gen := c.okGen
	c.mu.Unlock()
	time.AfterFunc(reconnectGrace, func() {
		c.mu.Lock()
		stuck := c.okGen == gen
		c.mu.Unlock()
		if stuck {
			c.setTransport(errReconnecting)
		}
	})
}

func (c *informerCache) close() { c.stopOnce.Do(func() { close(c.stop) }) }

func (c *informerCache) setTransport(err error) {
	if c.guard != nil {
		if blocked := c.guard.failure(); blocked != nil {
			err = blocked
		}
	}
	class, msg := classify(err)
	if class == provider.ClassGone {
		return // expired resourceVersion: the reflector relists, not a failure
	}
	tr := transport{failing: err != nil, class: class, message: msg}
	c.mu.Lock()
	if err == nil {
		c.okGen++
	}
	if c.tr == tr {
		c.mu.Unlock()
		return
	}
	c.tr = tr
	ws := make([]*viewWatch, 0, len(c.watchers))
	for w := range c.watchers {
		ws = append(ws, w)
	}
	c.mu.Unlock()
	if class == provider.ClassScopeLimit {
		c.close()
	}
	for _, w := range ws {
		w.pushStatus()
	}
}

func (c *informerCache) transport() transport {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tr
}

// cacheManager owns a session's informers. Informers are never restarted
// (a stopped SharedInformer cannot be): eviction stops and forgets one;
// the next lease creates a fresh one.
type cacheManager struct {
	dyn       dynamic.Interface
	watchList bool
	// noWatchList: the server refused WatchList (sendInitialEvents) once;
	// no informer of the session asks again.
	noWatchList atomic.Bool
	// tables reads discovered kinds as server-side Tables (nil: the plain
	// format through dyn); onSchema is told of an answer of another schema.
	tables   *tableClient
	onSchema func(old *tableSchema)
	// onNotTable is told the resource showed it does not serve Tables.
	onNotTable func(old *tableSchema)
	// onFailed is told the class of every failed list or watch (the
	// session's background: an unauthorized one is lost).
	onFailed       func(provider.ErrorClass)
	now            func() time.Time
	grace          time.Duration
	maxIdle        int
	maxIdleObjects int
	maxIdleBytes   int64
	budgetSweep    time.Duration
	// background: idle caches are kept past grace; all idle retention
	// budgets still apply. Set by the session (P18).
	background bool

	mu      sync.Mutex
	caches  map[cacheKey]*informerCache
	retired map[*informerCache]struct{}
	timer   *time.Timer
	closed  bool

	counts requestCounts
}

// requestCounts count the informers' requests (stats for soaks: a relist
// shows as another list or initial sync, a reconnect as a watch start).
type requestCounts struct {
	lists, watchStarts, initialSyncs atomic.Int64
}

func newCacheManager(dyn dynamic.Interface, watchList bool) *cacheManager {
	return &cacheManager{dyn: dyn, watchList: watchList, now: time.Now, grace: cacheGrace, maxIdle: maxIdleCaches, maxIdleObjects: maxIdleObjects, maxIdleBytes: maxIdlePayloadBytes, budgetSweep: cacheBudgetSweep, caches: map[cacheKey]*informerCache{}, retired: map[*informerCache]struct{}{}}
}

// acquire leases the cache for key, creating and starting it if needed.
func (m *cacheManager) acquire(key cacheKey, def *kindDef) (*informerCache, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, false
	}
	c := m.caches[key]
	if c == nil {
		c = m.start(key, def)
		m.caches[key] = c
	}
	c.mu.Lock()
	c.leases++
	c.idleAt = time.Time{}
	c.mu.Unlock()
	return c, true
}

func (m *cacheManager) start(key cacheKey, def *kindDef) *informerCache {
	c := &informerCache{key: key, def: def, stop: make(chan struct{}), watchers: map[*viewWatch]struct{}{}}
	res := m.dyn.Resource(key.gvr)
	var ri dynamic.ResourceInterface = res
	if key.namespace != "" {
		ri = res.Namespace(key.namespace)
	}
	report := func(err error) {
		c.setTransport(err)
		if err != nil && m.onFailed != nil {
			class, _ := classify(err)
			m.onFailed(class)
		}
	}
	if def.namespaced && key.namespace == "" {
		c.guard = newScopeBudget()
	}
	pre := func(u *unstructured.Unstructured) {
		if def.pre != nil {
			def.pre(u)
		}
		*u = *trim(u, def.keep)
	}
	compact := func(u *unstructured.Unstructured) (runtime.Object, error) {
		if def.pre != nil {
			def.pre(u)
		}
		return slim(trim(u, def.keep))
	}
	var lw cache.ListerWatcher = &statusListWatch{progress: &c.progress, guard: c.guard, pre: pre, compact: compact, res: ri, selector: key.selector, report: report, ended: c.streamEnded, watchList: m.watchList, refused: &m.noWatchList, counts: &m.counts}
	if sch := key.sch; sch != nil && sch.table && m.tables != nil {
		lw = &tableListWatch{progress: &c.progress, guard: c.guard, pre: pre, compact: compact, c: m.tables, gvr: key.gvr, namespace: key.namespace, selector: key.selector, report: report, ended: c.streamEnded, counts: &m.counts,
			check: func(cols []metav1.TableColumnDefinition) error {
				if sch.retired.Load() || !sch.matches(cols) {
					if m.onSchema != nil {
						m.onSchema(sch)
					}
					return errSchemaChanged
				}
				return nil
			},
			notTable: func() {
				if m.onNotTable != nil {
					m.onNotTable(sch)
				}
			}}
	}
	c.inf = cache.NewSharedIndexInformerWithOptions(lw, &unstructured.Unstructured{}, cache.SharedIndexInformerOptions{
		ObjectDescription: key.gvr.String(),
	})
	_ = c.inf.SetTransform(func(obj any) (any, error) {
		if u, ok := obj.(*unstructured.Unstructured); ok {
			return compact(u)
		}
		return obj, nil // LIST pages are already compact; watches use the same projection.
	})
	go c.inf.Run(c.stop)
	go func() {
		if cache.WaitForCacheSync(c.stop, c.inf.HasSynced) {
			c.progress.finished.Store(true)
		}
	}()
	return c
}

// release ends one lease; an unleased cache becomes idle and is evicted
// after the grace period or when too many are idle.
func (m *cacheManager) release(c *informerCache) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c.mu.Lock()
	c.leases--
	if c.leases == 0 {
		c.idleAt = m.now()
	}
	remaining := c.leases
	c.mu.Unlock()
	if _, detached := m.retired[c]; detached && remaining == 0 {
		delete(m.retired, c)
		c.close()
	}
	if remaining == 0 && !c.inf.HasSynced() && c.transport().class != provider.ClassScopeLimit && m.caches[c.key] == c {
		delete(m.caches, c.key)
		c.close() // no completed snapshot to keep warm
	}
	m.evictLocked()
}

// evictLocked stops caches idle past grace and the oldest beyond count,
// object or encoded-payload budgets, then arms the next local sweep.
func (m *cacheManager) evictLocked() {
	now := m.now()
	var idle []*informerCache
	for _, c := range m.caches {
		c.mu.Lock()
		if c.leases == 0 {
			idle = append(idle, c)
		}
		c.mu.Unlock()
	}
	// oldest first
	for i := 1; i < len(idle); i++ {
		for j := i; j > 0 && idle[j].idleAt.Before(idle[j-1].idleAt); j-- {
			idle[j], idle[j-1] = idle[j-1], idle[j]
		}
	}
	costs := make(map[*informerCache]cacheCost, len(idle))
	var objects int
	var payload int64
	for _, c := range idle {
		cost := idleCacheCost(c)
		costs[c] = cost
		objects += cost.objects
		payload += cost.payload
	}
	overBudget := func() bool {
		return (m.maxIdleObjects > 0 && objects > m.maxIdleObjects) || (m.maxIdleBytes > 0 && payload > m.maxIdleBytes)
	}
	var soonest time.Time
	var retained int
	for i, c := range idle {
		expired := !m.background && now.Sub(c.idleAt) >= m.grace
		if expired || len(idle)-i > m.maxIdle || overBudget() {
			objects -= costs[c].objects
			payload -= costs[c].payload
			delete(m.caches, c.key)
			c.close()
			continue
		}
		retained++
		if !m.background {
			soonest = earliest(soonest, c.idleAt.Add(m.grace))
		}
	}
	// Idle watches can grow after release, including in background sessions.
	// Periodically enforce local retention without issuing any API request.
	if retained > 0 && m.budgetSweep > 0 && (m.maxIdleObjects > 0 || m.maxIdleBytes > 0) {
		soonest = earliest(soonest, now.Add(m.budgetSweep))
	}
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	if !soonest.IsZero() && !m.closed {
		m.timer = time.AfterFunc(soonest.Sub(now), func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.evictLocked()
		})
	}
}

// setBackground keeps idle caches past grace while on; off, the grace of
// each idle cache counts from now (the return).
func (m *cacheManager) setBackground(on bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.background == on {
		return
	}
	m.background = on
	if !on {
		now := m.now()
		for _, c := range m.caches {
			c.mu.Lock()
			if c.leases == 0 {
				c.idleAt = now
			}
			c.mu.Unlock()
		}
	}
	m.evictLocked()
}

// lookup finds an object by name in any cache of gvr that covers ns (a
// namespace cache or an all-namespaces one).
func (m *cacheManager) lookup(gvr schema.GroupVersionResource, ns, name string) (metav1.Object, bool) {
	m.mu.Lock()
	var cs []*informerCache
	for k, c := range m.caches {
		if k.gvr == gvr && k.selector == "" && (k.namespace == "" || k.namespace == ns) {
			cs = append(cs, c)
		}
	}
	m.mu.Unlock()
	key := name
	if ns != "" {
		key = ns + "/" + name
	}
	for _, c := range cs {
		if obj, ok, _ := c.inf.GetStore().GetByKey(key); ok {
			if o, ok := obj.(metav1.Object); ok {
				return o, true
			}
		}
	}
	return nil, false
}

// stats reports cache counts for leak checks.
func (m *cacheManager) stats() (active, idle int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.allCachesLocked() {
		c.mu.Lock()
		if c.leases > 0 {
			active++
		} else {
			idle++
		}
		c.mu.Unlock()
	}
	return active, idle
}

// retire stops the caches of an ended schema epoch at once (their views
// ended first): no request of the old epoch goes on.
func (m *cacheManager) retire(sch *tableSchema) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, c := range m.caches {
		if k.sch == sch {
			delete(m.caches, k)
			c.close()
		}
	}
	for c := range m.retired {
		if c.key.sch == sch {
			delete(m.retired, c)
			c.close()
		}
	}
}

// routeOf: the namespace and selector of a leased cache of the epoch (the
// scope a re-probe may use); false if no view uses the epoch.
func (m *cacheManager) routeOf(sch *tableSchema) (ns, selector string, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.allCachesLocked() {
		k := c.key
		c.mu.Lock()
		leased := c.leases > 0
		c.mu.Unlock()
		if k.sch == sch && leased {
			return k.namespace, k.selector, true
		}
	}
	return "", "", false
}

// allWatchers: the views on every cache now.
func (m *cacheManager) allWatchers() []*viewWatch {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*viewWatch
	for _, c := range m.allCachesLocked() {
		c.mu.Lock()
		for w := range c.watchers {
			out = append(out, w)
		}
		c.mu.Unlock()
	}
	return out
}

// watchStats counts the views watching caches and their pending deadlines.
func (m *cacheManager) watchStats() (watchers, deadlines int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.allCachesLocked() {
		c.mu.Lock()
		for w := range c.watchers {
			watchers++
			w.mu.Lock()
			deadlines += len(w.deadlines)
			w.mu.Unlock()
		}
		c.mu.Unlock()
	}
	return watchers, deadlines
}

func (m *cacheManager) closeAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	if m.timer != nil {
		m.timer.Stop()
	}
	for k, c := range m.caches {
		c.close()
		delete(m.caches, k)
	}
	for c := range m.retired {
		c.close()
		delete(m.retired, c)
	}
}

// cacheCost counts retained objects and encoded payload, not Go heap bytes.
// Both limits matter: many tiny objects have overhead and one large CR can
// carry a large body. Active leases are never evicted by an idle budget.
type cacheCost struct {
	objects int
	payload int64
}

func idleCacheCost(c *informerCache) cacheCost {
	objects := c.inf.GetStore().List()
	cost := cacheCost{objects: len(objects)}
	for _, obj := range objects {
		if slim, ok := obj.(*slimObject); ok {
			cost.payload += int64(len(slim.body))
		}
		if meta, ok := obj.(metav1.Object); ok {
			cost.payload += int64(len(meta.GetName()) + len(meta.GetNamespace()) + len(meta.GetUID()) + len(meta.GetResourceVersion()))
			for k, v := range meta.GetLabels() {
				cost.payload += int64(len(k) + len(v))
			}
			for _, owner := range meta.GetOwnerReferences() {
				cost.payload += int64(len(owner.APIVersion) + len(owner.Kind) + len(owner.Name) + len(owner.UID))
			}
		}
	}
	return cost
}

func (m *cacheManager) allCachesLocked() []*informerCache {
	out := make([]*informerCache, 0, len(m.caches)+len(m.retired))
	for _, c := range m.caches {
		out = append(out, c)
	}
	for c := range m.retired {
		out = append(out, c)
	}
	return out
}

// invalidate detaches only matching routes. Existing consumers (for example
// logs) keep their admitted watch; the refreshed view acquires a fresh cache.
// Detached caches stop immediately after their last lease, never become idle.
func (m *cacheManager) invalidate(matches func(cacheKey) bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return &provider.Error{Class: provider.ClassGone, Message: "session closed"}
	}
	for key, c := range m.caches {
		if !matches(key) {
			continue
		}
		delete(m.caches, key)
		c.mu.Lock()
		leased := c.leases > 0
		c.mu.Unlock()
		if leased {
			m.retired[c] = struct{}{}
		} else {
			c.close()
		}
	}
	m.evictLocked()
	return nil
}
