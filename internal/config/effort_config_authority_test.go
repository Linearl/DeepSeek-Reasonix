package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Task 331: pin the enumeration authority chain for a mixed-gateway DeepSeek
// provider — [providers] supported_efforts (and model_overrides) outrank the
// built-in model table and the protocol defaults, and neither layer injects
// another family's vocabulary. The installed 1615 build's 3-tier menu came
// from the display-side fold (task 301), not from these layers; this test
// keeps the backend answer honest so any future regression here cannot hide
// behind "the UI folds it anyway".
func TestEffortConfigOverrideAuthorityForDeepSeekProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	cfgBody := `default_model = "deepseek-flash/deepseek-v4-flash"

[[providers]]
name     = "deepseek-flash"
kind     = "openai"
base_url = "https://api.deepseek.com"
models   = ["deepseek-v4-flash", "deepseek-v4-flash-vision-exp"]
default  = "deepseek-v4-flash"
supported_efforts = ["disabled", "low", "high", "max"]
default_effort    = "high"
model_overrides   = { "deepseek-v4-flash" = { supported_efforts = ["disabled", "low", "high", "max"], default_effort = "high" } }
`
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfgBody), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, ref := range []string{"deepseek-v4-flash", "deepseek-v4-flash-vision-exp"} {
		resolved, _, ok := cfg.ResolveModelWithFallback(ref)
		if !ok {
			t.Fatalf("%s: ResolveModelWithFallback not ok", ref)
		}
		entry, ok := cfg.ResolveModel(resolved)
		if !ok {
			t.Fatalf("%s -> %s: ResolveModel not ok", ref, resolved)
		}
		cap := EffortCapabilityForEntry(entry)
		want := []string{"auto", "disabled", "low", "high", "max"}
		if !cap.Supported || len(cap.Levels) != len(want) {
			t.Fatalf("%s capability = %+v, want levels %v", ref, cap, want)
		}
		for i := range want {
			if cap.Levels[i] != want[i] {
				t.Fatalf("%s levels = %v, want %v (order matters — the menu renders it verbatim)", ref, cap.Levels, want)
			}
		}
		if cap.Default != "high" {
			t.Fatalf("%s default = %q, want high (config default_effort)", ref, cap.Default)
		}
		// The wire options carry the same vocabulary with the composer "auto"
		// alias stripped, in provider order.
		rc := ReasoningCapabilityForEntry(entry)
		gotOptions := make([]string, 0, len(rc.Options))
		for _, opt := range rc.Options {
			gotOptions = append(gotOptions, opt.ID)
		}
		wantOptions := strings.Join([]string{"disabled", "low", "high", "max"}, ",")
		if strings.Join(gotOptions, ",") != wantOptions {
			t.Fatalf("%s wire options = %v, want %s", ref, gotOptions, wantOptions)
		}
	}
}

// TestModelLevelEffortVocabularyOverride pins task 1053's precedence chain for
// a single provider serving two models with different effort vocabularies:
// a model's own model_overrides supported_efforts wins over the connection
// list, and a model with no declaration of its own falls back to the
// connection vocabulary untouched (zero-migration compatibility).
func TestModelLevelEffortVocabularyOverride(t *testing.T) {
	cfg := &Config{Providers: []ProviderEntry{{
		Name: "gateway", Kind: "openai", BaseURL: "https://api.example-gateway.com/v1",
		Models: []string{"chat-model", "reasoning-model", "plain-model"},
		SupportedEfforts: []string{"low", "high", "max"}, DefaultEffort: "high",
		ModelOverrides: map[string]ProviderModelOverride{
			"reasoning-model": {SupportedEfforts: []string{"low", "xhigh", "max"}, DefaultEffort: "max"},
		},
	}}}
	normalizeEffortConfig(cfg)

	for _, tc := range []struct {
		model       string
		wantLevels  []string
		wantDefault string
	}{
		{"chat-model", []string{"auto", "low", "high", "max"}, "high"},
		{"reasoning-model", []string{"auto", "low", "xhigh", "max"}, "max"},
		{"plain-model", []string{"auto", "low", "high", "max"}, "high"},
	} {
		entry, ok := cfg.ResolveModel("gateway/" + tc.model)
		if !ok {
			t.Fatalf("gateway/%s did not resolve", tc.model)
		}
		cap := EffortCapabilityForEntry(entry)
		if len(cap.Levels) != len(tc.wantLevels) {
			t.Fatalf("%s levels = %v, want %v", tc.model, cap.Levels, tc.wantLevels)
		}
		for i := range tc.wantLevels {
			if cap.Levels[i] != tc.wantLevels[i] {
				t.Fatalf("%s levels = %v, want %v", tc.model, cap.Levels, tc.wantLevels)
			}
		}
		if cap.Default != tc.wantDefault {
			t.Fatalf("%s default = %q, want %q", tc.model, cap.Default, tc.wantDefault)
		}
	}
}

