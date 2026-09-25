// Package turncontrol provides the loopback-only control endpoint a process
// exposes for the turn it is currently running (task 290 S2).
//
// Another local process that can read the session lease sidecar (same
// machine, same user — file permissions are the identity boundary) uses this
// endpoint to deliver the three cross-process turn operations: steer, ask
// answer, and cancel. The endpoint grants nothing by itself: it is a door
// that only opens for the short-lived token minted with the turn, only on
// 127.0.0.1, and only while the turn is registered.
//
// Scope: S2 ships the endpoint with injected handlers. Wiring the real
// controller operations (S3) is deliberately out of scope until the upstream
// ownership design (#9692) is aligned; with no endpoint started, behavior is
// exactly the pre-290 status quo.
package turncontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Failure codes returned to the caller. Every failure is explicit — a
// cross-process delivery that silently did nothing is the bug this task fixes.
const (
	CodeOwnerElsewhere      = "owner_elsewhere"
	CodeNoPendingPrompt     = "no_pending_prompt"
	CodeNotOwner            = "not_owner"
	CodeStaleTurn           = "stale_turn"
	CodeBadToken            = "bad_token"
	CodeEndpointUnreachable = "endpoint_unreachable"
)

// Handler-side failures S3 will map onto. The server translates them into the
// wire codes above so callers never have to string-match error text.
var (
	ErrOwnerElsewhere  = errors.New(CodeOwnerElsewhere)
	ErrNoPendingPrompt = errors.New(CodeNoPendingPrompt)
	ErrNotOwner        = errors.New(CodeNotOwner)
)

// Operations the endpoint accepts.
const (
	OpSteer  = "steer"
	OpAnswer = "answer"
	OpCancel = "cancel"
)

// Handler executes one operation against the owning process's turn. Returning
// a sentinel error maps to its wire code; any other error is passed through
// as-is (never swallowed). payload is the raw JSON body of the request.
type Handler func(op, turnID string, payload []byte) error

// VerifyCaller reports whether the caller identified by writerID (from the
// X-Session-Writer-ID header) may operate on this turn. A nil verifier accepts
// every caller that already holds a valid token — the token itself came from
// the session sidecar, which same-user file permissions already gate.
type VerifyCaller func(writerID string) bool

// Server is the per-turn loopback endpoint. One server instance serves one
// registered turn: Close it when the turn ends and the token dies with it.
type Server struct {
	turnID  string
	token   string
	verify  VerifyCaller
	handler Handler

	httpServer *http.Server
	listener   net.Listener
	addr       string

	mu     sync.Mutex
	closed bool
}

// NewServer binds a loopback listener immediately. The address is always
// 127.0.0.1 with an ephemeral port — there is deliberately no option to listen
// anywhere else (security boundary: local only).
func NewServer(turnID, token string, verify VerifyCaller, handler Handler) (*Server, error) {
	if turnID == "" {
		return nil, errors.New("turncontrol: turn id is required")
	}
	if token == "" {
		return nil, errors.New("turncontrol: token is required")
	}
	if handler == nil {
		return nil, errors.New("turncontrol: handler is required")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("turncontrol: loopback listen: %w", err)
	}
	s := &Server{
		turnID:   turnID,
		token:    token,
		verify:   verify,
		handler:  handler,
		listener: listener,
		addr:     listener.Addr().String(),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /turn/{id}/"+OpSteer, s.serve(OpSteer))
	mux.HandleFunc("POST /turn/{id}/"+OpAnswer, s.serve(OpAnswer))
	mux.HandleFunc("POST /turn/{id}/"+OpCancel, s.serve(OpCancel))
	s.httpServer = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = s.httpServer.Serve(listener) }()
	return s, nil
}

// Addr returns the bound loopback address (127.0.0.1:port).
func (s *Server) Addr() string { return s.addr }

// TurnID returns the turn this endpoint is bound to.
func (s *Server) TurnID() string { return s.turnID }

