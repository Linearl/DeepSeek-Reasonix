package baseproc

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"reasonix/internal/tool"
)

// ErrNotWired marks protocol surface that is defined on the wire but has no
// behaviour behind it yet: attach/detach lease accounting and
// providerResolve (later slices), an inline client with no Surface attached,
// or a remote server that does not advertise the capability. Callers treat it
// as "take the pre-S1 local path" — never as a failure.
var ErrNotWired = errors.New("baseproc: method not wired in this slice")

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
// path, not the exceptional one). Identity and health answer locally; the
// S1b tool surface is backed by Surface when the caller attached one (boot
// hands over the registry it just built), and returns ErrNotWired otherwise —
// which is how an S1a-era inline client keeps behaving after S1b lands.
//
// TODO(S1c): Attach/Detach lease accounting and ProviderResolve still return
// ErrNotWired.
type InlineBaseClient struct {
	// ServerVersion is the local build identity reported by Hello.
	ServerVersion string
	// Surface backs ToolCatalog/ToolCall in-process (S1b). nil keeps the S1a
	// ErrNotWired semantics.
	Surface ToolSurface
}

// Mode implements BaseClient.
func (c InlineBaseClient) Mode() Mode { return ModeInline }

// Hello implements BaseClient. In-process there is no version skew (caller
// and server share one binary), so negotiation reduces to the uniform rule.
// The tools capability mirrors the attached Surface: an inline client with a
// surface behaves like a subprocess that advertised CapTools.
func (c InlineBaseClient) Hello(_ context.Context, params HelloParams) (HelloResult, error) {
	version, rpcErr := negotiateVersion(params.ProtocolVersion, ProtocolVersion)
	if rpcErr != nil {
		return HelloResult{}, rpcErr
	}
	var caps []string
	if c.Surface != nil {
		caps = []string{CapTools}
	}
	return HelloResult{
		ProtocolVersion: version,
		ServerVersion:   c.ServerVersion,
		Capabilities:    caps,
	}, nil
}

// Ping implements BaseClient. Inline is the process itself: always alive.
func (c InlineBaseClient) Ping(_ context.Context) error { return nil }

// Attach implements BaseClient.
func (c InlineBaseClient) Attach(_ context.Context, _ AttachParams) (AttachResult, error) {
	return AttachResult{}, ErrNotWired // TODO(S1c): lease accounting around the existing per-tab base
}

// Detach implements BaseClient.
func (c InlineBaseClient) Detach(_ context.Context, _ DetachParams) (DetachResult, error) {
	return DetachResult{}, ErrNotWired // TODO(S1c)
}

// ToolCatalog implements BaseClient: the same query the remote path sends
// over base.toolCatalog, answered here from the in-process surface — the S1b
// parity test (switch off = current path, switch on = the same query over the
// channel) compares exactly these two answers.
func (c InlineBaseClient) ToolCatalog(ctx context.Context, params ToolCatalogParams) (ToolCatalogResult, error) {
	if c.Surface == nil {
		return ToolCatalogResult{}, ErrNotWired
	}
	tools, err := c.Surface.Catalog(ctx, params.Scope)
	if err != nil {
		return ToolCatalogResult{}, err
	}
	return ToolCatalogResult{Tools: tools}, nil
}

// ToolCall implements BaseClient: the same validation and the same ctx-stamped
// progress source as the remote path, then straight into the in-process
// surface (R1 — the switch-off answer).
func (c InlineBaseClient) ToolCall(ctx context.Context, params ToolCallParams) (ToolCallResult, error) {
	if c.Surface == nil {
		return ToolCallResult{}, ErrNotWired
	}
	if rpcErr := validateToolCallParams(params); rpcErr != nil {
		return ToolCallResult{}, rpcErr
	}
	sink, _ := tool.ProgressFrom(ctx)
	return c.Surface.Execute(ctx, params, sink)
}

// ProviderResolve implements BaseClient.
func (c InlineBaseClient) ProviderResolve(_ context.Context, _ ProviderResolveParams) (ProviderResolveResult, error) {
	return ProviderResolveResult{}, ErrNotWired // TODO(S1c): reuse the existing in-process provider resolution
}

