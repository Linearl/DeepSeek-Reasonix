// Package baseproc implements the S1 resident base-subprocess protocol and the
// dual-implementation client skeleton (design "S1 底座常驻子进程——设计
// 2026-09-30" §3/§5/§10, slice S1a).
//
// Topology: tab runtimes reach the heavy base section (MCP connections, plugin
// and tool registries, builtin registration, provider factory) through the
// BaseClient interface. InlineBaseClient walks the existing in-process path
// (R1 fallback — same functions, same inputs, byte-identical behaviour);
// RemoteBaseClient reaches the resident subprocess over length-prefixed
// JSON-RPC 2.0 on stdio (decision D2). The subprocess is the existing reasonix
// binary run as `reasonix base serve --stdio` (decision D4) — no new binary.
//
// R1 is a hard constraint baked into this package: baseproc.Start never fails.
// When the switch is off (the default), or the subprocess cannot be spawned,
// or the protocol handshake fails (F3), the caller receives an inline client
// and the decision is logged (`boot: base fallback` / `boot: base remote`,
// decision D5's grep-able family).
//
// Slice state (design §10):
//   - S1a: protocol v1 definitions, frame codec, JSON-RPC serve dispatch,
//     hello/ping/shutdown core, dual-implementation skeletons, and the
//     experimental_base_process switch register.
//   - S1b: the tool surface — ToolSurface/RegistrySurface (surface.go),
//     base.toolCatalog/base.toolCall handlers behind AttachToolSurface with
//     the CapTools capability (serve_toolface.go), per-call_id progress
//     routing (client.go), the event-sink adapter (sink.go), and the
//     boot/controller/agent consumption gates. The subprocess still does not
//     host a registry, so a real serve process advertises no tools capability
//     and every gate falls back inline per R1 — the channel and gates are
//     proven by tests that attach a surface directly.
//   - S1c (this series): lifecycle closed — a Manager supervises the
//     subprocess (D5 state machine, health pings, exponential-backoff
//     restart, degraded fallback: lifecycle.go), shutdown is acknowledged
//     before teardown and a vanished parent self-exits the child (D4),
//     stderr lands in logs/base.log (F2: manager.go/baselog.go), and
//     base.attach/base.detach do real lease accounting with the C4 orphan
//     sweep (lease.go). Open and reported as remaining: hosting a
//     workspace-bound registry behind CapTools, providerResolve, and the
//     child-side liveness tick — all three wait on the base process owning
//     boot's construction code for a root.
//
// Contract discipline (design §5): protocol_version is negotiated at hello and
// v1 only ever grows; unknown methods answer JSON-RPC -32601 instead of
// dropping the connection, so S2 can add session-state methods without a
// client-visible protocol break.
package baseproc

import (
	"encoding/json"
	"fmt"
)

const (
	// ProtocolVersion is the v1 wire protocol version (design §5: v1 只增不改;
	// later slices add methods and capabilities, never reshape v1 ones).
	ProtocolVersion = 1

	// JSONRPCVersion is the JSON-RPC envelope version every frame carries.
	JSONRPCVersion = "2.0"
)

// Request methods (design §5 IPC contract v1 minimal surface).
const (
	MethodHello           = "base.hello"
	MethodAttach          = "base.attach"
	MethodDetach          = "base.detach"
	MethodToolCatalog     = "base.toolCatalog"
	MethodToolCall        = "base.toolCall"
	MethodProviderResolve = "base.providerResolve"
	MethodPing            = "base.ping"
	MethodShutdown        = "base.shutdown"
)

// Server→client notifications (design §5). The S1a core server never sends
// them; they are defined here so the wire shape freezes now and S1b/S1c only
// start emitting.
const (
	// NotifyToolProgress streams in-flight tool output chunks keyed by call_id
	// (decision D3: long tool runs must not wedge the RPC channel; the RPC
	// response only arrives on completion).
	NotifyToolProgress = "base.toolProgress"
	// NotifyCatalogChanged announces tool-catalog changes (plugin hot events).
	NotifyCatalogChanged = "base.catalogChanged"
	// NotifyDying is the subprocess's proactive goodbye, giving clients a
	// head start on inline fallback (R1 mitigation).
	NotifyDying = "base.dying"
)

