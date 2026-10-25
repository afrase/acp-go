package acp_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/afrase/acp-go"
)

func encode(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encoding %T: %v", v, err)
	}
	return string(b)
}

func decode[T any](t *testing.T, s string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("decoding %s: %v", s, err)
	}
	return v
}

func TestTaggedUnionRoundTrips(t *testing.T) {
	for _, in := range []string{
		`{"type":"text","text":"hi"}`,
		`{"type":"image","data":"AAAA","mimeType":"image/png"}`,
		`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello"}}`,
		`{"sessionUpdate":"tool_call","toolCallId":"t1","title":"Read a file","kind":"read","status":"pending"}`,
		`{"outcome":"cancelled"}`,
		`{"outcome":"selected","optionId":"allow"}`,
		`{"name":"fs","command":"/bin/fs","args":[],"env":[]}`, // stdio: the MCP server without a type
		`{"type":"http","name":"web","url":"https://example.com","headers":[]}`,
	} {
		var v any
		switch {
		case strings.Contains(in, `"sessionUpdate"`):
			v = decode[acp.SessionUpdate](t, in)
		case strings.Contains(in, `"outcome"`):
			v = decode[acp.RequestPermissionOutcome](t, in)
		case strings.Contains(in, `"name"`):
			v = decode[acp.MCPServer](t, in)
		default:
			v = decode[acp.ContentBlock](t, in)
		}
		if out := encode(t, v); out != in {
			t.Errorf("round trip:\n got %s\nwant %s", out, in)
		}
	}
}

