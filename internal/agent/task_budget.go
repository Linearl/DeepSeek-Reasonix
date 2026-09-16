package agent

import "context"

// defaultChildMaxStepsFloor is the minimum step budget for a non-review
// sub-agent when the parent has a finite cap (task 118). The old floor of 5
// was too tight for "research across files + implement" subtasks.
const defaultChildMaxStepsFloor = 12

// childMaxSteps resolves a sub-agent budget; an explicit request always wins.
func (t *TaskTool) childMaxSteps(requested int) int {
	return childMaxStepsForParent(t.maxSteps, requested, t.defaultSteps)
}

func childMaxStepsForParent(parent, requested, override int) int {
	if requested > 0 {
		return requested
	}
	if override > 0 {
		return override
	}
	if parent <= 0 {
		return 0
	}
	// Two-thirds of the parent's budget, floored: a sub-agent needs enough
	// rounds to finish a multi-file research or implementation subtask without
	// the parent having to babysit it (task 118).
	return max(parent*2/3, defaultChildMaxStepsFloor)
}

func (t *TaskTool) childMaxStepsForContext(_ context.Context, requested int) int {
	return t.childMaxSteps(requested)
}

func (t *TaskTool) childMaxStepsForSpec(ctx context.Context, spec *ProfileExecSpec) (context.Context, int) {
	applyReviewBudget(spec, t.reviewMaxSteps)
	fillChildFacts(ctx, spec)
	if spec == nil {
		return ctx, t.childMaxStepsForContext(ctx, 0)
	}
	ctx = withChildOutputBudget(ctx, spec.Sched.MaxOutputTokens)
	return ctx, t.childMaxStepsForContext(ctx, spec.Sched.MaxSteps)
}
