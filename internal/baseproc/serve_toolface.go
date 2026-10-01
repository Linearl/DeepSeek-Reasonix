package baseproc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// AttachToolSurface registers the S1b tool face on this server:
// base.toolCatalog (and, in the tool-call slice, base.toolCall) plus the CapTools
// capability base.hello advertises, so a client can tell "server build has no
// tool surface" apart from "method exists but failed" without paying a
// guaranteed -32601 round trip (the S1a capability-gating contract).
//
// Call before Serve. A nil surface is ignored — a serve process that has not
// built its base registry yet keeps the S1a core-only behaviour: no
// capability, unknown methods answer -32601, connection stays usable
// (design §5 contract discipline).
func (s *Server) AttachToolSurface(surface ToolSurface) {
	if surface == nil {
		return
	}
	s.mu.Lock()
	s.surface = surface
	s.mu.Unlock()
	s.Register(MethodToolCatalog, guardHandler("base.toolCatalog", s.handleToolCatalog))
	s.Register(MethodToolCall, guardHandler("base.toolCall", s.handleToolCall))
	s.AddCapabilities(CapTools)
}

// toolSurface loads the attached surface under the server lock. Attach happens
// before Serve; handlers run concurrently (matrix C1), so the read is guarded.
func (s *Server) toolSurface() ToolSurface {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.surface
}

// guardHandler converts a panicking handler into a CodeInternalError answer
// instead of letting the panic escape the dispatch goroutine and take the
// subprocess down mid-call: a tool crash must fail *that* call explicitly,
// with the channel still usable for the next one (contrast: the inline path
// propagates the panic to its caller exactly as it does today — the divergence
// exists only on the failure path and is documented in the S1b report).
func guardHandler(name string, fn func(ctx context.Context, params json.RawMessage) (any, error)) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (res any, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = &RPCError{
					Code:    CodeInternalError,
					Message: fmt.Sprintf("%s handler panicked: %v", name, r),
				}
			}
		}()
		return fn(ctx, params)
	}
}

// handleToolCatalog answers base.toolCatalog from the attached surface
// (design §5: 现 ToolsFor/registry semantics over IPC, scope mirrors the
// in-process visibility filters).
func (s *Server) handleToolCatalog(ctx context.Context, params json.RawMessage) (any, error) {
	var p ToolCatalogParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &RPCError{Code: CodeInvalidParams, Message: fmt.Sprintf("toolCatalog params: %v", err)}
		}
	}
	surface := s.toolSurface()
	if surface == nil {
		// AttachToolSurface registered this handler together with the
		// surface; reaching here means an inconsistent server wiring.
		return nil, errors.New("baseproc: tool surface not attached")
	}
	tools, err := surface.Catalog(ctx, p.Scope)
	if err != nil {
		if errors.Is(err, ErrUnknownScope) {
			return nil, &RPCError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return nil, err
	}
	return ToolCatalogResult{Tools: tools}, nil
}

// validateToolCallParams pins the v1 call contract on BOTH implementations:
// call_id correlates the base.toolProgress stream (design D3) and tool names
// the target. The inline client runs the same check so a caller observes one
// error shape whether the switch is on or off.
func validateToolCallParams(p ToolCallParams) *RPCError {
	if p.CallID == "" {
		return &RPCError{Code: CodeInvalidParams, Message: "call_id is required (progress correlation)"}
	}
	if p.Tool == "" {
		return &RPCError{Code: CodeInvalidParams, Message: "tool is required"}
	}
	return nil
}

// handleToolCall executes one tool from the attached surface (design §5/D3):
// progress chunks ride base.toolProgress notifications keyed by call_id while
// the RPC response only fires on completion, so a minute-long bash run cannot
// wedge the control channel.
func (s *Server) handleToolCall(ctx context.Context, params json.RawMessage) (any, error) {
	var p ToolCallParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: CodeInvalidParams, Message: fmt.Sprintf("toolCall params: %v", err)}
	}
	if rpcErr := validateToolCallParams(p); rpcErr != nil {
		return nil, rpcErr
	}
	surface := s.toolSurface()
	if surface == nil {
		return nil, errors.New("baseproc: tool surface not attached")
	}
	progress := func(chunk string) {
		// Best effort: a failed write means the channel is already dying, and
		// the pending call then fails through connection teardown. Progress
		// loss must never wedge or abort the tool itself.
		_ = s.Notify(NotifyToolProgress, ToolProgressParams{CallID: p.CallID, Chunk: chunk})
	}
	return surface.Execute(ctx, p, progress)
}
