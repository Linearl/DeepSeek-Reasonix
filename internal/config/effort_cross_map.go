package config

import "math"

// MapEffortAcrossVocabularies maps a carried effort level from one provider
// entry's vocabulary onto another entry's by relative position (task 1169,
// rank-based). The outgoing level keeps its rank — its index inside the
// source's reachable depth list, normalized to [0,1] — and lands on the
// same-rank slot of the destination's list, so a source ceiling maps onto a
// destination ceiling (deepseek max → MiMo's best) instead of collapsing to
// auto. This is the mapping step the model-switch fold runs BEFORE handing
// the level to the destination's own gate (the 1161b
// SetSessionModelOverrideWithEffort vocabulary check stays the downstream
// safety net; the two compose — this never replaces it).
//
// The source and target lists are EffortMenuDomain's, in its return order
// (already deduplicated and wire-filtered) — this function never sorts or
// renames levels to guess semantics. Levels whose normalized value means
// "thinking off" (the source/destination disabled slot, resolved through
// each side's own NormalizeEffort so a MiMo "none" is recognized as the
// disabled slot it is) are excluded from the rank space and handled by the
// off-thinking rule: a carried off-thought level carries onto the
// destination's off-thought slot when it has one and lands on auto when it
// does not — never onto low, which would silently turn thinking back on (a
// functional-correctness regression).
//
// Returns the mapped level in the destination's STORED vocabulary (the
// NormalizeEffort output: a rank slot may be a menu alias such as MiMo
// "xhigh" whose stored form is "high"), an optional user-facing note for a
// special mapping the default fold notice cannot describe, and ok=false when
// the caller must keep the status-quo auto ("": auto source, unknown source
// level, effort-less destination, unreachable defense). ok=true with an
// empty mapped means "map to auto deliberately" (the off-thought and
// off-only-destination rules).
func MapEffortAcrossVocabularies(src, dst *ProviderEntry, level string) (mapped, note string, ok bool) {
	// The destination must have a reachable menu at all; an effort-less or
	// fully filtered destination keeps the status-quo auto (ok=false).
	dstMenu, _ := EffortMenuDomain(dst)
	if countNonAuto(dstMenu) == 0 {
		return "", "", false
	}
	nl := normalizeEffortLevel(level)
	if nl == "" || nl == "auto" {
		// auto is the inherit-the-default alias on every destination; the
		// caller handles it before mapping ever runs.
		return "", "", false
	}
	if src == nil {
		// No source entry to rank against: treat like an unknown level and
		// keep the status-quo auto.
		return "", "", false
	}
	srcMenu, _ := EffortMenuDomain(src)

	// The off-thinking slot of each side, resolved through that side's own
	// normalizer so vocabulary spellings are not guessed by name: deepseek
	// keeps "disabled", MiMo folds disabled→"none". Empty = this entry has
	// no reachable off-thinking slot at all (MiniMax M3.1 rejects it).
	offSrc := offThinkingLevel(src)
	offDst := offThinkingLevel(dst)

	// Rule 1 — the carried level sits on the source's off-thinking slot
	// (the literal "disabled" or the entry's folded equivalent): carry it
	// onto the destination's off slot when one exists, else auto. It never
	// enters the rank space, so it can never surface as low on a destination
	// that cannot switch thinking off.
	if (nl == "disabled" && containsString(srcMenu, "disabled")) || (offSrc != "" && nl == offSrc) {
		if offDst != "" && containsString(dstMenu, offDst) {
			return offDst, "", true
		}
		return "", "", true
	}

	// Rule 2 — rank the carried level inside the source's depth list (menu
	// order is authoritative; off-thinking slot and the auto alias are not
	// depth levels). A level absent from it is a config-leftover oddity:
	// keep the status-quo auto, no mapping.
	srcDepths := depthList(srcMenu, offSrc)
	idx := -1
	for i, l := range srcDepths {
		if l == nl {
			idx = i
			break
		}
	}
	if idx < 0 {
		return "", "", false
	}

	// Rule 3 — the destination has no depth beyond its off-thinking slot (a
	// thinking-locked entry): auto plus a visible note — never silently.
	dstDepths := depthList(dstMenu, offDst)
	if len(dstDepths) == 0 {
		return "", "this model only supports turning thinking off; effort reset to auto", true
	}

	// The rank formula (task 1169): normalize the source index to [0,1],
	// land on the same relative position of the destination list. A single
	// depth destination takes every non-off source onto its only depth.
	// j is clamped as float defense; the formula already keeps it in
	// [0, m-1] because rank ∈ [0,1] exactly (i = n-1 yields 1.0).
	n := len(srcDepths)
	m := len(dstDepths)
	rank := 1.0
	if n > 1 {
		rank = float64(idx) / float64(n-1)
	}
	j := 0
	if m > 1 {
		j = int(math.Round(rank * float64(m-1)))
		if j < 0 {
			j = 0
		}
		if j > m-1 {
			j = m - 1
		}
	}
	picked := dstDepths[j]
	// Defense in depth (acceptance 6): the slot must survive the same gates
	// a stored level faces, and its stored form must normalize — both are
	// given for EffortMenuDomain members, but a silent violation here would
	// arm a depth the next request drops, so fail closed to the status quo.
	if !EffortLevelReachesWire(dst, picked) {
		return "", "", false
	}
	stored, err := NormalizeEffort(dst, picked)
	if err != nil {
		return "", "", false
	}
	return stored, "", true
}

// offThinkingLevel returns the level an entry's NormalizeEffort folds
// "disabled" onto ("" = this entry cannot express thinking-off): deepseek
// keeps "disabled", MiMo folds to "none". Fold-failures (MiniMax M3.1) and
// effort-less entries return "".
func offThinkingLevel(e *ProviderEntry) string {
	if e == nil {
		return ""
	}
	off, err := NormalizeEffort(e, "disabled")
	if err != nil {
		return ""
	}
	return normalizeEffortLevel(off)
}

// depthList filters a menu domain down to its depth levels in menu order:
// the "auto" alias and the entry's off-thinking slot are excluded.
func depthList(menu []string, offLevel string) []string {
	out := make([]string, 0, len(menu))
	for _, l := range menu {
		if l == "auto" || (offLevel != "" && l == offLevel) {
			continue
		}
		out = append(out, l)
	}
	return out
}

func countNonAuto(menu []string) int {
	n := 0
	for _, l := range menu {
		if l != "auto" {
			n++
		}
	}
	return n
}
