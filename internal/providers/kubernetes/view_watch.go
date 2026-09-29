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

	mu        sync.Mutex
	synced    bool
	stopped   bool
	deadlines map[string]time.Time // object key -> when to re-project
	timer     *time.Timer
}

func (w *viewWatch) row(u *unstructured.Unstructured) (core.Row, time.Time) {
	cells, h, next := w.def.project(u, w.now())
	id := string(u.GetUID())
	if id == "" {
		id = u.GetNamespace() + "/" + u.GetName()
	}
	return core.Row{
		ID: id,
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
			if u, ok := asUnstructured(obj); ok {
				r, next := w.row(u)
				w.sink.Apply(provider.Delta{Upserts: []core.Row{r}})
				w.schedule(objKey(u), next)
			}
		},
		UpdateFunc: func(oldObj, newObj any) {
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
			w.schedule(objKey(u), next)
		},
		DeleteFunc: func(obj any) {
			if tomb, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				obj = tomb.Obj
			}
			if u, ok := obj.(metav1.Object); ok {
				id := string(u.GetUID())
				if id == "" {
					id = u.GetNamespace() + "/" + u.GetName()
				}
				w.sink.Apply(provider.Delta{Deletes: []string{id}})
				w.schedule(metaKey(u), time.Time{})
			}
		},
	}
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

func (w *viewWatch) pushStatus() {
	w.mu.Lock()
	stopped := w.stopped
	w.mu.Unlock()
	if stopped {
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
// timer per view — not cluster polling.
func (w *viewWatch) schedule(key string, next time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return
	}
	if next.IsZero() {
		delete(w.deadlines, key)
	} else {
		w.deadlines[key] = next
	}
	w.armLocked()
}

func (w *viewWatch) armLocked() {
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	var soonest time.Time
	for _, t := range w.deadlines {
		soonest = earliest(soonest, t)
	}
	if soonest.IsZero() {
		return
	}
	d := soonest.Sub(w.now())
	if d < 0 {
		d = 0
	}
	w.timer = time.AfterFunc(d, w.fire)
}

func (w *viewWatch) fire() {
	now := w.now()
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	var due []string
	for k, t := range w.deadlines {
		if !t.After(now) {
			due = append(due, k)
			delete(w.deadlines, k)
		}
	}
	w.mu.Unlock()
	for _, k := range due {
		obj, ok, _ := w.c.inf.GetStore().GetByKey(k)
		if !ok {
			continue
		}
		if u, ok := asUnstructured(obj); ok {
			r, next := w.row(u)
			w.sink.Apply(provider.Delta{Upserts: []core.Row{r}})
			if !next.IsZero() && next.After(now) {
				w.mu.Lock()
				w.deadlines[k] = next
				w.mu.Unlock()
			}
		}
	}
	w.mu.Lock()
	w.timer = nil
	if !w.stopped {
		w.armLocked()
	}
	w.mu.Unlock()
}

func (w *viewWatch) stop() {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.stopped = true
	if w.timer != nil {
		w.timer.Stop()
	}
	w.mu.Unlock()
	close(w.done)
}
