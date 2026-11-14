# acp-go

[![Go Reference](https://pkg.go.dev/badge/github.com/afrase/acp-go.svg)](https://pkg.go.dev/github.com/afrase/acp-go)
[![CI](https://github.com/afrase/acp-go/actions/workflows/ci.yml/badge.svg)](https://github.com/afrase/acp-go/actions/workflows/ci.yml)

A Go library for the [Agent Client Protocol](https://agentclientprotocol.com)
(ACP). ACP is the JSON-RPC protocol between coding agents and the apps that
run them, such as editors and chat apps.

acp-go does both sides:

- **Client:** start or connect to an agent (Claude Code, Codex, Gemini CLI, …).
- **Agent:** serve a client, or bridge a client to another agent.

```
go get github.com/afrase/acp-go
```

It needs Go 1.25 or later and uses only the standard library.

## Features

- **All of ACP v1**, generated from the
  [official schema](https://github.com/agentclientprotocol/agent-client-protocol)
  (release 1.26.0). The schema's descriptions are the docs.
- **Unstable parts in their own package.**
  [`unstable`](https://pkg.go.dev/github.com/afrase/acp-go/unstable) holds
  session/fork, a turn's usage, subagents and the other draft features. Code
  that uses only the root package cannot depend on them by accident.
- **Back-pressure, no queues.** Notifications are handled in order, as they
  are read. A slow client slows the agent down. Nothing is dropped, and a
  busy connection never closes.
- **No message size limit**, unless you set one.
- **Contexts everywhere.** `$/cancel_request` works in both directions. A
  handler panic becomes an error. A connection that ends leaves no
  goroutines behind.
- **Tested against other implementations:** the official TypeScript SDK,
  ACP's conformance kit, and real agents.

If you know the TypeScript or Rust SDK: `AgentConn` is their
`ClientSideConnection`, and `ClientConn` is their `AgentSideConnection`.

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

[`examples/prompt`](examples/prompt/main.go) is a complete client. It starts
any agent, sends a prompt and prints the turn:

```
go run ./examples/prompt -p "hi" -- claude-agent-acp
```

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

[`examples/echo`](examples/echo/main.go) is a complete agent that Zed or any
other ACP client can run. Both examples above also run as tests
([`Example_client`, `Example_agent`](https://pkg.go.dev/github.com/afrase/acp-go#pkg-examples)).

## The unstable protocol

```go
import acp "github.com/afrase/acp-go/unstable"

fork, err := agent.ForkSession(ctx, &acp.ForkSessionRequest{SessionID: id, Cwd: dir})
result, err := agent.Prompt(ctx, req) // result.Usage: the turn's tokens
```

`unstable` has the same API as the root package. It is generated from the
schema's unstable release. Use one package or the other in a program, not
both. Its unstable parts can change in any minor release (see
[Versioning](#versioning)).

## How it works

- **Unions** (a session update, a content block, a tool call's content) are
  structs with one field for each variant. Set one field, and the tag is
  filled in. To read one, find the field that is not nil. A variant this
  package does not know (from a newer peer) is kept whole in `Other` and
  sent on unchanged.
- **Lenient decoding**, as the schema asks. A bad optional field (marked
  `x-deserialize-default-on-error`) decodes as absent. Bad items in some
  lists are dropped. The rest of the message still decodes. A request
  without a required property gets an Invalid params error.
- **`Nullable`** marks a field where null is different from absent. For
  example, an update that sets the title to null clears the title.
- **Form fields keep their order** (`ElicitationProperties`).
- **Extensions.** Every type has `Meta` for its `_meta`. Incoming methods
  that start with `_` go to `ExtMethod` and `ExtNotification`. Send your own
  with `Call` and `Notify`. The
  [`steering`](https://pkg.go.dev/github.com/afrase/acp-go/steering) package
  is one extension: `_session/steering`, which claude-agent-acp, codex-acp
  and others support.
- **Errors.** An error from the other side is an `*acp.Error` with ACP's
  code (use `errors.As`). A call after the connection ends returns
  `acp.ErrClosed`.
- **Options:** `WithLogger` (log/slog), `WithReadLimit`, `WithWriteLimit`.
  Without a logger, the library logs nothing. It never logs message
  contents.

<details>
<summary><b>Where the spec is silent</b></summary>

| Case | What acp-go does |
|---|---|
| JSON-RPC batches | Refused with Invalid request. ACP v1 does not mention batches, and the TypeScript SDK refuses them in v1 too. |
| A line that is not JSON (some agents log to stdout) | Answered with a Parse error (null id), as JSON-RPC says. Logged by size only. The connection continues. |
| A call whose context ends | Returns at once and withdraws the request with `$/cancel_request`. A late answer is dropped. To stop a prompt, use `Cancel` (session/cancel) instead, as ACP says. The turn then ends with stop reason `cancelled`. |
| Absent and null | The same, as ACP says, except for `Nullable` fields. |
| An agent that exits | Its remaining output is read for up to one second, in case a child process still holds its stdout open. Then its process group is killed. An agent that closes its stdout first has one second to exit, or, under `Shutdown`, until the context ends. |
| A number in an elicitation's answer | A JSON number without a fraction or exponent decodes as `Integer`. |
| Required properties | An incoming request without one is refused, as the TypeScript and Rust SDKs do. Outgoing messages always have them: a nil required list is sent as `[]`. |

</details>

## Compatibility

**In CI** (`make interop`), against the official TypeScript SDK 2.0.2:

- Its example agent with a Go client: a turn with a file read, then an edit
  that asks for permission (allowed and refused), a cancelled turn, and a
  withdrawn call.
- Its example client with `examples/echo`.

**Conformance:** ACP's conformance kit,
[acp-tck](https://github.com/agentclientprotocol/acp-tck), finds
`examples/echo` conformant: 28 of 28 mandatory checks. Of the advisory
checks, it misses only one: connecting to the MCP servers it is given. An
echo agent has no use for them.

**By hand:**

- Claude Code (claude-agent-acp 0.94.0) and Codex (codex-acp 2.2.2) with
  `examples/prompt`: a turn, `session/list` and `session/load`.
- [Nushi](https://github.com/afrase/nushi), which uses the `unstable`
  package: the same two agents, plus Ante and PiG through Nushi's own
  bridges (built on this library's agent side). Tested: tool calls,
  permission requests, a reopen after a restart, `session/fork`, a rewind,
  a cancelled turn, and steering.

## Why another Go SDK

[Nushi](https://github.com/afrase/nushi), a chat app for coding agents, first
used the existing Go SDKs. Two of their limits broke real chats:

- **A queue of 1,024 messages.** When it was full, the connection closed. A
  long session replay, or a fast turn while the app was busy, was enough.
- **A 10 MiB message cap.** A turn with a few images goes over it.

acp-go is the client Nushi wrote instead, extended to all of ACP and both
sides. Compared with coder's SDK (the most used) and caelis-labs' (the most
complete):

| | acp-go | [coder/acp-go-sdk](https://github.com/coder/acp-go-sdk) | [caelis-labs/acp-go-sdk](https://github.com/caelis-labs/acp-go-sdk) |
| ACP schema | 1.26.0, stable and unstable | an older one (0.13.5, June 2026) | 1.26.0 stable; the v2 draft in `experimental/v2` |
| Unstable parts | a package of their own, with the same API | in the main package, named `Unstable…` | not there |
| When the other side outpaces you | it waits (back-pressure) | the connection closes after 1,024 queued notifications | the connection fails at a queue limit (1,024 notifications or 32 MiB, configurable) |
| Message size | no cap unless you set one | 10 MiB | 10 MiB by default, configurable |
| `$/cancel_request` | both ways | both ways | both ways |
| Running an agent | `Start`: its process group killed on Unix, the process alone on Windows | you start it | its process tree, on Unix and on Windows (job objects) |
| Checked against | the TypeScript SDK both ways; acp-tck | its own tests and examples, including a Gemini CLI client | the TypeScript and Rust SDKs, four ways |
| Dependencies | none | none | `golang.org/x/sys` |

caelis-labs' SDK is the most thorough of the others. Its interop matrix,
Windows job objects and v2 draft go further than acp-go. Its API is also
much larger (prepared requests, readiness hooks, raw session updates).

- Pick caelis-labs' SDK if you need ACP v2 or Windows process trees.
- Pick acp-go if you want the unstable protocol, a small API, or a
  connection that never gives up under load.

## Versioning

- Semantic versioning, from v0.1.0. Before v1.0.0, a minor release can
  change the API. After v1.0.0, only a major release can.
- Each release follows one release of ACP's schema. `acp.SchemaVersion` and
  the [CHANGELOG](CHANGELOG.md) name it. A new schema release means a new
  minor release of this module.
- `acp.LatestProtocolVersion` is ACP's protocol version (1).
- The `unstable` package can change in any minor release, whatever this
  module's major version, because ACP changes those parts freely.

## Developing

```
make check     # lint, the generated code, the tests with -race
make interop   # against the TypeScript SDK (Node.js 24 or later; downloads into a temp folder)
make fuzz      # each fuzz target for FUZZTIME (30s)
make bench     # a streaming turn: about 260,000 updates a second on an M3 Pro, both sides in one process
make schema    # fetch the schema release in schema/VERSION, check it, regenerate
```

- `internal/acpgen` ([source](internal/acpgen/main.go)) generates the types
  and methods from the pinned schema in `schema/`.
- The `unstable` package's hand-written files are copies of the root
  package's (`make generate`).
- golangci-lint is pinned in `tools/lint` and runs through `go tool`, so
  you install nothing.

To move to a new schema release:

1. Put its version in `schema/VERSION`.
2. Put its files' SHA-256 sums in `schema/SHA256SUMS`.
3. Run `make schema`, then fix what the tests report.

To work on this module and a program that uses it at the same time, put a
`go.work` next to the program (`go work init . ../acp-go`). Keep it out of
git.

## Licence

MIT. The schema in `schema/` and the descriptions in the generated code are
ACP's, under the Apache License 2.0 (`schema/LICENSE`, `schema/NOTICE`).
