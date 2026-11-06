package tool

import "context"

// GoalReport is the model's structured per-turn goal disposition recorded via
// the update_goal tool. It only carries candidate state: the host commits the
// real FSM transition after the turn ends and Delivery readiness and budget
// checks pass.
type GoalReport struct {
	// Status is one of "continue", "complete", or "blocked".
	Status string
	// Reason is the short explanation (required for continue and blocked).
	Reason string
	// NextAction is an optional concrete next step (recommended for continue).
	NextAction string
}

// GoalTurnRecorder records the model's update_goal report for the active goal
// turn. The host controller implements it; it is absent in ordinary chat, where
// the update_goal tool must fail closed without changing any state.
type GoalTurnRecorder interface {
	// RecordGoalReport validates the report against the turn's goal lifecycle
	// (scope/epoch binding, idempotency, terminal-state conflicts) and returns
	// the tool-result text shown to the model.
	RecordGoalReport(r GoalReport) (string, error)
}

type goalTurnRecorderKey struct{}

// noGoalTurnRecorder shadows an ancestor recorder while preserving the rest
// of the context chain. Child agents must not report disposition for the
// parent's goal turn.
type noGoalTurnRecorder struct{}

// WithGoalTurnRecorder stamps ctx with the per-turn goal recorder so the
// update_goal tool can reach it from inside the run loop.
func WithGoalTurnRecorder(ctx context.Context, r GoalTurnRecorder) context.Context {
	if r == nil {
		return ctx
	}
	return context.WithValue(ctx, goalTurnRecorderKey{}, r)
}

// WithoutGoalTurnRecorder returns a child context that cannot access a goal
// recorder inherited from its parent. Other values and cancellation continue
// to flow through the context normally.
func WithoutGoalTurnRecorder(ctx context.Context) context.Context {
	return context.WithValue(ctx, goalTurnRecorderKey{}, noGoalTurnRecorder{})
}

// GoalTurnRecorderFromContext returns the active goal turn's recorder, if any.
func GoalTurnRecorderFromContext(ctx context.Context) (GoalTurnRecorder, bool) {
	if ctx == nil {
		return nil, false
	}
	r, ok := ctx.Value(goalTurnRecorderKey{}).(GoalTurnRecorder)
	return r, ok && r != nil
}

type goalTurnUnavailableKey struct{}

// WithGoalTurnUnavailableReason (tl1459) records why a turn that carries no
// goal recorder is not a goal turn: the Goal stopped, paused, or was cleared.
// update_goal returns it to the model instead of a bare refusal, so the model
// can tell the user what happened rather than retry blindly. Plain chat leaves
// it empty.
func WithGoalTurnUnavailableReason(ctx context.Context, reason string) context.Context {
	if reason == "" {
		return ctx
	}
	return context.WithValue(ctx, goalTurnUnavailableKey{}, reason)
}

// GoalTurnUnavailableReason returns the reason set by
// WithGoalTurnUnavailableReason, or "".
func GoalTurnUnavailableReason(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	reason, _ := ctx.Value(goalTurnUnavailableKey{}).(string)
	return reason
}

// UpdateGoalUnavailableMessage is the one refusal text for update_goal outside
// a goal turn; the provider-visibility gate and the tool itself both use it.
func UpdateGoalUnavailableMessage(ctx context.Context) string {
	msg := "update_goal is only available while an active goal turn is running — no goal state was changed"
	if reason := GoalTurnUnavailableReason(ctx); reason != "" {
		msg += " (" + reason + ")"
	}
	return msg
}
