package api

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

const connectionAttempts = 3
const connectionTimeout = 90 * time.Second

var connectionBackoff = []time.Duration{500 * time.Millisecond, time.Second}

type uiContextKey struct{}

// UIContext prevents UI reads from implicitly creating or reopening sessions.
// AgentCall has its own explicit grant checks and session lifetime.
func UIContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, uiContextKey{}, true)
}
func fromUI(ctx context.Context) bool { return ctx.Value(uiContextKey{}) == true }

type connectingSession struct {
	provider.Session
	once sync.Once
}

func (s *connectingSession) Close() { s.once.Do(s.Session.Close) }

type connectionAttempt struct {
	status                        core.ConnectionStatus
	provider, target, hash, owner string
	ctx                           context.Context
	cancel                        context.CancelFunc
	provisional                   *connectingSession
	createdOwner                  bool
}

func (s *Service) ConnectTarget(ctx context.Context, providerID, id string) (core.ConnectionStatus, error) {
	target, err := s.targetOf(ctx, providerID, id)
	if err != nil {
		return core.ConnectionStatus{}, err
	}
	p, _ := s.reg.Get(providerID)
	opener, ok := p.(provider.Opener)
	if !ok {
		return core.ConnectionStatus{}, coded(CodeUnsupported, errors.New("target cannot be connected"))
	}
	key := ownerKey(providerID, id)
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	if s.closed {
		return core.ConnectionStatus{}, coded(CodeGone, errors.New("application is closing"))
	}
	if old := s.connections[key]; old != nil && old.hash == target.ConfigHash && (old.status.State == "connecting" || old.status.State == "connected") {
		return old.status, nil
	}
	if old := s.connections[key]; old != nil {
		old.cancel()
	}
	return s.startConnectionLocked(providerID, id, target.ConfigHash, opener), nil
}

// startConnectionLocked admits an explicit connection or restores one whose
// configuration changed. Callers hold sessMu and have validated the target.
func (s *Service) startConnectionLocked(providerID, id, hash string, opener provider.Opener) core.ConnectionStatus {
	key := ownerKey(providerID, id)
	s.connectionSeq++
	a := &connectionAttempt{provider: providerID, target: id, hash: hash,
		status: core.ConnectionStatus{ID: s.connectionSeq, State: "connecting", Phase: "opening", MaxAttempts: connectionAttempts, StartedAt: s.now().UnixMilli()}}
	a.ctx, a.cancel = context.WithCancel(context.Background())
	s.connections[key] = a
	if e := s.sessions[key]; e != nil && e.hash == hash {
		e.closing = false
		a.owner = e.owner
		a.status.State, a.status.Phase, a.status.FinishedAt = "connected", "ready", s.now().UnixMilli()
		s.emitTargetsChanged(providerID)
		return a.status
	}
	s.wg.Add(1)
	go s.connect(a, opener)
	s.emitTargetsChanged(providerID)
	return a.status
}

func (s *Service) connectionActiveLocked(a *connectionAttempt) bool {
	return !s.closed && s.connections[ownerKey(a.provider, a.target)] == a && a.ctx.Err() == nil
}

func (s *Service) connectionProgress(a *connectionAttempt, update func(*core.ConnectionStatus)) bool {
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	if !s.connectionActiveLocked(a) {
		return false
	}
	update(&a.status)
	s.emitTargetsChanged(a.provider)
	return true
}

