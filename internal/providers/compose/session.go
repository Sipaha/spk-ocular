package compose

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/compose/engine"
)

// waitTimeout bounds how long Scopes and Get wait for a feed's first
// snapshot.
const waitTimeout = 20 * time.Second

var (
	_ provider.Opener   = (*Provider)(nil)
	_ provider.Session  = (*session)(nil)
	_ provider.Resyncer = (*session)(nil)
)

// Open builds a session for a context from the latest Discover. No
// network: the Engine is reached by the first view, Scopes or Get.
func (p *Provider) Open(_ context.Context, target string) (provider.Session, error) {
	c, ok := p.contextByID(target)
	if !ok {
		return nil, &provider.Error{Class: provider.ClassNotFound, Message: fmt.Sprintf("no Docker context %q", target)}
	}
	if c.TLSError != "" {
		return nil, &provider.Error{Class: provider.ClassInvalid, Message: "the context's TLS files cannot be used: " + c.TLSError}
	}
	cfg := engine.Config{Host: c.Host}
	if c.TLS != nil {
		cfg.TLS = &engine.TLSConfig{CA: c.TLS.CA, Cert: c.TLS.Cert, Key: c.TLS.Key, SkipVerify: c.TLS.SkipVerify}
	}
	cl, err := engine.New(cfg)
	if err != nil {
		return nil, providerError(err)
	}
	s := newSession(target, c.Hash, cl)
	s.title = c.Name
	s.logSlots = p.logSlots
	return s, nil
}

// providerError turns an Engine error into the provider's.
func providerError(err error) error {
	var pe *provider.Error
	if errors.As(err, &pe) {
		return err
	}
	var ee *engine.Error
	if errors.As(err, &ee) {
		return &provider.Error{Class: ee.Class, Message: ee.Message}
	}
	return &provider.Error{Class: engine.ClassOf(err), Message: err.Error()}
}

// projections are the pure functions the session shows its feeds with
// (tests replace them to observe feeds alone). Refs come without the
// target; the session stamps it.
type projections struct {
	rows     func(q provider.Query, w *World, now time.Time) (rows []core.Row, next time.Time)
	resource func(ref core.Ref, w *World, now time.Time) (*core.Resource, error)
	scopes   func(w *World) []core.Scope
}

var defaultProjections = projections{
	rows: func(q provider.Query, w *World, now time.Time) ([]core.Row, time.Time) {
		return projectRows(w, q, now)
	},
	resource: func(ref core.Ref, w *World, now time.Time) (*core.Resource, error) {
		return resourceOf(w, ref, now)
	},
	scopes: scopesOf,
}

type session struct {
	target string
	title  string // the context's name
	hash   string
	cl     *engine.Client
	now    func() time.Time
	proj   projections

	ctx    context.Context
	cancel context.CancelFunc
	pool   chan struct{} // the inspect pool, all feeds together
	// statsSlots: the stats requests at once, all metrics requests together.
	statsSlots chan struct{}
	// logSlots: the provider's log request slots (nil: unlimited).
	logSlots chan struct{}

	grace      time.Duration
	staleAfter time.Duration
	maxDirty   int
	// maxUnresolved: see the constant.
	maxUnresolved int
	backoff       []time.Duration

	mu     sync.Mutex
	osType string // the daemon's, once asked (metrics.go)
	feeds  [feedCount]*feed
	timer  *time.Timer
	closed bool
}

func newSession(target, hash string, cl *engine.Client) *session {
	ctx, cancel := context.WithCancel(context.Background())
	return &session{
		target: target, hash: hash, cl: cl, now: time.Now, proj: defaultProjections,
		ctx: ctx, cancel: cancel, pool: make(chan struct{}, inspectPool), statsSlots: make(chan struct{}, statsPool),
		grace: feedGrace, staleAfter: staleAfter, maxDirty: maxDirty, maxUnresolved: maxUnresolved, backoff: backoffSteps,
	}
}

func (s *session) ConfigHash() string           { return s.hash }
func (s *session) Kinds() []core.KindDescriptor { return kindDescriptors() }
func (s *session) ScopeKind() string            { return KindProjects }

func (s *session) Close() {
	s.mu.Lock()
	s.closed = true
	if s.timer != nil {
		s.timer.Stop()
	}
	for i, f := range s.feeds {
		if f != nil {
			f.stop()
			s.feeds[i] = nil
		}
	}
	s.mu.Unlock()
	s.cancel()
	s.cl.Close()
}

