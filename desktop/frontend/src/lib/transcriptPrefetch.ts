// transcriptPrefetch — task 123 (S5): warm the transcript store for a tab the
// user is about to switch to, so the first switch to it serves the page from
// memory instead of paying a backend read.
//
// Scope discipline: this only feeds the app-level transcript store
// (getTranscriptStore().loadLatest). It deliberately does NOT run the hydrate
// pipeline (loadSessionDataForTab) — that would dispatch tab state and fire four
// ancillary backend calls for a tab nobody is looking at yet. The switch path
// then finds the page resident through preserveCachedHistory (useController's
// switchTab consults store residency for exactly this reason) and renders it
// without a fetch.
//
// Budget guard: prefetching must never push the store over its byte ceiling,
// because evicting a resident session to make room for a speculative one makes
// the common case slower. Every entry point checks the live store stats first.

import { reportFrontendLog } from "./frontendLog";
import { effectiveMaxResidentSessions } from "./resourceBudgets";
import { getTranscriptStore } from "./transcriptStore";

/** The newest-page turn budget. Must stay equal to HISTORY_PAGE_TURNS in
 * lib/historyPaging.ts (and useController's copy of it): a warmed page is only
 * useful if it is the page the switch would have requested. */
const PREFETCH_PAGE_TURNS = 60;

export type PrefetchCandidate = {
  tabId: string;
  sessionPath: string;
  revision?: number;
  digest?: string;
};

/** How many tabs one switch may warm. More than two rarely pays off: switching
 * is a two-tab motion (leave, arrive) plus at most one return. */
export const PREFETCH_MAX_PER_TRIGGER = 2;

/** A tab already warmed stays warm for a while; re-reading it on every switch
 * would turn a background nicety into steady I/O. */
const PREFETCH_COOLDOWN_MS = 30_000;

/** Refuse when the store cannot absorb even a modest page without evicting a
 * resident session. The store's own budget is the real ceiling (192 MiB by
 * default, user-tunable); this is only the headroom floor for a speculative
 * read. */
const PREFETCH_MIN_HEADROOM_BYTES = 8 << 20;

const prefetchedAt = new Map<string, number>();
const inFlight = new Set<string>();

function prefetchKey(candidate: PrefetchCandidate): string {
  return `${candidate.tabId}\u0000${candidate.sessionPath}`;
}

function prefetchBudgetAllows(residentSessions: number): boolean {
  const stats = getTranscriptStore().stats();
  const budget = stats.bodyBudgetBytes || 0;
  if (budget <= 0) return false;
  if (budget - stats.bodyBytes < PREFETCH_MIN_HEADROOM_BYTES) return false;
  const residentLimit = effectiveMaxResidentSessions();
  return !(residentLimit > 0 && residentSessions >= residentLimit);
}

/**
 * Warms one tab's newest transcript page. Safe to call repeatedly: it coalesces
 * in-flight work, honours a cooldown, and silently gives up when the store is
 * under budget pressure or the page cannot be read.
 */
export function prefetchTabTranscript(candidate: PrefetchCandidate, options: { force?: boolean } = {}): void {
  const sessionPath = (candidate.sessionPath ?? "").trim();
  if (!candidate.tabId || !sessionPath) return;
  const key = prefetchKey(candidate);
  if (inFlight.has(key)) return;
  if (!options.force) {
    const last = prefetchedAt.get(key);
    if (last !== undefined && Date.now() - last < PREFETCH_COOLDOWN_MS) return;
  }
  const store = getTranscriptStore();
  const alreadyResident = store.isResident(candidate.tabId, sessionPath);
  if (!prefetchBudgetAllows(store.stats().residentSessions)) {
    reportFrontendLog("history-paging", "history prefetch skipped", `tab=${candidate.tabId} reason=budget`, "info");
    return;
  }
  inFlight.add(key);
  const startedAt = Date.now();
  void store
    .loadLatest(candidate.tabId, sessionPath, {
      turns: PREFETCH_PAGE_TURNS,
      // The point of the exercise is to populate the store; serving a resident
      // page here would report a hit we did not create.
      preferResident: false,
      expectedRevision: candidate.revision,
      expectedDigest: candidate.digest,
    })
    .then((projection) => {
      prefetchedAt.set(key, Date.now());
      reportFrontendLog(
        "history-paging",
        "history prefetched",
        `tab=${candidate.tabId} entries=${projection?.items.length ?? 0} residentBefore=${alreadyResident} ms=${Date.now() - startedAt}`,
        "info",
      );
    })
    .catch(() => {
      // A failed warm-up is not an error the user needs to see: the switch that
      // follows will fetch normally.
    })
    .finally(() => {
      inFlight.delete(key);
    });
}

/**
 * Warms the first `limit` candidates (already ordered most-recently-used first).
 * Each candidate is independent; one giving up does not stop the others.
 */
export function prefetchMruTabs(candidates: PrefetchCandidate[], limit = PREFETCH_MAX_PER_TRIGGER): void {
  let started = 0;
  for (const candidate of candidates) {
    if (started >= limit) return;
    if (!candidate?.tabId || !(candidate.sessionPath ?? "").trim()) continue;
    const key = prefetchKey(candidate);
    if (inFlight.has(key)) continue;
    const last = prefetchedAt.get(key);
    if (last !== undefined && Date.now() - last < PREFETCH_COOLDOWN_MS) continue;
    started += 1;
    prefetchTabTranscript(candidate);
  }
}

/** Test seam: forget every cooldown/in-flight marker. */
export function resetPrefetchState(): void {
  prefetchedAt.clear();
  inFlight.clear();
}
