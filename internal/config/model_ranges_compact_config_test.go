package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// tl1419 (迭代三): the model-driven ranges folds switch defaults off (iron
// rule 2), renders through the full chain (config → setter → render table —
// the 81/123 lost-save lesson), the flood-gate window knobs validate their
// ranges and resolve 0 = built-in defaults, and the package-level live reads
// follow the stored values without a restart.

func TestModelRangesCompactDefaultsOffAndRenders(t *testing.T) {
	cfg := Default()
	if cfg.Agent.ExperimentalModelRangesCompact {
		t.Fatalf("experimental_model_ranges_compact must default off (iron rule 2)")
	}
	if cfg.Agent.ModelRangesFoldWindowMinutes != 0 || cfg.Agent.ModelRangesFoldWindowMax != 0 {
		t.Fatalf("window knobs must default to 0 (= built-in defaults), got %d/%d",
			cfg.Agent.ModelRangesFoldWindowMinutes, cfg.Agent.ModelRangesFoldWindowMax)
	}
	cfg.Agent.ExperimentalModelRangesCompact = true
	if err := cfg.SetModelRangesFoldWindowMinutes(12); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetModelRangesFoldWindowMax(3); err != nil {
		t.Fatal(err)
	}
	out := RenderTOML(cfg)
	for _, key := range []string{
		"experimental_model_ranges_compact = true",
		"model_ranges_fold_window_minutes = 12",
		"model_ranges_fold_window_max = 3",
	} {
		if !strings.Contains(out, key) {
			t.Fatalf("rendered config missing %q:\n%s", key, out)
		}
	}
	// The risk hint rides the render comment (实验档纪律: 开关+风险提示文案).
	if !strings.Contains(out, "savings floor") {
		t.Fatalf("rendered config missing the model-ranges flood-gate hint:\n%s", out)
	}
	// Off state renders too (fixed-key-set rule: the key must always render or
	// a settings save drops it and the switch flips back).
	cfg.Agent.ExperimentalModelRangesCompact = false
	if err := cfg.SetModelRangesFoldWindowMinutes(0); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetModelRangesFoldWindowMax(0); err != nil {
		t.Fatal(err)
	}
	out = RenderTOML(cfg)
	if !strings.Contains(out, "experimental_model_ranges_compact = false") ||
		!strings.Contains(out, "model_ranges_fold_window_minutes = 0") ||
		!strings.Contains(out, "model_ranges_fold_window_max = 0") {
		t.Fatalf("off state must still render all family keys (fixed-key-set rule):\n%s", out)
	}
}

func TestModelRangesWindowSetterValidation(t *testing.T) {
	cfg := Default()
	for _, bad := range []int{-1, 1441, 20000} {
		if err := cfg.SetModelRangesFoldWindowMinutes(bad); err == nil {
			t.Fatalf("SetModelRangesFoldWindowMinutes(%d) must reject", bad)
		}
	}
	for _, ok := range []int{0, 1, 5, 1440} {
		if err := cfg.SetModelRangesFoldWindowMinutes(ok); err != nil {
			t.Fatalf("SetModelRangesFoldWindowMinutes(%d): %v", ok, err)
		}
	}
	for _, bad := range []int{-3, 51, 1000} {
		if err := cfg.SetModelRangesFoldWindowMax(bad); err == nil {
			t.Fatalf("SetModelRangesFoldWindowMax(%d) must reject", bad)
		}
	}
	for _, ok := range []int{0, 1, 2, 50} {
		if err := cfg.SetModelRangesFoldWindowMax(ok); err != nil {
			t.Fatalf("SetModelRangesFoldWindowMax(%d): %v", ok, err)
		}
	}
}

func TestModelRangesCompactLiveReads(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.SaveTo(filepath.Join(home, "config.toml")); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	// Off (the shipped default): the live read reports false and the window
	// resolves to the built-in defaults.
	if ModelRangesCompactLive() {
		t.Fatal("switch off: live read must be false")
	}
	minutes, maxFolds := ModelRangesFoldWindowLive()
	if minutes != 5 || maxFolds != 2 {
		t.Fatalf("default window = %d min / %d folds, want 5 / 2", minutes, maxFolds)
	}

	// On: persisted, and the next live reads see it without a restart.
	cfg, err = Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := cfg.SetExperimentalModelRangesCompact(true); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetModelRangesFoldWindowMinutes(10); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetModelRangesFoldWindowMax(4); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SaveTo(filepath.Join(home, "config.toml")); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	if !ModelRangesCompactLive() {
		t.Fatal("switch on: live read must follow the stored value")
	}
	minutes, maxFolds = ModelRangesFoldWindowLive()
	if minutes != 10 || maxFolds != 4 {
		t.Fatalf("window = %d min / %d folds, want 10 / 4", minutes, maxFolds)
	}

	// Off again round-trips (the kill-switch contract).
	cfg, err = Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := cfg.SetExperimentalModelRangesCompact(false); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SaveTo(filepath.Join(home, "config.toml")); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	if ModelRangesCompactLive() {
		t.Fatal("switch back off: live read must be false again")
	}
}
