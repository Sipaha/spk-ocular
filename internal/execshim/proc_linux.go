//go:build linux

package execshim

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// dieWithParent: the shim ends when the app does (crash, kill -9 included).
func dieWithParent() {
	_ = unix.Prctl(unix.PR_SET_PDEATHSIG, uintptr(syscall.SIGKILL), 0, 0, 0)
	if os.Getppid() == 1 { // the app died before prctl took effect
		os.Exit(1)
	}
}

// setChildAttrs: the plugin gets its own process group (so its helpers go
// with it) and dies with the shim.
func setChildAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}

func killTree(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
