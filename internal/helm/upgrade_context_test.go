package helm

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUpgradeContextDisarmsCleanupAfterCompletion(t *testing.T) {
	type key struct{}
	parent, cancel := context.WithTimeout(context.WithValue(t.Context(), key{}, "retained"), time.Minute)
	defer cancel()
	ctx, stop := newUpgradeContext(parent)
	require.Equal(t, "retained", ctx.Value(key{}))
	deadline, ok := ctx.Deadline()
	want, _ := parent.Deadline()
	require.True(t, ok)
	require.Equal(t, want, deadline)
	require.True(t, stop())
	cancel()
	// Model an SDK listener scheduled only after the operation has returned:
	// cleanup must leave only its SDK completion channel ready, not cancellation.
	select {
	case <-ctx.Done():
		t.Fatal("successful upgrade received a late cleanup cancellation")
	default:
	}
	require.NoError(t, ctx.Err())
	require.NoError(t, context.Cause(ctx))
}

func TestUpgradeContextForwardsActiveCancellation(t *testing.T) {
	parent, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	ctx, stop := newUpgradeContext(parent)
	defer stop()
	cause := errors.New("connection closed")
	cancel(cause)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("active upgrade did not receive cancellation")
	}
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.ErrorIs(t, context.Cause(ctx), cause)
}

func TestUpgradeContextPreservesExpiredDeadline(t *testing.T) {
	parent, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	ctx, stop := newUpgradeContext(parent)
	defer stop()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("already expired upgrade was not cancelled synchronously")
	}
	require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
	require.ErrorIs(t, context.Cause(ctx), context.DeadlineExceeded)
}
