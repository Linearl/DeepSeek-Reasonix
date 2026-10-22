package agent

import "context"

// ContinuationPolicy is the internal host policy for synthetic same-Run
// continuation. It is not a user configuration key.
type ContinuationPolicy uint8

const (
	// ContinuationDisabled is the default: a clean terminal ends the Run after
	// hard safety/readiness gates. Ordinary agents leave this unset.
	ContinuationDisabled ContinuationPolicy = iota
	// ContinuationExplicitFlow opts a dedicated Goal/review/guardian/typed-report
	// run into host-owned continuation helpers.
	ContinuationExplicitFlow
	// ContinuationStandardFlow (task 1070) opts an ordinary Standard session
	// into the bounded same-Run continuation helpers (executor-handoff nudge,
	// Standard todo continuation, stalled-intent repair, todo-progress nudge).
	// It is deliberately a separate value: the dedicated flows keep their
	// ExplicitFlow semantics (EBM and the goal/review/guardian boundary
	// decisions still read ContinuationExplicitFlow only), and this flow is
	// armed only by the lab switch standard_continuation (default off) — the
	// tag alone is inert without the agent-side boot snapshot, so an
	// already-running agent that was built with the switch off never opens the
	// gate no matter what the controller tags. Safety exclusions are unchanged:
	// every helper still runs its own eligibility gates (plan mode / read-only
	// / ForbidMutation / subagent depth / bounded rounds / no-progress stop).
	ContinuationStandardFlow
)

type continuationPolicyKey struct{}

// WithContinuationPolicy opts one Run into an explicit continuation flow.
func WithContinuationPolicy(ctx context.Context, policy ContinuationPolicy) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, continuationPolicyKey{}, policy)
}

func continuationPolicyFromContext(ctx context.Context) (ContinuationPolicy, bool) {
	if ctx == nil {
		return ContinuationDisabled, false
	}
	policy, ok := ctx.Value(continuationPolicyKey{}).(ContinuationPolicy)
	return policy, ok
}

func (a *Agent) hostContinuationEnabled(ctx context.Context) bool {
	if a == nil {
		return false
	}
	policy, ok := continuationPolicyFromContext(ctx)
	if !ok {
		policy = a.continuationPolicy
	}
	if policy == ContinuationExplicitFlow {
		return true
	}
	// Task 1070: the Standard flow reuses the same continuation helpers but
	// only when the lab switch (boot snapshot) armed this agent AND the run
	// is not an ExplicitFlow run — the dedicated flows must not change
	// semantics by sharing the switch.
	return policy == ContinuationStandardFlow && a.standardContinuation
}

// stalledIntentNudgeEnabled reports whether the stalled-intent repair may fire
// this run. It is true when ContinuationExplicitFlow is active (upstream path)
// or when the user opted in via config (task 117). The task-1070 Standard flow
// reaches it through hostContinuationEnabled.
func (a *Agent) stalledIntentNudgeEnabled(ctx context.Context) bool {
	if a == nil {
		return false
	}
	if a.hostContinuationEnabled(ctx) {
		return true
	}
	return a.stalledIntentNudge
}

// stalledIntentNudgeCap returns the effective per-run nudge limit.
func (a *Agent) stalledIntentNudgeCap() int {
	if a == nil || a.stalledIntentNudgeLimit <= 0 {
		return maxStalledIntentNudges
	}
	return a.stalledIntentNudgeLimit
}
