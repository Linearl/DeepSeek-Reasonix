package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// tl1397 (迭代一): the ranges small-segment incremental compression switch
// defaults off (iron rule 2), renders through the full chain (config →
// setter → render table — the 81/123 lost-save lesson), and the package-level
// live read follows the stored value without a restart.

func TestRangesCompactDefaultsOffAndRenders(t *testing.T) {
	cfg := Default()
	if cfg.Agent.ExperimentalRangesCompact {
		t.Fatalf("experimental_ranges_compact must default off (iron rule 2)")
	}
	cfg.Agent.ExperimentalRangesCompact = true
	out := RenderTOML(cfg)
	if !strings.Contains(out, "experimental_ranges_compact = true") {
		t.Fatalf("rendered config missing the ranges switch:\n%s", out)
	}
	// The risk hint rides the render comment so an on-by-hand user sees the
	// experiment's trade-off next to the switch (实验档纪律: 开关+风险提示文案).
	if !strings.Contains(out, "byte-stable head prefix") {
		t.Fatalf("rendered config missing the ranges risk hint:\n%s", out)
	}
	// Off state renders too (fixed-key-set rule: the key must always render or
	// a settings save drops it and the switch flips back).
	cfg.Agent.ExperimentalRangesCompact = false
	if out := RenderTOML(cfg); !strings.Contains(out, "experimental_ranges_compact = false") {
		t.Fatalf("off state must still render the key (fixed-key-set rule):\n%s", out)
	}
}

func TestRangesCompactSetterAndLiveRead(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.SaveTo(filepath.Join(home, "config.toml")); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	// Off (the shipped default): the live read reports false and the off path
	// is byte-for-byte today's behavior.
	if RangesCompactLive() {
		t.Fatal("switch off: live read must be false")
	}

	// On: persisted, and the next live read sees it without a restart.
	cfg, err = Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := cfg.SetExperimentalRangesCompact(true); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SaveTo(filepath.Join(home, "config.toml")); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	if !RangesCompactLive() {
		t.Fatal("switch on: live read must follow the stored value")
	}

	// Off again round-trips (the kill-switch contract).
	cfg, err = Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := cfg.SetExperimentalRangesCompact(false); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SaveTo(filepath.Join(home, "config.toml")); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	if RangesCompactLive() {
		t.Fatal("switch back off: live read must be false again")
	}
}
