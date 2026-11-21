//go:build unix

package process

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

// OwnGroup has cmd run in a process group of its own. A command that asks
// for a session of its own (Setsid) leads its own group already, and
// can't be moved to another (setpgid fails), so it is left as it is.
func OwnGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	if !cmd.SysProcAttr.Setsid {
		cmd.SysProcAttr.Setpgid = true
	}
}

// Group is a started program's process group: the one [OwnGroup] gave
// it, whose id is its pid.
type Group struct {
	cmd    *exec.Cmd
	killpg func(pgid int, sig syscall.Signal) error // signals a process group (a test's own func)

	mu    sync.Mutex
	swept bool
}

// NewGroup is the group of cmd, which has started.
func NewGroup(cmd *exec.Cmd) *Group {
	return &Group{cmd: cmd, killpg: func(pgid int, sig syscall.Signal) error { return syscall.Kill(-pgid, sig) }}
}

// Signal sends sig to the program's group, or, when the program doesn't
// lead one (its SysProcAttr put it in another, or the group is gone), to
// the program alone. Once the group was swept it signals the program
// alone, which does nothing once the program was waited for: the group's
// id is the program's pid, which may name another group by then.
func (g *Group) Signal(sig os.Signal) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if s, ok := sig.(syscall.Signal); ok && !g.swept && g.killpg(g.cmd.Process.Pid, s) == nil {
		return nil
	}
	return g.cmd.Process.Signal(sig)
}

// Kill kills the program's group (SIGKILL), as Signal does.
func (g *Group) Kill() error { return g.Signal(syscall.SIGKILL) }

// Sweep kills what the program left running in its group, once the
// program has exited. Those processes die then, and start no more, so
// later signals leave the group alone.
func (g *Group) Sweep() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.swept {
		return nil
	}
	g.swept = true
	return g.killpg(g.cmd.Process.Pid, syscall.SIGKILL)
}

// Release lets go of what the group holds, once the program has been
// waited for.
func (g *Group) Release() {}

// ExitSignal is the signal that ended the program whose Wait returned
// err, or nil.
func ExitSignal(err error) os.Signal {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return nil
	}
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return ws.Signal()
	}
	return nil
}
