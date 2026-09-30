package compose

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/compose/engine"
)

// Logs (plan decision 7). A container's journal survives its restarts:
// a follow that ended because the container stopped waits in the
// containers feed for its next start and resumes after what it delivered
// (the cursor), never from the start again. stdout and stderr are the
// channels; with both, each is a source of the member.

var _ provider.LogSource = (*session)(nil)

const (
	channelStdout = "stdout"
	channelStderr = "stderr"
	// allTail is what "all lines" means: the UI keeps at most 50k per tab.
	allTail = 50_000
	// startupTail bounds the backlog of a member found after the start.
	startupTail = 10_000
	batchLines  = 500
	batchBytes  = 64 << 10
	// maxGroupMembers bounds the containers of one log tab.
	maxGroupMembers = 20
	backlogWorkers  = 4
	logRetryFirst   = time.Second
	logRetryCap     = 10 * time.Second
)

// Backlog memory (vars for tests): the budget is split between a group's
// members.
var (
	backlogBudget       = 16 << 20
	backlogMinPerMember = 512 << 10
)

// Timeouts (vars for tests).
var (
	backlogTimeout = 30 * time.Second
	onceTimeout    = 2 * time.Minute
	membersWait    = 30 * time.Second
)

// LogInfo: a container, or a service's containers, have the channels
// stdout and stderr (both by default); a TTY has one stream.
func (s *session) LogInfo(ctx context.Context, ref core.Ref) (core.LogInfo, error) {
	switch ref.Kind {
	case KindContainers:
		c, err := s.container(ctx, ref)
		if err != nil {
			return core.LogInfo{}, err
		}
		return streamsInfo([]*engine.ContainerInspect{c}, false), nil
	case KindServices:
		members, err := s.serviceMembers(ctx, ref)
		if err != nil {
			return core.LogInfo{}, err
		}
		return streamsInfo(members, true), nil
	}
	return core.LogInfo{}, &provider.Error{Class: provider.ClassUnsupported, Message: fmt.Sprintf("%s have no logs", ref.Kind)}
}

func streamsInfo(cs []*engine.ContainerInspect, aggregate bool) core.LogInfo {
	stream, all := msg("logs.stream"), msg("logs.allStreams")
	info := core.LogInfo{Aggregate: aggregate, ChannelLabel: &stream, AllChannelsLabel: &all}
	tty := len(cs) > 0
	for _, c := range cs {
		tty = tty && c.Config.Tty
	}
	if tty {
		// A terminal's output is one stream: stderr cannot be told apart.
		info.Channels = []core.LogChannel{{ID: channelStdout, Title: "terminal (stdout and stderr together)"}}
		info.DefaultChannel = channelStdout
		return info
	}
	info.Channels = []core.LogChannel{{ID: channelStdout, Title: channelStdout}, {ID: channelStderr, Title: channelStderr}}
	info.DefaultChannel = provider.ChannelAll
	return info
}

// container reads ref's container (a Compose one, the same incarnation).
func (s *session) container(ctx context.Context, ref core.Ref) (*engine.ContainerInspect, error) {
	c, err := s.cl.InspectContainer(ctx, ref.Name)
	switch {
	case engine.IsNotFound(err):
		return nil, &provider.Error{Class: provider.ClassNotFound, Message: "the container was removed"}
	case err != nil:
		return nil, providerError(err)
	case c.Config.Labels[LabelProject] == "":
		return nil, &provider.Error{Class: provider.ClassNotFound, Message: "the container is not part of a Compose project"}
	case ref.UID != "" && c.ID != ref.UID:
		return nil, &provider.Error{Class: provider.ClassGone, Message: "the container was removed and another took its name"}
	}
	return &c, nil
}

// serviceMembers reads the service's containers from the containers feed.
func (s *session) serviceMembers(ctx context.Context, ref core.Ref) ([]*engine.ContainerInspect, error) {
	project, service, ok := strings.Cut(ref.Name, "/")
	if !ok {
		return nil, &provider.Error{Class: provider.ClassInvalid, Message: fmt.Sprintf("%q is not a service key (project/service)", ref.Name)}
	}
	fs, err := s.acquire([]Feed{FeedContainers})
	if err != nil {
		return nil, err
	}
	defer s.release(fs)
	ctx, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()
	if err := waitAll(ctx, fs); err != nil {
		return nil, err
	}
	objs, _, _, _ := fs[0].observe()
	members := membersOf(objs, project, service)
	if len(members) == 0 {
		return nil, &provider.Error{Class: provider.ClassNotFound, Message: "the service has no containers"}
	}
	return members, nil
}

