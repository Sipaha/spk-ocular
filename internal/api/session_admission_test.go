package api

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUIAdmissionNeverSubstitutesImplicitReplacement(t *testing.T) {
	for _, reviewed := range []bool{false, true} {
		t.Run(map[bool]string{false: "read", true: "reviewed"}[reviewed], func(t *testing.T) {
			p := newOpenable("a")
			s, _ := newService(t, p)
			ui := UIContext(t.Context())
			_, err := s.ConnectTarget(ui, "k", "a")
			require.NoError(t, err)
			awaitConnection(t, s, "connected")
			s.afterSessionResolved = func() {
				require.NoError(t, s.CloseTarget(ui, "k", "a"))
				_, err := s.session(context.Background(), "k", "a")
				require.NoError(t, err)
			}
			t.Cleanup(func() { s.afterSessionResolved = nil })
			if reviewed {
				got, err := s.checkedSession(ui, "k", "a", s.configRev("h1"))
				require.Nil(t, got)
				require.True(t, IsCoded(err, CodeGone))
			} else {
				got, err := s.sessionFor(ui, "k", "a")
				require.Nil(t, got)
				require.True(t, IsCoded(err, CodeGone))
			}
			require.Equal(t, "disconnected", connectionState(t, s).State)
			require.Len(t, p.opened, 2, "only the independent non-UI call can open its replacement")
		})
	}
}
