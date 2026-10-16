package helm

import (
	"context"
	"time"
)

// Helm 4.3's upgrade cancellation listener can start after RunWithContext has
// returned. Cancelling the caller during cleanup can then race with its closed
// completion channel and mark a successful release failed. Detach only that
// listener once the SDK returns; requests and waits retain the original bounded
// operation context through Configuration and WaitOptions.
func newUpgradeContext(parent context.Context) (context.Context, func() bool) {
	bridge, cancel := context.WithCancelCause(context.WithoutCancel(parent))
	forward := func() { cancel(context.Cause(parent)) }
	stop := context.AfterFunc(parent, forward)
	if parent.Err() != nil {
		forward()
	}
	return upgradeContext{Context: bridge, parent: parent}, stop
}

type upgradeContext struct {
	context.Context
	parent context.Context
}

func (c upgradeContext) Deadline() (time.Time, bool) { return c.parent.Deadline() }
func (c upgradeContext) Err() error {
	if c.Context.Err() != nil {
		return c.parent.Err()
	}
	return nil
}
