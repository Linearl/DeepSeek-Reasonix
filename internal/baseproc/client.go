package baseproc

import (
	"context"
	"errors"
)

// ErrNotWired marks protocol surface that is defined on the wire but has no
// behaviour behind it in the S1a skeleton. Later slices replace these returns:
// S1b wires the tool surface (InlineBaseClient wraps the existing boot.Build
// base section; RemoteBaseClient forwards to a server advertising the
// capability), S1b/S1c the session lease surface.
var ErrNotWired = errors.New("baseproc: method not wired in this slice (S1b pending)")

// Mode reports which R1 path produced a BaseClient (decision D5's state pairs:
// inline = the current in-process path; remote = the resident subprocess).
type Mode string

const (
	ModeInline Mode = "inline"
	ModeRemote Mode = "remote"
)

// BaseClient is the tab-side abstraction over the heavy base section (design
// §3): MCP connections, plugin/tool registries, builtin registration, and the
// provider factory, reached identically whether they live in-process
// (InlineBaseClient — the R1 fallback, byte-identical to the pre-S1 path) or
// in the resident subprocess (RemoteBaseClient).
//
// The interface is the full v1 protocol surface from day one (design R2: the
// adaptation layer stays thin); slices fill behaviour behind it without
// reshaping it, so S2 can add session-state methods as v2 without touching
// callers.
type BaseClient interface {
	// Mode reports inline vs remote for logging and tests.
	Mode() Mode
	// Hello performs the handshake/capability negotiation (design §5).
	Hello(ctx context.Context, params HelloParams) (HelloResult, error)
	// Ping is the health probe (the 15s loop itself is S1c).
	Ping(ctx context.Context) error
	// Attach binds a session to the base, returning the in-memory lease
	// (design §6 — R3; not exercised before the tool surface lands).
	Attach(ctx context.Context, params AttachParams) (AttachResult, error)
	// Detach releases one lease.
	Detach(ctx context.Context, params DetachParams) (DetachResult, error)
	// ToolCatalog lists visible tools (the ToolsFor semantics, over IPC in S1b).
	ToolCatalog(ctx context.Context, params ToolCatalogParams) (ToolCatalogResult, error)
	// ToolCall executes one tool; progress arrives via base.toolProgress
	// notifications, the response on completion (decision D3).
	ToolCall(ctx context.Context, params ToolCallParams) (ToolCallResult, error)
	// ProviderResolve resolves request-level provider/model/effort; keys never
	// leave the base process (design F1).
	ProviderResolve(ctx context.Context, params ProviderResolveParams) (ProviderResolveResult, error)
	// Shutdown asks the base to wind down (§4; full drain/kill in S1c).
	Shutdown(ctx context.Context) error
	// Close releases the underlying transport/process handles.
	Close() error
}

// InlineBaseClient is the current path: the heavy base keeps being built
// in-process by boot.Build exactly as before (R1 — fallback is the default
// path, not the exceptional one). S1a skeleton: identity and health answer
// locally; the tool/session methods return ErrNotWired until S1b wraps the
// existing base-section code behind them.
//
// TODO(S1b): hold the boot.Build base-section handle and back
// ToolCatalog/ToolCall/Attach/Detach/ProviderResolve with the existing
// in-process implementations, same functions and inputs as today.
type InlineBaseClient struct {
	// ServerVersion is the local build identity reported by Hello.
	ServerVersion string
}

// Mode implements BaseClient.
func (c InlineBaseClient) Mode() Mode { return ModeInline }

// Hello implements BaseClient. In-process there is no version skew (caller
// and server share one binary), so negotiation reduces to the uniform rule.
func (c InlineBaseClient) Hello(_ context.Context, params HelloParams) (HelloResult, error) {
	version, rpcErr := negotiateVersion(params.ProtocolVersion, ProtocolVersion)
	if rpcErr != nil {
		return HelloResult{}, rpcErr
	}
	return HelloResult{
		ProtocolVersion: version,
		ServerVersion:   c.ServerVersion,
		Capabilities:    nil, // no optional capability until S1b wraps the tool surface
	}, nil
}

// Ping implements BaseClient. Inline is the process itself: always alive.
func (c InlineBaseClient) Ping(_ context.Context) error { return nil }

// Attach implements BaseClient.
func (c InlineBaseClient) Attach(_ context.Context, _ AttachParams) (AttachResult, error) {
	return AttachResult{}, ErrNotWired // TODO(S1b): lease accounting around the existing per-tab base
}

// Detach implements BaseClient.
func (c InlineBaseClient) Detach(_ context.Context, _ DetachParams) (DetachResult, error) {
	return DetachResult{}, ErrNotWired // TODO(S1b)
}

// ToolCatalog implements BaseClient.
func (c InlineBaseClient) ToolCatalog(_ context.Context, _ ToolCatalogParams) (ToolCatalogResult, error) {
	return ToolCatalogResult{}, ErrNotWired // TODO(S1b): wrap the existing tool registry ToolsFor path
}

// ToolCall implements BaseClient.
func (c InlineBaseClient) ToolCall(_ context.Context, _ ToolCallParams) (ToolCallResult, error) {
	return ToolCallResult{}, ErrNotWired // TODO(S1b): wrap the existing in-process tool execution
}

