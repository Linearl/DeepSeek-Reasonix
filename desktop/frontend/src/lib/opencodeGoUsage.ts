// Task 163 — OpenCode Go subscription usage card (settings detail card).
//
// The wire shape and the official-but-undocumented endpoint are owned by the
// Go side (desktop/opencode_go_usage.go); this module only formats what comes
// back and names the degradation notes, so the card stays a pure view.

/** Official preset base — the Go side allow-lists this host before any request. */
export const OPENCODE_GO_OFFICIAL_BASE = "https://opencode.ai/zen/go/v1";

export type OpenCodeGoUsageTier = {
  window: string; // "rolling" (5h) | "weekly" (7d) | "monthly"
  percent: number | null;
  resetsAt: string;
};

export type OpenCodeGoUsageResult = {
  tiers: OpenCodeGoUsageTier[];
  note: string;
};

/** Window display keys; unknown wire keys fall through to their raw name. */
export const OPENCODE_GO_WINDOW_KEYS = ["rolling", "weekly", "monthly"] as const;

/**
 * resetCountdown renders the remaining time until an ISO reset instant as a
 * compact countdown ("42m", "3h 12m", "2d 4h"). Expired/invalid instants
 * return "" so the card never shows a bogus zero.
 */
export function resetCountdown(iso: string, now: number = Date.now()): string {
  const at = Date.parse(iso);
  if (!Number.isFinite(at)) return "";
  const ms = at - now;
  if (ms <= 0) return "";
  const totalMinutes = Math.floor(ms / 60_000);
  if (totalMinutes < 60) return `${totalMinutes}m`;
  const hours = Math.floor(totalMinutes / 60);
  if (hours < 24) return `${hours}h ${totalMinutes % 60}m`;
  return `${Math.floor(hours / 24)}d ${hours % 24}h`;
}

/**
 * usageNoteText maps the Go-side note to user copy. The 403 case must read as
 * an entitlement fact — a valid key without a Go subscription — never as a
 * generic authentication failure (task 163 acceptance 2).
 */
// The t parameter is typed as the five literal keys it may pass, so any
// Translator (whose parameter is the full DictKey union) satisfies it while a
// plain (key: string) function no longer type-checks by accident.
export type UsageNoteKey =
  | "settings.opencodeGoUsage.note.noKey"
  | "settings.opencodeGoUsage.note.noSubscription"
  | "settings.opencodeGoUsage.note.authFailed"
  | "settings.opencodeGoUsage.note.unsupported"
  | "settings.opencodeGoUsage.note.failed";

export function usageNoteText(note: string, t: (key: UsageNoteKey) => string): string {
  switch (note) {
    case "":
      return "";
    case "no-key":
      return t("settings.opencodeGoUsage.note.noKey");
    case "no-subscription":
      return t("settings.opencodeGoUsage.note.noSubscription");
    case "auth-failed":
      return t("settings.opencodeGoUsage.note.authFailed");
    case "unsupported-endpoint":
      return t("settings.opencodeGoUsage.note.unsupported");
    default:
      // network / parse / http-<code>: one honest generic line.
      return t("settings.opencodeGoUsage.note.failed");
  }
}
