package config

import "testing"

func mimoEntry(supported []string) *ProviderEntry {
	e := &ProviderEntry{
		Name:      "mimo",
		Kind:      "openai",
		BaseURL:   "https://api.xiaomimimo.com/v1",
		Model:     "mimo-v2.6-flash",
		APIKeyEnv: "MIMO_API_KEY",
	}
	e.SupportedEfforts = supported
	return e
}

// Task 1151: an undeclared MiMo entry offers the full documented 8-level
// vocabulary plus auto, marked AliasFold — display surfaces fold it onto the
// four honest tiers, and every alias level still reaches the wire through the
// normalizeMimoEffort fold (max→high is a legal stored alias, not a drop).
func TestEffortMenuDomainMimoUndeclared(t *testing.T) {
	levels, aliasFold := EffortMenuDomain(mimoEntry(nil))
	want := []string{"auto", "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"}
	if aliasFold != true {
		t.Fatalf("aliasFold = %v, want true (MiMo identity mark)", aliasFold)
	}
	if len(levels) != len(want) {
		t.Fatalf("levels = %v, want %v", levels, want)
	}
	for i, level := range want {
		if levels[i] != level {
			t.Fatalf("levels[%d] = %q, want %q", i, levels[i], level)
		}
	}
	for _, level := range []string{"max", "ultra", "xhigh", "minimal", "none"} {
		if !EffortLevelReachesWire(mimoEntry(nil), level) {
			t.Fatalf("level %q should reach the wire via the MiMo fold", level)
		}
	}
}

// Task 1151: the reported defect — a declared vocabulary is authoritative for
// NormalizeEffort, so a declared list without max makes max a value the
// runtime drops with a boot warning. The menu must not offer it, and the
// persisted level must be reported as not reaching the wire.
func TestEffortMenuDomainMimoDeclaredWithoutMax(t *testing.T) {
	entry := mimoEntry([]string{"low", "medium", "high"})
	levels, aliasFold := EffortMenuDomain(entry)
	want := []string{"auto", "low", "medium", "high"}
	if aliasFold != false {
		t.Fatalf("aliasFold = %v, want false (config-declared vocabulary)", aliasFold)
	}
	if len(levels) != len(want) {
		t.Fatalf("levels = %v, want %v", levels, want)
	}
	for i, level := range want {
		if levels[i] != level {
			t.Fatalf("levels[%d] = %q, want %q", i, levels[i], level)
		}
	}
	if EffortLevelReachesWire(entry, "max") {
		t.Fatal("declared [low,medium,high] must make max a dropped level")
	}
	if !EffortLevelReachesWire(entry, "high") {
		t.Fatal("declared level high must reach the wire")
	}
}

// The capability registry's honest max sets (deepseek v4 family, anthropic
// family) keep max in the menu — only MiMo compat vocabularies fold.
func TestEffortMenuDomainHonestMaxSets(t *testing.T) {
	deepseek := &ProviderEntry{Kind: "openai", BaseURL: "https://api.deepseek.com", Model: "deepseek-v4-flash"}
	levels, aliasFold := EffortMenuDomain(deepseek)
	want := []string{"auto", "disabled", "low", "high", "max"}
	if aliasFold {
		t.Fatal("deepseek must not be alias-folded")
	}
	if len(levels) != len(want) {
		t.Fatalf("levels = %v, want %v", levels, want)
	}
	for i, level := range want {
		if levels[i] != level {
			t.Fatalf("levels[%d] = %q, want %q", i, levels[i], level)
		}
	}

	anthropic := &ProviderEntry{Kind: "anthropic", BaseURL: "https://api.anthropic.com", Model: "claude-sonnet-4.6"}
	levels, aliasFold = EffortMenuDomain(anthropic)
	wantAnthropic := []string{"auto", "low", "medium", "high", "xhigh", "max"}
	if aliasFold {
		t.Fatal("anthropic must not be alias-folded")
	}
	if len(levels) != len(wantAnthropic) {
		t.Fatalf("anthropic levels = %v, want %v", levels, wantAnthropic)
	}
	for i, level := range wantAnthropic {
		if levels[i] != level {
			t.Fatalf("anthropic levels[%d] = %q, want %q", i, levels[i], level)
		}
	}
}

func TestEffortMenuDomainNoCapability(t *testing.T) {
	entry := &ProviderEntry{Kind: "openai", BaseURL: "https://example.com/v1", Model: "plain", ReasoningProtocol: ReasoningProtocolNone}
	levels, aliasFold := EffortMenuDomain(entry)
	if levels != nil || aliasFold {
		t.Fatalf("levels = %v, aliasFold = %v; want nil/false for a no-knob model", levels, aliasFold)
	}
}

func TestEffortLevelReachesWireBasics(t *testing.T) {
	if !EffortLevelReachesWire(nil, "") || !EffortLevelReachesWire(nil, "auto") {
		t.Fatal("empty/auto applies even without an entry")
	}
	if EffortLevelReachesWire(nil, "high") {
		t.Fatal("a concrete level without an entry cannot reach the wire")
	}
	if !EffortLevelReachesWire(mimoEntry(nil), "") {
		t.Fatal("empty stored level is auto and always applies")
	}
}

// Task 1161a: a thinking-locked entry (explicit `thinking = "disabled"` on the
// OpenAI-compatible family) drops every depth override at the wire — the
// adapter validates the override against its locked {disabled} capability at
// Stream time. The composite capability read re-widens through the
// richer-table rule, so without the lock face here the menu offered depths
// the runtime would reject per request. The menu must carry only the lock's
// own value (plus the composer's auto alias), in lockstep with the two
// settings surfaces that already read EffortMenuDomain (task 1151).
func TestEffortMenuDomainThinkingLockedEntry(t *testing.T) {
	entry := &ProviderEntry{Kind: "openai", BaseURL: "https://api.deepseek.com", Model: "deepseek-flash", Thinking: "disabled"}
	levels, _ := EffortMenuDomain(entry)
	want := []string{"auto", "disabled"}
	if len(levels) != len(want) {
		t.Fatalf("locked levels = %v, want %v", levels, want)
	}
	for i, level := range want {
		if levels[i] != level {
			t.Fatalf("locked levels[%d] = %q, want %q", i, levels[i], level)
		}
	}
	if !EffortLevelReachesWire(entry, "disabled") {
		t.Fatal("the lock's own value must reach the wire")
	}
	for _, level := range []string{"low", "high", "max"} {
		if EffortLevelReachesWire(entry, level) {
			t.Fatalf("locked entry must drop %q at the menu, not at request time", level)
		}
	}
	// The anthropic kind is exempt: its thinking field is a protocol switch
	// with its own lift, not the OpenAI-family lock.
	anthropic := &ProviderEntry{Kind: "anthropic", BaseURL: "https://api.anthropic.com", Model: "claude-sonnet-4.6", Thinking: "disabled"}
	if !EffortLevelReachesWire(anthropic, "high") {
		t.Fatal("anthropic thinking=disabled is not the OpenAI-family lock; high must keep reaching the wire")
	}
}
