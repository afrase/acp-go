package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// method is one of ACP's methods or notifications.
type method struct {
	wire      string // "session/prompt"
	name      string // the Go method: Prompt
	side      string // who handles it: "agent" or "client"
	req, resp string // Go types; resp is "" for a notification
	doc       string
	required  []string // the request's required properties
}

func (m method) notification() bool { return m.resp == "" }

// methods lists the schema's methods, in meta.json's order.
func methods(md *model, meta []byte) ([]method, error) {
	var top struct {
		AgentMethods  json.RawMessage `json:"agentMethods"`
		ClientMethods json.RawMessage `json:"clientMethods"`
	}
	if err := json.Unmarshal(meta, &top); err != nil {
		return nil, err
	}
	docs := methodDocs(md.s)
	byKey := map[string]*method{}
	for _, n := range md.s.names {
		x := md.s.defs[n]
		if x.Method == "" || x.Side == "protocol" {
			continue
		}
		key := x.Side + " " + x.Method
		mt := byKey[key]
		if mt == nil {
			mt = &method{wire: x.Method, side: x.Side}
			byKey[key] = mt
		}
		if strings.HasSuffix(n, "Response") {
			mt.resp = goName(n)
			continue
		}
		mt.req = goName(n)
		mt.doc = docs[n]
		mt.required = x.Required
		mt.name = strings.TrimSuffix(strings.TrimSuffix(mt.req, "Request"), "Notification")
		if mt.req == "SessionNotification" {
			mt.name = "SessionUpdate"
		}
	}
	var out []method
	for _, side := range []struct {
		name string
		raw  json.RawMessage
	}{{"agent", top.AgentMethods}, {"client", top.ClientMethods}} {
		_, values, err := orderedObject(side.raw)
		if err != nil {
			return nil, err
		}
		for _, v := range values {
			var wire string
			if err := json.Unmarshal(v, &wire); err != nil {
				return nil, err
			}
			mt := byKey[side.name+" "+wire]
			if mt == nil {
				return nil, fmt.Errorf("meta.json's %s has no types in the schema", wire)
			}
			out = append(out, *mt)
		}
	}
	return out, nil
}

// methodDocs are the descriptions the schema gives each method, by its
// request type, from the message unions (AgentRequest and the like).
func methodDocs(s *schema) map[string]string {
	docs := map[string]string{}
	for _, n := range s.names {
		d := s.defs[n]
		if !d.DocsIgnore || d.Properties.get("params") == nil {
			continue
		}
		for _, group := range d.Properties.get("params").AnyOf {
			for _, v := range group.AnyOf {
				if v.Title != "" && docs[v.Title] == "" {
					docs[v.Title] = v.Description
				}
			}
		}
	}
	return docs
}

// side is the Agent or the Client: the interface for the methods it
// handles, and the connection the other side calls them on.
type side struct{ name, conn, doc, other string }

// genMethods writes the Agent and Client interfaces, their Unimplemented
// types, the dispatch of incoming messages to them, and the calls each
// connection makes.
func genMethods(b *bytes.Buffer, md *model, ms []method) {
	for _, sd := range []side{
		{"Agent", "AgentConn", agentDoc, "agent's"},
		{"Client", "ClientConn", clientDoc, "client's"},
	} {
		var mine []method
		for _, m := range ms {
			if strings.EqualFold(m.side, sd.name) {
				mine = append(mine, m)
			}
		}
		genInterface(b, md, sd, mine)
		genUnimplemented(b, sd, mine)
		genDispatch(b, sd, mine)
		genCalls(b, sd, mine)
	}
}

func genInterface(b *bytes.Buffer, md *model, sd side, mine []method) {
	b.WriteString(sd.doc)
	fmt.Fprintf(b, "type %s interface {\n", sd.name)
	for _, m := range mine {
		md.comment(b, "\t", m.doc, "")
		if m.notification() {
			fmt.Fprintf(b, "\t%s(ctx context.Context, n *%s) error\n", m.name, m.req)
		} else {
			fmt.Fprintf(b, "\t%s(ctx context.Context, req *%s) (*%s, error)\n", m.name, m.req, m.resp)
		}
	}
	// The schema's docs for these are the client's (they say the agent
	// sends them), so each side gets its own.
	from := "agent"
	if sd.name == "Agent" {
		from = "client"
	}
	md.comment(b, "\t", fmt.Sprintf(extMethodDoc, from), "")
	b.WriteString("\tExtMethod(ctx context.Context, method string, params json.RawMessage) (any, error)\n")
	md.comment(b, "\t", fmt.Sprintf(extNotificationDoc, from), "")
	b.WriteString("\tExtNotification(ctx context.Context, method string, params json.RawMessage) error\n")
	fmt.Fprintf(b, "\tmustEmbedUnimplemented%s()\n}\n\n", sd.name)
}

