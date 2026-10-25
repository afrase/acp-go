package steering_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync/atomic"

	"github.com/afrase/acp-go"
	"github.com/afrase/acp-go/steering"
)

// agent steers while busy is set, as if a turn were running.
type agent struct {
	acp.UnimplementedAgent
	busy atomic.Bool
}

func (a *agent) Initialize(context.Context, *acp.InitializeRequest) (*acp.InitializeResponse, error) {
	return &acp.InitializeResponse{ProtocolVersion: acp.LatestProtocolVersion, Meta: steering.Advertise(nil)}, nil
}

func (a *agent) ExtMethod(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if method != steering.Method {
		return a.UnimplementedAgent.ExtMethod(ctx, method, params)
	}
	var req steering.Request[acp.ContentBlock]
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &acp.Error{Code: acp.ErrorCodeInvalidParams, Message: err.Error()}
	}
	switch {
	case a.busy.Load():
		// ... hand req.Prompt to the running turn ...
		return steering.Response{Outcome: steering.Injected}, nil
	case req.PromptRequired():
		return steering.Response{Outcome: steering.PromptRequired, Reason: "noRunningTurn"}, nil
	}
	// ... start a turn with req.Prompt ...
	return steering.Response{Outcome: steering.StartedNewTurn}, nil
}

func Example() {
	ctx := context.Background()
	a := &agent{}
	toAgentR, toAgentW := io.Pipe()
	toClientR, toClientW := io.Pipe()
	agentSide := acp.NewClientConn(toAgentR, toClientW, func(*acp.ClientConn) acp.Agent { return a })
	conn := acp.NewAgentConn(toClientR, toAgentW, func(*acp.AgentConn) acp.Client { return struct{ acp.UnimplementedClient }{} })
	defer func() { _ = conn.Close(); <-agentSide.Done() }()

	init, err := conn.Initialize(ctx, &acp.InitializeRequest{ProtocolVersion: acp.LatestProtocolVersion})
	if err != nil {
		panic(err)
	}
	fmt.Println("steers:", steering.Supported(init.Meta))

	msg := []acp.ContentBlock{{Text: &acp.TextContent{Text: "use tabs, not spaces"}}}
	outcome, err := steering.Steer(ctx, conn, "s1", msg)
	fmt.Println(outcome, err)
	a.busy.Store(true)
	outcome, err = steering.Steer(ctx, conn, "s1", msg)
	fmt.Println(outcome, err)
	// Output:
	// steers: true
	// promptRequired <nil>
	// injected <nil>
}
