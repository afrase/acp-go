package acp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/afrase/acp-go"
	"github.com/afrase/acp-go/internal/leaktest"
)

// TestMain runs the test binary as an agent when ACPGO_TEST_AGENT is set:
// the agent for the tests of Start.
func TestMain(m *testing.M) {
	if os.Getenv("ACPGO_TEST_AGENT") != "" {
		serveTestAgent()
		os.Exit(0)
	}
	m.Run()
}

// serveTestAgent is a testAgent on stdin and stdout whose prompt echoes
// it, with extensions to spawn a process that holds its stdout and to
// exit.
func serveTestAgent() {
	a := &testAgent{}
	a.prompt = func(ctx context.Context, a *testAgent, req *acp.PromptRequest) (*acp.PromptResponse, error) {
		return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, a.say(ctx, req.Prompt[0].Text.Text)
	}
	a.ext = func(ctx context.Context, method string, _ json.RawMessage) (any, error) {
		switch method {
		case "_test/spawn":
			cmd := exec.Command("sleep", "30")
			cmd.Stdout = os.Stdout // holds the agent's stdout open
			if err := cmd.Start(); err != nil {
				return nil, err
			}
			return cmd.Process.Pid, nil
		case "_test/exit":
			_ = a.say(ctx, "bye")
			os.Exit(3) //nolint:revive // the agent dies, as the test asks
		}
		return nil, errors.New("no such test method")
	}
	c := acp.NewClientConn(os.Stdin, os.Stdout, func(c *acp.ClientConn) acp.Agent { a.client = c; return a })
	<-c.Done()
	if d, err := time.ParseDuration(os.Getenv("ACPGO_TEST_EXIT_DELAY")); err == nil {
		time.Sleep(d) // ending its work, with its stdout closed
	}
}

func startTestAgent(t *testing.T, client acp.Client, env ...string) (*acp.AgentConn, *exec.Cmd) {
	t.Helper()
	leaktest.Check(t)
	cmd := exec.Command(os.Args[0]) //nolint:gosec // the test binary
	// GORACE: -race would wait a second as the agent exits.
	cmd.Env = append(append(os.Environ(), "ACPGO_TEST_AGENT=1", "GORACE=atexit_sleep_ms=0"), env...)
	cmd.Stderr = os.Stderr
	conn, err := acp.Start(cmd, client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		<-conn.Done()
	})
	return conn, cmd
}

func TestStartAnAgent(t *testing.T) {
	client := &testClient{}
	conn, cmd := startTestAgent(t, client)
	ctx := ctxFor(t)
	if _, err := conn.Initialize(ctx, &acp.InitializeRequest{ProtocolVersion: acp.LatestProtocolVersion}); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Prompt(ctx, &acp.PromptRequest{SessionID: "s1", Prompt: []acp.ContentBlock{{Text: &acp.TextContent{Text: "hello"}}}}); err != nil {
		t.Fatal(err)
	}
	if got := client.texts(); len(got) != 1 || got[0] != "hello" {
		t.Errorf("updates %v", got)
	}
	if err := conn.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if cmd.ProcessState == nil || !cmd.ProcessState.Success() {
		t.Errorf("the agent ended %v", cmd.ProcessState)
	}
}

// Shutdown gives an agent its whole context to exit, though it closed its
// stdout long before.
func TestShutdownWaitsForTheAgent(t *testing.T) {
	conn, cmd := startTestAgent(t, &testClient{}, "ACPGO_TEST_EXIT_DELAY=1500ms")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := conn.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if !cmd.ProcessState.Success() {
		t.Errorf("the agent ended %v; it had time to exit", cmd.ProcessState)
	}
}

// An agent that exits: what it wrote before is read, the call waiting
// fails, and its exit code is there once Done is closed.
func TestAnAgentThatExits(t *testing.T) {
	client := &testClient{}
	conn, cmd := startTestAgent(t, client)
	err := conn.Call(ctxFor(t), "_test/exit", nil, nil)
	if !errors.Is(err, acp.ErrClosed) {
		t.Errorf("the call: %v", err)
	}
	<-conn.Done()
	if code := cmd.ProcessState.ExitCode(); code != 3 {
		t.Errorf("exit code %d", code)
	}
	if got := client.texts(); len(got) != 1 || got[0] != "bye" {
		t.Errorf("updates %v", got)
	}
}

func TestStartRefusesWhatItCant(t *testing.T) {
	cmd := exec.Command("true")
	cmd.Stdout = os.Stdout
	if _, err := acp.Start(cmd, &testClient{}); err == nil {
		t.Error("Start took a command with its own stdout")
	}
	if _, err := acp.Start(exec.Command("/no/such/agent"), &testClient{}); !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, exec.ErrNotFound) { // Unix, Windows
		t.Errorf("a missing agent: %v", err)
	}
}
