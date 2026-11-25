package control

import (
	"context"

	"reasonix/internal/agent"
	"reasonix/internal/tool"
)

// bindTurnScope binds a Goal turn's spend budget and its usage recorder, the
// latter active until the FSM commits. Neither chat nor Goal gets a round
// ceiling: rounds carry no information the spend axes lack. An explicit
// max_steps still owns either turn. tl1472: every orchestrated turn also
// carries the host goal creator so the model's create_goal works with or
// without goal mode (双通道创建·模型通道).
func (c *Controller) bindTurnScope(ctx context.Context, continuation *goalContinuationSnapshot) context.Context {
	ctx = tool.WithGoalCreator(ctx, goalCreatorForController(c))
	goalScopeID, goalScoped := c.goals.goalScopeIDForTurn(continuation)
	if !goalScoped {
		// tl1459: a turn that is not a Goal turn because the Goal stopped or
		// was cleared tells update_goal why, instead of a bare refusal.
		if reason := c.goals.goalTurnUnavailableReason(); reason != "" {
			ctx = tool.WithGoalTurnUnavailableReason(ctx, reason)
		}
		return ctx
	}
	ctx = agent.WithTaskBudget(ctx, c.goalTaskBudget(heartbeatTurnRequested(ctx)))
	recorder := c.goals.newTurnRecorder(goalScopeID, c.goals.continuationToken())
	c.goalUsageTee.setActiveRecorder(recorder)
	return tool.WithGoalTurnRecorder(ctx, recorder)
}

// defaultGoalContinuationTokenBudget (tl1312) is the ceiling a heartbeat
// goal-mode continuation runs under when no spend axis is configured at all
// (no [agent].goal_token_budget, no task cost budget, no task time budget).
// It bounds the default state without inventing a stop cause: crossing it
// lands on the existing budget_spend chain (agent task-budget pause →
// goalPauseFromRunError → FSM pause), exactly the chain a user-configured
// budget uses. ~40-200 heavy agentic rounds: real work finishes inside it, a
// runaway loop stops inside it, and a heartbeat run that false-positives
// self-heals — the next scheduled run re-grants a fresh slice. Var, not
// const, so tests can exercise the crossing without burning 2M tokens.
var defaultGoalContinuationTokenBudget = 2_000_000

// goalTaskBudget is the spend gate a Goal turn runs under: the shared budget
// plus the Goal-only token axis. An interactive Goal still has no default —
// it runs until it finishes, reaches a genuine blocker, or the user stops it
// (someone is watching). tl1312: a heartbeat-driven Goal turn (the goalMode
// continuation surface, marker stamped by SubmitHeartbeatTurn and alive
// through goal-loop continuations) with every spend axis unset runs under the
// default token ceiling instead of unbounded — nobody is watching that loop.
// Any configured axis wins as-is; the default never stacks onto it.
func (c *Controller) goalTaskBudget(heartbeat bool) agent.TaskBudget {
	b := c.taskBudget
	b.Tokens = c.goalTokenBudget
	if heartbeat && b.Tokens <= 0 && b.Cost <= 0 && b.Wall <= 0 {
		b.Tokens = defaultGoalContinuationTokenBudget
	}
	return b
}

// goalSpendBudgetConfigured reports whether any Goal spend axis carries an
// explicit user configuration. turn_orchestrator uses it to name the default
// ceiling as the default when a budget pause fires without one.
func (c *Controller) goalSpendBudgetConfigured() bool {
	return c.goalTokenBudget > 0 || c.taskBudget.Cost > 0 || c.taskBudget.Wall > 0
}

// goalCreatorForController adapts the controller to the tool.GoalCreator
// port. Separate method so the adaptation stays lock-free and trivially
// testable.
func goalCreatorForController(c *Controller) tool.GoalCreator {
	if c == nil {
		return nil
	}
	return controllerGoalCreator{c: c}
}

type controllerGoalCreator struct{ c *Controller }

func (g controllerGoalCreator) CreateGoal(text, summary string) (string, error) {
	return g.c.CreateGoalByModel(text, summary)
}