// membersOf: the service's containers (not one-off), in replica order.
func membersOf(objs map[string]any, project, service string) []*engine.ContainerInspect {
	var out []*engine.ContainerInspect
	for _, o := range objs {
		c := o.(*engine.ContainerInspect)
		l := c.Config.Labels
		if l[LabelProject] == project && l[LabelService] == service && l[LabelOneoff] != "True" {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := strconv.Atoi(out[i].Config.Labels[LabelNumber])
		b, _ := strconv.Atoi(out[j].Config.Labels[LabelNumber])
		if a != b {
			return a < b
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// StreamLogs streams a container's logs, or a service's containers' as
// one (members come and go with the containers feed).
func (s *session) StreamLogs(ctx context.Context, ref core.Ref, q provider.LogQuery, sink provider.LogSink) error {
	if q.Previous {
		return &provider.Error{Class: provider.ClassUnsupported, Message: "a Docker container keeps one log across its restarts; there is no separate previous log"}
	}
	sel, err := selectStreams(q.Channel)
	if err != nil {
		return err
	}
	g := &logGroup{s: s, q: q, sel: sel, sink: &lockedSink{sink: sink}}
	switch ref.Kind {
	case KindContainers:
		c, err := s.container(ctx, ref)
		if err != nil {
			return err
		}
		g.one = c.ID
	case KindServices:
		project, service, ok := strings.Cut(ref.Name, "/")
		if !ok {
			return &provider.Error{Class: provider.ClassInvalid, Message: fmt.Sprintf("%q is not a service key (project/service)", ref.Name)}
		}
		g.project, g.service = project, service
	default:
		return &provider.Error{Class: provider.ClassUnsupported, Message: fmt.Sprintf("%s have no logs", ref.Kind)}
	}
	fs, err := s.acquire([]Feed{FeedContainers})
	if err != nil {
		return err
	}
	defer s.release(fs)
	g.f = fs[0]
	return g.run(ctx)
}

// streams is which of a container's streams are asked for.
type streams struct{ out, err bool }

func selectStreams(channel string) (streams, error) {
	switch channel {
	case "", provider.ChannelAll:
		return streams{true, true}, nil
	case channelStdout:
		return streams{out: true}, nil
	case channelStderr:
		return streams{err: true}, nil
	}
	return streams{}, &provider.Error{Class: provider.ClassInvalid, Message: fmt.Sprintf("no log channel %q (stdout, stderr)", channel)}
}

// lockedSink serializes a group's members on one sink.
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

// logGroup streams a set of containers as one: one container (one), or a
// service's live members.
type logGroup struct {
	s    *session
	f    *feed
	q    provider.LogQuery
	sel  streams
	sink *lockedSink

	one              string // a single container's id
	project, service string

	nextID   int
	active   map[string]*member
	finished map[string]bool
	limited  string
	done     chan string
}

func (g *logGroup) run(ctx context.Context) (err error) {
	g.active, g.finished, g.done = map[string]*member{}, map[string]bool{}, make(chan string, maxGroupMembers)
	wctx, cancel := context.WithTimeout(ctx, membersWait)
	err = g.f.wait(wctx)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &provider.Error{Class: engine.ClassOf(err), Message: "cannot read the containers to show logs of: " + err.Error()}
	}
	initial, _, err := g.admit(false)
	if err != nil {
		return err
	}
	if err := g.backlog(ctx, initial); err != nil {
		return err
	}
	if err := g.sink.Ready(); err != nil {
		return err
	}
	if !g.q.Follow {
		for _, m := range initial {
			if m.sent.State == provider.LogError {
				continue // its error stays its last word
			}
			if err := m.state(provider.LogEnded, "", ""); err != nil {
				return err
			}
		}
		return nil
	}
	// Every exit stops the members first, then joins them.
	ctx, stop := context.WithCancelCause(ctx)
	var wg sync.WaitGroup
	defer func() {
		stop(err)
		wg.Wait()
	}()
	start := func(ms []*member) {
		for _, m := range ms {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := m.follow(ctx); err != nil {
					stop(err)
					return
				}
				select {
				case g.done <- m.cid:
				case <-ctx.Done():
				}
			}()
		}
	}
	start(initial)
	for {
		// Decide from one snapshot and wait for changes after that same
		// snapshot: a member that came meanwhile is never missed.
		late, changed, err := g.admit(true)
		if err != nil {
			return err
		}
		start(late)
		if g.one != "" && len(g.active) == 0 {
			return nil // the container's logs are over
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case cid := <-g.done:
			delete(g.active, cid)
			g.finished[cid] = true
		case <-changed:
		}
	}
}

// admit starts members up to the cap; late ones read their logs from
// their start. It returns the change channel of the snapshot it used.
func (g *logGroup) admit(late bool) ([]*member, <-chan struct{}, error) {
	objs, _, st, changed := g.f.observe()
	var cs []*engine.ContainerInspect
	if g.one != "" {
		if c, ok := objs[g.one]; ok {
			cs = []*engine.ContainerInspect{c.(*engine.ContainerInspect)}
		}
	} else {
		cs = membersOf(objs, g.project, g.service)
	}
	present := map[string]bool{}
	for _, c := range cs {
		present[c.ID] = true
	}
	for cid := range g.finished {
		if !present[cid] {
			delete(g.finished, cid) // a removed member is forgotten (no growth)
		}
	}
	var out []*member
	total := 0
	for _, c := range cs {
		if g.finished[c.ID] {
			continue
		}
		total++
		if g.active[c.ID] != nil || len(g.active) >= maxGroupMembers {
			continue
		}
		m, err := g.newMember(c, late)
		if err != nil {
			return nil, nil, err
		}
		g.active[c.ID] = m
		out = append(out, m)
	}
	if g.one != "" && len(out) == 0 && len(g.active) == 0 && !late {
		return nil, nil, &provider.Error{Class: provider.ClassNotFound, Message: "the container was removed"}
	}
	var notes []string
	if total > len(g.active) {
		notes = append(notes, fmt.Sprintf("showing %d of %d containers (at most %d at once)", len(g.active), total, maxGroupMembers))
	}
	if st.State == provider.StatusStale || st.State == provider.StatusError {
		notes = append(notes, "the containers are not being watched ("+st.Message+"): new, restarted or removed containers may not be noticed")
	}
	note := strings.Join(notes, "; ")
	if note != g.limited {
		g.limited = note
		s := provider.LogState{State: provider.LogStreaming}
		if note != "" {
			s = provider.LogState{State: provider.LogLimited, Message: note}
		}
		if err := g.sink.State(0, s); err != nil {
			return nil, nil, err
		}
	}
	return out, changed, nil
}

func (g *logGroup) newMember(c *engine.ContainerInspect, late bool) (*member, error) {
	m := &member{g: g, cid: c.ID, name: containerName(c), tty: c.Config.Tty, late: late}
	sel := g.sel
	if m.tty {
		sel = streams{out: sel.out || sel.err} // one stream: what the terminal showed
	}
	if sel.out {
		g.nextID++
		m.outID = g.nextID
		label := m.name
		if m.tty {
			label += " (tty)"
		}
		if err := g.sink.Source(m.outID, m.cid+"/"+channelStdout, label, channelStdout); err != nil {
			return nil, err
		}
	}
	if sel.err && !m.tty {
		g.nextID++
		m.errID = g.nextID
		label := m.name
		if sel.out {
			label += " (stderr)"
		}
		if err := g.sink.Source(m.errID, m.cid+"/"+channelStderr, label, channelStderr); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// backlog reads the members' recent lines in parallel (bounded time and
// bytes), merges them by time and keeps the newest N overall. What was
// read moves each member's cursor, so its follow resumes after it.
func (g *logGroup) backlog(ctx context.Context, ms []*member) error {
	if len(ms) == 0 {
		return nil
	}
	bctx, cancel := context.WithTimeout(ctx, backlogTimeout)
	if !g.q.Follow {
		cancel()
		bctx, cancel = context.WithTimeout(ctx, onceTimeout)
	}
	defer cancel()
	per := max(int64(backlogMinPerMember), int64(backlogBudget/len(ms)))
	work := make(chan *member)
	var wg sync.WaitGroup
	for range min(backlogWorkers, len(ms)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for m := range work {
				m.readBacklog(bctx, per)
			}
		}()
	}
	for _, m := range ms {
		work <- m
	}
	close(work)
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var lists [][]provider.LogLine
	var ids []int
	for _, m := range ms {
		for i, l := range m.backlogLines {
			m.seen.read(l, m.backlogPartial && i == len(m.backlogLines)-1, true)
		}
		m.opened = m.backlogErr == nil || len(m.backlogLines) > 0
		out, errs := m.split(m.backlogLines)
		if len(out) > 0 {
			lists, ids = append(lists, out), append(ids, m.outID)
		}
		if len(errs) > 0 {
			lists, ids = append(lists, errs), append(ids, m.errID)
		}
	}
	merged := mergeByTime(lists)
	if keep := tailOf(g.q); len(merged) > keep {
		merged = merged[len(merged)-keep:]
	}
	for i := 0; i < len(merged); {
		j, size := i, 0
		for j < len(merged) && merged[j].src == merged[i].src && j-i < batchLines && size < batchBytes {
			size += len(merged[j].line.Text) + len(merged[j].line.TS)
			j++
		}
		batch := make([]provider.LogLine, j-i)
		for k := i; k < j; k++ {
			batch[k-i] = merged[k].line
		}
		if err := g.sink.Lines(ids[merged[i].src], batch); err != nil {
			return err
		}
		i = j
	}
	for _, m := range ms {
		m.backlogLines = nil
		switch err := m.backlogErr; {
		case err != nil && errors.Is(err, context.DeadlineExceeded):
			if err := m.state(provider.LogError, provider.ClassUnavailable, "its recent lines did not load in time; following live"); err != nil {
				return err
			}
		case err != nil && engine.IsNotFound(err):
			if err := m.state(provider.LogEnded, "", "the container was removed"); err != nil {
				return err
			}
		case err != nil:
			if err := m.state(provider.LogError, engine.ClassOf(err), errText(err)); err != nil {
				return err
			}
		case m.backlogCut:
			if err := m.state(provider.LogTruncated, "", "only the first part of its recent lines fit the backlog budget; the rest follows"); err != nil {
				return err
			}
		}
		for _, gap := range m.backlogGaps {
			if err := m.gap(gap); err != nil {
				return err
			}
		}
		m.backlogGaps = nil
	}
	return nil
}

func tailOf(q provider.LogQuery) int {
	if q.TailLines == provider.TailAll || q.TailLines > allTail || q.TailLines <= 0 {
		return allTail
	}
	return q.TailLines
}

func errText(err error) string {
	var ee *engine.Error
	if errors.As(err, &ee) {
		return ee.Message
	}
	return err.Error()
}

// seenLog is what a member read of its journal, for resuming with since.
// The Engine's since is positional (measured on Docker 29): it finds the
// first journal line stamped at or after since and answers everything
// after it in journal order — including lines stamped a little earlier
// (stdout and stderr are stamped apart), lines outside the user's tail
// window and lines of the other stream. So neither a time nor a count
// proves where a replay is: a resume asks since the time of the last line
// read (the anchor — the answer surely holds it), drops the records it
// read before (known by time, stream and text) until the anchor, and
// delivers the rest; an unknown record before the anchor is delivered with
// a gap (a repeat or an old line beats a loss). Records equal in time (to
// the nanosecond), stream and text are one record to this: a rotation
// that dropped one of two such and a new third one would go unnoticed.
// And the ring is bounded: more than maxSeen records written just before
// the anchor yet stamped after it (within the stamps' skew) may repeat on
// a resume, after a gap.
type seenLog struct {
	ring  []recordID // the last maxSeen records read, circular
	next  int
	count map[recordID]int // of ring
	// anchor: the last record delivered outside a replay that had a time
	// (a replay does not move it: the delivered history ends there until
	// the replay meets it, even when the attempt breaks).
	anchor    recordID
	anchorAt  time.Time
	anchorEnd time.Time // its last message's time: where it ends in the journal
	hasAnchor bool
	// anchorN: records equal to the anchor read up to it (kept apart from
	// the ring, which may drop them).
	anchorN int
	// partial: the anchor was delivered without its newline (the
	// container stopped mid-line); the journal may continue it.
	partial bool
	// torn: the start of a line whose read was cut (its damaged prefix was
	// not delivered): the resume reads from it.
	torn       time.Time
	tornStream engine.Stream
}

// maxSeen bounds the records a member remembers: a replay's known part is
// what was written within the stamps' skew before the anchor.
const maxSeen = 512

// replaySkew bounds how much later than the end of the anchor (its last
// message) a record written before it can be stamped (stdout and stderr
// are stamped apart, by microseconds): an unknown record begun later than
// this is past the anchor — which is gone (rotated) when the replay has not
// met it. A heuristic: a daemon clock stepping back more than this during
// a replay's span could end one early (a repeat, after the gap).
const replaySkew = time.Second

// recordID tells records apart: time, stream, and the text's length and
// hash.
type recordID struct {
	at     int64
	stream engine.Stream
	n      int
	sum    uint64
}

func idOf(stream engine.Stream, at time.Time, text string) recordID {
	id := recordID{stream: stream, n: len(text), sum: textSum(text)}
	if !at.IsZero() {
		id.at = at.UnixNano()
	}
	return id
}

// textSum is FNV-1a.
func textSum(text string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(text); i++ {
		h ^= uint64(text[i])
		h *= 1099511628211
	}
	return h
}

// read records a delivered record; move: it is now the anchor (not while
// a replay is before the anchor — the delivered history ends there until
// the replay meets it, even when this attempt breaks).
func (s *seenLog) read(l logLine, partial, move bool) {
	id := idOf(l.stream, l.at, l.line.Text)
	if s.count == nil {
		s.count = make(map[recordID]int)
	}
	if len(s.ring) < maxSeen {
		s.ring = append(s.ring, id)
	} else {
		old := s.ring[s.next]
		if s.count[old]--; s.count[old] <= 0 {
			delete(s.count, old)
		}
		s.ring[s.next] = id
		s.next = (s.next + 1) % maxSeen
	}
	s.count[id]++
	if move {
		s.moveAnchor(id, l.at, l.end, partial)
	}
	if !s.torn.IsZero() && l.stream == s.tornStream && l.at.Equal(s.torn) {
		s.torn = time.Time{} // the cut line came whole
	}
}

// moveAnchor: id is the new anchor; its count is of the records equal to
// it that were read (the ring's, at least one).
func (s *seenLog) moveAnchor(id recordID, at, end time.Time, partial bool) {
	if at.IsZero() {
		return
	}
	if end.IsZero() || end.Before(at) {
		end = at
	}
	s.anchor, s.anchorAt, s.anchorEnd, s.hasAnchor, s.partial = id, at, end, true, partial
	s.anchorN = max(s.count[id], 1)
}

// tear notes a line whose read was cut before its end.
func (s *seenLog) tear(stream engine.Stream, at time.Time) {
	if !at.IsZero() && (s.torn.IsZero() || at.Before(s.torn)) {
		s.torn, s.tornStream = at, stream
	}
}

// resumable: there is a position to resume from.
func (s *seenLog) resumable() bool { return s.hasAnchor || !s.torn.IsZero() }

func (s *seenLog) since() time.Time {
	if !s.torn.IsZero() && (!s.hasAnchor || s.torn.Before(s.anchorAt)) {
		return s.torn
	}
	return s.anchorAt
}

// replay starts matching an answer since since().
func (s *seenLog) replay() *replay {
	r := &replay{s: s, done: !s.hasAnchor, anchor: s.anchor, anchorEnd: s.anchorEnd, anchorLeft: s.anchorN, partial: s.partial, known: make(map[recordID]int, len(s.count))}
	for id, n := range s.count {
		if id != s.anchor {
			r.known[id] = n
		}
	}
	return r
}

// replay matches the start of a resumed answer against what was read.
type replay struct {
	s          *seenLog
	known      map[recordID]int // besides the anchor
	anchor     recordID
	anchorEnd  time.Time
	anchorLeft int
	partial    bool
	done       bool // the anchor was met (or is gone): the rest is new
	gapped     bool
	// last: the last record of the answer (dropped or delivered), the
	// position when the anchor turns out gone at the answer's end.
	last        recordID
	lastAt      time.Time
	lastEnd     time.Time
	lastPartial bool
}

// abandon ends a replay that read the whole answer without meeting the
// anchor (rotated away): the answer's last record is the new position.
func (r *replay) abandon() {
	if r == nil || r.done {
		return
	}
	r.done = true
	r.s.moveAnchor(r.last, r.lastAt, r.lastEnd, r.lastPartial)
}

// check decides a record of the answer: drop (read before), or deliver
// its text from skip on (a continued partial anchor: its new part);
// unknown: delivered before the anchor was met.
func (r *replay) check(l logLine, partial bool) (drop bool, skip int, unknown bool) {
	if r == nil || r.done {
		return false, 0, false
	}
	id := idOf(l.stream, l.at, l.line.Text)
	if !l.at.IsZero() {
		r.last, r.lastAt, r.lastEnd, r.lastPartial = id, l.at, l.end, partial
	}
	if id == r.anchor && r.anchorLeft > 0 {
		if r.anchorLeft--; r.anchorLeft == 0 {
			r.done = true
		}
		return true, 0, false
	}
	if r.known[id] > 0 {
		r.known[id]--
		return true, 0, false
	}
	a := r.anchor
	if r.partial && id.stream == a.stream && id.at == a.at && len(l.line.Text) > a.n && textSum(l.line.Text[:a.n]) == a.sum {
		r.done = true
		return false, a.n, false
	}
	if !r.s.torn.IsZero() && l.stream == r.s.tornStream && l.at.Equal(r.s.torn) {
		return false, 0, false // the cut line, expected
	}
	if l.at.Sub(r.anchorEnd) > replaySkew {
		r.done = true // begun after the anchor ended: the anchor is gone
	}
	return false, 0, true
}

// logLine is a record as delivered: the line and its stream and time (at:
// its start; end: its last message's).
type logLine struct {
	line   provider.LogLine
	stream engine.Stream
	at     time.Time
	end    time.Time
}

// member streams one container.
type member struct {
	g            *logGroup
	cid, name    string
	tty          bool
	outID, errID int // 0: not shown
	late         bool
	opened       bool // a request was answered
	seen         seenLog
	sent         provider.LogState

	backlogLines   []logLine
	backlogPartial bool // the last backlog line is a clean partial one
	backlogCut     bool
	backlogErr     error
	backlogGaps    []string
}

func (m *member) ids() []int {
	var out []int
	for _, id := range []int{m.outID, m.errID} {
		if id != 0 {
			out = append(out, id)
		}
	}
	return out
}

// state reports a state on the member's sources (unchanged ones are not
// repeated).
func (m *member) state(state string, class provider.ErrorClass, text string) error {
	st := provider.LogState{State: state, Class: class, Message: text}
	if st == m.sent {
		return nil
	}
	m.sent = st
	for _, id := range m.ids() {
		if err := m.g.sink.State(id, st); err != nil {
			return err
		}
	}
	return nil
}

// gap: lines around here may be missing or repeated; the state after it
// is sent again.
func (m *member) gap(why string) error {
	m.sent = provider.LogState{}
	for _, id := range m.ids() {
		if err := m.g.sink.State(id, provider.LogState{State: provider.LogGap, Message: why}); err != nil {
			return err
		}
	}
	return nil
}

// errFinished ends a member whose final state was reported.
var errFinished = errors.New("member finished")

func (m *member) finish(state string, class provider.ErrorClass, text string) error {
	if err := m.state(state, class, text); err != nil {
		return err
	}
	return errFinished
}

// split sorts a member's lines to its sources (a TTY's all go to out).
func (m *member) split(ls []logLine) (out, errs []provider.LogLine) {
	for _, l := range ls {
		if l.stream == engine.Stderr && !m.tty {
			if m.errID != 0 {
				errs = append(errs, l.line)
			}
			continue
		}
		if m.outID != 0 {
			out = append(out, l.line)
		}
	}
	return out, errs
}

func (m *member) options(follow bool) engine.LogsOptions {
	sel := m.g.sel
	return engine.LogsOptions{Follow: follow, Timestamps: true, Stdout: sel.out || m.tty, Stderr: sel.err || m.tty}
}

// open takes one of the app's log request slots for the life of the
// response (waiting, and saying so, while all are taken).
func (m *member) open(ctx context.Context, o engine.LogsOptions) (*engine.LogReader, func(), error) {
	slots := m.g.s.logSlots
	if slots != nil {
		select {
		case slots <- struct{}{}:
		default:
			if err := m.state(provider.LogWaiting, "", fmt.Sprintf("%d log streams are open at once; waiting for one to close", cap(slots))); err != nil {
				return nil, nil, err
			}
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			}
		}
	}
	release := func() {
		if slots != nil {
			<-slots
		}
	}
	lr, err := m.g.s.cl.ContainerLogs(ctx, m.cid, o, m.tty)
	if err != nil {
		release()
		return nil, nil, err
	}
	var once sync.Once
	return lr, func() { _ = lr.Close(); once.Do(release) }, nil
}

// readBacklog: a non-follow request for the user's window (late members:
// from their start), bounded by per bytes of lines.
func (m *member) readBacklog(ctx context.Context, per int64) {
	o := m.options(false)
	o.Tail = tailOf(m.g.q)
	o.Since = m.g.q.SinceTime
	if m.late {
		o.Tail = startupTail
	}
	lr, closeFn, err := m.open(ctx, o)
	if err != nil {
		m.backlogErr = err
		return
	}
	defer closeFn()
	var size int64
	for {
		rec, err := lr.Next()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				m.backlogErr = err
			}
			return
		}
		if rec.Gap != "" {
			m.backlogGaps = append(m.backlogGaps, rec.Gap)
			continue
		}
		if rec.Partial && !lr.EndedCleanly() {
			m.seen.tear(rec.Stream, rec.Time)
			continue // a damaged prefix: the follow reads the line whole
		}
		m.backlogLines = append(m.backlogLines, toLine(rec))
		m.backlogPartial = rec.Partial
		size += int64(len(rec.Line)) + lineKeep
		if size >= per {
			m.backlogCut = true
			return
		}
	}
}

