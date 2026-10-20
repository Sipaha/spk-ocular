package kubernetes

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/remotecommand"
)

type helperTestClock struct {
	mu     sync.Mutex
	now    time.Time
	timers map[*helperTestTimer]time.Time
}
type helperTestTimer struct {
	clock *helperTestClock
	f     func()
}

func newHelperTestClock() *helperTestClock {
	return &helperTestClock{now: time.Now(), timers: map[*helperTestTimer]time.Time{}}
}
func (c *helperTestClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *helperTestClock) After(d time.Duration, f func()) helperTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &helperTestTimer{clock: c, f: f}
	c.timers[t] = c.now.Add(d)
	return t
}
func (t *helperTestTimer) Stop() bool {
	c := t.clock
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.timers[t]
	delete(c.timers, t)
	return ok
}
func (c *helperTestClock) due(d time.Duration) []func() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	var ready []func()
	for timer, at := range c.timers {
		if !at.After(c.now) {
			delete(c.timers, timer)
			ready = append(ready, timer.f)
		}
	}
	return ready
}
func (c *helperTestClock) Step(d time.Duration) {
	for _, f := range c.due(d) {
		f()
	}
}
func helperClock(s *session) *helperTestClock { return s.conn.helpers.nowClockForTest() }
func (p *nodeHelperPool) nowClockForTest() *helperTestClock {
	p.mu.Lock()
	defer p.mu.Unlock()
	timer := p.after(24*time.Hour, func() {}).(*helperTestTimer)
	timer.Stop()
	return timer.clock
}
func waitHelperCleanup(t *testing.T, s *session) {
	t.Helper()
	p := s.conn.helpers
	p.mu.Lock()
	var done []<-chan struct{}
	for _, e := range p.entries {
		done = append(done, e.done)
	}
	p.mu.Unlock()
	for _, ch := range done {
		select {
		case <-ch:
		case <-time.After(3 * time.Second):
			t.Fatal("helper cleanup did not finish")
		}
	}
}
func finishHelperIdle(t *testing.T, s *session) {
	t.Helper()
	helperClock(s).Step(5 * time.Second)
	waitHelperCleanup(t, s)
}

func filesCommand(t *testing.T, h provider.FilesHandle) {
	t.Helper()
	command, err := h.PrepareCommand(t.Context(), []string{"sh", "-c", "true"})
	require.NoError(t, err)
	_, err = command.Run(t.Context(), provider.Terminal{Raw: true})
	require.NoError(t, err)
	command.Close()
}

