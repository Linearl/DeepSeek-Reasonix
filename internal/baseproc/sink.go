package baseproc

import (
	"context"

	"reasonix/internal/event"
	"reasonix/internal/nilutil"
	"reasonix/internal/tool"
)

// ToolEventSink is the existing output surface: desktop's tabEventSink and
// every frontend/test sink satisfy it with the same Emit shape (design D3/R6 —
// adapt to the existing sink, never rewrite the subscription model).
type ToolEventSink interface {
	Emit(event.Event)
}

// WithToolProgress stamps ctx so a BaseClient.ToolCall — inline or remote —
// streams progress into the existing sink as event.ToolProgress carrying the
// same ID+chunk pair the in-process agent stamps today (execute_one's
// tool.WithProgress). Remote chunks arrive as base.toolProgress notifications
// and re-enter through this very stamped function, so the frontend renders
// both paths with zero change (design R6: 适配器而非重写订阅模型).
//
// The agent does not need this helper for its own calls — it already stamps
// its sink the same way — but any other caller holding a sink and a call_id
// (boot-level harnesses, tests, future server-side emitters) does.
func WithToolProgress(ctx context.Context, sink ToolEventSink, callID string) context.Context {
	if nilutil.IsNil(sink) || callID == "" {
		return ctx
	}
	return tool.WithProgress(ctx, func(chunk string) {
		sink.Emit(event.Event{
			Kind: event.ToolProgress,
			Tool: event.Tool{ID: callID, Output: chunk},
		})
	})
}