// lineKeep is what keeping a backlog line costs besides its text (the
// record, its time text, the merge's copies).
const lineKeep = 192

func toLine(rec engine.LogRecord) logLine {
	l := provider.LogLine{Text: string(rec.Line)}
	if rec.Time.IsZero() {
		l.Flags |= provider.LineNoTime
	} else {
		l.TS = rec.Time.UTC().Format(time.RFC3339Nano)
	}
	if rec.Truncated {
		l.Flags |= provider.LineCut
	}
	return logLine{line: l, stream: rec.Stream, at: rec.Time, end: rec.End}
}

// follow streams the member live until it is removed, ctx ends (nil) or
// the sink fails (its error).
func (m *member) follow(ctx context.Context) error {
	err := m.followLoop(ctx)
	if errors.Is(err, errFinished) || ctx.Err() != nil {
		return nil
	}
	return err
}

func (m *member) followLoop(ctx context.Context) error {
	backoff := logRetryFirst
	for {
		obj, snap, changed := m.g.f.lookup(m.cid)
		c, _ := obj.(*engine.ContainerInspect)
		if snap && c == nil {
			return m.finish(provider.LogEnded, "", "the container was removed")
		}
		if c != nil && c.HostConfig.LogConfig.Type == "none" {
			return m.finish(provider.LogError, provider.ClassUnsupported, "the container's log driver (none) keeps no logs")
		}
		o := m.options(true)
		var rp *replay
		switch {
		case m.opened && m.seen.resumable():
			o.Since = m.seen.since()
			rp = m.seen.replay()
		case m.opened:
			o.Tail, o.Since = tailOf(m.g.q), m.g.q.SinceTime // nothing delivered yet: the same window
		case m.late:
			o.Tail, o.Since = startupTail, m.g.q.SinceTime
		default:
			o.Tail, o.Since = tailOf(m.g.q), m.g.q.SinceTime
		}
		lr, closeFn, err := m.open(ctx, o)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if engine.IsNotFound(err) {
				return m.finish(provider.LogEnded, "", "the container was removed")
			}
			if engine.ClassOf(err) == provider.ClassForbidden {
				return m.finish(provider.LogError, provider.ClassForbidden, errText(err))
			}
			if err := m.state(provider.LogError, engine.ClassOf(err), errText(err)); err != nil {
				return err
			}
			if err := wait(ctx, changed, jitter(backoff)); err != nil {
				return err
			}
			backoff = min(backoff*2, logRetryCap)
			continue
		}
		m.opened = true
		if err := m.state(provider.LogStreaming, "", ""); err != nil {
			closeFn()
			return err
		}
		n, failed, err := m.pump(ctx, lr, rp)
		closeFn()
		if err != nil {
			return err
		}
		if n > 0 {
			backoff = logRetryFirst
		}
		if failed {
			// the error stays said; the journal is read again whatever
			// the container's state (a stopped one's rest is still there)
			obj, snap, changed := m.g.f.lookup(m.cid)
			if snap && obj == nil {
				return m.finish(provider.LogEnded, "", "the container was removed")
			}
			if err := wait(ctx, changed, jitter(backoff)); err != nil {
				return err
			}
			backoff = min(backoff*2, logRetryCap)
			continue
		}
		if err := m.afterEnd(ctx, &backoff); err != nil {
			return err
		}
	}
}

