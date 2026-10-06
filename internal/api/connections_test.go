package api

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
)

type connectionProvider struct {
	*openable
	check     func(context.Context, int64) error
	checks    atomic.Int64
	resyncs   atomic.Int64
	closeHook func()
}

type checkedSession struct {
	*fakeSession
	p *connectionProvider
}

func (p *connectionProvider) Open(ctx context.Context, target string) (provider.Session, error) {
	sess, err := p.openable.Open(ctx, target)
	if err != nil {
		return nil, err
	}
	return &checkedSession{fakeSession: sess.(*fakeSession), p: p}, nil
}
func (s *checkedSession) CheckConnection(ctx context.Context) error {
	return s.p.check(ctx, s.p.checks.Add(1))
}
func (s *checkedSession) Resync(provider.Query) error { s.p.resyncs.Add(1); return nil }

func connectionState(t *testing.T, s *Service) *core.ConnectionStatus {
	t.Helper()
	v, err := s.ListTargets(context.Background())
	require.NoError(t, err)
	return v.Groups[0].Targets[0].Connection
}

func awaitConnection(t *testing.T, s *Service, state string) core.ConnectionStatus {
	t.Helper()
	var result *core.ConnectionStatus
	require.Eventually(t, func() bool { result = connectionState(t, s); return result != nil && result.State == state }, 5*time.Second, 5*time.Millisecond)
	return *result
}

func awaitConnectWorkers(t *testing.T, s *Service) {
	t.Helper()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("connection worker did not stop")
	}
}

func TestUISelectionAndRestoreDoNotConnect(t *testing.T) {
	p := newOpenable("a")
	s, em := newService(t, p)
	ctx := UIContext(context.Background())
	require.NoError(t, s.SelectTarget(ctx, "k", "a"))
	for _, service := range []*Service{s, NewService(s.reg, s.store, em, Options{})} {
		if service != s {
			t.Cleanup(service.Close)
		}
		v, err := service.ListTargets(ctx)
		require.NoError(t, err)
		require.Equal(t, &TargetRef{Provider: "k", ID: "a"}, v.Selected)
		require.Nil(t, v.Groups[0].Targets[0].Connection)
		_, err = service.ListKinds(ctx, "k", "a")
		require.True(t, IsCoded(err, CodeGone))
		_, err = service.ListScopes(ctx, "k", "a")
		require.True(t, IsCoded(err, CodeGone))
		_, err = service.OpenView(ctx, OpenViewRequest{Provider: "k", Target: "a", Query: provider.Query{Kind: "pods", Scope: allScopes}})
		require.True(t, IsCoded(err, CodeGone))
	}
	require.Empty(t, p.opened)
}

func TestConnectionRetriesAreBoundedAndKeepSessionCapabilities(t *testing.T) {
	for _, succeeds := range []bool{false, true} {
		t.Run(map[bool]string{false: "exhausted", true: "recovered"}[succeeds], func(t *testing.T) {
			p := &connectionProvider{openable: newOpenable("a"), check: func(_ context.Context, n int64) error {
				if succeeds && n == 3 {
					return nil
				}
				return &provider.Error{Class: provider.ClassUnavailable, Message: "connection refused"}
			}}
			s, _ := newService(t, p)
			start, err := s.ConnectTarget(context.Background(), "k", "a")
			require.NoError(t, err)
			state := "failed"
			if succeeds {
				state = "connected"
			}
			status := awaitConnection(t, s, state)
			require.Equal(t, start.ID, status.ID)
			require.Equal(t, 3, status.Attempt)
			require.EqualValues(t, 3, p.checks.Load())
			awaitConnectWorkers(t, s)
			if succeeds {
				ctx := UIContext(context.Background())
				info, err := s.OpenView(ctx, OpenViewRequest{Provider: "k", Target: "a", Query: provider.Query{Kind: "pods", Scope: allScopes}})
				require.NoError(t, err)
				require.True(t, info.Resync)
				require.NoError(t, s.ResyncView(ctx, info.ViewID))
				require.EqualValues(t, 1, p.resyncs.Load())
			} else {
				require.Equal(t, "connection refused", status.Error)
				for _, sess := range p.opened {
					require.True(t, sess.isClosed())
				}
			}
		})
	}
}

func TestConnectionDoesNotRetryAuthenticationAndAcceptsLimitedPermissions(t *testing.T) {
	for _, class := range []provider.ErrorClass{provider.ClassUnauthorized, provider.ClassInvalid, provider.ClassForbidden} {
		t.Run(string(class), func(t *testing.T) {
			p := &connectionProvider{openable: newOpenable("a"), check: func(context.Context, int64) error { return &provider.Error{Class: class, Message: "restricted"} }}
			s, _ := newService(t, p)
			_, err := s.ConnectTarget(context.Background(), "k", "a")
			require.NoError(t, err)
			state := "failed"
			if class == provider.ClassForbidden {
				state = "connected"
			}
			awaitConnection(t, s, state)
			require.EqualValues(t, 1, p.checks.Load())
		})
	}
}

func TestCancelConnectionStopsRetryWait(t *testing.T) {
	p := &connectionProvider{openable: newOpenable("a"), check: func(context.Context, int64) error {
		return &provider.Error{Class: provider.ClassUnavailable, Message: "offline"}
	}}
	s, _ := newService(t, p)
	start, err := s.ConnectTarget(context.Background(), "k", "a")
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		st := connectionState(t, s)
		return st != nil && st.Phase == "retry_wait" && st.RetryAt > st.AttemptStarted
	}, time.Second, 5*time.Millisecond)
	require.NoError(t, s.CancelConnectTarget(context.Background(), "k", "a", start.ID))
	awaitConnectWorkers(t, s)
	require.Equal(t, "cancelled", connectionState(t, s).State)
	require.EqualValues(t, 1, p.checks.Load())
}

