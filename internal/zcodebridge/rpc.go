package zcodebridge

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Wire frame shapes (zcode-protocol/index.ts:286-333, all `.strict()`):
//
//	request      {id, method, params?}        — NO "jsonrpc" key (verified live:
//	              the CLI rejects the JSON-RPC 2.0 envelope with "Unrecognized key")
//	notification {method, params?}
//	response     {id, result}
//	error        {id, error:{code, message, data?}}
//
// ids are string-or-int; this client emits incrementing decimal strings like
// the desktop host does.

// Frozen method names (spec §三).
const (
	methodConnectionFlow = "v4/connection/flow"
	methodCommand        = "v4/command"
	methodSessionList    = "session/list"
	methodSessionEvents  = "session/events"
)

// rpcError is the wire error object.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("zcode rpc %d: %s", e.Code, e.Message)
}

// inbound is one parsed inbound frame we care about.
type inbound struct {
	ID        string // "" when absent (notification)
	HasID     bool
	Method    string
	RawResult json.RawMessage // response frame
	Err       *rpcError       // error frame
}

type pendingCall struct {
	ch   chan inbound // buffered 1; the read loop never blocks on a caller
	dead chan struct{}
}

// conn is the NDJSON client half over an app-server's stdio. The read loop
// demultiplexes responses to pending callers; every other inbound frame is a
// notification and is counted and dropped (tolerant reader). A write or read
// failure marks the connection dead once; all pending calls fail with
// ErrClosed.
type conn struct {
	w        io.WriteCloser
	timeout  time.Duration // per-call default when ctx carries no deadline
	onDead   func()
	notify   atomic.Int64 // tolerated unrecognized notifications
	badLines atomic.Int64 // tolerated unparseable lines

	wmu     sync.Mutex
	bw      *bufio.Writer
	wmuDead atomic.Bool

	mu      sync.Mutex
	nextID  int64
	pending map[string]*pendingCall
	deadCh  chan struct{} // closed exactly once by markDead
}

func newConn(r io.ReadCloser, w io.WriteCloser, requestTimeout time.Duration, onDead func()) *conn {
	c := &conn{
		w:       w,
		bw:      bufio.NewWriterSize(w, 32<<10),
		timeout: requestTimeout,
		onDead:  onDead,
		pending: map[string]*pendingCall{},
		deadCh:  make(chan struct{}),
	}
	go c.readLoop(r)
	return c
}

// call writes one request and waits for its response. params may be nil.
// The returned raw result is the untouched `result` JSON for decoding by the
// face that owns the method.
func (c *conn) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if _, ok := ctx.Deadline(); !ok && c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	id, p, err := c.register()
	if err != nil {
		return nil, err
	}
	frame, err := marshalRequest(id, method, params)
	if err != nil {
		c.finish(id)
		return nil, err
	}
	if err := c.write(frame); err != nil {
		c.finish(id)
		return nil, err
	}

	select {
	case resp := <-p.ch:
		if resp.Err != nil {
			return nil, resp.Err
		}
		return resp.RawResult, nil
	case <-p.dead:
		return nil, ErrClosed
	case <-ctx.Done():
		// Detach the pending entry so a late response finds no listener and
		// leaks nothing; the read loop tolerates unknown ids.
		c.finish(id)
		return nil, ctx.Err()
	}
}

func (c *conn) register() (string, *pendingCall, error) {
	select {
	case <-c.deadCh:
		return "", nil, ErrClosed
	default:
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.deadCh:
		return "", nil, ErrClosed
	default:
	}
	c.nextID++
	id := strconv.FormatInt(c.nextID, 10)
	p := &pendingCall{
		ch:   make(chan inbound, 1),
		dead: make(chan struct{}),
	}
	c.pending[id] = p
	return id, p, nil
}

