package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/views"
)

type sessionEntry struct {
	sess     provider.Session
	provider string
	target   string
	hash     string
	lastUsed time.Time
	// owner is this incarnation's views owner: unique per session, so a
	// view (or an in-flight Open) of a retired session can never be taken
	// for one of its replacement.
	owner string
	seq   uint64 // the incarnation's number (KindsView.Session)
	// agentCalls: agents' calls holding the session now; agentUntil: it
	// stays spared until then after the last one (AgentCall).
	agentCalls map[*AgentCall]struct{}
	agentUntil time.Time
	// background: told so (provider.Backgrounder), its target not the
	// selected one.
	background bool
	// closing: the user closed it while agents' calls held it; it goes
	// with the last one (AgentCall.Done).
	closing bool
}

// sessionIdle: a session without views is closed after this long (a page
// that went away). A local timer, not cluster polling.
const sessionIdle = 60 * time.Second

// Recent targets (P18): the recentTargets targets left last keep their
// sessions (caches, watches) so switching back is instant; unused, one
// closes recentIdle after it was left.
const (
	recentTargets = 2
	recentIdle    = 10 * time.Minute
)

func ownerKey(providerID, target string) string { return providerID + "\x00" + target }

// sessionFor is session() for callers that need the incarnation too.
func (s *Service) sessionFor(ctx context.Context, providerID, target string) (*sessionEntry, error) {
	if _, err := s.session(ctx, providerID, target); err != nil {
		return nil, err
	}
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	e := s.sessions[ownerKey(providerID, target)]
	if e == nil {
		return nil, coded(CodeGone, errors.New("session closed"))
	}
	return e, nil
}

// session returns the open session for a target, building it on first use
// or when the target's configuration changed since. Sessions never block on
// the network (Open only builds clients).
func (s *Service) session(ctx context.Context, providerID, target string) (provider.Session, error) {
	p, ok := s.reg.Get(providerID)
	if !ok {
		return nil, coded(CodeNotFound, fmt.Errorf("unknown provider %q", providerID))
	}
	opener, ok := p.(provider.Opener)
	if !ok {
		return nil, coded(CodeUnsupported, fmt.Errorf("%s targets cannot be opened", p.Title()))
	}
	hash, found, err := s.targetHash(ctx, p, target)
	if err != nil {
		return nil, coded(CodeInternal, err)
	}
	if !found {
		return nil, coded(CodeNotFound, fmt.Errorf("no target %q in %s", target, p.Title()))
	}
	key := ownerKey(providerID, target)
	current := s.currentKey(ctx)
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	if e := s.sessions[key]; e != nil {
		if e.hash == hash {
			e.lastUsed = s.now()
			return e.sess, nil
		}
		s.closeSessionLocked(key) // configuration changed under it
	}
	sess, err := opener.Open(ctx, target)
	if err != nil {
		return nil, fromProvider(err)
	}
	s.sessSeq++
	seq := s.sessSeq
	e := &sessionEntry{sess: sess, provider: providerID, target: target, hash: sess.ConfigHash(), lastUsed: s.now(), owner: fmt.Sprintf("%s#%d", key, seq), seq: seq}
	s.sessions[key] = e
	if key != current {
		s.setBackgroundLocked(key, e, true)
	}
	s.emitTargetsChanged(providerID)
	if c, ok := sess.(provider.Cataloger); ok {
		// Set before anyone reads the catalog: a revision published
		// earlier is in the first ListKinds, a later one is an event.
		c.OnKindsChanged(func(rev uint64) {
			s.em.Emit(events.Event{Type: EventKindsChanged, Key: key,
				Payload: map[string]any{"provider": providerID, "target": target, "session": seq, "rev": rev}})
		})
	}
	s.armReaperLocked()
	return sess, nil
}

func (s *Service) targetHash(ctx context.Context, p provider.Provider, target string) (string, bool, error) {
	d, err := p.Discover(ctx)
	if err != nil {
		return "", false, err
	}
	for _, t := range d.Targets {
		if t.ID == target {
			return t.ConfigHash, true, nil
		}
	}
	return "", false, nil
}

// closeSessionLocked closes a session and its views; each view's UI learns
// it is gone (views.Manager.CloseOwner) and reopens against a new session.
func (s *Service) closeSessionLocked(key string) { s.closeSessionWhyLocked(key, nil) }

