package config

import (
	"fmt"
	"strings"
)

// 任务 1006 — 实验室批量开关：范围×行为两轴（用户 2026-10-19 设计）。
//
// 范围三档（无「全部」档——未稳定项不进批量，仅可单项手动）：
//   - stable      = 仅稳定档（tl 1037 后集合从 labFeatureTiers 派生，恒非空）
//   - recommended = 稳定 + 推荐
//   - optional    = 稳定 + 推荐 + 可选
// 行为两轴：on / off。
//
// 护栏（与 tl 1005 矩阵联动）：
//   ① 批量关含稳定档时带警示 —— 计划暴露 StableInScope，前端在确认前警示
//      （与单点关闭口径一致）；
//   ② 开发者模式关时批量关自动跳过稳定档并提示 —— 计划把稳定档从 Targets
//      移入 SkippedStable（开发者模式占位：1005 落地后接真值）。
//
// 批量只写「有布尔主开关且有既有 setter」的特性；三态键 / 需前置门控 /
// 需危险确认的特性不进批量，逐项列入 Skipped（原因码），由单项手动设置——
// 防止批量绕过 fullAccess 危险确认门（task 257）或 autopilot 的 yolo 前置。

// LabBulkScope 范围档（字符串值即前端传参，写错 = 0 目标 + 错误返回）。
type LabBulkScope string

const (
	LabBulkScopeStable      LabBulkScope = "stable"
	LabBulkScopeRecommended LabBulkScope = "recommended"
	LabBulkScopeOptional    LabBulkScope = "optional"
)

// LabBulkSkipReason 不进批量的原因码（前端按码映射文案）。
const (
	LabBulkSkipDangerGate   = "danger_gate"     // 需单项危险确认（fullAccess，task 257）
	LabBulkSkipPrecondition = "precondition"    // 有前置条件门（autopilot 需 yolo 审批模式）
	LabBulkSkipNotBool      = "not_bool_switch" // 主开关非布尔（三态/模式键），批量盲写会破坏语义
	LabBulkSkipNoSetter     = "no_bulk_setter"  // 未登记批量写入器（参数卡/家族键/纯展示行），需单项设置
	// 稳定档不在所选范围（tier=stable 不落 recommended/optional 的 tier 分支）。
	// tl 1037：此前走静默 continue，特性既不在 Targets 也不在任何 Skipped ⇒
	// 「范围内却看不到该项」；补记录后弹窗按码给文案，不再无声消失。
	LabBulkSkipStableScope = "stable_out_of_scope"
	// 开发者模式关 + 批量关 → 稳定档自动跳过（护栏②），记入 Skipped 使
	// skipCount 汇总含它（与 SkippedStable 并列，前端两处各司其职）。
	LabBulkSkipDevMode = "dev_mode_off"
)

// LabStableFeatureIDs —— 稳定档集合，**从 labFeatureTiers 派生**（tl 1037）。
//
// 1005 批1 把 autopilot / sessionCollab / fullAccess / runtimeReuse 升到
// LabTierStable 后，档位声明的唯一权威就是 labFeatureTiers；这里若再手写
// 一份集合就是双源（1037 缺陷根因：占位空 map 从未接线 ⇒ stable/recommended
// 范围恒不含稳定档，四项批量不可达且被静默 continue 吞掉）。派生后声明与
// 批量可达性同源，新条目升 stable 档自动进集合；一致性由
// TestLabStableFeatureIDsMatchesTierRegistry 守卫断言。
func LabStableFeatureIDs() map[string]bool {
	stable := make(map[string]bool, 4)
	for _, f := range labFeatureTiers {
		if f.tier == LabTierStable {
			stable[f.id] = true
		}
	}
	return stable
}

// LabDeveloperModeOn —— 开发者模式占位（tl 1005「允许关闭稳定能力」单项
// toggle 落地后改读该配置键；当前恒 false = 护栏②默认生效）。
func (c *Config) LabDeveloperModeOn() bool {
	return false
}

// LabBulkPlan 批量计划（纯计算，不写配置）。
type LabBulkPlan struct {
	Scope         LabBulkScope
	On            bool
	Targets       []string      // 实际要写的特性 id（布尔主开关，档位在范围内）
	StableInScope []string      // 目标中的稳定档 id（护栏①警示依据；dev-mode 跳过前）
	SkippedStable []string      // 护栏②：开发者模式关 + 批量关 → 自动跳过的稳定档 id
	Skipped       []LabBulkSkip // 范围内但不进批量的特性（逐项给原因码）
}

