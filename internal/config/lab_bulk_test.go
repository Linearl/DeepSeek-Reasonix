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

	// tl 1037：真集合（从 labFeatureTiers 派生，非空）下 stable 档覆盖 1005
	// 批1 四项 —— 全部为稳定档且可批量的 sessionCollab / runtimeReuse 进
	// Targets；fullAccess / autopilot 因排除名单（danger_gate / precondition）
	// 进 Skipped（原因码见各断言），绝非静默消失。
	p = PlanLabBulk(LabBulkScopeStable, true, LabStableFeatureIDs(), false)
	got = planIDs(p)
	for _, id := range []string{"sessionCollab", "runtimeReuse"} {
		if !got[id] {
			t.Errorf("stable scope (real stable set) must target %s, got %v", id, p.Targets)
		}
	}
	if got["sessionWall"] {
		t.Fatalf("stable scope must NOT include recommended-tier sessionWall: %v", p.Targets)
	}
	skipBy := map[string]string{}
	for _, s := range p.Skipped {
		skipBy[s.ID] = s.Reason
	}
	if skipBy["fullAccess"] != LabBulkSkipDangerGate {
		t.Errorf("stable scope must skip fullAccess with danger_gate, got %q", skipBy["fullAccess"])
	}
	if skipBy["autopilot"] != LabBulkSkipPrecondition {
		t.Errorf("stable scope must skip autopilot with precondition, got %q", skipBy["autopilot"])
	}
	for _, s := range p.Skipped {
		if s.Reason == "" {
			t.Errorf("skipped %q carries no reason — 静默跳过零提示（tl 1037 禁止）", s.ID)
		}
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
	// tl 1037 后 stable 项进 recommended 范围（stableIDs 含它）：sessionCollab
	// 已是默认开，recommended/on 写 true = 幂等保持开（不因批量而被关）。
	if !c.Agent.ExperimentalSessionCollab {
		t.Fatalf("recommended/on must keep stable sessionCollab on (default true, bulk writes true)")
	}
	// 家族子键不动：批量只翻主开关。
	if c.Desktop.ExperimentalAutopilotAskTimeout {
		t.Fatalf("bulk must NOT touch family sub-keys (ask-timeout)")
	}
	// 范围外（未稳定）键不动。
	if c.Desktop.ExperimentalCollabHub {
		t.Fatalf("bulk must NOT touch unstable-tier keys (collabHub)")
	}

	// 反向：批量关。tl 1037 后 sessionCollab 在 recommended 范围内、devMode
	// 恒 false ⇒ 被护栏②挡在 SkippedStable（不被关）；sessionWall 关掉。
	if _, _, err := c.ApplyLabBulk(LabBulkScopeRecommended, false); err != nil {
		t.Fatalf("ApplyLabBulk recommended/off: %v", err)
	}
	if c.Desktop.ExperimentalSessionWall {
		t.Fatalf("recommended/off must clear the master switches")
	}
	if !c.Agent.ExperimentalSessionCollab {
		t.Fatalf("recommended/off must NOT clear stable sessionCollab (护栏② devMode off, stays default-on)")
	}
}

