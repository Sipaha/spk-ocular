package api

import (
	"context"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
)

type historyTestProvider struct {
	*argoTestProvider
	onRead func(context.Context)
}

func (p *historyTestProvider) ID() string { return "kubernetes" }
func (p *historyTestProvider) Open(ctx context.Context, target string) (provider.Session, error) {
	s, e := p.argoTestProvider.Open(ctx, target)
	if e != nil {
		return nil, e
	}
	return &historyTestSession{s.(*argoTestSession), p.onRead}, nil
}

type historyTestSession struct {
	*argoTestSession
	onRead func(context.Context)
}

func (s *historyTestSession) ResourceMetricsHistory(ctx context.Context, r core.HistoryRequest) (core.MetricsHistory, error) {
	s.owner.calls.Add(1)
	if s.onRead != nil {
		s.onRead(ctx)
	}
	return core.MetricsHistory{Ref: r.Ref}, nil
}
func (s *historyTestSession) Get(_ context.Context, r core.Ref) (*core.Resource, error) {
	return &core.Resource{Ref: r, MetricsHistoryAvailable: true}, nil
}
func TestMetricsHistoryRequiresUIAdmittedSessionAndValidSource(t *testing.T) {
	p := &historyTestProvider{argoTestProvider: &argoTestProvider{openable: newOpenable("a")}}
	s, _ := newService(t, p)
	r := core.HistoryRequest{Ref: core.Ref{Provider: "kubernetes", Target: "a", Kind: "pods", Scope: "blue", Name: "web", UID: "12345678-abcd-1234-abcd-123456789012"}, Source: core.HistorySource{URL: "https://source.invalid", Dedicated: true}, Window: "1h"}
	_, e := s.ResourceMetricsHistory(t.Context(), r)
	require.ErrorContains(t, e, "only to the application UI")
	_, e = s.ResourceMetricsHistory(UIContext(t.Context()), r)
	require.Error(t, e)
	require.Empty(t, p.opened)
	_, e = s.ConnectTarget(UIContext(t.Context()), "kubernetes", "a")
	require.NoError(t, e)
	require.Eventually(t, func() bool {
		s.sessMu.Lock()
		defer s.sessMu.Unlock()
		a := s.connections[ownerKey("kubernetes", "a")]
		return a != nil && a.status.State == "connected"
	}, time.Second, time.Millisecond)
	_, e = s.ResourceMetricsHistory(UIContext(t.Context()), r)
	require.NoError(t, e)
	require.EqualValues(t, 1, p.calls.Load())
	ui, e := s.GetResource(UIContext(t.Context()), r.Ref)
	require.NoError(t, e)
	require.True(t, ui.MetricsHistoryAvailable)
	plain, e := s.GetResource(t.Context(), r.Ref)
	require.NoError(t, e)
	require.False(t, plain.MetricsHistoryAvailable)
	require.True(t, ui.MetricsHistoryAvailable)
	bad := r
	bad.Source.URL = "https://user:PRIVATE@source.invalid"
	_, e = s.ResourceMetricsHistory(UIContext(t.Context()), bad)
	require.Error(t, e)
	require.NotContains(t, e.Error(), "PRIVATE")
	require.EqualValues(t, 1, p.calls.Load())
	for _, mode := range []string{"cancel", "disconnect"} {
		ctx, cancel := context.WithCancel(UIContext(t.Context()))
		p.onRead = nil
		// Opened session captures its callback; update that exact admitted fixture.
		s.sessMu.Lock()
		fixture := s.sessions[ownerKey("kubernetes", "a")].sess.(*historyTestSession)
		s.sessMu.Unlock()
		fixture.onRead = func(c context.Context) {
			deadline, ok := c.Deadline()
			require.True(t, ok)
			require.LessOrEqual(t, time.Until(deadline), 20*time.Second)
			if mode == "cancel" {
				cancel()
			} else {
				require.NoError(t, s.CloseTarget(UIContext(t.Context()), "kubernetes", "a"))
			}
		}
		_, e = s.ResourceMetricsHistory(ctx, r)
		require.Error(t, e)
		cancel()
	}
}
