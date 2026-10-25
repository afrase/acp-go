//go:build interop

package interop_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/afrase/acp-go"
)

// The examples' commit: the typescript-sdk tag of the SDK's version
// (v2.0.2).
const examplesCommit = "f2f218d314ee8f42c37c466b55d2b917ddc0006c"

// work is the temporary folder with the SDK, its examples and the Go
// example agent.
var work string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "acp-go-interop-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	work = dir
	err = setUp()
	code := 1
	if err == nil {
		code = m.Run()
	} else {
		fmt.Fprintln(os.Stderr, "interop: setting up:", err)
	}
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func setUp() error {
	for _, f := range []string{"package.json", "package-lock.json"} {
		b, err := os.ReadFile(filepath.Join("testdata", f))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(work, f), b, 0o600); err != nil {
			return err
		}
	}
	npm := exec.Command("npm", "ci", "--ignore-scripts", "--no-audit", "--no-fund", "--cache", filepath.Join(work, ".npm"))
	npm.Dir = work
	npm.Env = append(os.Environ(), "npm_config_update_notifier=false")
	if out, err := npm.CombinedOutput(); err != nil {
		return fmt.Errorf("npm ci: %w\n%s", err, out)
	}
	sums, err := os.ReadFile(filepath.Join("testdata", "SHA256SUMS"))
	if err != nil {
		return err
	}
	for line := range strings.Lines(string(sums)) {
		sum, name, ok := strings.Cut(strings.TrimSpace(line), "  ")
		if !ok {
			continue
		}
		src, err := fetch("https://raw.githubusercontent.com/agentclientprotocol/typescript-sdk/" + examplesCommit + "/src/examples/" + name)
		if err != nil {
			return err
		}
		if got := sha256.Sum256(src); hex.EncodeToString(got[:]) != sum {
			return fmt.Errorf("%s isn't the file pinned", name)
		}
		src = bytes.ReplaceAll(src, []byte(`"../v1/stable/acp.js"`), []byte(`"@agentclientprotocol/sdk"`))
		// The example client runs the example agent with npx tsx; here it
		// runs the agent ACP_AGENT names.
		src = bytes.ReplaceAll(src, []byte(`spawn(npxCmd, ["tsx", agentPath],`), []byte(`spawn(process.env.ACP_AGENT, [],`))
		if err := os.WriteFile(filepath.Join(work, name), src, 0o600); err != nil {
			return err
		}
	}
	build := exec.Command("go", "build", "-o", filepath.Join(work, "echo"), "../../examples/echo")
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("building the echo agent: %w\n%s", err, out)
	}
	return nil
}

func fetch(url string) ([]byte, error) {
	resp, err := http.Get(url) //nolint:gosec // a test downloads a pinned file, checked after
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck // read-only
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// client records what the TypeScript agent sends and answers its
// permission request with the option it is told to.
type client struct {
	acp.UnimplementedClient
	option string

	mu      sync.Mutex
	updates []acp.SessionUpdate
	asked   []string
}

func (c *client) SessionUpdate(_ context.Context, n *acp.SessionNotification) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.updates = append(c.updates, n.Update)
	return nil
}

func (c *client) RequestPermission(_ context.Context, req *acp.RequestPermissionRequest) (*acp.RequestPermissionResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, o := range req.Options {
		c.asked = append(c.asked, string(o.OptionID))
	}
	return &acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Selected: &acp.SelectedPermissionOutcome{OptionID: acp.PermissionOptionID(c.option)}}}, nil
}

// summary is the updates as "kind detail" lines.
func (c *client) summary() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, u := range c.updates {
		switch {
		case u.AgentMessageChunk != nil:
			out = append(out, "text "+u.AgentMessageChunk.Content.Text.Text)
		case u.ToolCall != nil:
			out = append(out, fmt.Sprintf("tool %s %s %s %s", u.ToolCall.ToolCallID, u.ToolCall.Kind, u.ToolCall.Status, u.ToolCall.Locations[0].Path))
		case u.ToolCallUpdate != nil:
			out = append(out, fmt.Sprintf("update %s %s", u.ToolCallUpdate.ToolCallID, u.ToolCallUpdate.Status))
		default:
			out = append(out, u.SessionUpdate)
		}
	}
	return out
}

