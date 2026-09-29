package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/views"
)

type sessionEntry struct {
	sess     provider.Session
	hash     string
	lastUsed time.Time
	// owner is this incarnation's views owner: unique per session, so a
	// view (or an in-flight Open) of a retired session can never be taken
	// for one of its replacement.
	owner string
}

// sessionIdle: a session without views is closed after this long (a page
// that went away, a target left behind). A local timer, not cluster polling.
const sessionIdle = 60 * time.Second

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
	s.sessions[key] = &sessionEntry{sess: sess, hash: sess.ConfigHash(), lastUsed: s.now(), owner: fmt.Sprintf("%s#%d", key, s.sessSeq)}
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
func (s *Service) closeSessionLocked(key string) {
	e := s.sessions[key]
	if e == nil {
		return
	}
	delete(s.sessions, key)
	s.views.CloseOwner(e.owner)
	e.sess.Close()
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

// closeOtherSessions keeps only the selected target's session.
func (s *Service) closeOtherSessions(keep string) {
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	for key := range s.sessions {
		if key != keep {
			s.closeSessionLocked(key)
		}
	}
}

func (s *Service) ListKinds(ctx context.Context, providerID, target string) ([]core.KindDescriptor, error) {
	sess, err := s.session(ctx, providerID, target)
	if err != nil {
		return nil, err
	}
	return sess.Kinds(), nil
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
		return ViewInfo{}, coded(CodeUnsupported, fmt.Errorf("unknown kind %q", req.Query.Kind))
	}
	id, err := s.views.Open(e.owner, e.sess, req.Query)
	if errors.Is(err, views.ErrGone) {
		return ViewInfo{}, coded(CodeGone, errors.New("the session was replaced while opening; retry"))
	}
	if err != nil {
		return ViewInfo{}, fromProvider(err)
	}
	return ViewInfo{ViewID: id, Kind: *kind}, nil
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

func (s *Service) GetMetrics(ctx context.Context, viewID string) (MetricsView, error) {
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
	m, err := src.Metrics(ctx, q)
	if s.entryByOwner(owner) != e {
		return MetricsView{}, coded(CodeGone, errors.New("session closed")) // retired while fetching
	}
	if err != nil {
		ce := fromProvider(err)
		return MetricsView{Status: ce.Code, Message: ce.Detail, Values: map[string]provider.Usage{}}, nil
	}
	out := MetricsView{Status: "ok", Timestamp: m.Timestamp, Window: m.Window, Values: map[string]provider.Usage{}}
	// Join by the current rows: a deleted or replaced object gets nothing.
	for _, r := range rows {
		if u, ok := m.Values[r.Ref.Scope+"/"+r.Ref.Name]; ok {
			out.Values[r.ID] = u
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

// reapIdleSessions closes sessions that have had no views for sessionIdle.
func (s *Service) reapIdleSessions() {
	owners := s.views.Owners()
	now := s.now()
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	for key, e := range s.sessions {
		if owners[e.owner] > 0 {
			e.lastUsed = now
			continue
		}
		if now.Sub(e.lastUsed) >= sessionIdle {
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
