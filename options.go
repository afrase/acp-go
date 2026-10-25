package acp

import "log/slog"

// An Option configures a connection.
type Option func(*options)

type options struct {
	log        *slog.Logger
	readLimit  int
	writeLimit int
}

func newOptions(opts []Option) options {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

func (o options) logger() *slog.Logger {
	if o.log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return o.log
}

// WithLogger has the connection log to l: at DEBUG each call and handled
// request (its method, how long it took, its error) and each notification
// but session/update (a turn sends one a chunk); at WARN lines that aren't
// JSON-RPC (their size only) and failed notifications; at ERROR a
// handler's panic, with its stack. What the messages carry is never
// logged. Without it, the connection logs nothing.
func WithLogger(l *slog.Logger) Option { return func(o *options) { o.log = l } }

// WithReadLimit caps the size of a message read, in bytes: one larger ends
// the connection with [ErrTooLarge]. By default there is no cap.
func WithReadLimit(n int) Option { return func(o *options) { o.readLimit = n } }

// WithWriteLimit caps the size of a message written, in bytes: a call or
// notification larger fails with [ErrTooLarge] and isn't sent, and the
// connection goes on. Set it to what the other side reads (some agents end
// the connection on a larger line). By default there is no cap.
func WithWriteLimit(n int) Option { return func(o *options) { o.writeLimit = n } }