// Optional capabilities negotiated via base.hello. The S1a core server
// (hello/ping/shutdown) reports none of them; a client must treat a missing
// capability as "method unavailable in this server build" instead of
// round-tripping a guaranteed -32601. Later slices append new capability
// strings; existing ones are never repurposed.
const (
	CapTools           = "tools"            // S1b: toolCatalog / toolCall
	CapSessions        = "sessions"         // S1b/S1c: attach / detach lease accounting
	CapProviderResolve = "provider_resolve" // S1b: request-level provider resolution
)

// JSON-RPC standard error codes plus the base-specific range (-32000..-32099).
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
	// CodeVersionMismatch rejects base.hello when the peers share no protocol
	// version (design F3: upgrade window with old client + new subprocess).
	// The client must fall back inline and warn once — no retry storm.
	CodeVersionMismatch = -32001
	// CodeShuttingDown refuses new requests once base.shutdown has been
	// accepted. Full drain-and-kill semantics land in S1c.
	CodeShuttingDown = -32002
)

// RPCError is the JSON-RPC error object. BaseClient calls surface it directly
// when the peer answers with an error response, so callers can branch on
// CodeMethodNotFound etc. without re-parsing.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Error implements the error interface with a stable "code: message" shape.
func (e *RPCError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("baseproc rpc %d: %s", e.Code, e.Message)
}

// FrameKind classifies a decoded frame.
type FrameKind int

const (
	FrameInvalid FrameKind = iota
	FrameRequest
	FrameNotification
	FrameResponse
)

// Frame is the single envelope type for both directions. One struct keeps
// encode/decode symmetric: requests carry Method+ID(+Params), notifications
// carry Method without ID, responses carry ID with either Result or Error.
type Frame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// Kind classifies the frame. A frame with neither Method nor ID (and no
// response markers) is FrameInvalid — the serve loop answers it with
// CodeInvalidRequest and keeps reading.
func (f *Frame) Kind() FrameKind {
	if f == nil {
		return FrameInvalid
	}
	switch {
	case f.Method != "" && f.ID != nil:
		return FrameRequest
	case f.Method != "":
		return FrameNotification
	case f.ID != nil || f.Result != nil || f.Error != nil:
		return FrameResponse
	default:
		return FrameInvalid
	}
}

// HelloParams opens the connection (design §5): protocol_version is the
// highest version the client speaks; client_pid is the lease owner key the
// orphan-reclaim path (matrix C4) batches on during later hello handshakes.
type HelloParams struct {
	ProtocolVersion int `json:"protocol_version"`
	ClientPID       int `json:"client_pid"`
}

// HelloResult carries the negotiated version (the common one, i.e. the
// client's when the handshake succeeds), the server build identity, and the
// optional capability list.
type HelloResult struct {
	ProtocolVersion int      `json:"protocol_version"`
	ServerVersion   string   `json:"server_version"`
	Capabilities    []string `json:"capabilities,omitempty"`
}

// AttachParams bind a session to the base (design §6 — the R3 core).
type AttachParams struct {
	SessionID      string `json:"session_id"`
	Root           string `json:"root"`
	WorkspaceScope string `json:"workspace_scope"`
}

// AttachResult returns the base-side lease. The lease is in-memory accounting
// inside the subprocess (design §6: process death clears it; it never touches
// the *.jsonl.lease.json writer_id file locks — R8).
type AttachResult struct {
	LeaseID string `json:"lease_id"`
}

// DetachParams release one lease.
type DetachParams struct {
	LeaseID string `json:"lease_id"`
}

// DetachResult acknowledges the release (design §5: ok).
type DetachResult struct {
	OK bool `json:"ok"`
}

