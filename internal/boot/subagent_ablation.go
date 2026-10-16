package boot

import (
	"context"
	"fmt"

	"reasonix/internal/ablation"
	"reasonix/internal/skill"
	"reasonix/internal/tool"
)

// gateSubagentArm refuses skills that would spawn a child while the subagent
// ablation arm is off. Delegation is delegation whichever tool reached it, so
// the arm has to cover profile skills too or it is not a single-agent control.
// Inline playbooks are unaffected: they never spawn a child.
func gateSubagentArm(set ablation.Set, run skill.SubagentRunner) skill.SubagentRunner {
	if !set.Off(ablation.Subagent) {
		return run
	}
	return func(_ context.Context, sk skill.Skill, _ string, _ skill.SubagentRunOptions) (string, error) {
		return "", fmt.Errorf("subagent skill %q is disabled for this run (subagent ablation arm)", sk.Name)
	}
}

// builtinSubagentTools returns the dedicated explore/research/review wrappers,
// or nothing under the arm so the control run does not even carry their schemas.
// skills is the already-loaded list (boot's List() or the assembly-reused one);
// task 919 threads it through so building these wrappers never re-scans the
// skill roots — each store.Read used to trigger a full disk scan.
func builtinSubagentTools(set ablation.Set, store *skill.Store, skills []skill.Skill, run skill.SubagentRunner, profile ...skill.ProfileResolver) []tool.Tool {
	if set.Off(ablation.Subagent) {
		return nil
	}
	return skill.BuiltinSubagentToolsFromList(store, skills, run, profile...)
}
