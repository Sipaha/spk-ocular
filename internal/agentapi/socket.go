package agentapi

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

// ErrOtherInstance: another process of this data dir owns the socket.
var ErrOtherInstance = errors.New("another SPK Ocular instance serves agent access")

// socket is the owned agent socket: the lock is held for the process's
// life (one owner per data dir), the socket file is the owner's.
type socket struct {
	path string
	lock *os.File
	ln   net.Listener
	once sync.Once
}

// peerUID reads the connecting process's uid (a var: tests cannot connect
// as another user).
var peerUID = func(c net.Conn) (int, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return -1, errors.New("not a unix connection")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return -1, err
	}
	var (
		cred *unix.Ucred
		cerr error
	)
	if err := raw.Control(func(fd uintptr) { cred, cerr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }); err != nil {
		return -1, err
	}
	if cerr != nil {
		return -1, cerr
	}
	return int(cred.Uid), nil
}

// listen takes the lock and serves path: owner-only, a stale file of a
// dead owner replaced. Without the lock: ErrOtherInstance, and nothing of
// the owner's is touched.
func listen(path, lockPath string) (*socket, error) {
	lf, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(lf.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = lf.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrOtherInstance
		}
		return nil, fmt.Errorf("lock %s: %w", lockPath, err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = lf.Close()
		return nil, err
	}
	// No process-wide umask (other goroutines create files meanwhile): the
	// file is made owner-only right after; until then it has the default
	// mode (others cannot write, i.e. connect) in the 0700 data directory,
	// and connections of other users are dropped anyway (ownUIDListener).
	ln, err := net.Listen("unix", path)
	if err != nil {
		_ = lf.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		_ = lf.Close()
		return nil, err
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false) // removed under the lock, below
	return &socket{path: path, lock: lf, ln: &ownUIDListener{Listener: ln, uid: os.Getuid()}}, nil
}

// close stops listening, removes the socket file and releases the lock.
func (s *socket) close() {
	s.once.Do(func() {
		_ = s.ln.Close()
		_ = os.Remove(s.path)
		_ = s.lock.Close() // releases the flock
	})
}

// ownUIDListener drops connections of other users (the socket file is
// 0600 already: a cheap second check).
type ownUIDListener struct {
	net.Listener
	uid int
}

func (l *ownUIDListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		uid, err := peerUID(c)
		if err == nil && uid == l.uid {
			return c, nil
		}
		slog.Warn("agent socket: connection of another user refused", "uid", uid, "err", err)
		_ = c.Close()
	}
}
