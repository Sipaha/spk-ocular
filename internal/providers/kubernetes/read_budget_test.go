package kubernetes

import (
	"context"
	"io"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestReadBudgetSharedAcrossClientsUntilBodyReleased(t *testing.T) {
	var calls atomic.Int32
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("payload"))}, nil
	})
	cfg := budgetConfig(&rest.Config{Host: "https://cluster.invalid"})
	first, second := cfg.WrapTransport(base), cfg.WrapTransport(base)
	req, err := http.NewRequest(http.MethodGet, "https://cluster.invalid/api", nil)
	require.NoError(t, err)
	bodies := make([]io.ReadCloser, 0, sessionReadConcurrency)
	for i := 0; i < sessionReadConcurrency; i++ {
		resp, e := first.RoundTrip(req)
		require.NoError(t, e)
		bodies = append(bodies, resp.Body)
	}
	defer func() {
		for _, body := range bodies {
			_ = body.Close()
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = second.RoundTrip(req.WithContext(ctx))
	require.ErrorIs(t, err, context.Canceled)
	require.EqualValues(t, sessionReadConcurrency, calls.Load())
	require.NoError(t, bodies[0].Close())
	resp, err := second.RoundTrip(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.EqualValues(t, sessionReadConcurrency+1, calls.Load())
}

func TestReadBudgetStreamsDoNotConsumeFiniteBodySlots(t *testing.T) {
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("stream"))}, nil
	})
	b := &readBudget{next: base, rate: flowcontrol.NewFakeAlwaysRateLimiter(), slots: make(chan struct{}, 1)}
	req, err := http.NewRequest(http.MethodGet, "https://cluster.invalid/pods?watch=true", nil)
	require.NoError(t, err)
	resp, err := b.RoundTrip(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	finite, err := http.NewRequest(http.MethodGet, "https://cluster.invalid/pods", nil)
	require.NoError(t, err)
	r, err := b.RoundTrip(finite)
	require.NoError(t, err)
	defer func() { _ = r.Body.Close() }()
	require.Len(t, b.slots, 1)
}

func TestReadBudgetDoesNotMutateOriginalConfigAndComposesWrapper(t *testing.T) {
	var wrapped atomic.Int32
	original := &rest.Config{Host: "https://cluster.invalid", QPS: 50, Burst: 100, WrapTransport: func(next http.RoundTripper) http.RoundTripper { wrapped.Add(1); return next }}
	cfg := budgetConfig(original)
	require.EqualValues(t, 50, original.QPS)
	require.Nil(t, original.RateLimiter)
	_ = cfg.WrapTransport(http.DefaultTransport)
	_ = cfg.WrapTransport(http.DefaultTransport)
	require.EqualValues(t, 2, wrapped.Load())
}

type notifyingLimiter struct {
	flowcontrol.RateLimiter
	waiting chan struct{}
}

func (l notifyingLimiter) Wait(ctx context.Context) error {
	close(l.waiting)
	return l.RateLimiter.Wait(ctx)
}

func TestReadBudgetRateWaitCancels(t *testing.T) {
	limiter := flowcontrol.NewTokenBucketRateLimiter(.001, 1)
	require.True(t, limiter.TryAccept())
	waiting := make(chan struct{})
	b := &readBudget{next: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("canceled request reached transport")
		return nil, nil
	}), rate: notifyingLimiter{RateLimiter: limiter, waiting: waiting}, slots: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://cluster.invalid/api", nil)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { _, err := b.RoundTrip(req); done <- err }()
	select {
	case <-waiting:
	case <-time.After(time.Second):
		t.Fatal("request did not enter rate wait")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("rate wait ignored cancellation")
	}
	require.Empty(t, b.slots)
}

// Exercise the actual three client constructors. A raw Table/discovery request
// must not bypass slots occupied by dynamic-client responses (or vice versa).
func TestReadBudgetCoversDynamicTableAndDiscoveryHTTP(t *testing.T) {
	started := make(chan string, 8)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		started <- r.URL.Path
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		if strings.Contains(r.URL.Path, "/namespaces/table/") {
			_, _ = io.WriteString(w, `{"kind":"Table","apiVersion":"meta.k8s.io/v1","metadata":{"resourceVersion":"1"},"columnDefinitions":[],"rows":[]}`)
		} else {
			_, _ = io.WriteString(w, `{"kind":"PodList","apiVersion":"v1","metadata":{"resourceVersion":"1"},"items":[]}`)
		}
	}))
	defer server.Close()
	var once sync.Once
	defer once.Do(func() { close(release) })
	cfg := budgetConfig(&rest.Config{Host: server.URL})
	dyn, err := dynamic.NewForConfig(cfg)
	require.NoError(t, err)
	tables, err := newTableClient(cfg)
	require.NoError(t, err)
	get, err := httpGetter(cfg)
	require.NoError(t, err)
	done := make(chan error, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _, e := dyn.Resource(podsKind.gvr).List(ctx, metav1.ListOptions{}); done <- e }()
	go func() { _, e := dyn.Resource(nodesKind.gvr).List(ctx, metav1.ListOptions{}); done <- e }()
	go func() { _, e := tables.list(ctx, podsKind.gvr, "table", metav1.ListOptions{Limit: 1}); done <- e }()
	go func() { _, _, e := get(ctx, "/api", "application/json"); done <- e }()
	for range sessionReadConcurrency {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("four finite response bodies did not start")
		}
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	_, _, err = get(canceled, "/apis", "application/json")
	require.Error(t, err)
	require.Empty(t, started, "a canceled fifth request cannot pass occupied shared slots")
	once.Do(func() { close(release) })
	for range sessionReadConcurrency {
		require.NoError(t, <-done)
	}
}

func TestBroadResponseBudgetBoundsDecodeWhenServerIgnoresLimit(t *testing.T) {
	g := newScopeBudget()
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 9<<20)))}, nil
	})
	b := budgetConfig(&rest.Config{}).WrapTransport(base)
	req, err := http.NewRequestWithContext(g.context(context.Background()), http.MethodGet, "https://cluster.invalid/pods?limit=100", nil)
	require.NoError(t, err)
	resp, err := b.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	require.Error(t, err)
	require.LessOrEqual(t, len(data), 8<<20)
	require.Error(t, g.failure())
	// The slot is released despite a decoder error.
	require.Empty(t, b.(*readBudget).slots)
}
