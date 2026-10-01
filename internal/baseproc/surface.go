package baseproc

import (
	"context"
	"errors"
	"fmt"

	"reasonix/internal/tool"
)

// Catalog scope vocabulary (S1b decision for ToolCatalogParams.Scope: it
// mirrors the in-process visibility filters — Registry.ContractEntries is the
// provider-visible subset, Registry.AllContractEntries is everything). An
// unknown scope is rejected with ErrUnknownScope, never silently widened or
// narrowed: a typo must not quietly change which tools a caller sees.
const (
	// ScopeAll lists every registered tool, including entries hidden from the
	// provider schema (use_capability targets).
	ScopeAll = "all"
	// ScopeProvider lists only the provider-visible subset.
	ScopeProvider = "provider"
)

// ErrUnknownScope rejects an unknown ToolCatalogParams.Scope. The serve-side
// handler maps it to JSON-RPC -32602; the inline path surfaces it as-is.
var ErrUnknownScope = errors.New("baseproc: unknown tool catalog scope")

// ToolSurface is the heavy base's tool face behind the protocol (design §5,
// slice S1b): one catalog query plus one execution entry. It exists twice at
// runtime — the inline client calls an in-process instance directly (R1), the
// subprocess serves one over IPC — so both BaseClient implementations walk
// the same functions with the same inputs (design R2: one shared description
// shape, a thin adaptation layer).
type ToolSurface interface {
	// Catalog lists the tools visible under scope (ScopeAll/ScopeProvider).
	Catalog(ctx context.Context, scope string) ([]ToolDescriptor, error)
	// Execute runs one tool call. progress (may be nil) receives output chunks
	// as they are produced — decision D3's per-call streaming, correlated
	// upstream by call_id; the returned result is the completion answer.
	// Tool-level failures are a *completed* call: they land in
	// ToolCallResult.Err (the model sees the error text), never in the
	// returned error, which is reserved for surface/infrastructure faults.
	Execute(ctx context.Context, call ToolCallParams, progress func(chunk string)) (ToolCallResult, error)
}

// RegistrySurface backs a ToolSurface with the existing tool.Registry — the
// R1 dual-implementation rule concentrated in one type: the subprocess serves
// it over base.toolCatalog/base.toolCall and InlineBaseClient calls it
// directly, so "switch off = current behaviour, switch on = same behaviour
// over the channel" holds by construction (same functions, same inputs).
type RegistrySurface struct {
	// Reg is the registry the surface serves. The subprocess side hosts its
	// own registry; the inline side hands over the session registry boot just
	// built (TODO(S1c+): base-side registry hosting lands with workspace-root
	// attach — until the subprocess advertises CapTools the remote gates fall
	// back inline per R1).
	Reg *tool.Registry
}

// Catalog implements ToolSurface.
func (s *RegistrySurface) Catalog(_ context.Context, scope string) ([]ToolDescriptor, error) {
	if s == nil || s.Reg == nil {
		return nil, errors.New("baseproc: tool surface has no registry")
	}
	var entries []tool.ContractEntry
	switch scope {
	case ScopeAll:
		entries = s.Reg.AllContractEntries()
	case ScopeProvider:
		entries = s.Reg.ContractEntries()
	default:
		return nil, fmt.Errorf("%w: %q (want %q or %q)", ErrUnknownScope, scope, ScopeAll, ScopeProvider)
	}
	out := make([]ToolDescriptor, 0, len(entries))
	for _, e := range entries {
		out = append(out, ToolDescriptor{
			Name:        e.Name,
			Description: e.Description,
			Schema:      e.Schema,
			ReadOnly:    e.ReadOnly,
		})
	}
	return out, nil
}

// Execute implements ToolSurface: exact-name lookup, progress stamped into the
// call context (tools read it back via tool.ProgressFrom, exactly as the
// in-process agent path does), and tool-level errors folded into
// ToolCallResult.Err per the v1 contract.
func (s *RegistrySurface) Execute(ctx context.Context, call ToolCallParams, progress func(chunk string)) (ToolCallResult, error) {
	if s == nil || s.Reg == nil {
		return ToolCallResult{}, errors.New("baseproc: tool surface has no registry")
	}
	target, ok := s.Reg.Get(call.Tool)
	if !ok {
		// An unknown tool is a completed call: the caller (eventually the
		// model) sees "no such tool" as the result text instead of a
		// transport failure.
		return ToolCallResult{Err: fmt.Sprintf("unknown tool %q", call.Tool)}, nil
	}
	if progress != nil {
		ctx = tool.WithProgress(ctx, progress)
	}
	content, err := target.Execute(ctx, call.Args)
	if err != nil {
		return ToolCallResult{Err: err.Error()}, nil
	}
	return ToolCallResult{Content: content}, nil
}
