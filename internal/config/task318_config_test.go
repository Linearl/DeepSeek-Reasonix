package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// Task 318: the three lab switches default off, render through the full chain
// (config → setter → render table — the 81/123 lost-save lesson), and the fold
// cooldown normalizes to the historical 10 minutes.

func TestTask318SwitchesDefaultOffAndRender(t *testing.T) {
	cfg := Default()
	if cfg.Agent.ExperimentalHighSpeedModel || cfg.Agent.ExperimentalProactiveCompact || cfg.Agent.ExperimentalComposerDraft {
		t.Fatalf("task 318 switches must default off: %+v", cfg.Agent)
	}
	if cfg.Agent.ProactiveCompactCooldownMinutes != 0 {
		t.Fatalf("cooldown must render its stored value (0 = use the 10-minute default), got %d", cfg.Agent.ProactiveCompactCooldownMinutes)
	}

	cfg.Agent.ExperimentalHighSpeedModel = true
	cfg.Agent.ExperimentalProactiveCompact = true
	cfg.Agent.ExperimentalComposerDraft = true
	if err := cfg.SetProactiveCompactCooldownMinutes(25); err != nil {
		t.Fatalf("SetProactiveCompactCooldownMinutes: %v", err)
	}
	out := RenderTOML(cfg)
	for _, want := range []string{
		"experimental_high_speed_model = true",
		"experimental_proactive_compact = true",
		"proactive_compact_cooldown_minutes = 25",
		"experimental_composer_draft = true",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered config missing %q:\n%s", want, out)
		}
	}
}

func TestTask318CooldownNormalizesAndLives(t *testing.T) {
	cfg := Default()
	// Off (default): live reads report 0 = "the hard-coded interval governs".
	if got := cfg.ProactiveCompactCooldownMinutesLive(); got != 0 {
		t.Fatalf("switch off: live cooldown = %d, want 0", got)
	}
	if err := cfg.SetExperimentalProactiveCompact(true); err != nil {
		t.Fatal(err)
	}
	// On with nothing stored: the historical 10 minutes.
	if got := cfg.ProactiveCompactCooldownMinutesLive(); got != 10 {
		t.Fatalf("on + unset: live cooldown = %d, want 10", got)
	}
	// 0/negative normalize to 10 on write, so an explicit "on with default"
	// and a fresh config share one behavior.
	if err := cfg.SetProactiveCompactCooldownMinutes(0); err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.ProactiveCompactCooldownMinutes != 10 {
		t.Fatalf("0 minutes stored as %d, want 10", cfg.Agent.ProactiveCompactCooldownMinutes)
	}
	if err := cfg.SetProactiveCompactCooldownMinutes(3); err != nil {
		t.Fatal(err)
	}
	if got := cfg.ProactiveCompactCooldownMinutesLive(); got != 3 {
		t.Fatalf("on + 3 minutes: live cooldown = %d, want 3", got)
	}
}

func TestTask318SettersRoundTripThroughSaveLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.SetExperimentalHighSpeedModel(true); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetExperimentalProactiveCompact(true); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetProactiveCompactCooldownMinutes(7); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetExperimentalComposerDraft(true); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SaveTo(filepath.Join(home, "config.toml")); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	reloaded, err := Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.Agent.ExperimentalHighSpeedModel || !reloaded.Agent.ExperimentalProactiveCompact ||
		reloaded.Agent.ProactiveCompactCooldownMinutes != 7 || !reloaded.Agent.ExperimentalComposerDraft {
		t.Fatalf("task 318 switches lost across save/load: %+v", reloaded.Agent)
	}
}
