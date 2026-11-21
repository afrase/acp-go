//go:build !unix && !windows

package process

import (
	"os"
	"os/exec"
)

// OwnGroup does nothing: this system has no process groups this package
// knows.
func OwnGroup(*exec.Cmd) {}

// Group is a started program's process.
type Group struct{ cmd *exec.Cmd }

// NewGroup is cmd's process, once it has started.
func NewGroup(cmd *exec.Cmd) *Group { return &Group{cmd: cmd} }

// Kill kills the program; the processes it started are left.
func (g *Group) Kill() error { return g.cmd.Process.Kill() }

// Signal sends sig to the program.
func (g *Group) Signal(sig os.Signal) error { return g.cmd.Process.Signal(sig) }

// Sweep kills the program once it has exited, which does nothing.
func (g *Group) Sweep() error { return g.Kill() }

// Release does nothing.
func (g *Group) Release() {}

// ExitSignal is nil: this package doesn't know this system's signals.
func ExitSignal(error) os.Signal { return nil }
