package main

import (
	"testing"

	"reasonix/internal/config"
)

// Task 262 install-fix: the intake batch landed its config layer while the
// Wails App-method layer was missed — the frontend called into a method that
// did not exist, every call failed at runtime, and the switch clicked without
// ever saving (the installed user's "cannot turn quick commands on" report).
// These interface assertions are compile-time pins: deleting any wrapper below
// fails the build instead of resurfacing as a silently-dead switch. The value
// round trip itself is pinned in internal/config (render tests cover the
// nil-means-on defaults and the explicit off/on saves).
type labIntakeSwitches interface {
	SetExperimentalQuickCommands(bool) error
	SetExperimentalCompactionParallel(bool) error
	SetExperimentalContextBudget(bool) error
	SetExperimentalResearchBudget(bool) error
	SetExperimentalQuestionSearch(bool) error
	SetExperimentalSubagentPolicy(bool) error
	SetExperimentalSubagentTps(bool) error
	SetExperimentalCompletionSummary(bool) error
	// 任务 506: tab-strip adaptive compression (tiered tab width once >8 tabs).
	SetExperimentalTabCompress(bool) error
	// 任务 507: subagent detail view (row click → read-only in-dock detail + back).
	SetExperimentalSubagentDetail(bool) error
}

var _ labIntakeSwitches = (*App)(nil)

// 任务 506（铁律 2 第 4/5 段）：App 层 setter 落盘、启动视图与设置视图都
// 必须把保存后的开关值读回来——配置层往返由 internal/config 的 render 测试
// 钉住，这里钉 Wails 暴露面与两个视图映射面。
func TestSetExperimentalTabCompressPersistsAndReadsBack(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := &App{}

	if err := app.SetExperimentalTabCompress(true); err != nil {
		t.Fatalf("SetExperimentalTabCompress(true): %v", err)
	}
	cfg, err := config.LoadForEditReadOnlyStrict(config.UserConfigPath())
	if err != nil {
		t.Fatalf("load saved user config: %v", err)
	}
	if !cfg.Desktop.ExperimentalTabCompress {
		t.Fatal("saved user config must carry experimental_tab_compress = true")
	}
	if boot := app.DesktopStartupSettings(); !boot.ExperimentalTabCompress {
		t.Fatal("DesktopStartupSettings view must read back the saved switch")
	}
	if view := app.Settings(); !view.ExperimentalTabCompress {
		t.Fatal("Settings view must read back the saved switch")
	}

	if err := app.SetExperimentalTabCompress(false); err != nil {
		t.Fatalf("SetExperimentalTabCompress(false): %v", err)
	}
	cfg, err = config.LoadForEditReadOnlyStrict(config.UserConfigPath())
	if err != nil {
		t.Fatalf("reload user config after flip off: %v", err)
	}
	if cfg.Desktop.ExperimentalTabCompress {
		t.Fatal("flipping the switch back off must persist (never spring back on)")
	}
}

// 任务 507（铁律 2 双态开关）：App 层 setter 落盘、启动视图与设置视图都必须
// 把保存后的开关值读回来——配置层往返由 internal/config 的 render 测试钉住，
// 这里钉 Wails 暴露面与两个视图映射面。
func TestSetExperimentalSubagentDetailPersistsAndReadsBack(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := &App{}

	if err := app.SetExperimentalSubagentDetail(true); err != nil {
		t.Fatalf("SetExperimentalSubagentDetail(true): %v", err)
	}
	cfg, err := config.LoadForEditReadOnlyStrict(config.UserConfigPath())
	if err != nil {
		t.Fatalf("load saved user config: %v", err)
	}
	if !cfg.Desktop.ExperimentalSubagentDetail {
		t.Fatal("saved user config must carry experimental_subagent_detail = true")
	}
	if boot := app.DesktopStartupSettings(); !boot.ExperimentalSubagentDetail {
		t.Fatal("DesktopStartupSettings view must read back the saved switch")
	}
	if view := app.Settings(); !view.ExperimentalSubagentDetail {
		t.Fatal("Settings view must read back the saved switch")
	}

	if err := app.SetExperimentalSubagentDetail(false); err != nil {
		t.Fatalf("SetExperimentalSubagentDetail(false): %v", err)
	}
	cfg, err = config.LoadForEditReadOnlyStrict(config.UserConfigPath())
	if err != nil {
		t.Fatalf("reload user config after flip off: %v", err)
	}
	if cfg.Desktop.ExperimentalSubagentDetail {
		t.Fatal("flipping the switch back off must persist (never spring back on)")
	}
}
