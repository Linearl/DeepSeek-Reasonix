package config

import "testing"

// Task effortfix2 (A-line P2): AliasFold is the provider/protocol identity
// mark for display folding — set ONLY by the MiMo capability. Per-family
// isolation: a deepseek entry (config-declared or built-in) and an
// anthropic-kind entry never carry it, so the composer can never fold their
// honest vocabularies no matter what words they contain.
func TestAliasFoldIdentityIsolation(t *testing.T) {
	flash := &ProviderEntry{Kind: "openai", BaseURL: "https://api.deepseek.com", Model: "deepseek-v4-flash"}
	if ec := EffortCapabilityForEntry(flash); ec.AliasFold {
		t.Fatalf("deepseek entry must not carry AliasFold: %+v", ec)
	} else if !containsString(ec.Levels, "max") {
		t.Fatalf("deepseek levels = %v, want max present", ec.Levels)
	}

	anthropicGateway := &ProviderEntry{Kind: "anthropic", Model: "deepseek-v4-flash"}
	if ec := EffortCapabilityForEntry(anthropicGateway); ec.AliasFold {
		t.Fatalf("anthropic-kind entry must not carry AliasFold: %+v", ec)
	}

	mimo := &ProviderEntry{Kind: "openai", BaseURL: "https://token-plan-cn.xiaomimimo.com/v1", Model: "mimo-v2.5-pro"}
	ec := EffortCapabilityForEntry(mimo)
	if !ec.AliasFold {
		t.Fatalf("MiMo entry must carry AliasFold: %+v", ec)
	}
	if !containsString(ec.Levels, "minimal") || !containsString(ec.Levels, "ultra") {
		t.Fatalf("MiMo levels = %v, want the eight-level vocabulary", ec.Levels)
	}

	// A config-declared vocabulary on a MiMo endpoint is user-authored:
	// supported_efforts outranks the built-in table and never inherits the
	// mark — displayed verbatim.
	declared := &ProviderEntry{
		Kind: "openai", BaseURL: "https://token-plan-cn.xiaomimimo.com/v1", Model: "mimo-v2.5-pro",
		SupportedEfforts: []string{"low", "high"},
	}
	if ec := EffortCapabilityForEntry(declared); ec.AliasFold {
		t.Fatalf("config-declared vocabulary must not carry AliasFold: %+v", ec)
	}
}

// Task effortfix2 P1-① end-to-end at the config layer: the 283-block shape
// (no provider-level supported_efforts, bare deepseek-flash model, official
// endpoint) must reach the full-depth family through the fixed provider
// list — the menu the user sees carries "low" again, matching what config
// 173's supported_efforts declared for the flash family.
func TestBareDeepSeekFlashKeepsLowWithoutDeclaredSupport(t *testing.T) {
	flash := &ProviderEntry{
		Kind:    "openai",
		BaseURL: "https://api.deepseek.com",
		Model:   "deepseek-flash", // the bare alias the 283 block lists
	}
	if len(flash.SupportedEfforts) != 0 {
		t.Fatalf("test entry must have no declared support (283 shape): %v", flash.SupportedEfforts)
	}
	ec := EffortCapabilityForEntry(flash)
	if !ec.Supported {
		t.Fatal("deepseek endpoint must offer an effort vocabulary")
	}
	want := []string{"auto", "disabled", "low", "high", "max"}
	if len(ec.Levels) != len(want) {
		t.Fatalf("levels = %v, want %v (low must survive the alias fix)", ec.Levels, want)
	}
	for i := range want {
		if ec.Levels[i] != want[i] {
			t.Fatalf("levels[%d] = %q, want %q", i, ec.Levels[i], want[i])
		}
	}
}
