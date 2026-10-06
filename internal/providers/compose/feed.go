package compose

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/compose/engine"
)

// Observation tuning (docs/architecture.md).
const (
	// feedGrace: a feed no consumer leases is kept this long (switching
	// back is instant), like the Kubernetes caches. (The session's fields
	// hold these; tests shorten them.)
	feedGrace = 60 * time.Second
	// maxDirty bounds the ids waiting for an inspect; beyond it the feed
	// takes a new snapshot (a new epoch) instead of queueing more.
	maxDirty = 4096
	// maxUnresolved bounds the keys kept after a failed inspect; beyond it
	// the feed reads everything again (the keys of objects long gone
	// would otherwise pile up while the stream stays alive).
	maxUnresolved = 256
	// inspectPool: concurrent inspects of a session, all feeds together.
	inspectPool = 8
	// staleAfter: a broken feed not restored within this is stale.
	staleAfter = 5 * time.Second
	// eventsLookBack: events are read from the daemon's clock minus this
	// (clock skew; the overlap with the snapshot is harmless: events are
	// only hints).
	eventsLookBack = time.Second
)

var backoffSteps = []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}

var (
	errResync   = errors.New("a new reading was asked for")
	errOverflow = errors.New("too many changes at once; reading everything again")
	errEnded    = errors.New("the Docker Engine ended the events stream")
	// errUnresolved: too many objects could not be read; a new snapshot
	// replaces the pile (keys of objects long gone would stay otherwise).
	errUnresolved = errors.New("too many objects could not be read; reading everything again")
)

// feedOps is what differs between the feeds.
type feedOps struct {
	events engine.Filters
	// list returns the keys of the objects to inspect.
	list func(ctx context.Context, cl *engine.Client) ([]string, error)
	// inspect reads one object by key (or a name the Engine resolves) and
	// returns its canonical key; keep=false: not an object of this feed
	// (for example an object removed before inspect).
	inspect func(ctx context.Context, cl *engine.Client, key string) (canonical string, obj any, keep bool, err error)
	// dirty says which keys an event makes dirty; weak ones are inspected
	// only when already known (a network connect names any container).
	dirty func(ev engine.Event) (strong, weak []string)
}

var containerActions = []string{"create", "start", "restart", "die", "stop", "kill", "pause", "unpause", "rename", "update", "destroy", "health_status", "connect", "disconnect"}

var feedOpsOf = [feedCount]feedOps{
	FeedContainers: {
		// No label filter: it would drop the network events (a connect
		// names the container only in its attributes). Container events
		// include both standalone and Compose containers.
		events: engine.Filters{"type": {"container", "network"}, "event": containerActions},
		list: func(ctx context.Context, cl *engine.Client) ([]string, error) {
			l, err := cl.ListContainers(ctx, nil)
			keys := make([]string, 0, len(l))
			for _, c := range l {
				keys = append(keys, c.ID)
			}
			return keys, err
		},
		inspect: func(ctx context.Context, cl *engine.Client, key string) (string, any, bool, error) {
			c, err := cl.InspectContainer(ctx, key)
			if err != nil {
				return "", nil, false, err
			}
			return c.ID, &c, true, nil
		},
		dirty: func(ev engine.Event) (strong, weak []string) {
			switch ev.Type {
			case "container":
				return []string{ev.Actor.ID}, nil
			case "network":
				if c := ev.Actor.Attributes["container"]; c != "" && (ev.Action == "connect" || ev.Action == "disconnect") {
					return nil, []string{c}
				}
			}
			return nil, nil
		},
	},
	FeedNetworks: {
		events: engine.Filters{"type": {"network"}, "event": {"create", "destroy", "connect", "disconnect"}},
		list: func(ctx context.Context, cl *engine.Client) ([]string, error) {
			l, err := cl.ListNetworks(ctx, nil)
			keys := make([]string, 0, len(l))
			for _, n := range l {
				keys = append(keys, n.ID)
			}
			return keys, err
		},
		inspect: func(ctx context.Context, cl *engine.Client, key string) (string, any, bool, error) {
			n, err := cl.InspectNetwork(ctx, key)
			if err != nil {
				return "", nil, false, err
			}
			return n.ID, &n, true, nil
		},
		dirty: actorID,
	},
	FeedVolumes: {
		// mount/unmount are left out: who uses a volume comes from the
		// containers' inspects, and nothing a volume's inspect says changes.
		events: engine.Filters{"type": {"volume"}, "event": {"create", "destroy"}},
		list: func(ctx context.Context, cl *engine.Client) ([]string, error) {
			l, err := cl.ListVolumes(ctx, nil)
			keys := make([]string, 0, len(l.Volumes))
			for _, v := range l.Volumes {
				keys = append(keys, v.Name)
			}
			if err == nil && len(l.Warnings) > 0 {
				err = &engine.Error{Class: provider.ClassUnavailable, Message: "the volume list may be incomplete: " + l.Warnings[0]}
			}
			return keys, err
		},
		inspect: func(ctx context.Context, cl *engine.Client, key string) (string, any, bool, error) {
			v, err := cl.InspectVolume(ctx, key)
			if err != nil {
				return "", nil, false, err
			}
			return v.Name, &v, true, nil
		},
		dirty: actorID,
	},
	FeedImages: {
		// pull/load/import name a reference, the others the image id; the
		// inspect resolves either to the id.
		events: engine.Filters{"type": {"image"}, "event": {"pull", "tag", "untag", "delete", "load", "import"}},
		list: func(ctx context.Context, cl *engine.Client) ([]string, error) {
			l, err := cl.ListImages(ctx, nil)
			keys := make([]string, 0, len(l))
			for _, i := range l {
				keys = append(keys, i.ID)
			}
			return keys, err
		},
		inspect: func(ctx context.Context, cl *engine.Client, key string) (string, any, bool, error) {
			i, err := cl.InspectImage(ctx, key)
			if err != nil {
				return "", nil, false, err
			}
			return i.ID, &i, true, nil
		},
		dirty: actorID,
	},
}

