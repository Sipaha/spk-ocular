package api

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// What agents (P14, internal/agentapi) need of the service beyond the UI's
// API: a hold on a target's session for one call, rows without a view of
// the UI, a bounded tail of logs, metrics of rows read, the target's
// identity. The rights are checked by agentapi before any of this.

// AgentCall is an agent's hold on a target's session for one call: while
// it lasts, and for sessionIdle after, the UI selecting another target
// does not close the session (the reaper spares it as well). Closing the
// session otherwise (a configuration change, exit) ends the call's work:
// its context is cancelled and it answers gone.
type AgentCall struct {
	s      *Service
	e      *sessionEntry
	target core.Target
	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once
}

// maxAgentWatches bounds the watches agents' snapshots run at once (per
// process); more wait within their call's time, then limit.
const maxAgentWatches = 4

// snapshotWait bounds one watch of a snapshot (a var for tests).
var snapshotWait = 20 * time.Second

// Log tails: lines and bytes of text at most (vars for tests).
var (
	maxTailLines = 5000
	maxTailBytes = 1 << 20
	tailWait     = 20 * time.Second
)

// AgentCall takes the target's session for a call bound to ctx; Done ends
// it.
func (s *Service) AgentCall(ctx context.Context, providerID, target string) (*AgentCall, error) {
	t, err := s.targetOf(ctx, providerID, target)
	if err != nil {
		return nil, err
	}
	e, err := s.sessionFor(ctx, providerID, target)
	if err != nil {
		return nil, err
	}
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	if s.sessions[ownerKey(providerID, target)] != e {
		return nil, coded(CodeGone, errors.New("the session was replaced; retry"))
	}
	c := &AgentCall{s: s, e: e, target: t}
	c.ctx, c.cancel = context.WithCancel(ctx)
	if e.agentCalls == nil {
		e.agentCalls = map[*AgentCall]struct{}{}
	}
	e.agentCalls[c] = struct{}{}
	now := s.now()
	e.lastUsed, e.agentUntil = now, now.Add(sessionIdle)
	return c, nil
}

// targetOf is the target as discovered now.
func (s *Service) targetOf(ctx context.Context, providerID, target string) (core.Target, error) {
	p, ok := s.reg.Get(providerID)
	if !ok {
		return core.Target{}, coded(CodeNotFound, fmt.Errorf("unknown provider %q", providerID))
	}
	d, err := p.Discover(ctx)
	if err != nil {
		return core.Target{}, coded(CodeInternal, err)
	}
	for _, t := range d.Targets {
		if t.ID == target {
			return t, nil
		}
	}
	return core.Target{}, coded(CodeNotFound, fmt.Errorf("no target %q in %s", target, p.Title()))
}

// Done ends the call; the session stays spared for sessionIdle.
func (c *AgentCall) Done() {
	c.once.Do(func() {
		c.s.sessMu.Lock()
		delete(c.e.agentCalls, c)
		now := c.s.now()
		c.e.lastUsed, c.e.agentUntil = now, now.Add(sessionIdle)
		c.s.sessMu.Unlock()
		c.cancel()
	})
}

// Context ends with the call (Done), its caller, or the session.
func (c *AgentCall) Context() context.Context { return c.ctx }

// Title is the target's title.
func (c *AgentCall) Title() string { return c.target.Title }

// Kinds: the session's kinds now.
func (c *AgentCall) Kinds() []core.KindDescriptor { return c.e.sess.Kinds() }

// Kind is the session's kind id: unsupported when unknown, removed when
// it is no longer served.
func (c *AgentCall) Kind(id string) (core.KindDescriptor, error) {
	for _, k := range c.e.sess.Kinds() {
		if k.ID == id {
			return k, nil
		}
	}
	if cat, ok := c.e.sess.(provider.Cataloger); ok && cat.KindRemoved(id) {
		return core.KindDescriptor{}, coded(CodeRemoved, fmt.Errorf("%s is no longer served", id))
	}
	return core.KindDescriptor{}, coded(CodeUnsupported, fmt.Errorf("unknown kind %q", id))
}

