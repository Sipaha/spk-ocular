package kubernetes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/execshim"
	"github.com/spk/spk-ocular/internal/provider"
)

// scopesTimeout bounds the one-shot namespace list.
const scopesTimeout = 15 * time.Second

var namespacesGVR = schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}

// kinds in navigation order.
var allKinds = newKindRegistry(
	problemsKind,
	podsKind, deploymentsKind, statefulSetsKind, daemonSetsKind, replicaSetsKind,
	servicesKind, ingressesKind,
	configMapsKind, secretsKind,
	nodesKind, namespacesKind, eventsKind,
)

// kindAliases: kubectl's short and singular names, for palette commands.
var kindAliases = map[*kindDef][]string{
	podsKind:         {"po", "pod"},
	deploymentsKind:  {"deploy", "deployment"},
	statefulSetsKind: {"sts", "statefulset"},
	daemonSetsKind:   {"ds", "daemonset"},
	replicaSetsKind:  {"rs", "replicaset"},
	servicesKind:     {"svc", "service"},
	ingressesKind:    {"ing", "ingress"},
	configMapsKind:   {"cm", "configmap"},
	secretsKind:      {"secret"},
	nodesKind:        {"no", "node"},
	namespacesKind:   {"ns", "namespace"},
	eventsKind:       {"ev", "event"},
	problemsKind:     {"problem"},
}

// kindSingular: one object of a kind (dialogs, sentences).
var kindSingular = map[*kindDef]string{
	podsKind: "Pod", deploymentsKind: "Deployment", statefulSetsKind: "StatefulSet", daemonSetsKind: "DaemonSet",
	replicaSetsKind: "ReplicaSet", servicesKind: "Service", ingressesKind: "Ingress", configMapsKind: "ConfigMap",
	secretsKind: "Secret", nodesKind: "Node", namespacesKind: "Namespace", eventsKind: "Event", problemsKind: "Problem",
}

var _ provider.Opener = (*Provider)(nil)

// Open builds a session for a context from the current kubeconfig. No
// network I/O: connection problems surface as view statuses.
func (p *Provider) Open(_ context.Context, target string) (provider.Session, error) {
	l := load(p.sources())
	var kc *kubeContext
	for i := range l.Contexts {
		if l.Contexts[i].ID == target {
			kc = &l.Contexts[i]
		}
	}
	if kc == nil {
		return nil, &provider.Error{Class: provider.ClassNotFound, Message: fmt.Sprintf("no context %q in kubeconfig", target)}
	}
	cfg, err := restConfig(*kc)
	if err != nil {
		return nil, &provider.Error{Class: provider.ClassInternal, Message: err.Error()}
	}
	execshim.Wrap(cfg, p.shimPath, execshim.DefaultTimeout)
	sess, err := sessionFor(cfg, target, kc.Name, kc.Hash)
	if err != nil {
		return nil, &provider.Error{Class: provider.ClassInternal, Message: err.Error()}
	}
	sess.slots = p.logSlots
	return sess, nil
}

// sessionFor builds a session talking to cfg: its dynamic client, the
// connection snapshot, the log fetcher and the action writer.
func sessionFor(cfg *rest.Config, target, title, hash string) (*session, error) {
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	sess := newSession(target, hash, dyn, true)
	sess.conn = newConn(cfg, dyn, target, title, hash)
	if sess.logs, err = httpLogFetcher(cfg); err != nil {
		sess.Close()
		return nil, err
	}
	if sess.writer, err = newRESTWriter(cfg); err != nil {
		sess.Close()
		return nil, err
	}
	return sess, nil
}

// restConfig resolves a context exactly like kubectl --context would with
// the same files (relative paths, exec plugins, proxy settings included).
func restConfig(kc kubeContext) (*rest.Config, error) {
	rules := &clientcmd.ClientConfigLoadingRules{Precedence: kc.Files}
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{CurrentContext: kc.Name})
	cfg, err := cc.ClientConfig()
	if err != nil {
		return nil, err
	}
	cfg.UserAgent = "spk-ocular"
	cfg.QPS, cfg.Burst = 50, 100
	// No cfg.Timeout: it would cut long-lived watches. Calls use contexts.
	return cfg, nil
}