func actorID(ev engine.Event) (strong, weak []string) {
	if ev.Actor.ID == "" {
		return nil, nil
	}
	return []string{ev.Actor.ID}, nil
}

// feedState is a feed's observation state, which views aggregate.
type feedState struct {
	State   provider.StatusState
	Class   provider.ErrorClass
	Message string
}

// feed observes one type of Engine object for a session: epochs of
// (events subscription, list, inspects, reconciliation), then inspects of
// what events make dirty. One goroutine (run) does all of it, so nothing
// of an old epoch can land in a newer one; objects are replaced only by
// inspects and snapshots, never by an event.
type feed struct {
	s    *session
	kind Feed
	ops  feedOps

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	resync chan struct{} // capacity 1: requests coalesce

	mu   sync.Mutex
	objs map[string]any // by canonical key; each value is never mutated
	// published: a first snapshot was reconciled with the events read
	// during it and the stream was alive (the first Ready); later epochs
	// replace objs without unpublishing them.
	published  bool
	live       bool      // reconciled and the events stream is alive
	failed     error     // the last failure while not live
	broken     bool      // failed after a snapshot, for staleAfter or longer
	staleArmed bool      // the staleAfter timer of this outage runs
	liveGen    uint64    // bumped on every transition to live
	dirtyNow   *dirtySet // the running epoch's dirty set (touch)
	// unresolved: keys whose last inspect failed (not 404); their last
	// known objects stay, the feed is stale until they are read.
	unresolved map[string]error
	changed    chan struct{} // closed and replaced on every change (waiters)
	subs       map[*composeWatch]struct{}
	leases     int
	idleAt     time.Time

	epochs, lists, inspects atomic.Int64
}

func newFeed(s *session, kind Feed) *feed {
	ctx, cancel := context.WithCancel(s.ctx)
	return &feed{
		s: s, kind: kind, ops: feedOpsOf[kind], ctx: ctx, cancel: cancel,
		done: make(chan struct{}), resync: make(chan struct{}, 1),
		objs: map[string]any{}, unresolved: map[string]error{},
		changed: make(chan struct{}), subs: map[*composeWatch]struct{}{},
	}
}

func (f *feed) stop() { f.cancel() }

// state is the feed's status for views.
func (f *feed) state() feedState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stateLocked()
}

