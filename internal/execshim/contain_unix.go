//go:build !windows

package execshim

import "os/exec"

func containChild(cmd *exec.Cmd) (kill, release func(), err error) {
	return func() { killTree(cmd) }, func() {}, nil
}
