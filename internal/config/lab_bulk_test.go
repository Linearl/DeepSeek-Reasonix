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
	// tl1453：dream 退役后，可选档样例换 decisionPatterns（同可选、可批量）。
	if !got["sessionWall"] || !got["decisionPatterns"] {
		t.Fatalf("optional scope should include recommended(sessionWall) and optional(decisionPatterns): %v", p.Targets)
	}
	for _, id := range p.Targets {
		f := findLabFeatureTier(t, id)
		if f.tier == LabTierUnstable || f.tier == LabTierRetired {
			t.Fatalf("optional scope must NOT include %s (tier=%s) — 可选及以上不含未稳定", id, f.tier)
		}
	}

	// 推荐及以上：可选档不进。
	// tl1265 第2轮：splitView 自可选升推荐后不再作 optional 探针——探针改用
	// 仍为可选档的 decisionPatterns（自动化）+ highSpeedModel（提效）双点。
	p = PlanLabBulk(LabBulkScopeRecommended, true, stable, true)
	got = planIDs(p)
	if !got["sessionWall"] {
		t.Fatalf("recommended scope should include recommended-tier sessionWall")
	}
	if got["decisionPatterns"] || got["highSpeedModel"] || got["splitView"] && findLabFeatureTier(t, "splitView").tier == LabTierOptional {
		t.Fatalf("recommended scope must NOT include optional-tier features: %v", p.Targets)
	}

	// 稳定档：tl1741 换代——可批量稳定集合已空（sessionCollab/runtimeReuse
	// 撤开关退役），stable scope Targets 恒空。
	p = PlanLabBulk(LabBulkScopeStable, true, stable, true)
	got = planIDs(p)
	if len(p.Targets) != 0 {
		t.Fatalf("stable scope should target nothing after tl1741 retired the bulkable stable set: %v", p.Targets)
	}

	// tl 1037 → tl1741：真集合（从 labFeatureTiers 派生，仅 fullAccess）下
	// stable 档唯一成员因危险门排除进 Skipped，绝非静默消失。
	p = PlanLabBulk(LabBulkScopeStable, true, LabStableFeatureIDs(), false)
	got = planIDs(p)
	if len(p.Targets) != 0 {
		t.Fatalf("stable scope (real stable set) must have no bulkable target after tl1741, got %v", p.Targets)
	}
	skipBy := map[string]string{}
	for _, s := range p.Skipped {
		skipBy[s.ID] = s.Reason
	}
	if skipBy["fullAccess"] != LabBulkSkipDangerGate {
		t.Errorf("stable scope must skip fullAccess with danger_gate, got %q", skipBy["fullAccess"])
	}
	for _, s := range p.Skipped {
		if s.Reason == "" {
			t.Errorf("skipped %q carries no reason — 静默跳过零提示（tl 1037 禁止）", s.ID)
		}
	}
}

func TestLabBulkGuardrails(t *testing.T) {
	// tl1741 换代：可批量稳定集合已空（sessionCollab/runtimeReuse 撤开关退役）
	// → 护栏①StableInScope 与护栏②SkippedStable 恒空（机制保留：真集合里若
	// 未来出现可批量稳定档会重新生效）。真集合唯一稳定档 fullAccess 走排除
	// 名单（danger_gate 优先于护栏），绝不静默。
	p := PlanLabBulk(LabBulkScopeOptional, false, LabStableFeatureIDs(), false)
	if len(p.StableInScope) != 0 {
		t.Fatalf("guardrail①: StableInScope must be empty after tl1741 (no bulkable stable left), got %v", p.StableInScope)
	}
	if len(p.SkippedStable) != 0 {
		t.Fatalf("guardrail②: SkippedStable must be empty after tl1741, got %v", p.SkippedStable)
	}
	reasonBy := map[string]string{}
	for _, s := range p.Skipped {
		reasonBy[s.ID] = s.Reason
	}
	if reasonBy["fullAccess"] != LabBulkSkipDangerGate {
		t.Fatalf("fullAccess must be Skipped with danger_gate (排除名单优先于护栏), got %q", reasonBy["fullAccess"])
	}
	if planIDs(p)["fullAccess"] {
		t.Fatalf("fullAccess must never be a bulk target")
	}
	// 原注入集合机制钉（fake stable id 不在档位表 → 不进 plan 任何面）。
	fakeStable := map[string]bool{"sessionCollab": true}
	p = PlanLabBulk(LabBulkScopeOptional, false, fakeStable, false)
	if planIDs(p)["sessionCollab"] || len(p.SkippedStable) != 0 {
		t.Fatalf("retired id must not enter any plan face (键退役), targets=%v skippedStable=%v", p.Targets, p.SkippedStable)
	}
}

