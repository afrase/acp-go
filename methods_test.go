package acp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/afrase/acp-go"
	"github.com/afrase/acp-go/internal/leaktest"
)

// seeds are requests whose zero value has a union with no variant set.
var seeds = map[string]string{
	"SetSessionConfigOptionRequest": `{"sessionId":"s","configId":"c","value":"v"}`,
	"CreateElicitationRequest":      `{"message":"m","mode":"url","elicitationId":"e","url":"https://example.com"}`,
	"SessionNotification":           `{"sessionId":"s","update":{"sessionUpdate":"plan","entries":[]}}`,
}

// Every method of each connection reaches the other side's method of the
// same name: each request is answered "Method not found" by the
// Unimplemented side, which says which method it got.
func TestEveryMethodIsRouted(t *testing.T) {
	agentConn, clientConn := connect(t, struct{ acp.UnimplementedClient }{}, &testAgent{})
	// The test agent answers a few itself; the rest are UnimplementedAgent's.
	answered := map[string]bool{"Initialize": true, "NewSession": true, "Prompt": true}
	ctx := ctxFor(t)
	for _, conn := range []any{agentConn, clientConn} {
		v := reflect.ValueOf(conn)
		for i := range v.NumMethod() {
			m := v.Type().Method(i)
			ft := m.Type
			if ft.NumIn() != 3 || ft.In(1) != reflect.TypeFor[context.Context]() || ft.In(2).Kind() != reflect.Pointer || m.Name == "StartPrompt" {
				continue // Call, Notify, Close and the like
			}
			req := reflect.New(ft.In(2).Elem())
			if seed, ok := seeds[ft.In(2).Elem().Name()]; ok { // a zero union can't be sent
				if err := json.Unmarshal([]byte(seed), req.Interface()); err != nil {
					t.Fatal(err)
				}
			}
			out := v.Method(i).Call([]reflect.Value{reflect.ValueOf(ctx), req})
			errv := out[len(out)-1]
			if ft.NumOut() == 1 { // a notification
				if !errv.IsNil() {
					t.Errorf("%T.%s: %v", conn, m.Name, errv.Interface())
				}
				continue
			}
			if answered[m.Name] && v.Type() == reflect.TypeOf(agentConn) {
				continue
			}
			var e *acp.Error
			err, _ := reflect.TypeAssert[error](errv)
			if !errors.As(err, &e) || e.Code != acp.ErrorCodeMethodNotFound {
				t.Errorf("%T.%s: %v", conn, m.Name, err)
				continue
			}
			var data struct {
				Method string `json:"method"`
			}
			_ = json.Unmarshal(e.Data, &data)
			if !strings.HasPrefix(err.Error(), data.Method+": ") {
				t.Errorf("%T.%s called %q, which reached %q", conn, m.Name, err, data.Method)
			}
		}
	}
}

// Every notification of each connection reaches the other side's method of
// the same name. (One sent under a wrong method would be dropped as
// unknown, so TestEveryMethodIsRouted can't see it.)
func TestEveryNotificationIsRouted(t *testing.T) {
	leaktest.Check(t)
	got := make(chan string, 1)
	toAgentR, toAgentW := io.Pipe()
	toClientR, toClientW := io.Pipe()
	agentConn := acp.NewAgentConn(toClientR, toAgentW, func(*acp.AgentConn) acp.Client { return acp.RecordingClient{Got: got} })
	clientConn := acp.NewClientConn(toAgentR, toClientW, func(*acp.ClientConn) acp.Agent { return acp.RecordingAgent{Got: got} })
	t.Cleanup(func() {
		_ = agentConn.Close()
		_ = clientConn.Close()
	})
	ctx := ctxFor(t)
	sent := 0
	for _, conn := range []any{agentConn, clientConn} {
		v := reflect.ValueOf(conn)
		for i := range v.NumMethod() {
			m := v.Type().Method(i)
			ft := m.Type
			if ft.NumIn() != 3 || ft.NumOut() != 1 || ft.In(1) != reflect.TypeFor[context.Context]() || ft.In(2).Kind() != reflect.Pointer {
				continue // requests, Call, Notify, Close and the like
			}
			n := reflect.New(ft.In(2).Elem())
			if seed, ok := seeds[ft.In(2).Elem().Name()]; ok {
				if err := json.Unmarshal([]byte(seed), n.Interface()); err != nil {
					t.Fatal(err)
				}
			}
			if err, _ := reflect.TypeAssert[error](v.Method(i).Call([]reflect.Value{reflect.ValueOf(ctx), n})[0]); err != nil {
				t.Errorf("%T.%s: %v", conn, m.Name, err)
				continue
			}
			sent++
			select {
			case name := <-got:
				if name != m.Name {
					t.Errorf("%T.%s reached %s", conn, m.Name, name)
				}
			case <-time.After(5 * time.Second):
				t.Errorf("%T.%s reached nothing", conn, m.Name)
			}
		}
	}
	if sent < 3 {
		t.Errorf("only %d notifications sent", sent)
	}
}

// A request without a property its type requires, or with one of the
// wrong type, is answered Invalid params by its id; a handler's nil result
// is answered as an empty one.
func TestTheDispatchChecksParamsAndAnswersANilResult(t *testing.T) {
	r := rawClient(t, &testAgent{prompt: func(context.Context, *testAgent, *acp.PromptRequest) (*acp.PromptResponse, error) {
		return nil, nil //nolint:nilnil // the nil result under test
	}})
	for _, line := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","id":1,"method":"session/new","params":{"mcpServers":[]}}`,
		`{"jsonrpc":"2.0","id":1,"method":"session/new","params":{"cwd":5,"mcpServers":[]}}`,
	} {
		r.send(line)
		if m := r.read(); code(m) != -32602 || m["id"] != float64(1) {
			t.Errorf("%s: answered %v", line, m)
		}
	}
	r.send(`{"jsonrpc":"2.0","id":2,"method":"session/prompt","params":{"sessionId":"s1","prompt":[]}}`)
	if m := r.read(); m["error"] != nil || m["id"] != float64(2) {
		t.Errorf("a nil result: answered %v", m)
	} else if _, ok := m["result"].(map[string]any); !ok {
		t.Errorf("a nil result: answered %v, not an object", m)
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