func TestLabBulkApplyOffSkipsStableUnderPlaceholder(t *testing.T) {
	// tl 1037 后真实语义：稳定档集合非空（派生），开发者模式恒 false ⇒
	// 批量关把「可批量」稳定档（sessionCollab / runtimeReuse）移入
	// SkippedStable（护栏②）；autopilot / fullAccess 在排除名单更前一步
	// 落 Skipped（precondition / danger_gate）。推荐/可选目标正常关闭。
	c := Default()
	plan, _, err := c.ApplyLabBulk(LabBulkScopeOptional, false)
	if err != nil {
		t.Fatalf("ApplyLabBulk optional/off: %v", err)
	}
	// 护栏②挡下的可批量稳定档。
	for _, id := range []string{"sessionCollab", "runtimeReuse"} {
		found := false
		for _, s := range plan.SkippedStable {
			if s == id {
				found = true
			}
		}
		if !found {
			t.Errorf("bulkable stable %s must be in SkippedStable (护栏② devMode off), got %v", id, plan.SkippedStable)
		}
	}
	// 排除名单稳定档（autopilot/fullAccess）——非静默、带原因码。
	reasonBy := map[string]string{}
	for _, s := range plan.Skipped {
		reasonBy[s.ID] = s.Reason
	}
	if reasonBy["autopilot"] != LabBulkSkipPrecondition {
		t.Errorf("autopilot must be Skipped with precondition, got %q", reasonBy["autopilot"])
	}
	if reasonBy["fullAccess"] != LabBulkSkipDangerGate {
		t.Errorf("fullAccess must be Skipped with danger_gate, got %q", reasonBy["fullAccess"])
	}
	// 四项稳定档绝不落 Targets（排除/护栏②必挡其一）。
	got := planIDs(plan)
	for _, id := range []string{"autopilot", "sessionCollab", "fullAccess", "runtimeReuse"} {
		if got[id] {
			t.Errorf("stable %s must not be a bulk-off target (排除/护栏②), got %v", id, plan.Targets)
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

// tl 1037 ①+②：四稳定档批量可达 + Skipped 非静默。
//
// recommended/off：sessionCollab 在范围内（stableIDs 命中）→ 稳定档、
// devMode 关 → 被护栏②挡在 SkippedStable（带 dev_mode_off 原因，非静默）；
// autopilot / fullAccess 在稳定档范围（推荐+稳定档）→ 排除名单 → Skipped
// 带原因码；runtimeReuse 同 sessionCollab 被护栏②挡。sessionWall 推荐档
// 不受影响照常进 Targets（1037 零回归面）。
func TestLabBulkRecommendedOffCoversFourStableAndNotSilent(t *testing.T) {
	stableIDs := LabStableFeatureIDs()
	p := PlanLabBulk(LabBulkScopeRecommended, false, stableIDs, false)
	got := planIDs(p)

	// sessionWall 推荐档零回归：照常进 Targets。
	if !got["sessionWall"] {
		t.Fatalf("recommended/off must still target sessionWall (1037 零回归), got %v", p.Targets)
	}
	// 四项绝不落 Targets（devMode 关，护栏② 或排除名单必挡其一）。
	for _, id := range []string{"autopilot", "sessionCollab", "fullAccess", "runtimeReuse"} {
		if got[id] {
			t.Errorf("stable %s must NOT be in Targets (devMode off + 排除/护栏), got %v", id, p.Targets)
		}
	}
	// 非静默：四项每一项要么在 SkippedStable（带 dev_mode_off），要么在
	// Skipped（带 danger_gate / precondition），绝不消失。
	reasonBy := map[string]string{}
	for _, s := range p.Skipped {
		reasonBy[s.ID] = s.Reason
	}
	skippedStable := map[string]bool{}
	for _, id := range p.SkippedStable {
		skippedStable[id] = true
	}
	for _, id := range []string{"autopilot", "sessionCollab", "fullAccess", "runtimeReuse"} {
		inSkippedStable := skippedStable[id]
		reason, inSkipped := reasonBy[id]
		if !inSkippedStable && !inSkipped {
			t.Errorf("stable %s disappeared silently — 不在 Targets / SkippedStable / Skipped（tl 1037 禁止静默）", id)
			continue
		}
		if inSkippedStable && !inSkipped {
			t.Errorf("stable %s in SkippedStable but no Skipped reason record (skippedCount 汇总看不到)", id)
		}
		if inSkipped && reason == "" {
			t.Errorf("stable %s Skipped carries no reason", id)
		}
	}
	// autopilot/fullAccess 排除名单优先（danger_gate / precondition）。
	if reasonBy["autopilot"] != LabBulkSkipPrecondition {
		t.Errorf("autopilot must carry precondition reason, got %q", reasonBy["autopilot"])
	}
	if reasonBy["fullAccess"] != LabBulkSkipDangerGate {
		t.Errorf("fullAccess must carry danger_gate reason, got %q", reasonBy["fullAccess"])
	}
	// sessionCollab/runtimeReuse 被护栏②挡，必须同时挂 dev_mode_off 原因。
	for _, id := range []string{"sessionCollab", "runtimeReuse"} {
		if reasonBy[id] != LabBulkSkipDevMode {
			t.Errorf("%s must carry dev_mode_off reason (护栏②), got %q", id, reasonBy[id])
		}
	}
	// 所有 Skipped 记录原因非空（杜绝空原因静默）。
	for _, s := range p.Skipped {
		if s.Reason == "" {
			t.Errorf("skipped %q has empty reason", s.ID)
		}
	}
}

// tl 1037 ①：stable 范围在真集合下可批量项真正落 Targets（sessionCollab /
// runtimeReuse 进，fullAccess / autopilot 排除，sessionWall 推荐档不进）。
func TestLabBulkStableScopeTargetsBulkableStable(t *testing.T) {
	p := PlanLabBulk(LabBulkScopeStable, true, LabStableFeatureIDs(), false)
	got := planIDs(p)
	if !got["sessionCollab"] || !got["runtimeReuse"] {
		t.Fatalf("stable scope must target sessionCollab+runtimeReuse, got %v", p.Targets)
	}
	if got["sessionWall"] {
		t.Fatalf("stable scope must not include sessionWall (recommended tier), got %v", p.Targets)
	}
	if got["autopilot"] || got["fullAccess"] {
		t.Fatalf("stable scope must exclude autopilot/fullAccess (排除名单), got %v", p.Targets)
	}
	// 非静默：autopilot/fullAccess 在 Skipped 且带原因码。
	reasonBy := map[string]string{}
	for _, s := range p.Skipped {
		reasonBy[s.ID] = s.Reason
	}
	if reasonBy["autopilot"] != LabBulkSkipPrecondition {
		t.Errorf("stable scope autopilot Skipped must carry precondition, got %q", reasonBy["autopilot"])
	}
	if reasonBy["fullAccess"] != LabBulkSkipDangerGate {
		t.Errorf("stable scope fullAccess Skipped must carry danger_gate, got %q", reasonBy["fullAccess"])
	}
}

// tl 1037 ①：批量开（devMode 关也挡不住开）四项在 recommended/on 面内可达
// —— autopilot/fullAccess 排除（危险门/前置），sessionCollab/runtimeReuse
// 稳定档开 → 进 Targets；runtimeReuse 真被写（验配置值变化）。
func TestLabBulkRecommendedOnWritesStableOn(t *testing.T) {
	c := Default()
	// 反转默认：把稳定档可批量项关掉，再批量开，验证值真正变。
	if err := c.SetExperimentalSessionCollab(false); err != nil {
		t.Fatal(err)
	}
	if err := c.SetExperimentalRuntimeReuse(false); err != nil {
		t.Fatal(err)
	}
	plan, applied, err := c.ApplyLabBulk(LabBulkScopeRecommended, true)
	if err != nil {
		t.Fatalf("ApplyLabBulk recommended/on: %v", err)
	}
	appliedSet := map[string]bool{}
	for _, id := range applied {
		appliedSet[id] = true
	}
	if !appliedSet["sessionCollab"] || !appliedSet["runtimeReuse"] {
		t.Fatalf("recommended/on must apply sessionCollab+runtimeReuse (四稳定档批量开可达), plan=%v applied=%v", plan.Targets, applied)
	}
	if !c.Agent.ExperimentalSessionCollab || !c.Agent.ExperimentalRuntimeReuse {
		t.Fatalf("config values must actually flip to on: collab=%v runtimeReuse=%v",
			c.Agent.ExperimentalSessionCollab, c.Agent.ExperimentalRuntimeReuse)
	}
	// autopilot/fullAccess 不被批量开（排除名单强制）。
	for _, id := range plan.Targets {
		if id == "autopilot" || id == "fullAccess" {
			t.Errorf("excluded %s must never be a bulk target, got %v", id, plan.Targets)
		}
	}
}