// closeSessionWhyLocked is closeSessionLocked whose streams end saying why
// (nil: just gone).
func (s *Service) closeSessionWhyLocked(key string, why *core.Message) {
	e := s.sessions[key]
	if e == nil {
		return
	}
	delete(s.sessions, key)
	for c := range e.agentCalls {
		c.cancel() // their work ends: gone
	}
	s.views.CloseOwner(e.owner)
	s.streams.CloseOwnerWhy(e.owner, why)
	e.sess.Close()
	s.emitTargetsChanged(e.provider)
}

// CloseTarget closes a target's session on the user's word (P18): its
// views, its log streams (they say so), its caches. Not the selected
// target's. An agent's call in flight is not cut: the session goes when
// the last one ends. It is no longer a recent target.
func (s *Service) CloseTarget(ctx context.Context, providerID, id string) error {
	if _, err := s.targetOf(ctx, providerID, id); err != nil {
		return err
	}
	key := ownerKey(providerID, id)
	if key == s.currentKey(ctx) {
		return coded(CodeBadRequest, errors.New("the selected target's connection stays open"))
	}
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	delete(s.left, key)
	e := s.sessions[key]
	switch {
	case e == nil:
	case len(e.agentCalls) > 0:
		e.closing = true
	default:
		m := apiMessage("closedByUser")
		s.closeSessionWhyLocked(key, &m)
	}
	return nil
}

// emitTargetsChanged: a target's session opened or closed (its "open" dot).
func (s *Service) emitTargetsChanged(providerID string) {
	s.em.Emit(events.Event{Type: EventTargetsChanged, Key: providerID, Payload: map[string]any{"provider": providerID}})
}

// currentKey: the selected target's key — this run's selection, else the
// one remembered from the last run ("" when none).
func (s *Service) currentKey(ctx context.Context) string {
	s.sessMu.Lock()
	cur := s.current
	s.sessMu.Unlock()
	if cur != "" {
		return cur
	}
	if sel, err := s.selected(ctx); err == nil && sel != nil {
		return ownerKey(sel.Provider, sel.ID)
	}
	return ""
}

// setBackgroundLocked tells e (of key) it is in the background or not; its
// lost closes it — that incarnation only, and only while still in the
// background (a person selected it meanwhile: they will log in).
func (s *Service) setBackgroundLocked(key string, e *sessionEntry, on bool) {
	b, ok := e.sess.(provider.Backgrounder)
	if !ok || e.background == on {
		return
	}
	e.background = on
	b.SetBackground(on, func() {
		s.sessMu.Lock()
		defer s.sessMu.Unlock()
		if s.sessions[key] == e && e.background {
			m := apiMessage("loginNeeded")
			s.closeSessionWhyLocked(key, &m)
		}
	})
}

// revalidateSessions closes sessions whose target vanished or now resolves
// to another configuration (called when local configuration changed).
func (s *Service) revalidateSessions(ctx context.Context, providerID string) {
	p, ok := s.reg.Get(providerID)
	if !ok {
		return
	}
	d, err := p.Discover(ctx)
	if err != nil {
		slog.Warn("cannot revalidate sessions", "provider", providerID, "err", err)
		return
	}
	hashes := map[string]string{}
	for _, t := range d.Targets {
		hashes[ownerKey(providerID, t.ID)] = t.ConfigHash
	}
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	for key, e := range s.sessions {
		if keyProvider(key) != providerID {
			continue
		}
		if h, ok := hashes[key]; ok && h == e.hash {
			continue
		}
		s.closeSessionLocked(key)
	}
}

func keyProvider(key string) string {
	p, _, _ := strings.Cut(key, "\x00")
	return p
}

// selectSession makes cur the selected target's session key, prev (if
// another) a recent one, and closes the sessions that are neither current
// nor recent — but not busy ones: with a stream (a log tab of that target
// goes on) or agents' calls (AgentCall).
func (s *Service) selectSession(prev, cur string) {
	streams := s.streams.Owners()
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	now := s.now()
	if s.current != "" {
		prev = s.current // this run's own record beats the stored preference
	}
	if prev != "" && prev != cur {
		s.left[prev] = now
		if e := s.sessions[prev]; e != nil {
			s.setBackgroundLocked(prev, e, true)
		}
	}
	if e := s.sessions[cur]; e != nil {
		s.setBackgroundLocked(cur, e, false)
	}
	delete(s.left, cur)
	s.current = cur
	for len(s.left) > recentTargets {
		oldest := ""
		for k, at := range s.left {
			if oldest == "" || at.Before(s.left[oldest]) {
				oldest = k
			}
		}
		delete(s.left, oldest)
	}
	for key, e := range s.sessions {
		_, recent := s.left[key]
		if key != cur && !recent && !e.spared(now) && streams[e.owner] == 0 {
			s.closeSessionLocked(key)
		}
	}
}

