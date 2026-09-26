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
 * folded into high) and reordered the rest. The MiMo set is identified by its
 * unique alias words (minimal/xhigh/ultra); every other vocabulary passes
 * through untouched, preserving both the full list and its order. */
export function foldEffortMenu(levels: readonly string[]): string[] {
  const isMiMoVocabulary = levels.some((level) =>
    level === "minimal" || level === "xhigh" || level === "ultra");
  if (!isMiMoVocabulary) return [...levels];
  const folded = [...new Set(levels.map((level) => normalizeEffortForMenu(level)))];
  return [
    ...EFFORT_PRESETS.filter((tier) => folded.includes(tier)),
    ...folded.filter((level) => level !== "auto" && !EFFORT_PRESETS.includes(level)),
  ];
}