// Shutdown implements BaseClient. Inline owns no extra process to stop.
func (c InlineBaseClient) Shutdown(_ context.Context) error { return nil }

// Close implements BaseClient. Inline owns no transport.
func (c InlineBaseClient) Close() error { return nil }

// RemoteBaseClient reaches the resident subprocess over the framed stdio
// channel. Hello/Ping/Shutdown round-trip for real (S1a); the S1b tool face
// forwards toolCatalog/toolCall under the CapTools capability gate and routes
// base.toolProgress notifications back to the per-call progress sink stamped
// into the ToolCall context (decision D3).
type RemoteBaseClient struct {
	c       *conn
	hello   HelloResult
	onClose func() // subprocess teardown owned by the spawner; may be nil

	// progress routes base.toolProgress chunks by call_id for the duration of
	// one ToolCall. A chunk for an unknown call_id (it arrived after the call
	// completed, or the id is foreign) is stale by definition and dropped.
	progressMu sync.Mutex
	progress   map[string]func(chunk string)

	// outerNotify is the manager-supplied callback (S1a Options.Notify) that
	// still receives every notification the progress router does not own —
	// base.catalogChanged today, base.dying in S1c.
	outerNotify func(method string, params json.RawMessage)
}

// newRemoteClient wraps a live connection. onClose (may be nil) runs after the
// protocol channel closes — the manager's process teardown. The client takes
// over the connection's notification dispatch: progress chunks go to the
// per-call router, everything else passes through to the caller's callback.
func newRemoteClient(c *conn, onClose func()) *RemoteBaseClient {
	r := &RemoteBaseClient{
		c:        c,
		onClose:  onClose,
		progress: make(map[string]func(chunk string)),
	}
	r.outerNotify = c.swapNotify(r.dispatchNotify)
	return r
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

// Attach implements BaseClient (capability-gated; a later slice turns the
// server side on).
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

// ToolCall implements BaseClient (capability-gated): the request rides the
// channel, progress chunks come back as base.toolProgress notifications
// routed to the sink stamped into ctx, and the response fires on completion
// (decision D3). A dead channel fails the call immediately — conn teardown
// answers every pending call — so a broken channel can never wedge a caller.
func (r *RemoteBaseClient) ToolCall(ctx context.Context, params ToolCallParams) (ToolCallResult, error) {
	if !r.hasCapability(CapTools) {
		return ToolCallResult{}, ErrNotWired
	}
	if rpcErr := validateToolCallParams(params); rpcErr != nil {
		return ToolCallResult{}, rpcErr
	}
	// The agent stamps its sink into the call context today (execute_one's
	// tool.WithProgress); picking it up here is the D3 sink adapter — server
	// chunks re-enter the exact ToolProgress event shape the frontend already
	// renders, with zero frontend change.
	if sink, ok := tool.ProgressFrom(ctx); ok && params.CallID != "" {
		r.trackProgress(params.CallID, sink)
		defer r.untrackProgress(params.CallID)
	}
	var res ToolCallResult
	err := r.c.call(ctx, MethodToolCall, params, &res)
	return res, err
}

// dispatchNotify splits server notifications: base.toolProgress goes to the
// per-call router, everything else to the manager-supplied callback.
func (r *RemoteBaseClient) dispatchNotify(method string, params json.RawMessage) {
	if method == NotifyToolProgress {
		var p ToolProgressParams
		if err := json.Unmarshal(params, &p); err != nil || p.CallID == "" {
			return // malformed/uncorrelatable chunk: drop, never fail the call
		}
		r.progressMu.Lock()
		sink := r.progress[p.CallID]
		r.progressMu.Unlock()
		if sink != nil {
			sink(p.Chunk)
		}
		return
	}
	if r.outerNotify != nil {
		r.outerNotify(method, params)
	}
}

func (r *RemoteBaseClient) trackProgress(callID string, sink func(chunk string)) {
	r.progressMu.Lock()
	r.progress[callID] = sink
	r.progressMu.Unlock()
}

func (r *RemoteBaseClient) untrackProgress(callID string) {
	r.progressMu.Lock()
	delete(r.progress, callID)
	r.progressMu.Unlock()
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
