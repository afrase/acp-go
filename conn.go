package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime/debug"
	"strconv"
	"sync"
	"time"
)

// handler is what a connection does with the other side's messages: the
// generated dispatch to an Agent or a Client.
type handler struct {
	request func(ctx context.Context, method string, params json.RawMessage) (any, error)
	notify  func(ctx context.Context, method string, params json.RawMessage) error
}

// conn is a JSON-RPC 2.0 connection over newline-delimited JSON, ACP's
// stdio transport. Nothing is queued: the other side's notifications are
// handled one at a time on the reading goroutine, so a slow handler slows
// the reading, and the other side's writes, down (back-pressure); its
// requests are each handled on a goroutine of their own, after every
// notification that came before them, and that goroutine has started
// before anything after the request is handled; a response is delivered
// once every notification before it was handled. Messages are written one
// at a time by the writing goroutine; a caller waits while the other side
// doesn't read, until its context ends.
type conn struct {
	r     io.Reader
	w     io.Writer
	close func(cause error) error // closes r and w (for a process, ends it); cause is nil for Close
	h     handler
	opts  options
	log   *slog.Logger

	ctx        context.Context //nolint:containedctx // the connection's lifetime: handlers' contexts derive from it
	cancel     context.CancelFunc
	writes     chan writeReq // to the writing goroutine
	writerDone chan struct{}
	handedOff  chan struct{} // a request's goroutine has started (serve waits for it)

	mu       sync.Mutex
	nextID   int64
	pending  map[int64]chan response       // our requests waiting, by id
	incoming map[string]context.CancelFunc // the other side's requests being handled, by idKey
	err      error                         // why it ended; set once

	handlers sync.WaitGroup // request handlers and withdrawals running
	done     chan struct{}  // closed once the reading, the handlers and wait have ended
	wait     func()         // run after the reading ends, before done closes (a process's Wait)
	junk     int            // lines that weren't JSON-RPC (on the reading goroutine)
	seq      uint64         // lines read (on the reading goroutine): a message's place in the order
}

type response struct {
	result json.RawMessage
	err    *Error
}

// newConn makes a connection, which start sets going.
func newConn(r io.Reader, w io.Writer, closeFn func(cause error) error, opts options) *conn {
	c := &conn{
		r: r, w: w, close: closeFn, opts: opts, log: opts.logger(),
		writes: make(chan writeReq), writerDone: make(chan struct{}), handedOff: make(chan struct{}),
		pending: map[int64]chan response{}, incoming: map[string]context.CancelFunc{}, done: make(chan struct{}),
	}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	return c
}

// start reads from r until it ends, on a goroutine of its own.
func (c *conn) start(h handler) {
	c.h = h
	go c.writer()
	go c.read()
}

func (c *conn) read() {
	br := bufio.NewReaderSize(c.r, 64<<10)
	var err error
	for {
		var line []byte
		line, err = c.readLine(br)
		if len(line) > 0 {
			c.dispatch(line)
		}
		if err != nil {
			break
		}
	}
	if c.junk > maxJunkLogged {
		c.log.Warn("acp: lines that weren't JSON-RPC", "lines", c.junk)
	}
	c.fail(err)
	c.handlers.Wait()
	<-c.writerDone
	if c.wait != nil {
		c.wait()
	}
	close(c.done)
}

// readLine reads one line, of any length unless a read limit is set.
func (c *conn) readLine(br *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if limit := c.opts.readLimit; limit > 0 && len(line)+len(bytes.TrimRight(chunk, "\r\n")) > limit {
			return nil, fmt.Errorf("%w: a message over the read limit of %d bytes", ErrTooLarge, limit)
		}
		if line == nil && err == nil {
			return append([]byte(nil), chunk...), nil // the usual case: the line fit the buffer
		}
		line = append(line, chunk...)
		if !errors.Is(err, bufio.ErrBufferFull) {
			return line, err
		}
	}
}

// maxJunkLogged is how many lines that aren't JSON-RPC are logged one by
// one; the rest are counted when the connection ends.
const maxJunkLogged = 5

// frame is a JSON-RPC message as read.
type frame struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *Error          `json:"error"`
}

func (c *conn) dispatch(line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return
	}
	c.seq++
	var f frame
	if line[0] != '{' || json.Unmarshal(line, &f) != nil {
		c.junkLine(line)
		return
	}
	switch {
	case f.Method != "" && f.ID != nil:
		c.serve(f)
	case f.Method == "$/cancel_request":
		c.withdrawn(f.Params)
	case f.Method != "":
		c.notified(f)
	case f.ID != nil:
		c.answered(f)
	default:
		c.reply(&Error{Code: ErrorCodeInvalidRequest, Message: "Invalid request: neither a method nor an id"})
	}
}