// afterEnd decides what the end of a follow means, from the containers
// feed: removed → ended; running → the connection broke (reopen after a
// backoff, or at once on a change); stopped → wait for its next start.
func (m *member) afterEnd(ctx context.Context, backoff *time.Duration) error {
	for {
		obj, snap, changed := m.g.f.lookup(m.cid)
		c, _ := obj.(*engine.ContainerInspect)
		switch {
		case snap && c == nil:
			return m.finish(provider.LogEnded, "", "the container was removed")
		case c == nil || c.State.Running || c.State.Restarting:
			if err := wait(ctx, changed, jitter(*backoff)); err != nil {
				return err
			}
			*backoff = min(*backoff*2, logRetryCap)
			return nil
		}
		if err := m.state(provider.LogWaiting, "", stoppedText(c)); err != nil {
			return err
		}
		if err := wait(ctx, changed, 0); err != nil {
			return err
		}
	}
}

func stoppedText(c *engine.ContainerInspect) string {
	switch c.State.Status {
	case "created":
		return "the container has not started yet"
	case "paused":
		return "the container is paused"
	case "exited":
		return fmt.Sprintf("the container exited (code %d); its log continues when it starts again", c.State.ExitCode)
	}
	return "the container is " + c.State.Status + "; its log continues when it starts again"
}

