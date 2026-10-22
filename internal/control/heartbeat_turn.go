package control

import (
	"context"
)

// Task 1062 — heartbeat sessions run unattended but carry no autopilot gear
// (设计如此：防递归创建守护对话), so neither the task-477 ask-timeout refusal
// nor the task-109 B4 terminal stop can reach them: an ask the model fires
// mid-heartbeat surfaces an attended panel that nobody answers, and the turn
// hangs without bound (20261022 协调线实锤：prompt 措辞诱导调 ask，面板无人
// 应答即挂死). The heartbeat-turn marker below is the missing "this turn is
// scheduled, not attended" signal.
//
// The marker travels in the turn context — stamped once by
// SubmitHeartbeatTurn inside the runGuarded closure (withRecoveryFenceBindings
// is idempotent and preserves it), read by Ask, and explicitly stripped before
// every task-225 delegate.Ask hop so the parent's own semantics decide. It
// lives for exactly the submitted turn (goal-loop continuations included)
// because the context dies with the turn: no controller state to clear, no
// stale-flag misattribution window.

type heartbeatTurnContextKey struct{}

// withHeartbeatTurn stamps ctx as a heartbeat-driven turn. Stamping is
// add-only; callers never stamp false over an existing marker.
func withHeartbeatTurn(ctx context.Context) context.Context {
	return context.WithValue(ctx, heartbeatTurnContextKey{}, true)
}

// withoutHeartbeatTurn strips the heartbeat-turn marker: task-225 delegation
// forwards a child's ctx to the parent's Ask, and the parent must decide by
// its own posture (attended parent = a human answers; autopilot parent = the
// 477 waits) — a heartbeat child must not turn its parent's panel into a 10s
// refusal, and the delegation hop is the sanctioned escape hatch the 1062
// notice itself points at.
func withoutHeartbeatTurn(ctx context.Context) context.Context {
	return context.WithValue(ctx, heartbeatTurnContextKey{}, false)
}

// heartbeatTurnRequested reports whether ctx belongs to a heartbeat-driven
// turn.
func heartbeatTurnRequested(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(heartbeatTurnContextKey{}).(bool)
	return v
}

// SubmitHeartbeatTurn starts a scheduled turn: identical to SubmitUserTurn
// (plain user turn, no shell/slash interpretation, references resolved)
// except the turn context carries the heartbeat-turn marker, which gives
// every ask fired during the turn the unattended backstop instead of the
// attended unbounded wait. The desktop heartbeat engine calls this for
// every scheduled run, autopilot-guard wakes included. Nothing else changes:
// the turn still runs with the tab's own approval posture, and the session
// still has no autopilot gear (acceptance: 心跳会话仍无 autopilot 档位).
func (c *Controller) SubmitHeartbeatTurn(input, display string) {
	c.runGuarded(func(ctx context.Context) error {
		return c.runRefTurnWithResolverSync(withHeartbeatTurn(ctx), input, input, display, "", c.ResolveRefs)
	})
}
