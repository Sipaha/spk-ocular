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
	absent  time.Time // when the API was last found missing
}

// Metrics returns CPU (cores) and memory (bytes) per "scope/name" for pods
// or nodes. Missing API → unsupported; denied → forbidden; nothing is ever
// reported as a zero it did not measure.
func (s *session) Metrics(ctx context.Context, q provider.Query) (provider.Metrics, error) {
	var gvr schema.GroupVersionResource
	ns := ""
	switch q.Kind {
	case podsKind.desc.ID:
		gvr = podMetricsGVR
		if q.Scope.Mode == core.ScopeOne {
			ns = q.Scope.Name
		}
	case nodesKind.desc.ID:
		gvr = nodeMetricsGVR
	default:
		return provider.Metrics{}, &provider.Error{Class: provider.ClassUnsupported, Message: "no metrics for " + q.Kind}
	}
	key := gvr.Resource + "/" + ns
	now := s.now()

	m := &s.metrics
	m.mu.Lock()
	if m.entries == nil {
		m.entries = map[string]*metricsEntry{}
	}
	if !m.absent.IsZero() && now.Sub(m.absent) < metricsAbsentTTL {
		m.mu.Unlock()
		return provider.Metrics{}, &provider.Error{Class: provider.ClassUnsupported, Message: "metrics.k8s.io is not installed"}
	}
	if e := m.entries[key]; e != nil && (e.wait != nil || now.Sub(e.at) < metricsTTL) {
		wait := e.wait
		m.mu.Unlock()
		if wait != nil {
			select {
			case <-wait:
			case <-ctx.Done():
				return provider.Metrics{}, ctx.Err()
			}
		}
		return e.res, e.err
	}
	e := &metricsEntry{wait: make(chan struct{})}
	m.entries[key] = e
	m.mu.Unlock()

	res, err := s.fetchMetrics(gvr, ns)
	m.mu.Lock()
	e.at, e.res, e.err = s.now(), res, err
	close(e.wait)
	e.wait = nil
	var pe *provider.Error
	if errors.As(err, &pe) && pe.Class == provider.ClassUnsupported {
		m.absent = s.now()
	}
	m.mu.Unlock()
	return res, err
}

func (s *session) fetchMetrics(gvr schema.GroupVersionResource, ns string) (provider.Metrics, error) {
	ctx, cancel := context.WithTimeout(context.Background(), metricsTimeout)
	defer cancel()
	list, err := s.dyn.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return provider.Metrics{}, &provider.Error{Class: provider.ClassUnsupported, Message: "metrics.k8s.io is not installed"}
		}
		class, msg := classify(err)
		return provider.Metrics{}, &provider.Error{Class: class, Message: msg}
	}
	out := provider.Metrics{Values: map[string]provider.Usage{}}
	for _, it := range list.Items {
		var cpu, mem float64
		var have bool
		addUsage := func(u map[string]any) {
			if q, err := resource.ParseQuantity(str(u, "cpu")); err == nil {
				cpu += q.AsApproximateFloat64()
				have = true
			}
			if q, err := resource.ParseQuantity(str(u, "memory")); err == nil {
				mem += q.AsApproximateFloat64()
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
		if !have {
			continue // no sample: unknown, not zero
		}
		if t := timeAt(it.Object, "timestamp"); t.After(out.Timestamp) {
			out.Timestamp = t
		}
		out.Window = str(it.Object, "window")
		out.Values[it.GetNamespace()+"/"+it.GetName()] = provider.Usage{CPU: cpu, Memory: mem}
	}
	return out, nil
}