// Close shuts the endpoint down; the token stops working immediately.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.httpServer.Shutdown(ctx)
}

type turnControlResponse struct {
	OK    bool   `json:"ok"`
	Code  string `json:"code,omitempty"`
	Error string `json:"error,omitempty"`
}

func (s *Server) serve(op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		// Boundary 3: the short-lived token must match exactly.
		if r.Header.Get("X-Turn-Control-Token") != s.token {
			writeTurnControl(w, http.StatusForbidden, CodeBadToken, "invalid or missing turn control token")
			return
		}
		// Boundary 1: only callers the session lock semantics recognize may
		// operate. The verifier is injected so S3 can wire the real writer
		// identity; tests pin both directions.
		caller := r.Header.Get("X-Session-Writer-ID")
		if s.verify != nil && !s.verify(caller) {
			writeTurnControl(w, http.StatusForbidden, CodeNotOwner,
				"caller is not an authorized writer for this session")
			return
		}
		// Idempotency: a request aimed at a turn that is no longer the
		// registered one must fail loudly instead of acting on the wrong turn.
		if id := r.PathValue("id"); id != s.turnID {
			writeTurnControl(w, http.StatusConflict, CodeStaleTurn,
				fmt.Sprintf("turn %q is not the registered turn", id))
			return
		}
		payload, err := readTurnControlBody(r)
		if err != nil {
			writeTurnControl(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		if err := s.handler(op, s.turnID, payload); err != nil {
			code, status := mapHandlerError(err)
			writeTurnControl(w, status, code, err.Error())
			return
		}
		writeTurnControl(w, http.StatusOK, "", "")
	}
}

func readTurnControlBody(r *http.Request) ([]byte, error) {
	if r.ContentLength == 0 {
		return nil, nil
	}
	defer r.Body.Close()
	const maxBody = 1 << 20 // 1 MiB: operations carry instructions, never transcripts
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return body, nil
}

func mapHandlerError(err error) (code string, status int) {
	switch {
	case errors.Is(err, ErrOwnerElsewhere):
		return CodeOwnerElsewhere, http.StatusConflict
	case errors.Is(err, ErrNoPendingPrompt):
		return CodeNoPendingPrompt, http.StatusConflict
	case errors.Is(err, ErrNotOwner):
		return CodeNotOwner, http.StatusForbidden
	default:
		return "handler_error", http.StatusInternalServerError
	}
}

func writeTurnControl(w http.ResponseWriter, status int, code, msg string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(turnControlResponse{
		OK: status == http.StatusOK, Code: code, Error: msg,
	})
}

// Post delivers one operation to the endpoint at addr (the caller side of the
// four-step delivery: read sidecar → connect → authenticate → execute). Any
// transport failure is reported as endpoint_unreachable with the address so
// the caller can tell a dead process from a rejected request.
func Post(ctx context.Context, addr, token, writerID, op, turnID string, payload []byte) error {
	url := "http://" + addr + "/turn/" + turnID + "/" + op
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(payload)))
	if err != nil {
		return fmt.Errorf("%s: %w", CodeEndpointUnreachable, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Turn-Control-Token", token)
	req.Header.Set("X-Session-Writer-ID", writerID)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("%s: %s (%v)", CodeEndpointUnreachable, addr, err)
	}
	defer resp.Body.Close()
	var out turnControlResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out.OK {
		return nil
	}
	if out.Code == "" {
		out.Code = fmt.Sprintf("http_%d", resp.StatusCode)
	}
	return &DeliveryError{Code: out.Code, Message: out.Error, Addr: addr}
}

// DeliveryError is an explicit, coded cross-process delivery failure.
type DeliveryError struct {
	Code    string
	Message string
	Addr    string
}

func (e *DeliveryError) Error() string {
	return fmt.Sprintf("turn control delivery failed: %s: %s (%s)", e.Code, e.Message, e.Addr)
}
