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

// genMethods writes the Agent and Client interfaces, their Unimplemented
// types, the dispatch of incoming messages to them, and the calls each
// connection makes.
func genMethods(b *bytes.Buffer, md *model, ms []method) {
	docs := methodDocs(md.s)
	for _, side := range []struct{ name, conn, doc string }{
		{"Agent", "AgentConn", agentDoc},
		{"Client", "ClientConn", clientDoc},
	} {
		lower := strings.ToLower(side.name)
		var mine []method
		for _, m := range ms {
			if m.side == lower {
				mine = append(mine, m)
			}
		}
		// The interface.
		b.WriteString(side.doc)
		fmt.Fprintf(b, "type %s interface {\n", side.name)
		for _, m := range mine {
			md.comment(b, "\t", m.doc, "")
			if m.notification() {
				fmt.Fprintf(b, "\t%s(ctx context.Context, n *%s) error\n", m.name, m.req)
			} else {
				fmt.Fprintf(b, "\t%s(ctx context.Context, req *%s) (*%s, error)\n", m.name, m.req, m.resp)
			}
		}
		md.comment(b, "\t", docs["ExtMethodRequest"]+extMethodNote, "")
		b.WriteString("\tExtMethod(ctx context.Context, method string, params json.RawMessage) (any, error)\n")
		md.comment(b, "\t", docs["ExtNotification"]+extNotificationNote, "")
		b.WriteString("\tExtNotification(ctx context.Context, method string, params json.RawMessage) error\n")
		fmt.Fprintf(b, "\tmustEmbedUnimplemented%s()\n}\n\n", side.name)

		// Unimplemented.
		fmt.Fprintf(b, "// Unimplemented%s answers every request with ACP's \"Method not found\"\n// error and ignores every notification. Embed it in a %s: the methods\n// you write replace its, and a method a later release of this package\n// adds is answered for you.\ntype Unimplemented%s struct{}\n\n", side.name, side.name, side.name)
		for _, m := range mine {
			fmt.Fprintf(b, "// %s answers %s", m.name, m.wire)
			if m.notification() {
				fmt.Fprintf(b, " by ignoring it.\nfunc (Unimplemented%s) %s(context.Context, *%s) error { return nil }\n\n", side.name, m.name, m.req)
			} else {
				fmt.Fprintf(b, " with \"Method not found\".\nfunc (Unimplemented%s) %s(context.Context, *%s) (*%s, error) {\n\treturn nil, methodNotFound(%q)\n}\n\n", side.name, m.name, m.req, m.resp, m.wire)
			}
		}
		fmt.Fprintf(b, "// ExtMethod answers with \"Method not found\".\nfunc (Unimplemented%s) ExtMethod(_ context.Context, method string, _ json.RawMessage) (any, error) {\n\treturn nil, methodNotFound(method)\n}\n\n", side.name)
		fmt.Fprintf(b, "// ExtNotification ignores the notification.\nfunc (Unimplemented%s) ExtNotification(context.Context, string, json.RawMessage) error { return nil }\n\n", side.name)
		fmt.Fprintf(b, "func (Unimplemented%s) mustEmbedUnimplemented%s() {}\n\n", side.name, side.name)

		// Dispatch.
		fmt.Fprintf(b, "// %sHandler routes the messages for a %s to its methods.\nfunc %sHandler(x %s) handler {\n\treturn handler{\n", lower, side.name, lower, side.name)
		b.WriteString("\t\trequest: func(ctx context.Context, method string, params json.RawMessage) (any, error) {\n\t\t\tswitch method {\n")
		for _, m := range mine {
			if m.notification() {
				continue
			}
			fmt.Fprintf(b, "\t\t\tcase %q:\n\t\t\t\tvar req %s\n\t\t\t\tif err := decodeParams(params, &req); err != nil {\n\t\t\t\t\treturn nil, err\n\t\t\t\t}\n", m.wire, m.req)
			fmt.Fprintf(b, "\t\t\t\tresp, err := x.%s(ctx, &req)\n\t\t\t\tif err != nil {\n\t\t\t\t\treturn nil, err\n\t\t\t\t}\n\t\t\t\tif resp == nil {\n\t\t\t\t\tresp = new(%s)\n\t\t\t\t}\n\t\t\t\treturn resp, nil\n", m.name, m.resp)
		}
		b.WriteString("\t\t\t}\n\t\t\tif isExtension(method) {\n\t\t\t\treturn x.ExtMethod(ctx, method, params)\n\t\t\t}\n\t\t\treturn nil, methodNotFound(method)\n\t\t},\n")
		b.WriteString("\t\tnotify: func(ctx context.Context, method string, params json.RawMessage) error {\n\t\t\tswitch method {\n")
		for _, m := range mine {
			if !m.notification() {
				continue
			}
			fmt.Fprintf(b, "\t\t\tcase %q:\n\t\t\t\tvar n %s\n\t\t\t\tif err := decodeParams(params, &n); err != nil {\n\t\t\t\t\treturn err\n\t\t\t\t}\n\t\t\t\treturn x.%s(ctx, &n)\n", m.wire, m.req, m.name)
		}
		b.WriteString("\t\t\t}\n\t\t\tif isExtension(method) {\n\t\t\t\treturn x.ExtNotification(ctx, method, params)\n\t\t\t}\n\t\t\treturn errUnknownNotification\n\t\t},\n\t}\n}\n\n")

		// The other side's calls.
		other := "agent's"
		if side.name == "Client" {
			other = "client's"
		}
		for _, m := range mine {
			if m.notification() {
				fmt.Fprintf(b, "// %s sends the %s notification: see [%s.%s].\n", m.name, m.wire, side.name, m.name)
				fmt.Fprintf(b, "func (c *%s) %s(ctx context.Context, n *%s) error {\n\treturn c.c.notify(ctx, %q, n)\n}\n\n", side.conn, m.name, m.req, m.wire)
				continue
			}
			fmt.Fprintf(b, "// %s calls the %s %s: see [%s.%s].\n", m.name, other, m.wire, side.name, m.name)
			fmt.Fprintf(b, "func (c *%s) %s(ctx context.Context, req *%s) (*%s, error) {\n\tresp := new(%s)\n\tif err := c.c.call(ctx, %q, req, resp); err != nil {\n\t\treturn nil, err\n\t}\n\treturn resp, nil\n}\n\n", side.conn, m.name, m.req, m.resp, m.resp, m.wire)
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
	extMethodNote = "\n\nExtMethod gets every request whose method starts with \"_\"; its result\nis encoded as the response."
	// The notes start on a paragraph of their own.
	extNotificationNote = "\n\nExtNotification gets every notification whose method starts with \"_\"."
)