func TestLabBulkExcludedFeatures(t *testing.T) {
	// 任务 1005 联调：fullAccess/autopilot 已转「稳定」档（tier=LabTierStable），
	// 而 PlanLabBulk 的范围判定按宿主注入的 stableIDs（不看 tier 字段）——传 nil
	// 时两者不落范围、排除名单无从生效。本测试要验的是「排除名单在范围内强制
	// 生效」，故显式把两者喂进 stableIDs（真实宿主会把已开稳定项注入进来）。
	// tl1741：autopilot/collabHub/sessionStorage 随撤开关出排除名单，稳定档
	// 排除面只剩 fullAccess（危险门）。
	stableIDs := map[string]bool{"fullAccess": true}
	p := PlanLabBulk(LabBulkScopeOptional, true, stableIDs, true)
	skipBy := map[string]string{}
	for _, s := range p.Skipped {
		skipBy[s.ID] = s.Reason
	}
	if skipBy["fullAccess"] != LabBulkSkipDangerGate {
		t.Fatalf("fullAccess must be skipped with danger_gate (task 257 单项危险确认不可被批量绕过), got %q", skipBy["fullAccess"])
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
	// 家族子键不动：批量只翻主开关。
	if c.Desktop.ExperimentalAutopilotAskTimeout {
		t.Fatalf("bulk must NOT touch family sub-keys (ask-timeout)")
	}
	// tl1741：sessionCollab/collabHub 断言随撤开关退役（键/批量面不存在）。

	// 反向：批量关；sessionWall 关掉。tl1741：sessionCollab SkippedStable
	// 断言随撤开关退役（稳定档可批量集合已空）。
	if _, _, err := c.ApplyLabBulk(LabBulkScopeRecommended, false); err != nil {
		t.Fatalf("ApplyLabBulk recommended/off: %v", err)
	}
	if c.Desktop.ExperimentalSessionWall {
		t.Fatalf("recommended/off must clear the master switches")
	}
}

func TestLabBulkApplyOffSkipsStableUnderPlaceholder(t *testing.T) {
	// tl 1037 语义随 tl1741 换代：可批量稳定档集合已空（sessionCollab /
	// runtimeReuse 撤开关退役），SkippedStable 恒空；autopilot / collabHub /
	// sessionStorage 出排除名单，稳定档排除面只剩 fullAccess（danger_gate）。
	// 推荐/可选目标正常关闭。
	c := Default()
	plan, _, err := c.ApplyLabBulk(LabBulkScopeOptional, false)
	if err != nil {
		t.Fatalf("ApplyLabBulk optional/off: %v", err)
	}
	if len(plan.SkippedStable) != 0 {
		t.Errorf("SkippedStable must be empty after tl1741 retired the bulkable stable set, got %v", plan.SkippedStable)
	}
	reasonBy := map[string]string{}
	for _, s := range plan.Skipped {
		reasonBy[s.ID] = s.Reason
	}
	if reasonBy["fullAccess"] != LabBulkSkipDangerGate {
		t.Errorf("fullAccess must be Skipped with danger_gate, got %q", reasonBy["fullAccess"])
	}
	// 稳定档（fullAccess）与退役项绝不落 Targets。
	got := planIDs(plan)
	if got["fullAccess"] {
		t.Errorf("stable fullAccess must not be a bulk-off target (排除), got %v", plan.Targets)
	}
	for _, id := range []string{"autopilot", "sessionCollab", "collabHub", "runtimeReuse", "sessionStorage"} {
		if got[id] {
			t.Errorf("retired %s must not appear in any bulk plan (键退役), got %v", id, plan.Targets)
		}
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

// ── tl 1037 验收 ──────────────────────────────────────────────────────────

// tl 1037 ③ 双源一致性守卫：LabStableFeatureIDs 的派生集合必须与
// labFeatureTiers 的 stable 档子集逐项相等（含方向双向：不多不少）。
// 守的是「声明档位」与「批量可达」不漂移——labFeatureTiers 是唯一权威，
// LabStableFeatureIDs 从它派生；若有人改成手写集合，本断言立刻红。
func TestLabStableFeatureIDsMatchesTierRegistry(t *testing.T) {
	derived := LabStableFeatureIDs()
	fromRegistry := map[string]bool{}
	for _, f := range labFeatureTiers {
		if f.tier == LabTierStable {
			fromRegistry[f.id] = true
		}
	}
	if len(derived) == 0 {
		t.Fatalf("LabStableFeatureIDs() is empty — stable scope would be unreachable (tl 1037 defect regressed)")
	}
	if len(derived) != len(fromRegistry) {
		t.Errorf("stable set size mismatch: derived=%d registry=%d", len(derived), len(fromRegistry))
	}
	for id := range fromRegistry {
		if !derived[id] {
			t.Errorf("stable tier %q declared in labFeatureTiers but missing from LabStableFeatureIDs (双源漂移)", id)
		}
	}
	for id := range derived {
		if !fromRegistry[id] {
			t.Errorf("LabStableFeatureIDs carries %q which is not LabTierStable in labFeatureTiers (双源漂移)", id)
		}
	}
}

// tl 1037 ①+② → tl1741 换代：稳定档批量面收口为 fullAccess（危险门排除，
// 非静默）；退役四项不进 plan 任何面；sessionWall 推荐档零回归。
func TestLabBulkRecommendedOffCoversFourStableAndNotSilent(t *testing.T) {
	stableIDs := LabStableFeatureIDs()
	p := PlanLabBulk(LabBulkScopeRecommended, false, stableIDs, false)
	got := planIDs(p)

	// sessionWall 推荐档零回归：照常进 Targets。
	if !got["sessionWall"] {
		t.Fatalf("recommended/off must still target sessionWall (1037 零回归), got %v", p.Targets)
	}
	// 退役项绝不出现在 plan 任何面。
	for _, id := range []string{"autopilot", "sessionCollab", "runtimeReuse"} {
		if got[id] {
			t.Errorf("retired %s must NOT be in Targets (键退役), got %v", id, p.Targets)
		}
	}
	if got["fullAccess"] {
		t.Errorf("stable fullAccess must NOT be in Targets (危险门排除), got %v", p.Targets)
	}
	// 非静默：fullAccess 必须在 Skipped 带 danger_gate 原因。
	reasonBy := map[string]string{}
	for _, s := range p.Skipped {
		reasonBy[s.ID] = s.Reason
	}
	if reasonBy["fullAccess"] != LabBulkSkipDangerGate {
		t.Errorf("fullAccess must carry danger_gate reason, got %q", reasonBy["fullAccess"])
	}
	// 所有 Skipped 记录原因非空（杜绝空原因静默）。
	for _, s := range p.Skipped {
		if s.Reason == "" {
			t.Errorf("skipped %q has empty reason", s.ID)
		}
	}
}

// tl 1037 ① → tl1741 换代：stable 范围可批量集合已空（sessionCollab /
// runtimeReuse 退役），Targets 恒空；fullAccess 仍在排除名单（danger_gate）。
func TestLabBulkStableScopeTargetsBulkableStable(t *testing.T) {
	p := PlanLabBulk(LabBulkScopeStable, true, LabStableFeatureIDs(), false)
	got := planIDs(p)
	if len(p.Targets) != 0 {
		t.Fatalf("stable scope must have no bulkable targets after tl1741, got %v", p.Targets)
	}
	if got["fullAccess"] {
		t.Fatalf("stable scope must exclude fullAccess (危险门排除名单), got %v", p.Targets)
	}
	reasonBy := map[string]string{}
	for _, s := range p.Skipped {
		reasonBy[s.ID] = s.Reason
	}
	if reasonBy["fullAccess"] != LabBulkSkipDangerGate {
		t.Errorf("stable scope fullAccess Skipped must carry danger_gate, got %q", reasonBy["fullAccess"])
	}
}

// tl1741：TestLabBulkRecommendedOnWritesStableOn 随撤开关删除——
// sessionCollab/runtimeReuse 批量面不存在（键退役，功能常开）。
