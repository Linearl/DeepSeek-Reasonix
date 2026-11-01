package agent

import (
	"context"
	"sync"
)

// The workflow engine parses sub-agent answers programmatically (typed
// results, confirm verdicts), but the answer string TaskTool.Execute returns
// is host-assembled: composeSubagentAnswer may prefix the adjudicated
// completion report ("status: …", "summary: …") and append host receipts.
// Those envelopes are for the parent MODEL, not for a parser — a typed result
// wrapped this way cannot unmarshal (gap G1, audit 2026-11-01).
//
// This bypass carries the child's UNMODIFIED final answer alongside the
// wrapped one: the caller that holds the ctx handed to Execute installs a
// sink, the host records the raw answer into it at assembly time, and the
// caller reads it back after Execute returns.
//
// A plain context.WithValue cannot do this job: values only flow downward
// (parent → child ctx), while the raw answer must flow UP from deep inside
// Execute to the caller. A mutable carrier installed by the reader is the
// same upward pattern the host already uses for child evidence
// (evidence.FromContext + MergeChild) and keeps Execute's (string, error)
// contract untouched.

// subagentRawAnswerKey is the ctx key for the raw-answer sink. Unexported:
// the only door in is WithSubagentRawAnswerSink.
type subagentRawAnswerKey struct{}

// subagentRawAnswerSink is the mutable carrier. Writes and reads happen on
// the same run goroutine today; the mutex keeps that an implementation
// detail rather than a correctness assumption (background sub-agents may
// legally outlive the dispatching call frame).
type subagentRawAnswerSink struct {
	mu  sync.Mutex
	raw string
}

// WithSubagentRawAnswerSink returns ctx carrying an empty raw-answer sink.
// Install it on the context handed to TaskTool.Execute /
// ReadOnlyTaskTool.Execute when the answer text will be parsed
// programmatically rather than read by a model.
func WithSubagentRawAnswerSink(ctx context.Context) context.Context {
	return context.WithValue(ctx, subagentRawAnswerKey{}, &subagentRawAnswerSink{})
}

// RecordSubagentRawAnswer stores the child's final answer exactly as the
// child wrote it — call BEFORE any host wrapping is applied. A no-op when ctx
// carries no sink (ordinary delegation: the parent model reads the wrapped
// answer) or when raw is empty (nothing was answered).
func RecordSubagentRawAnswer(ctx context.Context, raw string) {
	sink, ok := ctx.Value(subagentRawAnswerKey{}).(*subagentRawAnswerSink)
	if !ok || raw == "" {
		return
	}
	sink.mu.Lock()
	sink.raw = raw
	sink.mu.Unlock()
}

// SubagentRawAnswer returns the child's unmodified final answer recorded
// during the run, or "" when no sink is present or the host recorded nothing
// (callers must fall back to the answer text Execute returned).
func SubagentRawAnswer(ctx context.Context) string {
	sink, ok := ctx.Value(subagentRawAnswerKey{}).(*subagentRawAnswerSink)
	if !ok {
		return ""
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return sink.raw
}
