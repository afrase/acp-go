//go:build windows

package process_test

import (
	"os/exec"
	"syscall"
	"testing"

	"github.com/afrase/acp-go/process"
)

// The program starts a console process group of its own, so the console's
// Ctrl+C doesn't reach it; the caller's flags stay.
func TestTheProgramHasAConsoleGroupOfItsOwn(t *testing.T) {
	cmd := exec.Command("agent")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_UNICODE_ENVIRONMENT}
	process.OwnGroup(cmd)
	if f := cmd.SysProcAttr.CreationFlags; f&syscall.CREATE_NEW_PROCESS_GROUP == 0 || f&syscall.CREATE_UNICODE_ENVIRONMENT == 0 {
		t.Errorf("creation flags %#x", f)
	}
}
