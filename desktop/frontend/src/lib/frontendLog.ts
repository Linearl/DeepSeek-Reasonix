import { app } from "./bridge";

/**
 * Report a fork-feature event to desktop.log.
 *
 * The fork's features are largely frontend-side (project grouping, colour filtering, question
 * search, draft persistence, history paging) and previously had no route to the log, so a
 * frontend problem left nothing behind. `feature=` makes these lines greppable and keeps them
 * distinguishable from upstream behaviour.
 *
 * Fire-and-forget, and optional on purpose: diagnostics must never be able to break a feature,
 * and test doubles or an older backend simply will not have the method.
 *
 * Callers own the frequency. These lines land in a 4MB rolling log, so an event that fires per
 * keystroke or per scroll frame does not belong here - log the transitions that explain
 * behaviour instead (a group moved, a draft evicted, a page request superseded).
 */
export function reportFrontendLog(
  feature: string,
  message: string,
  detail?: string,
  level: "info" | "warn" | "error" = "info",
): void {
  if (!feature || !message) return;
  void app.ReportFrontendLog?.(feature, level, message, detail ?? "")?.catch(() => {});
}
