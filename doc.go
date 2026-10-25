// Package acp speaks the Agent Client Protocol (ACP), version 1: the
// JSON-RPC protocol between a coding agent and the client (an editor, a
// chat app) that runs it.
//
// The types are generated from ACP's JSON schema, release [SchemaVersion],
// with its descriptions as their docs. This package has the stable
// protocol only; [github.com/afrase/acp-go/unstable] is the same API with
// the parts the schema still marks unstable (session/fork, a turn's usage,
// providers and more).
//
// # A client
//
// A client runs an agent with [Start] (or speaks to one over any streams
// with [NewAgentConn]) and calls it through the [AgentConn]: Initialize,
// NewSession, Prompt. What the agent sends back (session/update
// notifications, permission requests, file reads) goes to the [Client]
// its factory returns, called with the connection so the client can keep
// it; the Client embeds [UnimplementedClient] for what it doesn't do.
//
// # An agent
//
// An agent implements [Agent], embedding [UnimplementedAgent], and serves
// it on its stdin and stdout with [NewClientConn]; it talks back to the
// client (SessionUpdate, RequestPermission) through the [ClientConn].
//
// # How messages flow
//
// Nothing is queued and nothing is dropped. Notifications (session/update)
// are handled one at a time, in order, on the connection's reading
// goroutine; while one is handled nothing more is read, so a slow handler
// slows the other side's writing down rather than piling messages up. A
// response is delivered once every notification that came before it was
// handled: when Prompt returns, every update of the turn has been seen.
// Requests from the other side are each handled on a goroutine of their
// own, so they may wait (for a user's answer) and may call back. A
// notification handler must not call the other side and wait (the answer
// can't be read until it returns); such a call fails at once.
//
// Contexts reach every call and handler. A call whose context ends
// returns at once and withdraws its request ($/cancel_request); a request
// the other side withdraws has its handler's context end, and a handler
// that then returns the context's error is answered "Request cancelled".
// A handler's panic is recovered, logged ([WithLogger]) and answered as an
// internal error.
//
// # Errors
//
// An error the other side answers with is an [*Error], with its JSON-RPC
// code ([ErrorCode]); errors.As finds it. Once a connection has ended,
// calls fail with an error that errors.Is finds [ErrClosed] in.
//
// # Extensions
//
// Every type has its _meta (Meta), and methods outside ACP (named with a
// leading "_") go to ExtMethod and ExtNotification, and out through Call
// and Notify. [github.com/afrase/acp-go/steering] is one such extension.
package acp

//go:generate go run ./internal/acpgen -schema schema/schema.json -meta schema/meta.json -pkg acp -out .

// LatestProtocolVersion is the version of ACP this package speaks, for
// InitializeRequest.ProtocolVersion; an agent answers with the version it
// speaks.
const LatestProtocolVersion ProtocolVersion = 1
