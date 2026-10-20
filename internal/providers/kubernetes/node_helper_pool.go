package kubernetes

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

const nodeHelperIdle = 5 * time.Second
const nodeHelperLifetime = time.Hour
const maxNodeHelpers = 128

type helperTimer interface{ Stop() bool }

// The connection snapshot, rather than a view/session, owns its reviewed helpers.
// A target/configuration replacement gets a separate pool and cannot inherit one.
type nodeHelperPool struct {
	mu      sync.Mutex
	entries map[string]*nodeHelperEntry
	now     func() time.Time
	after   func(time.Duration, func()) helperTimer
	closed  bool
}
type nodeHelperEntry struct {
	pool             *nodeHelperPool
	owner            *nodeShellHandle
	refs             int
	started, closing bool
	ready, done      chan struct{}
	ctx              context.Context
	cancel           context.CancelFunc
	expires          time.Time
	idle, hard       helperTimer
	idleGeneration   uint64
	exec             *execHandle
	err              error
	listeners        map[*nodeHelperLease]func(core.Message)
}
type nodeHelperLease struct {
	entry  *nodeHelperEntry
	grant  nodeShellGrant
	now    func() time.Time
	ctx    context.Context
	cancel context.CancelFunc
	closed atomic.Bool
	initMu sync.Mutex
	begun  bool
}

