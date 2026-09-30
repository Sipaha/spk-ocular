package kubernetes

import (
	"context"
	"errors"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

const (
	metricsTTL     = 10 * time.Second // reuse window for identical requests
	metricsTimeout = 10 * time.Second
	// metricsAbsentTTL: after metrics.k8s.io was confirmed missing, ask again
	// only this often (someone may install metrics-server).
	metricsAbsentTTL = 2 * time.Minute
)

var (
	podMetricsGVR  = schema.GroupVersionResource{Group: "metrics.k8s.io", Version: "v1beta1", Resource: "pods"}
	nodeMetricsGVR = schema.GroupVersionResource{Group: "metrics.k8s.io", Version: "v1beta1", Resource: "nodes"}
)

var _ provider.MetricsSource = (*session)(nil)

type metricsEntry struct {
	at   time.Time
	res  provider.Metrics
	err  error
	wait chan struct{} // closed when the request finished (singleflight)
}

type metricsCache struct {
	mu      sync.Mutex
	entries map[string]*metricsEntry
	absent  map[string]time.Time // per metrics resource (pods/nodes): when found missing
}

var errNoMetricsAPI = &provider.Error{Class: provider.ClassUnsupported, Message: "metrics.k8s.io is not installed"}

// Metrics returns CPU (cores) and memory (bytes) keyed by row id (the
// object's UID) for pods or nodes. A sample is attributed to the object the
// informer cache holds under that name only if that object existed when the
// sample was taken — a same-named replacement gets nothing until the next
// sample. Missing API → unsupported; denied → forbidden; nothing is ever
// reported as a zero it did not measure.
//
// rowIDs are not needed: one list answers for every row of the query (and
// is shared by the callers of the same query for metricsTTL).
func (s *session) Metrics(ctx context.Context, q provider.Query, _ []string) (provider.Metrics, error) {
	var gvr, objGVR schema.GroupVersionResource
	ns := ""
	switch q.Kind {
	case podsKind.desc.ID:
		gvr, objGVR = podMetricsGVR, podsKind.gvr
		if q.Scope.Mode == core.ScopeOne {
			ns = q.Scope.Name
		}
	case nodesKind.desc.ID:
		gvr, objGVR = nodeMetricsGVR, nodesKind.gvr
	default:
		return provider.Metrics{}, &provider.Error{Class: provider.ClassUnsupported, Message: "no metrics for " + q.Kind}
	}
	key := gvr.Resource + "/" + ns
	now := s.now()

	m := &s.metrics
	m.mu.Lock()
	if m.entries == nil {
		m.entries = map[string]*metricsEntry{}
		m.absent = map[string]time.Time{}
	}
	for k, e := range m.entries { // bounded: only recently used queries stay
		if e.wait == nil && now.Sub(e.at) >= metricsTTL {
			delete(m.entries, k)
		}
	}
	if at, ok := m.absent[gvr.Resource]; ok && now.Sub(at) < metricsAbsentTTL {
		m.mu.Unlock()
		return provider.Metrics{}, errNoMetricsAPI
	}
	e := m.entries[key]
	if e == nil {
		e = &metricsEntry{wait: make(chan struct{})}
		m.entries[key] = e
		// The shared request lives with the session, not with one caller.
		go s.fetchInto(e, gvr, objGVR, ns)
	}
	wait := e.wait
	m.mu.Unlock()
	if wait != nil {
		select {
		case <-wait:
		case <-ctx.Done():
			return provider.Metrics{}, ctx.Err()
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return e.res, e.err
}

func (s *session) fetchInto(e *metricsEntry, gvr, objGVR schema.GroupVersionResource, ns string) {
	res, err := s.fetchMetrics(gvr, objGVR, ns)
	m := &s.metrics
	m.mu.Lock()
	e.at, e.res, e.err = s.now(), res, err
	close(e.wait)
	e.wait = nil
	if errors.Is(err, errNoMetricsAPI) {
		m.absent[gvr.Resource] = s.now()
	} else if err == nil {
		delete(m.absent, gvr.Resource)
	}
	m.mu.Unlock()
}

func (s *session) fetchMetrics(gvr, objGVR schema.GroupVersionResource, ns string) (provider.Metrics, error) {
	ctx, cancel := context.WithTimeout(s.ctx, metricsTimeout)
	defer cancel()
	list, err := s.dyn.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		// A 404 on the list itself means the metrics resource is not served
		// (a missing namespace lists empty, it is not a 404).
		if apierrors.IsNotFound(err) {
			return provider.Metrics{}, errNoMetricsAPI
		}
		class, msg := classify(err)
		return provider.Metrics{}, &provider.Error{Class: class, Message: msg}
	}
	out := provider.Metrics{Values: map[string]provider.Usage{}}
	for _, it := range list.Items {
		var cpu, mem float64
		var haveCPU, haveMem bool
		addUsage := func(u map[string]any) {
			if q, err := resource.ParseQuantity(str(u, "cpu")); err == nil {
				cpu += q.AsApproximateFloat64()
				haveCPU = true
			}
			if q, err := resource.ParseQuantity(str(u, "memory")); err == nil {
				mem += q.AsApproximateFloat64()
				haveMem = true
			}
		}
		if gvr == podMetricsGVR {
			for _, c := range slice(it.Object, "containers") {
				if u, ok := c["usage"].(map[string]any); ok {
					addUsage(u)
				}
			}
		} else if u, ok := it.Object["usage"].(map[string]any); ok {
			addUsage(u)
		}
		if !haveCPU && !haveMem {
			continue // no sample: unknown, not zero
		}
		at := timeAt(it.Object, "timestamp")
		obj, ok := s.caches.lookup(objGVR, it.GetNamespace(), it.GetName())
		if !ok {
			continue // not observed by any open view: cannot name its incarnation
		}
		if !at.IsZero() && obj.GetCreationTimestamp().After(at.Add(time.Second)) {
			continue // the sample predates this object: an earlier incarnation's
		}
		if at.After(out.Timestamp) {
			out.Timestamp = at
		}
		out.Window = str(it.Object, "window")
		u := provider.Usage{At: at}
		if haveCPU {
			u.CPU = provider.Num(cpu)
		}
		if haveMem {
			u.Memory = provider.Num(mem)
		}
		out.Values[string(obj.GetUID())] = u
	}
	return out, nil
}