func (c *conn) finish(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// markDead closes the death signal once and fails every in-flight call.
func (c *conn) markDead() {
	c.mu.Lock()
	select {
	case <-c.deadCh:
		c.mu.Unlock()
		return
	default:
	}
	close(c.deadCh)
	for id, p := range c.pending {
		delete(c.pending, id)
		close(p.dead)
	}
	c.mu.Unlock()
	if c.onDead != nil {
		c.onDead()
	}
}

func (c *conn) isDead() bool {
	select {
	case <-c.deadCh:
		return true
	default:
		return false
	}
}

func (c *conn) write(frame []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.wmuDead.Load() {
		return ErrClosed
	}
	if _, err := c.bw.Write(frame); err != nil {
		c.wmuDead.Store(true)
		go c.markDead()
		return err
	}
	if err := c.bw.WriteByte('\n'); err != nil {
		c.wmuDead.Store(true)
		go c.markDead()
		return err
	}
	if err := c.bw.Flush(); err != nil {
		c.wmuDead.Store(true)
		go c.markDead()
		return err
	}
	return nil
}

func (c *conn) close() {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.wmuDead.Load() {
		return
	}
	c.wmuDead.Store(true)
	_ = c.w.Close()
}

func (c *conn) readLoop(r io.ReadCloser) {
	defer func() {
		_ = r.Close()
		c.markDead()
	}()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxFrameBytes)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		in, err := parseInbound([]byte(line))
		if err != nil {
			// Tolerant reader: count and keep going. The desktop peer answers
			// garbage inbound the same way, so one bad line is not a desync.
			c.badLines.Add(1)
			continue
		}
		if !in.HasID {
			// Notification this client does not subscribe to (M4b will).
			c.notify.Add(1)
			continue
		}
		c.mu.Lock()
		p := c.pending[in.ID]
		if p != nil {
			delete(c.pending, in.ID)
		}
		c.mu.Unlock()
		if p == nil {
			continue // late reply to an abandoned request
		}
		p.ch <- in
	}
	if err := sc.Err(); err != nil && !isPipeClosedErr(err) {
		slog.Debug("zcodebridge: stdout read failed", "err", err)
	}
}

// isPipeClosedErr recognizes the benign pipe/handle errors a killed child
// produces on Windows and Unix; anything else is worth a debug line.
func isPipeClosedErr(err error) bool {
	s := err.Error()
	return strings.Contains(s, "closed") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "pipe is being closed") ||
		strings.Contains(s, "handle is invalid")
}

func parseInbound(line []byte) (inbound, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		return inbound{}, err
	}
	in := inbound{}
	if id, ok := raw["id"]; ok {
		in.HasID = true
		var idv any
		if err := json.Unmarshal(id, &idv); err != nil {
			return inbound{}, err
		}
		switch v := idv.(type) {
		case string:
			in.ID = v
		case float64:
			in.ID = strconv.FormatInt(int64(v), 10)
		default:
			return inbound{}, fmt.Errorf("unsupported id kind %T", idv)
		}
	}
	if m, ok := raw["method"]; ok {
		if err := json.Unmarshal(m, &in.Method); err != nil {
			return inbound{}, err
		}
	}
	if res, ok := raw["result"]; ok {
		in.RawResult = res
	}
	if e, ok := raw["error"]; ok {
		var re rpcError
		if err := json.Unmarshal(e, &re); err != nil {
			return inbound{}, err
		}
		in.Err = &re
	}
	if in.Method == "" && !in.HasID {
		return inbound{}, fmt.Errorf("frame is neither notification nor response")
	}
	return in, nil
}

// marshalRequest builds the strict {id, method, params} frame. params == nil
// omits the key entirely (the schema marks it optional).
func marshalRequest(id, method string, params any) ([]byte, error) {
	var b strings.Builder
	b.WriteString(`{"id":`)
	if err := writeJSONString(&b, id); err != nil {
		return nil, err
	}
	b.WriteString(`,"method":`)
	if err := writeJSONString(&b, method); err != nil {
		return nil, err
	}
	if params != nil {
		pb, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		b.WriteString(`,"params":`)
		b.Write(pb)
	}
	b.WriteString(`}`)
	return []byte(b.String()), nil
}

func writeJSONString(b *strings.Builder, s string) error {
	enc, err := json.Marshal(s)
	if err != nil {
		return err
	}
	b.Write(enc)
	return nil
}
