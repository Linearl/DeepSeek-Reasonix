package tool

import "context"

// CompressToolName is the model-side context compression tool's registry name.
// tl1719: shared by the builtin registration and the capability-list
// discoverability exception — the one model-invocable compaction entry stays
// listed in use_capability list even though its schema rides the provider
// surface.
const CompressToolName = "compress"

// CompressRequest describes one explicit, model-requested context fold.
type CompressRequest struct {
	Direction string
	Anchor    string
	Focus     string
}

// CompressResult is the stable model-facing outcome of a context fold.
type CompressResult struct {
	Status           string `json:"status"`
	Direction        string `json:"direction"`
	Anchor           string `json:"anchor"`
	Messages         int    `json:"messages"`
	SourceTokens     int    `json:"source_tokens"`
	ProjectionTokens int    `json:"projection_tokens"`
	Mode             string `json:"mode"`
	Reason           string `json:"reason"`
}

// ContextCompressor is supplied by the Agent that owns the active session.
// Keeping the binding on the call context lets parent and child agents share a
// stable tool schema without sharing projection state.
type ContextCompressor interface {
	CompressContext(context.Context, CompressRequest) (CompressResult, error)
}

type contextCompressorKey struct{}

// WithContextCompressor binds the active session's compressor to a tool call.
func WithContextCompressor(ctx context.Context, compressor ContextCompressor) context.Context {
	if compressor == nil {
		return ctx
	}
	return context.WithValue(ctx, contextCompressorKey{}, compressor)
}

// ContextCompressorFromContext returns the compressor bound to this tool call.
func ContextCompressorFromContext(ctx context.Context) (ContextCompressor, bool) {
	compressor, ok := ctx.Value(contextCompressorKey{}).(ContextCompressor)
	return compressor, ok && compressor != nil
}