// feedsOf is what a consumer of a kind depends on (kindFeeds: the plan's
// table).
func feedsOf(kind string) ([]Feed, bool) {
	fs := kindFeeds(kind)
	return fs, fs != nil
}

// acquire leases the feeds, starting those not running.
func (s *session) acquire(kinds []Feed) ([]*feed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, &provider.Error{Class: provider.ClassGone, Message: "the session was closed"}
	}
	out := make([]*feed, 0, len(kinds))
	for _, k := range kinds {
		f := s.feeds[k]
		if f == nil {
			f = newFeed(s, k)
			s.feeds[k] = f
			go f.run()
		}
		f.mu.Lock()
		f.leases++
		f.idleAt = time.Time{}
		f.mu.Unlock()
		out = append(out, f)
	}
	return out, nil
}

// release ends leases; a feed nobody leases stops after the grace.
func (s *session) release(fs []*feed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for _, f := range fs {
		f.mu.Lock()
		f.leases--
		if f.leases == 0 {
			f.idleAt = now
		}
		f.mu.Unlock()
	}
	s.evictLocked()
}

func (s *session) evictLocked() {
	if s.closed {
		return
	}
	now := s.now()
	var soonest time.Time
	for i, f := range s.feeds {
		if f == nil {
			continue
		}
		f.mu.Lock()
		idle, at := f.leases == 0, f.idleAt
		f.mu.Unlock()
		if !idle {
			continue
		}
		if now.Sub(at) >= s.grace {
			f.stop()
			s.feeds[i] = nil
			continue
		}
		if end := at.Add(s.grace); soonest.IsZero() || end.Before(soonest) {
			soonest = end
		}
	}
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	if !soonest.IsZero() {
		s.timer = time.AfterFunc(soonest.Sub(now), func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.evictLocked()
		})
	}
}

// world is one reading of the feeds for projections.
func worldOf(fs []*feed) *World {
	w := &World{}
	for _, f := range fs {
		objs := f.objects()
		w.Has[f.kind] = true
		switch f.kind {
		case FeedContainers:
			w.Containers = make(map[string]*engine.ContainerInspect, len(objs))
			for k, v := range objs {
				w.Containers[k] = v.(*engine.ContainerInspect)
			}
		case FeedNetworks:
			w.Networks = make(map[string]*engine.Network, len(objs))
			for k, v := range objs {
				w.Networks[k] = v.(*engine.Network)
			}
		case FeedVolumes:
			w.Volumes = make(map[string]*engine.Volume, len(objs))
			for k, v := range objs {
				w.Volumes[k] = v.(*engine.Volume)
			}
		case FeedImages:
			w.Images = make(map[string]*engine.ImageInspect, len(objs))
			for k, v := range objs {
				w.Images[k] = v.(*engine.ImageInspect)
			}
		}
	}
	return w
}

// waitAll waits for every feed's first snapshot.
func waitAll(ctx context.Context, fs []*feed) error {
	for _, f := range fs {
		if err := f.wait(ctx); err != nil {
			return providerError(err)
		}
	}
	return nil
}

// Scopes lists the Compose projects: one reading of the containers,
// networks and volumes feeds (kept warm for the projects view).
func (s *session) Scopes(ctx context.Context) ([]core.Scope, error) {
	fs, err := s.acquire([]Feed{FeedContainers, FeedNetworks, FeedVolumes})
	if err != nil {
		return nil, err
	}
	defer s.release(fs)
	ctx, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()
	if err := waitAll(ctx, fs); err != nil {
		return nil, err
	}
	return s.proj.scopes(worldOf(fs)), nil
}

// Get reads the object fresh (details show the state "at reading") over
// the feeds' reading of the rest; a fresh answer the feed has not seen
// makes the feed read the object again.
func (s *session) Get(ctx context.Context, ref core.Ref) (*core.Resource, error) {
	kinds, ok := feedsOf(ref.Kind)
	if !ok {
		return nil, &provider.Error{Class: provider.ClassUnsupported, Message: fmt.Sprintf("unknown kind %q", ref.Kind)}
	}
	fs, err := s.acquire(kinds)
	if err != nil {
		return nil, err
	}
	defer s.release(fs)
	ctx, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()
	if err := waitAll(ctx, fs); err != nil {
		return nil, err
	}
	w := worldOf(fs)
	if err := s.refresh(ctx, fs[0], ref, w); err != nil {
		return nil, err
	}
	r, err := s.proj.resource(ref, w, s.now())
	if err != nil {
		return nil, err
	}
	return resourceWithTarget(s.target, r), nil
}

