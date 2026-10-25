package unstable_test

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	acp "github.com/afrase/acp-go/unstable"
)

// forkingAgent forks sessions and reports a turn's usage.
type forkingAgent struct {
	acp.UnimplementedAgent
}

func (forkingAgent) Initialize(context.Context, *acp.InitializeRequest) (*acp.InitializeResponse, error) {
	return &acp.InitializeResponse{ProtocolVersion: acp.LatestProtocolVersion, AgentCapabilities: &acp.AgentCapabilities{
		SessionCapabilities: &acp.SessionCapabilities{Fork: &acp.SessionForkCapabilities{}},
	}}, nil
}

func (forkingAgent) ForkSession(_ context.Context, req *acp.ForkSessionRequest) (*acp.ForkSessionResponse, error) {
	return &acp.ForkSessionResponse{SessionID: req.SessionID + "-fork"}, nil
}

func (forkingAgent) Prompt(context.Context, *acp.PromptRequest) (*acp.PromptResponse, error) {
	cached := uint64(7)
	return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn, Usage: &acp.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 22, CachedReadTokens: &cached}}, nil
}

func TestForkAndUsage(t *testing.T) {
	toAgentR, toAgentW := io.Pipe()
	toClientR, toClientW := io.Pipe()
	agentSide := acp.NewClientConn(toAgentR, toClientW, func(*acp.ClientConn) acp.Agent { return forkingAgent{} })
	conn := acp.NewAgentConn(toClientR, toAgentW, struct{ acp.UnimplementedClient }{})
	t.Cleanup(func() {
		_ = conn.Close()
		<-conn.Done()
		<-agentSide.Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	init, err := conn.Initialize(ctx, &acp.InitializeRequest{ProtocolVersion: acp.LatestProtocolVersion})
	if err != nil || init.AgentCapabilities.SessionCapabilities.Fork == nil {
		t.Fatalf("Initialize = %+v, %v", init, err)
	}
	fork, err := conn.ForkSession(ctx, &acp.ForkSessionRequest{SessionID: "s1", Cwd: "/tmp"})
	if err != nil || fork.SessionID != "s1-fork" {
		t.Fatalf("ForkSession = %+v, %v", fork, err)
	}
	resp, err := conn.Prompt(ctx, &acp.PromptRequest{SessionID: fork.SessionID})
	if err != nil || resp.Usage == nil || resp.Usage.InputTokens != 10 || *resp.Usage.CachedReadTokens != 7 {
		t.Fatalf("Prompt = %+v, %v", resp, err)
	}
}

func TestUnstableUpdates(t *testing.T) {
	for _, in := range []string{
		`{"sessionUpdate":"plan_update","plan":{"type":"markdown","planId":"p","content":"- a"}}`,
		`{"sessionUpdate":"plan_removed","planId":"p"}`,
		`{"sessionUpdate":"subagent_update","sessionId":"child","title":null,"state":{"state":"idle","stopReason":"end_turn"}}`,
	} {
		var u acp.SessionUpdate
		if err := json.Unmarshal([]byte(in), &u); err != nil || u.Other != nil {
			t.Fatalf("%s: %+v, %v", in, u, err)
		}
		out, err := json.Marshal(u)
		if err != nil || string(out) != in {
			t.Errorf("round trip: %s, %v; want %s", out, err, in)
		}
	}
}