// Identity is what the target points at now: its discovered identity and,
// for a session that knows more (provider.Identifier: Docker's daemon id),
// that too. An unreachable target is an error, never another identity.
func (c *AgentCall) Identity() (string, error) {
	id := c.target.Identity
	if idf, ok := c.e.sess.(provider.Identifier); ok {
		ctx, cancel := context.WithTimeout(c.ctx, getTimeout)
		defer cancel()
		more, err := idf.Identity(ctx)
		if err != nil {
			return "", c.fail(err)
		}
		id += " | " + more
	}
	return id, nil
}

// getTimeout bounds one small read of an agent's call (the daemon's id).
const getTimeout = 10 * time.Second

// fail maps err, saying gone when the session closed under the call.
func (c *AgentCall) fail(err error) error {
	if c.ctx.Err() != nil && c.s.entryByOwner(c.e.owner) != c.e {
		return coded(CodeGone, errors.New("the target's session closed (its configuration changed); retry"))
	}
	return fromProvider(err)
}

// spared: an agent's call holds e, or held it within sessionIdle.
func (e *sessionEntry) spared(now time.Time) bool {
	return len(e.agentCalls) > 0 || now.Before(e.agentUntil)
}

// SnapshotRequest: the rows of a kind in a scope (optionally one object by
// name, or the objects about Subject), read once.
type SnapshotRequest struct {
	Kind    string
	Scope   core.ScopeSel
	Name    string
	Subject *core.Ref
}

// Snapshot is a kind's rows as read once, sorted by scope and name. Status
// is the view's when the rows were taken: loading when it did not become
// ready within snapshotWait (the rows are what came).
type Snapshot struct {
	Kind   core.KindDescriptor
	Rows   []core.Row
	Status provider.ViewStatus
	query  provider.Query
}

// Snapshot reads req's rows as OpenView opens a view — the kind from the
// catalog, the view's descriptor (server-side columns) — without a view of
// the UI (no id, no events): a watch collected until ready, then stopped.
// A schema change while reading is retried once.
func (c *AgentCall) Snapshot(req SnapshotRequest) (Snapshot, error) {
	if !req.Scope.Valid() {
		return Snapshot{}, coded(CodeBadRequest, errors.New("invalid scope selector"))
	}
	for attempt := 0; ; attempt++ {
		snap, err := c.snapshotOnce(req)
		changed := IsCoded(err, string(provider.ClassSchemaChanged)) ||
			err == nil && snap.Status.State == provider.StatusError && snap.Status.Class == provider.ClassSchemaChanged
		if changed && attempt == 0 {
			continue
		}
		if changed && err == nil {
			return Snapshot{}, &CodedError{Code: string(provider.ClassSchemaChanged), Detail: snap.Status.Message}
		}
		return snap, err
	}
}

func (c *AgentCall) snapshotOnce(req SnapshotRequest) (Snapshot, error) {
	kind, err := c.Kind(req.Kind)
	if err != nil {
		return Snapshot{}, err
	}
	q := provider.Query{Kind: req.Kind, Scope: req.Scope, Name: req.Name, Subject: req.Subject}
	if d, ok := c.e.sess.(provider.ViewDescriber); ok {
		desc, bound, err := d.DescribeView(c.ctx, q)
		if err != nil {
			return Snapshot{}, c.fail(err)
		}
		kind, q = desc, bound
	}
	release, err := c.s.agentWatchSlot(c)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	col := newCollector()
	stop, err := c.e.sess.Watch(q, col)
	if err != nil {
		return Snapshot{}, c.fail(err)
	}
	defer stop()
	timer := time.NewTimer(snapshotWait)
	defer timer.Stop()
	select {
	case <-col.done:
	case <-timer.C:
	case <-c.ctx.Done():
		return Snapshot{}, c.fail(c.ctx.Err())
	}
	rows, st := col.take()
	return Snapshot{Kind: kind, Rows: rows, Status: st, query: q}, nil
}

