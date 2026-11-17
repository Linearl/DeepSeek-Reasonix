package config

import (
	"strings"
	"testing"
)

// tl1666: the agent-layer Options fields that used to carry the retired
// soft/snip/force thresholds and the native context-editing flag are gone.
// The CONFIG layer keeps those keys as load-only compatibility entries, and
// this file is the regression anchor for the detector that must keep firing:
// an old config file carrying a retired key must still produce a
// "deprecated key" warning naming that key, and the retired value must never
// survive the load (nothing downstream can consume it any more).
//
// Without this anchor the config-side detector could be dropped together with
// the agent-side fields and old configs would silently load with no signal.

func TestTl1666RetiredCompactionKeysStillWarn(t *testing.T) {
	resetUnknownKeyWarnDedupeForTest()
	body := "[agent]\n" +
		"soft_compact_ratio = 0.003\n" +
		"tool_result_snip_ratio = 0.4\n" +
		"compact_force_ratio = 0.9\n" +
		"context_editing = \"native\"\n"
	path := task321WriteConfig(t, body)

	logs := task321CaptureWarnings(t)
	cfg := task321LoadReadOnly(t, path)

	if cfg == nil {
		t.Fatal("load must continue past retired keys")
	}
	out := logs.String()
	for _, key := range []string{
		"agent.soft_compact_ratio",
		"agent.tool_result_snip_ratio",
		"agent.compact_force_ratio",
		"agent.context_editing",
	} {
		if !strings.Contains(out, "deprecated key") {
			t.Fatalf("expected a deprecated-key warn line for %s, got:\n%s", key, out)
		}
		if !strings.Contains(out, key) {
			t.Fatalf("expected the deprecated-key warn to name %s, got:\n%s", key, out)
		}
	}
	// Retired keys are reported as deprecated, never double-reported as unknown.
	if strings.Contains(out, "unknown key") {
		t.Fatalf("retired keys must not double-report as unknown, got:\n%s", out)
	}
}

// The retired values must be cleared on load: the agent layer has no field to
// receive them any more, so a surviving value would be a silent leak.
func TestTl1666RetiredCompactionValuesAreClearedOnLoad(t *testing.T) {
	resetUnknownKeyWarnDedupeForTest()
	body := "[agent]\n" +
		"soft_compact_ratio = 0.003\n" +
		"tool_result_snip_ratio = 0.4\n" +
		"compact_force_ratio = 0.9\n" +
		"context_editing = \"native\"\n"
	path := task321WriteConfig(t, body)

	logs := task321CaptureWarnings(t)
	cfg := task321LoadReadOnly(t, path)

	if cfg == nil {
		t.Fatal("load must continue past retired keys")
	}
	if cfg.Agent.SoftCompactRatio != 0 {
		t.Fatalf("agent.soft_compact_ratio = %v, want 0 (retired key must be cleared on load)", cfg.Agent.SoftCompactRatio)
	}
	if cfg.Agent.ToolResultSnipRatio != 0 {
		t.Fatalf("agent.tool_result_snip_ratio = %v, want 0 (retired key must be cleared on load)", cfg.Agent.ToolResultSnipRatio)
	}
	if cfg.Agent.CompactForceRatio != 0 {
		t.Fatalf("agent.compact_force_ratio = %v, want 0 (retired key must be cleared on load)", cfg.Agent.CompactForceRatio)
	}
	if strings.TrimSpace(cfg.Agent.ContextEditing) != "" {
		t.Fatalf("agent.context_editing = %q, want empty (retired key must be cleared on load)", cfg.Agent.ContextEditing)
	}
	// The single live knob is untouched and still the only compaction control.
	if cfg.Agent.CompactRatio <= 0 {
		t.Fatalf("agent.compact_ratio = %v, want the live default (>0)", cfg.Agent.CompactRatio)
	}
	_ = logs
}
