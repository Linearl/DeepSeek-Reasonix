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
