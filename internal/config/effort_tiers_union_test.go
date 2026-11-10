package config

import "testing"

// tl1538 acceptance ②: the generic OpenAI protocol capability and the
// anthropic capability must stay in lockstep with the vendors' documented
// effort vocabularies (the seven-tier union), pinned by order so a silent
// reordering or dropping cannot slip through.
//
// Official vocabularies:
//   - OpenAI (union across model generations): none|minimal|low|medium|high|
//     xhigh|max. Generation boundaries: GPT-5.1+ none (default, replaces
//     minimal), GPT-5 era minimal, xhigh = pre-5.6 ceiling, max = GPT-5.6+.
//   - Anthropic: low|medium|high|xhigh|max (no "none" in the effort enum —
//     thinking-off is the separate thinking.type knob; per-model narrowing
//     like "xhigh not on every model" is the supported_efforts declaration's
//     job, so the connection menu stays the vendor union).
func TestOpenAIEffortCapabilityUnion(t *testing.T) {
	e := &ProviderEntry{Kind: "openai", BaseURL: "https://gateway.example.com/v1", ReasoningProtocol: ReasoningProtocolOpenAI}
	cap := EffortCapabilityForEntry(e)
	if !cap.Supported {
		t.Fatal("generic OpenAI protocol must support effort")
	}
	want := []string{"auto", "none", "minimal", "low", "medium", "high", "xhigh", "max"}
	if len(cap.Levels) != len(want) {
		t.Fatalf("openai levels = %v, want %v", cap.Levels, want)
	}
	for i, level := range want {
		if cap.Levels[i] != level {
			t.Fatalf("openai levels[%d] = %q, want %q", i, cap.Levels[i], level)
		}
	}
	if cap.Default != "auto" {
		t.Fatalf("openai default = %q, want auto (auto semantics unchanged)", cap.Default)
	}
	if cap.AliasFold {
		t.Fatal("generic OpenAI vocabulary must not be alias-folded")
	}
}

func TestAnthropicEffortCapabilityOfficialVocabulary(t *testing.T) {
	e := &ProviderEntry{Kind: "anthropic", BaseURL: "https://api.anthropic.com", Model: "claude-sonnet-4.6"}
	cap := EffortCapabilityForEntry(e)
	if !cap.Supported {
		t.Fatal("anthropic must support effort")
	}
	want := []string{"auto", "low", "medium", "high", "xhigh", "max"}
	if len(cap.Levels) != len(want) {
		t.Fatalf("anthropic levels = %v, want %v", cap.Levels, want)
	}
	for i, level := range want {
		if cap.Levels[i] != level {
			t.Fatalf("anthropic levels[%d] = %q, want %q", i, cap.Levels[i], level)
		}
	}
	if cap.Default != "auto" {
		t.Fatalf("anthropic default = %q, want auto", cap.Default)
	}
}

// tl1538: the generic OpenAI normalize chain carries the new tiers end to end
// verbatim — the stored value equals the wire value for every tier the menu
// offers, and anything outside the union (MiMo-exclusive "ultra", retired
// DeepSeek spellings, binary-knob words) is still rejected.
func TestNormalizeOpenAIEffortUnion(t *testing.T) {
	e := &ProviderEntry{Kind: "openai", BaseURL: "https://gateway.example.com/v1", ReasoningProtocol: ReasoningProtocolOpenAI}
	for _, level := range []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"} {
		got, err := NormalizeEffort(e, level)
		if err != nil || got != level {
			t.Fatalf("NormalizeEffort(%q) = %q/%v, want verbatim/nil", level, got, err)
		}
	}
	for _, level := range []string{"ultra", "turbo", "off", "disabled", "enabled", "adaptive"} {
		if got, err := NormalizeEffort(e, level); err == nil {
			t.Fatalf("NormalizeEffort(%q) = %q/nil, want rejection outside the union", level, got)
		}
	}
}

// The anthropic normalize chain keeps rejecting the spellings outside its
// official enum (no "none" — thinking-off rides the thinking knob).
func TestNormalizeAnthropicEffortOfficialVocabulary(t *testing.T) {
	e := &ProviderEntry{Kind: "anthropic", BaseURL: "https://api.anthropic.com", Model: "claude-sonnet-4.6"}
	for _, level := range []string{"low", "medium", "high", "xhigh", "max"} {
		got, err := NormalizeEffort(e, level)
		if err != nil || got != level {
			t.Fatalf("NormalizeEffort(%q) = %q/%v, want verbatim/nil", level, got, err)
		}
	}
	for _, level := range []string{"none", "minimal", "ultra", "disabled", "enabled"} {
		if got, err := NormalizeEffort(e, level); err == nil {
			t.Fatalf("NormalizeEffort(%q) = %q/nil, want rejection outside the official enum", level, got)
		}
	}
}
