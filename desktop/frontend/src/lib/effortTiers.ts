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

/** Fold an option list onto the composer effort menu (task 301), but only when
 * the vocabulary actually is the MiMo compatibility set. Task 331: the four
 * honest tiers are MiMo's — on other providers "max" and "disabled"/"enabled"
 * are honest levels, not aliases, and folding them onto the MiMo presets
 * silently dropped max (deepseek-v4-flash rendered low,high,disabled with max
 * folded into high) and reordered the rest.
 * Task effortfix2 (331 follow-up): the first判据 (any of minimal/xhigh/ultra)
 * was too loose — "xhigh" is NOT MiMo-exclusive: the deepseek model behind
 * opencode-go-anthropic carries the honest anthropic set
 * [low,medium,high,xhigh,max] and gpt-5.6-luna carries
 * [none,low,medium,high,xhigh,max]; folding those dropped the honest "max".
 * The MiMo set is now identified by BOTH of its genuinely exclusive words —
 * minimal AND ultra together (every MiMo eight-level vocabulary has both; no
 * other provider vocabulary has either); everything else passes through
 * untouched, preserving both the full list and its order. */
export function foldEffortMenu(levels: readonly string[]): string[] {
  if (!isMiMoEffortVocabulary(levels)) return [...levels];
  const folded = [...new Set(levels.map((level) => normalizeEffortForMenu(level)))];
  return [
    ...EFFORT_PRESETS.filter((tier) => folded.includes(tier)),
    ...folded.filter((level) => level !== "auto" && !EFFORT_PRESETS.includes(level)),
  ];
}

// Task effortfix2: the vocabulary decision behind foldEffortMenu, exported so
// the displayed current level follows the same family rule as the menu.
export function isMiMoEffortVocabulary(levels: readonly string[]): boolean {
  return levels.includes("minimal") && levels.includes("ultra");
}

/** Fold the CURRENT level the same way the menu folded its options: only when
 * the vocabulary is MiMo's (so deepseek current=max displays max, while MiMo
 * current=xhigh displays high). */
export function foldEffortCurrent(levels: readonly string[], current: string): string {
  if (!isMiMoEffortVocabulary(levels)) return current || "auto";
  return normalizeEffortForMenu(current) || "auto";
}
