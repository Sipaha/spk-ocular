package api

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/store"
	"github.com/stretchr/testify/require"
)

type recoveryProvider struct {
	*openable
	calls   atomic.Int32
	fail    bool
	started chan struct{}
}

func (p *recoveryProvider) Open(ctx context.Context, id string) (provider.Session, error) {
	s, err := p.openable.Open(ctx, id)
	if err != nil {
		return nil, err
	}
	return &recoverySession{fakeSession: s.(*fakeSession), owner: p}, nil
}

type recoverySession struct {
	*fakeSession
	owner *recoveryProvider
}

func (s *recoverySession) CleanupOwnedHelper(ctx context.Context, h core.OwnedHelper, pin func(string) error) (bool, error) {
	s.owner.calls.Add(1)
	if s.owner.started != nil {
		close(s.owner.started)
		<-ctx.Done()
		return false, ctx.Err()
	}
	if s.owner.fail {
		return false, errors.New("unavailable")
	}
	if h.UID == "" {
		if err := pin("server-pod-uid"); err != nil {
			return false, err
		}
	}
	return true, nil
}
func recoveryHelper() core.OwnedHelper {
	return core.OwnedHelper{ID: "nonce", Provider: "k", Target: "a", Namespace: "tools", NamespaceUID: "ns-uid", Name: "ocular-node-shell-test", NodeName: "worker", NodeUID: "node-uid"}
}
func connectRecovery(t *testing.T, s *Service) {
	t.Helper()
	ctx := UIContext(t.Context())
	_, err := s.ListTargets(ctx)
	require.NoError(t, err)
	_, err = s.ConnectTarget(ctx, "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
}

func TestHelperRecoverySurvivesReopenAndNeverReapsActiveOrDisconnectedTargets(t *testing.T) {
	ctx := t.Context()
	p := &recoveryProvider{openable: newOpenable("a")}
	reg, err := provider.NewRegistry(p)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "app.db")
	st, err := store.Open(ctx, path)
	require.NoError(t, err)
	var clock atomic.Int64
	clock.Store(time.Now().UnixMilli())
	now := func() time.Time { return time.UnixMilli(clock.Load()) }
	first := NewService(reg, st, events.NewEmitter(), Options{})
	first.now = now
	connectRecovery(t, first)
	raw, err := first.helperJournal.Begin(ctx, recoveryHelper(), func() {})
	require.NoError(t, err)
	lease := raw.(*helperLease)
	clock.Add((helperLeaseDuration + time.Minute).Milliseconds())
	first.reapOwnedHelpers(ctx)
	require.Zero(t, p.calls.Load(), "a locally active helper survives even a wall-clock jump")
	// Simulate a process crash: heartbeat ends, but neither cleanup nor Finish runs.
	lease.stop()
	<-lease.done
	// A crashed process loses its in-memory owner callbacks. Close only the
	// remaining fixture resources; this is not a graceful helper shutdown.
	first.helperJournal.mu.Lock()
	delete(first.helperJournal.running, lease.id)
	first.helperJournal.mu.Unlock()
	first.Close()
	require.NoError(t, st.Close())
	st, err = store.Open(ctx, path)
	require.NoError(t, err)
	defer func() { require.NoError(t, st.Close()) }()
	second := NewService(reg, st, events.NewEmitter(), Options{})
	second.now = now
	defer second.Close()
	second.reapOwnedHelpers(ctx)
	require.Zero(t, p.calls.Load(), "recovery must not open a disconnected target")
	connectRecovery(t, second)
	second.reapOwnedHelpers(ctx)
	require.EqualValues(t, 1, p.calls.Load())
	rows, err := st.DueHelpers(ctx, "k", "a", clock.Load()+24*time.Hour.Milliseconds())
	require.NoError(t, err)
	require.Empty(t, rows)
}
func TestHelperRecoveryBackoffIsSilentAndNotResetByReconnect(t *testing.T) {
	p := &recoveryProvider{openable: newOpenable("a"), fail: true}
	s, _ := newService(t, p)
	var clock atomic.Int64
	clock.Store(time.Now().UnixMilli())
	s.now = func() time.Time { return time.UnixMilli(clock.Load()) }
	connectRecovery(t, s)
	require.NoError(t, s.store.InsertHelper(t.Context(), store.HelperRecord{Helper: recoveryHelper(), Owner: "crashed", NextTry: clock.Load(), Delay: time.Minute.Milliseconds()}))
	delay := time.Minute.Milliseconds()
	for i := range 16 {
		before := p.calls.Load()
		s.reapOwnedHelpers(t.Context())
		require.Equal(t, before+1, p.calls.Load())
		rows, err := s.store.DueHelpers(t.Context(), "k", "a", clock.Load()+24*time.Hour.Milliseconds())
		require.NoError(t, err)
		require.Len(t, rows, 1)
		r := rows[0]
		require.Equal(t, clock.Load()+delay, r.NextTry)
		s.reapOwnedHelpers(t.Context())
		require.Equal(t, before+1, p.calls.Load(), "a scheduled retry cannot run early")
		if i == 0 {
			require.NoError(t, s.CloseTarget(UIContext(t.Context()), "k", "a"))
			connectRecovery(t, s)
			s.reapOwnedHelpers(t.Context())
			require.Equal(t, before+1, p.calls.Load(), "reconnect does not reset nextTry")
		}
		clock.Store(r.NextTry)
		delay = min(delay*2, 24*time.Hour.Milliseconds())
	}
	require.Equal(t, 24*time.Hour.Milliseconds(), delay)
}

func TestHelperRecoveryCancelsWhenExplicitConnectionCloses(t *testing.T) {
	p := &recoveryProvider{openable: newOpenable("a"), started: make(chan struct{})}
	s, _ := newService(t, p)
	connectRecovery(t, s)
	require.NoError(t, s.store.InsertHelper(t.Context(), store.HelperRecord{Helper: recoveryHelper(), Owner: "crashed", NextTry: s.now().UnixMilli(), Delay: 60000}))
	finished := make(chan struct{})
	go func() { s.reapOwnedHelpers(t.Context()); close(finished) }()
	<-p.started
	require.NoError(t, s.CloseTarget(UIContext(t.Context()), "k", "a"))
	<-finished
	require.EqualValues(t, 1, p.calls.Load())
	rows, err := s.store.DueHelpers(t.Context(), "k", "a", s.now().Add(24*time.Hour).UnixMilli())
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Greater(t, rows[0].NextTry, s.now().UnixMilli())
}

func TestHelperJournalShutdownCancelsOwnersAndWaitsForDurableFinish(t *testing.T) {
	s, _ := newService(t, newOpenable("a"))
	ownerCtx, stop := context.WithCancel(context.Background())
	defer stop()
	raw, err := s.helperJournal.Begin(t.Context(), recoveryHelper(), stop)
	require.NoError(t, err)
	finished := make(chan error, 1)
	go func() {
		<-ownerCtx.Done()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		finished <- raw.Finish(ctx, true)
	}()
	s.Close()
	require.NoError(t, <-finished)
	rows, err := s.store.DueHelpers(t.Context(), "k", "a", time.Now().Add(24*time.Hour).UnixMilli())
	require.NoError(t, err)
	require.Empty(t, rows)
	_, err = s.helperJournal.Begin(t.Context(), recoveryHelper(), func() {})
	require.ErrorContains(t, err, "closing")
}
