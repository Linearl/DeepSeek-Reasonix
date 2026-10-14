package config

import (
	"testing"
)

// Task 880: every model on every official OpenCode Go route must expose a
// configurable /effort ladder after a preset install — the user-facing bug was
// "effort is not configurable for opencode-go-..." on models whose reasoning
// protocol was never declared.
func TestOpenCodeGoPresetsExposeEffortOnAllRoutes(t *testing.T) {
	var cfg Config
	for _, preset := range CuratedProviderPresets() {
		for _, entry := range preset.Entries {
			if err := cfg.UpsertProvider(entry); err != nil {
				t.Fatalf("upsert preset %q entry %q: %v", preset.ID, entry.Name, err)
			}
		}
	}
	for _, ref := range []string{
		"opencode-go",
		"opencode-go-anthropic",
		"opencode-go-responses",
		"opencode-go-deepseek-anthropic",
		"opencode-go-deepseek-responses",
	} {
		base, ok := cfg.Provider(ref)
		if !ok {
			t.Fatalf("provider %q missing after preset install", ref)
		}
		for _, model := range base.ModelList() {
			entry, ok := cfg.ResolveModel(ref + "/" + model)
			if !ok {
				t.Fatalf("ResolveModel(%q) failed", ref+"/"+model)
			}
			cap := EffortCapabilityForEntry(entry)
			if !cap.Supported {
				t.Errorf("%s/%s: effort capability not supported (protocol=%q levels=%v)",
					ref, model, ReasoningProtocolForEntry(entry), cap.Levels)
				continue
			}
			if cap.Default == "" {
				t.Errorf("%s/%s: effort default is empty (levels=%v)", ref, model, cap.Levels)
				continue
			}
			if _, err := NormalizeEffort(entry, cap.Default); err != nil {
				t.Errorf("%s/%s: NormalizeEffort(default %q) = %v", ref, model, cap.Default, err)
			}
			// "auto" must always be accepted so users can return to defaults.
			if _, err := NormalizeEffort(entry, "auto"); err != nil {
				t.Errorf("%s/%s: NormalizeEffort(auto) = %v", ref, model, err)
			}
		}
	}
}

// Task 880: the catalog's per-level truth must survive into NormalizeEffort —
// a level the models.dev snapshot maps to null must be rejected before I/O,
// and a level it maps to a wire value must pass through verbatim.
func TestOpenCodeGoEffortLaddersFollowCatalogThinkingLevelMap(t *testing.T) {
	var cfg Config
	preset, ok := CuratedProviderPreset("opencode-go-recommended")
	if !ok {
		t.Fatal("opencode-go-recommended preset missing")
	}
	for _, entry := range preset.Entries {
		if err := cfg.UpsertProvider(entry); err != nil {
			t.Fatalf("upsert %q: %v", entry.Name, err)
		}
	}
	tests := []struct {
		model, level, want string
	}{
		{"qwen3.8-max", "xhigh", "xhigh"},
		{"qwen3.8-max", "high", ""}, // thinkingLevelMap.high = null
		{"qwen3.8-max", "medium", "medium"},
		{"hy4-preview", "none", "none"}, // thinkingLevelMap.off = "none"
		{"hy4-preview", "low", ""},      // thinkingLevelMap.low = null
		{"deepseek-v4.1-flash", "low", "low"},
		{"deepseek-v4.1-flash", "disabled", ""}, // thinkingLevelMap.off = null
		{"mimo-v2.5", "medium", "medium"},
		{"glm-5.1", "high", "high"},
	}
	for _, tc := range tests {
		entry, ok := cfg.ResolveModel("opencode-go/" + tc.model)
		if !ok {
			t.Fatalf("ResolveModel(opencode-go/%s) failed", tc.model)
		}
		got, err := NormalizeEffort(entry, tc.level)
		if got != tc.want {
			t.Errorf("opencode-go/%s NormalizeEffort(%q) = %q, want %q (err=%v)",
				tc.model, tc.level, got, tc.want, err)
		}
		if tc.want == "" && err == nil {
			t.Errorf("opencode-go/%s NormalizeEffort(%q) accepted a catalog-rejected level", tc.model, tc.level)
		}
	}
}

// Task 880: the legacy "opencode-go" compatibility preset installs an entry
// with the SAME name as the recommended bundle. If either preset's table
// drifts, install order silently downgrades models to "effort not
// configurable". Both must declare the shared chat ladders.
func TestOpenCodeGoChatPresetsDeclareSharedEffortLadders(t *testing.T) {
	for _, presetID := range []string{"opencode-go", "opencode-go-recommended"} {
		preset, ok := CuratedProviderPreset(presetID)
		if !ok {
			t.Fatalf("preset %q missing", presetID)
		}
		var cfg Config
		if err := cfg.UpsertProvider(preset.Entries[0]); err != nil {
			t.Fatalf("upsert %q: %v", presetID, err)
		}
		// Models that only exist in the merged pi catalog plus the ones the
		// two tables historically disagreed on.
		for _, model := range []string{
			"glm-5.1",
			"deepseek-v4-flash-vision-exp",
			"deepseek-v4.1-flash",
			"glm-5.3-flash",
			"hy4-preview",
			"longcat-2.0",
			"mimo-v2.5",
			"mimo-v2.6-pro",
			"minimax-m2.7",
			"qwen3.7-plus",
			"qwen3.8-max",
			"space-bunny-free",
		} {
			entry, ok := cfg.ResolveModel("opencode-go/" + model)
			if !ok {
				t.Fatalf("preset %q: ResolveModel(opencode-go/%s) failed", presetID, model)
			}
			if cap := EffortCapabilityForEntry(entry); !cap.Supported {
				t.Errorf("preset %q model %q: effort not configurable (protocol=%q)",
					presetID, model, ReasoningProtocolForEntry(entry))
			}
			if ReasoningProtocolForEntry(entry) == "" {
				t.Errorf("preset %q model %q: reasoning protocol not declared", presetID, model)
			}
		}
	}
}
