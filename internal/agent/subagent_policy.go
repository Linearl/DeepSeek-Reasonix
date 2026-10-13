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
	// SubagentPolicyLight is the economy tier (task 850, user decision
	// 2026-10-06): it now carries its own conservative guidance block — the
	// "keep quick, small tasks in the main session" clause the aggressive
	// tier shed in task 820 explicitly belongs HERE, where it matches the
	// tier's spend-controlling semantics. The ZERO-VALUE policy ("") still
	// injects nothing: a session that never expressed a tier keeps the
	// historical no-guidance behavior byte-identical (and the control-layer
	// default-no-injection contract stays pinned).
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
// tier. The ZERO-VALUE policy injects nothing, preserving the never-configured
// behavior exactly; an explicit light tier now carries the economy block.
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
//
// Task 850 (user decision 2026-10-12): light/aggressive gain display names
// (economy/efficiency) and the shared-reading-surface rule — serial
// consolidation only saves input when sub-tasks would read the same content;
// independent task domains dispatch in parallel at every tier.
func SubagentPolicyGuidance(p SubagentPolicy) string {
	switch p {
	case SubagentPolicyLight:
		return "<subagent-policy>light\n" +
			"Economy delegation guidance for this turn (the conservative tier — it exists to control spending):\n" +
			"- Do quick, small tasks yourself: anything finishable in a single round-trip with tiny I/O stays in the main session. Each dispatch carries a fixed overhead, and this tier spends it only when the work is real.\n" +
			"- When the work does warrant sub-agents, prefer fewer, larger dispatches over many small ones: hand one sub-agent several related sub-tasks in a single task instead of spawning one sub-agent per sub-task.\n" +
			"- If the sub-tasks share a reading surface — they would read the same files or the same content — consolidate them into one sub-agent (or run them serially through one) so the shared input is read once instead of once per sub-agent; parallel duplicates each pay the same input cost.\n" +
			"- If the sub-tasks live in independent domains — different modules, different files, no overlapping reads — serializing buys nothing there; dispatch them in parallel regardless of tier.\n" +
			"</subagent-policy>"
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
			"- Your only role for delegable sub-work is to coordinate: dispatch, then collect and verify. Do not perform delegable work inline while a sub-agent could be running it.\n" +
			"- When to dispatch (a positive list — the default, not a fallback): broad read-only investigation across many files; independent sub-tasks that can run at the same time; exploratory work whose intermediate output would otherwise flood the main context.\n" +
			"- What dispatching buys: the main context stays clean — only the sub-agent's final answer returns — independent sub-tasks progress at the same time so wall-clock time shrinks, and each sub-agent works in its own fresh, isolated context.\n" +
			"- Dispatching is affordable: the sub-agent step cap defaults to two-thirds of the parent's cap (min 12), long sub-tasks can run in the background instead of blocking (wherever the tool offers run_in_background), and write conflicts are avoided by declaring non-overlapping write_paths.\n" +
			"- When delegating a writing sub-task, fill in write_paths: non-overlapping write_paths are what unlock parallel writers (up to max_parallel_writers); omitting write_paths claims the whole workspace and serializes every writer behind it.\n" +
			"- Prefer delegation: any independent sub-task (research, codegen, review, parallel exploration) goes to a sub-agent instead of being inlined serially in the main chain.\n" +
			"- Input economics when you dispatch: if several sub-tasks would read the same files or the same content (a shared reading surface), consolidate them into one sub-agent so the shared input is read once instead of once per sub-agent; if they live in independent domains, dispatch them in parallel — serializing independent work buys nothing.\n" +
			"- Decompose up front: split the task into sub-tasks before executing, and dispatch early — delegation is the default strategy, not a fallback.\n" +
			"- Explicitly trade token cost for wall-clock time: sub-agent context is isolated, keeping the main chain short and cache-friendly.\n" +
			"- For sub-tasks that will take more than ~30 seconds, set run_in_background=true so the main chain stays unblocked; collect the result with wait when you need it.\n" +
			"- Do NOT inline sub-work that meets ANY of these thresholds — dispatch it instead:\n" +
			"  * it will plausibly take more than ~30 seconds of work (multiple tool rounds, large file reads/writes, long-running commands);\n" +
			"  * it needs more than ~50k tokens of input beyond the system prompt (deep research over many files, large docs);\n" +
			"  * its output will exceed ~10k tokens (long reports, generated code volumes).\n" +
			"- Only sub-work below every threshold and finishable in a single round-trip with tiny I/O stays in the main session. When in doubt, dispatch.\n" +
			"</subagent-policy>"
	default:
		return ""
	}
}

// delegationReminderMinSteps is the mid-turn reminder trigger (task 850, 816
// P1-②): once the efficiency tier has burned this many admitted main-chain
// tool steps — the same axis the DelegationMetricsAudit reports — without a
// single dispatch, the host re-tops the fading turn-opening guidance with one
// transient reminder. The value sits at the low edge of the 816 study's
// suggested 8-12 window: early enough to still redirect a long turn.
const delegationReminderMinSteps = 9

// shouldNudgeDelegationReminder reports whether this tool round should carry
// the once-per-turn delegation reminder: an explicit aggressive (efficiency)
// tier, past the step threshold, and zero dispatches so far. An economy /
// balanced / never-set tier never nudges — the reminder is the efficiency
// tier's behavioral half, not a general nag.
func (a *Agent) shouldNudgeDelegationReminder(state *turnRuntime) bool {
	if state == nil || state.delegationReminderSent {
		return false
	}
	if a.subagentPolicyTierValue() != string(SubagentPolicyAggressive) {
		return false
	}
	if state.mainChainToolSteps < delegationReminderMinSteps {
		return false
	}
	if state.taskDispatches+state.readOnlyTaskDispatches > 0 {
		return false
	}
	state.delegationReminderSent = true
	return true
}

// delegationReminderMessage is the transient mid-turn nudge text. It mirrors
// the aggressive block's single-direction tilt (task 820) — no conservative
// escape clause, the very thing the tier must not re-grow.
func delegationReminderMessage(steps int) string {
	return fmt.Sprintf(
		"delegation reminder: this turn has already run %d main-chain tool steps without a single sub-agent dispatch. If the remaining work includes delegable sub-work — multi-file investigation, independent sub-tasks, exploratory reads — dispatch it now and coordinate the results instead of continuing inline. When in doubt, dispatch.",
		steps)
}