// configHash identifies what a context resolves to (context, cluster and
// user entries). A changed hash means an open session talks to the wrong
// thing and must be rebuilt. The hash covers credentials but never exposes them.
func configHash(name string, cfg *clientcmdapi.Config, files []string) string {
	c := cfg.Contexts[name]
	var parts []any
	parts = append(parts, name, files, c)
	if c != nil {
		parts = append(parts, cfg.Clusters[c.Cluster], cfg.AuthInfos[c.AuthInfo])
	}
	b, _ := json.Marshal(parts)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

type session struct {
	// ctx lives as long as the session: shared background requests
	// (metrics) end with it.
	ctx    context.Context
	cancel context.CancelFunc

	target  string
	hash    string
	dyn     dynamic.Interface
	caches  *cacheManager
	kinds   *kindRegistry
	now     func() time.Time
	metrics metricsCache
	logs    logFetcher    // nil: no logs (tests without a server)
	slots   chan struct{} // the provider's logSlots; nil: unlimited
	// conn is the connection snapshot live resources (terminals, tunnels)
	// keep; it outlives the session.
	conn *conn
	// writer sends actions' writes (nil: through dyn — fake clients in tests).
	writer actionWriter
	// beforeWrite (tests) runs between an action's read and its write.
	beforeWrite func(action string, u *unstructured.Unstructured)
	// problemSources are what a Problems view observes (tests replace them).
	problemSources []problemSource
}

func newSession(target, hash string, dyn dynamic.Interface, watchList bool) *session {
	ctx, cancel := context.WithCancel(context.Background())
	return &session{
		ctx: ctx, cancel: cancel, target: target, hash: hash, dyn: dyn, caches: newCacheManager(dyn, watchList), kinds: allKinds, now: time.Now, problemSources: problemSources,
		conn: newConn(&rest.Config{Host: "https://cluster.invalid"}, dyn, target, target, hash),
	}
}

func (s *session) ConfigHash() string           { return s.hash }
func (s *session) ScopeKind() string            { return namespacesKind.desc.ID }
func (s *session) Kinds() []core.KindDescriptor { return s.kinds.descriptors() }
func (s *session) Close() {
	s.cancel()
	s.caches.closeAll()
}

func (s *session) Scopes(ctx context.Context) ([]core.Scope, error) {
	ctx, cancel := context.WithTimeout(ctx, scopesTimeout)
	defer cancel()
	list, err := s.dyn.Resource(namespacesGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		class, msg := classify(err)
		return nil, &provider.Error{Class: class, Message: msg}
	}
	out := make([]core.Scope, 0, len(list.Items))
	for _, ns := range list.Items {
		out = append(out, core.Scope{Name: ns.GetName()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *session) Watch(q provider.Query, sink provider.Sink) (func(), error) {
	def := s.kinds.byID[q.Kind]
	switch {
	case def == nil:
		return nil, &provider.Error{Class: provider.ClassUnsupported, Message: fmt.Sprintf("unknown kind %q", q.Kind)}
	case !q.Scope.Valid():
		return nil, &provider.Error{Class: provider.ClassInternal, Message: "invalid scope selector"}
	case def == problemsKind:
		return s.watchProblems(q, sink)
	}
	return s.watchDef(def, q, "", sink)
}

// watchDef feeds sink from def's cache for q; selector (a field selector)
// narrows the cache further.
func (s *session) watchDef(def *kindDef, q provider.Query, selector string, sink provider.Sink) (func(), error) {
	if def.virtual {
		return nil, &provider.Error{Class: provider.ClassUnsupported, Message: fmt.Sprintf("%s is not observed directly", def.desc.ID)}
	}
	if !q.Scope.Valid() {
		return nil, &provider.Error{Class: provider.ClassInternal, Message: "invalid scope selector"}
	}
	key := cacheKey{gvr: def.gvr, selector: selector}
	if def.namespaced && q.Scope.Mode == core.ScopeOne {
		key.namespace = q.Scope.Name
	}
	if q.Name != "" {
		if q.Subject != nil {
			return nil, &provider.Error{Class: provider.ClassUnsupported, Message: "name and subject cannot be combined"}
		}
		key.selector = "metadata.name=" + q.Name
	}
	if q.Subject != nil {
		// Only events can be narrowed to an object: core/v1 involvedObject.uid,
		// in the object's namespace (cluster-scoped objects: all namespaces).
		if def != eventsKind || q.Subject.UID == "" {
			return nil, &provider.Error{Class: provider.ClassUnsupported, Message: "narrowing to an object needs events and the object's UID"}
		}
		key.namespace = q.Subject.Scope
		key.selector = "involvedObject.uid=" + q.Subject.UID
	}
	c, ok := s.caches.acquire(key, def)
	if !ok {
		return nil, &provider.Error{Class: provider.ClassGone, Message: "session closed"}
	}
	w := &viewWatch{c: c, def: def, sink: sink, target: s.target, now: s.now, done: make(chan struct{}), deadlines: map[string]deadline{}}
	c.mu.Lock()
	c.watchers[w] = struct{}{}
	c.mu.Unlock()
	w.pushStatus() // loading, or an error the cache already knows
	reg, err := c.inf.AddEventHandler(w.handlers())
	if err != nil {
		s.detach(c, w)
		return nil, &provider.Error{Class: provider.ClassGone, Message: err.Error()}
	}
	w.reg = reg
	go w.waitSynced()
	return func() {
		w.stop()
		_ = c.inf.RemoveEventHandler(reg)
		s.detach(c, w)
	}, nil
}

func (s *session) detach(c *informerCache, w *viewWatch) {
	c.mu.Lock()
	delete(c.watchers, w)
	c.mu.Unlock()
	s.caches.release(c)
}

// Stats reports cache counts for leak checks (/api/_test/stats).
func (s *session) Stats() map[string]int {
	active, idle := s.caches.stats()
	watchers, deadlines := s.caches.watchStats()
	return map[string]int{"caches_active": active, "caches_idle": idle, "watchers": watchers, "deadlines": deadlines,
		"cache_lists": int(s.caches.counts.lists.Load()), "cache_watch_starts": int(s.caches.counts.watchStarts.Load()),
		"cache_initial_syncs": int(s.caches.counts.initialSyncs.Load())}
}