func (s *Service) connect(a *connectionAttempt, opener provider.Opener) {
	defer s.wg.Done()
	defer a.cancel()
	for attempt := 1; attempt <= connectionAttempts; attempt++ {
		if !s.connectionProgress(a, func(st *core.ConnectionStatus) {
			st.Attempt, st.AttemptStarted = attempt, s.now().UnixMilli()
			st.Phase, st.RetryAt = "opening", 0
		}) {
			return
		}
		ctx, cancel := context.WithTimeout(a.ctx, connectionTimeout)
		sess, err := opener.Open(ctx, a.target)
		var provisional *connectingSession
		if err == nil {
			// Automatic restoration of an unselected target must not launch an
			// interactive credential helper in the background.
			if b, ok := sess.(provider.Backgrounder); ok && s.currentKey(a.ctx) != ownerKey(a.provider, a.target) {
				b.SetBackground(true, nil)
			}
			provisional = &connectingSession{Session: sess}
			s.sessMu.Lock()
			active := s.connectionActiveLocked(a)
			if active {
				a.provisional = provisional
			}
			s.sessMu.Unlock()
			if !active {
				cancel()
				provisional.Close()
				return
			}
			s.connectionProgress(a, func(st *core.ConnectionStatus) { st.Phase = "checking" })
			done := make(chan error, 1)
			go func() {
				if checker, ok := sess.(provider.ConnectionChecker); ok {
					done <- checker.CheckConnection(ctx)
				} else {
					_, e := sess.Scopes(ctx)
					done <- e
				}
			}()
			select {
			case err = <-done:
			case <-ctx.Done():
				err = ctx.Err()
				provisional.Close()
				<-done
			}
		}
		var pe *provider.Error
		if provisional != nil && errors.As(err, &pe) && pe.Class == provider.ClassForbidden {
			err = nil
		}
		if err == nil && ctx.Err() != nil {
			err = ctx.Err()
		}
		cancel()
		if err == nil {
			// Configuration can change while authentication is in progress.
			target, checkErr := s.targetOf(a.ctx, a.provider, a.target)
			if checkErr != nil {
				err = checkErr
			} else if target.ConfigHash != a.hash || sess.ConfigHash() != a.hash {
				err = coded(CodeGone, errors.New("connection configuration changed; press Connect again"))
			}
		}
		if err == nil {
			current := s.currentKey(a.ctx)
			s.sessMu.Lock()
			if !s.connectionActiveLocked(a) {
				s.sessMu.Unlock()
				provisional.Close()
				return
			}
			key := ownerKey(a.provider, a.target)
			e := s.sessions[key]
			if e != nil && e.hash != a.hash {
				s.closeSessionLocked(key)
				e = nil
			}
			reused := e != nil
			if !reused {
				e = s.registerSessionLocked(provisional.Session, a.provider, a.target, current)
			}
			a.owner, a.provisional = e.owner, nil
			a.createdOwner = !reused
			a.status.State, a.status.Phase, a.status.FinishedAt = "connected", "ready", s.now().UnixMilli()
			a.status.Error, a.status.ErrorClass = "", ""
			s.emitTargetsChanged(a.provider)
			s.sessMu.Unlock()
			if reused {
				provisional.Close()
			}
			return
		}
		if provisional != nil {
			provisional.Close()
		}
		s.sessMu.Lock()
		if a.provisional == provisional {
			a.provisional = nil
		}
		s.sessMu.Unlock()
		if a.ctx.Err() != nil {
			return
		}
		class := string(provider.ClassInternal)
		message := err.Error()
		if errors.As(err, &pe) {
			class = string(pe.Class)
			message = pe.Message
		}
		var codedError *CodedError
		if errors.As(err, &codedError) {
			class, message = codedError.Code, codedError.Detail
		}
		if errors.Is(err, context.DeadlineExceeded) {
			class = string(provider.ClassUnavailable)
		}
		retry := class == string(provider.ClassUnavailable) && attempt < connectionAttempts
		if !s.connectionProgress(a, func(st *core.ConnectionStatus) {
			st.Error, st.ErrorClass = message, class
			if retry {
				st.Phase, st.RetryAt = "retry_wait", s.now().Add(connectionBackoff[attempt-1]).UnixMilli()
			} else {
				st.State, st.Phase, st.FinishedAt = "failed", "failed", s.now().UnixMilli()
			}
		}) {
			return
		}
		if !retry {
			return
		}
		timer := time.NewTimer(connectionBackoff[attempt-1])
		select {
		case <-timer.C:
		case <-a.ctx.Done():
			timer.Stop()
			return
		}
	}
}

func (s *Service) CancelConnectTarget(_ context.Context, providerID, id string, attempt uint64) error {
	s.sessMu.Lock()
	key := ownerKey(providerID, id)
	if s.closed {
		s.sessMu.Unlock()
		return nil
	}
	a := s.connections[key]
	if a == nil || a.status.ID != attempt {
		s.sessMu.Unlock()
		return nil
	}
	a.cancel()
	a.status.State, a.status.Phase, a.status.RetryAt = "cancelled", "cancelled", 0
	a.status.FinishedAt = s.now().UnixMilli()
	provisional := a.provisional
	if e := s.sessions[key]; e != nil && e.owner == a.owner && a.createdOwner {
		if len(e.agentCalls) > 0 {
			e.closing = true
		} else {
			s.closeSessionLocked(key)
		}
	}
	s.emitTargetsChanged(providerID)
	if provisional != nil {
		// The status and attempt cancellation must not wait for provider cleanup
		// (discovery or credential helpers may still be unwinding).
		s.wg.Add(1)
		go func() { defer s.wg.Done(); provisional.Close() }()
	}
	s.sessMu.Unlock()
	return nil
}

func (s *Service) stopConnections() {
	s.sessMu.Lock()
	s.closed = true
	var provisional []*connectingSession
	for _, a := range s.connections {
		a.cancel()
		if a.provisional != nil {
			provisional = append(provisional, a.provisional)
		}
	}
	s.sessMu.Unlock()
	for _, sess := range provisional {
		sess.Close()
	}
}