func genUnimplemented(b *bytes.Buffer, sd side, mine []method) {
	fmt.Fprintf(b, "// Unimplemented%s answers every request with ACP's \"Method not found\"\n// error and ignores every notification. Embed it in %s: the methods\n// you write replace its, and a method a later release of this package\n// adds is answered for you.\ntype Unimplemented%s struct{}\n\n", sd.name, article(sd.name), sd.name)
	for _, m := range mine {
		fmt.Fprintf(b, "// %s answers %s", m.name, m.wire)
		if m.notification() {
			fmt.Fprintf(b, " by ignoring it.\nfunc (Unimplemented%s) %s(context.Context, *%s) error { return nil }\n\n", sd.name, m.name, m.req)
		} else {
			fmt.Fprintf(b, " with \"Method not found\".\nfunc (Unimplemented%s) %s(context.Context, *%s) (*%s, error) {\n\treturn nil, methodNotFound(%q)\n}\n\n", sd.name, m.name, m.req, m.resp, m.wire)
		}
	}
	fmt.Fprintf(b, "// ExtMethod answers with \"Method not found\".\nfunc (Unimplemented%s) ExtMethod(_ context.Context, method string, _ json.RawMessage) (any, error) {\n\treturn nil, methodNotFound(method)\n}\n\n", sd.name)
	fmt.Fprintf(b, "// ExtNotification ignores the notification.\nfunc (Unimplemented%s) ExtNotification(context.Context, string, json.RawMessage) error { return nil }\n\n", sd.name)
	fmt.Fprintf(b, "func (Unimplemented%s) mustEmbedUnimplemented%s() {}\n\n", sd.name, sd.name)
}

func genDispatch(b *bytes.Buffer, sd side, mine []method) {
	lower := strings.ToLower(sd.name)
	fmt.Fprintf(b, "// %sHandler routes the messages for %s to its methods.\nfunc %sHandler(x %s) handler {\n\treturn handler{\n", lower, article(sd.name), lower, sd.name)
	b.WriteString("\t\trequest: func(ctx context.Context, method string, params json.RawMessage) (any, error) {\n\t\t\tswitch method {\n")
	for _, m := range mine {
		if m.notification() {
			continue
		}
		var args strings.Builder
		for _, r := range m.required {
			fmt.Fprintf(&args, ", %q", r)
		}
		fmt.Fprintf(b, "\t\t\tcase %q:\n\t\t\t\tvar req %s\n\t\t\t\tif err := decodeParams(params, &req%s); err != nil {\n\t\t\t\t\treturn nil, err\n\t\t\t\t}\n", m.wire, m.req, args.String())
		fmt.Fprintf(b, "\t\t\t\tresp, err := x.%s(ctx, &req)\n\t\t\t\tif err != nil {\n\t\t\t\t\treturn nil, err\n\t\t\t\t}\n\t\t\t\tif resp == nil {\n\t\t\t\t\tresp = new(%s)\n\t\t\t\t}\n\t\t\t\treturn resp, nil\n", m.name, m.resp)
	}
	b.WriteString("\t\t\t}\n\t\t\tif isExtension(method) {\n\t\t\t\treturn x.ExtMethod(ctx, method, params)\n\t\t\t}\n\t\t\treturn nil, methodNotFound(method)\n\t\t},\n")
	b.WriteString("\t\tnotify: func(ctx context.Context, method string, params json.RawMessage) error {\n\t\t\tswitch method {\n")
	for _, m := range mine {
		if m.notification() {
			fmt.Fprintf(b, "\t\t\tcase %q:\n\t\t\t\tvar n %s\n\t\t\t\tif err := decodeParams(params, &n); err != nil {\n\t\t\t\t\treturn err\n\t\t\t\t}\n\t\t\t\treturn x.%s(ctx, &n)\n", m.wire, m.req, m.name)
		}
	}
	b.WriteString("\t\t\t}\n\t\t\tif isExtension(method) {\n\t\t\t\treturn x.ExtNotification(ctx, method, params)\n\t\t\t}\n\t\t\treturn errUnknownNotification\n\t\t},\n\t}\n}\n\n")
}