// agentWatchSlot takes one of maxAgentWatches, waiting within the call.
func (s *Service) agentWatchSlot(c *AgentCall) (func(), error) {
	select {
	case s.agentWatches <- struct{}{}:
		return func() { <-s.agentWatches }, nil
	case <-c.ctx.Done():
		if err := c.fail(c.ctx.Err()); IsCoded(err, CodeGone) {
			return nil, err
		}
		return nil, &CodedError{Code: CodeLimit, Detail: fmt.Sprintf("more than %d reads of agents at once; retry", maxAgentWatches)}
	}
}

// collector is a snapshot's sink: deltas applied by their contract (Reset
// or Upserts, then Deletes, then Status), never blocking; done closes at
// the first status that is not loading, and the rows are those of that
// moment (later deliveries are changes after the snapshot).
type collector struct {
	mu   sync.Mutex
	rows map[string]core.Row
	st   provider.ViewStatus
	done chan struct{}
	shut bool
}

func newCollector() *collector {
	return &collector{rows: map[string]core.Row{}, st: provider.ViewStatus{State: provider.StatusLoading}, done: make(chan struct{})}
}

func (c *collector) Apply(d provider.Delta) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.shut {
		return
	}
	if d.Reset {
		c.rows = make(map[string]core.Row, len(d.Upserts))
	}
	for _, r := range d.Upserts {
		c.rows[r.ID] = r
	}
	for _, id := range d.Deletes {
		delete(c.rows, id)
	}
	if d.Status != nil {
		c.st = d.Status.Clone()
		if c.st.State != provider.StatusLoading && !c.shut {
			c.shut = true
			close(c.done)
		}
	}
}

func (c *collector) take() ([]core.Row, provider.ViewStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rows := make([]core.Row, 0, len(c.rows))
	for _, r := range c.rows {
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i].Ref, rows[j].Ref
		if a.Scope != b.Scope {
			return a.Scope < b.Scope
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return rows[i].ID < rows[j].ID
	})
	return rows, c.st.Clone()
}

// Metrics: usage of rowIDs of snap (MaxMetricRows at most; a session
// without metrics says unsupported, as GetMetrics does).
func (c *AgentCall) Metrics(snap Snapshot, rowIDs []string) (MetricsView, error) {
	src, ok := c.e.sess.(provider.MetricsSource)
	if !ok {
		return MetricsView{Status: CodeUnsupported, Values: map[string]provider.Usage{}}, nil
	}
	cut := len(rowIDs) > MaxMetricRows
	if cut {
		rowIDs = rowIDs[:MaxMetricRows]
	}
	ctx, cancel := context.WithTimeout(c.ctx, metricsTimeout)
	defer cancel()
	m, err := src.Metrics(ctx, snap.query, rowIDs)
	if err != nil {
		if IsCoded(c.fail(err), CodeGone) {
			return MetricsView{}, c.fail(err)
		}
		ce := fromProvider(err)
		if ctx.Err() != nil {
			ce = &CodedError{Code: string(provider.ClassUnavailable), Detail: fmt.Sprintf("no usage within %s", metricsTimeout)}
		}
		return MetricsView{Status: ce.Code, Message: ce.Detail, Values: map[string]provider.Usage{}}, nil
	}
	out := MetricsView{Status: "ok", Timestamp: m.Timestamp, Window: m.Window, Values: map[string]provider.Usage{}}
	if cut {
		out.Limit = MaxMetricRows
	}
	for _, id := range rowIDs {
		if u, ok := m.Values[id]; ok {
			out.Values[id] = u
		}
	}
	return out, nil
}

// TailRequest: the last lines of an object's logs, never followed.
type TailRequest struct {
	Ref       core.Ref
	Channel   string
	Previous  bool
	TailLines int
	SinceTime time.Time
}

// TailSource is one source of a tail (a pod's container), with its last
// state when it said one.
type TailSource struct {
	ID      int                `json:"id"`
	Label   string             `json:"label"`
	Channel string             `json:"channel,omitempty"`
	State   *provider.LogState `json:"state,omitempty"`
}

type TailLine struct {
	Source int    `json:"source"`
	TS     string `json:"ts,omitempty"`
	Text   string `json:"text"`
	// Cut: the line was longer than the provider's limit.
	Cut bool `json:"cut,omitempty"`
}

