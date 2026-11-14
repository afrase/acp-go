//go:build unix

package acp_test

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/afrase/acp-go"
	"github.com/afrase/acp-go/internal/leaktest"
)

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// startWith starts the test agent with attr as its SysProcAttr.
func startWith(t *testing.T, attr *syscall.SysProcAttr, env ...string) (*acp.AgentConn, *exec.Cmd, error) {
	t.Helper()
	leaktest.Check(t)
	cmd := exec.Command(os.Args[0]) //nolint:gosec // the test binary
	cmd.Env = append(append(os.Environ(), "ACPGO_TEST_AGENT=1", "GORACE=atexit_sleep_ms=0"), env...)
	cmd.SysProcAttr = attr
	conn, err := acp.Start(cmd, func(*acp.AgentConn) acp.Client { return &testClient{} })
	if err == nil {
		t.Cleanup(func() {
			_ = conn.Close()
			<-conn.Done()
		})
	}
	return conn, cmd, err
}

// An agent asked to run in a session of its own starts (it leads its own
// group already).
func TestStartWithASessionOfItsOwn(t *testing.T) {
	conn, _, err := startWith(t, &syscall.SysProcAttr{Setsid: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Initialize(ctxFor(t), &acp.InitializeRequest{ProtocolVersion: acp.LatestProtocolVersion}); err != nil {
		t.Fatal(err)
	}
}

// An agent put in a group it doesn't lead is still killed by Close: Done
// doesn't wait for it forever (this one goes on 30s after its input
// ends).
func TestCloseKillsAnAgentInAnotherGroup(t *testing.T) {
	leader := exec.Command("sleep", "30")
	leader.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := leader.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = leader.Process.Kill()
		_ = leader.Wait()
	}()
	conn, cmd, err := startWith(t, &syscall.SysProcAttr{Setpgid: true, Pgid: leader.Process.Pid}, "ACPGO_TEST_EXIT_DELAY=30s")
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	select {
	case <-conn.Done():
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill() // for the cleanup
		t.Fatal("Done is still open 5s after Close")
	}
}
