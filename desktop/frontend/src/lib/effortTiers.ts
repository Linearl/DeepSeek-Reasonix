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
