# Changelog

This module follows semantic versioning; see the README's Versioning
section for what that covers. Each release names the release of ACP's
schema it follows.

## Unreleased

- Tests: every notification is checked to reach the other side's method of
  its name, through a `RecordingAgent` and `RecordingClient` that acpgen
  writes for the tests (`methods_gen_test.go`).
- Fuzzing: `FuzzStream` fails on a handler panic (the connection recovers
  it, so it used to pass), and `unstable` is fuzzed too: `make fuzz` runs
  its targets, its copies read the root package's corpus, and
  `FuzzDecodeUnstable` covers its own types. `make cover` covers it.
- **Behaviour change:** required properties are checked at every level of
  every message read: requests, notifications, responses, and the
  objects, list items and union variants inside them. A property that is
  null where the schema doesn't allow it counts as missing. A request
  without them is answered Invalid params, a notification is dropped
  (logged), and a response is its call's error. Before, only a request's
  top-level properties were checked, and only for presence. A union in a
  list that is null, not an object, is refused too, rather than kept as
  `Other`. A JSON-RPC error object is not checked.
- `unstable`: `IdleStateUpdate.None`, the idle state without a stop reason.
  It can now be sent (`StateUpdate{Idle: &IdleStateUpdate{None: true}}`),
  and an absent, null or malformed `stopReason` decodes as it, as the
  schema says, instead of as `Other` or an error.
- `unstable`: an idle state with an unknown stop reason is passed on with
  its `"state"` key once; it was written twice.
- A union's variant without a tag (an MCP server's `Stdio`, an auth
  method's `Agent`, ...) also decodes when the tag is its title, such as
  `"type":"stdio"`, and is always sent without a tag.
- `unstable`: an `InitialSessionConfigOption` of an unknown type with a
  string value decodes as `ValueID`, as the schema says (acpgen now reads
  this from the schema, as it does for `SetSessionConfigOptionRequest`).
- **Breaking (`unstable`):** every field whose null the schema says clears
  it is a `Nullable`. acpgen now reads this from the schema's
  descriptions instead of a list kept by hand, which had fallen behind:
  `SessionMessage`'s `SenderSessionID`, `RecipientSessionID`,
  `SenderMessageID`, `RecipientMessageID` and `Meta`, and
  `SubagentUpdate`'s `Cwd`, `AdditionalDirectories` and `Meta` are new.
- `unstable`: a `SessionMessage`'s content drops bad items, as the schema
  says, rather than the whole list.
- **Breaking (`unstable`):** `MessageMCPRequest.Params` and
  `MessageMCPNotification.Params` are `json.RawMessage`, not
  `map[string]any`, as the response's `Result` already was: the inner MCP
  message is passed on as it came, big integers included.
- An optional list that is empty but not nil is sent as `[]` (`omitzero`),
  so an update such as `ToolCallUpdate` can replace its content or
  locations with nothing, and a decoded `[]` is passed on. A nil list is
  still left out.
- `<`, `>` and `&` are sent as they are at every depth. Required lists,
  untagged and value unions and `Nullable` were encoded with
  `json.Marshal`, which escaped them.
- `Agent.ExtMethod` and `Agent.ExtNotification` are documented as taking
  the client's messages; they had the schema's text, which says the agent
  sends them.
- CI checks that `schema/` is the pinned release (`make pinned`, in
  `make check` too).

## v0.3.0 (2026-11-14)

ACP schema 1.31.0 (protocol version 1).

- The client system prompt is stable: `SystemPrompt` (append or override) on
  new, load and resume requests (and fork's, in `unstable`), and
  `SessionSystemPromptCapabilities`.
- `unstable`: `ArchiveSession` and `UnarchiveSession`, `SessionStatus`,
  `SetSessionTitle`, `SessionOptions` with initial config option values,
  `AuthStatus` and the `AuthStatusUpdate` notification, the proxy's
  initialize and successor messages, and `AuthAccount`'s description.
- **Breaking (`unstable`):** `SessionMessageChunk` no longer has
  `SenderSessionID` or `RecipientSessionID`; they stay on `SessionMessage`.
- Doc links point at the protocol's v1 pages.

## v0.2.0 (2026-10-25)

ACP schema 1.26.0 (protocol version 1).

- **Breaking:** `NewAgentConn` and `Start` (and `unstable`'s) take a factory,
  `func(*AgentConn) Client`, rather than a `Client`, as `NewClientConn`
  already did and as the TypeScript SDK's `ClientSideConnection` does. It is
  called with the connection before anything is read, so a client can keep
  it without a race. To migrate, wrap the client:
  `acp.Start(cmd, func(*acp.AgentConn) acp.Client { return client })`.

## v0.1.0 (2026-10-25)

ACP schema 1.26.0 (protocol version 1).

The first release: Nushi's ACP client, grown into a library.

- Every type and method of the schema, generated with its descriptions as
  docs; the unstable parts in package `unstable`, with the same API.
- Both sides: `Start` and `NewAgentConn` for a client, `NewClientConn` for
  an agent, each with typed calls and an interface to implement (`Client`,
  `Agent`) and its `Unimplemented` type to embed.
- No queues and no size cap by default: back-pressure, notifications in
  order, requests concurrent; `$/cancel_request` both ways; handler panics
  answered as internal errors; connections that end without leaving
  goroutines.
- Lenient decoding where the schema asks for it; unions that keep a variant
  they don't know; `Nullable` for fields whose null means something.
- Extensions through `_meta`, `ExtMethod`/`ExtNotification` and
  `Call`/`Notify`; the `steering` package for `_session/steering`.
- Checked against the TypeScript SDK 2.0.2 and ACP's conformance kit;
  fuzzed, with its corpus; benchmarked.
