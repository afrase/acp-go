package acp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
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

func (c *testClient) texts() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, u := range c.updates {
		if ch := u.Update.AgentMessageChunk; ch != nil && ch.Content.Text != nil {
			out = append(out, ch.Content.Text.Text)
		}
	}
	return out
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
	cc := acp.NewAgentConn(toClientR, toAgentW, client, opts...)
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

func TestATurnsUpdatesArriveBeforeItsResult(t *testing.T) {
	big := strings.Repeat("x", 1<<20)
	client := &testClient{}
	agent := &testAgent{prompt: func(ctx context.Context, a *testAgent, _ *acp.PromptRequest) (*acp.PromptResponse, error) {
		for _, s := range []string{"one", big, "three"} {
			if err := a.say(ctx, s); err != nil {
				return nil, err
			}
		}
		return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
	}}
	agentConn, _ := connect(t, client, agent)
	ctx := ctxFor(t)
	init, err := agentConn.Initialize(ctx, &acp.InitializeRequest{ProtocolVersion: acp.LatestProtocolVersion})
	if err != nil || init.AgentInfo.Name != "test" {
		t.Fatalf("Initialize = %+v, %v", init, err)
	}
	sess, err := agentConn.NewSession(ctx, &acp.NewSessionRequest{Cwd: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := agentConn.Prompt(ctx, &acp.PromptRequest{SessionID: sess.SessionID, Prompt: []acp.ContentBlock{{Text: &acp.TextContent{Text: "hi"}}}})
	if err != nil || resp.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("Prompt = %+v, %v", resp, err)
	}
	if got := client.texts(); len(got) != 3 || got[0] != "one" || got[1] != big || got[2] != "three" {
		t.Errorf("got %d updates before the result", len(got))
	}
}

// Messages have no size cap by default: the old SDKs' 10 MiB one cut
// session replays with images short.
func TestNoSizeCapByDefault(t *testing.T) {
	huge := strings.Repeat("y", 16<<20)
	client := &testClient{}
	agent := &testAgent{prompt: func(ctx context.Context, a *testAgent, req *acp.PromptRequest) (*acp.PromptResponse, error) {
		return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, a.say(ctx, req.Prompt[0].Text.Text)
	}}
	agentConn, _ := connect(t, client, agent)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute) // slow under -race
	defer cancel()
	if _, err := agentConn.Prompt(ctx, &acp.PromptRequest{SessionID: "s1", Prompt: []acp.ContentBlock{{Text: &acp.TextContent{Text: huge}}}}); err != nil {
		t.Fatal(err)
	}
	if got := client.texts(); len(got) != 1 || len(got[0]) != len(huge) {
		t.Error("the echo lost its size")
	}
}

func TestLimits(t *testing.T) {
	agent := &testAgent{prompt: func(ctx context.Context, a *testAgent, _ *acp.PromptRequest) (*acp.PromptResponse, error) {
		return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, a.say(ctx, strings.Repeat("z", 5000))
	}}
	agentConn, _ := connect(t, &testClient{}, agent, acp.WithWriteLimit(1000), acp.WithReadLimit(2000))
	ctx := ctxFor(t)
	_, err := agentConn.Prompt(ctx, &acp.PromptRequest{SessionID: "s1", Prompt: []acp.ContentBlock{{Text: &acp.TextContent{Text: strings.Repeat("a", 2000)}}}})
	if !errors.Is(err, acp.ErrTooLarge) {
		t.Fatalf("a prompt over the write limit: %v", err)
	}
	if _, err := agentConn.NewSession(ctx, &acp.NewSessionRequest{Cwd: "/"}); err != nil {
		t.Fatalf("the connection didn't go on: %v", err)
	}
	// The agent's update is over the read limit: the connection ends.
	_, err = agentConn.Prompt(ctx, &acp.PromptRequest{SessionID: "s1", Prompt: []acp.ContentBlock{{Text: &acp.TextContent{Text: "go"}}}})
	<-agentConn.Done()
	if !errors.Is(err, acp.ErrClosed) || !errors.Is(agentConn.Err(), acp.ErrTooLarge) {
		t.Errorf("a message over the read limit: %v; the connection ended with %v", err, agentConn.Err())
	}
}

