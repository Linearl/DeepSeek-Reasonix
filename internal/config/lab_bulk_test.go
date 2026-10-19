package config

import (
	"strings"
	"testing"
)

// 任务 1006 验收①：范围三档×行为两轴的档位边界。
// 稳定档集合与开发者模式经参数注入（1005 未合入，占位为空集/恒 false）。

func planIDs(p LabBulkPlan) map[string]bool {
	m := map[string]bool{}
	for _, id := range p.Targets {
		m[id] = true
	}
	return m
}

func TestLabBulkScopeBoundary(t *testing.T) {
	// 注入非空稳定档集合验证三档边界（1005 联调后真值同路径）。
	stable := map[string]bool{"sessionCollab": true, "runtimeReuse": true}

	// 可选及以上：推荐+可选全进，未稳定/已退役绝不进。
	p := PlanLabBulk(LabBulkScopeOptional, true, stable, true)
	got := planIDs(p)
	if !got["sessionWall"] || !got["dream"] {
		t.Fatalf("optional scope should include recommended(sessionWall) and optional(dream): %v", p.Targets)
	}
	for _, id := range p.Targets {
		f := findLabFeatureTier(t, id)
		if f.tier == LabTierUnstable || f.tier == LabTierRetired {
			t.Fatalf("optional scope must NOT include %s (tier=%s) — 可选及以上不含未稳定", id, f.tier)
		}
	}

	// 推荐及以上：可选档不进。
	p = PlanLabBulk(LabBulkScopeRecommended, true, stable, true)
	got = planIDs(p)
	if !got["sessionWall"] {
		t.Fatalf("recommended scope should include recommended-tier sessionWall")
	}
	if got["dream"] || got["splitView"] {
		t.Fatalf("recommended scope must NOT include optional-tier features: %v", p.Targets)
	}

	// 稳定档：仅注入的稳定项。
	p = PlanLabBulk(LabBulkScopeStable, true, stable, true)
	got = planIDs(p)
	if !got["sessionCollab"] || !got["runtimeReuse"] || len(got) != 2 {
		t.Fatalf("stable scope should contain exactly the stable set: %v", p.Targets)
	}

	// 占位实测：当前稳定档集合为空 ⇒ stable 档 0 目标（不报错）。
	p = PlanLabBulk(LabBulkScopeStable, true, LabStableFeatureIDs(), false)
	if len(p.Targets) != 0 {
		t.Fatalf("placeholder stable set is empty — stable scope must plan 0 targets, got %v", p.Targets)
	}
}

func TestLabBulkGuardrails(t *testing.T) {
	stable := map[string]bool{"sessionCollab": true}

	// 护栏①：批量关含稳定档 → StableInScope 暴露给警示。
	p := PlanLabBulk(LabBulkScopeOptional, false, stable, true)
	if len(p.StableInScope) != 1 || p.StableInScope[0] != "sessionCollab" {
		t.Fatalf("guardrail①: bulk-off with stable in scope must expose StableInScope=[sessionCollab], got %v", p.StableInScope)
	}
	if !planIDs(p)["sessionCollab"] {
		t.Fatalf("dev-mode ON: stable feature must stay in targets, got %v", p.Targets)
	}

	// 护栏②：开发者模式关 + 批量关 → 稳定档自动跳过并提示。
	p = PlanLabBulk(LabBulkScopeOptional, false, stable, false)
	if len(p.SkippedStable) != 1 || p.SkippedStable[0] != "sessionCollab" {
		t.Fatalf("guardrail②: dev-mode OFF must auto-skip stable on bulk-off, got %v", p.SkippedStable)
	}
	if planIDs(p)["sessionCollab"] {
		t.Fatalf("guardrail②: skipped stable feature must NOT be in targets")
	}

	// 护栏②只作用于「关」：批量开不受开发者模式影响。
	p = PlanLabBulk(LabBulkScopeOptional, true, stable, false)
	if !planIDs(p)["sessionCollab"] || len(p.SkippedStable) != 0 {
		t.Fatalf("guardrail② must only gate bulk-OFF; bulk-ON with dev-mode off should keep stable target: %v", p)
	}
}

func TestLabBulkExcludedFeatures(t *testing.T) {
	// 任务 1005 联调：fullAccess/autopilot 已转「稳定」档（tier=LabTierStable），
	// 而 PlanLabBulk 的范围判定按宿主注入的 stableIDs（不看 tier 字段）——传 nil
	// 时两者不落范围、排除名单无从生效。本测试要验的是「排除名单在范围内强制
	// 生效」，故显式把两者喂进 stableIDs（真实宿主会把已开稳定项注入进来）。
	stableIDs := map[string]bool{"fullAccess": true, "autopilot": true}
	p := PlanLabBulk(LabBulkScopeOptional, true, stableIDs, true)
	skipBy := map[string]string{}
	for _, s := range p.Skipped {
		skipBy[s.ID] = s.Reason
	}
	if skipBy["fullAccess"] != LabBulkSkipDangerGate {
		t.Fatalf("fullAccess must be skipped with danger_gate (task 257 单项危险确认不可被批量绕过), got %q", skipBy["fullAccess"])
	}
	if skipBy["autopilot"] != LabBulkSkipPrecondition {
		t.Fatalf("autopilot must be skipped with precondition (yolo 前置), got %q", skipBy["autopilot"])
	}
	if skipBy["messageMerge"] != LabBulkSkipNotBool {
		t.Fatalf("messageMerge (tri-state master) must be skipped with not_bool_switch, got %q", skipBy["messageMerge"])
	}
	if _, ok := planIDs(p)["fullAccess"]; ok {
		t.Fatalf("fullAccess must never be a bulk target")
	}
}

