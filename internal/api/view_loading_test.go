package api

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
)

func TestCancelledInventorySurvivesNewOpenUntilRetryOrNewConnection(t *testing.T) {
	s, _ := newService(t, newOpenable("a"))
	ctx := context.Background()
	req := OpenViewRequest{Provider: "k", Target: "a", Connection: "connect-1", Query: provider.Query{Kind: "pods", Scope: allScopes}}
	info, err := s.OpenView(ctx, req)
	require.NoError(t, err)
	require.NoError(t, s.CancelViewLoading(ctx, CancelViewLoadingRequest{OpenViewRequest: req, ViewID: info.ViewID}))
	_, err = s.GetRows(ctx, info.ViewID, 0)
	require.True(t, IsCoded(err, CodeGone))
	// A freshly reconstructed frontend has no local memory of cancellation.
	_, err = s.OpenView(ctx, req)
	require.True(t, IsCoded(err, CodeCancelled))
	req.Retry = true
	retried, err := s.OpenView(ctx, req)
	require.NoError(t, err)
	require.NoError(t, s.CloseView(ctx, retried.ViewID))
	req.Retry = false
	require.NoError(t, s.CancelViewLoading(ctx, CancelViewLoadingRequest{OpenViewRequest: req}))
	req.Connection = "connect-2"
	_, err = s.OpenView(ctx, req)
	require.NoError(t, err, "new explicit connection must not inherit the cancelled placeholder")
}

type cancellableViewProvider struct {
	*openable
	probe *cancellableViewSession
}

func (p *cancellableViewProvider) Open(ctx context.Context, target string) (provider.Session, error) {
	base, err := p.openable.Open(ctx, target)
	if err != nil {
		return nil, err
	}
	p.probe.Session = base
	return p.probe, nil
}

type cancellableViewSession struct {
	provider.Session
	block     atomic.Bool
	entered   chan struct{}
	cancelled chan struct{}
	once      sync.Once
}

func (p *cancellableViewSession) DescribeView(ctx context.Context, q provider.Query) (core.KindDescriptor, provider.Query, error) {
	if p.block.Load() {
		p.entered <- struct{}{}
		<-ctx.Done()
		p.once.Do(func() { close(p.cancelled) })
		return core.KindDescriptor{}, q, ctx.Err()
	}
	return p.Kinds()[0], q, nil
}
func TestCancelViewStopsSchemaProbeBeforeAnyViewIDExists(t *testing.T) {
	probe := &cancellableViewSession{entered: make(chan struct{}, 2), cancelled: make(chan struct{})}
	probe.block.Store(true)
	s, _ := newService(t, &cancellableViewProvider{openable: newOpenable("a"), probe: probe})
	req := OpenViewRequest{Provider: "k", Target: "a", Connection: "connect-1", Query: provider.Query{Kind: "pods", Scope: allScopes}}
	finished := make(chan error, 1)
	go func() { _, err := s.OpenView(context.Background(), req); finished <- err }()
	select {
	case <-probe.entered:
	case <-time.After(time.Second):
		t.Fatal("schema probe did not start")
	}
	require.NoError(t, s.CancelViewLoading(context.Background(), CancelViewLoadingRequest{OpenViewRequest: req}))
	select {
	case <-probe.cancelled:
	case <-time.After(time.Second):
		t.Fatal("schema context was not cancelled")
	}
	select {
	case err := <-finished:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("OpenView did not finish")
	}
	_, err := s.OpenView(context.Background(), req)
	require.True(t, IsCoded(err, CodeCancelled))
	probe.block.Store(false)
	req.Retry = true
	_, err = s.OpenView(context.Background(), req)
	require.NoError(t, err)
}

func TestCancelBeforeOpenAndOwnerCleanup(t *testing.T) {
	var loading viewLoading
	req := OpenViewRequest{Connection: "connect-1", Query: provider.Query{Kind: "pods", Scope: allScopes}}
	key := viewLoadingKey("owner", req)
	loading.cancelled = map[string]bool{key: true}
	_, _, err := loading.begin(context.Background(), key, false, "request-1")
	require.True(t, IsCoded(err, CodeCancelled))
	ctx, finish, err := loading.begin(context.Background(), key, true, "request-1")
	require.NoError(t, err)
	loading.forgetOwner("other")
	require.NoError(t, ctx.Err())
	loading.forgetOwner("owner")
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	finish()
	require.Empty(t, loading.cancelled)
	require.Empty(t, loading.pending)
}

func TestCancelOvertakesViewIDDeliveryWithoutClosingAnotherConsumer(t *testing.T) {
	s, _ := newService(t, newOpenable("a"))
	ctx := context.Background()
	first := OpenViewRequest{Provider: "k", Target: "a", Connection: "connect-1", RequestID: "first", Query: provider.Query{Kind: "pods", Scope: allScopes}}
	second := first
	second.RequestID = "second"
	a, err := s.OpenView(ctx, first)
	require.NoError(t, err)
	b, err := s.OpenView(ctx, second)
	require.NoError(t, err)
	// The first result has been produced but its caller has not learned the ID.
	require.NoError(t, s.CancelViewLoading(ctx, CancelViewLoadingRequest{OpenViewRequest: first}))
	_, err = s.GetRows(ctx, a.ViewID, 0)
	require.True(t, IsCoded(err, CodeGone))
	_, err = s.GetRows(ctx, b.ViewID, 0)
	require.NoError(t, err)
	require.NoError(t, s.CloseView(ctx, b.ViewID))
	s.loading.mu.Lock()
	defer s.loading.mu.Unlock()
	require.Empty(t, s.loading.pending)
}

func TestCancelPendingProbePreservesOtherPendingConsumer(t *testing.T) {
	probe := &cancellableViewSession{entered: make(chan struct{}, 2), cancelled: make(chan struct{})}
	probe.block.Store(true)
	s, _ := newService(t, &cancellableViewProvider{openable: newOpenable("a"), probe: probe})
	first := OpenViewRequest{Provider: "k", Target: "a", Connection: "connect-1", RequestID: "first", Query: provider.Query{Kind: "pods", Scope: allScopes}}
	second := first
	second.RequestID = "second"
	a, b := make(chan error, 1), make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _, err := s.OpenView(context.Background(), first); a <- err }()
	select {
	case <-probe.entered:
	case <-time.After(time.Second):
		t.Fatal("first probe did not start")
	}
	go func() { _, err := s.OpenView(ctx, second); b <- err }()
	select {
	case <-probe.entered:
	case <-time.After(time.Second):
		t.Fatal("second probe did not start")
	}
	require.NoError(t, s.CancelViewLoading(context.Background(), CancelViewLoadingRequest{OpenViewRequest: first}))
	select {
	case err := <-a:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("first did not cancel")
	}
	select {
	case <-b:
		t.Fatal("one cancellation stopped another pending consumer")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case <-b:
	case <-time.After(time.Second):
		t.Fatal("second context did not cancel")
	}
}
