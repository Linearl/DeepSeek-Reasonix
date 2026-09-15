// Task 123: the observation layer behind the session-monitor board. These are the
// numbers the board shows, so they have to be exact: per-tab filtering, the
// slowest-stage pick, latest-decision-wins, and the opt-in gating.
import assert from "node:assert/strict";
import {
  beginGeometryFrame,
  beginSurfaceFrame,
  completeSurfaceFrame,
  consumeGeometryFrame,
  evictionsFor,
  hydrateDecisionFor,
  isGeometryFrameOpen,
  isSessionMonitorEnabled,
  isSessionMonitorOpen,
  noteEviction,
  noteGeometrySample,
  noteHydrateDecision,
  noteStageTiming,
  onSessionMonitorOpenChange,
  recentEvictions,
  renderMetricsFor,
  resetSessionMonitor,
  setSessionMonitorEnabled,
  setSessionMonitorOpen,
  slowestStageFor,
  stageTimingsFor,
} from "../lib/sessionMonitor";

let passed = 0;
function check(condition: boolean, label: string): void {
  if (!condition) {
    console.error(`  FAIL  ${label}`);
    process.exitCode = 1;
    return;
  }
  passed += 1;
  console.log(`  PASS  ${label}`);
}

resetSessionMonitor();

// ── stage timings ─────────────────────────────────────────────────────────────
noteStageTiming("tab-a", "switch-tab:history reconcile", 3075);
noteStageTiming("tab-a", "switch-tab:meta", 120);
noteStageTiming("tab-b", "switch-tab:history", 240);

const aStages = stageTimingsFor("tab-a");
check(aStages.length === 2, "stage timings are filtered to the tab");
check(aStages.every((entry) => entry.tabId === "tab-a"), "no foreign tab leaks into the tab slice");
check(slowestStageFor("tab-a")?.stage === "switch-tab:history reconcile", "slowest stage wins, not the newest");
check((slowestStageFor("tab-a")?.ms ?? 0) === 3075, "slowest stage keeps its duration");
check(slowestStageFor("tab-b")?.stage === "switch-tab:history", "each tab has its own slowest stage");
check(stageTimingsFor("tab-missing").length === 0, "an unknown tab reports nothing");
check(stageTimingsFor("tab-a", 1).length === 1, "the limit caps the returned slice");

// ── hydrate decisions ─────────────────────────────────────────────────────────
noteHydrateDecision({ tabId: "tab-a", sessionPath: "/s/a.jsonl", skipHistory: true, reason: "preserveCachedHistory" });
check(hydrateDecisionFor("tab-a")?.skipHistory === true, "skip decisions are recorded");
check(hydrateDecisionFor("tab-a")?.reason === "preserveCachedHistory", "the deciding branch is recorded");
noteHydrateDecision({ tabId: "tab-a", sessionPath: "/s/a.jsonl", skipHistory: false, reason: "reset" });
check(hydrateDecisionFor("tab-a")?.reason === "reset", "the latest decision wins");
check(hydrateDecisionFor("tab-missing") === undefined, "an unknown tab has no decision");

// ── evictions ─────────────────────────────────────────────────────────────────
noteEviction({ tabId: "tab-a", sessionPath: "/s/a.jsonl", reason: "lru", records: 900, bodyBytes: 12 * 1024 * 1024 });
noteEviction({ tabId: "tab-b", sessionPath: "/s/b.jsonl", reason: "budget", records: 40, bodyBytes: 4 * 1024 * 1024 });
noteEviction({ tabId: "tab-a", sessionPath: "/s/a.jsonl", reason: "budget", records: 300, bodyBytes: 2 * 1024 * 1024 });

check(evictionsFor("tab-a").length === 2, "evictions are filtered to the tab");
check(evictionsFor("tab-a")[0]?.reason === "lru", "evictions keep their order");
check(evictionsFor("tab-b").length === 1, "a second tab keeps its own evictions");
check(recentEvictions().length === 3, "the global log keeps every eviction");
check(recentEvictions(1)[0]?.tabId === "tab-a", "the global log is newest-last");
check((evictionsFor("tab-a")[1]?.bodyBytes ?? 0) === 2 * 1024 * 1024, "the freed byte count is recorded");

