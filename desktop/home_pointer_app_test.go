package main

// Task 846: binding-level tests for the change-data-home wizard. The heavy
// transaction tests live in internal/homemigrate; this file pins the wizard's
// write-path gates (验收 1) and the six-risk payload (验收 3) at the Wails
// surface.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/homemigrate"
)

func seedLiveHome(t *testing.T) string {
	t.Helper()
	home := config.ReasonixHomeDir()
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{".env", "settings.json", "skills/demo/SKILL.md"} {
		path := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("task846\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// config.toml must be loadable — the wizard's switch gate reads it.
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("default_model = \"test-model\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

func allRiskIDsForWizard(t *testing.T) []string {
	t.Helper()
	var ids []string
	for _, risk := range homemigrate.AllRisks() {
		ids = append(ids, string(risk.ID))
	}
	return ids
}

// 验收 3 的绑定层对位：向导预览必须携带恰好六条风险。
func TestHomeMigrationPreviewCarriesSixRisks(t *testing.T) {
	isolateDesktopUserDirs(t)
	seedLiveHome(t)
	app := NewApp()

	preview := app.HomeMigrationPreview(filepath.Join(t.TempDir(), "migrated"))
	if !preview.Valid {
		t.Fatalf("preview should validate: %s", preview.Error)
	}
	if len(preview.Risks) != 6 {
		t.Fatalf("wizard preview carries %d risks, the task-846 contract pins 6", len(preview.Risks))
	}
	seen := map[string]bool{}
	for _, risk := range preview.Risks {
		if seen[string(risk.ID)] {
			t.Fatalf("duplicate risk id %q in preview payload", risk.ID)
		}
		seen[string(risk.ID)] = true
		if risk.DetailKey == "" {
			t.Fatalf("risk %q must name a locale detail key", risk.ID)
		}
	}
}

func TestHomeMigrationPreviewRejectsBadTargets(t *testing.T) {
	isolateDesktopUserDirs(t)
	seedLiveHome(t)
	app := NewApp()

	preview := app.HomeMigrationPreview(config.ReasonixHomeDir())
	if preview.Valid {
		t.Fatal("target == source must not validate")
	}
	if preview.Error == "" {
		t.Fatal("refused preview must carry the reason")
	}
}

// 验收 1 的写路径对位：实验开关关 ⇒ Execute / Reset 直接拒绝（即使 ack 齐全）。
func TestHomeMigrationWritePathsRequireExperimentSwitch(t *testing.T) {
	isolateDesktopUserDirs(t)
	seedLiveHome(t)
	app := NewApp()

	ack := allRiskIDsForWizard(t)
	target := filepath.Join(t.TempDir(), "migrated")

	if _, err := app.HomeMigrationExecute(target, ack); err == nil || !strings.Contains(err.Error(), "experimental_home_pointer") {
		t.Fatalf("execute must refuse while the switch is off, got %v", err)
	}
	if err := app.HomeMigrationResetToDefault(); err == nil || !strings.Contains(err.Error(), "experimental_home_pointer") {
		t.Fatalf("reset must refuse while the switch is off, got %v", err)
	}
	// And nothing was written.
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("refused execute must not touch the target: %v", err)
	}
	pointer, err := config.ReadHomePointerFile(config.DefaultHomePointerPath())
	if err != nil || pointer != nil {
		t.Fatalf("refused execute must not write a pointer: (%v, %v)", pointer, err)
	}
}

// 开关开 + 全风险确认 ⇒ 事务全通：指针切到目标，目标拿到全部文件。
func TestHomeMigrationExecuteFullFlowThroughBinding(t *testing.T) {
	isolateDesktopUserDirs(t)
	source := seedLiveHome(t)
	app := NewApp()

	// Flip the lab switch through the real config write path.
	if err := app.applyConfigChange(func(cfg *config.Config) error {
		return cfg.SetExperimentalHomePointer(true)
	}); err != nil {
		t.Fatalf("enable switch: %v", err)
	}

	target := filepath.Join(t.TempDir(), "migrated")
	result, err := app.HomeMigrationExecute(target, allRiskIDsForWizard(t))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !result.Success || result.Phase != "done" {
		t.Fatalf("migration should complete, got %+v", result)
	}
	pointer, err := config.ReadHomePointerFile(config.DefaultHomePointerPath())
	if err != nil || pointer == nil {
		t.Fatalf("pointer missing after migration: %v", err)
	}
	if !strings.EqualFold(pointer.Home, target) && pointer.Home != target {
		t.Fatalf("pointer = %q, want %q", pointer.Home, target)
	}
	for _, rel := range []string{"config.toml", ".env", "settings.json", "skills/demo/SKILL.md"} {
		if _, statErr := os.Stat(filepath.Join(target, filepath.FromSlash(rel))); statErr != nil {
			t.Fatalf("copied file missing: %s (%v)", rel, statErr)
		}
	}
	if source == target {
		t.Fatal("sanity: source and target must differ")
	}

	// Reset (switch still on) removes the pointer again.
	if err := app.HomeMigrationResetToDefault(); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if _, err := os.Stat(config.DefaultHomePointerPath()); !os.IsNotExist(err) {
		t.Fatalf("pointer should be gone after reset: %v", err)
	}
}

// 状态视图：指针生效时 PointerActive 亮起，开关状态照实透出。
func TestHomePointerStatusView(t *testing.T) {
	isolateDesktopUserDirs(t)
	seedLiveHome(t)
	app := NewApp()

	view := app.HomePointerStatus()
	if view.ExperimentalEnabled {
		t.Fatal("switch should read off before anyone flips it")
	}
	if view.PointerActive {
		t.Fatalf("no pointer exists yet, but PointerActive=%v", view.PointerActive)
	}
	if view.DefaultHome == "" || view.ActiveHome == "" {
		t.Fatalf("status must resolve both homes, got %+v", view)
	}
	if view.PointerPath != config.DefaultHomePointerPath() {
		t.Fatalf("PointerPath = %q, want %q", view.PointerPath, config.DefaultHomePointerPath())
	}

	// Write a pointer at the default home, point the process env at it (as
	// the startup hook would), and re-read: now active.
	custom := view.DefaultHome + string(filepath.Separator) + "custom"
	if err := os.MkdirAll(custom, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteHomePointerFile(config.DefaultHomePointerPath(), custom); err != nil {
		t.Fatalf("seed pointer: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(config.DefaultHomePointerPath()) })
	t.Setenv("REASONIX_HOME", custom)

	view = app.HomePointerStatus()
	if !view.PointerActive {
		t.Fatalf("pointer exists and env follows it, but PointerActive=%v (view %+v)", view.PointerActive, view)
	}
}