func (f *feed) stateLocked() feedState {
	switch {
	case !f.published && f.failed != nil:
		return feedState{State: provider.StatusError, Class: classOf(f.failed), Message: f.failed.Error()}
	case !f.published:
		return feedState{State: provider.StatusLoading}
	case f.broken && f.failed != nil:
		return feedState{State: provider.StatusStale, Class: classOf(f.failed), Message: f.failed.Error()}
	case len(f.unresolved) > 0:
		var first error
		for _, err := range f.unresolved {
			first = err
			break
		}
		return feedState{State: provider.StatusStale, Class: classOf(first), Message: fmt.Sprintf("%d %s could not be read: %v", len(f.unresolved), f.kind, first)}
	}
	return feedState{State: provider.StatusReady}
}

// classOf classifies an observation failure.
func classOf(err error) provider.ErrorClass {
	switch {
	case errors.Is(err, errEnded):
		return provider.ClassUnavailable
	}
	return engine.ClassOf(err)
}

// notifyLocked wakes waiters and the views of this feed. Callers hold f.mu.
func (f *feed) notifyLocked() {
	close(f.changed)
	f.changed = make(chan struct{})
	for w := range f.subs {
		w.poke()
	}
}

// requestResync asks for a new epoch; requests coalesce, a running
// reconciliation is not interrupted.
func (f *feed) requestResync() {
	select {
	case f.resync <- struct{}{}:
	default:
	}
}

// wait blocks until the feed is published (nil) or fails before that
// (the error), or ctx ends.
func (f *feed) wait(ctx context.Context) error {
	for {
		f.mu.Lock()
		snap, failed, ch := f.published, f.failed, f.changed
		f.mu.Unlock()
		switch {
		case snap:
			return nil
		case failed != nil:
			return failed
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		case <-f.done:
			return &engine.Error{Class: provider.ClassGone, Message: "the session was closed"}
		}
	}
}

// run is the feed's life: epochs until the feed stops.
func (f *feed) run() {
	defer close(f.done)
	step := 0
	for {
		gen := f.liveGeneration()
		err := f.epoch()
		if f.ctx.Err() != nil {
			return
		}
		if f.liveGeneration() != gen {
			step = 0 // it was live: start the backoff over
		}
		if errors.Is(err, errResync) || errors.Is(err, errOverflow) {
			continue
		}
		f.fail(err)
		t := time.NewTimer(f.s.backoff[min(step, len(f.s.backoff)-1)])
		step++
		select {
		case <-t.C:
		case <-f.resync: // the user asks: try now
			t.Stop()
		case <-f.ctx.Done():
			t.Stop()
			return
		}
	}
}

func (f *feed) liveGeneration() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.liveGen
}

// fail records a failure: without a snapshot the views show it at once;
// with one they keep the rows and turn stale only if the feed is not live
// again within staleAfter.
func (f *feed) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.live = false
	f.failed = err
	if !f.published || f.broken {
		f.notifyLocked() // the error or its message changed
		return
	}
	if f.staleArmed {
		return
	}
	f.staleArmed = true
	gen := f.liveGen
	time.AfterFunc(f.s.staleAfter, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.liveGen == gen && !f.live && f.ctx.Err() == nil {
			f.broken = true
			f.notifyLocked()
		}
	})
}

