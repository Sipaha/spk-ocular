package api

import (
	"context"
	"errors"
	"github.com/spk/spk-ocular/internal/providers/kubernetes"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestConfigurationsUIOnly(t *testing.T) {
	p, err := kubernetes.NewWith(func(string) string { return "" }, t.TempDir()).WithConfigurations(t.TempDir())
	require.NoError(t, err)
	s, _ := newService(t, p)
	_, err = s.Configurations(context.Background(), kubernetes.ConfigRequest{Command: "setup", Password: "a valid master password"})
	require.ErrorContains(t, err, "only in the UI")
	state, err := s.Configurations(UIContext(t.Context()), kubernetes.ConfigRequest{Command: "status"})
	require.NoError(t, err)
	require.False(t, state.Encrypted)
}

func TestRemovingConfigurationClosesItsOpenSession(t *testing.T) {
	p, err := kubernetes.NewWith(func(string) string { return "" }, t.TempDir()).WithConfigurations(t.TempDir())
	require.NoError(t, err)
	s, _ := newService(t, p)
	ctx := UIContext(t.Context())
	_, err = s.Configurations(ctx, kubernetes.ConfigRequest{Command: "setup", Password: "a valid master password"})
	require.NoError(t, err)
	st, err := s.Configurations(ctx, kubernetes.ConfigRequest{Command: "create", Name: "Demo", YAML: "apiVersion: v1\nkind: Config\nclusters:\n- name: c\n  cluster:\n    server: https://127.0.0.1:1\ncontexts:\n- name: c\n  context:\n    cluster: c\n    user: c\nusers:\n- name: c\n  user: {}\n"})
	require.NoError(t, err)
	d, err := p.Discover(ctx)
	require.NoError(t, err)
	require.Len(t, d.Targets, 1)
	target := d.Targets[0]
	session := &fakeSession{}
	key := ownerKey(kubernetes.ProviderID, target.ID)
	s.sessions[key] = &sessionEntry{sess: session, provider: kubernetes.ProviderID, target: target.ID, hash: target.ConfigHash, owner: key}
	entry := st.Entries[0]
	_, err = s.Configurations(ctx, kubernetes.ConfigRequest{Command: "remove", ID: entry.ID, Expect: entry.Revision})
	require.NoError(t, err)
	require.True(t, session.isClosed())
	require.NotContains(t, s.sessions, key)
}

func TestConfigurationRevealRunsOnlyRegisteredSource(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux launcher fixture")
	}
	home := t.TempDir()
	folder := filepath.Join(home, ".kube")
	require.NoError(t, os.MkdirAll(folder, 0700))
	path := filepath.Join(folder, "config")
	require.NoError(t, os.WriteFile(path, []byte("apiVersion: v1\nkind: Config\nclusters:\n- name: c\n  cluster:\n    server: https://127.0.0.1:1\ncontexts:\n- name: c\n  context:\n    cluster: c\n"), 0600))
	p, err := kubernetes.NewWith(func(string) string { return "" }, home).WithConfigurations(t.TempDir())
	require.NoError(t, err)
	s, _ := newService(t, p)
	var revealed []string
	s.revealLocalFile = func(_ context.Context, got string) error { revealed = append(revealed, got); return nil }
	st, err := s.Configurations(UIContext(t.Context()), kubernetes.ConfigRequest{Command: "import", Paths: []string{path}})
	require.NoError(t, err)
	entry := st.Entries[0]
	req := kubernetes.ConfigRequest{Command: "reveal", ID: entry.ID, Expect: entry.Revision}
	_, err = s.Configurations(t.Context(), req)
	require.Error(t, err)

	require.Empty(t, revealed)
	_, err = s.Configurations(UIContext(t.Context()), req)
	require.NoError(t, err)
	require.Equal(t, []string{path}, revealed)
	req.Expect = "stale"
	_, err = s.Configurations(UIContext(t.Context()), req)
	require.Error(t, err)
	require.Len(t, revealed, 1)
	req.Expect = entry.Revision
	s.revealLocalFile = func(context.Context, string) error { return errors.New("could not select item") }
	_, err = s.Configurations(UIContext(t.Context()), req)
	require.ErrorContains(t, err, "could not select")
}