// junkLine answers a line that isn't a JSON-RPC message as JSON-RPC says
// to (with a null id), and logs its size, not what it holds: agents
// sometimes print logs to stdout, which may carry anything.
func (c *conn) junkLine(line []byte) {
	c.junk++
	if c.junk <= maxJunkLogged {
		c.log.Warn("acp: a line that isn't JSON-RPC", "bytes", len(line))
	}
	switch {
	case line[0] == '[' && json.Valid(line):
		c.reply(&Error{Code: ErrorCodeInvalidRequest, Message: "Invalid request: ACP v1 has no batches"})
	case json.Valid(line):
		c.reply(&Error{Code: ErrorCodeInvalidRequest, Message: "Invalid request"})
	default:
		c.reply(&Error{Code: ErrorCodeParseError, Message: "Parse error"})
	}
}

// reply sends an error for a message that can't be answered by a handler
// (one with a bad shape), on a goroutine of its own: the reading goroutine
// mustn't wait for the other side to read.
func (c *conn) reply(e *Error) {
	c.handlers.Go(func() {
		_, _ = c.write(c.ctx, encodeError(nil, e)) // the connection may be ending
	})
}

// idKey is a request id as a map key: its string, or its number's
// canonical form ("" for an id that is neither).
func idKey(id json.RawMessage) string {
	switch jsonKind(id) {
	case "string":
		var s string
		if json.Unmarshal(id, &s) == nil {
			return "s" + s
		}
	case "number":
		var n json.Number
		if json.Unmarshal(id, &n) == nil {
			if i, err := n.Int64(); err == nil {
				return "n" + strconv.FormatInt(i, 10)
			}
			return "n" + n.String()
		}
	case "null":
		return "null"
	}
	return ""
}

// serve handles one of the other side's requests on a goroutine of its own,
// and returns once that goroutine has started: what was read after the
// request (a session/cancel after a session/prompt) is handled only then,
// as the TypeScript SDK runs a handler up to its first await before it
// reads on.
//
//nolint:contextcheck // the response is written with the connection's context: it answers a withdrawn request too
func (c *conn) serve(f frame) {
	key := idKey(f.ID)
	if key == "" {
		c.reply(&Error{Code: ErrorCodeInvalidRequest, Message: "Invalid request: a bad id"})
		return
	}
	ctx, cancel := context.WithCancel(context.WithValue(context.WithValue(c.ctx, requestIDKey{}, f.ID), seqKey{}, c.seq))
	c.mu.Lock()
	c.incoming[key] = cancel
	c.mu.Unlock()
	c.handlers.Go(func() {
		c.handedOff <- struct{}{}
		defer cancel()
		start := time.Now()
		result, err := c.safeRequest(ctx, f.Method, f.Params)
		c.mu.Lock()
		delete(c.incoming, key)
		c.mu.Unlock()
		var msg []byte
		if err == nil {
			if msg, err = encodeResult(f.ID, result); err != nil {
				err = fmt.Errorf("encoding the result: %w", err)
			}
		}
		if err != nil {
			msg = encodeError(f.ID, errorFor(ctx, err))
		}
		c.logDone(ctx, "acp: request handled", f.Method, start, err)
		if _, werr := c.write(c.ctx, msg); werr != nil && !errors.Is(werr, ErrClosed) {
			c.log.Warn("acp: a response wasn't sent", "rpc", f.Method, "err", werr)
		}
	})
	<-c.handedOff
}

// withdrawn ends the context of a request the other side withdrew
// ($/cancel_request).
func (c *conn) withdrawn(params json.RawMessage) {
	var p CancelRequestNotification
	if json.Unmarshal(params, &p) != nil {
		return
	}
	c.mu.Lock()
	cancel := c.incoming[idKey(p.RequestID)]
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// notified handles one of the other side's notifications, on the reading
// goroutine: notifications are handled in order, and nothing more is read
// until the handler returns.
//
//nolint:contextcheck // its handler's context is the connection's
func (c *conn) notified(f frame) {
	err := c.safeNotify(&notifyCtx{Context: c.ctx, c: c, seq: c.seq}, f.Method, f.Params)
	switch {
	case errors.Is(err, errUnknownNotification):
		c.log.Debug("acp: notification ignored", "rpc", f.Method)
	case err != nil:
		c.log.Warn("acp: notification failed", "rpc", f.Method, "err", err)
	case f.Method != "session/update": // a turn sends one a chunk
		c.log.Debug("acp: notification handled", "rpc", f.Method)
	}
}

// notifyCtx is a notification handler's context: the connection's, marked
// as a notification handler's, with the notification's sequence. It is
// one allocation, not two context.WithValues: a turn sends an update a
// chunk.
type notifyCtx struct {
	context.Context //nolint:containedctx // the connection's
	c               *conn
	seq             uint64
}

func (n *notifyCtx) Value(key any) any {
	switch key.(type) {
	case notificationKey:
		return n.c
	case seqKey:
		return n.seq
	}
	return n.Context.Value(key)
}

// answered delivers a response to the call waiting for it.
func (c *conn) answered(f frame) {
	id, err := strconv.ParseInt(string(f.ID), 10, 64)
	c.mu.Lock()
	ch := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if err != nil || ch == nil {
		c.log.Debug("acp: a response to no request of ours", "bytes", len(f.ID))
		return
	}
	ch <- response{result: f.Result, err: f.Error}
}

// safeRequest runs the request handler, turning a panic into an error: one
// bad message can't take the process down. The panic is logged with its
// stack.
func (c *conn) safeRequest(ctx context.Context, method string, params json.RawMessage) (result any, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = c.panicked(method, p)
		}
	}()
	return c.h.request(ctx, method, params)
}

