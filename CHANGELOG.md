# Changelog

This module follows semantic versioning; see the README's Versioning
section for what that covers. Each release names the release of ACP's
schema it follows.

## v0.4.2 (2026-11-21)

ACP schema 1.31.0 (protocol version 1). Internal only: no API or
behaviour change.

- The generated code is about 1,400 lines smaller: each request a side
  handles and each call a connection makes goes through one generic
  helper, and a lenient `UnmarshalJSON` has one return.
- The tests of the connection, the processes and the README run once, in
  the root package; `unstable` keeps the tests of what its schema changes
  (the types, the methods' routing and their params, the examples).
- CI runs the tests shuffled, with a timeout per job.

## v0.4.1 (2026-11-21)

ACP schema 1.31.0 (protocol version 1). Fixes from a review.

- A message over the read limit whose `id` comes after its params or its
  result (as many encoders write it) is taken by its id: its tail is kept
  as well as its head. A request was taken for a notification and never
  answered, and a response was answered with a null-id error while its
  call waited on. A response whose id can't be read is dropped, never
  answered.
- A call or a notification whose context has already ended isn't sent.
  About half of them were.
- **Behaviour change:** a response with neither a result nor an error
  fails its call with an internal error; it was taken for an empty
  result. A null result of an ACP method counts as an object without its
  properties, so one its type requires is missing (an error): a
  `session/prompt` answered with null returned a `PromptResponse` with no
  stop reason. An extension's null result (`Call`) is as before.
- A request with the id of one still being handled is answered Invalid
  request and not handled. It took the first one's place, so
  `$/cancel_request` no longer reached the first, and both were answered
  with that id.
- `Start`, on Unix: once the agent has exited, its process group is
  killed once, for what the agent left running; later kills (after the
  output is read, or a `Close`) kill nothing. They were sent to the
  agent's pid as a group's id, which may name another group by then.
- Requests get back-pressure too: while 64 answers to the other side's
  requests wait to be written (it doesn't read them), its next request
  waits, and nothing after it is read. A side that sent requests and
  didn't read piled up a goroutine a request. A handler still running
  doesn't count, so handlers that call the other side and wait can't
  stall the reading.
- Docs: an agent's Cancel can run before the Prompt of a session/prompt
  sent before it (the docs, the README, `StartPrompt` and the v0.4.0
  entry said it ran only once Prompt was called), and `examples/echo`
  orders the two with `SequenceFromContext`.
- The error of a call from a notification handler says the call needs a
  context not derived from the handler's: another goroutine alone isn't
  enough.
- The README's comparison table has its delimiter row: it showed as a
  paragraph of pipes.
- An answer not sent because the connection, or the other side's input,
  was closed is logged at DEBUG ("a response wasn't sent"); it wasn't
  logged. Tests that ordered events with sleeps and short deadlines
  order them with channels.
- The package docs and the README no longer say nothing is dropped: a
  notification that doesn't decode is dropped (logged), as other parts
  of them said.
- Generated docs: "Embed it in an Agent" (it said "a Agent"), and every
  `Meta` field's link goes to ACP's v1 docs, as the others do.

## v0.4.0 (2026-11-14)

ACP schema 1.31.0 (protocol version 1). The fixes from a whole-code review.

- **Breaking:** `Diff.OldText` is a `*string`: absent for a new file, `""` for an empty one, which a string couldn't tell apart.
- A test builds the README's client and agent. The README said
  `Example_client` ran its client, which it doesn't (it has no `Start`).
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
- Docs: a connection's factory must not call the other side (nothing is
  read until it returns); a call from a notification handler fails at
  once only when made with the handler's context; the README's client and
  `examples/prompt` give `Shutdown` five seconds rather than a context
  that never ends.
- A request's goroutine has started before anything read after the
  request is handled. Its handler may not have been called yet, so an
  agent's Cancel can still run before the Prompt of a session/prompt that
  came before it: `SequenceFromContext` orders the two. (This entry first
  said Cancel no longer ran before Prompt was called.)
- `SequenceFromContext`: where a message came among the other side's, so
  an agent can tell a Cancel that came after a Prompt still setting up.
- `Shutdown` closes the agent's input through the writer, after the
  message being written, and with `CloseWrite` on a socket. What is sent
  after it fails with `ErrClosed` but no longer ends the connection, so
  the agent's last updates and results still arrive.
- A response over the write limit is sent as an internal error in its
  place, and a handler's nil `*acp.Error` (in a non-nil error) as an
  internal error rather than a line that isn't JSON: the other side's
  call no longer waits forever.
- Lines that aren't JSON-RPC no longer start a goroutine each while the
  other side doesn't read: at most 8 answers wait, and later lines go
  unanswered (and are counted in the log).
- A message whose id can be read but whose method or error can't (an
  error code that isn't an integer) is taken by its id: a response fails
  the call it answers, with no reply, and a request is answered Invalid
  request with its id. Before, the call waited forever.
- A message over the read limit no longer ends the connection: it is read
  to its end and skipped. A request is answered Invalid request by its
  id, a response fails its call with `ErrTooLarge`, and a notification is
  dropped.
- `Start` on Unix: a command with `Setsid` starts (it was refused with
  EPERM), and when the agent doesn't lead its group (a `Pgid` you set),
  Close kills the agent itself rather than nothing.
- What an agent `Start` ran wrote before it exited is all read, however
  slow the client's handlers are: its stdout is given up on only once a
  read has waited a second with nothing to read, not a second after the
  exit. Its process group is killed as soon as it exits.
- `Start` on Windows: the agent starts a console process group of its own
  (so the console's Ctrl+C reaches the client alone, as on Unix) and runs
  in a job object, so Close kills the processes it started too.
- `Close` ends a connection on a blocking pipe or socket file (an agent's
  stdin and stdout, usually) though the other side is silent or doesn't
  read: on Unix such a file is read and written through a non-blocking
  duplicate. Before, `Done` stayed open until the other side wrote or
  closed its end.

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
