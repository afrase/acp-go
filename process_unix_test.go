//go:build unix

package acp_test

import (
	"syscall"
	"testing"
	"time"
)

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// What an agent started dies with the connection, even when it holds the
// agent's stdout.
func TestTheAgentsProcessesEndWithIt(t *testing.T) {
	for _, how := range []string{"close", "exit"} {
		t.Run(how, func(t *testing.T) {
			conn, _ := startTestAgent(t, &testClient{})
			ctx := ctxFor(t)
			var pid int
			if err := conn.Call(ctx, "_test/spawn", nil, &pid); err != nil {
				t.Fatal(err)
			}
			if !alive(pid) {
				t.Fatal("no child")
			}
			start := time.Now()
			if how == "close" {
				_ = conn.Close()
			} else {
				_ = conn.Call(ctx, "_test/exit", nil, nil) // its child still holds its stdout
			}
			<-conn.Done()
			if took := time.Since(start); took > 5*time.Second {
				t.Errorf("the connection took %v to end", took)
			}
			deadline := time.Now().Add(5 * time.Second)
			for alive(pid) && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			if alive(pid) {
				t.Errorf("the agent's child %d outlived it", pid)
			}
		})
	}
}
