package baseproc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
)

// errConnClosed is returned by calls racing a closed (or failed) connection.
var errConnClosed = errors.New("baseproc: connection closed")

// conn is the client-side half of the framed channel. It serialises writes,
// correlates responses to pending calls by numeric id, and demultiplexes
// server notifications (decision D3: progress chunks ride notifications while
// the RPC response only fires on completion).
//
// A conn is safe for concurrent calls — matrix C1 has two sessions calling
// tools in parallel over one shared subprocess, so per-call goroutines are the
// norm, not the exception.
type conn struct {
	rw io.ReadWriteCloser

	writeMu sync.Mutex // guards frame writes (interleaved calls + server pushes)

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan *Frame
	closed  bool
	err     error

	closeOnce sync.Once
	done      chan struct{} // closed when the read loop exits

	onNotify func(method string, params json.RawMessage)
}

// newConn starts the read loop. onNotify (may be nil) receives every
// server→client notification.
func newConn(rw io.ReadWriteCloser, onNotify func(method string, params json.RawMessage)) *conn {
	c := &conn{
		rw:       rw,
		pending:  make(map[int64]chan *Frame),
		done:     make(chan struct{}),
		onNotify: onNotify,
	}
	go c.readLoop()
	return c
}

// call sends one request and waits for the matching response. When result is
// non-nil the response payload is unmarshalled into it. A peer error response
// surfaces as *RPCError; a dead connection as a wrapped errConnClosed or the
// underlying read error.
func (c *conn) call(ctx context.Context, method string, params, result any) error {
	if err := c.deadErr(); err != nil {
		return err
	}
	var rawParams json.RawMessage
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("baseproc: marshal %s params: %w", method, err)
		}
		rawParams = encoded
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errConnClosed
	}
	c.nextID++
	id := c.nextID
	ch := make(chan *Frame, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	frame, err := json.Marshal(Frame{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(strconv.AppendInt(nil, id, 10)),
		Method:  method,
		Params:  rawParams,
	})
	if err != nil {
		c.unregister(id)
		return fmt.Errorf("baseproc: marshal %s frame: %w", method, err)
	}
	if err := c.writeFrame(frame); err != nil {
		c.unregister(id)
		// A write failure kills the channel's framing guarantees; tear down so
		// every other pending call fails fast instead of hanging.
		c.close()
		return fmt.Errorf("baseproc: write %s request: %w", method, err)
	}

	select {
	case f := <-ch:
		if f == nil { // failed by finish()
			return c.deadErr()
		}
		if f.Error != nil {
			return f.Error
		}
		if result != nil {
			if len(f.Result) == 0 {
				return fmt.Errorf("baseproc: %s response carries no result", method)
			}
			if err := json.Unmarshal(f.Result, result); err != nil {
				return fmt.Errorf("baseproc: decode %s result: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		c.unregister(id)
		return ctx.Err()
	case <-c.done:
		return c.deadErr()
	}
}

// close shuts the channel down: the underlying transport closes (which
// unblocks the read loop) and every pending call fails with errConnClosed.
func (c *conn) close() error {
	var closeErr error
	c.closeOnce.Do(func() {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return
		}
		c.closed = true
		c.mu.Unlock()
		closeErr = c.rw.Close()
		<-c.done // readLoop finishes pending calls on exit
	})
	return closeErr
}

func (c *conn) writeFrame(encoded []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return WriteFrame(c.rw, encoded)
}

func (c *conn) unregister(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *conn) deadErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return fmt.Errorf("baseproc: connection dead: %w", c.err)
	}
	if c.closed {
		return errConnClosed
	}
	return nil
}

// finish marks the connection dead and fails every pending call.
func (c *conn) finish(err error) {
	c.mu.Lock()
	if c.closed && c.err == nil {
		c.err = errConnClosed
	} else if c.err == nil && err != nil {
		c.err = err
	}
	pending := c.pending
	c.pending = make(map[int64]chan *Frame)
	c.closed = true
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- nil // non-blocking: buffer 1, each channel delivered at most once
	}
	close(c.done)
}

func (c *conn) readLoop() {
	for {
		payload, err := ReadFrame(c.rw)
		if err != nil {
			if errors.Is(err, io.EOF) {
				c.finish(nil)
			} else {
				c.finish(err)
			}
			return
		}
		var f Frame
		if uerr := json.Unmarshal(payload, &f); uerr != nil {
			// A malformed frame from our own peer is a protocol breach; the
			// stream's framing is still intact but the peer is misbehaving —
			// fail the connection rather than guess.
			c.finish(fmt.Errorf("baseproc: malformed frame from peer: %w", uerr))
			return
		}
		switch f.Kind() {
		case FrameNotification:
			if c.onNotify != nil {
				c.onNotify(f.Method, f.Params)
			}
		case FrameRequest:
			// The v1 server never issues requests toward the client, but the
			// contract discipline is symmetric: answer unknown server methods
			// with -32601 instead of dropping the connection.
			c.writeRejectedServerRequest(f.ID)
		case FrameResponse:
			c.deliver(&f)
		default:
			// Ignore unclassifiable frames; never let one peer quirk kill a
			// healthy channel.
		}
	}
}

func (c *conn) deliver(f *Frame) {
	id, err := strconv.ParseInt(string(f.ID), 10, 64)
	if err != nil {
		return // not our id space; drop
	}
	c.mu.Lock()
	ch, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if ok {
		ch <- f
	}
}

func (c *conn) writeRejectedServerRequest(id json.RawMessage) {
	encoded, err := json.Marshal(Frame{JSONRPC: JSONRPCVersion, ID: id, Error: &RPCError{
		Code:    CodeMethodNotFound,
		Message: "base client does not accept server-to-client requests in v1",
	}})
	if err != nil {
		return
	}
	_ = c.writeFrame(encoded)
}