// The agent asks the client a question while the client calls the agent:
// requests are each handled on their own goroutine, so neither waits for
// the other.
func TestRequestsRunAtOnce(t *testing.T) {
	asked := make(chan struct{})
	release := make(chan struct{})
	client := &testClient{permission: func(context.Context, *acp.RequestPermissionRequest) (*acp.RequestPermissionResponse, error) {
		close(asked)
		<-release
		return &acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: true}}, nil
	}}
	agent := &testAgent{
		prompt: func(ctx context.Context, a *testAgent, _ *acp.PromptRequest) (*acp.PromptResponse, error) {
			resp, err := a.client.RequestPermission(ctx, &acp.RequestPermissionRequest{SessionID: "s1", ToolCall: acp.ToolCallUpdate{ToolCallID: "t1"},
				Options: []acp.PermissionOption{{OptionID: "ok", Name: "OK", Kind: acp.PermissionOptionKindAllowOnce}}})
			if err != nil {
				return nil, err
			}
			if !resp.Outcome.Cancelled {
				t.Errorf("outcome %+v", resp.Outcome)
			}
			return &acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
		},
		ext: func(context.Context, string, json.RawMessage) (any, error) { return map[string]int{"n": 1}, nil },
	}
	agentConn, _ := connect(t, client, agent)
	ctx := ctxFor(t)
	done := make(chan error, 1)
	go func() {
		_, err := agentConn.Prompt(ctx, &acp.PromptRequest{SessionID: "s1"})
		done <- err
	}()
	<-asked
	var out struct{ N int }
	if err := agentConn.Call(ctx, "_test/count", map[string]string{"a": "b"}, &out); err != nil || out.N != 1 {
		t.Fatalf("a call while a question waits: %+v, %v", out, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// A call whose context ends withdraws its request: the handler's context
// on the other side ends too.
func TestAWithdrawnCallEndsItsHandler(t *testing.T) {
	ended := make(chan error, 1)
	agent := &testAgent{prompt: func(ctx context.Context, _ *testAgent, _ *acp.PromptRequest) (*acp.PromptResponse, error) {
		<-ctx.Done()
		ended <- ctx.Err()
		return nil, ctx.Err()
	}}
	agentConn, _ := connect(t, &testClient{}, agent)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := agentConn.Prompt(ctx, &acp.PromptRequest{SessionID: "s1"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	select {
	case err := <-ended:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the handler's context ended with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the agent still works on a withdrawn prompt")
	}
	if _, err := agentConn.NewSession(ctxFor(t), &acp.NewSessionRequest{Cwd: "/"}); err != nil {
		t.Errorf("after a withdrawal: %v", err)
	}
}

// The agent stops waiting for an answer (another one came): the client's
// question goes away.
func TestTheAgentWithdrawsAQuestion(t *testing.T) {
	gone := make(chan struct{})
	client := &testClient{permission: func(ctx context.Context, _ *acp.RequestPermissionRequest) (*acp.RequestPermissionResponse, error) {
		<-ctx.Done()
		close(gone)
		return nil, ctx.Err()
	}}
	agent := &testAgent{prompt: func(ctx context.Context, a *testAgent, _ *acp.PromptRequest) (*acp.PromptResponse, error) {
		qctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		_, err := a.client.RequestPermission(qctx, &acp.RequestPermissionRequest{SessionID: "s1", ToolCall: acp.ToolCallUpdate{ToolCallID: "t"}})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("the question: %v", err)
		}
		return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
	}}
	agentConn, _ := connect(t, client, agent)
	if _, err := agentConn.Prompt(ctxFor(t), &acp.PromptRequest{SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gone:
	case <-time.After(5 * time.Second):
		t.Fatal("the client still asks")
	}
}

func TestStartPromptThenCancel(t *testing.T) {
	agent := &testAgent{prompt: func(ctx context.Context, a *testAgent, _ *acp.PromptRequest) (*acp.PromptResponse, error) {
		for {
			a.mu.Lock()
			n := len(a.cancelled)
			a.mu.Unlock()
			if n > 0 {
				return &acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Millisecond):
			}
		}
	}}
	agentConn, _ := connect(t, &testClient{}, agent)
	ctx := ctxFor(t)
	wait, err := agentConn.StartPrompt(ctx, &acp.PromptRequest{SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := agentConn.Cancel(ctx, &acp.CancelNotification{SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	resp, err := wait(ctx)
	if err != nil || resp.StopReason != acp.StopReasonCancelled {
		t.Errorf("the turn ended %+v, %v", resp, err)
	}
}

func TestAPanicIsAnInternalError(t *testing.T) {
	agent := &testAgent{prompt: func(context.Context, *testAgent, *acp.PromptRequest) (*acp.PromptResponse, error) {
		panic("boom")
	}}
	agentConn, _ := connect(t, &testClient{}, agent)
	ctx := ctxFor(t)
	_, err := agentConn.Prompt(ctx, &acp.PromptRequest{SessionID: "s1"})
	var e *acp.Error
	if !errors.As(err, &e) || e.Code != acp.ErrorCodeInternalError || !strings.Contains(e.Message, "boom") {
		t.Fatalf("err = %v", err)
	}
	if _, err := agentConn.NewSession(ctx, &acp.NewSessionRequest{Cwd: "/"}); err != nil {
		t.Errorf("after the panic: %v", err)
	}
}

func TestMethodsOutsideACP(t *testing.T) {
	agent := &testAgent{ext: func(_ context.Context, method string, params json.RawMessage) (any, error) {
		return map[string]string{"method": method, "params": string(params)}, nil
	}}
	agentConn, _ := connect(t, &testClient{}, agent)
	ctx := ctxFor(t)
	var got map[string]string
	if err := agentConn.Call(ctx, "_zed.dev/workspace/buffers", map[string]string{"language": "go"}, &got); err != nil {
		t.Fatal(err)
	}
	if got["method"] != "_zed.dev/workspace/buffers" || got["params"] != `{"language":"go"}` {
		t.Errorf("got %v", got)
	}
	var e *acp.Error
	if err := agentConn.Call(ctx, "no/such", nil, nil); !errors.As(err, &e) || e.Code != acp.ErrorCodeMethodNotFound {
		t.Errorf("an unknown method: %v", err)
	}
	if _, err := agentConn.LoadSession(ctx, &acp.LoadSessionRequest{SessionID: "s", Cwd: "/"}); !errors.As(err, &e) || e.Code != acp.ErrorCodeMethodNotFound {
		t.Errorf("a method the agent left to UnimplementedAgent: %v", err)
	}
	if err := agentConn.Notify(ctx, "_zed.dev/file_opened", map[string]string{"path": "/a"}); err != nil {
		t.Fatal(err)
	}
	if err := agentConn.Notify(ctx, "no/such/notification", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := agentConn.NewSession(ctx, &acp.NewSessionRequest{Cwd: "/"}); err != nil { // after the notifications
		t.Fatal(err)
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if len(agent.notes) != 1 || agent.notes[0] != "_zed.dev/file_opened" {
		t.Errorf("notifications %v", agent.notes)
	}
}

func TestACallFromANotificationHandlerFailsAtOnce(t *testing.T) {
	var agentConn *acp.AgentConn
	failed := make(chan error, 1)
	client := &testClient{update: func(ctx context.Context, _ *acp.SessionNotification) error {
		_, err := agentConn.NewSession(ctx, &acp.NewSessionRequest{Cwd: "/"})
		failed <- err
		return nil
	}}
	agent := &testAgent{prompt: func(ctx context.Context, a *testAgent, _ *acp.PromptRequest) (*acp.PromptResponse, error) {
		return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, a.say(ctx, "hi")
	}}
	agentConn, _ = connect(t, client, agent)
	if _, err := agentConn.Prompt(ctxFor(t), &acp.PromptRequest{SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	if err := <-failed; err == nil || !strings.Contains(err.Error(), "notification handler") {
		t.Errorf("the call: %v", err)
	}
}

// Nothing is queued: while the client's handler is stuck, the agent's
// next update waits to be written, until its context ends.
func TestASlowClientHoldsTheAgentBack(t *testing.T) {
	first := true
	client := &testClient{update: func(ctx context.Context, _ *acp.SessionNotification) error {
		if first {
			first = false
			<-ctx.Done() // stuck until the connection ends
		}
		return nil
	}}
	held := make(chan error, 1)
	agent := &testAgent{prompt: func(ctx context.Context, a *testAgent, _ *acp.PromptRequest) (*acp.PromptResponse, error) {
		if err := a.say(ctx, "first"); err != nil {
			return nil, err
		}
		wctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()
		held <- a.say(wctx, "second")
		return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
	}}
	agentConn, _ := connect(t, client, agent)
	go func() { _, _ = agentConn.Prompt(context.Background(), &acp.PromptRequest{SessionID: "s1"}) }()
	select {
	case err := <-held:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("the second update: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the update's context didn't end its wait")
	}
}

func TestOrderAndCountUnderLoad(t *testing.T) {
	const n = 20000
	client := &testClient{}
	agent := &testAgent{prompt: func(ctx context.Context, a *testAgent, _ *acp.PromptRequest) (*acp.PromptResponse, error) {
		for i := range n {
			if err := a.say(ctx, string(rune('a'+i%26))); err != nil {
				return nil, err
			}
		}
		return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
	}}
	agentConn, _ := connect(t, client, agent)
	if _, err := agentConn.Prompt(ctxFor(t), &acp.PromptRequest{SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	got := client.texts()
	if len(got) != n {
		t.Fatalf("got %d updates of %d", len(got), n)
	}
	for i, s := range got {
		if s != string(rune('a'+i%26)) {
			t.Fatalf("update %d is %q", i, s)
		}
	}
}

func TestCloseEndsWaitingCalls(t *testing.T) {
	started := make(chan struct{})
	agent := &testAgent{prompt: func(ctx context.Context, _ *testAgent, _ *acp.PromptRequest) (*acp.PromptResponse, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	agentConn, agentSide := connect(t, &testClient{}, agent)
	done := make(chan error, 1)
	go func() {
		_, err := agentConn.Prompt(context.Background(), &acp.PromptRequest{SessionID: "s1"})
		done <- err
	}()
	<-started
	if err := agentConn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, acp.ErrClosed) {
		t.Errorf("the waiting call: %v", err)
	}
	<-agentConn.Done()
	<-agentSide.Done() // the agent saw its input end
	if !errors.Is(agentSide.Err(), io.EOF) {
		t.Errorf("the agent's side ended with %v", agentSide.Err())
	}
	if _, err := agentConn.NewSession(context.Background(), &acp.NewSessionRequest{}); !errors.Is(err, acp.ErrClosed) {
		t.Errorf("a call after Close: %v", err)
	}
}

func TestShutdownLetsTheAgentEnd(t *testing.T) {
	agentConn, agentSide := connect(t, &testClient{}, &testAgent{})
	if err := agentConn.Shutdown(ctxFor(t)); err != nil {
		t.Fatal(err)
	}
	<-agentSide.Done()
}

func TestAHandlerKnowsItsRequestID(t *testing.T) {
	ids := make(chan string, 1)
	agent := &testAgent{ext: func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
		id, ok := acp.RequestIDFromContext(ctx)
		if !ok {
			t.Error("no request id")
		}
		ids <- string(id)
		return nil, nil //nolint:nilnil // a result of null
	}}
	agentConn, _ := connect(t, &testClient{}, agent)
	if err := agentConn.Call(ctxFor(t), "_x", nil, nil); err != nil {
		t.Fatal(err)
	}
	if id := <-ids; id != "1" {
		t.Errorf("id %s", id)
	}
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

func TestBadInputIsAnsweredAsJSONRPCSays(t *testing.T) {
	r := rawClient(t, &testAgent{})
	for _, tc := range []struct {
		line string
		code float64
	}{
		{`this isn't JSON`, -32700},
		{`[{"jsonrpc":"2.0","id":1,"method":"initialize"}]`, -32600},
		{`"a string"`, -32600},
		{`{"jsonrpc":"2.0"}`, -32600},
		{`{"jsonrpc":"2.0","id":{"a":1},"method":"initialize"}`, -32600},
	} {
		r.send(tc.line)
		if m := r.read(); code(m) != tc.code || m["id"] != nil {
			t.Errorf("%s: answered %v", tc.line, m)
		}
	}
	r.send(`{"jsonrpc":"2.0","id":99,"result":{}}`) // a response to nothing: ignored
	r.send(`{"jsonrpc":"2.0","id":"a","method":"session/new","params":{"cwd":5}}`)
	if m := r.read(); code(m) != -32602 || m["id"] != "a" {
		t.Errorf("bad params: %v", m)
	}
	r.send(`{"jsonrpc":"2.0","id":"b","method":"session/new","params":{"mcpServers":[]}}`)
	if m := r.read(); code(m) != -32602 || m["id"] != "b" {
		t.Errorf("no cwd: %v", m)
	}
	r.send(`{"jsonrpc":"2.0","id":7,"method":"session/new","params":{"cwd":"/","mcpServers":[]}}`)
	if m := r.read(); m["id"] != float64(7) || m["result"].(map[string]any)["sessionId"] != "s1" {
		t.Errorf("a good request after the bad ones: %v", m)
	}
}

func TestAWithdrawnRequestIsAnsweredRequestCancelled(t *testing.T) {
	agent := &testAgent{prompt: func(ctx context.Context, _ *testAgent, _ *acp.PromptRequest) (*acp.PromptResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	r := rawClient(t, agent)
	r.send(`{"jsonrpc":"2.0","id":"p","method":"session/prompt","params":{"sessionId":"s1","prompt":[]}}`)
	r.send(`{"jsonrpc":"2.0","method":"$/cancel_request","params":{"requestId":"p"}}`)
	if m := r.read(); code(m) != -32800 || m["id"] != "p" {
		t.Errorf("answered %v", m)
	}
}

// Text goes on the wire as it is: no < for a "<".
func TestTextGoesAsItIs(t *testing.T) {
	agent := &testAgent{prompt: func(ctx context.Context, a *testAgent, _ *acp.PromptRequest) (*acp.PromptResponse, error) {
		return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, a.say(ctx, "<b>&</b>")
	}}
	r := rawClient(t, agent)
	r.send(`{"jsonrpc":"2.0","id":1,"method":"session/prompt","params":{"sessionId":"s1","prompt":[]}}`)
	if !r.sc.Scan() || !strings.Contains(r.sc.Text(), `"text":"<b>&</b>"`) {
		t.Errorf("sent %s", r.sc.Text())
	}
	r.read() // the result
}