// ProviderResolve implements BaseClient.
func (c InlineBaseClient) ProviderResolve(_ context.Context, _ ProviderResolveParams) (ProviderResolveResult, error) {
	return ProviderResolveResult{}, ErrNotWired // TODO(S1b): reuse the existing in-process provider resolution
}

// Shutdown implements BaseClient. Inline owns no extra process to stop.
func (c InlineBaseClient) Shutdown(_ context.Context) error { return nil }

// Close implements BaseClient. Inline owns no transport.
func (c InlineBaseClient) Close() error { return nil }

// RemoteBaseClient reaches the resident subprocess over the framed stdio
// channel. S1a skeleton: Hello/Ping/Shutdown round-trip for real; capability
// surfaces answer locally with ErrNotWired until the server advertises them,
// which keeps an S1a subprocess and an S1b client mutually consistent without
// a wasted guaranteed-failing round trip.
type RemoteBaseClient struct {
	c       *conn
	hello   HelloResult
	onClose func() // subprocess teardown owned by the spawner; may be nil
}

// newRemoteClient wraps a live connection. onClose (may be nil) runs after the
// protocol channel closes — the manager's process teardown.
func newRemoteClient(c *conn, onClose func()) *RemoteBaseClient {
	return &RemoteBaseClient{c: c, onClose: onClose}
}

// Mode implements BaseClient.
func (r *RemoteBaseClient) Mode() Mode { return ModeRemote }

// Hello implements BaseClient and records the negotiated result for
// capability gating.
func (r *RemoteBaseClient) Hello(ctx context.Context, params HelloParams) (HelloResult, error) {
	var res HelloResult
	if err := r.c.call(ctx, MethodHello, params, &res); err != nil {
		return HelloResult{}, err
	}
	if res.ProtocolVersion > ProtocolVersion {
		// A server must never claim a version above what we sent; treat a
		// malformed peer as a handshake failure (the F3 fallback trigger).
		return HelloResult{}, &RPCError{
			Code:    CodeVersionMismatch,
			Message: "server negotiated above the requested version",
		}
	}
	r.hello = res
	return res, nil
}

// HelloResult returns the recorded handshake answer (zero value before Hello).
func (r *RemoteBaseClient) Handshake() HelloResult { return r.hello }

// Ping implements BaseClient.
func (r *RemoteBaseClient) Ping(ctx context.Context) error {
	return r.c.call(ctx, MethodPing, nil, nil)
}

// Attach implements BaseClient (capability-gated; S1b turns the server on).
func (r *RemoteBaseClient) Attach(ctx context.Context, params AttachParams) (AttachResult, error) {
	if !r.hasCapability(CapSessions) {
		return AttachResult{}, ErrNotWired
	}
	var res AttachResult
	err := r.c.call(ctx, MethodAttach, params, &res)
	return res, err
}

// Detach implements BaseClient (capability-gated).
func (r *RemoteBaseClient) Detach(ctx context.Context, params DetachParams) (DetachResult, error) {
	if !r.hasCapability(CapSessions) {
		return DetachResult{}, ErrNotWired
	}
	var res DetachResult
	err := r.c.call(ctx, MethodDetach, params, &res)
	return res, err
}

// ToolCatalog implements BaseClient (capability-gated).
func (r *RemoteBaseClient) ToolCatalog(ctx context.Context, params ToolCatalogParams) (ToolCatalogResult, error) {
	if !r.hasCapability(CapTools) {
		return ToolCatalogResult{}, ErrNotWired
	}
	var res ToolCatalogResult
	err := r.c.call(ctx, MethodToolCatalog, params, &res)
	return res, err
}

// ToolCall implements BaseClient (capability-gated).
func (r *RemoteBaseClient) ToolCall(ctx context.Context, params ToolCallParams) (ToolCallResult, error) {
	if !r.hasCapability(CapTools) {
		return ToolCallResult{}, ErrNotWired
	}
	var res ToolCallResult
	err := r.c.call(ctx, MethodToolCall, params, &res)
	return res, err
}

// ProviderResolve implements BaseClient (capability-gated).
func (r *RemoteBaseClient) ProviderResolve(ctx context.Context, params ProviderResolveParams) (ProviderResolveResult, error) {
	if !r.hasCapability(CapProviderResolve) {
		return ProviderResolveResult{}, ErrNotWired
	}
	var res ProviderResolveResult
	err := r.c.call(ctx, MethodProviderResolve, params, &res)
	return res, err
}

// Shutdown implements BaseClient: the server answers ok, drains in-flight
// handlers within its budget, and its Serve loop returns.
func (r *RemoteBaseClient) Shutdown(ctx context.Context) error {
	return r.c.call(ctx, MethodShutdown, nil, nil)
}

// Close implements BaseClient: protocol channel first, then the process
// teardown hook (stdin EOF offers the graceful path, bounded kill afterwards).
func (r *RemoteBaseClient) Close() error {
	err := r.c.close()
	if r.onClose != nil {
		r.onClose()
	}
	return err
}

func (r *RemoteBaseClient) hasCapability(cap string) bool {
	for _, c := range r.hello.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}
