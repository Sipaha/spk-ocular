package kubernetes

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/views"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

func TestLoadingProgressAndLastConsumerCancellation(t *testing.T) {
	// The server has not finished even one JSON page: progress must count reads,
	// not decoded objects, Content-Length estimates or completed-page sizes.
	stopped := make(chan struct{})
	var once sync.Once
	body := `{"apiVersion":"v1","kind":"PodList","metadata":{"resourceVersion":"1"},"items":[` + strings.Repeat(" ", 32768)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		once.Do(func() { close(stopped) })
	}))
	defer server.Close()
	dyn, err := dynamic.NewForConfig(budgetConfig(&rest.Config{Host: server.URL, QPS: 50, Burst: 100}))
	require.NoError(t, err)
	session := newSession("progress-fixture", "hash", dyn, false)
	defer session.Close()
	manager := views.NewManager(events.NewEmitter())
	defer manager.CloseAll()
	query := provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "selected"}}
	first, err := manager.Open("owner", session, query)
	require.NoError(t, err)
	second, err := manager.Open("owner", session, query)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		p, e := manager.Get(first, 0)
		return e == nil && p.Status.State == provider.StatusLoading && p.Status.DownloadedBytes != nil && *p.Status.DownloadedBytes == int64(len(body)) && len(p.Upserts) == 0
	}, 3*time.Second, 10*time.Millisecond)
	p, err := manager.Get(first, 0)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		other, e := manager.Get(second, 0)
		return e == nil && other.Status.DownloadedBytes != nil && *other.Status.DownloadedBytes == *p.Status.DownloadedBytes
	}, time.Second, 10*time.Millisecond)
	manager.Close(first)
	select {
	case <-stopped:
		t.Fatal("cancelling one view stopped another consumer")
	case <-time.After(100 * time.Millisecond):
	}
	manager.Close(second)
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("last view cancellation did not cancel the HTTP body")
	}
	require.Eventually(t, func() bool { a, b := session.caches.stats(); return a == 0 && b == 0 }, time.Second, 10*time.Millisecond)
}

func TestLoadingProgressCompleteInventoryUsesActualBodyBytes(t *testing.T) {
	session, api := loadSession(t, 150)
	manager := views.NewManager(events.NewEmitter())
	defer manager.CloseAll()
	id, err := manager.Open("owner", session, provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "load-scope"}})
	require.NoError(t, err)
	require.Eventually(t, func() bool { p, e := manager.Get(id, 0); return e == nil && p.Status.State == provider.StatusReady }, 5*time.Second, 10*time.Millisecond)
	page, err := manager.Get(id, 0)
	require.NoError(t, err)
	require.Len(t, page.Upserts, 150)
	require.NotNil(t, page.Status.DownloadedBytes)
	require.Equal(t, api.bytes.Load(), *page.Status.DownloadedBytes)
	require.EqualValues(t, 2, api.lists.Load())
	warm, err := manager.Open("owner", session, provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeOne, Name: "load-scope"}})
	require.NoError(t, err)
	require.Eventually(t, func() bool { p, e := manager.Get(warm, 0); return e == nil && p.Status.State == provider.StatusReady }, time.Second, 10*time.Millisecond)
	page, err = manager.Get(warm, 0)
	require.NoError(t, err)
	require.NotNil(t, page.Status.DownloadedBytes)
	require.Zero(t, *page.Status.DownloadedBytes)
	require.EqualValues(t, 2, api.lists.Load(), "warm replay must not issue another LIST or count old bytes as a new download")
}

func TestTransferCountsStreamingReadsAndFreezesAfterBootstrap(t *testing.T) {
	progress := &loadTransfer{}
	cfg := budgetConfig(&rest.Config{Host: "https://unused.invalid"})
	transport := cfg.WrapTransport(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("initial-live"))}, nil
	}))
	req, err := http.NewRequestWithContext(progress.context(context.Background()), http.MethodGet, "https://unused.invalid/pods?watch=true&sendInitialEvents=true", nil)
	require.NoError(t, err)
	response, err := transport.RoundTrip(req)
	require.NoError(t, err)
	defer response.Body.Close()
	require.EqualValues(t, 0, *progress.snapshot())
	dst := make([]byte, 7)
	_, err = io.ReadFull(response.Body, dst)
	require.NoError(t, err)
	require.EqualValues(t, 7, *progress.snapshot())
	progress.finished.Store(true)
	_, err = io.ReadAll(response.Body)
	require.NoError(t, err)
	require.EqualValues(t, 7, *progress.snapshot())
}
