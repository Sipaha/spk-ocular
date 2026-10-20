package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/store"
)

const helperLeaseDuration = 5 * time.Minute

type helperJournal struct {
	store  *store.Store
	now    func() time.Time
	mu     sync.Mutex
	active map[string]bool
}
type helperLease struct {
	journal   *helperJournal
	id, owner string
	stop      context.CancelFunc
	done      chan struct{}
	once      sync.Once
	err       error
}

func newHelperOwner() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func (j *helperJournal) Begin(ctx context.Context, h core.OwnedHelper, stopRun context.CancelFunc) (provider.HelperLease, error) {
	now := j.now()
	h.CreatedAt = now.UnixMilli()
	owner := newHelperOwner()
	j.mu.Lock()
	err := j.store.InsertHelper(ctx, store.HelperRecord{Helper: h, Owner: owner, LeaseUntil: now.Add(helperLeaseDuration).UnixMilli(), NextTry: now.UnixMilli(), Delay: time.Minute.Milliseconds()})
	if err == nil {
		if j.active == nil {
			j.active = map[string]bool{}
		}
		j.active[h.ID] = true
	}
	j.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("cannot persist helper intent; no Pod created: %w", err)
	}
	hbctx, cancel := context.WithCancel(context.Background())
	lease := &helperLease{journal: j, id: h.ID, owner: owner, stop: cancel, done: make(chan struct{})}
	go func() {
		defer close(lease.done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-hbctx.Done():
				return
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(hbctx, 5*time.Second)
				err := j.store.RenewHelper(ctx, h.ID, owner, j.now().Add(helperLeaseDuration).UnixMilli())
				cancel()
				if err != nil {
					stopRun()
					return
				}
			}
		}
	}()
	return lease, nil
}
func (l *helperLease) PinUID(ctx context.Context, uid string) error {
	if uid == "" {
		return errors.New("helper CREATE returned no UID")
	}
	return l.journal.store.PinHelperUID(ctx, l.id, l.owner, uid)
}
func (l *helperLease) Finish(ctx context.Context, clean bool) error {
	l.once.Do(func() {
		l.stop()
		<-l.done
		l.journal.mu.Lock()
		defer l.journal.mu.Unlock()
		l.err = l.journal.store.FinishHelper(ctx, l.id, l.owner, clean, l.journal.now().UnixMilli())
		delete(l.journal.active, l.id)
	})
	return l.err
}
func (j *helperJournal) claim(ctx context.Context, r store.HelperRecord) (string, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.active[r.Helper.ID] {
		return "", false, nil
	}
	owner := newHelperOwner()
	now := j.now()
	ok, err := j.store.ClaimHelper(ctx, r.Helper.ID, owner, now.UnixMilli(), now.Add(helperLeaseDuration).UnixMilli())
	return owner, ok, err
}
func (s *Service) wakeHelperCleanup() {
	select {
	case s.helperWake <- struct{}{}:
	default:
	}
}
func (s *Service) startHelperRecovery(ctx context.Context) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-s.helperWake:
			}
			s.reapOwnedHelpers(ctx)
		}
	}()
}

// Only live explicit connections are candidates. Background recovery never
// discovers targets, opens sources, or emits UI notifications. Authenticated
// requests reuse the already-connected provider client.
func (s *Service) reapOwnedHelpers(ctx context.Context) {
	s.sessMu.Lock()
	var entries []*sessionEntry
	for key, e := range s.sessions {
		if s.explicitConnectionLocked(key, e) {
			entries = append(entries, e)
		}
	}
	s.sessMu.Unlock()
	for _, e := range entries {
		cleaner, ok := e.sess.(provider.HelperCleaner)
		if !ok {
			continue
		}
		records, err := s.store.DueHelpers(ctx, e.provider, e.target, s.now().UnixMilli())
		if err != nil {
			continue
		}
		for i, r := range records {
			if i >= 4 || ctx.Err() != nil {
				break
			}
			s.sessMu.Lock()
			live := s.sessions[ownerKey(e.provider, e.target)] == e && s.explicitConnectionLocked(ownerKey(e.provider, e.target), e)
			var connectionContext context.Context
			if live {
				connectionContext = s.connections[ownerKey(e.provider, e.target)].liveCtx
			}
			s.sessMu.Unlock()
			if !live {
				break
			}
			owner, claimed, err := s.helperJournal.claim(ctx, r)
			if err != nil || !claimed {
				continue
			}
			attempt, stop := context.WithTimeout(ctx, 10*time.Second)
			stopConnection := context.AfterFunc(connectionContext, stop)
			clean, err := cleaner.CleanupOwnedHelper(attempt, r.Helper, func(uid string) error { return s.store.PinHelperUID(attempt, r.Helper.ID, owner, uid) })
			stopConnection()
			stop()
			// The schedule advances after every failed attempt, including ambiguous
			// deletes. No state is reset by reconnecting or restarting the app.
			finish, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = s.store.FinishHelper(finish, r.Helper.ID, owner, clean && err == nil, s.now().UnixMilli())
			cancel()
		}
	}
}
