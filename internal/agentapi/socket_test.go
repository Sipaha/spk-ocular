package agentapi

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sockDir(t *testing.T) string {
	t.Helper()
	// A short path: unix socket paths are limited to ~108 bytes.
	d, err := os.MkdirTemp(os.Getenv("TMPDIR"), "as")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

func TestTheSocketIsOwnerOnlyAndHasOneOwner(t *testing.T) {
	d := sockDir(t)
	path, lock := filepath.Join(d, "agent.sock"), filepath.Join(d, "agent.sock.lock")
	a, err := listen(path, lock)
	require.NoError(t, err)
	st, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), st.Mode().Perm())
	assert.NotZero(t, st.Mode()&os.ModeSocket)

	_, err = listen(path, lock)
	assert.True(t, errors.Is(err, ErrOtherInstance), "%v", err)
	_, err = os.Stat(path)
	assert.NoError(t, err, "the second instance left the owner's socket alone")
	c, err := net.Dial("unix", path)
	require.NoError(t, err, "still served")
	_ = c.Close()

	a.close()
	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err), "removed at close")
	b, err := listen(path, lock)
	require.NoError(t, err, "the next instance takes over")
	b.close()
}

// A socket file left by a dead owner (no lock held) is replaced.
func TestAStaleSocketFileIsReplaced(t *testing.T) {
	d := sockDir(t)
	path := filepath.Join(d, "agent.sock")
	ln, err := net.Listen("unix", path)
	require.NoError(t, err)
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = ln.Close()
	_, err = os.Stat(path)
	require.NoError(t, err)
	s, err := listen(path, path+".lock")
	require.NoError(t, err)
	defer s.close()
	accepted := make(chan struct{})
	go func() {
		defer close(accepted)
		c, err := s.ln.Accept()
		if err == nil {
			_ = c.Close()
		}
	}()
	c, err := net.Dial("unix", path)
	require.NoError(t, err)
	_ = c.Close()
	<-accepted
}

func TestConnectionsOfAnotherUserAreRefused(t *testing.T) {
	d := sockDir(t)
	path := filepath.Join(d, "agent.sock")
	s, err := listen(path, path+".lock")
	require.NoError(t, err)
	defer s.close()
	old := peerUID
	t.Cleanup(func() { peerUID = old })
	calls := make(chan struct{}, 4)
	peerUID = func(net.Conn) (int, error) {
		calls <- struct{}{}
		if len(calls) == 1 {
			return os.Getuid() + 1, nil
		}
		return os.Getuid(), nil
	}
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := s.ln.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	t.Cleanup(func() { s.close() }) // before peerUID is restored: Accept ends
	stranger, err := net.Dial("unix", path)
	require.NoError(t, err)
	_ = stranger.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = stranger.Read(make([]byte, 1))
	assert.Error(t, err, "closed by the server")
	mine, err := net.Dial("unix", path)
	require.NoError(t, err)
	defer func() { _ = mine.Close() }()
	select {
	case c := <-accepted:
		_ = c.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("the own user's connection was not accepted")
	}
	assert.Len(t, calls, 2)
}

// The real peer check reads this process's own uid.
func TestPeerUIDReadsTheCredentials(t *testing.T) {
	d := sockDir(t)
	path := filepath.Join(d, "agent.sock")
	ln, err := net.Listen("unix", path)
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	go func() {
		c, err := net.Dial("unix", path)
		if err == nil {
			time.Sleep(100 * time.Millisecond)
			_ = c.Close()
		}
	}()
	c, err := ln.Accept()
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	uid, err := peerUID(c)
	require.NoError(t, err)
	assert.Equal(t, os.Getuid(), uid)
}
