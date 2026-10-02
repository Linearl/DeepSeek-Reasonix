package baseproc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"
)

// gracefulDrainBudget bounds how long Serve waits for in-flight handlers after
// a base.shutdown response (design §4: 在途 ≤5s then kill). S1c replaces this
// with the full shutdown/orphan state machine; the bound itself stays.
const gracefulDrainBudget = 5 * time.Second

// HandlerFunc answers one request. Returning an *RPCError passes the code
// through verbatim; any other error becomes CodeInternalError with the error
// text as message (the channel is same-user stdio, not a trust boundary).
type HandlerFunc func(ctx context.Context, params json.RawMessage) (any, error)

// Server is the subprocess side of the base protocol: it reads length-prefixed
// JSON-RPC frames from a reader, dispatches requests to registered handlers
// concurrently (matrix C1 requires parallel tool execution, so the loop never
// serialises handlers), and writes responses serialized under a mutex so
// interleaved notifications cannot corrupt frames.
//
// The core server always knows hello/ping/shutdown. The S1b tool face joins
// via AttachToolSurface (base.toolCatalog/base.toolCall + CapTools); any other
// unregistered method answers CodeMethodNotFound and the connection stays
// usable (design §5 contract discipline).
type Server struct {
	version string

	mu         sync.Mutex
	methods    map[string]HandlerFunc
	caps       []string
	shutdown   bool
	out        io.Writer
	writeMu    sync.Mutex
	notifyFunc func(method string, params json.RawMessage)
	// surface is the S1b tool face behind base.toolCatalog/base.toolCall
	// (AttachToolSurface). nil until attached: the S1a core server then keeps
	// answering -32601 for tool methods without dropping the connection.
	surface ToolSurface

	// leases is the S1c session lease table (AttachSessionAccounting); nil
	// until attached, which is what keeps the S1a core-only contract.
	leases *leaseTable
	// clientPID is the owner key the latest base.hello declared; attach stamps
	// it onto the leases it creates (design §6 C4).
	clientPID int
	// onHello runs after a successful base.hello — the orphan-lease sweep.
	onHello func(clientPID int)

	quitOnce sync.Once
	quit     chan struct{}

	wg sync.WaitGroup // in-flight request handlers
}

// NewServer creates a server identifying itself with the given build version
// and registers the v1 core methods: base.hello, base.ping, base.shutdown.
func NewServer(version string) *Server {
	s := &Server{
		version: version,
		methods: make(map[string]HandlerFunc),
		quit:    make(chan struct{}),
	}
	s.methods[MethodHello] = s.handleHello
	s.methods[MethodPing] = handlePing
	s.methods[MethodShutdown] = s.handleShutdown
	return s
}

// Register installs a request handler. Call before Serve (or from a handler —
// the methods map is mutex-guarded); registering a core method name overrides
// the default, which later slices must not do.
func (s *Server) Register(method string, h HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.methods[method] = h
}

// AddCapabilities records optional capabilities reported by base.hello. Call
// before Serve; later slices (S1b) register their methods and capabilities
// together.
func (s *Server) AddCapabilities(caps ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range caps {
		if !slices.Contains(s.caps, c) {
			s.caps = append(s.caps, c)
		}
	}
}

// OnNotifications installs a handler for client→server notifications. v1
// defines none; unknown notifications are always ignored, never fatal.
func (s *Server) OnNotifications(f func(method string, params json.RawMessage)) {
	s.notifyFunc = f
}

// Serve runs the dispatch loop until the input hits EOF (the peer closed the
// pipe — the D4 orphan path: a vanished parent surfaces as EOF and the
// subprocess exits), a read error, base.shutdown completes, or ctx is done.
// It returns nil on clean shutdown and EOF, mirroring exit code 0 in
// RunStdioServer.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	s.mu.Lock()
	s.out = out
	s.mu.Unlock()

	readErr := make(chan error, 1)
	go func() { readErr <- s.readLoop(ctx, in) }()

	select {
	case err := <-readErr:
		return err
	case <-s.quit:
		// The shutdown response has been written; give in-flight handlers the
		// §4 budget, then leave. The blocked reader goroutine unblocks as soon
		// as the caller closes the pipe (RemoteBaseClient.Close does).
		s.drain(gracefulDrainBudget)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ShutdownRequested reports whether a base.shutdown has been accepted.
func (s *Server) ShutdownRequested() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shutdown
}

