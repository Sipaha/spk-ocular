package kubernetes

import (
	"context"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
)

// CRDs as the trigger of the next discovery (not a timer): a CRD added,
// changed or deleted asks the catalog to discover again, a second after
// the last change but no later than crdMaxDelay after the first one (a
// busy cluster's stream of CRD status changes must not postpone it
// forever). Each baseline (the collection's version the watch starts
// from, first or after an expiry) is followed by a discovery too: a change
// between the last discovery and the baseline has no event. Without the
// right to list and watch CRDs there is no trigger — F5 and reopening the
// target remain.

const crdKindID = "apiextensions.k8s.io/customresourcedefinitions"

// Package vars for tests.
var (
	crdDebounce   = time.Second
	crdMaxDelay   = 5 * time.Second
	crdBackoffMax = 5 * time.Minute
)

// crdTrigger coalesces CRD changes into catalog refreshes. Each timer
// carries its generation: a callback already on its way when a newer
// timer replaced it (or the trigger stopped) does nothing.
type crdTrigger struct {
	mu      sync.Mutex
	timer   *time.Timer
	gen     uint64
	first   time.Time // the first change not yet refreshed for
	refresh func()
}

func (t *crdTrigger) changed() {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	if t.timer == nil {
		t.first = now
	} else {
		t.timer.Stop()
	}
	t.gen++
	gen := t.gen
	delay := min(crdDebounce, t.first.Add(crdMaxDelay).Sub(now))
	t.timer = time.AfterFunc(max(delay, 0), func() { t.fire(gen) })
}

func (t *crdTrigger) fire(gen uint64) {
	t.mu.Lock()
	if gen != t.gen {
		t.mu.Unlock()
		return
	}
	t.timer = nil
	t.mu.Unlock()
	t.refresh()
}

func (t *crdTrigger) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gen++
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
}

func (s *session) watchCRDs(gvr schema.GroupVersionResource) {
	res := s.dyn.Resource(gvr)
	trig := &crdTrigger{refresh: s.cat.refresh}
	defer trig.stop()
	changed := trig.changed
	backoff := time.Second
	rv := ""
	for s.ctx.Err() == nil {
		if rv == "" {
			// Where to watch from: the collection's version, one object read.
			ctx, cancel := context.WithTimeout(s.ctx, listTimeout)
			l, err := res.List(ctx, metav1.ListOptions{Limit: 1})
			cancel()
			switch {
			case apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err):
				return
			case err != nil:
				if !s.sleep(backoff) {
					return
				}
				backoff = min(backoff*2, crdBackoffMax)
				continue
			}
			rv = l.GetResourceVersion()
			changed() // what changed before the baseline had no event
		}
		// A deadline on getting the stream only; it then lives until it ends.
		ctx, cancel := context.WithCancel(s.ctx)
		timer := time.AfterFunc(watchEstablishTimeout, cancel)
		w, err := res.Watch(ctx, metav1.ListOptions{ResourceVersion: rv, AllowWatchBookmarks: true})
		if !timer.Stop() && err == nil {
			w.Stop()
			err = context.DeadlineExceeded
		}
		if err != nil {
			cancel()
		}
		switch {
		case apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err):
			return
		case apierrors.IsGone(err) || apierrors.IsResourceExpired(err):
			rv = "" // a new baseline, then a discovery (changes since rv may be lost)
			continue
		case err != nil:
			if !s.sleep(backoff) {
				return
			}
			backoff = min(backoff*2, crdBackoffMax)
			continue
		}
		started := time.Now()
		var denied bool
		rv, denied = s.followCRDs(w, rv, changed)
		cancel()
		if denied {
			return
		}
		if time.Since(started) > time.Minute {
			backoff = time.Second
		} else if !s.sleep(backoff) {
			return
		} else {
			backoff = min(backoff*2, crdBackoffMax)
		}
	}
}

// followCRDs reads one watch stream until it ends; returns the version to
// watch from next ("" — a new baseline) and whether the right was denied.
func (s *session) followCRDs(w watch.Interface, rv string, changed func()) (string, bool) {
	defer w.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return rv, false
		case ev, ok := <-w.ResultChan():
			if !ok {
				return rv, false
			}
			if ev.Type == watch.Error {
				err := apierrors.FromObject(ev.Object)
				switch {
				case apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err):
					return rv, true
				case apierrors.IsGone(err) || apierrors.IsResourceExpired(err):
					return "", false
				}
				return rv, false
			}
			if o, ok := ev.Object.(metav1.Object); ok && o.GetResourceVersion() != "" {
				rv = o.GetResourceVersion()
			}
			if ev.Type != watch.Bookmark {
				changed()
				if u, ok := ev.Object.(*unstructured.Unstructured); ok {
					if ev.Type == watch.Deleted {
						s.crdDeleted(u.GetName())
					} else {
						s.crdChanged(u)
					}
				}
			}
		}
	}
}

// crdChanged: a CRD's printer columns (or scope, versions) may have
// changed — if what the columns are made of did, the schemas of its
// resource's versions are checked again (one check at a time each).
func (s *session) crdChanged(u *unstructured.Unstructured) {
	group, plural := str(u.Object, "spec", "group"), str(u.Object, "spec", "names", "plural")
	p := crdPrint(u)
	s.schemas.mu.Lock()
	if s.schemas.crdPrints == nil {
		s.schemas.crdPrints = map[string]string{}
	}
	if s.schemas.crdPrints[u.GetName()] == p {
		s.schemas.mu.Unlock()
		return // status, labels, …: the columns are what they were
	}
	s.schemas.crdPrints[u.GetName()] = p
	var gvrs []schema.GroupVersionResource
	for gvr := range s.schemas.by {
		if gvr.Group == group && gvr.Resource == plural {
			gvrs = append(gvrs, gvr)
		}
	}
	s.schemas.mu.Unlock()
	for _, gvr := range gvrs {
		go s.revalidate(&gvr)
	}
}

// crdDeleted: the CRD is gone (the catalog removes its kinds); a CRD of
// the name created later is compared afresh.
func (s *session) crdDeleted(name string) {
	s.schemas.mu.Lock()
	delete(s.schemas.crdPrints, name)
	s.schemas.mu.Unlock()
}

// sleep waits d unless the session ends first (false).
func (s *session) sleep(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-s.ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
