package kubernetes

import (
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// viewWatch feeds one view (a provider.Sink) from a shared informer cache.
//
// Ordering: the informer calls this watch's handler functions one at a
// time, in order, and replays the cache's current objects to a handler
// added to a running informer before any later change — so a view attached
// to a warm cache gets its snapshot and then the changes, without a gap.
// The view's Apply is synchronous and non-blocking (memory under a short
// lock). Ready is pushed only after the registration reports HasSynced,
// i.e. after every initial object reached this handler — an empty list too.
type viewWatch struct {
	c      *informerCache
	def    *kindDef
	sink   provider.Sink
	target string
	now    func() time.Time

	reg  cache.ResourceEventHandlerRegistration
	done chan struct{}

	// order serializes everything that projects an observation and applies
	// it to the sink: the informer's handler calls, deadline
	// re-evaluations and status publication. Without it a timer could read an object, lose the CPU,
	// and apply it after the delete handler removed the row (resurrection).
	order sync.Mutex

	mu        sync.Mutex
	synced    bool
	stopped   bool
	deadlines map[string]deadline // object key -> when to re-project which incarnation
	timer     *time.Timer
	timerGen  uint64
}

type deadline struct {
	uid string
	at  time.Time
}

func (w *viewWatch) row(u *unstructured.Unstructured) (core.Row, time.Time) {
	cells, h, next := w.def.project(u, w.now())
	id := string(u.GetUID())
	if id == "" {
		id = u.GetNamespace() + "/" + u.GetName()
	}
	return core.Row{
		ID:  id,
		Rev: u.GetResourceVersion(),
		Ref: core.Ref{
			Provider: ProviderID, Target: w.target, Scope: u.GetNamespace(),
			Kind: w.def.desc.ID, Name: u.GetName(), UID: string(u.GetUID()),
		},
		Cells:  cells,
		Health: h,
	}, next
}

func (w *viewWatch) handlers() cache.ResourceEventHandlerFuncs {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			w.order.Lock()
			defer w.order.Unlock()
			if w.isStopped() {
				return
			}
			if u, ok := asUnstructured(obj); ok {
				r, next := w.row(u)
				w.sink.Apply(provider.Delta{Upserts: []core.Row{r}})
				w.schedule(objKey(u), string(u.GetUID()), next)
			}
		},
		UpdateFunc: func(oldObj, newObj any) {
			w.order.Lock()
			defer w.order.Unlock()
			if w.isStopped() {
				return
			}
			u, ok := asUnstructured(newObj)
			if !ok {
				return
			}
			r, next := w.row(u)
			d := provider.Delta{Upserts: []core.Row{r}}
			// Same name, new object (client-go may report a replacement as
			// an update): the old row goes, object-local UI state with it.
			if old, ok := oldObj.(interface{ GetUID() types.UID }); ok && old.GetUID() != u.GetUID() && old.GetUID() != "" {
				d.Deletes = []string{string(old.GetUID())}
			}
			w.sink.Apply(d)
			w.schedule(objKey(u), string(u.GetUID()), next)
		},
		DeleteFunc: func(obj any) {
			w.order.Lock()
			defer w.order.Unlock()
			if w.isStopped() {
				return
			}
			if tomb, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				obj = tomb.Obj
			}
			if u, ok := obj.(metav1.Object); ok {
				id := string(u.GetUID())
				if id == "" {
					id = u.GetNamespace() + "/" + u.GetName()
				}
				w.sink.Apply(provider.Delta{Deletes: []string{id}})
				w.unschedule(metaKey(u), string(u.GetUID()))
			}
		},
	}
}

func (w *viewWatch) isStopped() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stopped
}

func objKey(u *unstructured.Unstructured) string { return metaKey(u) }

func metaKey(u metav1.Object) string {
	if u.GetNamespace() == "" {
		return u.GetName()
	}
	return u.GetNamespace() + "/" + u.GetName()
}