func TestNodeHelperSharedConsumersIdleGraceAndWarmAdmission(t *testing.T) {
	s, c := nodeShellSession(t)
	readyShells(c)
	s.conn.newExecutor = func(*url.URL) (remotecommand.Executor, error) {
		return &fakeExecutor{play: func(context.Context, remotecommand.StreamOptions) error { return nil }}, nil
	}
	file, err := s.PrepareFilesAction(t.Context(), nodeShellRun(nodeFilesReview(t, s)))
	require.NoError(t, err)
	filesCommand(t, file)
	warm := nodeShellPlan(t, s)
	require.True(t, warm.Reuse)
	require.Contains(t, text(warm), "no new pod")
	shell, err := s.PrepareTerminalAction(t.Context(), nodeShellRun(warm))
	require.NoError(t, err)
	require.Equal(t, file.Describe().Instance, shell.Describe().Instance)
	_, err = shell.Run(t.Context(), provider.Terminal{})
	require.NoError(t, err)
	require.NoError(t, file.Close())
	require.Equal(t, 1, countShellWrites(c, "create"))
	require.Zero(t, countShellWrites(c, "delete"))
	clock := helperClock(s)
	clock.Step(4 * time.Second)
	require.Zero(t, countShellWrites(c, "delete"))
	another, err := s.PrepareFilesAction(t.Context(), nodeShellRun(nodeFilesReview(t, s)))
	require.NoError(t, err)
	filesCommand(t, another)
	clock.Step(time.Second)
	require.Zero(t, countShellWrites(c, "delete"), "new consumer cancels the old idle deadline")
	require.NoError(t, another.Close())
	clock.Step(4 * time.Second)
	require.Zero(t, countShellWrites(c, "delete"))
	clock.Step(time.Second)
	waitHelperCleanup(t, s)
	require.Equal(t, 1, countShellWrites(c, "delete"))
	_, err = s.PrepareTerminalAction(t.Context(), nodeShellRun(warm))
	require.Error(t, err, "spent/warm admission never creates a replacement")
	cold := nodeShellPlan(t, s)
	require.False(t, cold.Reuse)
	require.Equal(t, 1, countShellWrites(c, "create"))
}
func TestNodeHelperStaleWarmTokenCannotCreateAfterGrace(t *testing.T) {
	s, c := nodeShellSession(t)
	readyShells(c)
	s.conn.newExecutor = func(*url.URL) (remotecommand.Executor, error) {
		return &fakeExecutor{play: func(context.Context, remotecommand.StreamOptions) error { return nil }}, nil
	}
	f, err := s.PrepareFilesAction(t.Context(), nodeShellRun(nodeFilesReview(t, s)))
	require.NoError(t, err)
	filesCommand(t, f)
	warm := nodeShellPlan(t, s)
	require.True(t, warm.Reuse)
	require.NoError(t, f.Close())
	finishHelperIdle(t, s)
	_, err = s.PrepareTerminalAction(t.Context(), nodeShellRun(warm))
	require.Error(t, err)
	require.Equal(t, 1, countShellWrites(c, "create"))
	require.Equal(t, 1, countShellWrites(c, "delete"))
}
func TestNodeHelperStaleTimerCannotDeleteNewConsumer(t *testing.T) {
	s, c := nodeShellSession(t)
	readyShells(c)
	s.conn.newExecutor = func(*url.URL) (remotecommand.Executor, error) {
		return &fakeExecutor{play: func(context.Context, remotecommand.StreamOptions) error { return nil }}, nil
	}
	f, err := s.PrepareFilesAction(t.Context(), nodeShellRun(nodeFilesReview(t, s)))
	require.NoError(t, err)
	filesCommand(t, f)
	warm := nodeFilesReview(t, s)
	require.NoError(t, f.Close())
	callbacks := helperClock(s).due(5 * time.Second)
	require.Len(t, callbacks, 1)
	next, err := s.PrepareFilesAction(t.Context(), nodeShellRun(warm))
	require.NoError(t, err)
	for _, f := range callbacks {
		f()
	}
	filesCommand(t, next)
	require.Zero(t, countShellWrites(c, "delete"))
	require.NoError(t, next.Close())
	finishHelperIdle(t, s)
	require.Equal(t, 1, countShellWrites(c, "delete"))
}
func TestNodeHelperWarmAdmissionRechecksUIDAndOwnership(t *testing.T) {
	for _, mode := range []string{"replacement", "marker", "owner", "stopped"} {
		t.Run(mode, func(t *testing.T) {
			s, c := nodeShellSession(t)
			readyShells(c)
			s.conn.newExecutor = func(*url.URL) (remotecommand.Executor, error) {
				return &fakeExecutor{play: func(context.Context, remotecommand.StreamOptions) error { return nil }}, nil
			}
			f, err := s.PrepareFilesAction(t.Context(), nodeShellRun(nodeFilesReview(t, s)))
			require.NoError(t, err)
			filesCommand(t, f)
			warm := nodeShellPlan(t, s)
			pod, err := c.Resource(podGVR).Namespace("kube-system").Get(t.Context(), f.Describe().Instance, metav1.GetOptions{})
			require.NoError(t, err)
			switch mode {
			case "replacement":
				pod.SetUID("foreign")
			case "marker":
				pod.SetLabels(nil)
			case "owner":
				pod.SetOwnerReferences(nil)
			case "stopped":
				pod.Object["status"] = map[string]any{"phase": "Succeeded", "containerStatuses": []any{map[string]any{"name": "shell", "state": map[string]any{"terminated": map[string]any{"exitCode": int64(0)}}}}}
			}
			_, err = c.Resource(podGVR).Namespace("kube-system").Update(t.Context(), pod, metav1.UpdateOptions{})
			require.NoError(t, err)
			_, err = s.PrepareTerminalAction(t.Context(), nodeShellRun(warm))
			require.Error(t, err)
			require.Equal(t, 1, countShellWrites(c, "create"))
			closeErr := f.Close()
			waitHelperCleanup(t, s)
			if mode == "marker" || mode == "owner" {
				require.Error(t, f.(*nodeFilesHandle).lease.entry.owner.state.cleanupError)
				if closeErr != nil {
					require.ErrorContains(t, closeErr, "cleanup")
				}
				require.Zero(t, countShellWrites(c, "delete"))
			} else {
				require.NoError(t, closeErr)
			}
		})
	}
}
func TestNodeHelperPrototypeEvictionDoesNotCancelItsActiveStream(t *testing.T) {
	s, c := nodeShellSession(t)
	readyShells(c)
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	s.conn.newExecutor = func(*url.URL) (remotecommand.Executor, error) {
		return &fakeExecutor{play: func(ctx context.Context, _ remotecommand.StreamOptions) error {
			close(started)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}}, nil
	}
	proto, err := s.PrepareTerminalAction(t.Context(), nodeShellRun(nodeShellPlan(t, s)))
	require.NoError(t, err)
	run, err := proto.Again()
	require.NoError(t, err)
	go func() { _, err := run.Run(t.Context(), provider.Terminal{}); done <- err }()
	<-started
	proto.Close()
	helperClock(s).Step(time.Minute)
	require.Zero(t, countShellWrites(c, "delete"))
	close(release)
	require.NoError(t, <-done)
	finishHelperIdle(t, s)
	require.Equal(t, 1, countShellWrites(c, "delete"))
}