// LabBulkSkip 不进批量的单项记录。
type LabBulkSkip struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// labBulkSetters 批量写注册表：特性 id → 布尔主开关写入器。
//
// 只登记「主开关是纯布尔、单次写入无副作用、setter 语义与单项 toggle 完全
// 一致」的特性。家族子键（ask-timeout / nudge / perf_monitor 等）一律不动
// ——批量只翻主开关，细粒度选项保持用户现值。新增实验项入批量 = 加一行
// （前提：主开关布尔且无门控）。
var labBulkSetters = map[string]func(*Config, bool) error{
	// ── automation ──
	"sessionCollab": func(c *Config, on bool) error { return c.SetExperimentalSessionCollab(on) },
	// optimistic_parallel：master = sandbox.optimistic_write。桌面单项 toggle
	// 会尝试 rebuild；批量走 config-only（重启选项兜底），字段直写。
	"optimisticParallel": func(c *Config, on bool) error { c.Sandbox.OptimisticWrite = on; return nil },
	// ── efficiency ──
	"budgetControl":         func(c *Config, on bool) error { return c.SetExperimentalContextBudget(on) },
	"compressOpt":           func(c *Config, on bool) error { return c.SetExperimentalProactiveCompact(on) },
	"quickCommands":         func(c *Config, on bool) error { return c.SetExperimentalQuickCommands(on) },
	"highSpeedModel":        func(c *Config, on bool) error { return c.SetExperimentalHighSpeedModel(on) },
	"compactionParallel":    func(c *Config, on bool) error { return c.SetExperimentalCompactionParallel(on) },
	"traceAsState":          func(c *Config, on bool) error { return c.SetExperimentalTraceAsState(on) },
	"compactModel":          func(c *Config, on bool) error { return c.SetExperimentalCompactModel(on) },
	"outputStyle":           func(c *Config, on bool) error { return c.SetExperimentalOutputStyleUI(on) },
	"cacheTuning":           func(c *Config, on bool) error { return c.SetExperimentalCacheTuning(on) },
	"sessionCollabAutoFold": func(c *Config, on bool) error { return c.SetExperimentalSessionCollabAutoFold(on) },
	// ── ui ──
	"trajectoryView":      func(c *Config, on bool) error { return c.SetExperimentalTrajectoryView(on) },
	"tabCompress":         func(c *Config, on bool) error { return c.SetExperimentalTabCompress(on) },
	"todoSidebar":         func(c *Config, on bool) error { return c.SetExperimentalTodoSidebar(on) },
	"promptHistoryPicker": func(c *Config, on bool) error { return c.SetExperimentalPromptHistoryPicker(on) },
	"restartUpdate":       func(c *Config, on bool) error { return c.SetExperimentalRestartUpdate(on) },
	"sessionWall":         func(c *Config, on bool) error { return c.SetExperimentalSessionWall(on) },
	"subagentPanel":       func(c *Config, on bool) error { return c.SetExperimentalSubagentPanel(on) },
	"subagentDetail":      func(c *Config, on bool) error { return c.SetExperimentalSubagentDetail(on) },
	"completionSummary":   func(c *Config, on bool) error { return c.SetExperimentalCompletionSummary(on) },
	"autoLoadOlder":       func(c *Config, on bool) error { return c.SetExperimentalAutoLoadOlder(on) },
	"splitView":           func(c *Config, on bool) error { return c.SetExperimentalSplitView(on) },
	"draftPersistence":    func(c *Config, on bool) error { return c.SetExperimentalComposerDraft(on) },
	"selectionActions":    func(c *Config, on bool) error { c.Agent.ExperimentalSelectionActions = on; return nil },
	"questionSearch":      func(c *Config, on bool) error { return c.SetExperimentalQuestionSearch(on) },
	"feedback":            func(c *Config, on bool) error { return c.SetExperimentalFeedback(on) },
	"sidebarHoverLeft":    func(c *Config, on bool) error { return c.SetExperimentalSidebarHoverLeft(on) },
	"sidebarHoverRight":   func(c *Config, on bool) error { return c.SetExperimentalSidebarHoverRight(on) },
	"approvalSelect":      func(c *Config, on bool) error { return c.SetExperimentalApprovalSelect(on) },
	"tableWrap":           func(c *Config, on bool) error { return c.SetExperimentalTableWrap(on) },
	"sendTargetSelect":    func(c *Config, on bool) error { return c.SetExperimentalSendTargetSelect(on) },
	"draftRewrite":        func(c *Config, on bool) error { return c.SetExperimentalDraftRewrite(on) },
	// tl 1132：启动遮罩（默认关；纯布尔展示开关，无护栏/前置，随批量面）。
	"startupSplash": func(c *Config, on bool) error { return c.SetExperimentalStartupSplash(on) },
	// 任务 1056：网络重试预算分级（[network.retry_budget] 总开关；family 子键
	// （max_retries/dial_timeout_seconds 等）不进批量——批量只翻 enabled，
	// 细粒度旋钮保持用户现值）。
	"networkRetryBudget": func(c *Config, on bool) error {
		b := c.Network.RetryBudget
		b.Enabled = on
		return c.SetNetworkRetryBudget(b)
	},
	// ── observability ──
	"monitoring":  func(c *Config, on bool) error { return c.SetExperimentalSessionMonitor(on) },
	"subagentTps": func(c *Config, on bool) error { return c.SetExperimentalSubagentTps(on) },
	// ── automation（记忆整理族）──
	"dream":            func(c *Config, on bool) error { return c.SetExperimentalDream(on) },
	"decisionPatterns": func(c *Config, on bool) error { return c.SetExperimentalDecisionPatterns(on) },
	// 任务 1070：Standard 流续跑总闸（默认关；见 render.go lab 表注释）。
	"standardContinuation": func(c *Config, on bool) error { return c.SetStandardContinuation(on) },
	// tl 1105：中断自动续轮（默认关；outcome_unknown 中断→一次宿主续轮）。
	"interruptedAutoResume": func(c *Config, on bool) error { return c.SetInterruptedAutoResume(on) },
	"safetyCostControl":    func(c *Config, on bool) error { return c.SetExperimentalSafetyCostControl(on) },
	"stalledIntentNudge":   func(c *Config, on bool) error { return c.SetStalledIntentNudge(on) },
	// ── infra / storage ──
	"runtimeReuse":        func(c *Config, on bool) error { return c.SetExperimentalRuntimeReuse(on) },
	"orphanHandling":      func(c *Config, on bool) error { return c.SetExperimentalOrphanHandling(on) },
	"heartbeatBackground": func(c *Config, on bool) error { return c.SetExperimentalHeartbeatBackground(on) },
	// ── tool-opt ──
	"toolOptimizations":   func(c *Config, on bool) error { return c.SetExperimentalToolOptimizations(on) },
	"editEvidenceRelaxed": func(c *Config, on bool) error { return c.SetExperimentalEditEvidenceRelaxed(on) },
	"multiEditInjection":  func(c *Config, on bool) error { return c.SetExperimentalMultiEditInjection(on) },
	"capabilityGroups":    func(c *Config, on bool) error { return c.SetExperimentalCapabilityGroups(on) },
}

