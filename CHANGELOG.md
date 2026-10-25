# Changelog

This module follows semantic versioning; see the README's Versioning
section for what that covers. Each release names the release of ACP's
schema it follows.

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