func TestTheTagIsFilledInFromTheVariant(t *testing.T) {
	got := encode(t, acp.SessionUpdate{AgentMessageChunk: &acp.ContentChunk{Content: acp.ContentBlock{Text: &acp.TextContent{Text: "hi"}}}})
	if want := `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hi"}}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if got := encode(t, acp.RequestPermissionOutcome{Cancelled: true}); got != `{"outcome":"cancelled"}` {
		t.Errorf("a variant without fields: %s", got)
	}
	for _, bad := range []any{
		acp.ContentBlock{},
		acp.ContentBlock{Text: &acp.TextContent{}, Image: &acp.ImageContent{}},
		acp.ContentBlock{Type: "text"},
	} {
		if _, err := json.Marshal(bad); err == nil {
			t.Errorf("%+v encoded", bad)
		}
	}
}

// A variant this package doesn't know (a newer agent's) is kept whole and
// sent on as it came.
func TestAnUnknownVariantIsKept(t *testing.T) {
	in := `{"sessionUpdate":"hologram","beam":{"x":1},"_meta":{"k":"v"}}`
	u := decode[acp.SessionUpdate](t, in)
	if u.SessionUpdate != "hologram" || string(u.Other) != in {
		t.Fatalf("decoded %+v", u)
	}
	if out := encode(t, u); out != in {
		t.Errorf("sent on as %s", out)
	}
}

func TestAnUnknownConfigTypeWithAStringIsAValueID(t *testing.T) {
	r := decode[acp.SetSessionConfigOptionRequest](t, `{"sessionId":"s","configId":"model","type":"future","value":"opus"}`)
	if r.ValueID == nil || r.ValueID.Value != "opus" {
		t.Errorf("decoded %+v", r)
	}
	r = decode[acp.SetSessionConfigOptionRequest](t, `{"sessionId":"s","configId":"x","type":"future","value":{"a":1}}`)
	if r.ValueID != nil || r.Other == nil {
		t.Errorf("an unknown type with an object: %+v", r)
	}
	r = decode[acp.SetSessionConfigOptionRequest](t, `{"sessionId":"s","configId":"x","type":"boolean","value":true}`)
	if r.Boolean == nil || !r.Boolean.Value {
		t.Errorf("a boolean: %+v", r)
	}
	if got := encode(t, acp.SetSessionConfigOptionRequest{SessionID: "s", ConfigID: "m", ValueID: &acp.SetSessionConfigOptionRequestValueID{Value: "v"}}); got != `{"sessionId":"s","configId":"m","value":"v"}` {
		t.Errorf("a value id: %s", got)
	}
}

// What the schema marks x-deserialize-default-on-error decodes as absent
// when it is bad, rather than failing the whole message.
func TestABadOptionalFieldIsDropped(t *testing.T) {
	n := decode[acp.SessionNotification](t, `{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","messageId":7,"content":{"type":"text","text":"hi","annotations":"loud"},"_meta":[1]}}`)
	c := n.Update.AgentMessageChunk
	if c == nil || c.Content.Text == nil || c.Content.Text.Text != "hi" || c.MessageID != "" || c.Meta != nil || c.Content.Text.Annotations != nil {
		t.Fatalf("decoded %+v", c)
	}
	var bad acp.SessionNotification
	if err := json.Unmarshal([]byte(`{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","content":"hi"}}`), &bad); err == nil {
		t.Error("a bad required field decoded")
	}
}

func TestBadItemsAreSkipped(t *testing.T) {
	r := decode[acp.ListSessionsResponse](t, `{"sessions":[{"sessionId":"a","cwd":"/a"},{"sessionId":5},{"sessionId":"b","cwd":"/b"}]}`)
	if len(r.Sessions) != 2 || r.Sessions[0].SessionID != "a" || r.Sessions[1].SessionID != "b" {
		t.Errorf("sessions %+v", r.Sessions)
	}
}

func TestARequiredListIsNeverNull(t *testing.T) {
	if got := encode(t, acp.NewSessionRequest{Cwd: "/tmp"}); got != `{"cwd":"/tmp","mcpServers":[]}` {
		t.Errorf("got %s", got)
	}
}

func TestNullableTellsAbsentNullAndValueApart(t *testing.T) {
	title := "Fix the bug"
	for _, tc := range []struct {
		u    acp.SessionInfoUpdate
		json string
	}{
		{acp.SessionInfoUpdate{}, `{}`},
		{acp.SessionInfoUpdate{Title: acp.Nullable[string]{Present: true}}, `{"title":null}`},
		{acp.SessionInfoUpdate{Title: acp.Nullable[string]{Value: &title, Present: true}}, `{"title":"Fix the bug"}`},
	} {
		if got := encode(t, tc.u); got != tc.json {
			t.Errorf("encoded %s, want %s", got, tc.json)
		}
		back := decode[acp.SessionInfoUpdate](t, tc.json)
		if back.Title.Present != tc.u.Title.Present || (back.Title.Value == nil) != (tc.u.Title.Value == nil) {
			t.Errorf("%s decoded as %+v", tc.json, back.Title)
		}
	}
}

func TestAFormKeepsItsFieldsInOrder(t *testing.T) {
	in := `{"type":"object","properties":{"zeta":{"type":"string"},"alpha":{"type":"boolean"},"mid":{"type":"integer"}},"required":["zeta"]}`
	s := decode[acp.ElicitationSchema](t, in)
	var names []string
	for _, p := range s.Properties {
		names = append(names, p.Name)
	}
	if strings.Join(names, ",") != "zeta,alpha,mid" || s.Properties[1].Schema.Boolean == nil {
		t.Errorf("properties %+v", s.Properties)
	}
	if out := encode(t, s); out != in {
		t.Errorf("encoded %s", out)
	}
}

func TestValueAndArrayUnions(t *testing.T) {
	content := decode[map[string]acp.ElicitationContentValue](t, `{"s":"x","i":42,"n":1.5,"b":true,"a":["p","q"]}`)
	if *content["s"].String != "x" || *content["i"].Integer != 42 || *content["n"].Number != 1.5 || !*content["b"].Boolean || len(content["a"].StringArray) != 2 {
		t.Errorf("content %+v", content)
	}
	grouped := decode[acp.SessionConfigSelectOptions](t, `[{"group":"g","name":"G","options":[{"value":"v","name":"V"}]}]`)
	flat := decode[acp.SessionConfigSelectOptions](t, `[{"value":"v","name":"V"}]`)
	if len(grouped.Grouped) != 1 || grouped.Ungrouped != nil || len(flat.Ungrouped) != 1 || flat.Grouped != nil {
		t.Errorf("grouped %+v, flat %+v", grouped, flat)
	}
	res := decode[acp.EmbeddedResourceResource](t, `{"uri":"file:///a","blob":"AA=="}`)
	if res.BlobResourceContents == nil || res.TextResourceContents != nil {
		t.Errorf("resource %+v", res)
	}
}

func TestErrorsCarryTheirCode(t *testing.T) {
	var err error = &acp.Error{Code: acp.ErrorCodeAuthenticationRequired, Message: "Authentication required"}
	var e *acp.Error
	if !errors.As(err, &e) || e.Code != -32000 || !strings.Contains(err.Error(), "-32000") {
		t.Errorf("%v", err)
	}
}
