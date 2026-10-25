package acp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/afrase/acp-go"
)

// FuzzStream feeds whatever a peer might write to each side of a
// connection: it must neither panic nor hang, and must end when the input
// does.
func FuzzStream(f *testing.F) {
	for _, s := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}` + "\n",
		`{"jsonrpc":"2.0","id":"x","method":"session/prompt","params":{"sessionId":"s","prompt":[{"type":"text","text":"hi"}]}}` + "\n" +
			`{"jsonrpc":"2.0","method":"$/cancel_request","params":{"requestId":"x"}}` + "\n",
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"a"}}}}` + "\n",
		`{"jsonrpc":"2.0","id":2,"method":"session/request_permission","params":{"sessionId":"s","toolCall":{"toolCallId":"t"},"options":[]}}` + "\r\n",
		`{"jsonrpc":"2.0","id":1,"result":{"stopReason":"end_turn"}}` + "\n",
		`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"Method not found"}}` + "\n",
		"not json\n[1,2]\n\"s\"\n{}\n\n{\"id\":null,\"method\":\"_x\"}\n",
		`{"jsonrpc":"2.0","id":3,"method":"elicitation/create","params":{"sessionId":"s","mode":"form","message":"m","requestedSchema":{"type":"object","properties":{"b":{"type":"boolean"},"a":{"type":"string","enum":["x"]}}}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"_vendor/thing","params":[1,{"a":null}]}` + "\n",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(_ *testing.T, data []byte) {
		agentSide := acp.NewClientConn(bytes.NewReader(data), io.Discard, func(c *acp.ClientConn) acp.Agent {
			return &testAgent{client: c, prompt: func(ctx context.Context, a *testAgent, _ *acp.PromptRequest) (*acp.PromptResponse, error) {
				_ = a.say(ctx, "x")
				return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
			}}
		})
		clientSide := acp.NewAgentConn(bytes.NewReader(data), io.Discard, func(*acp.AgentConn) acp.Client { return &testClient{} })
		<-agentSide.Done()
		<-clientSide.Done()
	})
}

// FuzzDecode decodes whatever a peer might send as ACP's main types: no
// panic, and what decodes encodes to something that decodes to the same.
func FuzzDecode(f *testing.F) {
	for _, s := range []string{
		`{"sessionId":"s","update":{"sessionUpdate":"tool_call","toolCallId":"t","title":"x","content":[{"type":"diff","path":"/a","newText":"b","oldText":null}],"locations":[{"path":"/a","line":3}]}}`,
		`{"sessionId":"s","update":{"sessionUpdate":"session_info_update","title":null,"updatedAt":"2026-01-01T00:00:00Z"}}`,
		`{"sessionId":"s","update":{"sessionUpdate":"config_option_update","configOptions":[{"id":"m","name":"Model","type":"select","currentValue":"a","options":[{"group":"g","name":"G","options":[]}]}]}}`,
		`{"sessionId":"s","update":{"sessionUpdate":"future","x":1}}`,
		`{"sessionId":"s","prompt":[{"type":"resource","resource":{"uri":"file:///a","text":"t"}},{"type":"image","data":"AA==","mimeType":"image/png"}]}`,
		`{"sessionId":"s","mode":"form","message":"m","requestedSchema":{"type":"object","properties":{"z":{"type":"array","items":{"anyOf":[{"const":"a","title":"A"}]}}}}}`,
		`{"protocolVersion":1,"agentCapabilities":{"loadSession":true,"sessionCapabilities":{"list":{},"close":null}},"authMethods":[{"id":"a","name":"A"},{"type":"terminal","id":"t","name":"T"}],"_meta":{"steering":{"supported":true}}}`,
		`{"action":"accept","content":{"a":1,"b":[ "x" ],"c":true,"d":1.5,"e":"s"}}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		roundTrip[acp.SessionNotification](t, data)
		roundTrip[acp.PromptRequest](t, data)
		roundTrip[acp.CreateElicitationRequest](t, data)
		roundTrip[acp.CreateElicitationResponse](t, data)
		roundTrip[acp.InitializeResponse](t, data)
		roundTrip[acp.SetSessionConfigOptionRequest](t, data)
		roundTrip[acp.MCPServer](t, data)
	})
}

// roundTrip checks that a T decoded from data, once encoded, decodes and
// encodes again to the same bytes.
func roundTrip[T any](t *testing.T, data []byte) {
	t.Helper()
	var v T
	if json.Unmarshal(data, &v) != nil {
		return
	}
	first, err := json.Marshal(v)
	if err != nil {
		return // a union missing its required variant decodes, and can't be sent on
	}
	var again T
	if err := json.Unmarshal(first, &again); err != nil {
		t.Fatalf("%T: %s encoded as %s, which doesn't decode: %v", v, data, first, err)
	}
	second, err := json.Marshal(again)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("%T: %s encoded as\n%s, then as\n%s (%v)", v, data, first, second, err)
	}
}
