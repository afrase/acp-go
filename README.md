# acp-go

A Go library for the [Agent Client Protocol](https://agentclientprotocol.com)
(ACP), the JSON-RPC protocol between coding agents (Claude Code through
claude-agent-acp, Codex through codex-acp, Gemini CLI, …) and the clients that
run them (editors, chat apps). It does both sides: a client that runs or
connects to an agent, and an agent (or a bridge to one) that serves a client.

```
go get github.com/afrase/acp-go
```

It needs Go 1.25 or later and imports only the standard library. Coming from
the TypeScript or Rust SDK: `AgentConn` is their `ClientSideConnection`, and
`ClientConn` their `AgentSideConnection`.

- **All of ACP v1**, generated from the [official schema](https://github.com/agentclientprotocol/agent-client-protocol)
  (release 1.26.0), with the schema's descriptions as the docs. The parts the
  schema marks unstable (session/fork, a turn's usage, providers, MCP over
  ACP, system prompts, subagents, …) are a package of their own,
  [`unstable`](https://pkg.go.dev/github.com/afrase/acp-go/unstable), so code
  that uses only the stable protocol can't depend on them by accident.
- **No queues, no size cap.** Notifications are handled in order as they are
  read, so a slow client slows the agent down instead of a buffer filling
  up; nothing is dropped and the connection never closes for being busy.
  Messages have no size limit unless you set one.
- **Contexts all the way through**, `$/cancel_request` both ways, handler
  panics turned into errors, and connections that end without leaving
  goroutines behind.
- **Checked against others**: the official TypeScript SDK's example agent
  and client (both ways), ACP's conformance kit, and real agents.

## Why another Go SDK

[Nushi](https://github.com/afrase/nushi), a chat app for coding agents, first
used the Go SDKs there were, and two of their limits broke real chats: a
1,024-message queue that, once full (a long session replayed, a fast turn
while the app was busy), closed the connection; and a 10 MiB cap on a
message, which a turn with a few images passes. This library is the client
Nushi wrote instead, grown to cover all of ACP and both sides. Compared
plainly with coder's (the most used) and caelis-labs' (the most complete):

| | acp-go | [coder/acp-go-sdk](https://github.com/coder/acp-go-sdk) | [caelis-labs/acp-go-sdk](https://github.com/caelis-labs/acp-go-sdk) |
|---|---|---|---|
| ACP schema | 1.26.0, stable and unstable | an older one (0.13.5, June 2026) | 1.26.0 stable; the v2 draft in `experimental/v2` |
| Unstable parts | a package of their own, with the same API | in the main package, named `Unstable…` | not there |
| When the other side outpaces you | it waits (back-pressure) | the connection closes after 1,024 queued notifications | the connection fails at a queue limit (1,024 notifications or 32 MiB, configurable) |
| Message size | no cap unless you set one | 10 MiB | 10 MiB by default, configurable |
| `$/cancel_request` | both ways | both ways | both ways |
| Running an agent | `Start`: its process group killed on Unix, the process alone on Windows | you start it | its process tree, on Unix and on Windows (job objects) |
| Checked against | the TypeScript SDK both ways; acp-tck | its own tests and examples, including a Gemini CLI client | the TypeScript and Rust SDKs, four ways |
| Dependencies | none | none | `golang.org/x/sys` |

caelis-labs' SDK is the most thorough of the others (its interop matrix,
Windows job objects and v2 draft are beyond this one); its API is much larger
(prepared requests, readiness hooks, raw session updates). Pick it if you
need ACP v2 or Windows process trees. Pick this one if you want the unstable
protocol, a small API, or a connection that never gives up under load.

## A client

```go
type client struct {
	acp.UnimplementedClient // answers what this client doesn't do
}

func (c *client) SessionUpdate(ctx context.Context, n *acp.SessionNotification) error {
	if chunk := n.Update.AgentMessageChunk; chunk != nil && chunk.Content.Text != nil {
		fmt.Print(chunk.Content.Text.Text)
	}
	return nil
}

func (c *client) RequestPermission(ctx context.Context, req *acp.RequestPermissionRequest) (*acp.RequestPermissionResponse, error) {
	// Ask the user; here, refuse.
	return &acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: true}}, nil
}

func run(ctx context.Context) error {
	agent, err := acp.Start(exec.Command("claude-agent-acp"), func(*acp.AgentConn) acp.Client { return &client{} })
	if err != nil {
		return err
	}
	defer agent.Shutdown(ctx)

	if _, err := agent.Initialize(ctx, &acp.InitializeRequest{ProtocolVersion: acp.LatestProtocolVersion}); err != nil {
		return err
	}
	session, err := agent.NewSession(ctx, &acp.NewSessionRequest{Cwd: "/path/to/project"})
	if err != nil {
		return err
	}
	result, err := agent.Prompt(ctx, &acp.PromptRequest{
		SessionID: session.SessionID,
		Prompt:    []acp.ContentBlock{{Text: &acp.TextContent{Text: "What does this project do?"}}},
	})
	if err != nil {
		return err
	}
	fmt.Println("\nended:", result.StopReason) // every update of the turn has been handled by now
	return nil
}
```

[`examples/prompt`](examples/prompt/main.go) is a whole client: it runs any
agent, sends a prompt and prints the turn (`go run ./examples/prompt -p "hi"
-- claude-agent-acp`).

## An agent

```go
type agent struct {
	acp.UnimplementedAgent // answers what this agent doesn't do
	client *acp.ClientConn
}

func (a *agent) Initialize(ctx context.Context, req *acp.InitializeRequest) (*acp.InitializeResponse, error) {
	return &acp.InitializeResponse{ProtocolVersion: acp.LatestProtocolVersion}, nil
}

func (a *agent) NewSession(ctx context.Context, req *acp.NewSessionRequest) (*acp.NewSessionResponse, error) {
	return &acp.NewSessionResponse{SessionID: "session-1"}, nil
}

func (a *agent) Prompt(ctx context.Context, req *acp.PromptRequest) (*acp.PromptResponse, error) {
	err := a.client.SessionUpdate(ctx, &acp.SessionNotification{
		SessionID: req.SessionID,
		Update: acp.SessionUpdate{AgentMessageChunk: &acp.ContentChunk{
			Content: acp.ContentBlock{Text: &acp.TextContent{Text: "Hello!"}},
		}},
	})
	return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, err
}

func main() {
	conn := acp.NewClientConn(os.Stdin, os.Stdout, func(c *acp.ClientConn) acp.Agent {
		return &agent{client: c}
	})
	<-conn.Done()
}
```

[`examples/echo`](examples/echo/main.go) is a whole agent, which Zed or any
ACP client can run. Both examples above run as tests
([`Example_client`, `Example_agent`](https://pkg.go.dev/github.com/afrase/acp-go#pkg-examples)).

## The unstable protocol

```go
import acp "github.com/afrase/acp-go/unstable"

fork, err := agent.ForkSession(ctx, &acp.ForkSessionRequest{SessionID: id, Cwd: dir})
result, err := agent.Prompt(ctx, req) // result.Usage: the turn's tokens
```

`unstable` has the same API as the root package, generated from the
schema's unstable release; use one or the other throughout a program. Its
unstable parts may change in any minor release (see Versioning).

## How it works

- **Unions** (a session update, a content block, a tool call's content) are
  a struct with a field per variant: set one (`ContentBlock{Text: …}`) and
  the tag is filled in; read the one that isn't nil. A variant this package
  doesn't know (a newer peer's) is kept whole in `Other` and sent on as it
  came.
- **Lenient decoding**, as the schema asks: a bad optional field (one marked
  `x-deserialize-default-on-error`) decodes as absent, and bad items of some
  lists are dropped, rather than the whole message failing. A request
  missing a required property is answered Invalid params.
- **`Nullable`** is a field whose null means something other than its
  absence (an update's title set to null clears it). A form's fields keep
  their order (`ElicitationProperties`).
- **Extensions**: every type has its `_meta` (`Meta`); methods starting with
  `_` go to `ExtMethod` and `ExtNotification`, and out with `Call` and
  `Notify`. [`steering`](https://pkg.go.dev/github.com/afrase/acp-go/steering)
  is one, the `_session/steering` extension claude-agent-acp, codex-acp and
  others have.
- **Errors** the other side answers with are an `*acp.Error` with ACP's code
  (`errors.As`); calls after a connection ended fail with `acp.ErrClosed`.
- **Options**: `WithLogger` (log/slog; the library logs nothing without it,
  and never what messages carry), `WithReadLimit`, `WithWriteLimit`.

### Where the spec is quiet

- *Batches*: JSON-RPC allows them, ACP v1 doesn't mention them; the
  TypeScript SDK refuses them in v1, and so does this (Invalid request).
- *A line that isn't JSON*: agents sometimes log to stdout. It is answered
  with a Parse error (null id), as JSON-RPC says, logged by size only, and
  the connection goes on.
- *A call whose context ends*: it returns at once and the request is
  withdrawn with `$/cancel_request`; the answer, when it comes, is dropped.
  A prompt is still best stopped with `Cancel` (session/cancel), as ACP
  says, which ends it with stop reason `cancelled`.
- *Absent and null*: ACP treats an omitted optional field and null alike,
  except where the schema says otherwise; those fields are `Nullable`.
- *An agent that exits*: what it wrote before exiting is still read (for a
  second, in case a process it left holds its stdout open), then its
  process group is killed. One that closes its stdout first has a second
  to exit, or, under `Shutdown`, until its context ends.
- *An elicitation's answer* may be an integer or a number; a JSON number
  without a fraction or exponent decodes as `Integer`.
- *Required properties*: an incoming request without one is refused, as
  the TypeScript and Rust SDKs do; one this side sends always has them (a
  required list left nil goes as `[]`).

## Interoperability

Tested in CI (`make interop`) against the official TypeScript SDK 2.0.2:
its example agent with a Go client (a turn with a read, then an edit it asks
permission for, allowed or refused; a cancelled turn; a withdrawn call), and
its example client with `examples/echo`. ACP's conformance kit,
[acp-tck](https://github.com/agentclientprotocol/acp-tck), finds
`examples/echo` conformant (28 of 28 mandatory checks; of the advisory
ones it misses only connecting to the MCP servers it is given, which an
echo has no use for). By hand: Claude Code (claude-agent-acp 0.94.0) and
Codex (codex-acp 2.2.2) with `examples/prompt` (a turn, `session/list`,
`session/load`), and all of it again through
[Nushi](https://github.com/afrase/nushi), which runs on the `unstable`
package: those two, and Ante and PiG through Nushi's own bridges, built on
this library's agent side (turns with tool calls, permission requests, a
reopen after a restart, `session/fork`, a rewind, a cancelled turn,
steering).

## Versioning

Semantic versioning, from v0.1.0. Until v1.0.0 a minor release may change
the API; after, only a major one. Each release follows one release of ACP's
schema, named in `acp.SchemaVersion` and in the [CHANGELOG](CHANGELOG.md);
a new schema release means a new minor release of this module. ACP's
protocol version (1) is `acp.LatestProtocolVersion`. The `unstable` package
follows the schema's unstable parts, which ACP changes freely: they may
change in any minor release, whatever this module's major version.

## Developing

```
make check     # lint, the generated code, the tests with -race
make interop   # against the TypeScript SDK (Node.js 24 or later; downloads into a temp folder)
make fuzz      # each fuzz target for FUZZTIME (30s)
make bench     # a streaming turn: about 260,000 updates a second on an M3 Pro, both sides in one process
make schema    # fetch the schema release in schema/VERSION, check it, regenerate
```

The types and methods are generated by [`internal/acpgen`](internal/acpgen/main.go)
from the pinned schema in `schema/`, and the `unstable` package's
hand-written files are copies of the root package's (`make generate`).
golangci-lint is pinned in `tools/lint` and run through `go tool`, so
nothing is installed. To move to a new schema release, put its version in
`schema/VERSION` and its files' SHA-256s in `schema/SHA256SUMS`, then run
`make schema` and fix what the tests say.

Working on this module and a program that uses it at once: a `go.work`
beside the program (`go work init . ../acp-go`), kept out of git.

## Licence

MIT. The schema in `schema/` and the descriptions in the generated code are
ACP's, under the Apache License 2.0 (`schema/LICENSE`, `schema/NOTICE`).
