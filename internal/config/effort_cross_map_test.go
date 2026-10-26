package config

import (
	"strings"
	"testing"
)

// xe builds a declarative entry whose effort vocabulary is an explicit
// supported_efforts list — NormalizeEffort passes it verbatim and
// EffortMenuDomain exposes it in declaration order, so rank tests pin the
// mapping formula without any vendor protocol table in the way.
func xe(efforts ...string) *ProviderEntry {
	return &ProviderEntry{Name: "prov-x", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "test-model", SupportedEfforts: efforts}
}

// dsEntry is the official DeepSeek v4 shape (disabled|low|high|max, with the
// off-thinking slot spelled "disabled").
func dsEntry() *ProviderEntry {
	return &ProviderEntry{Name: "prov-ds", Kind: "openai", BaseURL: "https://api.deepseek.com", Model: "deepseek-v4-flash"}
}

// TestMapEffortRankSevenToThree is exception-list item 1: a 7-depth source
// onto a 3-depth destination never overflows — the ceiling maps onto the
// ceiling, the lowest non-off depth onto the lowest slot, and every landed
// index stays inside [0,2].
func TestMapEffortRankSevenToThree(t *testing.T) {
	src := xe("level1", "level2", "level3", "level4", "level5", "level6", "level7")
	dst := xe("low", "medium", "high")
	cases := []struct {
		level string
		want  string
	}{
		{"level7", "high"},  // rank 1.0 → slot m-1: ceiling → ceiling
		{"level1", "low"},   // rank 0.0 → slot 0: floor → floor
		{"level2", "low"},   // rank 1/6 → round(1/3)=0
		{"level4", "medium"}, // rank 0.5 → slot 1
		{"level6", "high"},  // rank 5/6 → round(5/3)=2
	}
	for _, tc := range cases {
		got, _, ok := MapEffortAcrossVocabularies(src, dst, tc.level)
		if !ok {
			t.Fatalf("%s: mapping declined, want %s", tc.level, tc.want)
		}
		if got != tc.want {
			t.Fatalf("%s: mapped to %q, want %s", tc.level, got, tc.want)
		}
		if !containsString(dst.SupportedEfforts, got) {
			t.Fatalf("%s: mapped to %q which is outside the destination vocabulary", tc.level, got)
		}
	}
}

// TestMapEffortRankThreeToSeven is exception-list item 2: a 3-depth source
// onto a 7-depth destination never overflows (skipping middle slots is
// expected — the round lands on the nearest relative position).
func TestMapEffortRankThreeToSeven(t *testing.T) {
	src := xe("low", "high", "max")
	dst := xe("d1", "d2", "d3", "d4", "d5", "d6", "d7")
	cases := []struct {
		level string
		want  string
	}{
		{"low", "d1"},  // rank 0
		{"high", "d4"}, // rank 0.5 → round(3)
		{"max", "d7"},  // rank 1 → slot m-1
	}
	for _, tc := range cases {
		got, _, ok := MapEffortAcrossVocabularies(src, dst, tc.level)
		if !ok {
			t.Fatalf("%s: mapping declined, want %s", tc.level, tc.want)
		}
		if got != tc.want {
			t.Fatalf("%s: mapped to %q, want %s", tc.level, got, tc.want)
		}
	}
}

// TestMapEffortRankEqualSizes is exception-list item 3: equal-size
// vocabularies align position by position (i → i).
func TestMapEffortRankEqualSizes(t *testing.T) {
	src := xe("a1", "a2", "a3", "a4")
	dst := xe("b1", "b2", "b3", "b4")
	for i, level := range src.SupportedEfforts {
		got, _, ok := MapEffortAcrossVocabularies(src, dst, level)
		if !ok {
			t.Fatalf("%s: mapping declined", level)
		}
		if want := dst.SupportedEfforts[i]; got != want {
			t.Fatalf("%s (index %d): mapped to %q, want positional slot %q", level, i, got, want)
		}
	}
}

