//go:build unix

package process

import (
	"os/exec"
	"syscall"
	"testing"
)

// Once the program has exited and its group was swept, the group is
// signalled no more: its id, the program's pid, may name another group by
// then.
func TestAGroupIsKilledByItsIDOnceTheProgramExited(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	var killed []int
	g := NewGroup(cmd)
	g.killpg = func(pgid int, _ syscall.Signal) error {
		killed = append(killed, pgid)
		return nil
	}
	_ = g.Kill() // before the sweep: the group
	_ = g.Sweep()
	_ = g.Kill()
	_ = g.Sweep()
	_ = g.Signal(syscall.SIGTERM)
	if len(killed) != 2 || killed[0] != cmd.Process.Pid || killed[1] != cmd.Process.Pid {
		t.Errorf("the group was signalled %d times: %v", len(killed), killed)
	}
}

// A signal reaches the program alone when its group is gone.
func TestASignalWithoutTheGroupReachesTheProgram(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	g := NewGroup(cmd)
	var sent []syscall.Signal
	g.killpg = func(_ int, sig syscall.Signal) error {
		sent = append(sent, sig)
		return syscall.ESRCH
	}
	if err := g.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	err := cmd.Wait()
	if sig := ExitSignal(err); sig != syscall.SIGTERM {
		t.Errorf("ended by %v (%v)", sig, err)
	}
	if len(sent) != 1 || sent[0] != syscall.SIGTERM {
		t.Errorf("the group got %v", sent)
	}
	if ExitSignal(nil) != nil || ExitSignal(exec.ErrNotFound) != nil {
		t.Error("a signal for no exit")
	}
}