type pooledJournal struct {
	began, pinned, finished atomic.Int32
	clean                   atomic.Bool
}

func (j *pooledJournal) Begin(context.Context, core.OwnedHelper, context.CancelFunc) (provider.HelperLease, error) {
	j.began.Add(1)
	return j, nil
}
func (j *pooledJournal) PinUID(_ context.Context, uid string) error {
	if uid != "helper-uid" {
		return errors.New("wrong UID")
	}
	j.pinned.Add(1)
	return nil
}
func (j *pooledJournal) Finish(_ context.Context, clean bool) error {
	j.clean.Store(clean)
	j.finished.Add(1)
	return nil
}
func TestNodeHelperColdShellAndFilesShareOwnerLeaseAndIndependentCancellation(t *testing.T) {
	s, c := nodeShellSession(t)
	readyShells(c)
	j := &pooledJournal{}
	f, err := s.PrepareFilesAction(t.Context(), nodeShellRun(nodeFilesReview(t, s)))
	require.NoError(t, err)
	f.(provider.HelperTracked).SetHelperJournal(j)
	shell, err := s.PrepareTerminalAction(t.Context(), nodeShellRun(nodeShellPlan(t, s)))
	require.NoError(t, err)
	shell.(provider.HelperTracked).SetHelperJournal(j)
	shellStarted := make(chan struct{})
	shellDone, fileDone := make(chan error, 1), make(chan error, 1)
	s.conn.newExecutor = func(u *url.URL) (remotecommand.Executor, error) {
		args := u.Query()["command"]
		return &fakeExecutor{play: func(ctx context.Context, _ remotecommand.StreamOptions) error {
			if args[len(args)-1] == "-l" {
				close(shellStarted)
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		}}, nil
	}
	shellCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _, err := shell.Run(shellCtx, provider.Terminal{}); shellDone <- err }()
	command, err := f.PrepareCommand(t.Context(), []string{"sh", "-c", "true"})
	require.NoError(t, err)
	go func() { _, err := command.Run(t.Context(), provider.Terminal{Raw: true}); fileDone <- err }()
	select {
	case <-shellStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("shell did not start")
	}
	require.NoError(t, <-fileDone)
	require.Equal(t, 1, countShellWrites(c, "create"))
	require.EqualValues(t, 1, j.began.Load())
	require.EqualValues(t, 1, j.pinned.Load())
	require.Zero(t, j.finished.Load())
	require.NoError(t, f.Close())
	helperClock(s).Step(time.Minute)
	require.Zero(t, countShellWrites(c, "delete"))
	require.Zero(t, j.finished.Load(), "active shell retains the one durable lease")
	cancel()
	require.Error(t, <-shellDone)
	helperClock(s).Step(4 * time.Second)
	require.Zero(t, countShellWrites(c, "delete"))
	require.Zero(t, j.finished.Load(), "idle grace retains the durable lease")
	helperClock(s).Step(time.Second)
	waitHelperCleanup(t, s)
	require.Equal(t, 1, countShellWrites(c, "delete"))
	require.EqualValues(t, 1, j.finished.Load())
	require.True(t, j.clean.Load())
}
func TestNodeHelperHardLifetimeDoesNotResetForConsumers(t *testing.T) {
	s, c := nodeShellSession(t)
	readyShells(c)
	s.conn.newExecutor = func(*url.URL) (remotecommand.Executor, error) {
		return &fakeExecutor{play: func(context.Context, remotecommand.StreamOptions) error { return nil }}, nil
	}
	f, err := s.PrepareFilesAction(t.Context(), nodeShellRun(nodeFilesReview(t, s)))
	require.NoError(t, err)
	filesCommand(t, f)
	helperClock(s).Step(time.Hour - time.Second)
	require.Zero(t, countShellWrites(c, "delete"))
	helperClock(s).Step(time.Second)
	waitHelperCleanup(t, s)
	require.Equal(t, 1, countShellWrites(c, "delete"))
	command, err := f.PrepareCommand(t.Context(), []string{"sh", "-c", "true"})
	require.NoError(t, err)
	_, err = command.Run(t.Context(), provider.Terminal{Raw: true})
	require.Error(t, err)
	require.Equal(t, 1, countShellWrites(c, "create"))
	require.NoError(t, f.Close())
}
func TestNodeHelperFailedCleanupBlocksAnotherPodUntilConfirmedAbsence(t *testing.T) {
	s, c := nodeShellSession(t)
	readyShells(c)
	var deny atomic.Bool
	deny.Store(true)
	c.PrependReactor("delete", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		if deny.Load() {
			return true, nil, errors.New("delete denied")
		}
		return false, nil, nil
	})
	s.conn.newExecutor = func(*url.URL) (remotecommand.Executor, error) {
		return &fakeExecutor{play: func(context.Context, remotecommand.StreamOptions) error { return nil }}, nil
	}
	f, err := s.PrepareFilesAction(t.Context(), nodeShellRun(nodeFilesReview(t, s)))
	require.NoError(t, err)
	filesCommand(t, f)
	name := f.Describe().Instance
	require.NoError(t, f.Close())
	finishHelperIdle(t, s)
	plan, err := s.PrepareAction(t.Context(), nodeRef("worker", "node-uid"), "node-shell", core.ActionParams{})
	require.NoError(t, err)
	require.NotNil(t, plan.Unavailable)
	require.Empty(t, plan.Expect)
	require.Equal(t, 1, countShellWrites(c, "create"))
	require.NoError(t, c.Tracker().Delete(podGVR, "kube-system", name))
	deny.Store(false)
	next, err := s.PrepareFilesAction(t.Context(), nodeShellRun(nodeFilesReview(t, s)))
	require.NoError(t, err)
	filesCommand(t, next)
	require.Equal(t, 2, countShellWrites(c, "create"))
	require.NoError(t, next.Close())
	finishHelperIdle(t, s)
}