func TestCancelLateConnectionCannotReplaceNewAttempt(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	p := &connectionProvider{openable: newOpenable("a"), check: func(_ context.Context, n int64) error {
		if n == 1 {
			close(entered)
			<-release
		}
		return nil
	}}
	s, _ := newService(t, p)
	first, err := s.ConnectTarget(context.Background(), "k", "a")
	require.NoError(t, err)
	<-entered
	require.NoError(t, s.CancelConnectTarget(context.Background(), "k", "a", first.ID))
	second, err := s.ConnectTarget(context.Background(), "k", "a")
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID)
	status := awaitConnection(t, s, "connected")
	require.Equal(t, second.ID, status.ID)
	close(release)
	awaitConnectWorkers(t, s)
	require.NoError(t, s.CancelConnectTarget(context.Background(), "k", "a", first.ID))
	require.Equal(t, "connected", connectionState(t, s).State)
	require.True(t, p.opened[0].isClosed())
	require.False(t, p.opened[1].isClosed())
}

func TestConfigurationChangeCancelsPendingConnection(t *testing.T) {
	entered := make(chan struct{})
	p := &connectionProvider{openable: newOpenable("a"), check: func(ctx context.Context, _ int64) error { close(entered); <-ctx.Done(); return ctx.Err() }}
	s, _ := newService(t, p)
	_, err := s.ConnectTarget(context.Background(), "k", "a")
	require.NoError(t, err)
	<-entered
	p.setHash("a", "changed")
	s.revalidateSessions(context.Background(), "k")
	awaitConnectWorkers(t, s)
	require.Equal(t, "disconnected", connectionState(t, s).State)
	require.True(t, p.opened[0].isClosed())
}

func TestCancelUIConnectionPreservesAnExistingAgentSession(t *testing.T) {
	p := newOpenable("a")
	s, _ := newService(t, p)
	agent, err := s.AgentCall(UIContext(context.Background()), "k", "a")
	require.NoError(t, err)
	defer agent.Done()
	start, err := s.ConnectTarget(context.Background(), "k", "a")
	require.NoError(t, err)
	require.Equal(t, "connected", start.State)
	require.NoError(t, s.CancelConnectTarget(context.Background(), "k", "a", start.ID))
	require.NoError(t, agent.ctx.Err())
	require.False(t, p.opened[0].isClosed())
	_, err = s.ListKinds(UIContext(context.Background()), "k", "a")
	require.True(t, IsCoded(err, CodeGone))
	require.Len(t, p.opened, 1)
}

func TestCancelJustAdmittedSessionWaitsForItsAgentCall(t *testing.T) {
	p := &connectionProvider{openable: newOpenable("a"), check: func(context.Context, int64) error { return nil }}
	s, _ := newService(t, p)
	start, err := s.ConnectTarget(context.Background(), "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	awaitConnectWorkers(t, s)
	agent, err := s.AgentCall(context.Background(), "k", "a")
	require.NoError(t, err)
	defer agent.Done()
	require.NoError(t, s.CancelConnectTarget(context.Background(), "k", "a", start.ID))
	require.NoError(t, agent.ctx.Err())
	require.False(t, p.opened[0].isClosed())
	agent.Done()
	require.True(t, p.opened[0].isClosed())
}

func (s *checkedSession) Close() {
	if s.p.closeHook != nil {
		s.p.closeHook()
	}
	s.fakeSession.Close()
}

func TestCancelConnectionDoesNotWaitForProviderCleanup(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	entered := make(chan struct{}, 1)
	p := &connectionProvider{openable: newOpenable("a"),
		check:     func(ctx context.Context, _ int64) error { <-ctx.Done(); return ctx.Err() },
		closeHook: func() { entered <- struct{}{}; <-release },
	}
	s, _ := newService(t, p)
	st, err := s.ConnectTarget(t.Context(), "k", "a")
	require.NoError(t, err)
	require.Eventually(t, func() bool { return connectionState(t, s).Phase == "checking" }, time.Second, time.Millisecond)
	done := make(chan error, 1)
	go func() { done <- s.CancelConnectTarget(t.Context(), "k", "a", st.ID) }()
	select {
	case err = <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("cancel waited for slow provider cleanup")
	}
	require.Equal(t, "cancelled", connectionState(t, s).State)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("cancel did not start provider cleanup")
	}
}

func TestDisconnectSelectedUIKeepsAgentCallAndAllowsExplicitReconnect(t *testing.T) {
	p := &connectionProvider{openable: newOpenable("a"), check: func(context.Context, int64) error { return nil }}
	s, _ := newService(t, p)
	require.NoError(t, s.SelectTarget(UIContext(t.Context()), "k", "a"))
	_, err := s.ConnectTarget(UIContext(t.Context()), "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	agent, err := s.AgentCall(t.Context(), "k", "a")
	require.NoError(t, err)
	defer agent.Done()
	require.NoError(t, s.CloseTarget(UIContext(t.Context()), "k", "a"))
	require.NoError(t, agent.Context().Err())
	require.Equal(t, "disconnected", connectionState(t, s).State)
	view, err := s.ListTargets(UIContext(t.Context()))
	require.NoError(t, err)
	require.False(t, view.Groups[0].Targets[0].Open)
	require.Equal(t, &TargetRef{Provider: "k", ID: "a"}, view.Selected)
	st, err := s.ConnectTarget(UIContext(t.Context()), "k", "a")
	require.NoError(t, err)
	require.Equal(t, "connected", st.State)
	agent.Done()
	view, err = s.ListTargets(UIContext(t.Context()))
	require.NoError(t, err)
	require.True(t, view.Groups[0].Targets[0].Open)
}
