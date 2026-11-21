//go:build unix

package acp

import (
	"os/exec"
	"sync"
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
type group struct {
	cmd    *exec.Cmd
	killpg func(pgid int) error // SIGKILLs a process group (a test's own func)

	mu    sync.Mutex
	swept bool
}

func newGroup(cmd *exec.Cmd) *group {
	return &group{cmd: cmd, killpg: func(pgid int) error { return syscall.Kill(-pgid, syscall.SIGKILL) }}
}

// kill kills the agent's process group, or, when the agent doesn't lead
// one (its SysProcAttr put it in another), the agent alone. Once the
// group was swept, it kills the agent alone, which does nothing once the
// agent was waited for: the group's id is the agent's pid, which may name
// another group by then.
func (g *group) kill() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.swept && g.killpg(g.cmd.Process.Pid) == nil {
		return nil
	}
	return g.cmd.Process.Kill()
}

// sweep kills what the agent left running in its group, once the agent
// has exited. Those processes die then, and start no more, so later kills
// leave the group alone.
func (g *group) sweep() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.swept {
		return nil
	}
	g.swept = true
	return g.killpg(g.cmd.Process.Pid)
}

// release lets go of what the group holds, once the agent has been waited
// for.
func (g *group) release() {}