func TestNodeHelperEndedStreamDoesNotRetainHelperThroughItsRememberedPrototype(t *testing.T) {
	s, c := nodeShellSession(t)
	readyShells(c)
	s.conn.newExecutor = func(*url.URL) (remotecommand.Executor, error) {
		return &fakeExecutor{play: func(context.Context, remotecommand.StreamOptions) error { return nil }}, nil
	}
	proto, err := s.PrepareTerminalAction(t.Context(), nodeShellRun(nodeShellPlan(t, s)))
	require.NoError(t, err)
	run, err := proto.Again()
	require.NoError(t, err)
	_, err = run.Run(t.Context(), provider.Terminal{})
	require.NoError(t, err)
	helperClock(s).Step(4 * time.Second)
	require.Zero(t, countShellWrites(c, "delete"))
	helperClock(s).Step(time.Second)
	waitHelperCleanup(t, s)
	require.Equal(t, 1, countShellWrites(c, "delete"))
	proto.Close()
}

func TestNodeHelperMissingPodRetiresPoolAndRequiresFreshLaunchReview(t *testing.T) {
	s, c := nodeShellSession(t)
	readyShells(c)
	s.conn.newExecutor = func(*url.URL) (remotecommand.Executor, error) {
		return &fakeExecutor{play: func(context.Context, remotecommand.StreamOptions) error { return nil }}, nil
	}
	f, err := s.PrepareFilesAction(t.Context(), nodeShellRun(nodeFilesReview(t, s)))
	require.NoError(t, err)
	filesCommand(t, f)
	require.NoError(t, c.Tracker().Delete(podGVR, "kube-system", f.Describe().Instance))
	stale, err := s.PrepareAction(t.Context(), nodeRef("worker", "node-uid"), "node-files", core.ActionParams{})
	require.NoError(t, err)
	require.NotNil(t, stale.Unavailable)
	require.Empty(t, stale.Expect)
	waitHelperCleanup(t, s)
	fresh := nodeFilesReview(t, s)
	require.False(t, fresh.Reuse)
	require.Equal(t, 1, countShellWrites(c, "create"))
	require.NoError(t, f.Close())
	next, err := s.PrepareFilesAction(t.Context(), nodeShellRun(fresh))
	require.NoError(t, err)
	filesCommand(t, next)
	require.Equal(t, 2, countShellWrites(c, "create"))
	require.NoError(t, next.Close())
	finishHelperIdle(t, s)
}

