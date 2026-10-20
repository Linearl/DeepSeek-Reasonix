package config

import (
	"strings"
	"testing"
)

// Task 1031: the /effort menu and validation for the MiniMax M3.1 family must
// mirror the provider-layer capability (reasoning_effort low..max, no
// thinking-off), while M3 keeps the binary adaptive|disabled vocabulary.

func TestEffortCapabilityMiniMaxM31(t *testing.T) {
	for _, model := range []string{"MiniMax-M3.1", "MiniMax-M3.1-Flash"} {
		e := &ProviderEntry{Kind: "openai", BaseURL: "https://api.minimaxi.com/v1", Model: model}
		cap := EffortCapabilityForEntry(e)
		if !cap.Supported {
			t.Fatalf("%s entry should expose /effort, got %+v", model, cap)
		}
		want := []string{"auto", "low", "medium", "high", "xhigh", "max"}
		if len(cap.Levels) != len(want) {
			t.Fatalf("%s: levels = %v, want %v", model, cap.Levels, want)
		}
		for i, l := range want {
			if cap.Levels[i] != l {
				t.Errorf("%s: levels[%d] = %q, want %q", model, i, cap.Levels[i], l)
			}
		}
		for i, l := range cap.Levels {
			if l == "disabled" || l == "adaptive" {
				t.Errorf("%s: levels[%d] = %q — M3.1 vocabulary leaked into the M3.1 menu", model, i, l)
			}
		}
		if cap.Default != "auto" {
			t.Errorf("%s: default = %q, want auto", model, cap.Default)
		}
	}
	// M3 zero regression: the binary menu stays as before.
	m3 := EffortCapabilityForEntry(&ProviderEntry{Kind: "openai", BaseURL: "https://api.minimaxi.com/v1", Model: "MiniMax-M3"})
	if got, want := strings.Join(m3.Levels, ","), "auto,adaptive,disabled"; got != want {
		t.Errorf("MiniMax-M3: levels = [%s], want [%s] (M3 unchanged)", got, want)
	}
	if m3.Default != "adaptive" {
		t.Errorf("MiniMax-M3: default = %q, want adaptive", m3.Default)
	}
}

func TestNormalizeEffortMiniMaxM31(t *testing.T) {
	e := &ProviderEntry{Kind: "openai", BaseURL: "https://api.minimaxi.com/v1", Model: "MiniMax-M3.1-Flash"}
	cases := []struct {
		in, want string
	}{
		{"auto", ""},
		{"low", "low"},
		{"medium", "medium"},
		{"high", "high"},
		{"xhigh", "xhigh"},
		{"max", "max"},
		{"XHIGH", "xhigh"}, // case-insensitive
		// M3-era stored value the old three-state menu offered M3.1 users:
		// "adaptive" means provider-default == auto on M3.1, so it folds
		// instead of failing every already-persisted entry.
		{"adaptive", ""},
	}
	for _, tc := range cases {
		got, err := NormalizeEffort(e, tc.in)
		if err != nil {
			t.Errorf("NormalizeEffort(%q) returned error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("NormalizeEffort(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// Thinking-off requests must fail fast: M3.1 400s thinking.type=disabled.
	for _, in := range []string{"disabled", "off", "none", "turbo"} {
		_, err := NormalizeEffort(e, in)
		if err == nil {
			t.Errorf("NormalizeEffort(%q) should be rejected", in)
			continue
		}
		if !strings.Contains(err.Error(), "low|medium|high|xhigh|max") {
			t.Errorf("NormalizeEffort(%q) error = %q, want the M3.1 usage hint", in, err.Error())
		}
	}
}

func TestMigrateStoredMiniMaxEffort(t *testing.T) {
	m31 := &ProviderEntry{Kind: "openai", BaseURL: "https://api.minimaxi.com/v1", Model: "MiniMax-M3.1-Flash"}
	if got := migrateStoredMiniMaxEffort(m31, "adaptive"); got != "" {
		t.Errorf("M3.1 stored adaptive = %q, want \"\" (auto) so boot Validate passes", got)
	}
	// "disabled" is NOT folded: it asked for thinking-off, which must surface
	// as an actionable boot error rather than silently becoming max thinking.
	if got := migrateStoredMiniMaxEffort(m31, "disabled"); got != "disabled" {
		t.Errorf("M3.1 stored disabled = %q, want it preserved for the boot error", got)
	}
	if got := migrateStoredMiniMaxEffort(m31, "high"); got != "high" {
		t.Errorf("M3.1 stored high = %q, want high unchanged", got)
	}
	// M3 zero regression: no folding on the M3 generation.
	m3 := &ProviderEntry{Kind: "openai", BaseURL: "https://api.minimaxi.com/v1", Model: "MiniMax-M3"}
	if got := migrateStoredMiniMaxEffort(m3, "adaptive"); got != "adaptive" {
		t.Errorf("M3 stored adaptive = %q, want adaptive unchanged", got)
	}
}

func TestEffectiveEffortMiniMaxM31MigratesAdaptive(t *testing.T) {
	e := &ProviderEntry{Kind: "openai", BaseURL: "https://api.minimaxi.com/v1", Model: "MiniMax-M3.1-Flash", Effort: "adaptive"}
	if got := EffectiveEffort(e); got != "" {
		t.Errorf("EffectiveEffort(stored adaptive on M3.1) = %q, want \"\" (auto)", got)
	}
	e2 := &ProviderEntry{Kind: "openai", BaseURL: "https://api.minimaxi.com/v1", Model: "MiniMax-M3.1-Flash", Effort: "xhigh"}
	if got := EffectiveEffort(e2); got != "xhigh" {
		t.Errorf("EffectiveEffort(stored xhigh on M3.1) = %q, want xhigh", got)
	}
}
