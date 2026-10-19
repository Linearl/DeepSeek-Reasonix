package main

import (
	"reasonix/internal/config"
)

// 任务 1006 — 实验室批量开关（范围×行为两轴 + 重启两选项）的桌面绑定层。
//
// 计算与护栏全在 config.PlanLabBulk（纯函数，可测）；本层只做三件事：
// ① 把计划投影成前端视图；② Apply 走既有 applyConfigOnly 单事务写入；
// ③ NeedsRestart 恒 true——批量面覆盖大量 boot-snapshot 开关，写入即下次
//    启动生效（task 987「需重启明示」口径），前端据此给「立即重启/稍后
//    重启」两选项（稍后 = 配置已写入，不打断在飞任务）。

// LabBulkSkipView 单项不进批量的记录（原因码映射见前端文案表）。
type LabBulkSkipView struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// LabBulkPlanView 批量计划视图（确认前的预览 + 执行后的回执共用）。
type LabBulkPlanView struct {
	Scope         string           `json:"scope"`
	On            bool             `json:"on"`
	Targets       []string         `json:"targets"`
	StableInScope []string         `json:"stableInScope"`
	SkippedStable []string         `json:"skippedStable"`
	Skipped       []LabBulkSkipView `json:"skipped"`
	// Applied 仅执行后非空（预览为 null）——已写配置的特性 id。
	Applied []string `json:"applied"`
	// NeedsRestart 恒 true：配置已写入，下次启动生效。
	NeedsRestart bool `json:"needsRestart"`
}

func labBulkPlanView(plan config.LabBulkPlan, applied []string, executed bool) LabBulkPlanView {
	v := LabBulkPlanView{
		Scope:         string(plan.Scope),
		On:            plan.On,
		Targets:       plan.Targets,
		StableInScope: plan.StableInScope,
		SkippedStable: plan.SkippedStable,
		Skipped:       make([]LabBulkSkipView, 0, len(plan.Skipped)),
		Applied:       applied,
		NeedsRestart:  true,
	}
	for _, s := range plan.Skipped {
		v.Skipped = append(v.Skipped, LabBulkSkipView{ID: s.ID, Reason: s.Reason})
	}
	if !executed {
		v.Applied = nil
	}
	if v.Targets == nil {
		v.Targets = []string{}
	}
	if v.StableInScope == nil {
		v.StableInScope = []string{}
	}
	if v.SkippedStable == nil {
		v.SkippedStable = []string{}
	}
	return v
}

// LabBulkPlan 返回范围×行为的批量计划（纯读，不写配置）——前端弹窗在
// 用户切换范围/行为时调用，用目标数与护栏输出驱动确认前的警示。
func (a *App) LabBulkPlan(scope string, on bool) (LabBulkPlanView, error) {
	parsed, err := config.ParseLabBulkScope(scope)
	if err != nil {
		return LabBulkPlanView{}, err
	}
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		cfg = config.Default()
	}
	plan := config.PlanLabBulk(parsed, on, config.LabStableFeatureIDs(), cfg.LabDeveloperModeOn())
	return labBulkPlanView(plan, nil, false), nil
}

// LabBulkApply 执行批量写：单次 applyConfigOnly 事务（一次读改写，非逐项
// 落盘）；成功后返回计划 + 已写清单，前端随即展示重启两选项。
func (a *App) LabBulkApply(scope string, on bool) (LabBulkPlanView, error) {
	parsed, err := config.ParseLabBulkScope(scope)
	if err != nil {
		return LabBulkPlanView{}, err
	}
	var plan config.LabBulkPlan
	var applied []string
	if err := a.applyConfigOnly(func(c *config.Config) error {
		var err error
		plan, applied, err = c.ApplyLabBulk(parsed, on)
		return err
	}); err != nil {
		return LabBulkPlanView{}, err
	}
	return labBulkPlanView(plan, applied, true), nil
}