// epoch runs one epoch; it returns why it ended.
func (f *feed) epoch() error {
	f.epochs.Add(1)
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	info, err := f.s.cl.Info(ctx)
	if err != nil {
		return err
	}
	var since time.Time
	if !info.SystemTime.IsZero() {
		since = info.SystemTime.Add(-eventsLookBack)
	}
	stream, err := f.s.cl.Events(ctx, since, f.ops.events)
	if err != nil {
		return err
	}
	defer stream.Close()
	d := newDirtySet(f.s.maxDirty)
	f.mu.Lock()
	f.dirtyNow = d
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.dirtyNow = nil
		f.mu.Unlock()
	}()
	broken := make(chan error, 1)
	go f.read(stream, d, broken, cancel)
	// why: an epoch cut short by its stream's end says so, not "canceled".
	why := func(err error) error {
		select {
		case b := <-broken:
			return b
		default:
			return err
		}
	}

	f.lists.Add(1)
	keys, err := f.ops.list(ctx, f.s.cl)
	if err != nil {
		return why(err)
	}
	objs := map[string]any{}
	unresolved := map[string]error{}
	for _, r := range f.inspectAll(ctx, keys) {
		switch {
		case r.err == nil && r.keep:
			objs[r.canonical] = r.obj
		case r.err == nil, engine.IsNotFound(r.err):
		default:
			unresolved[r.key] = r.err
		}
	}
	if ctx.Err() != nil {
		return why(ctx.Err())
	}
	if len(unresolved) > f.s.maxUnresolved {
		return fmt.Errorf("%d %s could not be read: %w", len(unresolved), f.kind, firstErr(unresolved))
	}
	f.applySnapshot(objs, unresolved)
	// Reconcile: what changed during the snapshot is read again after it
	// — one batch; what changes after that is a live change.
	if _, err := f.drainOnce(ctx, d); err != nil {
		return why(err)
	}
	select {
	case err := <-broken:
		return err
	default:
	}
	f.setLive()
	for {
		select {
		case <-d.signal:
			if err := f.drain(ctx, d); err != nil {
				return why(err)
			}
		case err := <-broken:
			return err
		case <-f.resync:
			return errResync
		case <-ctx.Done():
			return why(ctx.Err())
		}
	}
}

// read turns the events stream into dirty keys, continuously and apart
// from the inspects (a slow reader makes the daemon skip deliveries). Its
// end ends the epoch at once, even in the middle of a snapshot.
func (f *feed) read(stream *engine.EventStream, d *dirtySet, broken chan<- error, cancel context.CancelFunc) {
	for {
		ev, err := stream.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = errEnded
			}
			broken <- err
			cancel()
			return
		}
		strong, weak := f.ops.dirty(ev)
		d.add(strong, weak)
	}
}

// drain inspects dirty keys until none are left.
func (f *feed) drain(ctx context.Context, d *dirtySet) error {
	for {
		more, err := f.drainOnce(ctx, d)
		if err != nil || !more {
			return err
		}
		// A user's Resync is served between batches: steady churn must
		// not queue it forever (the batch in flight is finished first).
		select {
		case <-f.resync:
			return errResync
		default:
		}
	}
}

// drainOnce inspects the keys dirty now (one batch); more: there were
// some (others may have become dirty meanwhile).
func (f *feed) drainOnce(ctx context.Context, d *dirtySet) (more bool, err error) {
	strong, weak, overflow := d.take()
	if overflow {
		return false, errOverflow
	}
	keys := strong
	if len(weak) > 0 {
		f.mu.Lock()
		for _, k := range weak {
			if _, known := f.objs[k]; known {
				keys = append(keys, k)
			}
		}
		f.mu.Unlock()
	}
	if len(keys) == 0 {
		return false, nil
	}
	results := f.inspectAll(ctx, keys)
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if f.applyResults(results) {
		return false, errUnresolved
	}
	return true, nil
}

type inspectResult struct {
	key, canonical string
	obj            any
	keep           bool
	err            error
}

// inspectAll inspects keys in the session's pool; the results are in the
// order of keys.
func (f *feed) inspectAll(ctx context.Context, keys []string) []inspectResult {
	out := make([]inspectResult, len(keys))
	var wg sync.WaitGroup
	for i, k := range keys {
		select {
		case f.s.pool <- struct{}{}:
		case <-ctx.Done():
			for j := i; j < len(keys); j++ {
				out[j] = inspectResult{key: keys[j], err: ctx.Err()}
			}
			wg.Wait()
			return out
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-f.s.pool }()
			f.inspects.Add(1)
			c, obj, keep, err := f.ops.inspect(ctx, f.s.cl, k)
			out[i] = inspectResult{key: k, canonical: c, obj: obj, keep: keep, err: err}
		}()
	}
	wg.Wait()
	return out
}

// applySnapshot replaces the objects by a new epoch's snapshot; the last
// known objects of unresolved keys stay.
func (f *feed) applySnapshot(objs map[string]any, unresolved map[string]error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k := range unresolved {
		if old, ok := f.objs[k]; ok {
			objs[k] = old
		}
	}
	f.objs = objs
	f.unresolved = unresolved
	f.notifyLocked()
}

