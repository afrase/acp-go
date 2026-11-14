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

// group is a started agent's process group.
type group struct{ cmd *exec.Cmd }

func newGroup(cmd *exec.Cmd) *group { return &group{cmd: cmd} }

// kill kills the agent's process group, or, when the agent doesn't lead
// one (its SysProcAttr put it in another), the agent alone.
func (g *group) kill() error {
	if err := syscall.Kill(-g.cmd.Process.Pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return g.cmd.Process.Kill()
}

// release lets go of what the group holds, once the agent has been waited
// for.
func (g *group) release() {}
