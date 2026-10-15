package compose

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/compose/engine"
	"github.com/spk/spk-ocular/internal/views"
	"github.com/stretchr/testify/require"
)

func stalledContainerBody(e *testEnv) (<-chan struct{}, <-chan struct{}) {
	started, stopped := make(chan struct{}), make(chan struct{})
	var begin, end sync.Once
	e.fe.AddHook(func(w http.ResponseWriter, r *http.Request, path string) bool {
		if path != "/containers/json" {
			return false
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "["+strings.Repeat(" ", 32768))
		w.(http.Flusher).Flush()
		begin.Do(func() { close(started) })
		<-r.Context().Done()
		end.Do(func() { close(stopped) })
		return true
	})
	return started, stopped
}
func TestLoadingProgressAndLastDockerConsumerCancellation(t *testing.T) {
	e := newTestEnv(t, func(cfg *engine.Config) { cfg.RequestTimeout = 5 * time.Second })
	started, stopped := stalledContainerBody(e)
	m := views.NewManager(events.NewEmitter())
	defer m.CloseAll()
	q := provider.Query{Kind: KindContainers, Scope: core.ScopeSel{Mode: core.ScopeAll}}
	first, err := m.Open("owner", e.s, q)
	require.NoError(t, err)
	second, err := m.Open("owner", e.s, q)
	require.NoError(t, err)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("list body did not start")
	}
	require.Eventually(t, func() bool {
		p, err := m.Get(first, 0)
		return err == nil && p.Status.State == provider.StatusLoading && p.Status.DownloadedBytes != nil && *p.Status.DownloadedBytes >= 32769
	}, 3*time.Second, 10*time.Millisecond)
	m.Close(first)
	select {
	case <-stopped:
		t.Fatal("one view stopped another consumer")
	case <-time.After(100 * time.Millisecond):
	}
	m.Close(second)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("last consumer kept reading during cache grace")
	}
	require.Zero(t, e.s.Stats()["feeds_active"])
	require.Zero(t, e.s.Stats()["feeds_idle"])
}
func TestDockerRefreshReportsPartialBytesAndCancellationStopsPreparation(t *testing.T) {
	e := newTestEnv(t, func(cfg *engine.Config) { cfg.RequestTimeout = 5 * time.Second })
	e.fe.PutContainer(composeContainer(id(1), "shop", "api", "running"))
	m := views.NewManager(events.NewEmitter())
	defer m.CloseAll()
	q := provider.Query{Kind: KindContainers, Scope: core.ScopeSel{Mode: core.ScopeAll}}
	view, err := m.Open("owner", e.s, q)
	require.NoError(t, err)
	require.Eventually(t, func() bool { p, err := m.Get(view, 0); return err == nil && p.Status.State == provider.StatusReady }, 3*time.Second, 10*time.Millisecond)
	_, stopped := stalledContainerBody(e)
	done := make(chan error, 1)
	go func() { done <- m.Refresh(context.Background(), view) }()
	require.Eventually(t, func() bool {
		p, err := m.Get(view, 0)
		return err == nil && p.Status.State == provider.StatusLoading && p.Status.DownloadedBytes != nil && *p.Status.DownloadedBytes >= 32769 && len(p.Upserts) == 1
	}, 3*time.Second, 10*time.Millisecond)
	m.Close(view)
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("refresh preparation did not cancel")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("refresh HTTP body did not cancel")
	}
	require.Eventually(t, func() bool { return e.s.Stats()["feeds_active"] == 0 && e.s.Stats()["feeds_idle"] == 0 }, time.Second, 10*time.Millisecond)
}
func TestDockerWarmViewDoesNotCountHistoricalSnapshotAsNewDownload(t *testing.T) {
	e := newTestEnv(t)
	e.fe.PutContainer(composeContainer(id(1), "shop", "api", "running"))
	m := views.NewManager(events.NewEmitter())
	defer m.CloseAll()
	q := provider.Query{Kind: KindContainers, Scope: core.ScopeSel{Mode: core.ScopeAll}}
	first, err := m.Open("owner", e.s, q)
	require.NoError(t, err)
	require.Eventually(t, func() bool { p, err := m.Get(first, 0); return err == nil && p.Status.State == provider.StatusReady }, 3*time.Second, 10*time.Millisecond)
	p, err := m.Get(first, 0)
	require.NoError(t, err)
	require.NotNil(t, p.Status.DownloadedBytes)
	require.Positive(t, *p.Status.DownloadedBytes)
	count := e.fe.Count("/containers/json")
	second, err := m.Open("owner", e.s, q)
	require.NoError(t, err)
	require.Eventually(t, func() bool { p, err := m.Get(second, 0); return err == nil && p.Status.State == provider.StatusReady }, time.Second, 10*time.Millisecond)
	p, err = m.Get(second, 0)
	require.NoError(t, err)
	require.NotNil(t, p.Status.DownloadedBytes)
	require.Zero(t, *p.Status.DownloadedBytes)
	require.Equal(t, count, e.fe.Count("/containers/json"))
}