func startTSAgent(t *testing.T, c *client) *acp.AgentConn {
	t.Helper()
	cmd := exec.Command("node", "agent.ts")
	cmd.Dir = work
	cmd.Stderr = os.Stderr
	conn, err := acp.Start(cmd, func(*acp.AgentConn) acp.Client { return c })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Shutdown(ctx)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	init, err := conn.Initialize(ctx, &acp.InitializeRequest{ProtocolVersion: acp.LatestProtocolVersion, ClientInfo: &acp.Implementation{Name: "acp-go", Version: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	if init.ProtocolVersion != 1 || init.AgentCapabilities == nil || init.AgentCapabilities.LoadSession {
		t.Errorf("initialize: %+v", init)
	}
	return conn
}

func turn(t *testing.T, conn *acp.AgentConn) (acp.SessionID, func() (*acp.PromptResponse, error)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	sess, err := conn.NewSession(ctx, &acp.NewSessionRequest{Cwd: work})
	if err != nil {
		t.Fatal(err)
	}
	wait, err := conn.StartPrompt(ctx, &acp.PromptRequest{SessionID: sess.SessionID, Prompt: []acp.ContentBlock{{Text: &acp.TextContent{Text: "Hello, agent!"}}}})
	if err != nil {
		t.Fatal(err)
	}
	return sess.SessionID, func() (*acp.PromptResponse, error) { return wait(ctx) }
}

// A Go client and the TypeScript example agent: a turn with a read, then
// an edit it asks permission for, allowed or rejected.
func TestGoClientTSAgent(t *testing.T) {
	for _, option := range []string{"allow", "reject"} {
		t.Run(option, func(t *testing.T) {
			c := &client{option: option}
			conn := startTSAgent(t, c)
			_, wait := turn(t, conn)
			resp, err := wait()
			if err != nil || resp.StopReason != acp.StopReasonEndTurn {
				t.Fatalf("the turn: %+v, %v", resp, err)
			}
			last := " Perfect! I've successfully updated the configuration. The changes have been applied."
			if option == "reject" {
				last = " I understand you prefer not to make that change. I'll skip the configuration update."
			}
			want := []string{
				"text I'll help you with that. Let me start by reading some files to understand the current situation.",
				"tool call_1 read pending /project/README.md",
				"update call_1 completed",
				"text  Now I understand the project structure. I need to make some changes to improve it.",
				"tool call_2 edit pending /project/config.json",
			}
			if option == "allow" {
				want = append(want, "update call_2 completed")
			}
			want = append(want, "text "+last)
			if got := c.summary(); strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("updates:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
			if strings.Join(c.asked, ",") != "allow,reject" {
				t.Errorf("permission options %v", c.asked)
			}
		})
	}
}

// Cancelling the TypeScript agent's turn ends it "cancelled".
func TestGoClientCancelsTSAgent(t *testing.T) {
	c := &client{option: "allow"}
	conn := startTSAgent(t, c)
	sid, wait := turn(t, conn)
	for deadline := time.Now().Add(10 * time.Second); len(c.summary()) == 0; {
		if time.Now().After(deadline) {
			t.Fatal("no update")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := conn.Cancel(context.Background(), &acp.CancelNotification{SessionID: sid}); err != nil {
		t.Fatal(err)
	}
	resp, err := wait()
	if err != nil || resp.StopReason != acp.StopReasonCancelled {
		t.Errorf("the cancelled turn: %+v, %v", resp, err)
	}
}

// A call the Go client withdraws ($/cancel_request) leaves the TypeScript
// agent working and the connection usable.
func TestGoClientWithdrawsFromTSAgent(t *testing.T) {
	conn := startTSAgent(t, &client{option: "allow"})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sess, err := conn.NewSession(ctx, &acp.NewSessionRequest{Cwd: work})
	if err != nil {
		t.Fatal(err)
	}
	short, stop := context.WithTimeout(ctx, 200*time.Millisecond)
	defer stop()
	if _, err := conn.Prompt(short, &acp.PromptRequest{SessionID: sess.SessionID, Prompt: []acp.ContentBlock{{Text: &acp.TextContent{Text: "hi"}}}}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the withdrawn prompt: %v", err)
	}
	if _, err := conn.NewSession(ctx, &acp.NewSessionRequest{Cwd: work}); err != nil {
		t.Errorf("after the withdrawal: %v", err)
	}
}

// The TypeScript example client and the Go example agent.
func TestTSClientGoAgent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "client.ts")
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "ACP_AGENT="+filepath.Join(work, "echo"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"Connected to agent (protocol v1)", "Created session:", "Hello, \nagent! \n", "Agent completed with: end_turn"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("the client printed:\n%s\nwithout %q", out, want)
		}
	}
}
