// Package zcodebridge is the narrow Reasonix→zcode real-time injection channel
// (M4a of the Reasonix task bus). It spawns one `zcode app-server --stdio`
// child (the same launch shape as the desktop host), speaks the CLI's bare
// NDJSON request/response wire, and exposes exactly the frozen M4a face:
//
//	handshake   v4/connection/flow + frozen-face smoke probe, fail-closed:
//	            any mismatch tears the child down and refuses to work.
//	enum        session/list — session/task discovery.
//	inject      v4/command {type:"sendText", payload:{text, requestedDelivery}}
//	            with startNow (phone semantics: atomic preemption of the
//	            current turn, bypassing queue admission) / queue (mail
//	            semantics) / guide.
//	events      session/events with afterSeq incremental pull — pull beats
//	            push; the v4 subscribe face is deferred to M4b.
//
// Frozen-protocol references (zcode v3.14.3, 29628c9): V4_WIRE_PROTOCOL_VERSION=3
// (zcode-protocol-v4/core.ts:7), commandEnvelopeSchema / commandTypeSchema /
// sendText payload (zcode-protocol-v4/command.ts), clientHello `.strict()`
// capability gate (zcode-protocol-v4/transport.ts:68), sessionList /
// sessionEvents methods (zcode-protocol/index.ts:3565,3573). The wire frames
// are NOT JSON-RPC 2.0: the CLI's zcodeProtocolRequestSchema is strict
// `{id, method, params}` — a `jsonrpc` key makes the whole line fail to parse
// (verified live against the installed CLI).
//
// Tolerant-reader principle: inbound notifications this package does not
// understand (startup/storageState and future additions) are counted and
// dropped; outbound frames carry only the frozen subset. Schema evolution on
// the zcode side therefore cannot crash the bridge, while any change to the
// frozen face itself fails the handshake gate (spec §三: probe failures are a
// breaking change — bump the spec, do not guess).
package zcodebridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Frozen protocol constants (see package doc). Declared here so a zcode-side
// bump surfaces as a compile-time-visible fact instead of folklore.
const (
	// LegacyWireProtocolVersion is ZCODE_PROTOCOL_VERSION (legacy session/* wire).
	LegacyWireProtocolVersion = 1
	// V4WireProtocolVersion is V4_WIRE_PROTOCOL_VERSION (zcode-protocol-v4/core.ts).
	V4WireProtocolVersion = 3
)

// Error-code contract (zcode-protocol/index.ts:80).
const errCodeSessionUnavailable = -32004

// Defaults for Config zero values.
const (
	defaultCommand          = "zcode"
	defaultHandshakeTimeout = 30 * time.Second // desktop host waits 30s for first storage status
	defaultRequestTimeout   = 30 * time.Second
	defaultConnectionID     = "reasonix-zcodebridge"
	defaultClientID         = "reasonix-zcodebridge"
	// maxFrameBytes bounds one NDJSON frame. The zcode face caps RPC payloads
	// far below this (512KiB chunks); the slack covers chatty snapshots.
	maxFrameBytes = 16 << 20
	// stderrTailCap keeps the last chunk of child stderr for diagnostics.
	stderrTailCap = 16 << 10
)

// Config builds a Bridge. Zero values pick the documented defaults; New
// validates. M4a intentionally has no [config] table yet (a parallel branch
// owns internal/config/render.go) — construction is wired in serve.go behind
// an environment gate, so the only surface is this struct.
type Config struct {
	// Command is the zcode CLI argv[0]. Default "zcode".
	Command string
	// Args is the full argv tail. Default ["app-server", "--stdio"] (the
	// desktop host launch shape, zcodeAgentProcessManager.ts:369).
	Args []string
	// Workspace is the child's working directory. Empty inherits the parent.
	Workspace string
	// HandshakeTimeout bounds the whole Open gate. Default 30s.
	HandshakeTimeout time.Duration
	// RequestTimeout bounds a single RPC when the caller passed no deadline.
	// Default 30s.
	RequestTimeout time.Duration
	// ConnectionID names the v4 connection in flow-control calls.
	// Default "reasonix-zcodebridge".
	ConnectionID string
	// ClientID stamps every command envelope. Default "reasonix-zcodebridge".
	ClientID string
}