// ── opt-in gating ─────────────────────────────────────────────────────────────
check(isSessionMonitorEnabled() === false, "the board ships off");
check(isSessionMonitorOpen() === false, "the panel ships closed");

// The panel follows the switch: opening it while enabled, then disabling the
// experiment must close the panel rather than leave an orphan floating surface.
setSessionMonitorEnabled(true);
check(isSessionMonitorEnabled() === true, "enabling the experiment flips the gate");
setSessionMonitorOpen(true);
check(isSessionMonitorOpen() === true, "the panel can be opened while enabled");
setSessionMonitorEnabled(false);
check(isSessionMonitorOpen() === false, "disabling the experiment closes the panel");
setSessionMonitorEnabled(false);
check(isSessionMonitorEnabled() === false, "re-disabling is a no-op");
setSessionMonitorOpen(false);
check(isSessionMonitorOpen() === false, "closing a closed panel is a no-op");

// The left-rail button subscribes to the open state; an unsubscribed listener
// must stop firing so a remount cannot stack callbacks.
const openEvents: boolean[] = [];
const offOpen = onSessionMonitorOpenChange((open) => openEvents.push(open));
setSessionMonitorOpen(true);
setSessionMonitorOpen(false);
offOpen();
setSessionMonitorOpen(true);
check(openEvents.length === 2, "open changes notify the subscriber");
check(openEvents[0] === true && openEvents[1] === false, "notifications carry the new state");
setSessionMonitorOpen(false);

resetSessionMonitor();
check(stageTimingsFor("tab-a").length === 0, "reset clears the recorded stages");
check(recentEvictions().length === 0, "reset clears the eviction log");
check(hydrateDecisionFor("tab-a") === undefined, "reset clears the decisions");

// ── task 125: geometry accumulator + first frame ─────────────────────────────
check(isGeometryFrameOpen() === false, "geometry frame starts closed");
noteGeometrySample(5);
beginGeometryFrame();
check(isGeometryFrameOpen() === true, "beginGeometryFrame opens the window");
noteGeometrySample(12);
noteGeometrySample(8);
check(consumeGeometryFrame() === 20, "geometry samples accumulate inside the window");
check(isGeometryFrameOpen() === false, "consumeGeometryFrame closes the window");
noteGeometrySample(99);
beginGeometryFrame();
check(consumeGeometryFrame() === 0, "samples outside a window are discarded");

check(completeSurfaceFrame("tab-a", "surface-x") === null, "completing without a matching begin is a no-op");
beginSurfaceFrame("surface-x");
check(completeSurfaceFrame("tab-a", "surface-y") === null, "a mismatched surface key does not complete");
beginSurfaceFrame("surface-x");
noteGeometrySample(3);
const frame = completeSurfaceFrame("tab-a", "surface-x");
check(frame !== null && frame.geometryMs === 3, "completeSurfaceFrame reports geometry ms");
check(frame !== null && frame.firstFrameMs >= 0, "completeSurfaceFrame reports first-frame ms");
const metrics = renderMetricsFor("tab-a");
check(metrics.firstFrameMs !== undefined, "renderMetricsFor exposes first-frame");
check(metrics.geometryMs === 3, "renderMetricsFor exposes geometry measure");
check(renderMetricsFor("tab-missing").firstFrameMs === undefined, "an unknown tab has no render metrics");
check(stageTimingsFor("tab-a").some((entry) => entry.stage === "transcript:first-frame"), "first-frame is staged for the board");
check(stageTimingsFor("tab-a").some((entry) => entry.stage === "transcript:geometry-measure"), "geometry-measure is staged for the board");

resetSessionMonitor();
check(isGeometryFrameOpen() === false, "reset closes any open geometry frame");
check(renderMetricsFor("tab-a").firstFrameMs === undefined, "reset clears render metrics");

console.log(`\n${passed} passed${process.exitCode ? ", with failures" : ""}`);
if (process.exitCode) process.exit(1);
