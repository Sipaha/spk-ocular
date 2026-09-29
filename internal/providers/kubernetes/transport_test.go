package kubernetes

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/views"
)

const podListJSON = `{"kind":"PodList","apiVersion":"v1","metadata":{"resourceVersion":"10"},"items":[
 {"kind":"Pod","apiVersion":"v1","metadata":{"name":"a","namespace":"ns","uid":"u-a","resourceVersion":"10","creationTimestamp":"2026-09-29T10:00:00Z"},
  "status":{"phase":"Running"}}]}`

// fakeAPIServer serves a pod list; watch requests behave as watchMode says.
func fakeAPIServer(t *testing.T, watchMode func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, dynamic.Interface) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") == "true" {
			watchMode(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, podListJSON)
	}))
	t.Cleanup(srv.Close)
	dyn, err := dynamic.NewForConfig(&rest.Config{Host: srv.URL})
	require.NoError(t, err)
	return srv, dyn
}

func withTimeouts(t *testing.T, establish, grace time.Duration) {
	t.Helper()
	oldE, oldG := watchEstablishTimeout, reconnectGrace
	watchEstablishTimeout, reconnectGrace = establish, grace
	t.Cleanup(func() { watchEstablishTimeout, reconnectGrace = oldE, oldG })
}

func openPods(t *testing.T, dyn dynamic.Interface) (*views.Manager, string) {
	t.Helper()
	s := newSession("t", "h", dyn, false) // plain list+watch against this minimal server
	t.Cleanup(s.Close)
	m := views.NewManager(eventsEmitter())
	t.Cleanup(m.CloseAll)
	id, err := m.Open("s", s, provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeAll}})
	require.NoError(t, err)
	return m, id
}

// Review 2026-09-29: a watch that never gets response headers left a synced
// view "ready" forever.
func TestWatchWithoutHeadersGoesStale(t *testing.T) {
	withTimeouts(t, 300*time.Millisecond, time.Hour)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	_, dyn := fakeAPIServer(t, func(_ http.ResponseWriter, r *http.Request) {
		select { // accept, never answer
		case <-release:
		case <-r.Context().Done():
		}
	})
	m, id := openPods(t, dyn)
	var p views.Page
	require.Eventually(t, func() bool {
		p, _ = m.Get(id, 0)
		return p.Status.State == provider.StatusStale
	}, 10*time.Second, 20*time.Millisecond, "last: %+v", p.Status)
	assert.Equal(t, provider.ClassUnavailable, p.Status.Class)
	assert.Contains(t, p.Status.Message, "did not start")
	assert.Len(t, p.Upserts, 1, "the listed rows stay, marked stale")
}

// A stream that ends and cannot be re-established within the grace period
// makes the view stale ("reconnecting"); a stream renewed at once does not.
func TestDroppedStreamThatStaysDownGoesStale(t *testing.T) {
	withTimeouts(t, time.Hour, 200*time.Millisecond)
	var watches atomic.Int32
	_, dyn := fakeAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		if watches.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			return // first stream: headers, then EOF
		}
		<-r.Context().Done() // the re-watch hangs (half-open network)
	})
	m, id := openPods(t, dyn)
	var p views.Page
	require.Eventually(t, func() bool {
		p, _ = m.Get(id, 0)
		return p.Status.State == provider.StatusStale
	}, 10*time.Second, 20*time.Millisecond, "last: %+v", p.Status)
	assert.Contains(t, p.Status.Message, "reconnecting")
}

func TestRenewedStreamStaysReady(t *testing.T) {
	withTimeouts(t, time.Hour, 300*time.Millisecond)
	_, dyn := fakeAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		// A stream the reflector renews at once. (client-go treats a watch
		// shorter than a second without events as a failure and backs off;
		// real renewals last minutes.)
		select {
		case <-time.After(1100 * time.Millisecond):
		case <-r.Context().Done():
		}
	})
	m, id := openPods(t, dyn)
	require.Eventually(t, func() bool {
		p, _ := m.Get(id, 0)
		return p.Status.State == provider.StatusReady
	}, 5*time.Second, 20*time.Millisecond)
	time.Sleep(3500 * time.Millisecond) // several renewals, each re-established well within grace
	p, _ := m.Get(id, 0)
	assert.Equal(t, provider.StatusReady, p.Status.State, "%+v", p.Status)
}

func eventsEmitter() *events.Emitter { return events.NewEmitter() }