// Bridge is one live connection to one zcode app-server child. All methods
// are safe for concurrent use; after the child dies every call fails with
// ErrClosed until a new Open.
type Bridge struct {
	cfg   Config
	child *childProcess
	conn  *conn

	dead      chan struct{}
	closeOnce sync.Once
	deadOnce  sync.Once

	mu       sync.Mutex
	inFlight map[string]struct{} // per-session politeness guard: ≤1 sendText in flight
	closed   bool
}

// Sentinel errors. ErrProtocolMismatch means the handshake gate failed: the
// peer does not speak the frozen M4a face (or is not a zcode app-server at
// all) — per spec §三 the client must refuse to work, never guess.
var (
	ErrDisabled         = errors.New("zcodebridge: not enabled")
	ErrClosed           = errors.New("zcodebridge: connection closed")
	ErrProtocolMismatch = errors.New("zcodebridge: frozen protocol face mismatch")
	ErrInFlight         = errors.New("zcodebridge: sendText already in flight for session")
	ErrSessionNotActive = errors.New("zcodebridge: session is not active in the app-server process")
)

// New validates cfg. It does not spawn anything — see Open.
func New(cfg Config) (*Bridge, error) {
	cfg.Command = strings.TrimSpace(cfg.Command)
	if cfg.Command == "" {
		cfg.Command = defaultCommand
	}
	if len(cfg.Args) == 0 {
		cfg.Args = []string{"app-server", "--stdio"}
	}
	for _, a := range cfg.Args {
		if strings.TrimSpace(a) == "" {
			return nil, fmt.Errorf("zcodebridge: blank argv element %q", a)
		}
	}
	if cfg.HandshakeTimeout <= 0 {
		cfg.HandshakeTimeout = defaultHandshakeTimeout
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = defaultRequestTimeout
	}
	if strings.TrimSpace(cfg.ConnectionID) == "" {
		cfg.ConnectionID = defaultConnectionID
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		cfg.ClientID = defaultClientID
	}
	return &Bridge{
		cfg:      cfg,
		dead:     make(chan struct{}),
		inFlight: map[string]struct{}{},
	}, nil
}

// Open spawns the app-server child and runs the fail-closed handshake gate.
// Any gate failure tears the child down and returns an error wrapping either
// ErrProtocolMismatch (peer does not speak the frozen face) or the transport
// cause — serve logs it and stays without a bridge; it never takes the
// session server down (same posture as bus-mcp/bus-worker wiring).
func Open(ctx context.Context, cfg Config) (*Bridge, error) {
	b, err := New(cfg)
	if err != nil {
		return nil, err
	}
	child, err := startChild(ctx, b.cfg)
	if err != nil {
		return nil, fmt.Errorf("zcodebridge: spawn %s %s: %w", b.cfg.Command, strings.Join(b.cfg.Args, " "), err)
	}
	b.child = child
	b.conn = newConn(child.stdout, child.stdin, b.cfg.RequestTimeout, b.markDead)
	go child.reap()

	gateErr := b.handshake(ctx)
	if gateErr != nil {
		b.Close()
		return nil, gateErr
	}
	return b, nil
}