func (c *conn) safeNotify(ctx context.Context, method string, params json.RawMessage) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = c.panicked(method, p)
		}
	}()
	return c.h.notify(ctx, method, params)
}

// panicked logs a handler's panic, with its stack, and returns it as an
// error.
func (c *conn) panicked(method string, p any) error {
	c.log.Error("acp: handler panicked", "rpc", method, "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
	return fmt.Errorf("the handler for %s panicked: %v", method, p)
}

// call sends a request and decodes its result into result (unless nil).
func (c *conn) call(ctx context.Context, method string, params, result any) error {
	wait, err := c.send(ctx, method, params)
	if err != nil {
		return err
	}
	return wait(ctx, result)
}

// send writes a request and returns once it is written; wait waits for its
// response. If wait's context ends first, the request is withdrawn
// ($/cancel_request) and wait returns the context's error.
func (c *conn) send(ctx context.Context, method string, params any) (wait func(context.Context, any) error, err error) {
	if ctx.Value(notificationKey{}) == c {
		return nil, fmt.Errorf("%s: %w", method, errInNotification)
	}
	p, err := encodeValue(params)
	if err != nil {
		return nil, fmt.Errorf("%s: encoding the params: %w", method, err)
	}
	ch := make(chan response, 1)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("%s: %w", method, c.err)
	}
	c.nextID++
	id := c.nextID
	c.pending[id] = ch
	c.mu.Unlock()
	start := time.Now()
	if handed, err := c.write(ctx, encodeRequest(id, method, p)); err != nil {
		if c.forget(id) && handed {
			c.withdraw(id) // it may yet be written
		}
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	return func(ctx context.Context, result any) error {
		err := c.await(ctx, method, id, ch, result)
		c.logDone(ctx, "acp: call", method, start, err)
		return err
	}, nil
}

func (c *conn) await(ctx context.Context, method string, id int64, ch chan response, result any) error {
	select {
	case r, ok := <-ch:
		switch {
		case !ok:
			return fmt.Errorf("%s: %w", method, c.Err())
		case r.err != nil:
			return fmt.Errorf("%s: %w", method, r.err)
		case result == nil || len(r.result) == 0 || bytes.Equal(r.result, []byte("null")):
			return nil
		}
		if err := json.Unmarshal(r.result, result); err != nil {
			return fmt.Errorf("%s: decoding the result: %w", method, err)
		}
		return nil
	case <-ctx.Done():
		if c.forget(id) {
			c.withdraw(id)
		}
		return fmt.Errorf("%s: %w", method, ctx.Err())
	}
}

// withdraw tells the other side we stopped waiting for a request
// ($/cancel_request), on a goroutine of its own: the caller has given up
// and mustn't wait for the write.
func (c *conn) withdraw(id int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return // the connection ended, which withdrew it; and its handlers may be being waited for
	}
	c.handlers.Go(func() {
		msg := encodeNotification("$/cancel_request", []byte(`{"requestId":`+strconv.FormatInt(id, 10)+`}`))
		_, _ = c.write(c.ctx, msg) // the connection may be ending, which withdraws it anyway
	})
}

// forget drops a pending call, reporting whether it was still pending.
func (c *conn) forget(id int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.pending[id]
	delete(c.pending, id)
	return ok
}

func (c *conn) notify(ctx context.Context, method string, params any) error {
	p, err := encodeValue(params)
	if err != nil {
		return fmt.Errorf("%s: encoding the params: %w", method, err)
	}
	if _, err := c.write(ctx, encodeNotification(method, p)); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	return nil
}

