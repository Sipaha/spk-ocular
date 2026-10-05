package kubernetes

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/provider"
)

const (
	// maxGroupStreams bounds container streams per log tab (not pods: "all
	// containers" of 20 pods would be more).
	maxGroupStreams = 20
	backlogWorkers  = 4
	// A slow source does not hold the others' backlog back (it continues
	// live).
	backlogTimeoutDefault = 30 * time.Second
	// backlogBudget is split between a group's sources (limitBytes).
	backlogBudget       = 16 << 20
	backlogMinPerSource = 512 << 10
	// groupSyncTimeout: how long to wait for the pod list of a group.
	groupSyncTimeout = 30 * time.Second
)

// backlogTimeout bounds the initial backlog of a group (a var for tests).
var backlogTimeout = backlogTimeoutDefault

// lockedSink serializes a group's sources on one sink.
type lockedSink struct {
	mu   sync.Mutex
	sink provider.LogSink
}

func (l *lockedSink) Source(id int, key, label, channel string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sink.Source(id, key, label, channel)
}

func (l *lockedSink) Lines(id int, lines []provider.LogLine) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sink.Lines(id, lines)
}

func (l *lockedSink) State(id int, st provider.LogState) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sink.State(id, st)
}

func (l *lockedSink) Ready() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sink.Ready()
}

// logGroup streams several container streams as one: a workload's pods,
// or all containers of a pod.
type logGroup struct {
	s       *session
	ns      string
	q       provider.LogQuery
	sink    *lockedSink
	tr      *groupTracker
	channel func(m memberPod) []string
	onePod  bool // all containers of one pod (not a workload)

	nextID   int
	active   map[string]*groupSrc
	finished map[string]bool
	limited  string // the last "showing N of K" message
	done     chan string
}

type groupSrc struct {
	key   string
	pod   memberPod
	ctr   string
	src   *podSource
	lines []provider.LogLine // backlog
	cut   bool               // the backlog hit its byte budget
	err   error              // the backlog request failed
}

func (g *logGroup) run(ctx context.Context) (err error) {
	g.active, g.finished, g.done = map[string]*groupSrc{}, map[string]bool{}, make(chan string, maxGroupStreams)
	if err := g.awaitMembers(ctx); err != nil {
		return err
	}
	initial, _, err := g.admit(false)
	if err != nil {
		return err
	}
	if g.q.Archive {
		for _, x := range initial {
			if obs, synced, _, _ := x.pod.box.get(); synced && (!obs.exists || obs.uid != x.pod.uid) {
				if err := x.src.requestFailedState(errMemberGone); err != nil {
					return err
				}
				continue
			}
			if err := x.src.run(ctx); err != nil {
				return err
			}
		}
		return g.sink.Ready()
	}
	if err := g.backlog(ctx, initial); err != nil {
		return err
	}
	if err := g.sink.Ready(); err != nil {
		return err
	}
	if !g.q.Follow {
		return nil
	}
	// Every exit stops the sources first, then joins them (a source blocked
	// on a quiet pod only returns when cancelled). A source whose sink
	// failed (the page stopped reading) ends the group.
	ctx, cancel := context.WithCancelCause(ctx)
	var wg sync.WaitGroup
	defer func() {
		cancel(err)
		wg.Wait()
	}()
	start := func(gs []*groupSrc) {
		for _, x := range gs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := x.src.run(ctx); err != nil {
					cancel(err)
					return
				}
				select {
				case g.done <- x.key:
				case <-ctx.Done():
				}
			}()
		}
	}
	start(initial)
	for {
		// Reconcile with the current member set, and wait for changes after
		// that same snapshot: a pod that came during the backlog (or between
		// two waits) is never missed.
		late, changed, err := g.admit(true)
		if err != nil {
			return err
		}
		start(late)
		if g.over() {
			return nil
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case key := <-g.done:
			delete(g.active, key)
			g.finished[key] = true
		case <-changed:
		}
	}
}

// over: the group of one pod ("all containers") ends when the pod is gone
// and all its sources have finished; a workload waits for future pods.
func (g *logGroup) over() bool {
	if !g.onePod || len(g.active) > 0 {
		return false
	}
	members, _, _, _ := g.tr.snapshot()
	return len(members) == 0
}

// awaitMembers waits for the group's pod list (both caches of a
// deployment) before deciding anything about membership.
func (g *logGroup) awaitMembers(ctx context.Context) error {
	limit := time.NewTimer(groupSyncTimeout)
	defer limit.Stop()
	for {
		_, synced, blind, changed := g.tr.snapshot()
		if synced {
			return nil
		}
		if blind != "" {
			class, msg, _ := strings.Cut(blind, ": ")
			return &provider.Error{Class: provider.ErrorClass(class), Message: "cannot list the pods to show logs of: " + msg}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-limit.C:
			return &provider.Error{Class: provider.ClassUnavailable, Message: "the pod list did not load in time"}
		case <-changed:
		}
	}
}