// applyResults applies inspects of dirty keys: an answer replaces (or,
// for a foreign object, removes) the object, 404 removes it, a failure
// keeps it and makes the feed stale until it is read.
func (f *feed) applyResults(rs []inspectResult) (tooMany bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	changed := false
	for _, r := range rs {
		switch {
		case r.err == nil:
			// a read after a failed one is a recovery even with the same bytes
			for _, k := range []string{r.key, r.canonical} {
				if _, had := f.unresolved[k]; had {
					delete(f.unresolved, k)
					changed = true
				}
			}
			old, had := f.objs[r.canonical]
			if !r.keep {
				if had {
					delete(f.objs, r.canonical)
					changed = true
				}
				continue
			}
			if !had || !bytes.Equal(rawOf(old), rawOf(r.obj)) {
				f.objs[r.canonical] = r.obj
				changed = true
			}
		case engine.IsNotFound(r.err):
			if _, had := f.unresolved[r.key]; had {
				delete(f.unresolved, r.key)
				changed = true
			}
			if _, had := f.objs[r.key]; had {
				delete(f.objs, r.key)
				changed = true
			}
		default:
			f.unresolved[r.key] = r.err
			changed = true
		}
	}
	if changed {
		f.notifyLocked()
	}
	return len(f.unresolved) > f.s.maxUnresolved
}

func (f *feed) setLive() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.live, f.failed, f.broken, f.staleArmed = true, nil, false, false
	f.published = true
	f.liveGen++
	f.notifyLocked()
}

// touch makes a key dirty from outside the feed (details read an object
// fresh and found it changed): the feed reads it again.
func (f *feed) touch(key string) {
	f.mu.Lock()
	d := f.dirtyNow
	f.mu.Unlock()
	if d != nil {
		d.add([]string{key}, nil)
	}
}

// lookup is key's object (nil: none) and whether the feed has a
// snapshot to say so; changed is closed by the next change.
func (f *feed) lookup(key string) (obj any, snap bool, changed <-chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.objs[key], f.published, f.changed
}

// observe is the current objects (copied as by objects), whether they are
// a snapshot, the feed's state and the channel of the next change.
func (f *feed) observe() (objs map[string]any, snap bool, st feedState, changed <-chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	objs = make(map[string]any, len(f.objs))
	for k, v := range f.objs {
		objs[k] = v
	}
	return objs, f.published, f.stateLocked(), f.changed
}

// objects copies the current objects (the values are shared: never
// mutated).
func (f *feed) objects() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]any, len(f.objs))
	for k, v := range f.objs {
		out[k] = v
	}
	return out
}

func rawOf(obj any) []byte {
	switch o := obj.(type) {
	case *engine.ContainerInspect:
		return o.Raw
	case *engine.Network:
		return o.Raw
	case *engine.Volume:
		return o.Raw
	case *engine.ImageInspect:
		return o.Raw
	}
	return nil
}

// dirtySet is a bounded, coalescing set of keys waiting for an inspect;
// adding past its bound marks it overflowed (the feed reads everything
// again) instead of growing.
type dirtySet struct {
	max    int
	signal chan struct{} // capacity 1

	mu       sync.Mutex
	keys     map[string]bool // key → strong
	overflow bool
}

func newDirtySet(limit int) *dirtySet {
	return &dirtySet{max: limit, signal: make(chan struct{}, 1), keys: map[string]bool{}}
}

func (d *dirtySet) add(strong, weak []string) {
	if len(strong)+len(weak) == 0 {
		return
	}
	d.mu.Lock()
	for _, k := range strong {
		d.keys[k] = true
	}
	for _, k := range weak {
		if _, ok := d.keys[k]; !ok {
			d.keys[k] = false
		}
	}
	if len(d.keys) > d.max {
		d.overflow = true
		d.keys = map[string]bool{}
	}
	d.mu.Unlock()
	select {
	case d.signal <- struct{}{}:
	default:
	}
}

// take empties the set.
func (d *dirtySet) take() (strong, weak []string, overflow bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.overflow {
		d.overflow = false
		return nil, nil, true
	}
	for k, s := range d.keys {
		if s {
			strong = append(strong, k)
		} else {
			weak = append(weak, k)
		}
	}
	d.keys = map[string]bool{}
	return strong, weak, false
}

func firstErr(m map[string]error) error {
	for _, err := range m {
		return err
	}
	return nil
}