// write has one message written, after the ones before it, unless ctx
// ends first. A message is written by the writing goroutine, so a caller
// whose ctx ends while the other side doesn't read returns at once; handed
// reports whether the message had been handed over by then (it may still
// be written). A failed write ends the connection: the other side can't
// read.
func (c *conn) write(ctx context.Context, msg []byte) (handed bool, err error) {
	if limit := c.opts.writeLimit; limit > 0 && len(msg)-1 > limit {
		return false, fmt.Errorf("%w: %d bytes, over the write limit of %d", ErrTooLarge, len(msg)-1, limit)
	}
	w := writeReq{msg: msg, done: doneChans.Get().(chan error)} //nolint:forcetypeassert // the pool holds channels
	select {
	case c.writes <- w:
	case <-ctx.Done():
		doneChans.Put(w.done)
		return false, ctx.Err()
	case <-c.ctx.Done():
		doneChans.Put(w.done)
		return false, c.Err()
	}
	select {
	case err := <-w.done:
		doneChans.Put(w.done) // empty again
		return true, err
	case <-ctx.Done():
		return true, ctx.Err()
	case <-c.ctx.Done(): // a write stuck on a writer that can't be closed
		return true, c.Err()
	}
}

type writeReq struct {
	msg  []byte     // nil: close w's writing side (Shutdown)
	done chan error // buffered: the writer never waits on it
}

// errInputClosed is what a write after closeInput returns: the other side
// can't read it, but the connection goes on until it closes its end.
var errInputClosed = &closedError{cause: errors.New("the other side's input was closed (Shutdown)")}

// closeInput closes w's writing side once the messages handed over before
// are written, never in the middle of one: CloseWrite when w has it (a
// socket, whose reading then goes on), else Close. Later writes fail with
// errInputClosed and leave the connection up.
func (c *conn) closeInput(ctx context.Context) error {
	_, err := c.write(ctx, nil)
	return err
}

// doneChans are writeReq.done channels to reuse; one whose write a caller
// stopped waiting for isn't put back.
var doneChans = sync.Pool{New: func() any { return make(chan error, 1) }}

// writer writes the messages handed to it, one at a time, until the
// connection ends.
func (c *conn) writer() {
	defer close(c.writerDone)
	inputClosed := false
	for {
		select {
		case w := <-c.writes:
			switch {
			case inputClosed:
				w.done <- errInputClosed
				continue
			case w.msg == nil:
				inputClosed = true
				w.done <- closeWrite(c.w)
				continue
			}
			if _, err := c.w.Write(w.msg); err != nil {
				c.fail(fmt.Errorf("writing: %w", err))
				w.done <- c.Err()
				return
			}
			w.done <- nil
		case <-c.ctx.Done():
			return
		}
	}
}

// closeWrite closes w's writing side: CloseWrite when it has it, else
// Close, if it is an io.Closer.
func closeWrite(w io.Writer) error {
	switch w := w.(type) {
	case interface{ CloseWrite() error }:
		return w.CloseWrite()
	case io.Closer:
		return w.Close()
	}
	return nil
}

// fail ends the connection with cause, once: calls waiting fail, handlers'
// contexts end, and r and w are closed.
func (c *conn) fail(cause error) {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return
	}
	c.err = &closedError{cause: cause}
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
	c.cancel()
	if c.close != nil {
		_ = c.close(cause) // unblocks the reading; its error says nothing the cause doesn't
	}
}

// Err is why the connection ended, or nil while it hasn't.
func (c *conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// logDone logs a call or a handled request once it ended, at DEBUG, with
// how long it took and its error: never what it carried.
func (c *conn) logDone(ctx context.Context, what, method string, start time.Time, err error) {
	if !c.log.Enabled(ctx, slog.LevelDebug) {
		return
	}
	args := []any{"rpc", method, "took", time.Since(start).Round(time.Millisecond)}
	if err != nil {
		args = append(args, "err", err)
	}
	c.log.DebugContext(ctx, what, args...)
}

type (
	requestIDKey    struct{}
	notificationKey struct{}
	seqKey          struct{}
)

// RequestIDFromContext returns the JSON-RPC id of the request whose handler
// got ctx: an elicitation tied to a request outside a session (during
// authenticate) names it ([ElicitationRequestScope]).
func RequestIDFromContext(ctx context.Context) (RequestID, bool) {
	id, ok := ctx.Value(requestIDKey{}).(RequestID)
	return id, ok
}

// SequenceFromContext returns where the message whose handler got ctx
// came among the other side's messages: one read later has a larger
// number.
//
// It orders a request and a notification whose handlers run side by
// side. An agent's Cancel can run while the Prompt before it is still
// setting up, before it has registered its turn. So the agent keeps the
// last Cancel's sequence, and a Prompt that registers its turn with a
// smaller sequence than that is cancelled already.
func SequenceFromContext(ctx context.Context) (uint64, bool) {
	n, ok := ctx.Value(seqKey{}).(uint64)
	return n, ok
}
