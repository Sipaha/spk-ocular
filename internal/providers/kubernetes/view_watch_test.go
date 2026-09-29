package kubernetes

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

type rowSink struct {
	mu   sync.Mutex
	rows map[string]string // id -> status text
}

func (s *rowSink) Apply(d provider.Delta) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range d.Upserts {
		s.rows[r.ID] = r.Cells[0].Text
	}
	for _, id := range d.Deletes {
		delete(s.rows, id)
	}
}

func (s *rowSink) snapshot() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for k, v := range s.rows {
		out[k] = v
	}
	return out
}

// gatedWatch builds a viewWatch whose projection blocks on the first
// deadline re-evaluation, so a handler can race it deterministically.
func gatedWatch(t *testing.T, sink provider.Sink) (w *viewWatch, entered, proceed chan struct{}) {
	t.Helper()
	entered, proceed = make(chan struct{}), make(chan struct{})
	var once sync.Once
	blocking := false
	def := &kindDef{desc: core.KindDescriptor{ID: "pods"}, project: func(u *unstructured.Unstructured, _ time.Time) ([]core.Cell, core.Health, time.Time) {
		if blocking {
			once.Do(func() { close(entered); <-proceed })
		}
		return []core.Cell{core.TextCell(string(u.GetUID()))}, core.Health{State: core.HealthOK}, time.Time{}
	}}
	inf := cache.NewSharedIndexInformer(&cache.ListWatch{}, &unstructured.Unstructured{}, 0, nil)
	w = &viewWatch{c: &informerCache{inf: inf}, def: def, sink: sink, now: time.Now, done: make(chan struct{}), deadlines: map[string]deadline{}}
	blocking = true
	return w, entered, proceed
}

func podU(uid string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{}}
	u.SetName("p")
	u.SetNamespace("ns")
	u.SetUID(typesUID(uid))
	return u
}

// Review 2026-09-29: a deadline re-projection used to apply an old
// observation after the delete handler removed the row.
func TestDeadlineCannotResurrectADeletedObject(t *testing.T) {
	sink := &rowSink{rows: map[string]string{"old": "old"}}
	w, entered, proceed := gatedWatch(t, sink)
	store := w.c.inf.GetStore()
	u := podU("old")
	require.NoError(t, store.Add(u))
	w.deadlines["ns/p"] = deadline{uid: "old", at: time.Now().Add(-time.Second)}

	w.timerGen = 1
	done := make(chan struct{})
	go func() { w.fire(1); close(done) }()
	<-entered
	// The informer removes the object from its store, then calls the handler.
	require.NoError(t, store.Delete(u))
	handlerDone := make(chan struct{})
	go func() { w.handlers().DeleteFunc(u); close(handlerDone) }()
	select {
	case <-handlerDone:
		t.Fatal("the delete handler must wait for the in-flight re-evaluation")
	case <-time.After(50 * time.Millisecond):
	}
	close(proceed)
	<-done
	<-handlerDone
	assert.Empty(t, sink.snapshot(), "deleted stays deleted")
}

func TestDeadlineSkipsAReplacementAndLateDeleteKeepsItsDeadline(t *testing.T) {
	sink := &rowSink{rows: map[string]string{}}
	w, _, proceed := gatedWatch(t, sink)
	close(proceed)
	store := w.c.inf.GetStore()
	require.NoError(t, store.Add(podU("new"))) // the store already holds the replacement
	w.reevaluate("ns/p", "old")
	assert.Empty(t, sink.snapshot(), "a deadline of the old incarnation does not project the new one")

	w.deadlines["ns/p"] = deadline{uid: "new", at: time.Now().Add(time.Hour)}
	w.unschedule("ns/p", "old") // late delete of the old UID
	assert.Contains(t, w.deadlines, "ns/p", "the replacement's deadline survives")
	w.unschedule("ns/p", "new")
	assert.NotContains(t, w.deadlines, "ns/p")
}

func TestStopDuringReevaluationAppliesNothing(t *testing.T) {
	sink := &rowSink{rows: map[string]string{}}
	w, entered, proceed := gatedWatch(t, sink)
	require.NoError(t, w.c.inf.GetStore().Add(podU("u1")))
	w.deadlines["ns/p"] = deadline{uid: "u1", at: time.Now().Add(-time.Second)}
	w.timerGen = 1
	done := make(chan struct{})
	go func() { w.fire(1); close(done) }()
	<-entered
	w.stop()
	close(proceed)
	<-done
	// The projection was already running; what matters is that nothing is
	// scheduled or applied after stop by handlers.
	w.handlers().AddFunc(podU("u2"))
	assert.NotContains(t, sink.snapshot(), "u2")
	assert.Nil(t, w.timer)
}

func TestSupersededTimerIsANoop(t *testing.T) {
	sink := &rowSink{rows: map[string]string{}}
	w, _, proceed := gatedWatch(t, sink)
	close(proceed)
	require.NoError(t, w.c.inf.GetStore().Add(podU("u1")))
	w.mu.Lock()
	w.deadlines["ns/p"] = deadline{uid: "u1", at: time.Now().Add(time.Hour)}
	w.armLocked() // gen 1
	w.armLocked() // gen 2 supersedes
	w.mu.Unlock()
	w.fire(1)
	assert.Empty(t, sink.snapshot())
	assert.NotNil(t, w.timer, "the newer timer is still tracked")
	w.stop()
}

func typesUID(s string) types.UID { return types.UID(s) }