type cancelingPoolJournal struct {
	pooledJournal
	stops chan context.CancelFunc
}

func (j *cancelingPoolJournal) Begin(_ context.Context, _ core.OwnedHelper, stop context.CancelFunc) (provider.HelperLease, error) {
	j.began.Add(1)
	j.stops <- stop
	return &j.pooledJournal, nil
}
func TestNodeHelperDurableOwnerCancellationStopsConsumersBeforeCleanupCompletes(t *testing.T) {
	s, c := nodeShellSession(t)
	readyShells(c)
	j := &cancelingPoolJournal{stops: make(chan context.CancelFunc, 1)}
	releaseDelete := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-releaseDelete:
		default:
			close(releaseDelete)
		}
	})
	c.PrependReactor("delete", "pods", func(k8stesting.Action) (bool, runtime.Object, error) { <-releaseDelete; return false, nil, nil })
	started, done := make(chan struct{}), make(chan error, 1)
	s.conn.newExecutor = func(*url.URL) (remotecommand.Executor, error) {
		return &fakeExecutor{play: func(ctx context.Context, _ remotecommand.StreamOptions) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}}, nil
	}
	shell, err := s.PrepareTerminalAction(t.Context(), nodeShellRun(nodeShellPlan(t, s)))
	require.NoError(t, err)
	shell.(provider.HelperTracked).SetHelperJournal(j)
	go func() { _, err := shell.Run(t.Context(), provider.Terminal{}); done <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("shell did not start")
	}
	stop := <-j.stops
	stop()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("owner cancellation did not stop the active command before cleanup")
	}
	require.Zero(t, j.finished.Load())
	close(releaseDelete)
	waitHelperCleanup(t, s)
	require.EqualValues(t, 1, j.finished.Load())
}

func TestNodeHelperRepeatedCommandPreservesStartupFailureWithoutCreateRetry(t *testing.T) {
	s, c := nodeShellSession(t)
	c.PrependReactor("create", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(podGVR.GroupResource(), "helper", errors.New("creation denied"))
	})
	f, err := s.PrepareFilesAction(t.Context(), nodeShellRun(nodeFilesReview(t, s)))
	require.NoError(t, err)
	for range 2 {
		command, err := f.PrepareCommand(t.Context(), []string{"sh", "-c", "true"})
		require.NoError(t, err)
		_, err = command.Run(t.Context(), provider.Terminal{Raw: true})
		require.ErrorContains(t, err, "creation denied")
		var pe *provider.Error
		require.ErrorAs(t, err, &pe)
		require.Equal(t, provider.ClassForbidden, pe.Class)
	}
	require.Equal(t, 1, countShellWrites(c, "create"))
	require.NoError(t, f.Close())
}
