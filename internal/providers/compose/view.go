package compose

import (
	"reflect"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// composeWatch feeds one view: on every change of its feeds (coalesced)
// it projects a reading of them and applies the difference to the last
// rows it applied. One goroutine does the projecting; the order gate
// makes stop final (nothing is applied after it returns).
type composeWatch struct {
	s     *session
	q     provider.Query
	sink  provider.Sink
	feeds []*feed

	pokes chan struct{} // capacity 1
	done  chan struct{}

	order   sync.Mutex // held while applying; stop takes it
	stopped bool

	// the loop's own state
	rows   map[string]core.Row
	reset  bool // the initial rows were applied
	status *provider.ViewStatus
	timer  *time.Timer
}

func newComposeWatch(s *session, q provider.Query, sink provider.Sink, fs []*feed) *composeWatch {
	w := &composeWatch{s: s, q: q, sink: sink, feeds: fs, pokes: make(chan struct{}, 1), done: make(chan struct{})}
	for _, f := range fs {
		f.mu.Lock()
		f.subs[w] = struct{}{}
		f.mu.Unlock()
	}
	w.poke()
	go w.loop()
	return w
}

func (w *composeWatch) poke() {
	select {
	case w.pokes <- struct{}{}:
	default:
	}
}

func (w *composeWatch) loop() {
	var tick <-chan time.Time
	for {
		select {
		case <-w.done:
			if w.timer != nil {
				w.timer.Stop()
			}
			return
		case <-w.pokes:
		case <-tick:
		}
		next := w.step()
		if w.timer != nil {
			w.timer.Stop()
			w.timer, tick = nil, nil
		}
		if !next.IsZero() {
			w.timer = time.NewTimer(max(next.Sub(w.s.now()), 0))
			tick = w.timer.C
		}
	}
}

// step projects the feeds' current reading and applies what changed; it
// returns when the rows need projecting again without a change (health
// deadlines).
func (w *composeWatch) step() time.Time {
	st := aggregate(w.feeds)
	var d provider.Delta
	var next time.Time
	if st.State == provider.StatusReady || st.State == provider.StatusStale {
		rows, n := w.s.proj.rows(w.q, worldOf(w.feeds), w.s.now())
		rows = withTarget(w.s.target, rows)
		next = n
		d = w.diff(rows)
	}
	if !w.reset && (st.State == provider.StatusReady || st.State == provider.StatusStale) {
		d.Reset = true // the first rows, even none, then the status
		w.reset = true
	}
	if w.status == nil || !w.status.Equal(st) {
		c := st.Clone()
		w.status = &c
		d.Status = &st
	}
	if !d.Reset && len(d.Upserts) == 0 && len(d.Deletes) == 0 && d.Status == nil {
		return next
	}
	w.order.Lock()
	defer w.order.Unlock()
	if w.stopped {
		return time.Time{}
	}
	w.sink.Apply(d)
	return next
}

// diff compares rows with the last applied ones; on the first reading
// every row is an upsert.
func (w *composeWatch) diff(rows []core.Row) provider.Delta {
	var d provider.Delta
	seen := make(map[string]core.Row, len(rows))
	for _, r := range rows {
		seen[r.ID] = r
		if old, ok := w.rows[r.ID]; !ok || !sameRow(old, r) {
			d.Upserts = append(d.Upserts, r)
		}
	}
	for id := range w.rows {
		if _, ok := seen[id]; !ok {
			d.Deletes = append(d.Deletes, id)
		}
	}
	w.rows = seen
	return d
}

func sameRow(a, b core.Row) bool {
	return a.Rev == b.Rev && a.Ref == b.Ref && reflect.DeepEqual(a.Health, b.Health) && reflect.DeepEqual(a.Cells, b.Cells)
}

// aggregate is a view's status over its feeds: error > loading > stale >
// ready (the worst feed's class and message). Rows need a snapshot of
// every feed: a feed still loading hides the others' staleness.
func aggregate(fs []*feed) provider.ViewStatus {
	rank := map[provider.StatusState]int{provider.StatusReady: 0, provider.StatusStale: 1, provider.StatusLoading: 2, provider.StatusError: 3}
	worst := feedState{State: provider.StatusReady}
	for _, f := range fs {
		if st := f.state(); rank[st.State] > rank[worst.State] {
			worst = st
		}
	}
	return provider.ViewStatus{State: worst.State, Class: worst.Class, Message: worst.Message}
}

func (w *composeWatch) stop() {
	w.order.Lock()
	if w.stopped {
		w.order.Unlock()
		return
	}
	w.stopped = true
	w.order.Unlock()
	close(w.done)
	for _, f := range w.feeds {
		f.mu.Lock()
		delete(f.subs, w)
		f.mu.Unlock()
	}
	w.s.release(w.feeds)
}
