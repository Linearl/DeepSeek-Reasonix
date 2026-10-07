package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// Task 468: the write side of the provider-level HighSpeedModels list (the
// 318.1 lane's user-maintained input) and its render rows. Before this task
// the field existed only on the struct: the renderer had no high_speed_models
// line, so even a hand-written entry was wiped by the next settings save (the
// vision_models class of loss — render.go renders both, decode restores both).

func task468ProviderWithModels() *Config {
	cfg := Default()
	cfg.Providers = []ProviderEntry{{
		Name:    "prov",
		Kind:    "openai",
		BaseURL: "https://example.com/v1",
		Models:  []string{"fast-1", "slow-1"},
	}}
	cfg.DefaultModel = "prov/fast-1"
	return cfg
}

func TestTask468HighSpeedSetterMarkUnmarkRoundTrip(t *testing.T) {
	cfg := task468ProviderWithModels()

	// Mark: the configured model joins the list.
	if err := cfg.SetProviderModelHighSpeed("prov", "fast-1", true); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if got := cfg.Providers[0].HighSpeedModels; len(got) != 1 || got[0] != "fast-1" {
		t.Fatalf("after mark: %v", got)
	}
	// Marking twice is a no-op (no duplicates).
	if err := cfg.SetProviderModelHighSpeed("prov", " fast-1 ", true); err != nil {
		t.Fatalf("re-mark: %v", err)
	}
	if got := cfg.Providers[0].HighSpeedModels; len(got) != 1 {
		t.Fatalf("re-mark duplicated the entry: %v", got)
	}
	// Marking a model the provider does not list is rejected — the list must
	// never go stale.
	if err := cfg.SetProviderModelHighSpeed("prov", "ghost", true); err == nil {
		t.Fatal("marking an unconfigured model must fail")
	}
	// Unmark removes; unmarking an unmarked model is a no-op.
	if err := cfg.SetProviderModelHighSpeed("prov", "fast-1", false); err != nil {
		t.Fatalf("unmark: %v", err)
	}
	if got := cfg.Providers[0].HighSpeedModels; got != nil {
		t.Fatalf("after unmark: %v, want nil (empty list drops out)", got)
	}
	if err := cfg.SetProviderModelHighSpeed("prov", "fast-1", false); err != nil {
		t.Fatalf("unmark unmarked: %v", err)
	}
	// Unknown provider and empty model are errors.
	if err := cfg.SetProviderModelHighSpeed("missing", "fast-1", true); err == nil {
		t.Fatal("unknown provider must fail")
	}
	if err := cfg.SetProviderModelHighSpeed("prov", "  ", true); err == nil {
		t.Fatal("empty model must fail")
	}
}

func TestTask468HighSpeedRendersBothPathsAndSurvivesDecode(t *testing.T) {
	cfg := task468ProviderWithModels()
	if err := cfg.SetProviderModelHighSpeed("prov", "fast-1", true); err != nil {
		t.Fatalf("mark: %v", err)
	}

	// Full render (settings saves drive this): the line must be present, in
	// the vision_models slot of the provider block, and decode must restore it.
	full := RenderTOML(cfg)
	if !strings.Contains(full, "high_speed_models = [\"fast-1\"]") {
		t.Fatalf("full render lost high_speed_models:\n%s", full)
	}
	decoded := Default()
	if _, err := toml.Decode(full, &decoded); err != nil {
		t.Fatalf("decode full render: %v", err)
	}
	if len(decoded.Providers) != 1 {
		t.Fatalf("decoded providers: %d", len(decoded.Providers))
	}
	if got := decoded.Providers[0].HighSpeedModels; len(got) != 1 || got[0] != "fast-1" {
		t.Fatalf("high_speed_models lost in decode: %v", got)
	}
	// RenderTOMLForScope(user) shares the full provider loop.
	if !strings.Contains(RenderTOMLForScope(cfg, RenderScopeUser), "high_speed_models = [\"fast-1\"]") {
		t.Fatalf("user-scope render lost high_speed_models")
	}
	// Project delta render: the second provider loop (same class as
	// vision_models) must carry the row too.
	delta := RenderTOMLProjectDelta(cfg)
	if !strings.Contains(delta, "high_speed_models = [\"fast-1\"]") {
		t.Fatalf("project delta render lost high_speed_models:\n%s", delta)
	}

	// An unmarked provider renders no line at all (empty never lingers).
	plain := task468ProviderWithModels()
	if strings.Contains(RenderTOML(plain), "high_speed_models") {
		t.Fatalf("unmarked provider rendered a high_speed_models line:\n%s", RenderTOML(plain))
	}
}

func TestTask468HighSpeedSurvivesSaveLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.Providers = []ProviderEntry{{
		Name:    "prov",
		Kind:    "openai",
		BaseURL: "https://example.com/v1",
		Models:  []string{"fast-1", "slow-1"},
	}}
	if err := cfg.SetProviderModelHighSpeed("prov", "fast-1", true); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if err := cfg.SaveTo(filepath.Join(home, "config.toml")); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	reloaded, err := Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded.Providers) != 1 {
		t.Fatalf("providers lost across save/load: %d", len(reloaded.Providers))
	}
	got := reloaded.Providers[0].HighSpeedModels
	if len(got) != 1 || got[0] != "fast-1" {
		t.Fatalf("high_speed_models lost across save/load: %v", got)
	}
}
