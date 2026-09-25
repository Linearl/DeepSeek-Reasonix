package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// Task 242: the fallback target setter rejects bare model ids (identity needs
// the provider half — the create_collab_session approver convention) and
// accepts the "provider/model" form; empty clears.
func TestSetFallbackModelValidatesProviderModelForm(t *testing.T) {
	c := &Config{}

	if err := c.SetFallbackModel("mimo-v2.6-flash"); err == nil {
		t.Fatal("bare model id must be rejected")
	}
	if err := c.SetFallbackModel("mimo-api/"); err == nil {
		t.Fatal("missing model half must be rejected")
	}
	if err := c.SetFallbackModel("/mimo-v2.6-flash"); err == nil {
		t.Fatal("missing provider half must be rejected")
	}
	if err := c.SetFallbackModel("a/b/c"); err == nil {
		t.Fatal("extra path segments must be rejected")
	}
	if err := c.SetFallbackModel("  mimo-api / mimo-v2.6-flash "); err != nil {
		t.Fatalf("valid pair with padding: %v", err)
	}
	if c.Agent.FallbackModel != "mimo-api/mimo-v2.6-flash" {
		t.Fatalf("stored = %q, want normalized provider/model", c.Agent.FallbackModel)
	}
	if err := c.SetFallbackModel(""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if c.Agent.FallbackModel != "" {
		t.Fatalf("clear left %q", c.Agent.FallbackModel)
	}
}

// Task 242 rendering-table roundtrip (the 81/123 lesson): both fields must be
// emitted by Render so a save-then-reload keeps them, and the default-off
// switch must render as false with an empty target.
func TestFallbackModelRenders(t *testing.T) {
	c := &Config{}
	out := RenderTOML(c)
	if !strings.Contains(out, "experimental_fallback_model = false") {
		t.Fatalf("switch missing from render output:\n%s", out)
	}
	if !strings.Contains(out, `fallback_model = ""`) {
		t.Fatalf("target missing from render output:\n%s", out)
	}

	c.Agent.ExperimentalFallbackModel = true
	c.Agent.FallbackModel = "glm-coding-plan-cn-abc/glm-5.3-flash"
	out = RenderTOML(c)
	if !strings.Contains(out, "experimental_fallback_model = true") ||
		!strings.Contains(out, `fallback_model = "glm-coding-plan-cn-abc/glm-5.3-flash"`) {
		t.Fatalf("set values not rendered:\n%s", out)
	}
}

// FallbackModelLive is the gate every runtime path reads: off, unconfigured,
// and on+configured — resolved from an isolated home (SaveTo, never Save():
// Save falls back to the relative reasonix.toml and would leak into the
// package dir, task 225's finding).
func TestFallbackModelLiveGatesOnSwitchAndTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	if got := FallbackModelLive(); got != "" {
		t.Fatalf("default-off live = %q, want empty", got)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := cfg.SetExperimentalFallbackModel(true); err != nil {
		t.Fatalf("switch: %v", err)
	}
	// SaveTo the SAME home FallbackModelLive reads — saving elsewhere would
	// make every assertion below vacuously true.
	if err := cfg.SaveTo(filepath.Join(home, "config.toml")); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Switch on but no target: still "" — callers treat that as keep-primary.
	if got := FallbackModelLive(); got != "" {
		t.Fatalf("on+unconfigured live = %q, want empty", got)
	}

	if err := cfg.SetFallbackModel("mimo-api/mimo-v2.6-flash"); err != nil {
		t.Fatalf("target: %v", err)
	}
	if err := cfg.SaveTo(filepath.Join(home, "config.toml")); err != nil {
		t.Fatalf("save target: %v", err)
	}
	if got := FallbackModelLive(); got != "mimo-api/mimo-v2.6-flash" {
		t.Fatalf("on+configured live = %q, want the target", got)
	}

	// Switch off, target present: live must still be empty (zero regression
	// stays one flag away even with a stored target).
	if err := cfg.SetExperimentalFallbackModel(false); err != nil {
		t.Fatalf("switch off: %v", err)
	}
	if err := cfg.SaveTo(filepath.Join(home, "config.toml")); err != nil {
		t.Fatalf("save off: %v", err)
	}
	if got := FallbackModelLive(); got != "" {
		t.Fatalf("off live = %q, want empty", got)
	}
}

var _ = strings.TrimSpace