// PingResult answers base.ping (design §5: health check, no payload).
type PingResult struct {
	OK bool `json:"ok"`
}

// ShutdownResult acknowledges base.shutdown; Serve drains in-flight handlers
// and returns right after this response is written (§4, bounded by S1c).
type ShutdownResult struct {
	OK bool `json:"ok"`
}

// ToolCatalogParams carries the visibility filter scope. S1b settled the
// vocabulary in surface.go — ScopeAll/ScopeProvider mirror the in-process
// Registry.AllContractEntries/ContractEntries filters; an unknown scope is
// rejected, never silently reinterpreted.
type ToolCatalogParams struct {
	Scope string `json:"scope,omitempty"`
}

// ToolDescriptor is the shared tool description structure both BaseClient
// implementations speak (design R2: the interface layer stays thin and both
// sides share one description shape). It mirrors the tool.Tool description
// surface — name, description, JSON Schema, read-only parallelism hint.
type ToolDescriptor struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema,omitempty"`
	ReadOnly    bool            `json:"read_only,omitempty"`
}

// ToolCatalogResult is the catalog answer.
type ToolCatalogResult struct {
	Tools []ToolDescriptor `json:"tools"`
}

// ToolCallParams execute one tool inside the subprocess (design §5/D3).
// CallID correlates base.toolProgress notification chunks with the eventual
// RPC response; SessionID scopes execution state without moving session
// ownership into the base (S1 boundary).
type ToolCallParams struct {
	CallID    string          `json:"call_id"`
	Tool      string          `json:"tool"`
	Args      json.RawMessage `json:"args,omitempty"`
	SessionID string          `json:"session_id,omitempty"`
}

// ToolCallResult is the completion answer. Progress arrives earlier via
// NotifyToolProgress frames; the RPC response only fires on completion (D3).
type ToolCallResult struct {
	Content string `json:"content"`
	// Err carries the tool-level failure text. A tool failing is a completed
	// call (the model sees the error), distinct from a transport failure.
	Err string `json:"error,omitempty"`
}

// ProviderResolveParams ask for request-level provider resolution (design §5,
// isomorphic to the 423 Q1 ruling: model/effort are request parameters).
type ProviderResolveParams struct {
	ModelRef string `json:"model_ref"`
	Effort   string `json:"effort,omitempty"`
}

// ProviderResolveResult reports availability only — the provider key never
// leaves the subprocess (design F1: credentials are decrypted exclusively
// inside the base process). The full config-snapshot fields land in S1b.
type ProviderResolveResult struct {
	Available bool   `json:"available"`
	Provider  string `json:"provider,omitempty"`
	ModelRef  string `json:"model_ref,omitempty"`
}

// ToolProgressParams ride NotifyToolProgress notifications (server→client).
type ToolProgressParams struct {
	CallID string `json:"call_id"`
	Chunk  string `json:"chunk,omitempty"`
}

// CatalogChangedParams ride NotifyCatalogChanged notifications.
type CatalogChangedParams struct {
	Reason string `json:"reason,omitempty"`
}

// DyingParams ride NotifyDying notifications (R1 early-warning window).
type DyingParams struct {
	Reason string `json:"reason,omitempty"`
}

// negotiateVersion resolves the common protocol version for a hello. The
// server speaks up to maxVersion; a client at or below that gets its own
// version back (v1 only grows, so a newer server is compatible with an older
// client). A client claiming a version above the server's is rejected with
// CodeVersionMismatch — the F3 fallback trigger.
func negotiateVersion(clientVersion, maxVersion int) (int, *RPCError) {
	if clientVersion <= 0 {
		return 0, &RPCError{Code: CodeInvalidParams, Message: "protocol_version must be positive"}
	}
	if clientVersion > maxVersion {
		return 0, &RPCError{
			Code:    CodeVersionMismatch,
			Message: fmt.Sprintf("client protocol_version %d above server max %d", clientVersion, maxVersion),
		}
	}
	return clientVersion, nil
}