// admit starts sources for candidates up to the cap: newest pods first,
// containers in pod order; admitted sources stay until they end, freed
// slots are refilled. late sources read new instances from their start.
// It returns the change channel of the snapshot it decided from.
func (g *logGroup) admit(late bool) ([]*groupSrc, <-chan struct{}, error) {
	limit := maxGroupStreams
	if g.q.Archive {
		limit = 1000
	}
	members, _, blind, changed := g.tr.snapshot()
	// A finished source stays finished only while its pod does: the key of
	// a pod that left the inventory is forgotten (no growth over rollouts).
	present := map[string]bool{}
	for _, m := range members {
		present[m.uid] = true
	}
	for key := range g.finished {
		if uid, _, _ := strings.Cut(key, "/"); !present[uid] {
			delete(g.finished, key)
		}
	}
	var out []*groupSrc
	total := 0
	for _, m := range members {
		for _, ctr := range g.channel(m) {
			key := m.uid + "/" + ctr
			if g.finished[key] {
				continue
			}
			total++
			if g.active[key] != nil || len(g.active) >= limit {
				continue
			}
			g.nextID++
			x := &groupSrc{key: key, pod: m, ctr: ctr}
			x.src = newPodSource(g.nextID, g.ns, m.name, m.uid, ctr, g.s.logs, m.box, g.sink, g.q)
			x.src.slots, x.src.late = g.s.slots, late
			if err := g.sink.Source(x.src.id, key, m.name+"/"+ctr, ctr); err != nil {
				return nil, nil, err
			}
			g.active[key] = x
			out = append(out, x)
		}
	}
	var notes []string
	if total > len(g.active) {
		notes = append(notes, fmt.Sprintf("showing %d of %d container streams (at most %d at once)", len(g.active), total, limit))
	}
	if blind != "" {
		notes = append(notes, "the pod list is not being watched ("+blind+"): new or replaced pods will not appear")
	}
	msg := strings.Join(notes, "; ")
	if msg != g.limited {
		g.limited = msg
		st := provider.LogState{State: provider.LogStreaming}
		if msg != "" {
			st = provider.LogState{State: provider.LogLimited, Message: msg}
		}
		if err := g.sink.State(0, st); err != nil {
			return nil, nil, err
		}
	}
	return out, changed, nil
}

// backlog reads the sources' recent lines in parallel (bounded time and
// bytes), merges them by timestamp and keeps the newest N overall. Every
// line read counts as consumed by its source's cursor, so the follow that
// comes next resumes after it.
func (g *logGroup) backlog(ctx context.Context, srcs []*groupSrc) error {
	if len(srcs) == 0 {
		return nil
	}
	bctx, cancel := context.WithTimeout(ctx, backlogTimeout)
	defer cancel()
	per := max(int64(backlogMinPerSource), int64(backlogBudget/len(srcs)))
	work := make(chan *groupSrc)
	var wg sync.WaitGroup
	for range min(backlogWorkers, len(srcs)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for x := range work {
				x.lines, x.cut, x.err = g.readBacklog(bctx, x, per)
			}
		}()
	}
	for _, x := range srcs {
		work <- x
	}
	close(work)
	wg.Wait()
	if ctx.Err() != nil && g.q.Follow {
		return ctx.Err()
	}
	// Without follow (a tail) the caller's deadline ends the backlog: what
	// the others read is delivered, the late ones say they did not load.

	lists := make([][]provider.LogLine, len(srcs))
	for i, x := range srcs {
		lists[i] = x.lines
		// What was read is shown, even from an answer that broke off: the
		// follow resumes after it (the cursor), never replays it.
		x.src.opened = x.err == nil || len(x.lines) > 0
		if obs, _, _, _ := x.pod.box.get(); x.src.opened {
			x.src.incarnation = obs.ctrs[x.ctr].id
		}
		for _, l := range x.lines {
			if l.TS != "" {
				x.src.cur.add(l.TS)
			}
		}
	}
	merged := mergeByTime(lists)
	if keep := int(tailOf(g.q)); len(merged) > keep {
		merged = merged[len(merged)-keep:]
	}
	for i := 0; i < len(merged); {
		// runs of one source, bounded by lines and bytes like live batches
		// (the page refuses oversized frames)
		j, size := i, 0
		for j < len(merged) && merged[j].src == merged[i].src && j-i < batchLines && size < batchBytes {
			size += len(merged[j].line.Text) + len(merged[j].line.TS)
			j++
		}
		batch := make([]provider.LogLine, j-i)
		for k := i; k < j; k++ {
			batch[k-i] = merged[k].line
		}
		if err := g.sink.Lines(srcs[merged[i].src].src.id, batch); err != nil {
			return err
		}
		i = j
	}
	merged = nil
	for _, x := range srcs {
		x.lines = nil // emitted: do not keep up to 16 MiB alive while following
	}
	for _, x := range srcs {
		switch {
		case x.err != nil && errors.Is(x.err, context.DeadlineExceeded):
			if err := g.sink.State(x.src.id, provider.LogState{State: provider.LogError, Class: provider.ClassUnavailable, Message: lateBacklog(g.q)}); err != nil {
				return err
			}
		case x.err != nil:
			if err := x.src.requestFailedState(x.err); err != nil {
				return err
			}
		case x.cut:
			if err := g.sink.State(x.src.id, provider.LogState{State: provider.LogTruncated, Message: "only the first part of its recent lines fit the backlog budget; the rest follows"}); err != nil {
				return err
			}
		}
	}
	return nil
}