func (s *Server) readLoop(ctx context.Context, in io.Reader) error {
	for {
		payload, err := ReadFrame(in)
		if err != nil {
			if errors.Is(err, errFrameTooLarge) {
				// Framing is unrecoverable past an oversized header.
				return err
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		var frame Frame
		if err := json.Unmarshal(payload, &frame); err != nil {
			// The length prefix is intact, so the stream stays usable: answer
			// with a parse error (ID null per JSON-RPC) and keep reading.
			s.writeResponse(Frame{ID: []byte("null"), Error: &RPCError{
				Code: CodeParseError, Message: fmt.Sprintf("frame is not valid JSON: %v", err),
			}})
			continue
		}
		switch frame.Kind() {
		case FrameRequest:
			s.dispatch(ctx, frame)
		case FrameNotification:
			if s.notifyFunc != nil {
				s.notifyFunc(frame.Method, frame.Params)
			}
		case FrameResponse:
			// The v1 server issues no requests toward the client; a stray
			// response is ignored, never fatal.
		default:
			s.writeResponse(Frame{ID: []byte("null"), Error: &RPCError{
				Code: CodeInvalidRequest, Message: "frame carries neither method nor id",
			}})
		}
	}
}

// dispatch runs one request handler on its own goroutine so a slow tool call
// cannot head-of-line block others (matrix C1). Shutdown bookkeeping happens
// after the response is on the wire.
func (s *Server) dispatch(ctx context.Context, req Frame) {
	if s.shuttingDown() {
		s.writeResponse(Frame{ID: req.ID, Error: &RPCError{
			Code: CodeShuttingDown, Message: "server is shutting down",
		}})
		return
	}
	s.mu.Lock()
	handler, ok := s.methods[req.Method]
	s.mu.Unlock()
	isShutdown := req.Method == MethodShutdown
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		resp := Frame{JSONRPC: JSONRPCVersion, ID: req.ID}
		if !ok {
			// Contract discipline (design §5): unknown methods answer -32601
			// instead of dropping the connection, so newer clients survive an
			// older base process (S2 session methods ride this).
			resp.Error = &RPCError{Code: CodeMethodNotFound, Message: fmt.Sprintf("method %q not found", req.Method)}
		} else {
			result, err := handler(ctx, req.Params)
			switch {
			case err != nil:
				var rpcErr *RPCError
				if errors.As(err, &rpcErr) {
					resp.Error = rpcErr
				} else {
					resp.Error = &RPCError{Code: CodeInternalError, Message: err.Error()}
				}
			default:
				if result != nil {
					encoded, marshalErr := json.Marshal(result)
					if marshalErr != nil {
						resp.Error = &RPCError{Code: CodeInternalError, Message: fmt.Sprintf("marshal result: %v", marshalErr)}
					} else {
						resp.Result = encoded
					}
				}
			}
		}
		s.writeResponse(resp)
		if isShutdown && resp.Error == nil {
			s.quitOnce.Do(func() { close(s.quit) })
		}
	}()
}

func (s *Server) shuttingDown() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shutdown
}

// drain waits for in-flight handlers up to budget.
func (s *Server) drain(budget time.Duration) {
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(budget):
	}
}

// Notify emits a server→client notification frame (base.toolProgress /
// base.catalogChanged / base.dying from later slices; any method string is
// wireable). It errors before Serve has an output or if the frame cannot be
// written — notification loss never corrupts the response stream because
// writes stay serialised under the same mutex.
func (s *Server) Notify(method string, params any) error {
	var raw json.RawMessage
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("baseproc: marshal %s notification: %w", method, err)
		}
		raw = encoded
	}
	return s.writeResponse(Frame{Method: method, Params: raw})
}

// writeResponse serialises one frame onto the output under the write mutex.
func (s *Server) writeResponse(resp Frame) error {
	s.mu.Lock()
	out := s.out
	s.mu.Unlock()
	if out == nil {
		return errors.New("baseproc: server has no output writer (Serve not running)")
	}
	resp.JSONRPC = JSONRPCVersion
	encoded, err := json.Marshal(resp)
	if err != nil {
		// A response that cannot marshal is dropped; the caller-side pending
		// call resolves via connection close. Never write partial frames.
		return fmt.Errorf("baseproc: marshal response: %w", err)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := WriteFrame(out, encoded); err != nil {
		return fmt.Errorf("baseproc: write response: %w", err)
	}
	return nil
}

func (s *Server) handleHello(_ context.Context, params json.RawMessage) (any, error) {
	var p HelloParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: CodeInvalidParams, Message: fmt.Sprintf("hello params: %v", err)}
	}
	version, rpcErr := negotiateVersion(p.ProtocolVersion, ProtocolVersion)
	if rpcErr != nil {
		return nil, rpcErr
	}
	s.mu.Lock()
	caps := slices.Clone(s.caps)
	onHello := s.onHello
	s.mu.Unlock()
	// The session face's orphan sweep (matrix C4) runs after the answer is
	// assembled but before it is returned, so a client that re-attaches right
	// after hello already sees a cleaned table. It must run outside s.mu: the
	// sweep takes the same lock to record the declaring pid.
	if onHello != nil {
		onHello(p.ClientPID)
	}
	return HelloResult{
		ProtocolVersion: version,
		ServerVersion:   s.version,
		Capabilities:    caps,
	}, nil
}

func handlePing(_ context.Context, _ json.RawMessage) (any, error) {
	return PingResult{OK: true}, nil
}

func (s *Server) handleShutdown(_ context.Context, _ json.RawMessage) (any, error) {
	s.mu.Lock()
	s.shutdown = true
	s.mu.Unlock()
	return ShutdownResult{OK: true}, nil
}
