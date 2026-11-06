package agent

import (
	"context"
	"sync"
)

// The workflow engine budgets a run by what its sub-agents actually spent
// (tl1454), but TaskTool.Execute only surfaces usage as prose inside the host
// envelope for the parent model. This carrier is the same upward pattern as
// the raw-answer sink (subagent_raw_answer.go): the reader installs a mutable
// sink on the ctx handed to Execute, RunProfileSpec records the child's usage
// summary on every exit, and the reader takes it after Execute returns.
// Last write wins: a nested delegation records before its parent finishes, and
// the parent's tracker already folds nested usage into its own totals.

type subagentUsageKey struct{}

type subagentUsageSink struct {
	mu       sync.Mutex
	usage    SubagentUsageSummary
	recorded bool
}

// WithSubagentUsageSink returns ctx carrying an empty usage sink.
func WithSubagentUsageSink(ctx context.Context) context.Context {
	return context.WithValue(ctx, subagentUsageKey{}, &subagentUsageSink{})
}

// RecordSubagentUsage stores one child run's usage summary. A no-op when ctx
// carries no sink (ordinary delegation reads the footer line instead).
func RecordSubagentUsage(ctx context.Context, usage SubagentUsageSummary) {
	sink, ok := ctx.Value(subagentUsageKey{}).(*subagentUsageSink)
	if !ok {
		return
	}
	sink.mu.Lock()
	sink.usage = usage
	sink.recorded = true
	sink.mu.Unlock()
}

// SubagentUsage returns the usage recorded during the run; ok is false when
// no sink is installed or nothing was recorded.
func SubagentUsage(ctx context.Context) (SubagentUsageSummary, bool) {
	sink, ok := ctx.Value(subagentUsageKey{}).(*subagentUsageSink)
	if !ok {
		return SubagentUsageSummary{}, false
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return sink.usage, sink.recorded
}