// status combines this view's initial-sync state with the cache transport.
func (w *viewWatch) status() provider.ViewStatus {
	tr := w.c.transport()
	w.mu.Lock()
	synced := w.synced
	w.mu.Unlock()
	switch {
	case !synced && tr.failing:
		return provider.ViewStatus{State: provider.StatusError, Class: tr.class, Message: tr.message}
	case !synced:
		return provider.ViewStatus{State: provider.StatusLoading}
	case tr.failing:
		return provider.ViewStatus{State: provider.StatusStale, Class: tr.class, Message: tr.message}
	}
	return provider.ViewStatus{State: provider.StatusReady}
}

// pushStatus reads and applies the status under the order gate: a status
// computed earlier can never be applied after a newer one (the last push
// reads the current state). Callers hold no cache lock.
func (w *viewWatch) pushStatus() {
	w.order.Lock()
	defer w.order.Unlock()
	if w.isStopped() {
		return
	}
	st := w.status()
	w.sink.Apply(provider.Delta{Status: &st}) // the view drops an unchanged status
}

// waitSynced pushes Ready once every initial object reached the handler.
func (w *viewWatch) waitSynced() {
	stop := make(chan struct{})
	go func() {
		select {
		case <-w.done:
		case <-w.c.stop:
		}
		close(stop)
	}()
	if !cache.WaitForCacheSync(stop, w.reg.HasSynced) {
		return
	}
	w.mu.Lock()
	w.synced = true
	w.mu.Unlock()
	w.pushStatus()
}

// schedule re-projects the object at next (time-based health: "Pending for
// more than 5m" must turn into a warning without any API change). One local
// timer per view — not cluster polling. The deadline belongs to one
// incarnation (UID): a same-named replacement has its own.
func (w *viewWatch) schedule(key, uid string, next time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return
	}
	if next.IsZero() {
		if d, ok := w.deadlines[key]; ok && d.uid == uid {
			delete(w.deadlines, key)
		}
	} else {
		w.deadlines[key] = deadline{uid: uid, at: next}
	}
	w.armLocked()
}

// unschedule drops a deleted incarnation's deadline — not a replacement's.
func (w *viewWatch) unschedule(key, uid string) { w.schedule(key, uid, time.Time{}) }

func (w *viewWatch) armLocked() {
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	var soonest time.Time
	for _, d := range w.deadlines {
		soonest = earliest(soonest, d.at)
	}
	if soonest.IsZero() {
		return
	}
	delay := soonest.Sub(w.now())
	if delay < 0 {
		delay = 0
	}
	w.timerGen++
	gen := w.timerGen
	w.timer = time.AfterFunc(delay, func() { w.fire(gen) })
}

func (w *viewWatch) fire(gen uint64) {
	now := w.now()
	w.mu.Lock()
	if w.stopped || gen != w.timerGen {
		w.mu.Unlock()
		return // stopped, or superseded by a newer timer
	}
	w.timer = nil
	due := map[string]deadline{}
	for k, d := range w.deadlines {
		if !d.at.After(now) {
			due[k] = d
			delete(w.deadlines, k)
		}
	}
	w.mu.Unlock()
	for k, d := range due {
		w.reevaluate(k, d.uid)
	}
	w.mu.Lock()
	if !w.stopped && w.timer == nil {
		w.armLocked()
	}
	w.mu.Unlock()
}

// reevaluate re-projects the current cached object under the order gate:
// the informer updates its store before calling handlers, so under the gate
// the store is at least as new as anything applied — a deleted object is
// gone from it, and a replacement (other UID) is left to its handler.
func (w *viewWatch) reevaluate(key, uid string) {
	w.order.Lock()
	defer w.order.Unlock()
	if w.isStopped() {
		return
	}
	obj, ok, _ := w.c.inf.GetStore().GetByKey(key)
	if !ok {
		return
	}
	u, ok := asUnstructured(obj)
	if !ok || string(u.GetUID()) != uid {
		return
	}
	r, next := w.row(u)
	w.sink.Apply(provider.Delta{Upserts: []core.Row{r}})
	if !next.IsZero() {
		w.schedule(key, uid, next)
	}
}

func (w *viewWatch) stop() {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.stopped = true
	w.timerGen++ // a timer already firing becomes a no-op
	if w.timer != nil {
		w.timer.Stop()
	}
	w.mu.Unlock()
	close(w.done)
}
