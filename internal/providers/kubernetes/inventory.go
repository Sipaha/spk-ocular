package kubernetes

import "context"

// inventoryContext binds a snapshot to its session before waiting for the shared
// inventory gate. Cancellation while queued must not wait for another snapshot.
func (s *session) inventoryContext(parent context.Context) (context.Context, func(), error) {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(s.ctx, cancel)
	cleanup := func() { stop(); cancel() }
	if s.ctx.Err() != nil {
		cancel()
	}
	select {
	case s.graphGate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-s.graphGate
			cleanup()
			return nil, nil, err
		}
		return ctx, func() { <-s.graphGate; cleanup() }, nil
	case <-ctx.Done():
		cleanup()
		return nil, nil, ctx.Err()
	}
}

// inventoryCursor owns continuation identity only. Resource/page/byte budgets,
// data projection and error/absence semantics remain with each snapshot adapter.
type inventoryCursor struct {
	token string
	seen  map[string]bool
}

// advance returns whether another page exists and whether a repeated token made
// the inventory incomplete. The initial empty token is never a continuation.
func (p *inventoryCursor) advance(next string) (more, repeated bool) {
	if next == "" {
		return false, false
	}
	if p.seen[next] {
		return false, true
	}
	if p.seen == nil {
		p.seen = make(map[string]bool)
	}
	p.seen[next] = true
	p.token = next
	return true, false
}
