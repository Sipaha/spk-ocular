package kubernetes

import (
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
)

// CRDs as the trigger of the next discovery (not a timer): a CRD added,
// changed or deleted asks the catalog to discover again, a second after
// the last change. Without the right to list and watch CRDs there is no
// trigger — F5 and reopening the target remain.

const crdKindID = "apiextensions.k8s.io/customresourcedefinitions"

// Package vars for tests.
var (
	crdDebounce   = time.Second
	crdBackoffMax = 5 * time.Minute
)

func (s *session) watchCRDs(gvr schema.GroupVersionResource) {
	res := s.dyn.Resource(gvr)
	var pending *time.Timer
	changed := func() {
		if pending != nil {
			pending.Stop()
		}
		pending = time.AfterFunc(crdDebounce, s.cat.refresh)
	}
	defer func() {
		if pending != nil {
			pending.Stop()
		}
	}()
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
			rv = ""
			changed() // changes since rv may be lost
			continue
		case err != nil:
			if !s.sleep(backoff) {
				return
			}
			backoff = min(backoff*2, crdBackoffMax)
			continue
		}
		started := time.Now()
		rv = s.followCRDs(w, rv, changed)
		cancel()
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
// watch from next ("" — list again).
func (s *session) followCRDs(w watch.Interface, rv string, changed func()) string {
	defer w.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return rv
		case ev, ok := <-w.ResultChan():
			if !ok {
				return rv
			}
			if ev.Type == watch.Error {
				if st, ok := ev.Object.(*metav1.Status); ok && st.Code == 410 {
					changed()
					return ""
				}
				return rv
			}
			if o, ok := ev.Object.(metav1.Object); ok && o.GetResourceVersion() != "" {
				rv = o.GetResourceVersion()
			}
			if ev.Type != watch.Bookmark {
				changed()
			}
		}
	}
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