// TestMapEffortSingleDepthDestination is exception-list item 4: a
// single-depth destination takes every non-off source level onto its only
// slot, while an off-thinking source still lands on auto (the destination
// has no off slot — never rise to the depth that turns thinking on).
func TestMapEffortSingleDepthDestination(t *testing.T) {
	// The off-thinking source must be an entry whose vocabulary really has
	// the disabled slot (a "disabled" carry from a vocabulary without one is
	// the config-leftover path and declines instead).
	src := dsEntry()
	dst := xe("only")
	for _, level := range []string{"low", "high", "max"} {
		got, _, ok := MapEffortAcrossVocabularies(src, dst, level)
		if !ok || got != "only" {
			t.Fatalf("%s: mapped to (%q, ok=%v), want the only depth", level, got, ok)
		}
	}
	got, _, ok := MapEffortAcrossVocabularies(src, dst, "disabled")
	if !ok || got != "" {
		t.Fatalf("disabled: mapped to (%q, ok=%v), want deliberate auto (no off slot on the destination)", got, ok)
	}
}

// TestMapEffortMenuOrderAuthoritative is exception-list item 5: a scrambled
// or duplicated destination declaration is used exactly as EffortMenuDomain
// returns it (declaration order, deduplicated) — the mapping never sorts or
// re-guesses semantics.
func TestMapEffortMenuOrderAuthoritative(t *testing.T) {
	src := xe("low", "high", "max")
	// Declared scrambled: menu order is max,low,high. rank 1.0 lands on the
	// LAST menu slot ("high") — a sort-by-strength would have picked "max".
	dst := xe("max", "low", "high")
	got, _, ok := MapEffortAcrossVocabularies(src, dst, "max")
	if !ok || got != "high" {
		t.Fatalf("scrambled dst: max mapped to (%q, ok=%v), want the last menu slot high", got, ok)
	}
	// Duplicated declarations deduplicate: menu is auto,high,low → depths
	// high,low; rank 1.0 → "low".
	dstDup := xe("high", "high", "low")
	got, _, ok = MapEffortAcrossVocabularies(src, dstDup, "max")
	if !ok || got != "low" {
		t.Fatalf("duplicated dst: max mapped to (%q, ok=%v), want the deduplicated last slot low", got, ok)
	}
}

// TestMapEffortUnknownSourceLevel is exception-list item 6: a carried level
// absent from the source menu (config leftover) is not mapped — ok=false
// keeps the status-quo auto.
func TestMapEffortUnknownSourceLevel(t *testing.T) {
	src := xe("low", "high")
	dst := xe("low", "medium", "high")
	got, _, ok := MapEffortAcrossVocabularies(src, dst, "max")
	if ok || got != "" {
		t.Fatalf("unknown source level: got (%q, ok=%v), want declined", got, ok)
	}
}

// TestMapEffortOffThinkingSemantics pins the disabled rules: an off-thinking
// level carries onto the destination's off slot when one exists (including
// the folded MiMo "none" spelling), lands on auto when it does not — and
// NEVER on a depth that would turn thinking on.
func TestMapEffortOffThinkingSemantics(t *testing.T) {
	ds := dsEntry()
	// disabled → destination without any off slot: deliberate auto, never low/high.
	dstNoOff := xe("low", "medium", "high")
	got, _, ok := MapEffortAcrossVocabularies(ds, dstNoOff, "disabled")
	if !ok || got != "" {
		t.Fatalf("disabled → no-off destination: got (%q, ok=%v), want auto", got, ok)
	}
	if got == "low" || got == "medium" || got == "high" {
		t.Fatalf("disabled must never map onto a thinking-on depth, got %q", got)
	}
	// disabled → destination with a disabled slot: stays disabled.
	dstWithOff := xe("disabled", "low", "high")
	got, _, ok = MapEffortAcrossVocabularies(ds, dstWithOff, "disabled")
	if !ok || got != "disabled" {
		t.Fatalf("disabled → off-capable destination: got (%q, ok=%v), want disabled", got, ok)
	}
	// The MiMo folded off slot ("none") carries onto deepseek "disabled".
	mimo := mimoEntry(nil)
	got, _, ok = MapEffortAcrossVocabularies(mimo, ds, "none")
	if !ok || got != "disabled" {
		t.Fatalf("mimo none → deepseek: got (%q, ok=%v), want disabled (folded off slot carries)", got, ok)
	}
	// ...and lands on auto when the destination cannot switch thinking off.
	got, _, ok = MapEffortAcrossVocabularies(mimo, dstNoOff, "none")
	if !ok || got != "" {
		t.Fatalf("mimo none → no-off destination: got (%q, ok=%v), want auto", got, ok)
	}
}

