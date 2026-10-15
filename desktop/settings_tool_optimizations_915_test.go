package main

import (
	"strings"
	"testing"

	"reasonix/internal/config"
)

// Task 915: the 工具优化 family view field is a *bool so the panel can tell
// "key absent" (nil → frontend default-on display, materialized by writing
// true) from an explicit off (false → stays off). Both settings views must
// carry the same nil-able shape (the 81/123 two-view rule).
func TestSettingsViewToolOptimizationsThreeStates(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()

	// State 1 — key absent (fresh install / reset): both views report nil.
	if got := app.Settings().ExperimentalToolOptimizations; got != nil {
		t.Fatalf("fresh Settings().ExperimentalToolOptimizations = %v, want nil (panel default-on)", *got)
	}
	if got := app.DesktopStartupSettings().ExperimentalToolOptimizations; got != nil {
		t.Fatalf("fresh startup view = %v, want nil", *got)
	}

	// State 2 — explicit on: both views carry true, config stores non-nil.
	if err := app.SetExperimentalToolOptimizations(true); err != nil {
		t.Fatalf("SetExperimentalToolOptimizations(true): %v", err)
	}
	if got := app.Settings().ExperimentalToolOptimizations; got == nil || !*got {
		t.Fatalf("Settings() after on = %v, want true", got)
	}
	if got := app.DesktopStartupSettings().ExperimentalToolOptimizations; got == nil || !*got {
		t.Fatalf("startup view after on = %v, want true", got)
	}
	if cfg := config.LoadForEdit(config.UserConfigPath()); !cfg.ToolOptimizationsEnabled() {
		t.Fatal("stored config must resolve the family as enabled after on")
	}

	// State 3 — explicit off: the stored value is a non-nil false and the
	// panel must render it as off (no nil fallback for an explicit choice).
	if err := app.SetExperimentalToolOptimizations(false); err != nil {
		t.Fatalf("SetExperimentalToolOptimizations(false): %v", err)
	}
	got := app.Settings().ExperimentalToolOptimizations
	if got == nil || *got {
		t.Fatalf("Settings() after off = %v, want explicit false", got)
	}
	if cfg := config.LoadForEdit(config.UserConfigPath()); cfg.ToolOptimizationsEnabled() {
		t.Fatal("stored config must resolve the family as disabled after off")
	}
	if cfg := config.LoadForEdit(config.UserConfigPath()); cfg.Agent.ExperimentalToolOptimizations == nil {
		t.Fatal("explicit off must persist a non-nil false (render must not drop it)")
	}
}

// Task 915 panel default-on materialization: writing the default (what the
// frontend does on first load when the view reports nil) puts the family key
// on disk and a rebuilt runtime surface then carries multi_edit.
func TestToolOptimizationsDefaultWriteMaterializesTheKey(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()

	// Fresh view is nil → the panel writes true (the acceptance flow).
	if err := app.SetExperimentalToolOptimizations(true); err != nil {
		t.Fatalf("materialize default: %v", err)
	}
	cfg := config.LoadForEdit(config.UserConfigPath())
	if cfg.Agent.ExperimentalToolOptimizations == nil || !*cfg.Agent.ExperimentalToolOptimizations {
		t.Fatalf("family key after materialization = %v, want true", cfg.Agent.ExperimentalToolOptimizations)
	}
	// A later unrelated save must keep the explicit true (81/123 lost-save).
	if err := cfg.SetDesktopStatusBarStyle("icon"); err != nil {
		t.Fatalf("unrelated save: %v", err)
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	after := config.LoadForEdit(config.UserConfigPath())
	if after.Agent.ExperimentalToolOptimizations == nil || !*after.Agent.ExperimentalToolOptimizations {
		t.Fatalf("explicit true lost across an unrelated save = %v", after.Agent.ExperimentalToolOptimizations)
	}
}

// Task 915 conditional render: an absent key stays absent across saves (the
// panel-layer default can never be baked into an explicit false), while both
// explicit values render.
func TestToolOptimizationsRenderConditional(t *testing.T) {
	absent := config.Default()
	if strings.Contains(config.RenderTOML(absent), "experimental_tool_optimizations =") {
		t.Fatal("absent family key must not render a line (panel default-on would be baked to false)")
	}
	explicitOff := config.Default()
	if err := explicitOff.SetExperimentalToolOptimizations(false); err != nil {
		t.Fatalf("set off: %v", err)
	}
	if !strings.Contains(config.RenderTOML(explicitOff), "experimental_tool_optimizations = false") {
		t.Fatal("explicit false must render (81/123 lost-save rule)")
	}
	explicitOn := config.Default()
	if err := explicitOn.SetExperimentalToolOptimizations(true); err != nil {
		t.Fatalf("set on: %v", err)
	}
	if !strings.Contains(config.RenderTOML(explicitOn), "experimental_tool_optimizations = true") {
		t.Fatal("explicit true must render")
	}
}