// Tail: Truncated — older lines left out (more lines or bytes than a
// tail keeps), no end within tailWait, or not every source shown; State —
// the whole stream's last state (a group's, e.g. limited).
type Tail struct {
	Sources   []TailSource       `json:"sources"`
	Lines     []TailLine         `json:"lines"`
	State     *provider.LogState `json:"state,omitempty" jsonschema_description:"The whole stream's last state: for a workload, e.g. limited (only some of its pods' streams are read; the message says how many)."`
	Truncated bool               `json:"truncated,omitempty" jsonschema_description:"Older lines were left out (a tail keeps the newest 5000 lines and 1 MiB), not every source answered in time, or not every source is read (see state)."`
}

// TailLogs reads the backlog of an object's logs, never followed (the
// stream ends after it), and keeps its newest maxTailLines lines and
// maxTailBytes of text: Truncated says older ones were left out.
func (c *AgentCall) TailLogs(req TailRequest) (Tail, error) {
	if req.TailLines < 1 || req.TailLines > maxTailLines {
		return Tail{}, coded(CodeBadRequest, fmt.Errorf("tailLines must be 1..%d", maxTailLines))
	}
	if req.Previous && !req.SinceTime.IsZero() {
		return Tail{}, coded(CodeBadRequest, errors.New("previous logs take no since time"))
	}
	src, ok := c.e.sess.(provider.LogSource)
	if !ok {
		return Tail{}, coded(CodeUnsupported, errors.New("this target has no logs"))
	}
	ctx, cancel := context.WithTimeout(c.ctx, tailWait)
	defer cancel()
	sink := &tailSink{}
	q := provider.LogQuery{Channel: req.Channel, Previous: req.Previous, TailLines: req.TailLines, SinceTime: req.SinceTime}
	err := src.StreamLogs(ctx, req.Ref, q, sink)
	if c.ctx.Err() != nil {
		// The session closed (or the agent left): not a complete answer,
		// though a backlog without follow ends with what it read.
		return Tail{}, c.fail(c.ctx.Err())
	}
	if ctx.Err() != nil { // out of time: what came
		sink.out.Truncated, err = true, nil
	}
	if err != nil {
		return Tail{}, c.fail(err)
	}
	if sink.out.Sources == nil {
		sink.out.Sources = []TailSource{}
	}
	sink.out.Lines = append([]TailLine{}, sink.out.Lines...) // not the dropped front
	return sink.out, nil
}

// tailSink collects a tail: the newest lines within maxTailLines (over
// all sources: each has its own TailLines) and maxTailBytes, the older
// dropped from the front. Ready is no end: a pod's own stream sends it
// before its lines, and a stream without Follow ends by itself.
type tailSink struct {
	bytes int
	out   Tail
}

func (t *tailSink) Source(id int, _, label, channel string) error {
	t.out.Sources = append(t.out.Sources, TailSource{ID: id, Label: label, Channel: channel})
	return nil
}

func (t *tailSink) Lines(id int, lines []provider.LogLine) error {
	for _, l := range lines {
		t.bytes += len(l.Text)
		t.out.Lines = append(t.out.Lines, TailLine{Source: id, TS: l.TS, Text: l.Text, Cut: l.Flags&provider.LineCut != 0})
		for len(t.out.Lines) > maxTailLines || t.bytes > maxTailBytes {
			t.bytes -= len(t.out.Lines[0].Text)
			t.out.Lines = t.out.Lines[1:]
			t.out.Truncated = true
		}
	}
	return nil
}

func (t *tailSink) State(id int, st provider.LogState) error {
	if id == 0 { // the whole stream
		s := st
		t.out.State = &s
		t.out.Truncated = t.out.Truncated || st.State == provider.LogLimited
		return nil
	}
	for i := range t.out.Sources {
		if t.out.Sources[i].ID == id {
			s := st
			t.out.Sources[i].State = &s
		}
	}
	return nil
}

func (t *tailSink) Ready() error { return nil }