// TestMapEffortOffOnlyDestination: a destination whose only reachable level
// beyond auto is the off slot (a thinking-locked entry) maps every depth
// onto auto WITH a visible note, and carries an off source onto the lock.
func TestMapEffortOffOnlyDestination(t *testing.T) {
	src := dsEntry()
	locked := &ProviderEntry{Name: "prov-locked", Kind: "openai", BaseURL: "https://api.deepseek.com", Model: "deepseek-v4-flash", Thinking: "disabled"}
	got, note, ok := MapEffortAcrossVocabularies(src, locked, "max")
	if !ok || got != "" {
		t.Fatalf("depth → locked destination: got (%q, ok=%v), want auto", got, ok)
	}
	if !strings.Contains(note, "auto") {
		t.Fatalf("locked destination note must tell the user, got %q", note)
	}
	got, _, ok = MapEffortAcrossVocabularies(src, locked, "disabled")
	if !ok || got != "disabled" {
		t.Fatalf("disabled → locked destination: got (%q, ok=%v), want disabled", got, ok)
	}
}

// TestMapEffortDeepSeekMaxOntoMimo is the user-scenario nail (task 1169
// acceptance 1): deepseek max — the ceiling of deepseek's vocabulary — maps
// onto the ceiling of MiMo's reachable depths (its stored form "high"), not
// onto auto. The other depths keep their relative positions without ever
// reaching MiMo's off slot.
func TestMapEffortDeepSeekMaxOntoMimo(t *testing.T) {
	ds := dsEntry()
	mimo := mimoEntry(nil)
	got, _, ok := MapEffortAcrossVocabularies(ds, mimo, "max")
	if !ok {
		t.Fatal("max: mapping declined; the ceiling must carry")
	}
	if got != "high" {
		t.Fatalf("deepseek max mapped to %q, want mimo's ceiling high", got)
	}
	// The rest of the vocabulary keeps relative positions, none landing on
	// the off slot. mimo's depth list is minimal,low,medium,high,xhigh,max,
	// ultra: ds ranks 0/.5/1 land on minimal(stored low), high, ultra(stored high).
	for level, want := range map[string]string{"low": "low", "high": "high", "disabled": "none"} {
		got, _, ok := MapEffortAcrossVocabularies(ds, mimo, level)
		if !ok || got != want {
			t.Fatalf("%s mapped to (%q, ok=%v), want %s", level, got, ok, want)
		}
	}
}

// TestMapEffortAutoAndEffortlessKeepsStatusQuo: an auto (or empty) source
// never maps, and an effort-less destination keeps the status quo — the
// caller falls back to its existing auto behavior unchanged.
func TestMapEffortAutoAndEffortlessKeepsStatusQuo(t *testing.T) {
	ds := dsEntry()
	dst := xe("low", "high")
	for _, level := range []string{"auto", "", "  "} {
		got, _, ok := MapEffortAcrossVocabularies(ds, dst, level)
		if ok || got != "" {
			t.Fatalf("level %q: got (%q, ok=%v), want declined", level, got, ok)
		}
	}
	effortless := &ProviderEntry{Name: "prov-plain", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "plain", ReasoningProtocol: "none"}
	if got, _, ok := MapEffortAcrossVocabularies(ds, effortless, "max"); ok || got != "" {
		t.Fatalf("effort-less destination: got (%q, ok=%v), want declined", got, ok)
	}
	if got, _, ok := MapEffortAcrossVocabularies(nil, dst, "max"); ok || got != "" {
		t.Fatalf("nil source: got (%q, ok=%v), want declined", got, ok)
	}
}
