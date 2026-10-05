package agentapi

import (
	"errors"
	"golang.org/x/sys/unix"
	"net"
)

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
		cred *unix.Xucred
		cerr error
	)
	if err := raw.Control(func(fd uintptr) { cred, cerr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED) }); err != nil {
		return -1, err
	}
	if cerr != nil {
		return -1, cerr
	}
	return int(cred.Uid), nil
}