func (s *Service) ListKinds(ctx context.Context, providerID, target string) (KindsView, error) {
	e, err := s.sessionFor(ctx, providerID, target)
	if err != nil {
		return KindsView{}, err
	}
	return KindsView{KindCatalog: catalogOf(e.sess), Session: e.seq}, nil
}

// catalogOf: a session without a catalog has one revision of fixed kinds.
func catalogOf(sess provider.Session) core.KindCatalog {
	if c, ok := sess.(provider.Cataloger); ok {
		return c.Catalog()
	}
	return core.KindCatalog{Kinds: sess.Kinds(), Rev: 1, State: core.CatalogReady}
}

func (s *Service) RefreshKinds(ctx context.Context, providerID, target string) error {
	sess, err := s.session(ctx, providerID, target)
	if err != nil {
		return err
	}
	if c, ok := sess.(provider.Cataloger); ok {
		c.RefreshKinds()
	}
	return nil
}

func (s *Service) ListScopes(ctx context.Context, providerID, target string) (ScopesView, error) {
	sess, err := s.session(ctx, providerID, target)
	if err != nil {
		return ScopesView{}, err
	}
	scopes, err := sess.Scopes(ctx)
	if err != nil {
		return ScopesView{Scopes: []core.Scope{}, Error: fromProvider(err)}, nil
	}
	if scopes == nil {
		scopes = []core.Scope{}
	}
	return ScopesView{Scopes: scopes, Kind: sess.ScopeKind()}, nil
}

func (s *Service) OpenView(ctx context.Context, req OpenViewRequest) (ViewInfo, error) {
	if !req.Query.Scope.Valid() {
		return ViewInfo{}, coded(CodeBadRequest, errors.New("invalid scope selector"))
	}
	e, err := s.sessionFor(ctx, req.Provider, req.Target)
	if err != nil {
		return ViewInfo{}, err
	}
	var kind *core.KindDescriptor
	for _, k := range e.sess.Kinds() {
		if k.ID == req.Query.Kind {
			kind = &k
		}
	}
	if kind == nil {
		if c, ok := e.sess.(provider.Cataloger); ok && c.KindRemoved(req.Query.Kind) {
			return ViewInfo{}, coded(CodeRemoved, fmt.Errorf("%s is no longer served", req.Query.Kind))
		}
		return ViewInfo{}, coded(CodeUnsupported, fmt.Errorf("unknown kind %q", req.Query.Kind))
	}
	q := req.Query
	q.Schema = 0 // only the session binds a view to columns
	if d, ok := e.sess.(provider.ViewDescriber); ok {
		desc, bound, err := d.DescribeView(ctx, q)
		if err != nil {
			return ViewInfo{}, fromProvider(err)
		}
		kind, q = &desc, bound
	}
	id, err := s.views.Open(e.owner, e.sess, q)
	if errors.Is(err, views.ErrGone) {
		return ViewInfo{}, coded(CodeGone, errors.New("the session was replaced while opening; retry"))
	}
	if err != nil {
		return ViewInfo{}, fromProvider(err)
	}
	_, resync := e.sess.(provider.Resyncer)
	return ViewInfo{ViewID: id, Kind: *kind, Resync: resync}, nil
}

func (s *Service) GetRows(_ context.Context, viewID string, since uint64) (views.Page, error) {
	p, err := s.views.Get(viewID, since)
	if errors.Is(err, views.ErrGone) {
		return views.Page{}, coded(CodeGone, err)
	}
	return p, err
}

func (s *Service) CloseView(_ context.Context, viewID string) error {
	s.views.Close(viewID)
	s.dropMetricGate(viewID)
	return nil
}