// readBacklog: a non-follow request limited to per bytes; complete lines
// only when the limit was hit (the cut line comes with the follow).
func (g *logGroup) readBacklog(ctx context.Context, x *groupSrc, per int64) ([]provider.LogLine, bool, error) {
	// the queue may have waited: never read a replacement by its name
	if obs, synced, _, _ := x.pod.box.get(); synced && (!obs.exists || obs.uid != x.pod.uid) {
		return nil, false, errMemberGone
	}
	rc, err := x.src.open(ctx, podLogRequest{
		Namespace: g.ns, Pod: x.pod.name, Container: x.ctr, Previous: g.q.Previous,
		TailLines: tailOf(g.q), SinceTime: g.q.SinceTime, LimitBytes: per,
	})
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rc.Close() }()
	cr := &countingReader{r: rc}
	lr := newLineReader(cr)
	var out []provider.LogLine
	for {
		l, complete, rerr := lr.next()
		limitHit := cr.n >= per
		if complete || (errors.Is(rerr, io.EOF) && !limitHit && (l.Text != "" || l.TS != "")) {
			out = append(out, l)
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return out, limitHit, nil
			}
			return out, false, rerr
		}
	}
}

// lateBacklog: a source whose recent lines did not come in time.
func lateBacklog(q provider.LogQuery) string {
	if q.Follow {
		return "its recent lines did not load in time; following live"
	}
	return "its recent lines did not load in time"
}

var errMemberGone = &provider.Error{Class: provider.ClassNotFound, Message: "the pod was deleted"}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// requestFailedState reports a failed backlog request of a source that
// will still be followed (its own follow retries or ends it).
func (s *podSource) requestFailedState(err error) error {
	class, msg := classify(err)
	st := provider.LogState{State: provider.LogError, Class: class, Message: msg}
	if kindOfLogError(err) == logErrWaiting {
		st = provider.LogState{State: provider.LogWaiting, Message: statusMessage(err)}
	}
	s.sent = st
	return s.sink.State(s.id, st)
}

// mergedLine is a line with the index of its source.
type mergedLine struct {
	src  int
	line provider.LogLine
}

// mergeByTime merges per-source lists (each in its file order) by
// timestamp; a line without one sorts with the line before it. Order
// within a source is kept even where its timestamps go backwards.
func mergeByTime(lists [][]provider.LogLine) []mergedLine {
	h := &mergeHeap{}
	total := 0
	for i, l := range lists {
		total += len(l)
		if len(l) > 0 {
			heap.Push(h, &mergeHead{src: i, list: l, at: tsTime(l[0].TS)})
		}
	}
	out := make([]mergedLine, 0, total)
	for h.Len() > 0 {
		hd := (*h)[0]
		out = append(out, mergedLine{src: hd.src, line: hd.list[hd.i]})
		hd.i++
		if hd.i == len(hd.list) {
			heap.Pop(h)
			continue
		}
		if ts := hd.list[hd.i].TS; ts != "" {
			hd.at = tsTime(ts)
		}
		heap.Fix(h, 0)
	}
	return out
}

type mergeHead struct {
	src  int
	list []provider.LogLine
	i    int
	at   time.Time
}

type mergeHeap []*mergeHead

func (h mergeHeap) Len() int { return len(h) }
func (h mergeHeap) Less(i, j int) bool {
	if !h[i].at.Equal(h[j].at) {
		return h[i].at.Before(h[j].at)
	}
	return h[i].src < h[j].src
}
func (h mergeHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *mergeHeap) Push(x any)   { *h = append(*h, x.(*mergeHead)) }
func (h *mergeHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}