// labBulkExcluded 范围内但明确不进批量的特性：id → 原因码。
//
//   - fullAccess：单项开关带一次性危险确认（task 257「写入不再逐项批准」），
//     批量开若绕过该门等于静默放开全量写权限——护栏级回归，必须单项手动；
//   - autopilot：开有关联前置（desktop 默认审批模式必须 yolo，task 325），
//     批量直写会绕过或直接失败，交单项走既有拒绝路径；
//   - messageMerge：主开关是三态键 collab_inbox_merge（off|same_sender|all），
//     「开」没有唯一布尔语义；
//   - eventsRotation：主开关是三态键 events_auto_rotation（off|manual|…）。
var labBulkExcluded = map[string]string{
	"fullAccess":     LabBulkSkipDangerGate,
	"autopilot":      LabBulkSkipPrecondition,
	"messageMerge":   LabBulkSkipNotBool,
	"eventsRotation": LabBulkSkipNotBool,
}

// ParseLabBulkScope 校验范围串（未知值报错，前端传参 typo 不静默落空集）。
func ParseLabBulkScope(s string) (LabBulkScope, error) {
	switch LabBulkScope(strings.TrimSpace(s)) {
	case LabBulkScopeStable:
		return LabBulkScopeStable, nil
	case LabBulkScopeRecommended:
		return LabBulkScopeRecommended, nil
	case LabBulkScopeOptional:
		return LabBulkScopeOptional, nil
	default:
		return "", fmt.Errorf("unknown lab bulk scope %q (want stable | recommended | optional)", s)
	}
}

