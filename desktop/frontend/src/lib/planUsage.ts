// Task 287 — provider plan usage formatting (coding plan / token plan quota).
//
// The wire shape and the per-provider endpoints are owned by the Go side
// (desktop/plan_usage.go, ported from cc-switch); this module only names the
// windows, picks display tones, and formats the status bar / overview readout,
// so the surfaces stay pure views.

/** Window display keys; unknown wire keys fall through to their raw name. */
export const PLAN_USAGE_WINDOW_KEYS = ["five_hour", "weekly", "monthly"] as const;

export type PlanUsageWindowKey = (typeof PLAN_USAGE_WINDOW_KEYS)[number];

export type PlanUsageWindowView = {
  window: string;
  percent: number | null;
  resetsAt: string;
};

export type PlanUsageResult = {
  supported: boolean;
  provider?: string;
  region?: string;
  windows: PlanUsageWindowView[];
  note: string;
  queriedAt: number;
};

/** Degradation notes the Go side can emit; "" means real windows. */
export type PlanUsageNoteKey =
  | "planUsage.note.noKey"
  | "planUsage.note.authFailed"
  | "planUsage.note.unsupported"
  | "planUsage.note.failed";

/** planWindowLabel maps a wire window key to its display key; unknown keys
 * render their raw name so a renamed window is still visible. */
export function planWindowLabelKey(window: string): string {
  switch (window) {
    case "five_hour":
      return "planUsage.window.fiveHour";
    case "weekly":
      return "planUsage.window.weekly";
    case "monthly":
      return "planUsage.window.monthly";
    default:
      return window;
  }
}

/**
 * planUsageTone picks the display tone from a used percent: exhausted (>=100)
 * is critical — the 5h-exhausted warning the status bar must show — and >=80
 * is notice (window closing). Anything below is neutral.
 */
export function planUsageTone(percent: number | null | undefined): "critical" | "notice" | undefined {
  if (typeof percent !== "number" || !Number.isFinite(percent)) return undefined;
  if (percent >= 100) return "critical";
  if (percent >= 80) return "notice";
  return undefined;
}

/**
 * planUsageNoteText maps the Go-side note to user copy. The no-key case reads
 * as setup guidance, never as an error (unsupported providers are hidden
 * upstream of this — the note surfaces only for a configured plan provider).
 */
export function planUsageNoteText(note: string, t: (key: PlanUsageNoteKey) => string): string {
  switch (note) {
    case "":
      return "";
    case "no-key":
      return t("planUsage.note.noKey");
    case "auth-failed":
      return t("planUsage.note.authFailed");
    case "unsupported":
      return t("planUsage.note.unsupported");
    default:
      // network / parse / http-<code> / api-error: one honest generic line.
      return t("planUsage.note.failed");
  }
}

/**
 * planExhausted reports whether the five-hour window — the one the fallback
 * model switch (task 242) cares about — is used up. This is the proactive
 * data source: 242's own trigger stays reactive (quota errors), while this
 * readout lets any surface warn before the next request fails.
 */
export function planFiveHourExhausted(view: PlanUsageResult | null | undefined): boolean {
  const five = view?.windows.find((w) => w.window === "five_hour");
  return typeof five?.percent === "number" && Number.isFinite(five.percent) && five.percent >= 100;
}
