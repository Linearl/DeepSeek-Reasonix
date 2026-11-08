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
	ctx = agent.WithTaskBudget(ctx, c.goalTaskBudget())
	recorder := c.goals.newTurnRecorder(goalScopeID, c.goals.continuationToken())
	c.goalUsageTee.setActiveRecorder(recorder)
	return tool.WithGoalTurnRecorder(ctx, recorder)
}

// goalTaskBudget is the spend gate a Goal turn runs under: the shared budget
// plus the Goal-only token axis. Nothing here has a default — a Goal runs
// until it finishes, reaches a genuine blocker, or the user stops it. Anyone
// who wants a ceiling on an unattended loop sets one.
func (c *Controller) goalTaskBudget() agent.TaskBudget {
	b := c.taskBudget
	b.Tokens = c.goalTokenBudget
	return b
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
