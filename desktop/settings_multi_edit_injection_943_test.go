package main

import (
	"strings"
	"testing"

	"reasonix/internal/config"
)

// 任务 943：multi_edit 直注独立键（原「工具优化」族拆分）的 desktop 视图/
// setter/迁移回落三面钉。81/123 两视图规则：两个 settings 视图必须同形
// 携带 *bool（键缺省 → null 到面板）。
func TestSettingsViewMultiEditInjectionThreeStates(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()

	// State 1 — 键缺省：两视图报 nil（Go 侧回落族键，面板按回落链显示）。
	if got := app.Settings().ExperimentalMultiEditInjection; got != nil {
		t.Fatalf("fresh Settings().ExperimentalMultiEditInjection = %v, want nil (fallback key)", *got)
	}
	if got := app.DesktopStartupSettings().ExperimentalMultiEditInjection; got != nil {
		t.Fatalf("fresh startup view = %v, want nil", *got)
	}

	// State 2 — 显式开：两视图报 true，落盘非 nil。
	if err := app.SetExperimentalMultiEditInjection(true); err != nil {
		t.Fatalf("SetExperimentalMultiEditInjection(true): %v", err)
	}
	if got := app.Settings().ExperimentalMultiEditInjection; got == nil || !*got {
		t.Fatalf("Settings() after on = %v, want true", got)
	}
	if got := app.DesktopStartupSettings().ExperimentalMultiEditInjection; got == nil || !*got {
		t.Fatalf("startup view after on = %v, want true", got)
	}
	if cfg := config.LoadForEdit(config.UserConfigPath()); !cfg.MultiEditInjectionEnabled() {
		t.Fatal("stored config must resolve injection as enabled after on")
	}

	// State 3 — 显式关：非 nil false 落盘（渲染不丢），回落被显式值压过。
	if err := app.SetExperimentalMultiEditInjection(false); err != nil {
		t.Fatalf("SetExperimentalMultiEditInjection(false): %v", err)
	}
	got := app.Settings().ExperimentalMultiEditInjection
	if got == nil || *got {
		t.Fatalf("Settings() after off = %v, want explicit false", got)
	}
	cfg := config.LoadForEdit(config.UserConfigPath())
	if cfg.MultiEditInjectionEnabled() {
		t.Fatal("stored config must resolve injection as disabled after off")
	}
	if cfg.Agent.ExperimentalMultiEditInjection == nil {
		t.Fatal("explicit off must persist a non-nil false (render must not drop it)")
	}
}

// 任务 943 迁移回落的 desktop 面：族键显式开、注入键缺省 →
// MultiEditInjectionEnabled 解析为开（存量用户拆分后注入面不丢）；族键
// 显式关、注入键缺省 → 解析为关（不会被悄悄打开）。
func TestMultiEditInjectionFallsBackToLegacyFamilyKey(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()

	if err := app.SetExperimentalToolOptimizations(true); err != nil {
		t.Fatalf("SetExperimentalToolOptimizations(true): %v", err)
	}
	cfg := config.LoadForEdit(config.UserConfigPath())
	if cfg.Agent.ExperimentalMultiEditInjection != nil {
		t.Fatalf("setting the family key must not materialize the injection key (read %v)", cfg.Agent.ExperimentalMultiEditInjection)
	}
	if !cfg.MultiEditInjectionEnabled() {
		t.Fatal("legacy family-on config must keep the injection face through the task-943 fallback")
	}

	// 族键显式关（存量用户关过整个族）：回落关，面板不显示开。
	if err := app.SetExperimentalToolOptimizations(false); err != nil {
		t.Fatalf("SetExperimentalToolOptimizations(false): %v", err)
	}
	cfg = config.LoadForEdit(config.UserConfigPath())
	if cfg.MultiEditInjectionEnabled() {
		t.Fatal("legacy family-off config must keep injection off through the fallback")
	}
}

// 任务 943 条件渲染：注入键缺席不渲染（回落永不被拍成显式值），显式
// true/false 都渲染（81/123 lost-save）。
func TestMultiEditInjectionRenderConditional(t *testing.T) {
	absent := config.Default()
	if strings.Contains(config.RenderTOML(absent), "experimental_multi_edit_injection =") {
		t.Fatal("absent injection key must not render a line (the migration fallback would be frozen)")
	}
	explicitOff := config.Default()
	if err := explicitOff.SetExperimentalMultiEditInjection(false); err != nil {
		t.Fatalf("set off: %v", err)
	}
	if !strings.Contains(config.RenderTOML(explicitOff), "experimental_multi_edit_injection = false") {
		t.Fatal("explicit false must render (81/123 lost-save rule)")
	}
	explicitOn := config.Default()
	if err := explicitOn.SetExperimentalMultiEditInjection(true); err != nil {
		t.Fatalf("set on: %v", err)
	}
	if !strings.Contains(config.RenderTOML(explicitOn), "experimental_multi_edit_injection = true") {
		t.Fatal("explicit true must render")
	}
}