// ResyncView resolves the view through its owner — the session incarnation
// that opened it — so a view of a retired session never reaches the
// current one.
func (s *Service) ResyncView(_ context.Context, viewID string) error {
	owner, q, _, err := s.views.Info(viewID)
	if errors.Is(err, views.ErrGone) {
		return coded(CodeGone, err)
	}
	e := s.entryByOwner(owner)
	if e == nil {
		return coded(CodeGone, errors.New("session closed"))
	}
	r, ok := e.sess.(provider.Resyncer)
	if !ok {
		return coded(CodeUnsupported, errors.New("this target's views are not read again on request"))
	}
	if err := r.Resync(q); err != nil {
		return fromProvider(err)
	}
	return nil
}

func (s *Service) TouchViews(_ context.Context, viewIDs []string) ([]string, error) {
	gone := s.views.Touch(viewIDs)
	if gone == nil {
		gone = []string{}
	}
	return gone, nil
}

func (s *Service) GetResource(ctx context.Context, ref core.Ref) (*core.Resource, error) {
	sess, err := s.session(ctx, ref.Provider, ref.Target)
	if err != nil {
		return nil, err
	}
	r, err := sess.Get(ctx, ref)
	if err != nil {
		return nil, fromProvider(err)
	}
	return r, nil
}

// MaxMetricRows: the rows one metrics request asks for (a screenful).
const MaxMetricRows = 100

// metricsTimeout bounds a metrics request as a whole (a var for tests).
var metricsTimeout = 10 * time.Second

// metricGate: a view's metrics request in flight (cancel, by its seq) and
// the lowest seq a request may still start with.
type metricGate struct {
	next   uint64
	seq    uint64
	cancel context.CancelFunc
	call   *int // identifies the request in flight
}

// canceledMetrics: what a request the page gave up gets.
func canceledMetrics() MetricsView {
	return MetricsView{Status: string(provider.ClassUnavailable), Message: "the request was canceled", Values: map[string]provider.Usage{}}
}

// errViewClosed: the view closed before its metrics request took its place.
var errViewClosed = coded(CodeGone, errors.New("the view was closed"))

// enterMetrics makes the request the view's one in flight (ending an
// older one), or refuses it: canceled (false) when its seq was already
// passed, errViewClosed when the view is gone — checked under metricsMu,
// which CloseView takes after closing the view, so a request either sees
// the view closed or is ended by the close. The returned release ends it
// and forgets it.
func (s *Service) enterMetrics(ctx context.Context, viewID string, seq uint64) (context.Context, func(), bool, error) {
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	if !s.views.Exists(viewID) {
		return nil, nil, false, errViewClosed
	}
	if s.metricGates == nil {
		s.metricGates = map[string]*metricGate{}
	}
	s.sweepMetricGatesLocked()
	g := s.metricGates[viewID]
	if g == nil {
		g = &metricGate{}
		s.metricGates[viewID] = g
	}
	if seq != 0 && seq < g.next {
		return nil, nil, false, nil
	}
	if g.cancel != nil {
		g.cancel() // superseded
	}
	ctx, cancel := context.WithCancel(ctx)
	call := new(int)
	g.seq, g.cancel, g.call = seq, cancel, call
	if seq != 0 {
		g.next = seq + 1
	}
	return ctx, func() {
		cancel()
		s.metricsMu.Lock()
		if g.call == call { // not replaced by a newer request
			g.cancel, g.call = nil, nil
		}
		s.metricsMu.Unlock()
	}, true, nil
}

// sweepMetricGatesLocked forgets the gates of views that are gone
// (expired rather than closed) with nothing in flight.
func (s *Service) sweepMetricGatesLocked() {
	for id, g := range s.metricGates {
		if g.cancel == nil && !s.views.Exists(id) {
			delete(s.metricGates, id)
		}
	}
}

func (s *Service) dropMetricGate(viewID string) {
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	if g := s.metricGates[viewID]; g != nil {
		if g.cancel != nil {
			g.cancel()
		}
		delete(s.metricGates, viewID)
	}
}

func (s *Service) CancelMetrics(_ context.Context, viewID string, seq uint64) error {
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	if !s.views.Exists(viewID) {
		return nil // nothing runs for a gone view: its gate went with it
	}
	if s.metricGates == nil {
		s.metricGates = map[string]*metricGate{}
	}
	g := s.metricGates[viewID]
	if g == nil {
		g = &metricGate{}
		s.metricGates[viewID] = g
	}
	g.next = max(g.next, seq+1)
	if g.cancel != nil && g.seq != 0 && g.seq <= seq {
		g.cancel()
	}
	return nil
}

