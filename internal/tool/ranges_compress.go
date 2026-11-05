package tool

import "context"

// RangesFoldRequest describes one model-initiated small-segment fold
// (tl1419). Unlike the whole-range compress tool there is no anchor: the
// host selects the un-folded segment (newest summary boundary → active turn)
// itself, so the model only steers the note content via Focus.
type RangesFoldRequest struct {
	Focus string
}

// RangesFoldResult is the stable model-facing outcome of a ranges fold.
// Status is "ok" or "rejected"; a rejection always carries Reason with the
// failing gate and its billing-ruler readings (失败可见性: the model must be
// able to decide retry-vs-wait from the result alone).
type RangesFoldResult struct {
	Status           string `json:"status"`
	Reason           string `json:"reason,omitempty"`
	SegmentTokens    int    `json:"segment_tokens,omitempty"`
	SourceTokens     int    `json:"source_tokens,omitempty"`
	ProjectionTokens int    `json:"projection_tokens,omitempty"`
	FoldIndex        int    `json:"fold_index,omitempty"`
}

// RangesFolder is supplied by the Agent that owns the active session — the
// same session-binding protocol as ContextCompressor (the agent owns the
// projection state, the schema stays static).
type RangesFolder interface {
	RangesFoldCompact(context.Context, RangesFoldRequest) (RangesFoldResult, error)
}

type rangesFolderKey struct{}

// WithRangesFolder binds the active session's ranges folder to a tool call.
func WithRangesFolder(ctx context.Context, folder RangesFolder) context.Context {
	if folder == nil {
		return ctx
	}
	return context.WithValue(ctx, rangesFolderKey{}, folder)
}

// RangesFolderFromContext returns the ranges folder bound to this tool call.
func RangesFolderFromContext(ctx context.Context) (RangesFolder, bool) {
	folder, ok := ctx.Value(rangesFolderKey{}).(RangesFolder)
	return folder, ok && folder != nil
}
