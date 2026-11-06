// A small ring buffer of recent app events, attached to crash reports so a stack
// trace arrives with the steps that led to it. Self-contained (no app imports) so
// the crash overlay can read it even when the rest of the app is broken.

export type Breadcrumb = { t: number; cat: string; msg: string };

const MAX = 30;
const ring: Breadcrumb[] = [];
let browserHooksInstalled = false;

export function addBreadcrumb(cat: string, msg: string): void {
  ring.push({ t: Date.now(), cat, msg: msg.length > 200 ? `${msg.slice(0, 200)}…` : msg });
  if (ring.length > MAX) ring.shift();
}

export function snapshotBreadcrumbs(): Breadcrumb[] {
  return ring.map((b) => ({ ...b }));
}

export function dumpBreadcrumbs(): string {
  if (!ring.length) return "";
  const now = Date.now();
  return ring.map((b) => `-${((now - b.t) / 1000).toFixed(1)}s [${b.cat}] ${b.msg}`).join("\n");
}

// ── tl1417 (issue #82 gap 2): hot-path activity lane ─────────────────────────
// The 30-slot ring above is curated on purpose: the bridge proxy only crumbs a
// hand-picked set of low-frequency methods, because one streaming turn would
// otherwise push every console.error out within seconds. That curation is why a
// 33s stretch before a 16s stall carried zero crumbs — the hydrate, the history
// page and the event stream that ran in it are exactly the hot-path calls it
// drops. This lane records those at near-zero cost and privacy by construction:
// kind + name + time only (a bridge method name, an event kind, a DOM event
// type — never arguments, payloads or content), and consecutive repeats
// coalesce into one counted entry so a 400-delta stream or a 1Hz poll holds one
// slot instead of flushing the ring. Timestamps are performance.now() so they
// line up with longTasks.startMs in the performance report.
export type ActivityKind = "bridge" | "bridge.done" | "bridge.error" | "stream" | "ui";
export type ActivityEntry = { t: number; last: number; kind: ActivityKind; name: string; n: number };

const ACTIVITY_MAX = 48;
// Coalesce against the last two entries, not just the last one: a poll loop
// alternates call/done and a stream interleaves deltas with tool events; both
// must stay one entry per name instead of one entry per occurrence.
const ACTIVITY_COALESCE_DEPTH = 2;
const ACTIVITY_NAME_RE = /^[A-Za-z0-9._:-]{1,64}$/;
const activity: ActivityEntry[] = [];

function activityNow(): number {
  return typeof performance !== "undefined" ? performance.now() : Date.now();
}

export function noteActivity(kind: ActivityKind, name: string): void {
  const now = activityNow();
  for (let i = activity.length - 1, stop = Math.max(0, activity.length - ACTIVITY_COALESCE_DEPTH); i >= stop; i -= 1) {
    const entry = activity[i];
    if (entry.kind === kind && entry.name === name) {
      entry.n += 1;
      entry.last = now;
      return;
    }
  }
  activity.push({ t: now, last: now, kind, name, n: 1 });
  if (activity.length > ACTIVITY_MAX) activity.shift();
}

// Snapshot for the performance report. Names are code identifiers by
// construction; the allowlist is the belt to that brace — anything that is not a
// plain token (a path, a sentence, a value) collapses to "other" before it can
// reach either outlet.
export function snapshotActivity(): ActivityEntry[] {
  return activity.map((e) => ({ ...e, name: ACTIVITY_NAME_RE.test(e.name) ? e.name : "other" }));
}

/** Test seam: the lane is module state. */
export function resetActivityForTest(): void {
  activity.length = 0;
}

export function activityCapacity(): number {
  return ACTIVITY_MAX;
}

function stringifyArg(a: unknown): string {
  if (typeof a === "string") return a;
  if (a instanceof Error) return a.message;
  try {
    return JSON.stringify(a);
  } catch {
    return String(a);
  }
}

export function installBreadcrumbConsoleHook(): void {
  if (!browserHooksInstalled) {
    browserHooksInstalled = true;
    addBreadcrumb("app", "start");
    if (typeof window !== "undefined") {
      window.addEventListener("online", () => addBreadcrumb("network", "online"));
      window.addEventListener("offline", () => addBreadcrumb("network", "offline"));
      document.addEventListener("visibilitychange", () => addBreadcrumb("view", document.visibilityState));
      // tl1417: interaction taps for the activity lane — event type only, capture
      // + passive so they can neither see nor delay the app's own handlers.
      for (const type of ["pointerdown", "keydown", "wheel"] as const) {
        document.addEventListener(type, () => noteActivity("ui", type), { capture: true, passive: true });
      }
    }
  }
  for (const level of ["error", "warn"] as const) {
    const orig = console[level].bind(console);
    console[level] = (...args: unknown[]) => {
      addBreadcrumb(`console.${level}`, args.map(stringifyArg).join(" "));
      orig(...args);
    };
  }
}
