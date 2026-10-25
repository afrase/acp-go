// Package unstable is package acp with the parts of ACP that its schema
// still marks unstable: session/fork, a turn's token usage, providers,
// MCP over ACP, a client's system prompt, subagents, plan updates and
// session state, and whatever the schema adds to them. It is generated
// from the schema's unstable release ([SchemaVersion]), and its API is
// package acp's, so it is imported in its place:
//
//	import acp "github.com/afrase/acp-go/unstable"
//
// Use one of the two throughout a program: their types are distinct, and
// the stable package's are a subset of these. The unstable parts can
// change in any release of ACP's schema, and so in any minor release of
// this module, without the compatibility the stable package keeps.
//
// See package [github.com/afrase/acp-go] for how connections work.
package unstable

//go:generate go run ../internal/acpgen -schema ../schema/schema.unstable.json -meta ../schema/meta.unstable.json -pkg unstable -out . -copy ..

// LatestProtocolVersion is the version of ACP this package speaks, for
// InitializeRequest.ProtocolVersion; an agent answers with the version it
// speaks.
const LatestProtocolVersion ProtocolVersion = 1