// handshake is the fail-closed compatibility gate (spec §二/§三): the v4 face
// must accept flow registration, the legacy read face must answer a strict
// session/list, and the events face must answer with a page when any session
// is active. "Session is not active" (-32004) is a domain state, not a
// protocol mismatch — cold sessions are normal. Anything else fails closed.
func (b *Bridge) handshake(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, b.cfg.HandshakeTimeout)
	defer cancel()

	if err := b.setConnectionFlowDrained(ctx); err != nil {
		return fmt.Errorf("%w: v4/connection/flow: %v", ErrProtocolMismatch, err)
	}
	sessions, err := b.List(ctx)
	if err != nil {
		return fmt.Errorf("%w: session/list: %v", ErrProtocolMismatch, err)
	}
	if len(sessions) > 0 {
		if _, err := b.Events(ctx, sessions[0].SessionID, 0, 1); err != nil {
			if !errors.Is(err, ErrSessionNotActive) {
				return fmt.Errorf("%w: session/events: %v", ErrProtocolMismatch, err)
			}
			// No active session to probe the events face with; List already
			// proved the read face. Events proves itself on first injection.
		}
	}
	return nil
}

// Probe runs the frozen-face smoke probe on an already-open bridge (spec §三
// compatibility rule: list once, events page once). Upgrade-regression runs
// call this against a real CLI; failures mean "protocol face mismatch", never
// "retry".
func (b *Bridge) Probe(ctx context.Context) error {
	return b.handshake(ctx)
}

// setConnectionFlowDrained registers the bridge's v4 connection in the
// drained (flow-open) state — the app-server treats an unknown connectionId
// as a no-op, and an incompatible peer fails the strict params parse. The
// result contract is `{}` strict (v4ConnectionFlowResultSchema); anything
// else is a frozen-face mismatch.
func (b *Bridge) setConnectionFlowDrained(ctx context.Context) error {
	raw, err := b.conn.call(ctx, methodConnectionFlow, map[string]any{
		"connectionId": b.cfg.ConnectionID,
		"state":        "drained",
	})
	if err != nil {
		return err
	}
	if !isEmptyJSONObject(raw) {
		return fmt.Errorf("connection/flow result is not the frozen empty object: %s", clipRaw(raw))
	}
	return nil
}

// isEmptyJSONObject reports whether raw is exactly `{}` (possibly with
// whitespace), the v4ConnectionFlowResultSchema shape.
func isEmptyJSONObject(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s == "{}"
}

func clipRaw(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// markDead tears the bridge down after the child died on its own. Every later
// call observes ErrClosed; Close stays idempotent.
func (b *Bridge) markDead() {
	b.deadOnce.Do(func() { close(b.dead) })
	b.conn.close()
}

// Dead returns a channel closed once the child process has exited (or the
// bridge was closed). Callers use it to schedule a rebuild+reconnect
// (spec §四.5: crash → relaunch → events continue from afterSeq).
func (b *Bridge) Dead() <-chan struct{} { return b.dead }

// StderrTail returns the last captured chunk of child stderr for diagnostics.
func (b *Bridge) StderrTail() string {
	if b.child == nil {
		return ""
	}
	return b.child.stderr.String()
}

// Closed reports whether Close has been called locally.
func (b *Bridge) Closed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}

// Close kills the child process tree and releases the connection. Idempotent.
func (b *Bridge) Close() {
	b.closeOnce.Do(func() {
		b.mu.Lock()
		b.closed = true
		b.mu.Unlock()
		b.markDead()
		b.child.kill()
	})
}

// alive returns ErrClosed once the bridge is closed or the child died.
func (b *Bridge) alive() error {
	select {
	case <-b.dead:
		return ErrClosed
	default:
		return nil
	}
}

// acquireInFlight takes the per-session sendText slot (spec §四.4: ≤1 in
// flight per session; the CLI's CommandInbox owns real serialization).
func (b *Bridge) acquireInFlight(sessionID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, busy := b.inFlight[sessionID]; busy {
		return fmt.Errorf("%w %s", ErrInFlight, sessionID)
	}
	b.inFlight[sessionID] = struct{}{}
	return nil
}

func (b *Bridge) releaseInFlight(sessionID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.inFlight, sessionID)
}
