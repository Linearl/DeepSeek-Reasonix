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

// Task 125 render-side: geometry estimation is called once per row and does not
// know its tab, so it accumulates into a short-lived frame window. The window
// opens when a surface starts painting and closes on first frame, which keeps
// the hot path to a boolean check when no measurement is active.
let geometryFrameActive = false;
let geometryFrameMs = 0;
let surfaceFrameStart: { key: string; at: number; geometryAtMs: number } | null = null;

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
  // Task 151 (A-level prerequisite): the decision used to live only in this
  // in-memory map, which made "why did this switch re-load history"
  // undiagnosable after the fact (the 2026-09-16 switch-tab investigation
  // stalled exactly here). Log the non-reuse branch — the diagnostic signal —
  // while cache hits stay monitor-only to keep desktop.log quiet.
  if (!decision.skipHistory) {
    reportFrontendLog(
      "session-monitor",
      "hydrate reloaded history",
      `tab=${decision.tabId} reason=${decision.reason} path=${decision.sessionPath}`,
      "info",
    );
  }
}

/**
 * Opens a geometry-measurement window for one surface paint (task 125).
 * Subsequent estimateTranscriptRowGeometry calls accumulate into it until
 * consumeGeometryFrame. A no-op when no frame is being measured keeps the
 * estimator hot path free of Date/Number work.
 */
export function beginGeometryFrame(): void {
  geometryFrameActive = true;
  geometryFrameMs = 0;
}

/** True while a geometry-measurement window is open. */
export function isGeometryFrameOpen(): boolean {
  return geometryFrameActive;
}

/** Adds one estimator sample. Cheap no-op outside an open geometry frame. */
export function noteGeometrySample(ms: number): void {
  if (geometryFrameActive) geometryFrameMs += ms;
}

/** Closes the geometry window and returns the accumulated milliseconds. */
export function consumeGeometryFrame(): number {
  geometryFrameActive = false;
  const ms = geometryFrameMs;
  geometryFrameMs = 0;
  return ms;
}

/**
 * Starts the transcript first-frame clock (task 125). The key is the surface
 * identity (geometry session key), so a remount of the same surface restarts
 * the clock rather than leaking a stale start into the next paint.
 */
export function beginSurfaceFrame(surfaceKey: string): void {
  surfaceFrameStart = { key: surfaceKey, at: performance.now(), geometryAtMs: 0 };
  beginGeometryFrame();
}

/**
 * Records first-frame and geometry-measure stage timings for a tab, then
 * writes one greppable log line. Returns null when no matching frame was open
 * (startup without a prior begin, or a superseded surface).
 */
export function completeSurfaceFrame(tabId: string, surfaceKey: string): { firstFrameMs: number; geometryMs: number } | null {
  if (!surfaceFrameStart || surfaceFrameStart.key !== surfaceKey) return null;
  const firstFrameMs = performance.now() - surfaceFrameStart.at;
  const geometryMs = consumeGeometryFrame();
  surfaceFrameStart = null;
  noteStageTiming(tabId, "transcript:first-frame", firstFrameMs);
  noteStageTiming(tabId, "transcript:geometry-measure", geometryMs);
  reportFrontendLog(
    "session-monitor",
    "transcript first frame",
    `tab=${tabId} firstFrame=${Math.round(firstFrameMs)}ms geometry=${Math.round(geometryMs)}ms`,
  );
  return { firstFrameMs, geometryMs };
}

/** Latest first-frame / geometry-measure pair for one tab. */
export function renderMetricsFor(tabId: string): { firstFrameMs?: number; geometryMs?: number } {
  let firstFrameMs: number | undefined;
  let geometryMs: number | undefined;
  for (const entry of stageTimings) {
    if (entry.tabId !== tabId) continue;
    if (entry.stage === "transcript:first-frame") firstFrameMs = entry.ms;
    if (entry.stage === "transcript:geometry-measure") geometryMs = entry.ms;
  }
  return { firstFrameMs, geometryMs };
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

/**
 * Task 151: the stage window of the MOST RECENT switch for a tab — from the
 * newest entry back to (and including) its `:total` marker. Without this
 * window the board's headline mixed every past switch of the tab, so a
 * stale 5-6 s reload kept masking today's fast cached switches
 * (2026-09-17 screenshot evidence: 辣椒识别2 showed total 5156 ms while every
 * live segment was under 300 ms).
 */
export function latestSwitchStages(tabId: string, cap = 24): StageTiming[] {
  const all: StageTiming[] = [];
  for (const entry of stageTimings) if (entry.tabId === tabId) all.push(entry);
  const recent = all.slice(-cap);
  let start = recent.length;
  for (let i = recent.length - 1; i >= 0; i--) {
    start = i;
    if (recent[i].stage.endsWith(":total")) break;
  }
  return start < recent.length ? recent.slice(start) : [];
}

/** The slowest stage of the MOST RECENT switch for a tab. */
export function slowestStageFor(tabId: string): StageTiming | undefined {
  let worst: StageTiming | undefined;
  for (const entry of latestSwitchStages(tabId)) {
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
  // Task 151: only the latest switch's stages — stageTimingsFor pulled up to
  // 24 records spanning SEVERAL switches, which concatenated multiple
  // meta/ancillary segments into one summary line and made desktop.log
  // summaries unreadable (2026-09-17 evidence: one line carried three
  // meta/ancillary sequences).
  const stages = latestSwitchStages(tabId, 24).filter((entry) => entry.stage.startsWith(prefix));
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
  // DIAG (task 123): separates "click never reached the store" from "store
  // changed but nothing was listening/rendering". listeners= is the number of
  // mounted subscribers, so 0 explains a silent no-op. Remove once the panel
  // is confirmed working.
  if (monitorOpen === next) {
    reportFrontendLog("session-monitor", "open request ignored", `requested=${next} current=${monitorOpen} listeners=${openListeners.size}`);
    return;
  }
  monitorOpen = next;
  reportFrontendLog("session-monitor", "open changed", `open=${next} listeners=${openListeners.size}`);
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
  geometryFrameActive = false;
  geometryFrameMs = 0;
  surfaceFrameStart = null;
}
