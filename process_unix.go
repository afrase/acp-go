//go:build unix

package acp

import (
	"os/exec"
	"syscall"
)

// ownGroup has cmd run in a process group of its own.
func ownGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// kill kills cmd's process group.
func kill(cmd *exec.Cmd) error {
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