func (s *Service) GetMetrics(ctx context.Context, req MetricsRequest) (MetricsView, error) {
	viewID, rowIDs := req.ViewID, req.RowIDs
	owner, q, rows, err := s.views.Info(viewID)
	if errors.Is(err, views.ErrGone) {
		return MetricsView{}, coded(CodeGone, err)
	}
	e := s.entryByOwner(owner)
	if e == nil {
		return MetricsView{}, coded(CodeGone, errors.New("session closed"))
	}
	src, ok := e.sess.(provider.MetricsSource)
	if !ok {
		return MetricsView{Status: CodeUnsupported, Values: map[string]provider.Usage{}}, nil
	}
	// The asked rows that are the view's current rows (ids are
	// incarnations), in the page's order, at most MaxMetricRows.
	inView := make(map[string]bool, len(rows))
	for _, r := range rows {
		inView[r.ID] = true
	}
	asked := make([]string, 0, min(len(rowIDs), MaxMetricRows))
	seen := map[string]bool{}
	cut := false
	for _, id := range rowIDs {
		if !inView[id] || seen[id] {
			continue
		}
		if len(asked) == MaxMetricRows {
			cut = true
			break
		}
		seen[id] = true
		asked = append(asked, id)
	}
	if len(asked) == 0 {
		return MetricsView{Status: "ok", Values: map[string]provider.Usage{}}, nil
	}
	if s.beforeMetricsGate != nil {
		s.beforeMetricsGate()
	}
	ctx, release, ok, err := s.enterMetrics(ctx, viewID, req.Seq)
	if err != nil {
		return MetricsView{}, err
	}
	if !ok {
		return canceledMetrics(), nil
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, metricsTimeout)
	defer cancel()
	m, err := src.Metrics(ctx, q, asked)
	if s.entryByOwner(owner) != e {
		return MetricsView{}, coded(CodeGone, errors.New("session closed")) // retired while fetching
	}
	if err != nil {
		ce := fromProvider(err)
		if ctx.Err() != nil { // the provider gave up with the request
			ce = &CodedError{Code: string(provider.ClassUnavailable), Detail: fmt.Sprintf("no usage within %s", metricsTimeout)}
			if errors.Is(ctx.Err(), context.Canceled) {
				ce.Detail = "the request was canceled"
			}
		}
		return MetricsView{Status: ce.Code, Message: ce.Detail, Values: map[string]provider.Usage{}}, nil
	}
	out := MetricsView{Status: "ok", Timestamp: m.Timestamp, Window: m.Window, Values: map[string]provider.Usage{}, Coverage: m.Coverage}
	if cut {
		out.Limit = MaxMetricRows
	}
	for _, id := range asked {
		if u, ok := m.Values[id]; ok {
			out.Values[id] = u
		}
	}
	return out, nil
}

// armReaperLocked schedules one idle-session check while sessions exist.
func (s *Service) armReaperLocked() {
	if s.reaper != nil || len(s.sessions) == 0 {
		return
	}
	s.reaper = time.AfterFunc(sessionIdle/2, func() {
		s.reapIdleSessions()
		s.sessMu.Lock()
		s.reaper = nil
		s.armReaperLocked()
		s.sessMu.Unlock()
	})
}

// reapIdleSessions closes sessions that have had no views and no streams
// (an open log tab) for sessionIdle.
func (s *Service) reapIdleSessions() {
	owners := s.views.Owners()
	for o, n := range s.streams.Owners() {
		owners[o] += n
	}
	now := s.now()
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	for key, e := range s.sessions {
		if owners[e.owner] > 0 || len(e.agentCalls) > 0 {
			e.lastUsed = now
			continue
		}
		idle, since := sessionIdle, e.lastUsed
		if at, ok := s.left[key]; ok {
			idle = recentIdle
			if at.After(since) {
				since = at
			}
		}
		if now.Sub(since) >= idle {
			s.closeSessionLocked(key)
		}
	}
}

// entryByOwner finds the live session incarnation owning views, or nil.
func (s *Service) entryByOwner(owner string) *sessionEntry {
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	for _, e := range s.sessions {
		if e.owner == owner {
			return e
		}
	}
	return nil
}
