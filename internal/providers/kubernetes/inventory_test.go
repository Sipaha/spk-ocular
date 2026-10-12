package kubernetes

import (
	"context"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/stretchr/testify/require"
)

func TestInventoryCursorRejectsRepeatedContinuation(t *testing.T) {
	var cursor inventoryCursor
	more, repeated := cursor.advance("a")
	require.True(t, more)
	require.False(t, repeated)
	more, repeated = cursor.advance("b")
	require.True(t, more)
	require.False(t, repeated)
	more, repeated = cursor.advance("a")
	require.False(t, more)
	require.True(t, repeated)
	require.Equal(t, "b", cursor.token)
	more, repeated = cursor.advance("")
	require.False(t, more)
	require.False(t, repeated)
}

func TestSnapshotCancellationWhileWaitingForInventoryGate(t *testing.T) {
	calls := map[string]func(*session, context.Context) error{
		"graph": func(s *session, c context.Context) error {
			_, e := s.Graph(c, core.ScopeSel{Mode: core.ScopeAll})
			return e
		},
		"timeline": func(s *session, c context.Context) error {
			_, e := s.Timeline(c, core.ScopeSel{Mode: core.ScopeAll})
			return e
		},
		"rbac": func(s *session, c context.Context) error {
			_, e := s.RBAC(c, core.ScopeSel{Mode: core.ScopeAll}, nil)
			return e
		},
		"security": func(s *session, c context.Context) error {
			_, e := s.Security(c, core.ScopeSel{Mode: core.ScopeAll})
			return e
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			sessionCtx, cancel := context.WithCancel(t.Context())
			defer cancel()
			s := &session{ctx: sessionCtx, graphGate: make(chan struct{}, 1)}
			s.graphGate <- struct{}{}
			result := make(chan error, 1)
			go func() { result <- call(s, t.Context()) }()
			cancel()
			select {
			case err := <-result:
				require.ErrorIs(t, err, context.Canceled)
			case <-time.After(time.Second):
				t.Fatal("session cancellation did not interrupt the inventory gate wait")
			}
			require.Len(t, s.graphGate, 1, "queued cancellation must not release another snapshot's gate")
		})
	}
}

func TestInventoryContextReleasesGateAndSessionHook(t *testing.T) {
	sessionCtx, endSession := context.WithCancel(t.Context())
	defer endSession()
	s := &session{ctx: sessionCtx, graphGate: make(chan struct{}, 1)}
	ctx, release, err := s.inventoryContext(t.Context())
	require.NoError(t, err)
	require.Len(t, s.graphGate, 1)
	release()
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.Empty(t, s.graphGate)
	// A later snapshot obtains the same gate, and ends with this session.
	ctx, release, err = s.inventoryContext(t.Context())
	require.NoError(t, err)
	defer release()
	endSession()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("session did not cancel active inventory")
	}
}
