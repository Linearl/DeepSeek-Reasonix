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
 *
 * 任务587（网关断连不静默）：the desktop.log route itself dies silently when the Wails
 * gateway is down — which is exactly the moment these reports matter (511 复发 §5: a swallowed
 * panel failure left ZERO trace anywhere). So every report now lands in three places, in order:
 *   1. an in-memory ring buffer (survives gateway loss; drained by tests / future diagnostics),
 *   2. the devtools console (always visible in a packed build via remote debugging),
 *   3. the desktop.log binding (best effort, unchanged).
 */
const RING_MAX = 100;
const ring: string[] = [];

/** Snapshot of the most recent reports, oldest first (read-only view). */
export function peekFrontendLogRing(): readonly string[] {
  return ring;
}

/** Take everything buffered so far, oldest first, and empty the buffer. */
export function drainFrontendLogRing(): string[] {
  return ring.splice(0, ring.length);
}

function remember(line: string): void {
  ring.push(line);
  if (ring.length > RING_MAX) ring.splice(0, ring.length - RING_MAX);
}

export function reportFrontendLog(
  feature: string,
  message: string,
  detail?: string,
  level: "info" | "warn" | "error" = "info",
): void {
  if (!feature || !message) return;
  const line = `[frontend:${feature}] ${message}${detail ? ` — ${detail}` : ""}`;
  // ① local ring: the only trace that survives a dead gateway.
  remember(line);
  // ② console: visible without any binding being reachable.
  if (level === "error") console.error(line);
  else if (level === "warn") console.warn(line);
  else console.info(line);
  // ③ desktop.log: best effort — a closed gateway swallows this one call,
  // but ①② already hold the evidence.
  void app.ReportFrontendLog?.(feature, level, message, detail ?? "")?.catch(() => {});
}
