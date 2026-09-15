// Session monitor (task 123): the experimental diagnostics board that shows what
// each open tab actually costs - transcript-cache residency, switch stage timings,
// history-skip decisions and cache evictions.
//
// Observation only: nothing in this module changes transcript behaviour. The
// board exists because desktop.log could not answer "did this tab keep its cache
// and where did the three seconds go" (task 123 sections D and F).

import { reportFrontendLog } from "./frontendLog";

export type SessionEviction = {
  at: number;
  tabId: string;
  sessionPath: string;
  /** lru = over the resident-session cap, budget = over the history byte budget. */
  reason: "lru" | "budget";
  records: number;
  bodyBytes: number;
};

export type StageTiming = { at: number; tabId: string; stage: string; ms: number };

export type HydrateDecision = {
  at: number;
  tabId: string;
  sessionPath: string;
  skipHistory: boolean;
  /** Which branch produced the decision, e.g. "preserveCachedHistory" or "reset". */
  reason: string;
};

const STAGE_LIMIT = 120;
const EVICTION_LIMIT = 40;
const SLOW_STAGE_MS = 1000;

const stageTimings: StageTiming[] = [];
const hydrateDecisions = new Map<string, HydrateDecision>();
const evictions: SessionEviction[] = [];

let monitorEnabled = false;
let monitorOpen = false;
const enabledListeners = new Set<(enabled: boolean) => void>();
const openListeners = new Set<(open: boolean) => void>();

/**
 * Records one timed switch stage. Stages at or above SLOW_STAGE_MS also go to
 * desktop.log, so a slow switch can be diagnosed after the fact instead of only
 * being visible while the monitor happens to be open.
 */
export function noteStageTiming(tabId: string, stage: string, ms: number): void {
  stageTimings.push({ at: Date.now(), tabId, stage, ms });
  if (stageTimings.length > STAGE_LIMIT) stageTimings.splice(0, stageTimings.length - STAGE_LIMIT);
  if (ms >= SLOW_STAGE_MS) {
    reportFrontendLog("session-monitor", "slow switch stage", `tab=${tabId} stage=${stage} ms=${Math.round(ms)}`, "warn");
  }
}

/** Records why a tab hydrate did or did not reuse its cached transcript. */
export function noteHydrateDecision(decision: Omit<HydrateDecision, "at">): void {
  hydrateDecisions.set(decision.tabId, { ...decision, at: Date.now() });
  if (hydrateDecisions.size > 64) {
    const oldest = hydrateDecisions.keys().next().value;
    if (oldest !== undefined) hydrateDecisions.delete(oldest);
  }
}

/** Records a transcript-cache eviction (task 123 section F). */
export function noteEviction(event: Omit<SessionEviction, "at">): void {
  evictions.push({ ...event, at: Date.now() });
  if (evictions.length > EVICTION_LIMIT) evictions.splice(0, evictions.length - EVICTION_LIMIT);
  reportFrontendLog(
    "session-monitor",
    "transcript evicted",
    `tab=${event.tabId} reason=${event.reason} records=${event.records} bodyBytes=${event.bodyBytes} session=${event.sessionPath}`,
    "warn",
  );
}

/** Stage timings for one tab, newest last (capped by limit). */
export function stageTimingsFor(tabId: string, limit = 12): StageTiming[] {
  const out: StageTiming[] = [];
  for (const entry of stageTimings) if (entry.tabId === tabId) out.push(entry);
  return out.slice(-limit);
}

/** The slowest recent stage for a tab - the headline number of the board. */
export function slowestStageFor(tabId: string): StageTiming | undefined {
  let worst: StageTiming | undefined;
  for (const entry of stageTimings) {
    if (entry.tabId !== tabId) continue;
    if (!worst || entry.ms > worst.ms) worst = entry;
  }
  return worst;
}

export function hydrateDecisionFor(tabId: string): HydrateDecision | undefined {
  return hydrateDecisions.get(tabId);
}

/** Evictions for one tab, newest last. */
export function evictionsFor(tabId: string, limit = 5): SessionEviction[] {
  const out: SessionEviction[] = [];
  for (const entry of evictions) if (entry.tabId === tabId) out.push(entry);
  return out.slice(-limit);
}

/** Every recorded eviction, newest last (the board's global footer). */
export function recentEvictions(limit = 10): SessionEviction[] {
  return evictions.slice(-limit);
}

/**
 * One structured line per hydrate (task 125): the slowest stage alone does not say
 * where the time went, and the stages are otherwise only visible while the board is
 * open. Format keeps every segment on one greppable line so a slow switch can be
 * reconstructed from desktop.log.
 */
export function reportStageSummary(tabId: string, reason: string): void {
  const prefix = `${reason}:`;
  const stages = stageTimingsFor(tabId, 24).filter((entry) => entry.stage.startsWith(prefix));
  if (stages.length === 0) return;
  const total = stages.find((entry) => entry.stage.endsWith(":total"))?.ms ?? 0;
  const parts = stages
    .map((entry) => `${entry.stage.slice(prefix.length)}=${Math.round(entry.ms)}ms`)
    .join(" ");
  reportFrontendLog("tab-switch", `${reason} summary`, `tab=${tabId} total=${Math.round(total)}ms ${parts}`);
}

// ── stores ─────────────────────────────────────────────────────────────────────

export function isSessionMonitorEnabled(): boolean {
  return monitorEnabled;
}

export function setSessionMonitorEnabled(next: boolean): void {
  if (monitorEnabled === next) return;
  monitorEnabled = next;
  if (!next) setSessionMonitorOpen(false);
  for (const listener of enabledListeners) listener(next);
}

export function onSessionMonitorEnabledChange(cb: (enabled: boolean) => void): () => void {
  enabledListeners.add(cb);
  return () => enabledListeners.delete(cb);
}

export function isSessionMonitorOpen(): boolean {
  return monitorOpen;
}

export function setSessionMonitorOpen(next: boolean): void {
  if (monitorOpen === next) return;
  monitorOpen = next;
  for (const listener of openListeners) listener(next);
}

export function onSessionMonitorOpenChange(cb: (open: boolean) => void): () => void {
  openListeners.add(cb);
  return () => openListeners.delete(cb);
}

/** Test helper: clears every recorded observation. */
export function resetSessionMonitor(): void {
  stageTimings.length = 0;
  hydrateDecisions.clear();
  evictions.length = 0;
}