// genCalls writes the other side's calls of sd's methods, on its
// connection.
func genCalls(b *bytes.Buffer, sd side, mine []method) {
	for _, m := range mine {
		if m.notification() {
			fmt.Fprintf(b, "// %s sends the %s notification: see [%s.%s].\n", m.name, m.wire, sd.name, m.name)
			fmt.Fprintf(b, "func (c *%s) %s(ctx context.Context, n *%s) error {\n\treturn c.c.notify(ctx, %q, n)\n}\n\n", sd.conn, m.name, m.req, m.wire)
			continue
		}
		fmt.Fprintf(b, "// %s calls the %s %s: see [%s.%s].\n", m.name, sd.other, m.wire, sd.name, m.name)
		fmt.Fprintf(b, "func (c *%s) %s(ctx context.Context, req *%s) (*%s, error) {\n\tresp := new(%s)\n\tif err := c.c.call(ctx, %q, req, resp); err != nil {\n\t\treturn nil, err\n\t}\n\treturn resp, nil\n}\n\n", sd.conn, m.name, m.req, m.resp, m.resp, m.wire)
	}
}

// genRecorders writes, for the tests, a RecordingAgent and a
// RecordingClient: each sends the name of every notification method called
// on it, so a test can see a notification reach the method of its name.
// They are exported from a _test.go file of the package, for its external
// tests.
func genRecorders(b *bytes.Buffer, ms []method) {
	for _, sd := range []string{"Agent", "Client"} {
		fmt.Fprintf(b, "// Recording%s is %s that sends the name of each notification\n// method called to Got.\ntype Recording%s struct {\n\tUnimplemented%s\n\tGot chan<- string\n}\n\n", sd, map[string]string{"Agent": "an Agent", "Client": "a Client"}[sd], sd, sd)
		for _, m := range ms {
			if m.notification() && strings.EqualFold(m.side, sd) {
				fmt.Fprintf(b, "// %s records the call.\nfunc (r Recording%s) %s(context.Context, *%s) error {\n\tr.Got <- %q\n\treturn nil\n}\n\n", m.name, sd, m.name, m.req, m.name)
			}
		}
	}
}

const agentDoc = `// Agent is what an agent does: the methods a client calls and the
// notifications it sends. Its methods run as the client's messages arrive:
// each request on a goroutine of its own, each notification in turn on
// the connection's reading goroutine (so it must not wait for the client:
// see [ClientConn]).
//
// Implementations embed [UnimplementedAgent], which answers what they
// leave out, so a method added to Agent in a later release doesn't break
// them. An agent answers only the optional methods its capabilities say
// it has.
`

const clientDoc = `// Client is what a client does: the methods an agent calls and the
// notifications it sends. Its methods run as the agent's messages arrive:
// each request on a goroutine of its own, each notification in turn on
// the connection's reading goroutine (so it must not wait for the agent:
// see [AgentConn]).
//
// Implementations embed [UnimplementedClient], which answers what they
// leave out, so a method added to Client in a later release doesn't break
// them. A client answers only the optional methods its capabilities say
// it has.
`

const (
	extMethodDoc = "ExtMethod answers the %s's requests whose method starts with \"_\":\n" +
		"methods outside ACP, which extensions add. Its result is encoded as\n" +
		"the response.\n\nSee protocol docs: [Extensibility](/protocol/v1/extensibility)"
	extNotificationDoc = "ExtNotification gets the %s's notifications whose method starts\n" +
		"with \"_\": notifications outside ACP, which extensions add.\n\n" +
		"See protocol docs: [Extensibility](/protocol/v1/extensibility)"
)

// article is name with "a" or "an" before it, by its first letter.
func article(name string) string {
	if name != "" && strings.ContainsRune("AEIOU", rune(name[0])) {
		return "an " + name
	}
	return "a " + name
}
