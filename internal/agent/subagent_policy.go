package agent

import (
	"fmt"
	"strings"
)

// SubagentPolicy tiers the sub-agent delegation tendency (#9004). The tier
// rides the user turn as a transient <subagent-policy> block (stripped from
// stored history), so switching is zero-rebuild, zero-persistence and does
// not touch the provider-visible prefix.
//
// The tier only shapes the delegation strategy (when to dispatch); the
// scheduler concurrency/depth limits stay the policy-independent guardrails
// configured at startup.
type SubagentPolicy string

const (
	// SubagentPolicyLight is the current conservative behavior: no extra
	// delegation guidance is injected.
	SubagentPolicyLight SubagentPolicy = "light"
	// SubagentPolicyBalanced permits delegation of self-contained sub-tasks
	// (research, codegen, review) with a concrete deliverable.
	SubagentPolicyBalanced SubagentPolicy = "balanced"
	// SubagentPolicyAggressive prefers delegation: any independent sub-task
	// above the cost thresholds goes to a sub-agent.
	SubagentPolicyAggressive SubagentPolicy = "aggressive"
)

// NormalizeSubagentPolicy validates and canonicalizes a tier label.
func NormalizeSubagentPolicy(v string) (SubagentPolicy, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "light":
		return SubagentPolicyLight, nil
	case "balanced":
		return SubagentPolicyBalanced, nil
	case "aggressive", "full":
		return SubagentPolicyAggressive, nil
	default:
		return "", fmt.Errorf("subagent policy must be one of light|balanced|aggressive")
	}
}

// SubagentPolicyGuidance returns the transient per-turn guidance block for a
// tier. The light tier injects nothing, preserving current behavior exactly.
//
// Task 531: each non-light tier carries the positive half of the delegation
// decision — when to dispatch, what dispatching buys, and that the costs are
// bounded — alongside the negative thresholds, so the model sees a complete
// decision basis instead of only a "dispatching is not worth it" list. The
// balanced tier keeps concurrency out of the prompt (scheduler guardrail,
// decoupled from the delegation strategy); the aggressive tier additionally
// carries the write_paths hint (declaring non-overlapping write_paths is what
// unlocks parallel writers) — the hint points at the mechanism, while the
// scheduler cap still governs actual concurrency.
func SubagentPolicyGuidance(p SubagentPolicy) string {
	switch p {
	case SubagentPolicyBalanced:
		return "<subagent-policy>balanced\n" +
			"Delegation guidance for this turn:\n" +
			"- When to dispatch (a positive list, not a fallback): broad read-only investigation across many files; self-contained sub-tasks with a concrete deliverable; exploratory work whose intermediate output would otherwise flood the main context.\n" +
			"- What dispatching buys: the main context stays clean — only the sub-agent's final answer returns — and each sub-agent works in its own fresh, isolated context.\n" +
			"- Dispatching is affordable: the sub-agent step cap defaults to two-thirds of the parent's cap (min 12), enough runway for a focused deliverable.\n" +
			"- Delegate self-contained sub-tasks that benefit from isolated context: deep research across many files, focused code generation, independent verification or review.\n" +
			"- Identify delegable sub-tasks when you start a task, not only when you get stuck.\n" +
			"- Keep delegated tasks single-purpose with a concrete deliverable; the sub-agent returns only its final answer.\n" +
			"</subagent-policy>"
	case SubagentPolicyAggressive:
		return "<subagent-policy>aggressive\n" +
			"Delegation guidance for this turn:\n" +
			"- When to dispatch (a positive list, not a fallback): broad read-only investigation across many files; independent sub-tasks that can run at the same time; exploratory work whose intermediate output would otherwise flood the main context.\n" +
			"- What dispatching buys: the main context stays clean — only the sub-agent's final answer returns — independent sub-tasks progress at the same time so wall-clock time shrinks, and each sub-agent works in its own fresh, isolated context.\n" +
			"- Dispatching is affordable: the sub-agent step cap defaults to two-thirds of the parent's cap (min 12), long sub-tasks can run in the background instead of blocking (wherever the tool offers run_in_background), and write conflicts are avoided by declaring non-overlapping write_paths.\n" +
			"- When delegating a writing sub-task, fill in write_paths: non-overlapping write_paths are what unlock parallel writers (up to max_parallel_writers); omitting write_paths claims the whole workspace and serializes every writer behind it.\n" +
			"- Prefer delegation: any independent sub-task (research, codegen, review, parallel exploration) goes to a sub-agent instead of being inlined serially in the main chain.\n" +
			"- Decompose up front: split the task into sub-tasks before executing, and dispatch early — delegation is the default strategy, not a fallback.\n" +
			"- Explicitly trade token cost for wall-clock time: sub-agent context is isolated, keeping the main chain short and cache-friendly.\n" +
			"- For sub-tasks that will take more than ~30 seconds, set run_in_background=true so the main chain stays unblocked; collect the result with wait when you need it.\n" +
			"- Delegate when a sub-task meets ANY of these thresholds:\n" +
			"  * it will plausibly take more than ~30 seconds of work (multiple tool rounds, large file reads/writes, long-running commands) — keeps the main turn from blocking;\n" +
			"  * it needs more than ~50k tokens of input beyond the system prompt (deep research over many files, large docs) — keeps the main context clean;\n" +
			"  * its output will exceed ~10k tokens (long reports, generated code volumes) — large outputs are slow and bloat the main chain.\n" +
			"- Keep quick, small tasks (single round-trip, tiny I/O) in the main session: delegation overhead is not worth it below these thresholds.\n" +
			"</subagent-policy>"
	default:
		return ""
	}
}
