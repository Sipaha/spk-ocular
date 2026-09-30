package kubernetes

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	"github.com/spk/spk-ocular/internal/provider"
)

// Cache retention (docs/specs "Ленивые informers с бюджетом"): a cache no
// view uses is kept for cacheGrace (switching back is instant), but at most
// maxIdleCaches such caches exist; the oldest idle one goes first.
const (
	cacheGrace    = 60 * time.Second
	maxIdleCaches = 8
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
	key  cacheKey
	def  *kindDef
	inf  cache.SharedIndexInformer
	stop chan struct{}

	mu       sync.Mutex
	leases   int
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

func (c *informerCache) setTransport(err error) {
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
	now        func() time.Time
	grace      time.Duration
	maxIdle    int

	mu     sync.Mutex
	caches map[cacheKey]*informerCache
	timer  *time.Timer
	closed bool

	counts requestCounts
}

// requestCounts count the informers' requests (stats for soaks: a relist
// shows as another list or initial sync, a reconnect as a watch start).
type requestCounts struct {
	lists, watchStarts, initialSyncs atomic.Int64
}

func newCacheManager(dyn dynamic.Interface, watchList bool) *cacheManager {
	return &cacheManager{dyn: dyn, watchList: watchList, now: time.Now, grace: cacheGrace, maxIdle: maxIdleCaches, caches: map[cacheKey]*informerCache{}}
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
	var lw cache.ListerWatcher = &statusListWatch{res: ri, selector: key.selector, report: c.setTransport, ended: c.streamEnded, watchList: m.watchList, refused: &m.noWatchList, counts: &m.counts}
	if sch := key.sch; sch != nil && sch.table && m.tables != nil {
		lw = &tableListWatch{c: m.tables, gvr: key.gvr, namespace: key.namespace, selector: key.selector, report: c.setTransport, ended: c.streamEnded, counts: &m.counts,
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
	keep, pre := def.keep, def.pre
	_ = c.inf.SetTransform(func(obj any) (any, error) {
		if u, ok := obj.(*unstructured.Unstructured); ok {
			if pre != nil {
				pre(u)
			}
			return slim(trim(u, keep))
		}
		return obj, nil // already slim, or DeletedFinalStateUnknown of a slim object
	})
	go c.inf.Run(c.stop)
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
	c.mu.Unlock()
	m.evictLocked()
}

// evictLocked stops caches idle past grace and the oldest beyond maxIdle,
// then arms a timer for the next grace expiry.
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
	var soonest time.Time
	for i, c := range idle {
		if now.Sub(c.idleAt) >= m.grace || len(idle)-i > m.maxIdle {
			delete(m.caches, c.key)
			close(c.stop)
			continue
		}
		soonest = earliest(soonest, c.idleAt.Add(m.grace))
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
	for _, c := range m.caches {
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
			close(c.stop)
		}
	}
}

// routeOf: the namespace and selector of a leased cache of the epoch (the
// scope a re-probe may use); false if no view uses the epoch.
func (m *cacheManager) routeOf(sch *tableSchema) (ns, selector string, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, c := range m.caches {
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
	for _, c := range m.caches {
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
	for _, c := range m.caches {
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
		close(c.stop)
		delete(m.caches, k)
	}
}
