package kubernetes

import (
	"sync"
	"time"

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
	watchers map[*viewWatch]struct{}
}

func (c *informerCache) setTransport(err error) {
	class, msg := classify(err)
	if class == provider.ClassGone {
		return // expired resourceVersion: the reflector relists, not a failure
	}
	tr := transport{failing: err != nil, class: class, message: msg}
	c.mu.Lock()
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
	now       func() time.Time
	grace     time.Duration
	maxIdle   int

	mu     sync.Mutex
	caches map[cacheKey]*informerCache
	timer  *time.Timer
	closed bool
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
	lw := &statusListWatch{res: ri, selector: key.selector, report: c.setTransport, watchList: m.watchList}
	c.inf = cache.NewSharedIndexInformerWithOptions(lw, &unstructured.Unstructured{}, cache.SharedIndexInformerOptions{
		ObjectDescription: key.gvr.String(),
	})
	keep := def.keep
	_ = c.inf.SetTransform(func(obj any) (any, error) {
		if u, ok := obj.(*unstructured.Unstructured); ok {
			return trim(u, keep), nil
		}
		return obj, nil // DeletedFinalStateUnknown etc.: already transformed inside
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