// TestSetProviderModelEffort pins the write side: the setter edits only the
// named model's override, validates the level against the model's effective
// vocabulary, and refuses a level outside it before anything reaches disk.
func TestSetProviderModelEffort(t *testing.T) {
	cfg := &Config{Providers: []ProviderEntry{{
		Name: "gateway", Kind: "openai", BaseURL: "https://api.example-gateway.com/v1",
		Models: []string{"chat-model", "reasoning-model"},
		SupportedEfforts: []string{"low", "high", "max"},
		ModelOverrides: map[string]ProviderModelOverride{
			"reasoning-model": {SupportedEfforts: []string{"low", "xhigh", "max"}},
		},
	}}}
	normalizeEffortConfig(cfg)

	// A level from the connection vocabulary lands on a model with no
	// override of its own.
	if err := cfg.SetProviderModelEffort("gateway", "chat-model", "max"); err != nil {
		t.Fatalf("set chat-model effort: %v", err)
	}
	if got := cfg.Providers[0].ModelOverrides["chat-model"].DefaultEffort; got != "max" {
		t.Fatalf("chat-model override default_effort = %q, want max", got)
	}
	// A level from the model's own vocabulary lands on the overridden model.
	if err := cfg.SetProviderModelEffort("gateway", "reasoning-model", "xhigh"); err != nil {
		t.Fatalf("set reasoning-model effort: %v", err)
	}
	if got := cfg.Providers[0].ModelOverrides["reasoning-model"].DefaultEffort; got != "xhigh" {
		t.Fatalf("reasoning-model override default_effort = %q, want xhigh", got)
	}
	// A level outside the model's own vocabulary is refused.
	if err := cfg.SetProviderModelEffort("gateway", "", "high"); err == nil {
		t.Fatal("empty model should be refused")
	}
	if err := cfg.SetProviderModelEffort("gateway", "reasoning-model", "high"); err == nil {
		t.Fatal("level outside the model's own vocabulary should be refused")
	}
	// Unknown models and providers error instead of silently no-op.
	if err := cfg.SetProviderModelEffort("gateway", "ghost-model", "max"); err == nil {
		t.Fatal("unknown model should be refused")
	}
	if err := cfg.SetProviderModelEffort("ghost", "chat-model", "max"); err == nil {
		t.Fatal("unknown provider should be refused")
	}
	// Clearing with "auto" removes the override entry it emptied.
	if err := cfg.SetProviderModelEffort("gateway", "chat-model", "auto"); err != nil {
		t.Fatalf("clear chat-model effort: %v", err)
	}
	if _, ok := cfg.Providers[0].ModelOverrides["chat-model"]; ok {
		t.Fatal("cleared chat-model override should be removed, not kept empty")
	}
	// The override's default survives a round-trip through ResolveModel.
	if entry, ok := cfg.ResolveModel("gateway/reasoning-model"); !ok || entry.DefaultEffort != "xhigh" {
		t.Fatalf("resolved reasoning-model default_effort = %q (ok=%t), want xhigh", entry.DefaultEffort, ok)
	}
}
