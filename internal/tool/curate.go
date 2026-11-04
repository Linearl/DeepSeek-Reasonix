package tool

import "context"

// CurateDrop names one already-processed tool result the model wants replaced
// by a one-line note (tl1378 增量1). ID is the host receipt id the result ends
// with (`[receipt r_…]`); note is the annotation that survives in its place.
type CurateDrop struct {
	ID   string `json:"id"`
	Note string `json:"note"`
}

// CurateRequest describes one fine-grained curation pass. Results the request
// does not name stay byte-for-byte — that selectivity is the whole point: the
// prompt-cache damage is bounded by the replaced entries instead of rewriting
// the prefix the way a full fold does.
type CurateRequest struct {
	Drop []CurateDrop
}

// CurateResult is the stable model-facing outcome of one curation pass.
type CurateResult struct {
	Status           string `json:"status"`
	Replaced         int    `json:"replaced"`
	SourceTokens     int    `json:"source_tokens"`
	ProjectionTokens int    `json:"projection_tokens"`
	SavedTokens      int    `json:"saved_tokens"`
	Reason           string `json:"reason"`
}

// ContextCurator is supplied by the Agent that owns the active session, the
// same context-binding shape as ContextCompressor: the tool schema stays
// stable while the projection state stays with the owning session.
type ContextCurator interface {
	CurateContext(ctx context.Context, req CurateRequest) (CurateResult, error)
}

type contextCuratorKey struct{}

// WithContextCurator binds the active session's curator to a tool call.
func WithContextCurator(ctx context.Context, curator ContextCurator) context.Context {
	if curator == nil {
		return ctx
	}
	return context.WithValue(ctx, contextCuratorKey{}, curator)
}

// ContextCuratorFromContext returns the curator bound to this tool call.
func ContextCuratorFromContext(ctx context.Context) (ContextCurator, bool) {
	curator, ok := ctx.Value(contextCuratorKey{}).(ContextCurator)
	return curator, ok && curator != nil
}