func (p *nodeHelperPool) initLocked() {
	if p.entries == nil {
		p.entries = map[string]*nodeHelperEntry{}
	}
	if p.now == nil {
		p.now = time.Now
	}
	if p.after == nil {
		p.after = func(d time.Duration, f func()) helperTimer { return time.AfterFunc(d, f) }
	}
}
func helperSameConfig(a, b nodeShellGrant) bool {
	return a.Namespace == b.Namespace && a.NamespaceUID == b.NamespaceUID && a.Policy == b.Policy && a.Image == b.Image
}
func helperGone() error { return provider.Said(provider.ClassGone, msg("nodeShell.changed")) }
func (p *nodeHelperPool) reserve(h *nodeShellHandle) (*nodeHelperLease, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.initLocked()
	if p.closed {
		return nil, helperGone()
	}
	e := p.entries[h.g.Ref.UID]
	if h.g.Shared != "" && (e == nil || e.owner.g.Nonce != h.g.Shared || e.exec == nil) {
		return nil, helperGone()
	}
	if e != nil {
		if e.closing {
			return nil, provider.Said(provider.ClassConflict, msg("nodeShell.closing", "namespace", e.owner.g.Namespace, "name", e.owner.g.Name))
		}
		if !helperSameConfig(e.owner.g, h.g) {
			return nil, provider.Said(provider.ClassConflict, msg("nodeShell.sharedConfig", "namespace", e.owner.g.Namespace, "image", e.owner.g.Image))
		}
		if e.started && (e.ctx.Err() != nil || !p.now().Before(e.expires)) {
			return nil, helperGone()
		}
	} else {
		if len(p.entries) >= maxNodeHelpers {
			return nil, provider.Said(provider.ClassUnavailable, msg("nodeShell.helperLimit"))
		}
		e = &nodeHelperEntry{pool: p, owner: h, ready: make(chan struct{}), done: make(chan struct{}), listeners: map[*nodeHelperLease]func(core.Message){}}
		p.entries[h.g.Ref.UID] = e
	}
	e.refs++
	e.idleGeneration++
	if e.idle != nil {
		e.idle.Stop()
		e.idle = nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &nodeHelperLease{entry: e, grant: h.g, now: h.now, ctx: ctx, cancel: cancel}, nil
}
func (p *nodeHelperPool) acquire(ctx context.Context, h *nodeShellHandle) (*nodeHelperLease, error) {
	l, err := p.reserve(h)
	if err != nil {
		return nil, err
	}
	if h.g.Shared != "" {
		p.mu.Lock()
		exec := l.entry.exec
		p.mu.Unlock()
		if exec == nil {
			l.close()
			return nil, helperGone()
		}
		if err = l.entry.validate(ctx, exec); err != nil {
			l.close()
			return nil, err
		}
	}
	return l, nil
}

func (l *nodeHelperLease) describe() core.LiveTarget { return l.entry.owner.Describe() }
func (l *nodeHelperLease) setJournal(j provider.HelperJournal) {
	p := l.entry.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	if !l.entry.started && l.entry.owner.journal == nil {
		l.entry.owner.journal = j
	}
}
func (l *nodeHelperLease) close() {
	if !l.closed.CompareAndSwap(false, true) {
		return
	}
	l.cancel()
	e := l.entry
	p := e.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(e.listeners, l)
	e.refs--
	if e.refs != 0 || e.closing {
		return
	}
	if !e.started {
		e.closing = true
		e.err = helperGone()
		delete(p.entries, e.owner.g.Ref.UID)
		close(e.ready)
		close(e.done)
		return
	}
	e.idleGeneration++
	generation := e.idleGeneration
	e.idle = p.after(nodeHelperIdle, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if e.refs == 0 && !e.closing && e.idleGeneration == generation {
			e.closing = true
			e.cancel()
		}
	})
}
func (e *nodeHelperEntry) emit(m core.Message) {
	p := e.pool
	p.mu.Lock()
	listeners := make([]func(core.Message), 0, len(e.listeners))
	for _, f := range e.listeners {
		listeners = append(listeners, f)
	}
	p.mu.Unlock()
	for _, f := range listeners {
		f(m)
	}
}
func (l *nodeHelperLease) ready(ctx context.Context, notice func(core.Message)) (*execHandle, context.Context, error) {
	l.initMu.Lock()
	if !l.begun {
		if !l.now().Before(time.UnixMilli(l.grant.Exp)) {
			l.initMu.Unlock()
			return nil, nil, provider.Said(provider.ClassConflict, msg("run.expired"))
		}
		l.begun = true
	}
	l.initMu.Unlock()
	e := l.entry
	p := e.pool
	p.mu.Lock()
	if !l.closed.Load() && e.err != nil {
		p.mu.Unlock()
		return nil, nil, e.err
	}
	if l.closed.Load() || e.closing || p.entries[e.owner.g.Ref.UID] != e {
		p.mu.Unlock()
		return nil, nil, helperGone()
	}
	if notice != nil {
		e.listeners[l] = notice
	}
	if !e.started {
		e.started = true
		e.expires = p.now().Add(nodeHelperLifetime)
		e.ctx, e.cancel = context.WithCancel(context.Background())
		e.hard = p.after(nodeHelperLifetime, e.cancel)
		go e.run()
	}
	ready := e.ready
	p.mu.Unlock()
	select {
	case <-ready:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-l.ctx.Done():
		return nil, nil, helperGone()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if e.err != nil {
		return nil, nil, e.err
	}
	if l.closed.Load() || e.closing || e.exec == nil || e.ctx.Err() != nil {
		return nil, nil, helperGone()
	}
	return e.exec, e.ctx, nil
}
func (e *nodeHelperEntry) run() {
	_, err := e.owner.run(e.ctx, provider.Terminal{Notice: e.emit}, func(ctx context.Context, h *execHandle) (provider.ExitStatus, error) {
		p := e.pool
		p.mu.Lock()
		e.exec = h
		e.ctx = ctx // includes cancellation by the durable owner lease
		close(e.ready)
		p.mu.Unlock()
		<-ctx.Done()
		return provider.ExitStatus{Known: true}, nil
	})
	p := e.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	e.closing = true
	e.err = err
	if e.exec == nil {
		close(e.ready)
	}
	if e.idle != nil {
		e.idle.Stop()
	}
	e.hard.Stop()
	e.cancel()
	// Failed/ambiguous deletion keeps a tombstone until a read proves the old
	// helper absent/replaced. A fresh launch cannot overlap an unfinished one.
	if e.owner.state.cleanupError == nil && p.entries[e.owner.g.Ref.UID] == e {
		delete(p.entries, e.owner.g.Ref.UID)
	}
	close(e.done)
}
func (l *nodeHelperLease) command(ctx context.Context, argv []string, t provider.Terminal) (provider.ExitStatus, error) {
	if err := ctx.Err(); err != nil {
		return provider.ExitStatus{}, err
	}
	e, lifetime, err := l.ready(ctx, t.Notice)
	if err != nil {
		return provider.ExitStatus{}, err
	}
	defer func() { p := l.entry.pool; p.mu.Lock(); delete(l.entry.listeners, l); p.mu.Unlock() }()
	runctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(l.ctx, cancel)
	defer stop()
	stopPool := context.AfterFunc(lifetime, cancel)
	defer stopPool()
	if err = l.entry.validate(runctx, e); err != nil {
		return provider.ExitStatus{}, err
	}
	command := *e
	if argv != nil {
		args := append([]string(nil), argv...)
		if len(args) > 0 && args[0] == "sh" {
			args[0] = "/bin/sh"
		}
		command.argv = append([]string{"nsenter", "-t", "1", "-m", "-u", "-i", "-n", "-p", "-r", "-w", "--"}, args...)
	}
	return command.Run(runctx, t)
}
func (e *nodeHelperEntry) validate(ctx context.Context, h *execHandle) (err error) {
	defer func() {
		var pe *provider.Error
		if errors.As(err, &pe) && (pe.Class == provider.ClassGone || pe.Class == provider.ClassNotFound || pe.Class == provider.ClassConflict || pe.Class == provider.ClassInvalid) {
			p := e.pool
			p.mu.Lock()
			if !e.closing {
				e.closing = true
				e.cancel()
			}
			p.mu.Unlock()
		}
	}()
	if err := validateNodeShell(ctx, h.conn, e.owner.g); err != nil {
		return err
	}
	p, err := h.pinnedPod(ctx)
	if err != nil {
		return err
	}
	if !e.owner.owns(p) {
		return helperGone()
	}
	return channelRunnable(execInstance(p), h.container)
}

// reusable only reads. Issuing a review does not retain a helper or reset idle time.
func (p *nodeHelperPool) reusable(ctx context.Context, ref core.Ref) (*nodeHelperEntry, error) {
	p.mu.Lock()
	e := p.entries[ref.UID]
	if e == nil {
		p.mu.Unlock()
		return nil, nil
	}
	closing := e.closing
	h := e.exec
	done := false
	select {
	case <-e.done:
		done = true
	default:
	}
	p.mu.Unlock()
	if closing {
		if done {
			pod, err := e.owner.conn.getPod(ctx, e.owner.g.Namespace, e.owner.g.Name)
			if apierrors.IsNotFound(err) || providerNotFound(err) || (err == nil && e.owner.state.helperUID != "" && pod.GetUID() != e.owner.state.helperUID) {
				p.mu.Lock()
				if p.entries[ref.UID] == e {
					delete(p.entries, ref.UID)
				}
				p.mu.Unlock()
				return nil, nil
			}
		}
		return nil, provider.Said(provider.ClassConflict, msg("nodeShell.closing", "namespace", e.owner.g.Namespace, "name", e.owner.g.Name))
	}
	if h == nil {
		return nil, nil
	}
	if err := e.validate(ctx, h); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if e.closing || e.ctx.Err() != nil || !p.now().Before(e.expires) {
		return nil, helperGone()
	}
	return e, nil
}
func providerNotFound(err error) bool {
	var e *provider.Error
	return errors.As(err, &e) && e.Class == provider.ClassNotFound
}

// close is used for explicit owner shutdown; ordinary session closure does not
// interrupt connection snapshots still retained by live consumers.
func (p *nodeHelperPool) close(ctx context.Context) {
	p.mu.Lock()
	p.closed = true
	entries := make([]*nodeHelperEntry, 0, len(p.entries))
	for _, e := range p.entries {
		entries = append(entries, e)
		if e.started {
			e.closing = true
			e.cancel()
		} else if !e.closing {
			e.closing = true
			e.err = helperGone()
			close(e.ready)
			close(e.done)
		}
	}
	p.mu.Unlock()
	for _, e := range entries {
		select {
		case <-e.done:
		case <-ctx.Done():
			return
		}
	}
}

// A one-shot prototype transfers its reservation to the active stream. Evicting
// the prototype cannot cancel that stream, and an ended stream cannot leave an
// otherwise unused helper retained merely by an ended tab.
type sharedNodeShellHandle struct {
	lease   *nodeHelperLease
	started *atomic.Bool
}

func (h *sharedNodeShellHandle) Describe() core.LiveTarget                 { return h.lease.describe() }
func (h *sharedNodeShellHandle) SetHelperJournal(j provider.HelperJournal) { h.lease.setJournal(j) }
func (h *sharedNodeShellHandle) Close()                                    { h.lease.close() }
func (h *sharedNodeShellHandle) Again() (provider.ExecHandle, error) {
	if h.started.Load() {
		return nil, helperGone()
	}
	lease, err := h.lease.fork()
	if err != nil {
		return nil, err
	}
	h.lease.close() // transfer the reservation; only the stream now consumes it
	return &sharedNodeShellHandle{lease: lease, started: h.started}, nil
}
func (h *sharedNodeShellHandle) Run(ctx context.Context, t provider.Terminal) (provider.ExitStatus, error) {
	if !h.started.CompareAndSwap(false, true) {
		return provider.ExitStatus{}, helperGone()
	}
	defer h.Close()
	return h.lease.command(ctx, nil, t)
}
func (h *sharedNodeShellHandle) WaitCleanup(context.Context) error {
	select {
	case <-h.lease.entry.done:
		return h.lease.entry.owner.state.cleanupError
	default:
		return nil
	}
}
func (l *nodeHelperLease) fork() (*nodeHelperLease, error) {
	e := l.entry
	p := e.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	if l.closed.Load() || e.closing || p.closed {
		return nil, helperGone()
	}
	e.refs++
	e.idleGeneration++
	if e.idle != nil {
		e.idle.Stop()
		e.idle = nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &nodeHelperLease{entry: e, grant: l.grant, now: l.now, ctx: ctx, cancel: cancel}, nil
}
