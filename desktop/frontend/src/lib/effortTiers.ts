// Task 254 (user ruling on task 251): the settings UI offers four honest
// effort tiers, while the kernel keeps accepting the full eight-level wire set
// (none|minimal|low|medium|high|xhigh|max|ultra) — minimal/xhigh/max/ultra are
// compatibility aliases per the MiMo docs, folded by the backend's
// normalizeMimoEffort, which this module deliberately does not touch.

/** The four honest tiers the settings pickers advertise. */
export const EFFORT_PRESETS: readonly string[] = ["none", "low", "medium", "high"];

/** Fold a stored /effort level onto the four-tier menu (task 254). Aliases keep
 * working on the wire; the picker shows the tier they mean. Unknown values are
 * returned as-is so a hand-edited level is never silently rewritten. */
export function normalizeEffortForMenu(level: string | undefined): string {
  switch ((level ?? "").trim()) {
    case "minimal":
      return "low";
    case "xhigh":
    case "max":
    case "ultra":
      return "high";
    default:
      return level ?? "";
  }
}

/** Fold an option list onto the composer effort menu (task 301), ONLY when the
 * backend marked the entry as MiMo (EffortInfo.aliasFold, task effortfix2 /
 * A-line P2).
 *
 * The trigger is the provider/protocol IDENTITY, not a vocabulary shape:
 * task 301 folded every vocabulary unconditionally; task 331's first fix
 * sniffed the words (minimal|xhigh|ultra) and mis-folded the honest anthropic
 * family set [low,medium,high,xhigh,max] ("xhigh" is not MiMo-exclusive),
 * dropping its honest max. Now `aliasFold=false` (every non-MiMo family, and
 * any config-declared supported_efforts) passes the list through byte-for-byte;
 * when the backend DID mark MiMo, the shape check below stays as a second
 * line of defense against a mis-mark, never as an independent trigger. */
export function foldEffortMenu(levels: readonly string[], aliasFold = false): string[] {
  if (!aliasFold || !isMiMoEffortVocabulary(levels)) return [...levels];
  const folded = [...new Set(levels.map((level) => normalizeEffortForMenu(level)))];
  return [
    ...EFFORT_PRESETS.filter((tier) => folded.includes(tier)),
    ...folded.filter((level) => level !== "auto" && !EFFORT_PRESETS.includes(level)),
  ];
}

// The vocabulary shape (both MiMo-exclusive words present) — kept as a
// defensive second condition behind the identity flag, exported for tests and
// for foldEffortCurrent's shared rule.
export function isMiMoEffortVocabulary(levels: readonly string[]): boolean {
  return levels.includes("minimal") && levels.includes("ultra");
}

/** Fold the CURRENT level by the same identity rule as the menu: only for a
 * backend-marked MiMo entry (so deepseek current=max displays max, while MiMo
 * current=xhigh displays high). */
export function foldEffortCurrent(levels: readonly string[], current: string, aliasFold = false): string {
  if (!aliasFold || !isMiMoEffortVocabulary(levels)) return current || "auto";
  return normalizeEffortForMenu(current) || "auto";
}