// pump reads one response into the sink; it returns the lines delivered,
// whether the read failed (said), and an error only when the sink or ctx
// failed.
func (m *member) pump(ctx context.Context, lr *engine.LogReader, rp *replay) (int, bool, error) {
	// One batch of one source at a time: stdout and stderr lines reach
	// the sink in the order they arrived.
	var batch []provider.LogLine
	batchID, size, n := 0, 0, 0
	flush := func() error {
		if len(batch) > 0 {
			if err := m.g.sink.Lines(batchID, batch); err != nil {
				return err
			}
		}
		batch, size = nil, 0
		return nil
	}
	for {
		rec, err := lr.Next()
		if err != nil {
			if ferr := flush(); ferr != nil {
				return n, false, ferr
			}
			if ctx.Err() != nil {
				return n, false, ctx.Err()
			}
			if !errors.Is(err, io.EOF) {
				// a broken read or the daemon's own error in the stream: said,
				// then decided like any end (the cursor stays)
				if serr := m.state(provider.LogError, engine.ClassOf(err), errText(err)); serr != nil {
					return n, false, serr
				}
				return n, true, nil
			}
			if !lr.EndedCleanly() {
				// cut inside a frame (the gap was said): read again, the
				// journal still holds the line whatever the container's state
				return n, true, nil
			}
			if rp != nil && !rp.done && !rp.gapped {
				// the answer ended before replaying what was delivered
				if gerr := m.gap("lines around the reconnect may be missing or repeated (the log was rotated?)"); gerr != nil {
					return n, false, gerr
				}
			}
			rp.abandon()
			return n, false, nil
		}
		if rec.Gap != "" {
			if err := flush(); err != nil {
				return n, false, err
			}
			if err := m.gap(rec.Gap); err != nil {
				return n, false, err
			}
			if err := m.state(provider.LogStreaming, "", ""); err != nil {
				return n, false, err
			}
			continue
		}
		if rec.Partial && !lr.EndedCleanly() {
			m.seen.tear(rec.Stream, rec.Time)
			continue // the next request replays it whole
		}
		l := toLine(rec)
		drop, skip, unknown := rp.check(l, rec.Partial)
		if drop {
			continue
		}
		if unknown && !rp.gapped {
			rp.gapped = true
			if err := flush(); err != nil {
				return n, false, err
			}
			if err := m.gap("lines around the reconnect may be missing, repeated or out of order (the log was rotated?)"); err != nil {
				return n, false, err
			}
			if err := m.state(provider.LogStreaming, "", ""); err != nil {
				return n, false, err
			}
		}
		m.seen.read(l, rec.Partial, rp == nil || rp.done)
		l.line.Text = l.line.Text[skip:]
		to := m.outID
		if l.stream == engine.Stderr && !m.tty {
			to = m.errID
		}
		if to != 0 {
			if to != batchID {
				if err := flush(); err != nil {
					return n, false, err
				}
				batchID = to
			}
			batch = append(batch, l.line)
			size += len(l.line.Text)
		}
		n++
		// Nothing more decoded: the next read may wait for the daemon
		// (bytes of an incomplete frame do not count) — deliver now.
		if len(batch) >= batchLines || size >= batchBytes || lr.Pending() == 0 {
			if err := flush(); err != nil {
				return n, false, err
			}
		}
	}
}

// wait blocks until ctx ends, changed fires (if not nil) or d passes (if > 0).
func wait(ctx context.Context, changed <-chan struct{}, d time.Duration) error {
	var timer <-chan time.Time
	if d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		timer = t.C
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-changed:
	case <-timer:
	}
	return nil
}

func jitter(d time.Duration) time.Duration {
	return d/2 + rand.N(d/2+1) //nolint:gosec // backoff jitter
}

// mergedLine is a line with the index of its list.
type mergedLine struct {
	src  int
	line provider.LogLine
}

// mergeByTime merges lists (each in its own order) by timestamp; a line
// without one sorts with the line before it.
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

func tsTime(ts string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, ts)
	return t
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
