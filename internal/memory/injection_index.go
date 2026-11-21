// tl1733 Block1: the injection-side slimming pipeline for the background
// memory index. IndexFrom stays the untouched source of truth (its output is
// what tool layers, disk MEMORY.md, and every legacy test rely on); this file
// renders the *injected* variant of the same index — shorter lines, low-value
// facts dropped, a hard size budget — for BackgroundDataBlock only.
//
// Measured baseline (task book, 2026-11-20): the injected index ran 908 lines
// / 357K chars; titles+file references (redundant with the title) carried 64%
// of it, descriptions 36% (p50=73, only 6.7% over 150); 41% of facts had zero
// recalls in 30 days. Competitors ship no unbounded full-pool injection
// (zcode caps at ~200 lines/25K, codex summarizes + searches on demand).
package memory

import (
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// Injection tuning constants (tl1733 Block1; token-budget acceptance is
// measured against these in the US-1 report).
const (
	// InjectionDescriptionRunes bounds the per-line description, mirroring the
	// pinned tier's PinnedIndexDescriptionRunes. Render-time truncation only:
	// the body (and full description, via memory search) stays retrievable.
	InjectionDescriptionRunes = 150
	// InjectionBudgetRunes is the fallback budget (item 4, character-based
	// first pass — the token-based measurement rides the US-1 acceptance run).
	// When items 1-3 still leave the section over budget, lines are admitted
	// most-recently-recalled-first until the budget is spent.
	InjectionBudgetRunes = 20000
	// InjectionNoticeMinOmitted: the adaptive section-head notice (item 7)
	// appears only when at least this many facts were left out of the section,
	// so a light trim never spends a line announcing itself.
	InjectionNoticeMinOmitted = 20
	// zeroRecallMinAgeDays: a fact younger than this is never dropped by the
	// zero-recall cutoff (item 3's cold-start exemption — new facts have had
	// no chance to be recalled, and a zero count means nothing about them).
	zeroRecallMinAgeDays = 30
	// zeroRecallWindowDays is the observation window: a fact is eligible for
	// the cutoff only when it recorded no recall inside the last window.
	zeroRecallWindowDays = 30 * 24 * time.Hour
	// UsageMaturityDays is the hard data-maturity gate (expert-reviewed): the
	// cutoff stays entirely inert until the ledger has accumulated this long,
	// so day-zero (all counters at zero) can never trim in-use memories.
	UsageMaturityDays = 14
)

// injectionNoticeLine is the section-head notice (item 7): declarative only —
// no suggested action, so the notice itself never steers the model into
// mass-searching the on-demand tier. It re-renders with the data section every
// turn, so it stays fresh by construction.
const injectionNoticeLine = "已省略 %d 条低频记忆（可检索）。\n"

// memoryIndexSlimModeFlag is the tl1733 lab gate (config
// experimental_memory_index_slim, boot-resolved like the pinned-index gate).
// On = emergency bypass: the background index section stops injecting
// entirely; only pinned guidance rides session-context. Off (default) = the
// slimmed injection pipeline below.
var memoryIndexSlimModeFlag atomic.Bool

// SetMemoryIndexSlimMode sets the tl1733 injection-slimming gate. Boot calls
// it once with the saved config value; tests call it directly (t.Cleanup
// restores false). Read per call through memoryIndexSlimMode.
func SetMemoryIndexSlimMode(on bool) { memoryIndexSlimModeFlag.Store(on) }

// memoryIndexSlimMode reports whether the emergency bypass is on: when true,
// the background index section is not injected at all (pinned guidance keeps
// its own channel) while the on-demand layer (memory search/read/list) stays
// complete — verified by the acceptance matrix (E2).
func memoryIndexSlimMode() bool {
	return memoryIndexSlimModeFlag.Load()
}

// injectionLine is one rendered index row plus its admission metadata for the
// budget pass (item 4).
type injectionLine struct {
	text  string
	runes int
	// lastRecall is the fact's most recent recall stamp (zero = never
	// recalled); it orders the budget fallback's admission.
	lastRecall time.Time
	updatedAt  time.Time
}

// RenderInjectionIndex renders the injected background index over the pool the
// caller already read (the Load-time listing; IndexFrom takes its own copy so
// orderings never clash). Returns the section body without the "### Background
// memory index" frame:
//   - every line is title + scope/type marker + ≤150-rune description (items
//     1-2; the file reference is dropped from the injected form only);
//   - facts older than zeroRecallMinAgeDays with no recall inside
//     zeroRecallWindowDays are left out, but only once the usage ledger is
//     mature (item 3 + its hard maturity gate);
//   - a 20K-rune budget admits lines most-recently-recalled-first (item 4);
//   - omitting ≥InjectionNoticeMinOmitted facts prepends the adaptive notice
//     (item 7).
// Pinned facts are exempt from every cut.
func (s Store) RenderInjectionIndex(all []Memory, now time.Time) string {
	memories := all
	if len(memories) == 0 {
		return ""
	}
	mature := s.UsageMature(now, UsageMaturityDays)
	shadowed := map[string]string{} // global fact ID -> winning project reference
	for _, o := range FindOverrides(memories) {
		shadowed[o.Global.ID] = providerMemoryReference(o.Project)
	}
	sort.SliceStable(memories, func(i, j int) bool {
		if memories[i].Name != memories[j].Name {
			return memories[i].Name < memories[j].Name
		}
		return NormalizeFactScope(string(memories[i].Scope)) == FactScopeProject
	})

	lines := make([]injectionLine, 0, len(memories))
	omitted := 0
	seen := map[string]bool{} // collapse legacy migration duplicates (same qualified ref)
	for _, m := range memories {
		if ref := providerMemoryReference(m); seen[ref] {
			continue
		} else {
			seen[ref] = true
		}
		if pinned := ResolveActivation(m) == ActivationPinned; !pinned && s.zeroRecallDroppable(m, now, mature) {
			omitted++
			continue
		}
		text := renderInjectionLine(m)
		if winner, ok := shadowed[m.ID]; ok &&
			NormalizeFactScope(string(m.Scope)) == FactScopeGlobal {
			text += " (overridden by " + winner + ")"
		}
		lines = append(lines, injectionLine{
			text:       text,
			runes:      utf8.RuneCountInString(text) + 1, // + trailing newline
			lastRecall: s.usageLastRecall(m.ID),
			updatedAt:  m.UpdatedAt,
		})
	}

	// Item 4: when the section still exceeds the budget, admission flips from
	// name order to most-recently-recalled order (never-recalled facts trail
	// by their update time) and lines are kept while the budget lasts.
	// Items 1-3 render in name order (the legacy order — byte-stable for the
	// session-context delta machinery). Item 4 only reorders when the budget
	// actually bites: below budget, the section stays in the legacy order.
	totalRunes := 0
	for _, line := range lines {
		totalRunes += line.runes
	}
	if totalRunes > InjectionBudgetRunes {
		sort.SliceStable(lines, func(i, j int) bool { return injectionRecencyLess(lines[i], lines[j]) })
	}
	var b strings.Builder
	used := 0
	for i, line := range lines {
		// The first line is always admitted (used > 0 guard) so the section
		// can never come back empty-handed while facts qualified for it.
		if used > 0 && used+line.runes > InjectionBudgetRunes {
			omitted += len(lines) - i
			break
		}
		used += line.runes
		b.WriteString(line.text)
		b.WriteString("\n")
	}
	if omitted >= InjectionNoticeMinOmitted {
		return fmt.Sprintf(injectionNoticeLine, omitted) + b.String()
	}
	return b.String()
}

// injectionRecencyLess orders lines most-recently-recalled first; facts never
// recalled trail in update-time order, so the budget pass keeps the live tier.
func injectionRecencyLess(a, b injectionLine) bool {
	if !a.lastRecall.Equal(b.lastRecall) {
		if a.lastRecall.IsZero() {
			return false
		}
		if b.lastRecall.IsZero() {
			return true
		}
		return a.lastRecall.After(b.lastRecall)
	}
	return a.updatedAt.After(b.updatedAt)
}

// zeroRecallDroppable implements item 3 with its two safety valves: the hard
// maturity gate (no ledger tenure → nothing is droppable, so day-zero zero
// counts can never trim in-use memories) and the cold-start exemption (facts
// younger than zeroRecallMinAgeDays — or with an unknown creation date — are
// never dropped by a zero count).
func (s Store) zeroRecallDroppable(m Memory, now time.Time, mature bool) bool {
	if !mature {
		return false
	}
	if m.CreatedAt.IsZero() {
		return false // unknown age: no subtraction on missing data
	}
	if now.Sub(m.CreatedAt) <= zeroRecallMinAgeDays*24*time.Hour {
		return false // cold start: too young to have earned a recall count
	}
	return !s.UsageRecalledWithin(m.ID, now, zeroRecallWindowDays)
}

// usageLastRecall returns the fact's most recent recall stamp for the budget
// ordering (zero when never recalled).
func (s Store) usageLastRecall(id string) time.Time {
	u := usageFor(s)
	if u == nil {
		return time.Time{}
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.ensureLoadedLocked()
	if e, ok := u.data.Memories[id]; ok && e != nil {
		return e.LastRecallAt
	}
	return time.Time{}
}

// renderInjectionLine is the injected variant of renderQualifiedIndexLine
// (tl1733 item 1): the file reference leaves the injected line — the title
// already carries the same information (avg 67 chars/line of framework cost),
// and every consumer of the reference re-derives it via the memory tool
// (search returns providerMemoryReference, read accepts it back). The marker
// and the ≤150-rune description (item 2) stay.
func renderInjectionLine(m Memory) string {
	marker := ""
	if ResolveActivation(m) == ActivationPinned {
		marker = " pinned"
	}
	return fmt.Sprintf("- %s — [%s/%s%s] %s",
		displayTitle(m.Title, m.Name),
		NormalizeFactScope(string(m.Scope)), NormalizeType(string(m.Type)), marker,
		truncateRunes(oneLine(m.Description), InjectionDescriptionRunes))
}
