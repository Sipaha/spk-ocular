package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
)

type viewOpening struct {
	cancel    context.CancelFunc
	requestID string
	viewID    string
}
type viewOpeningKey struct{}

type viewLoading struct {
	mu        sync.Mutex
	cancelled map[string]bool
	pending   map[string]map[*viewOpening]struct{}
}

// Tokens distinguish explicit UI connection incarnations even when an agent
// holds the same provider session across Disconnect/Connect. State is in memory
// in the app, not in browser storage or saved target preferences.
func viewLoadingKey(owner string, req OpenViewRequest) string {
	q := req.Query
	q.Schema = 0
	data, _ := json.Marshal([]any{owner, req.Connection, q})
	return string(data)
}
func (l *viewLoading) begin(ctx context.Context, key string, retry bool, requestID string) (context.Context, func(), error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cancelled == nil {
		l.cancelled = map[string]bool{}
	}
	if l.pending == nil {
		l.pending = map[string]map[*viewOpening]struct{}{}
	}
	if retry {
		delete(l.cancelled, key)
	}
	if l.cancelled[key] {
		return nil, nil, coded(CodeCancelled, errors.New("request cancelled"))
	}
	child, cancel := context.WithCancel(ctx)
	op := &viewOpening{cancel: cancel, requestID: requestID}
	if l.pending[key] == nil {
		l.pending[key] = map[*viewOpening]struct{}{}
	}
	l.pending[key][op] = struct{}{}
	child = context.WithValue(child, viewOpeningKey{}, op)
	return child, func() {
		cancel()
		l.mu.Lock()
		if op.viewID == "" {
			delete(l.pending[key], op)
		}
		if len(l.pending[key]) == 0 {
			delete(l.pending, key)
		}
		l.mu.Unlock()
	}, nil
}

// CancelViewLoading latches this query, cancels a pending schema probe and
// closes this view's observation. Shared caches keep their other consumers.
func (s *Service) CancelViewLoading(ctx context.Context, req CancelViewLoadingRequest) error {
	if !req.Query.Scope.Valid() {
		return coded(CodeBadRequest, errors.New("invalid scope selector"))
	}
	e, err := s.sessionFor(ctx, req.Provider, req.Target)
	if err != nil {
		return err
	}
	if req.ViewID != "" {
		owner, err := s.views.Owner(req.ViewID)
		if err == nil && owner != e.owner {
			return coded(CodeGone, errors.New("view belongs to another session"))
		}
	}
	key := viewLoadingKey(e.owner, req.OpenViewRequest)
	s.loading.mu.Lock()
	if s.loading.cancelled == nil {
		s.loading.cancelled = map[string]bool{}
	}
	s.loading.cancelled[key] = true
	ids := []string{}
	for op := range s.loading.pending[key] {
		if op.requestID == req.RequestID {
			op.cancel()
			if op.viewID != "" {
				ids = append(ids, op.viewID)
			}
		}
	}
	s.loading.mu.Unlock()
	for _, id := range ids {
		_ = s.CloseView(ctx, id)
	}
	if req.ViewID != "" {
		return s.CloseView(ctx, req.ViewID)
	}
	return nil
}

func (l *viewLoading) forgetOwner(owner string) {
	prefix, _ := json.Marshal(owner)
	match := string(append(append([]byte{'['}, prefix...), ','))
	l.mu.Lock()
	defer l.mu.Unlock()
	for key := range l.cancelled {
		if strings.HasPrefix(key, match) {
			delete(l.cancelled, key)
		}
	}
	for key, ops := range l.pending {
		if strings.HasPrefix(key, match) {
			for op := range ops {
				op.cancel()
			}
			delete(l.pending, key)
		}
	}
}

// Keep the request-to-view association until Close/expiry so cancellation can
// overtake delivery of OpenView's result without orphaning a backend lease.
func (l *viewLoading) bind(ctx context.Context, id string) {
	op, ok := ctx.Value(viewOpeningKey{}).(*viewOpening)
	if !ok {
		return
	}
	l.mu.Lock()
	op.viewID = id
	l.mu.Unlock()
}
func (l *viewLoading) closeView(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for key, ops := range l.pending {
		for op := range ops {
			if op.viewID == id {
				delete(ops, op)
			}
		}
		if len(ops) == 0 {
			delete(l.pending, key)
		}
	}
}
func (l *viewLoading) prune(exists func(string) bool) {
	l.mu.Lock()
	ids := []string{}
	for _, ops := range l.pending {
		for op := range ops {
			if op.viewID != "" {
				ids = append(ids, op.viewID)
			}
		}
	}
	l.mu.Unlock()
	for _, id := range ids {
		if !exists(id) {
			l.closeView(id)
		}
	}
}