// refresh replaces ref's object in w by a fresh inspect (kinds that are
// one Engine object); a differing answer is handed to the feed as dirty.
// A failed read is Get's error: details are "as of reading".
func (s *session) refresh(ctx context.Context, f *feed, ref core.Ref, w *World) error {
	var fk Feed
	switch ref.Kind {
	case KindContainers:
		fk = FeedContainers
	case KindNetworks:
		fk = FeedNetworks
	case KindVolumes:
		fk = FeedVolumes
	case KindImages:
		fk = FeedImages
	default:
		return nil // services and projects are readings of several objects
	}
	if f.kind != fk || ref.Name == "" {
		return nil
	}
	canonical, obj, keep, err := f.ops.inspect(ctx, s.cl, ref.Name)
	switch {
	case engine.IsNotFound(err) || err == nil && !keep:
		canonical, obj = ref.Name, nil
	case err != nil:
		// never the feed's older reading passed off as a fresh one
		return providerError(err)
	}
	var old any
	switch fk {
	case FeedContainers:
		old = w.Containers[canonical]
		if obj == nil {
			delete(w.Containers, canonical)
		} else {
			w.Containers[canonical] = obj.(*engine.ContainerInspect)
		}
	case FeedNetworks:
		old = w.Networks[canonical]
		if obj == nil {
			delete(w.Networks, canonical)
		} else {
			w.Networks[canonical] = obj.(*engine.Network)
		}
	case FeedVolumes:
		old = w.Volumes[canonical]
		if obj == nil {
			delete(w.Volumes, canonical)
		} else {
			w.Volumes[canonical] = obj.(*engine.Volume)
		}
	case FeedImages:
		old = w.Images[canonical]
		if obj == nil {
			delete(w.Images, canonical)
		} else {
			w.Images[canonical] = obj.(*engine.ImageInspect)
		}
	}
	if differs(old, obj) {
		f.touch(canonical)
	}
	return nil
}

// differs: a fresh answer is not what the feed holds.
func differs(old, fresh any) bool {
	oldNil := old == nil || isNilObj(old)
	if oldNil || fresh == nil {
		return oldNil != (fresh == nil)
	}
	return string(rawOf(old)) != string(rawOf(fresh))
}

func isNilObj(o any) bool {
	switch v := o.(type) {
	case *engine.ContainerInspect:
		return v == nil
	case *engine.Network:
		return v == nil
	case *engine.Volume:
		return v == nil
	case *engine.ImageInspect:
		return v == nil
	}
	return o == nil
}

// Watch feeds sink with q's rows from the feeds q depends on.
func (s *session) Watch(q provider.Query, sink provider.Sink) (func(), error) {
	kinds, ok := feedsOf(q.Kind)
	switch {
	case !ok:
		return nil, &provider.Error{Class: provider.ClassUnsupported, Message: fmt.Sprintf("unknown kind %q", q.Kind)}
	case !q.Scope.Valid():
		return nil, &provider.Error{Class: provider.ClassInternal, Message: "invalid scope selector"}
	case q.Subject != nil:
		return nil, &provider.Error{Class: provider.ClassUnsupported, Message: "Docker objects have no events view"}
	}
	fs, err := s.acquire(kinds)
	if err != nil {
		return nil, err
	}
	w := newComposeWatch(s, q, sink, fs)
	return w.stop, nil
}

// Resync starts a new epoch of the feeds q depends on.
func (s *session) Resync(q provider.Query) error {
	kinds, ok := feedsOf(q.Kind)
	if !ok {
		return &provider.Error{Class: provider.ClassUnsupported, Message: fmt.Sprintf("unknown kind %q", q.Kind)}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return &provider.Error{Class: provider.ClassGone, Message: "the session was closed"}
	}
	for _, k := range kinds {
		if f := s.feeds[k]; f != nil {
			f.requestResync()
		}
	}
	return nil
}

// Stats reports feed counts for leak checks and soaks (/api/_test/stats).
func (s *session) Stats() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := map[string]int{}
	for _, f := range s.feeds {
		if f == nil {
			continue
		}
		f.mu.Lock()
		if f.leases > 0 {
			st["feeds_active"]++
		} else {
			st["feeds_idle"]++
		}
		st["watchers"] += len(f.subs)
		st["objects"] += len(f.objs)
		f.mu.Unlock()
		st["feed_epochs"] += int(f.epochs.Load())
		st["feed_lists"] += int(f.lists.Load())
		st["feed_inspects"] += int(f.inspects.Load())
	}
	return st
}
