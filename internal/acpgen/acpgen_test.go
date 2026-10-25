package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestGoName(t *testing.T) {
	for in, want := range map[string]string{
		"sessionId": "SessionID", "_meta": "Meta", "McpServerHttp": "MCPServerHTTP", "agent_message_chunk": "AgentMessageChunk",
		"value_id": "ValueID", "Parse error": "ParseError", "resource_link": "ResourceLink", "LlmProtocol": "LLMProtocol",
		"session/request_permission": "SessionRequestPermission", "mcpServers": "MCPServers", "uri": "URI",
	} {
		if got := goName(in); got != want {
			t.Errorf("goName(%q) = %q, want %q", in, got, want)
		}
	}
}

func loadModel(t *testing.T, file string) *model {
	t.Helper()
	raw, err := os.ReadFile("../../schema/" + file)
	if err != nil {
		t.Fatal(err)
	}
	s, err := loadSchema(raw)
	if err != nil {
		t.Fatal(err)
	}
	m, err := newModel(s, "acp")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestComment(t *testing.T) {
	m := loadModel(t, "schema.json")
	var b bytes.Buffer
	m.comment(&b, "", "Sends a [`SessionNotification`] with **care**.\n\nSee protocol docs: [Prompt Turn](/protocol/prompt-turn)\n\n```json\n{\"a\": 1}\n```", "")
	want := "// Sends a [SessionNotification] with care.\n//\n// See protocol docs: [Prompt Turn]\n//\n// \t{\"a\": 1}\n//\n// [Prompt Turn]: https://agentclientprotocol.com/protocol/prompt-turn\n"
	if b.String() != want {
		t.Errorf("got\n%s\nwant\n%s", b.String(), want)
	}
	b.Reset()
	m.comment(&b, "\t", "", "the fallback")
	if b.String() != "\t// the fallback\n" {
		t.Errorf("fallback: %q", b.String())
	}
}

// What the schema's definitions become, for each kind there is.
func TestKinds(t *testing.T) {
	m := loadModel(t, "schema.unstable.json")
	for name, want := range map[string]kind{
		"SessionUpdate": kTagged, "ContentBlock": kTagged, "SessionConfigOption": kTagged, "McpServer": kTagged,
		"EmbeddedResourceResource": kUntagged, "ElicitationContentValue": kValue, "SessionConfigSelectOptions": kArrayUnion,
		"StopReason": kEnum, "ErrorCode": kEnum, "SessionId": kAlias, "RequestId": kAlias, "PromptRequest": kStruct,
		"ElicitationFormMode": kStruct, "CompactionCapabilities": kStruct,
	} {
		d := m.defs[name]
		if d == nil || d.kind != want {
			t.Errorf("%s: %+v, want kind %d", name, d, want)
		}
	}
	if d := m.defs["McpServer"]; !hasDefault(d) || !d.open && len(d.vars) != 4 {
		t.Errorf("McpServer's variants: %+v", d.vars)
	}
	if d := m.defs["ErrorCode"]; d.under != "int32" || len(d.consts) < 8 || !d.open {
		t.Errorf("ErrorCode: %+v", d)
	}
	f := fieldOf(m.defs["SessionInfoUpdate"], "title")
	if f == nil || !f.nullable || f.typ != "Nullable[string]" {
		t.Errorf("SessionInfoUpdate.title: %+v", f)
	}
	if f := fieldOf(m.defs["TerminalExitStatus"], "exitCode"); f == nil || !f.ptr {
		t.Errorf("an optional number is held by pointer: %+v", f)
	}
	if f := fieldOf(m.defs["ElicitationSchema"], "properties"); f == nil || f.typ != "ElicitationProperties" {
		t.Errorf("a form's properties keep their order: %+v", f)
	}
}

func hasDefault(d *def) bool {
	for _, v := range d.vars {
		if v.def {
			return true
		}
	}
	return false
}

func fieldOf(d *def, name string) *field {
	for _, f := range d.fields {
		if f.json == name {
			return f
		}
	}
	return nil
}

// Every method of meta.json has its types, and comes out in its order.
func TestMethods(t *testing.T) {
	m := loadModel(t, "schema.unstable.json")
	meta, err := os.ReadFile("../../schema/meta.unstable.json")
	if err != nil {
		t.Fatal(err)
	}
	ms, err := methods(m, meta)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, x := range ms {
		names = append(names, x.side+":"+x.name)
	}
	got := strings.Join(names, " ")
	for _, want := range []string{"agent:Initialize", "agent:ForkSession", "agent:Cancel", "client:SessionUpdate", "client:MessageMCP", "agent:MessageMCP"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %s in %s", want, got)
		}
	}
	if !strings.HasPrefix(got, "agent:Initialize agent:Authenticate") {
		t.Errorf("not in meta.json's order: %s", got)
	}
}

// The generated code in the repository is what acpgen makes now, for both
// packages (the unstable one with its copies).
func TestTheGeneratedCodeIsCurrent(t *testing.T) {
	for _, pkg := range []struct{ name, schema, meta, dir, copyFrom string }{
		{"acp", "schema.json", "meta.json", "../..", ""},
		{"unstable", "schema.unstable.json", "meta.unstable.json", "../../unstable", "../.."},
	} {
		out := t.TempDir()
		if err := run("../../schema/"+pkg.schema, "../../schema/"+pkg.meta, pkg.name, out, pkg.copyFrom); err != nil {
			t.Fatal(err)
		}
		made, err := os.ReadDir(out)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range made {
			got, err := os.ReadFile(out + "/" + f.Name())
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(pkg.dir + "/" + f.Name())
			if err != nil || !bytes.Equal(got, want) {
				t.Errorf("%s/%s isn't what acpgen makes: run make generate", pkg.dir, f.Name())
			}
		}
	}
}
