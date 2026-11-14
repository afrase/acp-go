# Changelog

This module follows semantic versioning; see the README's Versioning
section for what that covers. Each release names the release of ACP's
schema it follows.

## Unreleased

- A request's handler has started before anything read after the request
  is handled: an agent's Cancel no longer runs before the Prompt of a
  session/prompt that came before it was called.
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
