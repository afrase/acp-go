//go:build !unix && !windows

package acp

import "os/exec"

// ownGroup does nothing: this system has no process groups Start knows.
func ownGroup(*exec.Cmd) {}

// group is a started agent's process.
type group struct{ cmd *exec.Cmd }

func newGroup(cmd *exec.Cmd) *group { return &group{cmd: cmd} }

// kill kills the agent's process; the processes it started are left.
func (g *group) kill() error { return g.cmd.Process.Kill() }

// sweep kills the agent once it has exited, which does nothing.
func (g *group) sweep() error { return g.kill() }

func (g *group) release() {}
