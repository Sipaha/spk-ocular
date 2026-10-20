package kubernetes

import (
	"context"
	"errors"
	"testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
)

func TestHelperRecoveryChecksNamespaceOwnershipAndUID(t *testing.T) {
	for _, mode := range []string{"normal", "ambiguous", "missing", "replacement", "marker", "owner", "namespace", "pin-error", "target"} {
		t.Run(mode, func(t *testing.T) {
			s, c := nodeShellSession(t)
			h := shellHandle(t, s)
			p := h.g.pod()
			p.SetUID("created-uid")
			record := core.OwnedHelper{ID: h.g.Nonce, Provider: ProviderID, Target: s.conn.target, Namespace: h.g.Namespace, NamespaceUID: string(h.g.NamespaceUID), Name: h.g.Name, UID: "created-uid", NodeName: h.g.Ref.Name, NodeUID: h.g.Ref.UID}
			switch mode {
			case "ambiguous", "pin-error":
				record.UID = ""
			case "replacement":
				p.SetUID("replacement")
			case "marker":
				p.SetLabels(nil)
			case "owner":
				p.SetOwnerReferences(nil)
			case "namespace":
				record.NamespaceUID = "other-namespace"
			case "target":
				record.Target = "other-target"
			}
			if mode != "missing" {
				require.NoError(t, c.Tracker().Create(podGVR, p, p.GetNamespace()))
			}
			pinned := ""
			clean, err := s.CleanupOwnedHelper(t.Context(), record, func(uid string) error {
				if mode == "pin-error" {
					return errors.New("journal unavailable")
				}
				pinned = uid
				return nil
			})
			switch mode {
			case "normal", "ambiguous":
				require.NoError(t, err)
				require.True(t, clean)
				require.Equal(t, 1, countShellWrites(c, "delete"))
				if mode == "ambiguous" {
					require.Equal(t, "created-uid", pinned)
				}
			case "missing", "replacement":
				require.NoError(t, err)
				require.True(t, clean)
				require.Zero(t, countShellWrites(c, "delete"))
			default:
				require.Error(t, err)
				require.False(t, clean)
				require.Zero(t, countShellWrites(c, "delete"))
			}
			if mode == "replacement" {
				got, err := c.Resource(podGVR).Namespace(record.Namespace).Get(t.Context(), record.Name, metav1.GetOptions{})
				require.NoError(t, err)
				require.EqualValues(t, "replacement", got.GetUID())
			}
		})
	}
}

type failingHelperJournal struct{}

func (failingHelperJournal) Begin(context.Context, core.OwnedHelper, context.CancelFunc) (provider.HelperLease, error) {
	return nil, errors.New("cannot persist helper intent")
}
func TestHelperJournalFailurePreventsCreate(t *testing.T) {
	s, c := nodeShellSession(t)
	h := shellHandle(t, s)
	h.SetHelperJournal(failingHelperJournal{})
	_, err := h.Run(t.Context(), provider.Terminal{})
	require.ErrorContains(t, err, "persist")
	require.Zero(t, countShellWrites(c, "create"))
	require.Zero(t, countShellWrites(c, "delete"))
}

type recordingHelperJournal struct {
	began, pinned, finished bool
	clean                   bool
	createObserved          *bool
}

func (j *recordingHelperJournal) Begin(_ context.Context, r core.OwnedHelper, _ context.CancelFunc) (provider.HelperLease, error) {
	j.began = r.ID != "" && r.NamespaceUID != "" && r.NodeUID != "" && !*j.createObserved
	return j, nil
}
func (j *recordingHelperJournal) PinUID(_ context.Context, uid string) error {
	j.pinned = uid == "helper-uid"
	return nil
}
func (j *recordingHelperJournal) Finish(_ context.Context, clean bool) error {
	j.finished, j.clean = true, clean
	return nil
}
func TestHelperJournalRecordsBeforeCreateAndPinsBeforeHostExec(t *testing.T) {
	s, c := nodeShellSession(t)
	readyShells(c)
	h := shellHandle(t, s)
	created := false
	j := &recordingHelperJournal{createObserved: &created}
	h.SetHelperJournal(j)
	c.PrependReactor("create", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		require.True(t, j.began)
		created = true
		return false, nil, nil
	})
	// A failed startup read still cleans the created helper and its record.
	c.PrependReactor("get", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		require.True(t, j.pinned)
		return true, nil, errors.New("startup read failed")
	})
	_, err := h.Run(t.Context(), provider.Terminal{})
	require.Error(t, err)
	require.True(t, j.began)
	require.True(t, j.pinned)
	require.True(t, j.finished)
	require.False(t, j.clean, "failed confirmation retains the durable record")
}

type pinFailureJournal struct{ recordingHelperJournal }

func (j *pinFailureJournal) Begin(ctx context.Context, r core.OwnedHelper, stop context.CancelFunc) (provider.HelperLease, error) {
	_, err := j.recordingHelperJournal.Begin(ctx, r, stop)
	return j, err
}
func (j *pinFailureJournal) PinUID(context.Context, string) error {
	return errors.New("cannot persist helper UID")
}
func TestHelperUIDPersistenceFailurePreventsHostAccessAndCleans(t *testing.T) {
	s, c := nodeShellSession(t)
	readyShells(c)
	h := shellHandle(t, s)
	created := false
	j := &pinFailureJournal{recordingHelperJournal{createObserved: &created}}
	h.SetHelperJournal(j)
	_, err := h.Run(t.Context(), provider.Terminal{})
	require.ErrorContains(t, err, "persist helper UID")
	require.Equal(t, 1, countShellWrites(c, "create"))
	require.Equal(t, 1, countShellWrites(c, "delete"))
	require.True(t, j.finished)
	require.True(t, j.clean)
}
