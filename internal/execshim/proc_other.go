//go:build !linux

package execshim

import "os/exec"

func dieWithParent() {}

func setChildAttrs(*exec.Cmd) {}

func killTree(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
