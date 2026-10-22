package krr

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type testExecution func(context.Context) ([]byte, error)

func (f testExecution) Execute(ctx context.Context) ([]byte, error) { return f(ctx) }
func awaitRun(t *testing.T, m *Manager, id string) Run {
	t.Helper()
	var out Run
	require.Eventually(t, func() bool {
		for _, r := range m.List() {
			if r.ID == id && r.State != "running" && r.State != "cancelling" {
				out = r
				return true
			}
		}
		return false
	}, time.Second, time.Millisecond)
	return out
}
func TestRunOwnershipCancellationAndReportRetention(t *testing.T) {
	reports := NewRegistry(time.Now)
	m := NewManager(reports, time.Now)
	defer m.Close(t.Context())
	release := make(chan struct{})
	started := make(chan struct{})
	run, e := m.Start(context.Background(), testExecution(func(ctx context.Context) ([]byte, error) {
		close(started)
		select {
		case <-release:
			return fixture(t), ErrCleanup
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}))
	require.NoError(t, e)
	<-started
	// Independent reads do not own or stop the backend execution.
	require.Equal(t, "running", m.List()[0].State)
	close(release)
	got := awaitRun(t, m, run.ID)
	require.Equal(t, "failed", got.State)
	require.True(t, got.CleanupPending)
	require.NotEmpty(t, got.ReportID)
	stored, e := reports.Get(got.ReportID)
	require.NoError(t, e)
	require.Equal(t, "job", stored.Summary.Origin)
	second, e := m.Start(context.Background(), testExecution(func(ctx context.Context) ([]byte, error) { <-ctx.Done(); return nil, ctx.Err() }))
	require.NoError(t, e)
	require.NoError(t, m.Cancel(second.ID))
	require.Equal(t, "cancelled", awaitRun(t, m, second.ID).State)
	require.NoError(t, m.Cancel(second.ID))
	require.ErrorIs(t, m.Cancel("missing"), ErrGone)
	require.Len(t, reports.List(), 1)
}
func TestRunBudgetsReserveResultAndShutdown(t *testing.T) {
	reports := NewRegistry(time.Now)
	m := NewManager(reports, time.Now)
	var wg sync.WaitGroup
	wg.Add(2)
	execution := testExecution(func(ctx context.Context) ([]byte, error) { wg.Done(); <-ctx.Done(); return nil, ctx.Err() })
	for range 2 {
		_, e := m.Start(context.Background(), execution)
		require.NoError(t, e)
	}
	wg.Wait()
	_, e := m.Start(context.Background(), execution)
	require.ErrorIs(t, e, ErrLimit)
	// Two pre-reserved 1MiB result budgets survive concurrent manual imports.
	for range 6 {
		_, e = reports.Import(t.Context(), fixture(t), "1.31.0")
		require.NoError(t, e)
	}
	_, e = reports.Import(t.Context(), fixture(t), "1.31.0")
	require.ErrorIs(t, e, ErrLimit)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	m.Close(ctx)
	for _, r := range m.List() {
		require.Equal(t, "cancelled", r.State)
	}
	_, e = m.Start(context.Background(), execution)
	require.ErrorIs(t, e, ErrLimit)
	reports.mu.Lock()
	require.Empty(t, reports.reserved)
	reports.mu.Unlock()
}
func TestRunConnectionCancellationAndMissingOutput(t *testing.T) {
	reports := NewRegistry(time.Now)
	m := NewManager(reports, time.Now)
	defer m.Close(t.Context())
	ctx, cancel := context.WithCancel(context.Background())
	r, e := m.Start(ctx, testExecution(func(ctx context.Context) ([]byte, error) { <-ctx.Done(); return nil, errors.New("PRIVATE-untrusted") }))
	require.NoError(t, e)
	cancel()
	got := awaitRun(t, m, r.ID)
	require.Equal(t, "cancelled", got.State)
	r, e = m.Start(context.Background(), testExecution(func(context.Context) ([]byte, error) { return nil, nil }))
	require.NoError(t, e)
	require.Equal(t, "failed", awaitRun(t, m, r.ID).State)
}
func TestRunOptionsExplicitScopeAndNoCredentialSources(t *testing.T) {
	base := RunOptions{Namespace: "tools", ServiceAccount: "reader", Source: "https://metrics.invalid/base", SingleCluster: true, ReviewedAccount: true, NoSourceAuth: true}
	require.True(t, base.Valid())
	for _, source := range []string{"file:///etc/token", "https://user:secret@metrics.invalid", "https://metrics.invalid?token=secret", "https://metrics.invalid/#token", "http://", "http://metrics.invalid/\n"} {
		o := base
		o.Source = source
		require.False(t, o.Valid(), source)
	}
	o := base
	o.SingleCluster = false
	require.False(t, o.Valid())
	o.Label = "cluster"
	o.Cluster = "production"
	require.True(t, o.Valid())
	o.Cluster = "x\" or vector(1)"
	require.False(t, o.Valid())
	o = base
	o.NoSourceAuth = false
	require.False(t, o.Valid())
	o = base
	o.ReviewedAccount = false
	require.False(t, o.Valid())
}

func TestRunSummaryEvictionDoesNotBlockAfterReportForget(t *testing.T) {
	reports := NewRegistry(time.Now)
	m := NewManager(reports, time.Now)
	defer m.Close(t.Context())
	data := fixture(t)
	var first string
	for i := 0; i < 10; i++ {
		r, e := m.Start(context.Background(), testExecution(func(context.Context) ([]byte, error) { return data, nil }))
		require.NoError(t, e)
		if i == 0 {
			first = r.ID
		}
		done := awaitRun(t, m, r.ID)
		require.Equal(t, "succeeded", done.State)
		require.NoError(t, reports.Forget(done.ReportID))
	}
	require.Len(t, m.List(), 8)
	for _, r := range m.List() {
		require.NotEqual(t, first, r.ID)
	}
	require.Empty(t, reports.List())
}
