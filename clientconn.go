package acp

import (
	"context"
	"io"
)

// ClientConn is an agent's connection to its client: its methods call the
// client (SessionUpdate, RequestPermission, ReadTextFile, ...), and the
// client's requests and notifications go to the [Agent] it was made with.
//
// Its methods may be called from any goroutine. Each waits until its
// message is written (while the client doesn't read, or until ctx ends),
// then for the answer. When ctx ends first, the call returns ctx's error
// and the request is withdrawn with $/cancel_request: a question the
// agent no longer needs answered goes away.
type ClientConn struct {
	c *conn
}

// NewClientConn speaks ACP to a client over r (what the client writes) and
// w (what it reads): an agent's stdin and stdout. It calls newAgent with
// the connection, before reading anything, so the agent can keep it to
// talk to the client; the client's messages go to the agent it returns.
// Close closes r and w, those that are io.Closers.
func NewClientConn(r io.Reader, w io.Writer, newAgent func(*ClientConn) Agent, opts ...Option) *ClientConn {
	c := &ClientConn{c: newConn(r, w, closeBoth(r, w), newOptions(opts))}
	c.c.start(agentHandler(newAgent(c)))
	return c
}

// Call sends the client a request for a method outside ACP, an extension's
// (its name starts with "_"), and decodes its result into result (unless
// nil). params is encoded as JSON.
func (c *ClientConn) Call(ctx context.Context, method string, params, result any) error {
	return c.c.call(ctx, method, params, result)
}

// Notify sends the client an extension's notification.
func (c *ClientConn) Notify(ctx context.Context, method string, params any) error {
	return c.c.notify(ctx, method, params)
}

// Done is closed once the connection has ended (the client closed its end,
// or Close was called) and every handler it ran has returned.
func (c *ClientConn) Done() <-chan struct{} { return c.c.done }

// Err is why the connection ended, which errors.Is finds [ErrClosed] in,
// or nil while it hasn't.
func (c *ClientConn) Err() error { return c.c.Err() }

// Close ends the connection now: calls waiting fail, handlers' contexts
// end, and the streams are closed. It doesn't wait: Done says when
// everything has ended.
func (c *ClientConn) Close() error {
	c.c.fail(nil)
	return nil
}
