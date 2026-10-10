package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// 任务 707: the compact target setter mirrors the task-242 convention — bare
// model ids are rejected (identity needs the provider half), the
// "provider/model" form is normalized, empty clears.
func TestSetCompactModelValidatesProviderModelForm(t *testing.T) {
	c := &Config{}

	if err := c.SetCompactModel("minimax-m3"); err == nil {
		t.Fatal("bare model id must be rejected")
	}
	if err := c.SetCompactModel("minimax/"); err == nil {
		t.Fatal("missing model half must be rejected")
	}
	if err := c.SetCompactModel("/minimax-m3"); err == nil {
		t.Fatal("missing provider half must be rejected")
	}
	if err := c.SetCompactModel("a/b/c"); err == nil {
		t.Fatal("extra path segments must be rejected")
	}
	if err := c.SetCompactModel("  minimax / minimax-m3 "); err != nil {
		t.Fatalf("valid pair with padding: %v", err)
	}
	if c.Agent.CompactModel != "minimax/minimax-m3" {
		t.Fatalf("stored = %q, want normalized provider/model", c.Agent.CompactModel)
	}
	if err := c.SetCompactModel(""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if c.Agent.CompactModel != "" {
		t.Fatalf("clear left %q", c.Agent.CompactModel)
	}
}

// 任务 707 rendering-table roundtrip (the 81/123 lesson): both fields must be
// emitted by Render so a save-then-reload keeps them, and the default-off
// switch must render as false with an empty target.
func TestCompactModelRenders(t *testing.T) {
	c := &Config{}
	out := RenderTOML(c)
	if !strings.Contains(out, "experimental_compact_model = false") {
		t.Fatalf("switch missing from render output:\n%s", out)
	}
	if !strings.Contains(out, `compact_model = ""`) {
		t.Fatalf("target missing from render output:\n%s", out)
	}

	c.Agent.ExperimentalCompactModel = true
	c.Agent.CompactModel = "minimax/minimax-m3"
	out = RenderTOML(c)
	if !strings.Contains(out, "experimental_compact_model = true") ||
		!strings.Contains(out, `compact_model = "minimax/minimax-m3"`) {
		t.Fatalf("set values not rendered:\n%s", out)
	}

	// Round-trip: save → reload keeps both values (persistence test, 验收⑤).
	// Isolated home + SaveTo (never Save(): Save falls back to the relative
	// reasonix.toml and would leak into the package dir, task 225's finding).
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	path := filepath.Join(home, "config.toml")
	if err := c.SaveTo(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	reloaded, err := Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.Agent.ExperimentalCompactModel || reloaded.Agent.CompactModel != "minimax/minimax-m3" {
		t.Fatalf("round-trip lost state: switch=%v target=%q", reloaded.Agent.ExperimentalCompactModel, reloaded.Agent.CompactModel)
	}
}

// CompactModelLive is the gate the boot assembly reads: off, on+unconfigured,
// on+configured, and off-with-stored-target must each resolve exactly.
func TestCompactModelLiveGatesOnSwitchAndTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := cfg.CompactModelLive(); got != "" {
		t.Fatalf("default-off live = %q, want empty", got)
	}
	if err := cfg.SetExperimentalCompactModel(true); err != nil {
		t.Fatalf("switch: %v", err)
	}
	// On but no target: still "" — callers treat that as keep-conversation.
	if got := cfg.CompactModelLive(); got != "" {
		t.Fatalf("on+unconfigured live = %q, want empty", got)
	}
	if err := cfg.SetCompactModel("minimax/minimax-m3"); err != nil {
		t.Fatalf("target: %v", err)
	}
	if got := cfg.CompactModelLive(); got != "minimax/minimax-m3" {
		t.Fatalf("on+configured live = %q, want the target", got)
	}
	// Switch off, target stored: live is empty (zero regression stays one
	// flag away even with a stored target).
	if err := cfg.SetExperimentalCompactModel(false); err != nil {
		t.Fatalf("switch off: %v", err)
	}
	if got := cfg.CompactModelLive(); got != "" {
		t.Fatalf("off live = %q, want empty", got)
	}
}

// 任务 707 验收⑤（模型偏好家族契约）：压缩目的地是 agent 的 boot 快照，所以
// 开关或目标变化必须改变 ModelRuntimeFingerprint——运行中的 tab 据此标记
// pending 并在下一轮运行前重应用。
func TestCompactModelParticipatesInRuntimeFingerprint(t *testing.T) {
	c := Default()
	base := c.ModelRuntimeFingerprint(c.DefaultModel)

	c.Agent.ExperimentalCompactModel = true
	if c.ModelRuntimeFingerprint(c.DefaultModel) == base {
		t.Fatal("switching the compact-model lab switch on did not move the runtime fingerprint")
	}

	c.Agent.CompactModel = "minimax/minimax-m3"
	if c.ModelRuntimeFingerprint(c.DefaultModel) == base {
		t.Fatal("configuring the compact target did not move the runtime fingerprint")
	}

	// Same switch+target (even with a different stored formatting spacing)
	// stays stable: the fingerprint is a change detector, not a hash to game.
	same := Default()
	same.Agent.ExperimentalCompactModel = true
	same.Agent.CompactModel = "minimax/minimax-m3"
	if c.ModelRuntimeFingerprint(c.DefaultModel) != same.ModelRuntimeFingerprint(c.DefaultModel) {
		t.Fatal("identical compact-model state produced different fingerprints")
	}
}
