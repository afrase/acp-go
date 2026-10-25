//go:build !unix

package acp

import "os/exec"

// ownGroup does nothing: process groups are Unix's.
func ownGroup(*exec.Cmd) {}

// kill kills cmd's process; the processes it started are left.
func kill(cmd *exec.Cmd) error { return cmd.Process.Kill() }