func TestLabBulkApplyWritesConfig(t *testing.T) {
	c := Default()
	// 全部默认关的推荐/可选键先确认零值。
	if c.Desktop.ExperimentalSessionWall || c.Desktop.ExperimentalTodoSidebar {
		t.Fatalf("fixture sanity: defaults should start off")
	}

	plan, applied, err := c.ApplyLabBulk(LabBulkScopeRecommended, true)
	if err != nil {
		t.Fatalf("ApplyLabBulk recommended/on: %v", err)
	}
	if len(applied) == 0 || len(applied) != len(plan.Targets) {
		t.Fatalf("applied (%d) must cover every target (%d)", len(applied), len(plan.Targets))
	}
	if !c.Desktop.ExperimentalSessionWall || !c.Desktop.ExperimentalTodoSidebar {
		t.Fatalf("recommended/on must write session_wall/todo_sidebar keys, got wall=%v todo=%v",
			c.Desktop.ExperimentalSessionWall, c.Desktop.ExperimentalTodoSidebar)
	}
	// 任务 1005 联调：sessionCollab 已转「稳定」档（默认开），不在 recommended
	// 批量面内——批量只翻推荐/可选键，稳定档键保持默认值。
	if !c.Agent.ExperimentalSessionCollab {
		t.Fatalf("stable-tier sessionCollab must keep its default-on value: recommended scope must not touch it")
	}
	// 家族子键不动：批量只翻主开关。
	if c.Desktop.ExperimentalAutopilotAskTimeout {
		t.Fatalf("bulk must NOT touch family sub-keys (ask-timeout)")
	}
	// 范围外（未稳定）键不动。
	if c.Desktop.ExperimentalCollabHub {
		t.Fatalf("bulk must NOT touch unstable-tier keys (collabHub)")
	}

	// 反向：批量关。
	if _, _, err := c.ApplyLabBulk(LabBulkScopeRecommended, false); err != nil {
		t.Fatalf("ApplyLabBulk recommended/off: %v", err)
	}
	if c.Desktop.ExperimentalSessionWall {
		t.Fatalf("recommended/off must clear the master switches")
	}
	if !c.Agent.ExperimentalSessionCollab {
		t.Fatalf("recommended/off must NOT clear stable-tier sessionCollab (task 1005)")
	}
}

func TestLabBulkApplyOffSkipsStableUnderPlaceholder(t *testing.T) {
	// 占位语义回归：当前稳定档空集 + 开发者模式恒 false，批量关不跳过任何
	// 项（SkippedStable 空），也不误伤推荐/可选目标。
	c := Default()
	plan, _, err := c.ApplyLabBulk(LabBulkScopeOptional, false)
	if err != nil {
		t.Fatalf("ApplyLabBulk optional/off: %v", err)
	}
	if len(plan.SkippedStable) != 0 {
		t.Fatalf("placeholder stable set is empty — SkippedStable must be empty, got %v", plan.SkippedStable)
	}
	if len(plan.Targets) == 0 {
		t.Fatalf("optional/off should still target every bulkable recommended+optional feature")
	}
}

func TestParseLabBulkScopeRejectsTypo(t *testing.T) {
	if _, err := ParseLabBulkScope("all"); err == nil {
		t.Fatalf("scope 'all' must be rejected — 用户 2026-10-19 明确无「全部」档")
	}
	if _, err := ParseLabBulkScope("unstable"); err == nil {
		t.Fatalf("scope 'unstable' must be rejected — 未稳定项不进批量")
	}
	for _, ok := range []string{"stable", "recommended", "optional"} {
		if _, err := ParseLabBulkScope(ok); err != nil {
			t.Fatalf("scope %q should parse: %v", ok, err)
		}
	}
}

func findLabFeatureTier(t *testing.T, id string) labFeatureTier {
	t.Helper()
	for _, f := range labFeatureTiers {
		if f.id == id {
			return f
		}
	}
	t.Fatalf("feature %q not in labFeatureTiers registry", id)
	return labFeatureTier{}
}

// 门禁：批量注册表的每个 id 必须存在于表A（防注册表漂移——改名/退役后残留）。
func TestLabBulkSettersRegistryHygiene(t *testing.T) {
	known := map[string]bool{}
	for _, f := range labFeatureTiers {
		known[f.id] = true
	}
	for id := range labBulkSetters {
		if !known[id] {
			t.Fatalf("labBulkSetters has %q which is not in labFeatureTiers (registry drift)", id)
		}
	}
	for id := range labBulkExcluded {
		if !known[id] {
			t.Fatalf("labBulkExcluded has %q which is not in labFeatureTiers (registry drift)", id)
		}
	}
	// 覆盖率注记：范围内推荐/可选特性要么可批量、要么有明确排除原因——不允许
	// 静默漏网（静默漏网 = 用户选了范围却看不到该项，也没有提示）。
	stable := map[string]bool{}
	for _, f := range labFeatureTiers {
		if f.tier != LabTierRecommended && f.tier != LabTierOptional {
			continue
		}
		if _, bulkable := labBulkSetters[f.id]; bulkable {
			continue
		}
		if _, excluded := labBulkExcluded[f.id]; !excluded {
			// 允许无排除条目但必须有 reason（no_bulk_setter 兜底在 Plan 里），
			// 此处仅防「表A 新增推荐/可选特性时忘了考虑批量面」——失败信息
			// 直接点名，要求显式登记（加 setter 或加排除原因）。
			t.Errorf("表A 特性 %q（%s 档）未登记 labBulkSetters 也未登记 labBulkExcluded —— 新增实验项须显式决定是否进批量面", f.id, f.tier)
		}
	}
	_ = stable
	_ = strings.TrimSpace
}
