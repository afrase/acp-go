package acp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/afrase/acp-go"
	"github.com/afrase/acp-go/internal/leaktest"
)

// testAgent is an agent whose prompt does what the test says.
type testAgent struct {
	acp.UnimplementedAgent
	client *acp.ClientConn
	prompt func(ctx context.Context, a *testAgent, req *acp.PromptRequest) (*acp.PromptResponse, error)
	ext    func(ctx context.Context, method string, params json.RawMessage) (any, error)

	mu        sync.Mutex
	cancelled []acp.SessionID
	notes     []string
}

func (a *testAgent) Initialize(context.Context, *acp.InitializeRequest) (*acp.InitializeResponse, error) {
	return &acp.InitializeResponse{ProtocolVersion: acp.LatestProtocolVersion, AgentInfo: &acp.Implementation{Name: "test", Version: "1"}}, nil
}

func (a *testAgent) NewSession(context.Context, *acp.NewSessionRequest) (*acp.NewSessionResponse, error) {
	return &acp.NewSessionResponse{SessionID: "s1"}, nil
}

func (a *testAgent) Prompt(ctx context.Context, req *acp.PromptRequest) (*acp.PromptResponse, error) {
	if a.prompt == nil {
		return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
	}
	return a.prompt(ctx, a, req)
}

func (a *testAgent) Cancel(_ context.Context, n *acp.CancelNotification) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cancelled = append(a.cancelled, n.SessionID)
	return nil
}

func (a *testAgent) ExtMethod(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if a.ext == nil {
		return a.UnimplementedAgent.ExtMethod(ctx, method, params)
	}
	return a.ext(ctx, method, params)
}

func (a *testAgent) ExtNotification(_ context.Context, method string, _ json.RawMessage) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.notes = append(a.notes, method)
	return nil
}

// say sends a text chunk of the agent's message.
func (a *testAgent) say(ctx context.Context, text string) error {
	return a.client.SessionUpdate(ctx, &acp.SessionNotification{SessionID: "s1", Update: acp.SessionUpdate{
		AgentMessageChunk: &acp.ContentChunk{Content: acp.ContentBlock{Text: &acp.TextContent{Text: text}}},
	}})
}

// testClient records the updates it gets and answers permission requests
// with permission (the first option when nil).
type testClient struct {
	acp.UnimplementedClient
	update     func(ctx context.Context, n *acp.SessionNotification) error
	permission func(ctx context.Context, req *acp.RequestPermissionRequest) (*acp.RequestPermissionResponse, error)

	mu      sync.Mutex
	updates []acp.SessionNotification
}

func (c *testClient) SessionUpdate(ctx context.Context, n *acp.SessionNotification) error {
	c.mu.Lock()
	c.updates = append(c.updates, *n)
	c.mu.Unlock()
	if c.update != nil {
		return c.update(ctx, n)
	}
	return nil
}

func (c *testClient) RequestPermission(ctx context.Context, req *acp.RequestPermissionRequest) (*acp.RequestPermissionResponse, error) {
	if c.permission != nil {
		return c.permission(ctx, req)
	}
	return &acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Selected: &acp.SelectedPermissionOutcome{OptionID: req.Options[0].OptionID}}}, nil
}

// connect joins a client and an agent through pipes, both on this
// package; the test ends with both connections closed and no goroutine
// left.
func connect(t *testing.T, client acp.Client, agent *testAgent, opts ...acp.Option) (*acp.AgentConn, *acp.ClientConn) {
	t.Helper()
	leaktest.Check(t)
	toAgentR, toAgentW := io.Pipe()
	toClientR, toClientW := io.Pipe()
	ac := acp.NewClientConn(toAgentR, toClientW, func(c *acp.ClientConn) acp.Agent {
		agent.client = c
		return agent
	})
	cc := acp.NewAgentConn(toClientR, toAgentW, func(*acp.AgentConn) acp.Client { return client }, opts...)
	t.Cleanup(func() {
		_ = cc.Close()
		_ = ac.Close()
		<-cc.Done()
		<-ac.Done()
	})
	return cc, ac
}

func ctxFor(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// raw is a peer that writes and reads lines itself.
type raw struct {
	t  *testing.T
	w  io.Writer
	sc *bufio.Scanner
}

func (r *raw) send(line string) {
	r.t.Helper()
	if _, err := io.WriteString(r.w, line+"\n"); err != nil {
		r.t.Fatal(err)
	}
}

func (r *raw) read() map[string]any {
	r.t.Helper()
	if !r.sc.Scan() {
		r.t.Fatalf("nothing to read: %v", r.sc.Err())
	}
	var m map[string]any
	if err := json.Unmarshal(r.sc.Bytes(), &m); err != nil {
		r.t.Fatalf("%s: %v", r.sc.Text(), err)
	}
	return m
}

// rawClient serves agent to a raw client.
func rawClient(t *testing.T, agent *testAgent) *raw {
	t.Helper()
	return rawClientWith(t, agent)
}

func rawClientWith(t *testing.T, agent *testAgent, opts ...acp.Option) *raw {
	t.Helper()
	leaktest.Check(t)
	toAgentR, toAgentW := io.Pipe()
	toClientR, toClientW := io.Pipe()
	c := acp.NewClientConn(toAgentR, toClientW, func(c *acp.ClientConn) acp.Agent { agent.client = c; return agent }, opts...)
	t.Cleanup(func() {
		_ = toAgentW.Close()
		go func() { _, _ = io.Copy(io.Discard, toClientR) }()
		<-c.Done()
	})
	sc := bufio.NewScanner(toClientR)
	sc.Buffer(nil, 1<<20)
	return &raw{t: t, w: toAgentW, sc: sc}
}

func code(m map[string]any) float64 {
	e, _ := m["error"].(map[string]any)
	c, _ := e["code"].(float64)
	return c
}
