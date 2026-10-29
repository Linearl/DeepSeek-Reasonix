// Plan E (tl1193): pinned responsibility migration. The pinned tier's real
// demand (8 facts × 500-700 runes ≈ 5477) never fit the 1500-rune body budget
// — the symptom was the budget, the root cause was the tier carrying full
// bodies. Plan E moves the standing tier to one self-sufficient index line per
// pinned fact (zcode MEMORY.md pattern, code-verified 2026-10-27) and turns
// bodies into an on-demand recall channel. Gated behind the
// experimental_pinned_index config lab switch (tl1193b: the former env channel
// REASONIX_EXPERIMENT_TL1193_PINNED_INDEX retired — it was a merge-line
// evaluation gate, never shipped in a release build; boot resolves the saved
// switch into the package gate below once before the first Load, so the mode
// applies on restart). Default off = bodies keep riding session-context
// byte-for-byte unchanged. No data migration: fact files stay the single
// source of truth, and the recall pool picks bodies up by simply not excluding
// pinned facts while the mode is on.
package memory

import (
	"fmt"
	"strings"
	"sync/atomic"
	"unicode/utf8"
)

// PinnedIndexMaxEntries caps the standing pinned tier by count once plan E is
// on. With bodies off the prefix, per-entry cost is one bounded line, so a
// count cap (not a rune budget) is the natural growth bound: 12 entries ×
// ~200 runes of line frame ≈ 2.4K worst case, still under half of the
// 5477-rune status quo the tier grew into. Enforced at write time for net
// growth only — replacing or unpinning never regresses (same shape as the
// 1192 ratchet repair).
const PinnedIndexMaxEntries = 12

// PinnedIndexDescriptionRunes caps the description carried per standing line
// (task-book acceptance: ≤150 runes/line). A longer description is truncated
// at render, never rejected at write: the body stays fully retrievable, so a
// soft truncation cannot lose information — it only bounds the standing cost.
const PinnedIndexDescriptionRunes = 150

// pinnedIndexModeFlag is the package-level plan-E gate (tl1193b: config lab
// switch experimental_pinned_index, boot-resolved — capabilityGroups
// boot-snapshot precedent). Zero value = off, so a process that never runs
// boot keeps the legacy full-body injection byte-for-byte.
var pinnedIndexModeFlag atomic.Bool

// SetPinnedIndexMode sets the plan-E gate. Boot calls it once with the saved
// config value before the first Load; tests call it directly (t.Cleanup
// restores false). Read per call through pinnedIndexMode, so flips are seen
// immediately by render/save/recall alike.
func SetPinnedIndexMode(on bool) { pinnedIndexModeFlag.Store(on) }

// pinnedIndexMode reports whether plan E is on. Read per call (not cached at
// init) so a boot-time resolve (or a test flip) applies to every consumer —
// render, write-path quota and recall pool — without restart within the
// process.
func pinnedIndexMode() bool {
	return pinnedIndexModeFlag.Load()
}

// pinnedIndexBlock renders the standing tier in index form: one line per
// pinned fact plus the instruction that points reads at the `memory` tool.
// Deterministic for a given snapshot (stable sort upstream, fixed frame), so
// repeated renders stay byte-identical like the body form.
func pinnedIndexBlock(guidance []Memory) string {
	var b strings.Builder
	b.WriteString("### Pinned preferences and feedback\n")
	b.WriteString("\nEach line below is the standing index of one pinned fact; the full body is not injected every turn. Read a fact's complete body with the `memory` tool (reference in parentheses) before relying on its details.\n")
	for _, m := range guidance {
		b.WriteString("\n")
		b.WriteString(renderPinnedIndexLine(m))
		b.WriteString("\n")
	}
	return b.String()
}

// renderPinnedIndexLine is the pinned-tier variant of the background index
// line: same shape the model already knows from "### Background memory index"
// (title, tool-accepted reference, scope/type marker), with the description
// bounded to PinnedIndexDescriptionRunes.
func renderPinnedIndexLine(m Memory) string {
	return fmt.Sprintf("- [%s](%s) — [%s/%s pinned] %s",
		displayTitle(m.Title, m.Name), providerMemoryReference(m),
		NormalizeFactScope(string(m.Scope)), NormalizeType(string(m.Type)),
		truncateRunes(oneLine(m.Description), PinnedIndexDescriptionRunes))
}

// truncateRunes bounds s to max runes, appending an ellipsis when truncated.
func truncateRunes(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:max])) + "…"
}

// validatePinnedIndexQuota is the plan-E replacement for the body-rune budget:
// with bodies off the prefix, body size no longer costs the session anything,
// so the standing tier is bounded by entry count instead. A save that would
// push the tier past PinnedIndexMaxEntries while growing it is rejected with a
// copyable /memory unpin ledger; replacing an already-pinned fact or unpinning
// always passes, so an over-cap store stays curatable (1192 principle).
func (s Store) validatePinnedIndexQuota(m Memory) error {
	if ResolveActivation(m) != ActivationPinned {
		return nil
	}
	guidance := s.pinnedGuidance()
	others := 0
	replacing := false
	for _, p := range guidance {
		if p.ID == m.ID || (m.ID == "" && p.Name == m.Name) {
			replacing = true
			continue
		}
		others++
	}
	if replacing || others+1 <= PinnedIndexMaxEntries {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "pinning this fact would make the standing pinned index %d entries, over the %d-entry budget: pinned facts ride session-context as one-line indexes, and full bodies stay one `memory` read away\n",
		others+1, PinnedIndexMaxEntries)
	fmt.Fprintf(&b, "pinned index total=%d limit=%d; current pinned entries (name | scope | updated_at):\n", others, PinnedIndexMaxEntries)
	suggest, suggestChars := "", -1
	for _, p := range guidance {
		fmt.Fprintf(&b, "  %s | %s | %s\n", p.Name, NormalizeFactScope(string(p.Scope)), formatMemoryDay(p.UpdatedAt))
		if chars := BodyChars(p); chars > suggestChars {
			suggest, suggestChars = p.Name, chars
		}
	}
	if suggest != "" {
		fmt.Fprintf(&b, "frees the most budget: /memory unpin %s", suggest)
	}
	return fmt.Errorf("%s", b.String())
}
