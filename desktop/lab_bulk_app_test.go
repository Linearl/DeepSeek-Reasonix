package main

import (
	"reasonix/internal/config"
	"testing"
)

// 任务 1006 桌面绑定层验收：
// ② 重启语义 —— 计划与执行回执都 NeedsRestart=true（配置已写入、下次启动
//    生效；task 987「需重启明示」由该字段驱动前端弹窗文案）；
// ③ 护栏投影 —— StableInScope / SkippedStable 原样透传（当前占位下均为空，
//    1005 合入后无需改本层）；
// ④ 非法 scope 拒绝（不静默落空集）。

func TestLabBulkPlanViewProjection(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := &App{}

	v, err := app.LabBulkPlan("optional", true)
	if err != nil {
		t.Fatalf("LabBulkPlan(optional): %v", err)
	}
	if v.Scope != "optional" || !v.On {
		t.Fatalf("view must echo scope/on, got %+v", v)
	}
	if !v.NeedsRestart {
		t.Fatalf("plan view must carry NeedsRestart=true (987 需重启明示)")
	}
	if v.Applied != nil {
		t.Fatalf("preview must not carry applied list, got %v", v.Applied)
	}
	if len(v.Targets) == 0 {
		t.Fatalf("optional scope should have bulkable targets under placeholder registry")
	}
	// 空集字段序列化为 [] 而非 null（前端免判空）。
	if v.StableInScope == nil || v.SkippedStable == nil || v.Skipped == nil {
		t.Fatalf("empty slices must project as [], got %+v", v)
	}

	if _, err := app.LabBulkPlan("all", true); err == nil {
		t.Fatalf("scope 'all' must be rejected at the binding layer")
	}
	if _, err := app.LabBulkApply("unstable", false); err == nil {
		t.Fatalf("scope 'unstable' must be rejected at the binding layer")
	}
}

func TestLabBulkApplyWritesUserConfig(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := &App{}

	v, err := app.LabBulkApply("recommended", true)
	if err != nil {
		t.Fatalf("LabBulkApply(recommended/on): %v", err)
	}
	if len(v.Applied) == 0 || len(v.Applied) != len(v.Targets) {
		t.Fatalf("applied must cover every target, got applied=%v targets=%v", v.Applied, v.Targets)
	}
	if !v.NeedsRestart {
		t.Fatalf("apply receipt must carry NeedsRestart=true (稍后重启=下次启动生效)")
	}

	// 落盘核验：配置文件真的写了（不是只在内存态）。
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if !cfg.Desktop.ExperimentalSessionWall || !cfg.Agent.ExperimentalSessionCollab {
		t.Fatalf("recommended/on must persist session_wall + session_collab, got wall=%v collab=%v",
			cfg.Desktop.ExperimentalSessionWall, cfg.Agent.ExperimentalSessionCollab)
	}
	// 范围外（未稳定）不被误写。
	if cfg.Desktop.ExperimentalCollabHub {
		t.Fatalf("bulk must not touch unstable-tier keys")
	}

	// 反向关。
	if _, err := app.LabBulkApply("recommended", false); err != nil {
		t.Fatalf("LabBulkApply(recommended/off): %v", err)
	}
	cfg, err = config.Load()
	if err != nil {
		t.Fatalf("reload config after off: %v", err)
	}
	if cfg.Desktop.ExperimentalSessionWall || cfg.Agent.ExperimentalSessionCollab {
		t.Fatalf("recommended/off must clear the master switches on disk")
	}
}
