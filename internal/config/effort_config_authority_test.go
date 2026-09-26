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
