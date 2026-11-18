package boot

import (
	"reasonix/internal/command"
	"reasonix/internal/extension"
	"reasonix/internal/hook"
	"reasonix/internal/memory"
	"reasonix/internal/sandbox"
	"reasonix/internal/skill"
	"reasonix/internal/tool"
)

// ReusedAssembly holds rediscovery-free inputs for narrow/no-op rebuilds.
// Populated on successful BuildRuntime so the next RebuildFrom can skip
// skill/command/hook rediscovery and snapshot re-freeze when the plan allows.
type ReusedAssembly struct {
	SystemPrompt            string
	Skills                  []skill.Skill
	SlashSkills             []skill.Skill // visible slash directory (task 919: rebuild slash tool without a re-scan)
	Commands                []command.Command
	Hooks                   []hook.ResolvedHook
	Registry                *tool.Registry
	ImplicitSkillInvocation bool
	// Memory is the previous build's memory snapshot. tl1683: the memory
	// segments (migrate / pinned / load) were the boot hot path's dominant
	// cost — the migrate segment alone was 98.9% of the P95 growth — and
	// they ran unconditionally even when the whole system prompt was about
	// to be replaced by the stored one. A reuse build now reuses this
	// snapshot instead of re-reading the store.
	Memory *memory.Set
	// MemoryFingerprint is the store change token captured together with
	// Memory (each fact file's name, size and mtime). The reuse build only
	// takes Memory when the token still matches — a store that changed
	// since the snapshot falls back to a fresh load.
	MemoryFingerprint string
	// Shell is the previously resolved shell. tl1683: the resolution runs
	// subprocess health probes (`bash -c true`, a 3s budget per candidate on
	// Windows), and a reuse build serves the previous prompt anyway, so the
	// probe would be paid for nothing.
	Shell sandbox.Shell
}

// shouldReuseDiscovery reports whether rediscovery of skills/commands/hooks
// can be skipped for this rebuild plan. Provider-only and MCP-only rebuilds
// do not change skill/command/hook discovery either — only the live backend.
func shouldReuseDiscovery(plan *extension.RuntimePlan) bool {
	if plan == nil {
		return false
	}
	switch plan.Kind {
	case extension.SubgraphNone,
		extension.SubgraphInterceptorOnly,
		extension.SubgraphUIOnly,
		extension.SubgraphProviderOnly,
		extension.SubgraphMCPOnly:
		return true
	default:
		return false
	}
}

// shouldReuseSnapshot reports whether the previous RuntimeSnapshot body can be
// re-published with a new generation (provider-visible prefix unchanged).
func shouldReuseSnapshot(plan *extension.RuntimePlan) bool {
	if plan == nil {
		return false
	}
	return plan.IsNoOp() || !plan.MayChangePrefix()
}

// FullReusePlan returns the plan a cold cross-tab build presents when it wants
// full assembly reuse (task 363A): SubgraphNone — nothing extension-side
// changed between tabs, so cached prompt/skill/command/hook discovery inputs
// stay valid. Without a plan the reuse guards refuse (they are designed for
// incremental rebuilds that know their previous generation).
func FullReusePlan() *extension.RuntimePlan {
	return &extension.RuntimePlan{Kind: extension.SubgraphNone}
}

// shouldSkipPromptStrategy is true when system_prompt.build must not re-run.
func shouldSkipPromptStrategy(plan *extension.RuntimePlan) bool {
	return shouldReuseSnapshot(plan)
}