// labBulkScopeHas 判定 (id, tier) 是否落入范围档。
func labBulkScopeHas(scope LabBulkScope, id string, tier LabTier, stableIDs map[string]bool) bool {
	isStable := stableIDs[id]
	switch scope {
	case LabBulkScopeStable:
		return isStable
	case LabBulkScopeRecommended:
		return isStable || tier == LabTierRecommended
	case LabBulkScopeOptional:
		return isStable || tier == LabTierRecommended || tier == LabTierOptional
	default:
		return false
	}
}

// PlanLabBulk 纯计划：范围×行为 → 目标集 + 护栏输出。不写配置。
//
// 稳定档集合与开发者模式都经参数注入（LabStableFeatureIDs 从档位表派生 /
// LabDeveloperModeOn 占位恒 false），测试可用注入集合验证护栏。
// 任何不进 Targets 的特性都必须落在 Skipped / SkippedStable 之一并带原因
// —— tl 1037：杜绝「静默 continue」（范围判定不命中时原先无任何记录）。
func PlanLabBulk(scope LabBulkScope, on bool, stableIDs map[string]bool, devModeOn bool) LabBulkPlan {
	plan := LabBulkPlan{Scope: scope, On: on}
	for _, f := range labFeatureTiers {
		if !labBulkScopeHas(scope, f.id, f.tier, stableIDs) {
			// 范围外的推荐/可选项不进本范围是正常语义（不记）；但稳定档项
			// 在 stableIDs 里却未命中范围，只可能是「所选范围不含稳定档」
			// （stableIDs 注入与 scope 档位不一致的调用面）——记录并给原因，
			// 保证「选了范围却看不到该项」必定有提示。
			if stableIDs[f.id] {
				plan.Skipped = append(plan.Skipped, LabBulkSkip{ID: f.id, Reason: LabBulkSkipStableScope})
			}
			continue
		}
		if reason, excluded := labBulkExcluded[f.id]; excluded {
			plan.Skipped = append(plan.Skipped, LabBulkSkip{ID: f.id, Reason: reason})
			continue
		}
		if _, bulkable := labBulkSetters[f.id]; !bulkable {
			// 无批量写入器登记 = 参数卡 / 家族复合键 / 纯展示行 —— 需单项设置。
			plan.Skipped = append(plan.Skipped, LabBulkSkip{ID: f.id, Reason: LabBulkSkipNoSetter})
			continue
		}
		if stableIDs[f.id] {
			plan.StableInScope = append(plan.StableInScope, f.id)
			// 护栏②：开发者模式关 + 批量关 → 稳定档自动跳过并提示。
			if !on && !devModeOn {
				plan.SkippedStable = append(plan.SkippedStable, f.id)
				plan.Skipped = append(plan.Skipped, LabBulkSkip{ID: f.id, Reason: LabBulkSkipDevMode})
				continue
			}
		}
		plan.Targets = append(plan.Targets, f.id)
	}
	return plan
}

// ApplyLabBulk 执行批量写：单次 mutate 事务内逐目标调用其布尔 setter
// （与单项 toggle 完全同一写入路径，语义零漂移）。返回计划 + 已写 id 列表；
// 单个 setter 报错不中断其余目标，错误合并返回（配置在 SaveTo 前均为内存态，
// 失败目标仅缺写，不产生半写键）。
func (c *Config) ApplyLabBulk(scope LabBulkScope, on bool) (LabBulkPlan, []string, error) {
	plan := PlanLabBulk(scope, on, LabStableFeatureIDs(), c.LabDeveloperModeOn())
	var applied []string
	var errs []string
	for _, id := range plan.Targets {
		if err := labBulkSetters[id](c, on); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		applied = append(applied, id)
	}
	if len(errs) > 0 {
		return plan, applied, fmt.Errorf("lab bulk partial failure: %s", strings.Join(errs, "; "))
	}
	return plan, applied, nil
}
