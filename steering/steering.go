// Package steering is the steering extension of ACP, which
// claude-agent-acp, codex-acp and other agents have: a message for a
// session's running turn, taken in while the turn runs instead of
// waiting to be a prompt of its own.
//
// It isn't part of ACP. An agent advertises it in its initialize
// response's _meta (steering.supported), and takes the _session/steering
// request: a session and a prompt, answered "injected" when a turn was
// running, and otherwise either "promptRequired" (the client sends the
// message as a prompt itself) or "startedNewTurn", as the request's
// _meta.steering.idleBehavior asks.
//
// It works with either package, [github.com/afrase/acp-go] or
// [github.com/afrase/acp-go/unstable]: B is that package's ContentBlock.
package steering

import "context"

// Method is the request's method.
const Method = "_session/steering"

// Outcome is what became of a steered message.
type Outcome string

// The outcomes.
const (
	// Injected: a turn was running and took the message in.
	Injected Outcome = "injected"
	// PromptRequired: no turn was running, and the request asked that the
	// client send the message as a prompt then; the agent did nothing.
	PromptRequired Outcome = "promptRequired"
	// StartedNewTurn: no turn was running, so the agent started one with
	// the message (what an agent does when the request doesn't say).
	StartedNewTurn Outcome = "startedNewTurn"
)

// Request is the _session/steering request. B is the ContentBlock of the
// acp package in use.
type Request[B any] struct {
	SessionID string         `json:"sessionId"`
	Prompt    []B            `json:"prompt"`
	Meta      map[string]any `json:"_meta,omitempty"`
}

// PromptRequired reports whether the request asks for [PromptRequired]
// when no turn runs (idleBehavior "promptRequired").
func (r *Request[B]) PromptRequired() bool {
	s, _ := r.Meta["steering"].(map[string]any)
	return s["idleBehavior"] == "promptRequired"
}

// Response is the request's answer.
type Response struct {
	Outcome Outcome `json:"outcome"`
	// Reason says why, for an outcome other than Injected
	// ("noRunningTurn").
	Reason string `json:"reason,omitempty"`
}

// Supported reports whether an agent's initialize response advertises
// steering; meta is its Meta.
func Supported(meta map[string]any) bool {
	s, _ := meta["steering"].(map[string]any)
	ok, _ := s["supported"].(bool)
	return ok
}

// Advertise adds steering to meta, an agent's initialize response's Meta
// (nil makes one), and returns it.
func Advertise(meta map[string]any) map[string]any {
	if meta == nil {
		meta = map[string]any{}
	}
	meta["steering"] = map[string]any{"supported": true}
	return meta
}

// Caller is a connection to an agent: an *AgentConn of either package.
type Caller interface {
	Call(ctx context.Context, method string, params, result any) error
}

// Steer hands prompt to the session's running turn. It returns
// [PromptRequired] when no turn runs, leaving the message to the caller
// to send as a prompt: the agent then does nothing.
func Steer[B any](ctx context.Context, agent Caller, sessionID string, prompt []B) (Outcome, error) {
	var resp Response
	err := agent.Call(ctx, Method, Request[B]{
		SessionID: sessionID,
		Prompt:    prompt,
		Meta:      map[string]any{"steering": map[string]any{"idleBehavior": string(PromptRequired)}},
	}, &resp)
	return resp.Outcome, err
}
