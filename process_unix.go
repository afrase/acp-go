//go:build unix

package acp

import (
	"os/exec"
	"syscall"
)

// ownGroup has cmd run in a process group of its own. A command that asks
// for a session of its own (Setsid) leads its own group already, and
// can't be moved to another (setpgid fails), so it is left as it is.
func ownGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	if !cmd.SysProcAttr.Setsid {
		cmd.SysProcAttr.Setpgid = true
	}
}

// kill kills cmd's process group, or, when cmd doesn't lead one (its
// SysProcAttr put it in another), cmd's process alone.
func kill(cmd *exec.Cmd) error {
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}
