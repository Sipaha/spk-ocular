package compose

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestExplicitRefreshWaitsForCompletedSharedEpoch(t *testing.T) {
	f := &feed{resync: make(chan struct{}, 1), changed: make(chan struct{}), done: make(chan struct{})}
	f.epochs.Store(1)
	target := f.queueFresh()
	require.EqualValues(t, 2, target)
	require.Equal(t, target, f.queueFresh())
	require.Len(t, f.resync, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.waitFresh(ctx, target) }()
	f.setLive() // old epoch's completion cannot satisfy the requested replacement.
	select {
	case <-done:
		t.Fatal("refresh completed on old epoch")
	default:
	}
	f.epochs.Store(2)
	f.setLive()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("refresh did not finish with its snapshot")
	}
}
func TestExplicitRefreshFailureAndCancellation(t *testing.T) {
	f := &feed{resync: make(chan struct{}, 1), changed: make(chan struct{}), done: make(chan struct{})}
	target := f.queueFresh()
	f.epochs.Store(1)
	want := errors.New("fixture read failed")
	f.fail(want)
	require.ErrorIs(t, f.waitFresh(context.Background(), target), want)
	target = f.queueFresh()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, f.waitFresh(ctx, target), context.Canceled)
	close(f.done)
	require.Error(t, f.waitFresh(context.Background(), target))
}
