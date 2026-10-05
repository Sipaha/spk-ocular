package agentapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/agentgrant"
	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/store"
)

// session is one call's hold on a target: its grants and session, the
// target's identity checked.
type session struct {
	call   *api.AgentCall
	grants agentgrant.Target
	kinds  map[string]core.KindDescriptor
}

func (x *session) done() { x.call.Done() }

// grantsOf is a target's grants (forbidden: none).
func (s *Server) grantsOf(ctx context.Context, provider, target string) (agentgrant.Target, error) {
	all, err := s.o.Store.AgentTargets(ctx)
	if err != nil {
		return agentgrant.Target{}, &api.CodedError{Code: api.CodeInternal, Detail: err.Error()}
	}
	for _, t := range all {
		if t.Provider == provider && t.Target == target {
			return t, nil
		}
	}
	return agentgrant.Target{}, forbidden("nothing is granted for %s target %q: the user grants access in SPK Ocular", provider, target)
}

// open takes the target's session for a call: the target must have grants
// for the identity it has now (another one suspends them until the user
// confirms it again; the one granted resumes them).
func (s *Server) open(ctx context.Context, c caller, method, provider, target string) (*session, error) {
	if provider == "" || target == "" {
		return nil, badRequest("provider and target are required (Access lists them)")
	}
	g, err := s.grantsOf(ctx, provider, target)
	if err != nil {
		s.refused(c, method, provider, target, "", "", err)
		return nil, err
	}
	call, err := s.o.Service.AgentCall(ctx, provider, target)
	if err != nil {
		return nil, err
	}
	id, err := call.Identity()
	if err != nil {
		call.Done()
		return nil, err
	}
	if changed, err := s.o.Store.ObserveAgentIdentity(ctx, provider, target, id); err != nil {
		slog.Warn("agent access: identity not recorded", "err", err)
	} else if changed {
		s.cancelLogReads(provider, target)
		s.o.Service.Emit(api.EventAgentGrantsChanged, "", nil)
	}
	if id != g.Identity {
		call.Done()
		err := forbidden("%s target %q now points elsewhere than when access was granted (%s; granted for %s): the grants are suspended until the user confirms them again in SPK Ocular", provider, target, id, g.Identity)
		s.refused(c, method, provider, target, "", "", err)
		return nil, err
	}
	kinds := map[string]core.KindDescriptor{}
	for _, k := range call.Kinds() {
		kinds[k.ID] = k
	}
	return &session{call: call, grants: g, kinds: kinds}, nil
}

// kind is the session's kind (removed / unsupported otherwise).
func (x *session) kind(id string) (core.KindDescriptor, error) {
	if k, ok := x.kinds[id]; ok {
		return k, nil
	}
	return x.call.Kind(id)
}

// allows decides a verb on ref's object of kind k.
func (x *session) allows(k core.KindDescriptor, scope, verb string, destructive bool) agentgrant.Decision {
	return agentgrant.Allows(x.grants.Grants, agentgrant.Request{Scope: scope, Scoped: k.Scoped, Kind: k.ID, Sensitive: k.Sensitive, Verb: verb, Destructive: destructive})
}

// readable: an object may be read (its kind by the catalog; a kind not
// served is judged by its scope).
func (x *session) readable(ref core.Ref) bool {
	k, ok := x.kinds[ref.Kind]
	if !ok {
		k = core.KindDescriptor{ID: ref.Kind, Scoped: ref.Scope != ""}
	}
	return x.allows(k, ref.Scope, agentgrant.VerbRead, false).OK
}

// check refuses a verb not granted (journaled).
func (s *Server) check(c caller, x *session, method string, k core.KindDescriptor, ref core.Ref, verb string, destructive bool) (agentgrant.Decision, error) {
	d := x.allows(k, ref.Scope, verb, destructive)
	if !d.OK {
		err := forbidden("%s", d.Reason)
		s.refused(c, method, ref.Provider, ref.Target, ref.Scope, objectOf(ref), err)
		return d, err
	}
	return d, nil
}

func objectOf(ref core.Ref) string {
	if ref.Name == "" {
		return ref.Kind
	}
	return ref.String()
}

// audit records e (a failure to record is logged, not the call's).
func (s *Server) audit(e store.AuditEntry) {
	if e.At.IsZero() {
		e.At = s.now()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.o.Store.AppendAudit(ctx, e); err != nil {
		slog.Warn("agent access: journal not written", "err", err)
		return
	}
	s.o.Service.Emit(api.EventAgentAuditChanged, "", nil)
}

func (s *Server) read(c caller, method, provider, target, scope, object string) {
	s.audit(store.AuditEntry{Agent: c.agent, Method: method, Provider: provider, Target: target, Scope: scope, Object: object, Phase: store.AuditRead, Outcome: "done"})
}

func (s *Server) refused(c caller, method, provider, target, scope, object string, err error) {
	s.audit(store.AuditEntry{Agent: c.agent, Method: method, Provider: provider, Target: target, Scope: scope, Object: object, Phase: store.AuditRefused, Outcome: codeOf(err), Detail: detailOf(err)})
}

func codeOf(err error) string {
	return errCoded(err).Code
}

func detailOf(err error) string {
	return errCoded(err).Detail
}

func errCoded(err error) *api.CodedError {
	var ce *api.CodedError
	if errors.As(err, &ce) {
		return ce
	}
	return &api.CodedError{Code: api.CodeInternal, Detail: err.Error()}
}

// Plans and edit sources live this long and this many (the oldest go).
const (
	planTTL  = 10 * time.Minute
	maxPlans = 200
)

// registry keeps values by random id for a while (a bounded map).
type registry[T any] struct {
	mu    sync.Mutex
	ttl   time.Duration
	max   int
	items map[string]regItem[T]
	now   func() time.Time
}

type regItem[T any] struct {
	v  T
	at time.Time
}

func newRegistry[T any](ttl time.Duration, most int) *registry[T] {
	return &registry[T]{ttl: ttl, max: most, items: map[string]regItem[T]{}, now: time.Now}
}

func newID(prefix string) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func (r *registry[T]) put(prefix string, v T) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweepLocked()
	if len(r.items) >= r.max {
		var oldest string
		var at time.Time
		for id, it := range r.items {
			if oldest == "" || it.at.Before(at) {
				oldest, at = id, it.at
			}
		}
		delete(r.items, oldest)
	}
	id := newID(prefix)
	r.items[id] = regItem[T]{v: v, at: r.now()}
	return id
}

func (r *registry[T]) get(id string) (T, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweepLocked()
	it, ok := r.items[id]
	return it.v, ok
}

// take gets and forgets.
func (r *registry[T]) take(id string) (T, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweepLocked()
	it, ok := r.items[id]
	delete(r.items, id)
	return it.v, ok
}

func (r *registry[T]) sweepLocked() {
	now := r.now()
	for id, it := range r.items {
		if now.Sub(it.at) >= r.ttl {
			delete(r.items, id)
		}
	}
}

// notFound: an id this server does not know (expired, taken, of another
// process).
func notFound(what, id string) *api.CodedError {
	return &api.CodedError{Code: api.CodeNotFound, Detail: fmt.Sprintf("no %s %q: it expired (%s), was used, or is of an earlier run of Ocular — prepare again", what, id, planTTL)}
}

// trimLower is s lowered for a substring match.
func trimLower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
