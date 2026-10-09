// Last-resort crash surface: a React render error with no boundary unmounts the
// whole tree (blank window), and global errors/rejections leave no trace either.

import { addBreadcrumb, dumpBreadcrumbs, snapshotBreadcrumbs, type Breadcrumb } from "./breadcrumbs";
import { writeClipboardText } from "./clipboard";
import { app } from "./bridge";
import { t } from "./i18n";
import { sessionPipelineDiagnostics, type SessionPipelineDiagnostics } from "./sessionDiagnostics";
import { isWailsRuntimeOnlyCrashEvent } from "./wailsRuntimeCrash";
import { buildCrashIssueSkeleton } from "./crashIssue";
import type { CrashAnalysisAvailabilityReport } from "./types";
declare const __BUILD_COMMIT__: string;
declare const __BUILD_CHANNEL__: string;

export type CrashKind = "crash" | "exception" | "feedback" | "performance" | "bot";

export type PerformanceSnapshot = {
  reason: string;
  uptimeMs: number;
  visibility: string;
  focused: boolean;
  online: boolean;
  hardwareConcurrency: number;
  deviceMemoryGb?: number;
  jsHeap?: {
    usedMb: number;
    totalMb: number;
    limitMb: number;
    usagePercent?: number;
  };
  eventLoopLag?: {
    currentMs: number;
    maxMs: number;
    avgMs: number;
    samples: number;
  };
  longTasks?: {
    count: number;
    totalMs: number;
    maxMs: number;
    recent: { startMs: number; durationMs: number; attribution?: string }[];
  };
  longTaskFrames?: { label: string; samples: number }[];
  connection?: {
    effectiveType?: string;
    downlinkMbps?: number;
    rttMs?: number;
    saveData?: boolean;
  };
  // Session-switch/history pipeline diagnostics (Phase F): last activation
  // timings, last HistorySlice page stats with index hit/miss, virtual mounted
  // rows, markdown worker counters, transcript cache weights. All optional —
  // absent before the first switch/page or when a provider never registered.
  sessionPipeline?: SessionPipelineDiagnostics;
};

export type CrashPayload = {
  schemaVersion: 2;
  source: "frontend" | "frontend.react" | "frontend.global" | "frontend.performance" | "frontend.mock" | "bot.runtime";
  kind: CrashKind;
  label: string;
  message: string;
  errorType: string;
  errorMessage: string;
  stack?: string;
  componentStack?: string;
  topFrame?: string;
  // Optional, non-display grouping context for otherwise opaque WebView errors.
  // It is deliberately restricted to build/view/breadcrumb categories and never
  // contains breadcrumb messages, tab IDs, paths, or user content.
  fingerprintHint?: string;
  // Task 642: lab-simulated reports only. The flag travels the whole real
  // pipeline (own channel / pending queue / issue skeleton / analysis
  // instruction) so the receiving end can tell the drill from a real failure.
  testMock?: boolean;
  buildCommit: string;
  channel: string;
  language: string;
  view: string;
  breadcrumbs: Breadcrumb[];
  occurredAt: string;
};

type NormalizedError = {
  errorType: string;
  errorMessage: string;
  stack?: string;
};

type LongTaskSample = {
  startMs: number;
  durationMs: number;
  attribution?: string;
};

// WICG JS Self-Profiling API (https://wicg.github.io/js-self-profiling/), available
// in Chromium WebViews when the document is served with `Document-Policy: js-profiling`.
export type ProfilerTrace = {
  resources?: string[];
  frames?: { name?: string; resourceId?: number; line?: number; column?: number }[];
  stacks?: { frameId: number; parentId?: number }[];
  samples?: { timestamp: number; stackId?: number }[];
};

type ProfilerLike = {
  stop(): Promise<ProfilerTrace>;
  addEventListener?: (type: string, listener: () => void) => void;
};

type ProfilerConstructor = new (options: { sampleInterval: number; maxBufferSize: number }) => ProfilerLike;

type BrowserPerformanceMemory = {
  usedJSHeapSize?: number;
  totalJSHeapSize?: number;
  jsHeapSizeLimit?: number;
};

type BrowserNavigator = Navigator & {
  deviceMemory?: number;
  connection?: {
    effectiveType?: string;
    downlink?: number;
    rtt?: number;
    saveData?: boolean;
  };
};

const LONG_TASK_WINDOW_MS = 60_000;
const LONG_TASK_PROMPT_MS = 800;
// Streaming renders routinely accumulate ~1.5s of 70-240ms tasks per minute without
// user-visible jank, so the cumulative prompt only fires past half of that budget spent blocked.
const LONG_TASK_TOTAL_PROMPT_MS = 3_000;
const EVENT_LOOP_LAG_PROMPT_MS = 1_200;
const EVENT_LOOP_LAG_CONSECUTIVE_SAMPLES = 2;
const STARTUP_GRACE_MS = 15_000;
const PROMPT_COOLDOWN_MS = 10 * 60_000;
const MAX_LAG_SAMPLES = 60;
const VISIBILITY_RESUME_GRACE_MS = 5_000;

const longTasks: LongTaskSample[] = [];
const lagSamples: number[] = [];
let performanceMonitorInstalled = false;
let lastPerformancePromptAt = 0;

// Rolling self-profiling sampler (Chromium WebViews only; requires the asset server
// to send `Document-Policy: js-profiling`, see jsProfilingMiddleware on the Go side).
// ~10ms native sampling; the buffer covers the same 60s window as longTasks.
const PROFILER_SAMPLE_INTERVAL_MS = 10;
const PROFILER_MAX_BUFFER_SAMPLES = LONG_TASK_WINDOW_MS / PROFILER_SAMPLE_INTERVAL_MS;
let activeProfiler: ProfilerLike | null = null;

function startLongTaskProfiler(): void {
  const ProfilerCtor = (globalThis as { Profiler?: ProfilerConstructor }).Profiler;
  if (!ProfilerCtor) return;
  try {
    const profiler = new ProfilerCtor({
      sampleInterval: PROFILER_SAMPLE_INTERVAL_MS,
      maxBufferSize: PROFILER_MAX_BUFFER_SAMPLES,
    });
    // A full buffer stops sampling silently; drop the stale trace and roll over.
    profiler.addEventListener?.("samplebufferfull", () => {
      if (activeProfiler !== profiler) return;
      activeProfiler = null;
      void profiler.stop().catch(() => {});
      startLongTaskProfiler();
    });
    activeProfiler = profiler;
  } catch {
    // Document policy missing or the API is disabled in this WebView.
    activeProfiler = null;
  }
}

async function collectLongTaskFrames(
  windows: { startMs: number; durationMs: number }[],
): Promise<{ label: string; samples: number }[]> {
  const profiler = activeProfiler;
  if (!profiler) return [];
  activeProfiler = null;
  try {
    const trace = await profiler.stop();
    return aggregateLongTaskProfile(trace, windows);
  } catch {
    return [];
  } finally {
    startLongTaskProfiler();
  }
}

const PERF_REPORTED_STORAGE_KEY = "reasonix:perf-reported";

// Idempotent per pressure label: once a category is reported (persisted per build) or
// dismissed (session only), stop re-surfacing it so a steady slowdown can't spam prompts.
const dismissedPerfLabels = new Set<string>();
let reportedPerfLabels: Set<string> | null = null;

function currentBuildCommit(): string {
  return typeof __BUILD_COMMIT__ === "string" ? __BUILD_COMMIT__ : "dev";
}

export function parseReportedPerf(raw: string | null, build: string): Set<string> {
  if (!raw) return new Set();
  try {
    const parsed = JSON.parse(raw) as { build?: string; labels?: unknown };
    if (parsed.build !== build || !Array.isArray(parsed.labels)) return new Set();
    return new Set(parsed.labels.filter((label): label is string => typeof label === "string"));
  } catch {
    return new Set();
  }
}

export function serializeReportedPerf(labels: ReadonlySet<string>, build: string): string {
  return JSON.stringify({ build, labels: [...labels] });
}

function getReportedPerfLabels(): Set<string> {
  if (reportedPerfLabels) return reportedPerfLabels;
  let raw: string | null = null;
  try {
    raw = typeof localStorage !== "undefined" ? localStorage.getItem(PERF_REPORTED_STORAGE_KEY) : null;
  } catch {
    raw = null;
  }
  reportedPerfLabels = parseReportedPerf(raw, currentBuildCommit());
  return reportedPerfLabels;
}

function markPerfReported(label: string): void {
  const set = getReportedPerfLabels();
  if (set.has(label)) return;
  set.add(label);
  try {
    if (typeof localStorage !== "undefined") {
      localStorage.setItem(PERF_REPORTED_STORAGE_KEY, serializeReportedPerf(set, currentBuildCommit()));
    }
  } catch {
    // localStorage can throw (private mode / quota); the session-level set still dedups.
  }
}

function clip(s: string, n: number): string {
  return s.length > n ? s.slice(0, n) : s;
}

function safeStringify(value: unknown): string {
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}

export function normalizeCrashError(err: unknown): NormalizedError {
  if (err instanceof Error) {
    return {
      errorType: err.name || "Error",
      errorMessage: err.message || String(err),
      stack: err.stack,
    };
  }
  if (typeof err === "string") {
    return { errorType: "string", errorMessage: err };
  }
  if (err && typeof err === "object") {
    const obj = err as { name?: unknown; message?: unknown; stack?: unknown; constructor?: { name?: string } };
    const errorType = typeof obj.name === "string" && obj.name ? obj.name : obj.constructor?.name || "object";
    const errorMessage =
      typeof obj.message === "string" && obj.message ? obj.message : clip(safeStringify(err), 1000);
    return {
      errorType,
      errorMessage,
      stack: typeof obj.stack === "string" ? obj.stack : undefined,
    };
  }
  return { errorType: typeof err, errorMessage: String(err) };
}

export function topFrameFromStack(stack?: string): string {
  if (!stack) return "";
  const lines = stack
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean);
  return lines.find((l) => /\b(src|assets|wails|frontend)\b|\.tsx?:|\.jsx?:/.test(l)) ?? lines[1] ?? lines[0] ?? "";
}

function currentView(): string {
  if (typeof window === "undefined") return "";
  const { protocol, host, pathname, hash } = window.location;
  const safeHash = hash && hash.length < 80 ? hash : "";
  return clip(`${protocol}//${host}${pathname}${safeHash}`, 180);
}

function kindForLabel(label: string): CrashKind {
  return label === "unhandledrejection" ? "exception" : "crash";
}

function sourceForLabel(label: string): CrashPayload["source"] {
  if (label === "react") return "frontend.react";
  if (label === "window.error" || label === "unhandledrejection") return "frontend.global";
  return "frontend";
}

function formatText(label: string, normalized: NormalizedError, extra?: string): string {
  const detail = normalized.stack || normalized.errorMessage;
  const crumbs = dumpBreadcrumbs();
  const buildCommit = typeof __BUILD_COMMIT__ === "string" ? __BUILD_COMMIT__ : "dev";
  return [`[${label}]`, detail, extra?.trim(), crumbs && `--- breadcrumbs ---\n${crumbs}`, `build ${buildCommit}`]
    .filter(Boolean)
    .join("\n\n");
}

function fmtNumber(n: number, digits = 0): string {
  return Number.isFinite(n) ? n.toFixed(digits) : "0";
}

function fmtMb(n: number): string {
  return `${fmtNumber(n, 1)} MB`;
}

function readHeapSnapshot(): PerformanceSnapshot["jsHeap"] | undefined {
  if (typeof performance === "undefined") return undefined;
  const memory = (performance as Performance & { memory?: BrowserPerformanceMemory }).memory;
  if (!memory?.usedJSHeapSize || !memory.totalJSHeapSize || !memory.jsHeapSizeLimit) return undefined;
  const usedMb = memory.usedJSHeapSize / 1024 / 1024;
  const totalMb = memory.totalJSHeapSize / 1024 / 1024;
  const limitMb = memory.jsHeapSizeLimit / 1024 / 1024;
  return {
    usedMb,
    totalMb,
    limitMb,
    usagePercent: limitMb > 0 ? (usedMb / limitMb) * 100 : undefined,
  };
}

function pruneLongTasks(now = performance.now()): void {
  while (longTasks.length && now - longTasks[0].startMs > LONG_TASK_WINDOW_MS) longTasks.shift();
}

function longTaskSummary(now = performance.now()): PerformanceSnapshot["longTasks"] {
  pruneLongTasks(now);
  if (!longTasks.length) return undefined;
  const totalMs = longTasks.reduce((sum, t) => sum + t.durationMs, 0);
  const maxMs = Math.max(...longTasks.map((t) => t.durationMs));
  return {
    count: longTasks.length,
    totalMs,
    maxMs,
    recent: longTasks.slice(-5),
  };
}

function eventLoopLagSummary(currentMs = 0): PerformanceSnapshot["eventLoopLag"] {
  const samples = lagSamples.filter((n) => n > 0);
  if (!samples.length && currentMs <= 0) return undefined;
  const all = currentMs > 0 ? [...samples, currentMs] : samples;
  const total = all.reduce((sum, n) => sum + n, 0);
  return {
    currentMs,
    maxMs: Math.max(...all),
    avgMs: total / all.length,
    samples: all.length,
  };
}

function networkSnapshot(): PerformanceSnapshot["connection"] {
  if (typeof navigator === "undefined") return undefined;
  const connection = (navigator as BrowserNavigator).connection;
  if (!connection) return undefined;
  return {
    effectiveType: connection.effectiveType,
    downlinkMbps: connection.downlink,
    rttMs: connection.rtt,
    saveData: connection.saveData,
  };
}

function performanceSnapshot(reason: string, currentLagMs = 0): PerformanceSnapshot {
  const nav = typeof navigator === "undefined" ? undefined : (navigator as BrowserNavigator);
  const doc = typeof document === "undefined" ? undefined : document;
  const pipeline = sessionPipelineDiagnostics();
  return {
    reason,
    uptimeMs: typeof performance !== "undefined" ? performance.now() : 0,
    visibility: doc?.visibilityState ?? "",
    focused: doc?.hasFocus?.() ?? false,
    online: nav?.onLine ?? true,
    hardwareConcurrency: nav?.hardwareConcurrency ?? 0,
    deviceMemoryGb: nav?.deviceMemory,
    jsHeap: readHeapSnapshot(),
    eventLoopLag: eventLoopLagSummary(currentLagMs),
    longTasks: typeof performance !== "undefined" ? longTaskSummary() : undefined,
    connection: networkSnapshot(),
    sessionPipeline: Object.keys(pipeline).length > 0 ? pipeline : undefined,
  };
}

export function formatPerformanceContext(snapshot: PerformanceSnapshot): string {
  const lines = [
    `reason: ${snapshot.reason}`,
    `uptime: ${fmtNumber(snapshot.uptimeMs / 1000, 1)}s`,
    `visibility: ${snapshot.visibility || "unknown"}`,
    `focused: ${snapshot.focused ? "true" : "false"}`,
    `online: ${snapshot.online ? "true" : "false"}`,
    `hardware concurrency: ${snapshot.hardwareConcurrency || "unknown"}`,
  ];
  if (snapshot.deviceMemoryGb) lines.push(`device memory: ${snapshot.deviceMemoryGb} GB`);
  if (snapshot.jsHeap) {
    const pct =
      snapshot.jsHeap.usagePercent !== undefined ? `, ${fmtNumber(snapshot.jsHeap.usagePercent)}% of limit` : "";
    lines.push(
      `js heap: ${fmtMb(snapshot.jsHeap.usedMb)} used, ${fmtMb(snapshot.jsHeap.totalMb)} allocated, ${fmtMb(snapshot.jsHeap.limitMb)} limit${pct}`,
    );
  }
  if (snapshot.eventLoopLag) {
    lines.push(
      `event loop lag: current ${fmtNumber(snapshot.eventLoopLag.currentMs)}ms, max ${fmtNumber(snapshot.eventLoopLag.maxMs)}ms, avg ${fmtNumber(snapshot.eventLoopLag.avgMs)}ms over ${snapshot.eventLoopLag.samples} samples`,
    );
  }
  if (snapshot.longTasks) {
    const recent = snapshot.longTasks.recent
      .map(
        (t) =>
          `${fmtNumber(t.durationMs)}ms @ ${fmtNumber(t.startMs / 1000, 1)}s${t.attribution ? ` (${t.attribution})` : ""}`,
      )
      .join("; ");
    lines.push(
      `long tasks: ${snapshot.longTasks.count} in the last 60s, max ${fmtNumber(snapshot.longTasks.maxMs)}ms, total ${fmtNumber(snapshot.longTasks.totalMs)}ms`,
    );
    if (recent) lines.push(`recent long tasks: ${recent}`);
  }
  if (snapshot.longTaskFrames?.length) {
    lines.push("long task top frames (sampled):");
    for (const frame of snapshot.longTaskFrames) lines.push(`  ${frame.samples}x ${frame.label}`);
  }
  if (snapshot.connection) {
    const parts = [
      snapshot.connection.effectiveType,
      snapshot.connection.rttMs !== undefined ? `${snapshot.connection.rttMs}ms rtt` : "",
      snapshot.connection.downlinkMbps !== undefined ? `${snapshot.connection.downlinkMbps} Mbps` : "",
      snapshot.connection.saveData !== undefined ? `saveData ${snapshot.connection.saveData ? "true" : "false"}` : "",
    ].filter(Boolean);
    if (parts.length) lines.push(`connection: ${parts.join(", ")}`);
  }
  const pipeline = snapshot.sessionPipeline;
  if (pipeline?.activation) {
    const a = pipeline.activation;
    const parts = [`request ${a.requestId}`];
    if (a.tabId) parts.push(`tab ${a.tabId}`);
    if (a.ticketToStartingMs !== undefined) parts.push(`ticket→starting ${fmtNumber(a.ticketToStartingMs)}ms`);
    if (a.startingToReadyMs !== undefined) parts.push(`starting→ready ${fmtNumber(a.startingToReadyMs)}ms`);
    if (a.totalMs !== undefined) parts.push(`total ${fmtNumber(a.totalMs)}ms`);
    if (a.outcome) parts.push(`outcome ${a.outcome}`);
    if (a.failureClass) parts.push(`failure ${a.failureClass}`);
    lines.push(`activation: ${parts.join(", ")}`);
  }
  if (pipeline?.history) {
    const h = pipeline.history;
    lines.push(
      `history page: ${h.entries} entries, ${fmtNumber(h.inlineBytes / 1024, 1)} KiB inline, ${fmtNumber(h.durationMs)}ms, source ${h.source || "unknown"}${h.stale ? ", stale" : ""} ` +
        `(pages ${h.pages}, stale ${h.staleCount}, index hits ${h.indexHits}, misses ${h.indexMisses})`,
    );
  }
  if (pipeline?.mountedRows) {
    lines.push(`mounted rows: ${pipeline.mountedRows.mounted} of ${pipeline.mountedRows.total}`);
  }
  if (pipeline?.markdownWorker) {
    const w = pipeline.markdownWorker;
    lines.push(
      `markdown worker: ${w.pending} pending, ${w.completed} parsed, avg ${fmtNumber(w.avgParseMs, 1)}ms, max ${fmtNumber(w.maxParseMs)}ms` +
        `${w.fallbackActive ? ", fallback active" : ""}${w.workerFailures > 0 ? `, ${w.workerFailures} worker failures` : ""}`,
    );
  }
  if (pipeline?.transcriptCache) {
    const c = pipeline.transcriptCache;
    lines.push(
      `transcript cache: ${c.residentSessions}/${c.maxResidentSessions} resident sessions, ` +
        `bodies ${fmtMb(c.bodyBytes / 1048576)} of ${fmtMb(c.bodyBudgetBytes / 1048576)}, ` +
        `markdown ${fmtMb(c.markdownBytes / 1048576)} of ${fmtMb(c.markdownBudgetBytes / 1048576)}, ` +
        `evictions ${c.historyEvictions} history + ${c.markdownEvictions} markdown`,
    );
  }
  return lines.join("\n");
}

export function performanceLabelForReason(reason: string): string {
  const normalized = reason.trim().toLowerCase();
  if (normalized.startsWith("event loop lag")) return "performance.lag";
  if (normalized.startsWith("long task")) return "performance.longtask";
  if (normalized.startsWith("js heap")) return "performance.heap";
  return "performance.pressure";
}

export function performanceFingerprintHintForReason(reason: string): string | undefined {
  const normalized = reason.trim().toLowerCase();
  if (!normalized.startsWith("js heap")) return undefined;
  const match = normalized.match(/(\d+(?:\.\d+)?)%/);
  const percent = match ? Number(match[1]) : Number.NaN;
  if (!Number.isFinite(percent)) return "frontend.performance.heap.unknown";
  return percent >= 95
    ? "frontend.performance.heap.critical"
    : "frontend.performance.heap.high";
}

export function shouldRecordLongTaskSample(
  startMs: number,
  durationMs: number,
  graceUntilMs: number,
  visibilityHidden = false,
  visibleSinceMs = 0,
  focused = true,
): boolean {
  if (!focused) return false;
  if (visibilityHidden) return false;
  return durationMs >= 50 && startMs >= graceUntilMs && startMs - visibleSinceMs >= VISIBILITY_RESUME_GRACE_MS;
}

export function shouldPromptForLongTasks(summary: { count: number; totalMs: number; maxMs: number }): boolean {
  return summary.maxMs >= LONG_TASK_PROMPT_MS || (summary.count >= 3 && summary.totalMs >= LONG_TASK_TOTAL_PROMPT_MS);
}

// Task 692 (issue #36): the trigger reason must carry the cumulative window,
// not just the max. "long task 507ms" reads as one 507ms stall, but the report
// it names actually fired on the cumulative branch (26 tasks / total 3050ms in
// the 60s window) — the prefix stays "long task" so the pressure label mapping
// in performanceLabelForReason is unchanged.
export function formatLongTaskReason(summary: { count: number; totalMs: number; maxMs: number }): string {
  return `long task ${summary.count} in ${LONG_TASK_WINDOW_MS / 1000}s, max ${fmtNumber(summary.maxMs)}ms, total ${fmtNumber(summary.totalMs)}ms`;
}

export function shouldPromptForEventLoopLag(
  samples: readonly number[],
  longTask?: { count: number; totalMs: number; maxMs: number },
): boolean {
  const recent = samples.slice(-EVENT_LOOP_LAG_CONSECUTIVE_SAMPLES);
  const sustained =
    recent.length === EVENT_LOOP_LAG_CONSECUTIVE_SAMPLES &&
    recent.every((sample) => sample >= EVENT_LOOP_LAG_PROMPT_MS);
  const current = samples.length ? samples[samples.length - 1] : 0;
  const corroborated = current >= EVENT_LOOP_LAG_PROMPT_MS && Boolean(longTask && shouldPromptForLongTasks(longTask));
  return sustained || corroborated;
}

type TaskAttributionLike = {
  containerType?: string;
  containerName?: string;
  containerId?: string;
  containerSrc?: string;
};

// Longtask entries carry no stacks, only a culprit descriptor ("self", "same-origin",
// iframe container, ...). "self" and "unknown" are the expected no-signal cases, so
// only anomalies (cross-context culprits, named containers) make it into the report.
export function formatLongTaskAttribution(entryName?: string, attribution?: TaskAttributionLike[]): string {
  const parts: string[] = [];
  if (entryName && entryName !== "unknown" && entryName !== "self") parts.push(entryName);
  const culprit = attribution?.[0];
  if (culprit) {
    const container = culprit.containerName || culprit.containerId || culprit.containerSrc || "";
    const containerType = culprit.containerType && culprit.containerType !== "window" ? culprit.containerType : "";
    const detail = [containerType, container].filter(Boolean).join(":");
    if (detail) parts.push(detail);
  }
  return parts.join(" ");
}

// Self-time view of a self-profiling trace: count each sample that landed inside a
// long-task window against its leaf frame, so the report names the code that was
// actually on-CPU while the UI was blocked.
export function aggregateLongTaskProfile(
  trace: ProfilerTrace,
  windows: { startMs: number; durationMs: number }[],
  maxFrames = 8,
): { label: string; samples: number }[] {
  if (!windows.length) return [];
  const counts = new Map<number, number>();
  for (const sample of trace.samples ?? []) {
    if (sample.stackId === undefined) continue;
    const inWindow = windows.some(
      (w) => sample.timestamp >= w.startMs && sample.timestamp <= w.startMs + w.durationMs,
    );
    if (!inWindow) continue;
    const stack = trace.stacks?.[sample.stackId];
    if (!stack) continue;
    counts.set(stack.frameId, (counts.get(stack.frameId) ?? 0) + 1);
  }
  return [...counts.entries()]
    .sort((a, b) => b[1] - a[1])
    .slice(0, maxFrames)
    .map(([frameId, samples]) => ({ label: formatProfilerFrame(trace, frameId), samples }));
}

function formatProfilerFrame(trace: ProfilerTrace, frameId: number): string {
  const frame = trace.frames?.[frameId];
  if (!frame) return `frame#${frameId}`;
  const name = frame.name || "(anonymous)";
  const resource = frame.resourceId !== undefined ? trace.resources?.[frame.resourceId] : undefined;
  if (!resource) return name;
  const line = frame.line !== undefined ? `:${frame.line}${frame.column !== undefined ? `:${frame.column}` : ""}` : "";
  return `${name} (${resource}${line})`;
}

export function shouldRecordEventLoopLagSample(
  visibilityHidden: boolean,
  msSinceVisible: number,
  focused = true,
  msSinceFocused = msSinceVisible,
): boolean {
  if (!focused) return false;
  if (visibilityHidden) return false;
  return msSinceVisible >= VISIBILITY_RESUME_GRACE_MS && msSinceFocused >= VISIBILITY_RESUME_GRACE_MS;
}

export function buildPerformancePayload(snapshot: PerformanceSnapshot): CrashPayload {
  const buildCommit = typeof __BUILD_COMMIT__ === "string" ? __BUILD_COMMIT__ : "dev";
  const context = formatPerformanceContext(snapshot);
  const crumbs = dumpBreadcrumbs();
  const label = performanceLabelForReason(snapshot.reason);
  const errorMessage = "UI responsiveness degraded because the app observed long tasks, event-loop lag, or high JS heap pressure.";
  return {
    schemaVersion: 2,
    source: "frontend.performance",
    kind: "performance",
    label,
    message: [
      `[${label}]`,
      errorMessage,
      `--- performance context ---\n${context}`,
      crumbs && `--- breadcrumbs ---\n${crumbs}`,
      `build ${buildCommit}`,
    ]
      .filter(Boolean)
      .join("\n\n"),
    errorType: "PerformancePressure",
    errorMessage,
    topFrame: "frontend.performance",
    fingerprintHint: performanceFingerprintHintForReason(snapshot.reason),
    buildCommit,
    channel: typeof __BUILD_CHANNEL__ === "string" ? __BUILD_CHANNEL__ : "",
    language: typeof navigator !== "undefined" ? navigator.language || "" : "",
    view: currentView(),
    breadcrumbs: snapshotBreadcrumbs(),
    occurredAt: new Date().toISOString(),
  };
}

export function buildCrashPayload(label: string, err: unknown, extra?: string): CrashPayload {
  const normalized = normalizeCrashError(err);
  const buildCommit = typeof __BUILD_COMMIT__ === "string" ? __BUILD_COMMIT__ : "dev";
  return {
    schemaVersion: 2,
    source: sourceForLabel(label),
    kind: kindForLabel(label),
    label,
    message: formatText(label, normalized, extra),
    errorType: normalized.errorType,
    errorMessage: normalized.errorMessage,
    stack: normalized.stack,
    componentStack: extra?.trim() || undefined,
    topFrame: topFrameFromStack(normalized.stack || extra),
    buildCommit,
    channel: typeof __BUILD_CHANNEL__ === "string" ? __BUILD_CHANNEL__ : "",
    language: typeof navigator !== "undefined" ? navigator.language || "" : "",
    view: currentView(),
    breadcrumbs: snapshotBreadcrumbs(),
    occurredAt: new Date().toISOString(),
  };
}

export function opaqueScriptFingerprintHint(
  rawView = currentView(),
  breadcrumbs = snapshotBreadcrumbs(),
  buildCommit = currentBuildCommit(),
): string {
  const view = rawView
    .replace(/[?#].*$/, "")
    .replace(/\b[0-9a-f]{8,}\b/gi, "_")
    .replace(/\/\d+(?=\/|$)/g, "/_");
  const categories = breadcrumbs
    .slice(-8)
    .map((crumb) => crumb.cat?.trim().toLowerCase().replace(/[^a-z0-9_.-]+/g, "_") ?? "")
    .filter(Boolean)
    .join(">");
  return clip(`build:${buildCommit.slice(0, 16)}|view:${view}|cats:${categories || "none"}`, 300);
}

function sendButton(
  payload: CrashPayload,
  className = "crash-overlay__send",
  onSent?: () => void,
  mock = false,
): HTMLButtonElement | null {
  // Resolved at click time via window.go, not the bridge module: this overlay must
  // stay usable even when the rest of the app (and its imports) is broken.
  // Task 642: mock reports take the ReportMockCrash binding, which runs the same
  // real channel and queues the report on upload failure like a native panic.
  const app = window.go?.main?.App;
  const report = mock ? app?.ReportMockCrash : app?.ReportCrash;
  if (!report) return null;
  const send = document.createElement("button");
  send.className = className;
  send.textContent = t(mock ? "crash.mockSend" : "crash.send");
  send.onclick = async () => {
    send.disabled = true;
    send.textContent = t("crash.sending");
    try {
      const status = await report(payload.kind, JSON.stringify(payload));
      send.textContent = mock ? t(status === "queued" ? "crash.mockQueued" : "crash.mockSent") : t("crash.sent");
      onSent?.();
    } catch (err) {
      send.textContent = mock ? t("crash.mockSendFailed") : t("crash.sendFailed");
      send.title = err instanceof Error ? err.message : String(err);
      send.disabled = false;
    }
  };
  return send;
}

const COPY_FEEDBACK_MS = 2_000;

// Task 617 route B: one-click analyze. The click runs the three prerequisite
// probes (source checkout / gh auth / live workspace) and only proceeds to a
// spend confirmation when all hard prerequisites pass — each failure paints its
// own distinct notice into `note` and route B stops, pointing at route A
// (Copy). Like the send button, both bindings are resolved at click time off
// window.go so the overlay keeps working when the rest of the app is broken.
//
// Task 663: the gates are shared by the whole analysis family — the hang entry
// (StartHangAnalysis) and the startup pending-crash entry (StartCrashAnalysis
// with a queued payload) pass their own start thunk, so there is one gate
// order, one spend confirmation and one progress/completion face everywhere.
export function analyzeEntryButton(
  className: string,
  note: HTMLDivElement,
  onStart: () => Promise<string>,
): HTMLButtonElement | null {
  const probe = window.go?.main?.App?.CrashAnalysisAvailability;
  if (!probe) return null;
  const analyze = document.createElement("button");
  analyze.className = className;
  analyze.textContent = t("crash.analyze");
  analyze.onclick = async () => {
    analyze.disabled = true;
    analyze.textContent = t("crash.analyzeChecking");
    let report: CrashAnalysisAvailabilityReport | null = null;
    try {
      report = await probe();
    } catch {
      report = null;
    }
    analyze.disabled = false;
    analyze.textContent = t("crash.analyze");
    if (!report || !report.workspaceReady) {
      note.textContent = t("crash.analyzeNoWorkspace");
      return;
    }
    if (!report.sourceReady) {
      note.textContent = t("crash.analyzeNoSource");
      return;
    }
    if (!report.ghAuthenticated) {
      // Task 643: the backend detail tells not-found apart from an auth
      // failure (the 2026-10-08 false alarm was a PATH-only miss reported as
      // "not authenticated"); surface it verbatim under the localized notice.
      const detail = typeof report.ghCheckDetail === "string" ? report.ghCheckDetail.trim() : "";
      note.textContent = detail ? `${t("crash.analyzeNoGh")}\n${detail}` : t("crash.analyzeNoGh");
      return;
    }
    paintAnalysisSpendConfirm(note, className, onStart);
  };
  return analyze;
}

function analyzeButton(
  payload: CrashPayload,
  className: string,
  note: HTMLDivElement,
): HTMLButtonElement | null {
  const start = window.go?.main?.App?.StartCrashAnalysis;
  if (!start) return null;
  return analyzeEntryButton(className, note, () => start(payload.kind, JSON.stringify(payload)));
}

// Prerequisite 2 is an explicit notice, not a gate: the analysis really runs an
// agent turn, so it only starts after the user confirms the spend.
function paintAnalysisSpendConfirm(note: HTMLDivElement, className: string, onStart: () => Promise<string>) {
  const text = document.createElement("span");
  text.textContent = t("crash.analyzeConfirm");
  const actions = document.createElement("span");
  const go = document.createElement("button");
  go.className = className;
  go.textContent = t("crash.analyzeConfirmGo");
  const cancel = document.createElement("button");
  cancel.className = className;
  cancel.textContent = t("crash.analyzeCancel");
  actions.append(go, cancel);
  go.onclick = async () => {
    go.disabled = true;
    cancel.disabled = true;
    note.textContent = t("crash.analyzeStarting");
    try {
      const summary = await onStart();
      // Task 663 ②: a started analysis must stay visible — the note switches
      // to the live progress face (running elapsed / done) instead of a
      // one-shot line the user cannot tell from a dead click.
      paintAnalysisProgress(note, className, summary);
    } catch (err) {
      note.textContent = `${t("crash.analyzeFailed")}\n${err instanceof Error ? err.message : String(err)}`;
    }
  };
  cancel.onclick = () => {
    note.textContent = "";
  };
  note.replaceChildren(text, actions);
}

// Task 663 ②: the progress face. Polls CrashAnalysisProgress while the note is
// attached; the polling backend state machine classifies done (turn settled,
// terminal status, or hosting tab gone), so a fast failure can never read as
// an endless "running". The restart button (gap ③) rides along from the start
// — the analysis conclusion usually wants an app restart to take effect.
function paintAnalysisProgress(note: HTMLDivElement, className: string, summary: string) {
  const started = document.createElement("span");
  started.textContent = `${t("crash.analyzeStarted")}\n${summary}`;
  const status = document.createElement("span");
  status.textContent = t("crash.analyzeRunning");
  const actions = document.createElement("span");
  const restart = restartButton(className, note);
  if (restart) actions.append(restart);
  note.replaceChildren(started, status, actions);

  const startedAtMs = Date.now();
  const progress = window.go?.main?.App?.CrashAnalysisProgress;
  if (!progress) return; // older backend: keep the static started face
  const formatElapsed = (secs: number) =>
    `${Math.floor(secs / 60)}:${String(secs % 60).padStart(2, "0")}`;
  const timer = window.setInterval(async () => {
    if (!note.isConnected) {
      window.clearInterval(timer);
      return;
    }
    try {
      const state = await progress();
      if (!state?.active) return;
      if (state.done) {
        window.clearInterval(timer);
        status.textContent = t("crash.analyzeDone");
        return;
      }
      const secs = Math.max(0, Math.floor((Date.now() - startedAtMs) / 1000));
      status.textContent = `${t("crash.analyzeRunning")} ${formatElapsed(secs)}`;
    } catch {
      // Keep the last painted state; a single failed poll is not a failure.
    }
  }, 3_000);
}

// Task 663 ③: the restart face of the analysis family, riding the restart
// pipeline (App.RestartDesktop → restartActiveVersionExempt: grace window,
// cancel-with-resume-marker, launcher handoff). Resolved at click time like
// every overlay binding. The confirm is painted into `note` — a plain restart
// interrupts running conversations, so it must never fire from one click.
export function restartButton(className: string, note: HTMLDivElement): HTMLButtonElement | null {
  const restart = window.go?.main?.App?.RestartDesktop;
  if (!restart) return null;
  const button = document.createElement("button");
  button.className = className;
  button.textContent = t("crash.restart");
  button.onclick = () => {
    const text = document.createElement("span");
    text.textContent = t("crash.restartConfirm");
    const actions = document.createElement("span");
    const go = document.createElement("button");
    go.className = className;
    go.textContent = t("crash.restartGo");
    const cancel = document.createElement("button");
    cancel.className = className;
    cancel.textContent = t("crash.analyzeCancel");
    actions.append(go, cancel);
    go.onclick = async () => {
      go.disabled = true;
      cancel.disabled = true;
      note.textContent = t("crash.restarting");
      try {
        await restart();
        // The process quits shortly after; keep the notice as-is.
      } catch (err) {
        // Explicit failure (e.g. a portable build has no launcher): the note
        // names it instead of a dead button.
        note.textContent = `${t("crash.restartFailed")}\n${err instanceof Error ? err.message : String(err)}`;
      }
    };
    cancel.onclick = () => {
      note.textContent = "";
    };
    note.replaceChildren(text, actions);
  };
  return button;
}

function copyButton(text: string, className: string): HTMLButtonElement {
  const copy = document.createElement("button");
  copy.className = className;
  copy.textContent = t("crash.copy");
  copy.onclick = async () => {
    copy.disabled = true;
    let copied = false;
    // The crash overlay is the last-resort surface, so the button must re-enable
    // even if the clipboard path throws unexpectedly — a stuck disabled Copy is
    // exactly the #6388 unresponsive symptom. Catch so a rejection can't escape as
    // an unhandledrejection into the global crash handler either.
    try {
      copied = await writeClipboardText(text);
    } catch {
      copied = false;
    } finally {
      copy.textContent = copied ? t("crash.copied") : t("crash.copyFailed");
      copy.disabled = false;
      window.setTimeout(() => {
        copy.textContent = t("crash.copy");
      }, COPY_FEEDBACK_MS);
    }
  };
  return copy;
}

function paintPerformancePrompt(payload: CrashPayload, snapshot: PerformanceSnapshot) {
  if (typeof document === "undefined") return;
  let host = document.getElementById("performance-report-prompt");
  if (!host) {
    host = document.createElement("div");
    host.id = "performance-report-prompt";
    document.body.appendChild(host);
  }
  const title = document.createElement("div");
  title.className = "performance-report__title";
  title.textContent = t("performanceReport.title");
  const body = document.createElement("pre");
  body.className = "performance-report__body";
  body.textContent = formatPerformanceContext(snapshot);
  const actions = document.createElement("div");
  actions.className = "performance-report__actions";
  const send = sendButton(payload, "performance-report__send", () => markPerfReported(payload.label));
  // Task 617 route A: copy a paste-ready GitHub issue skeleton, not the bare
  // diagnostic text — the upstream endpoint is down (618) and this is the
  // zero-dependency feedback path.
  const copy = copyButton(buildCrashIssueSkeleton(payload), "performance-report__copy");
  const analysisNote = document.createElement("div");
  analysisNote.className = "performance-report__analysis";
  const analyze = analyzeButton(payload, "performance-report__analyze", analysisNote);
  const dismiss = document.createElement("button");
  dismiss.className = "performance-report__dismiss";
  dismiss.textContent = t("performanceReport.dismiss");
  dismiss.onclick = () => {
    dismissedPerfLabels.add(payload.label);
    host?.remove();
  };
  if (send) actions.append(send);
  actions.append(copy);
  if (analyze) actions.append(analyze);
  actions.append(dismiss);
  const note = document.createElement("div");
  note.className = "performance-report__note";
  note.textContent = t("performanceReport.privacyNote");
  const children = [title, body, actions];
  if (analyze) children.push(analysisNote);
  children.push(note);
  host.replaceChildren(...children);
}

export function paintCrashOverlay(payload: CrashPayload, options?: { mock?: boolean }) {
  const mock = options?.mock === true;
  let host = document.getElementById("crash-overlay");
  if (!host) {
    host = document.createElement("div");
    host.id = "crash-overlay";
    document.body.appendChild(host);
  }
  const title = document.createElement("div");
  title.className = "crash-overlay__title";
  title.textContent = t("crash.title");
  if (mock) {
    // Task 642 anti-misreport marking: the lab drill must never be readable as
    // a real failure, so the badge rides on the title itself.
    const badge = document.createElement("span");
    badge.className = "crash-overlay__mock-badge";
    badge.textContent = t("crash.mockBadge");
    title.appendChild(document.createTextNode(" "));
    title.appendChild(badge);
  }
  const banner = mock
    ? (() => {
        const el = document.createElement("div");
        el.className = "crash-overlay__mock-banner";
        el.textContent = t("crash.mockBanner");
        return el;
      })()
    : null;
  const body = document.createElement("pre");
  body.className = "crash-overlay__body";
  body.textContent = payload.message;
  // Task 617 route A: copy a paste-ready GitHub issue skeleton (title,
  // sectioned body, repo link, labels) instead of the bare diagnostic text.
  const copy = copyButton(buildCrashIssueSkeleton(payload), "crash-overlay__copy");
  const actions = document.createElement("div");
  actions.className = "crash-overlay__actions";
  const send = sendButton(payload, undefined, undefined, mock);
  const analysisNote = document.createElement("div");
  analysisNote.className = "crash-overlay__analysis";
  const analyze = analyzeButton(payload, "crash-overlay__analyze", analysisNote);
  if (send) actions.append(send);
  actions.append(copy);
  if (analyze) actions.append(analyze);
  const note = document.createElement("div");
  note.className = "crash-overlay__note";
  note.textContent = t(mock ? "crash.mockNote" : "crash.privacyNote");
  const children: HTMLElement[] = [title];
  if (banner) children.push(banner);
  children.push(body, actions);
  if (analyze) children.push(analysisNote);
  if (send) children.push(note);
  host.replaceChildren(...children);
}

export function reportCrash(label: string, err: unknown, extra?: string) {
  paintCrashOverlay(buildCrashPayload(label, err, extra));
}

type GlobalCrashEventLike = Pick<Event, "defaultPrevented"> & {
  message?: unknown;
  error?: unknown;
  reason?: unknown;
  filename?: unknown;
  lineno?: unknown;
  colno?: unknown;
};

const RESIZE_OBSERVER_LOOP_MESSAGE_RE = /^ResizeObserver loop (?:limit exceeded|completed with undelivered notifications\.?)$/;
const OPAQUE_SCRIPT_ERROR_MESSAGE = "Script error.";
function globalCrashEventMessages(e: GlobalCrashEventLike): string[] {
  const messages: string[] = [];
  const pushMessage = (message: string) => {
    const trimmed = message.trim();
    if (trimmed) messages.push(trimmed);
  };
  if (typeof e.message === "string") pushMessage(e.message);
  const error = e.error ?? e.reason;
  if (typeof error === "string") pushMessage(error);
  if (error && typeof error === "object" && "message" in error) {
    const msg = (error as { message?: unknown }).message;
    if (typeof msg === "string") pushMessage(msg);
  }
  return messages;
}

export function shouldReportGlobalCrashEvent(e: GlobalCrashEventLike): boolean {
  if (e.defaultPrevented) return false;
  if (globalCrashEventMessages(e).some((message) => RESIZE_OBSERVER_LOOP_MESSAGE_RE.test(message) ||
    /Minified React error #520\b/.test(message) || message.includes("status was superseded by"))) return false;
  if (isWailsRuntimeOnlyCrashEvent(e)) return false;
  return true;
}

export function isOpaqueScriptErrorEvent(e: GlobalCrashEventLike): boolean {
  return (
    (e.error === undefined || e.error === null) &&
    typeof e.message === "string" &&
    e.message.trim() === OPAQUE_SCRIPT_ERROR_MESSAGE &&
    globalScriptErrorLocation(e) === ""
  );
}

function globalScriptErrorLocation(e: GlobalCrashEventLike): string {
  const parts: string[] = [];
  if (typeof e.filename === "string" && e.filename.trim()) parts.push(`filename=${e.filename.trim()}`);
  if (typeof e.lineno === "number" && Number.isFinite(e.lineno) && e.lineno > 0) parts.push(`lineno=${e.lineno}`);
  if (typeof e.colno === "number" && Number.isFinite(e.colno) && e.colno > 0) parts.push(`colno=${e.colno}`);
  return parts.join(" ");
}

export function globalCrashReportReason(e: GlobalCrashEventLike): unknown {
  if (e.error !== undefined && e.error !== null) return e.error;
  const message = typeof e.message === "string" ? e.message.trim() : e.message;
  if (message === OPAQUE_SCRIPT_ERROR_MESSAGE) {
    const location = globalScriptErrorLocation(e);
    if (location) return `${OPAQUE_SCRIPT_ERROR_MESSAGE}\n${location}`;
  }
  return e.message;
}

export function shouldPromptForPerformanceLabel(
  alreadyHandled: boolean,
  msSinceLastPrompt: number,
  visibilityHidden: boolean,
  focused = true,
): boolean {
  if (alreadyHandled) return false;
  if (msSinceLastPrompt < PROMPT_COOLDOWN_MS) return false;
  if (visibilityHidden) return false;
  if (!focused) return false;
  return true;
}

function isPerfLabelHandled(label: string): boolean {
  return dismissedPerfLabels.has(label) || getReportedPerfLabels().has(label);
}

// ── Task 360: 卡顿事件自动落盘 ───────────────────────────────────────────────
// 弹窗详情/breadcrumb/帧采样过去只活在内存与弹窗 payload 里（breadcrumb=30 条
// 环形、desktop.log 对前端卡顿零痕迹），不点「发送报告」事后无从排障。这里把
// 三个触发源（long task / js heap / event loop lag）任一触发整理成一条 jank
// 记录，经 ReportJankRecord 落 logs/perf/jank-YYYYMMDD.jsonl（与后端
// perf-sample 同目录同天，时间轴可对齐）。

export type JankRecord = {
  recordedAt: string;
  label: string;
  reason: string;
  /** Same-label triggers folded into this record by the throttle. */
  suppressedSinceLast?: number;
  snapshot: PerformanceSnapshot;
  breadcrumbs: Breadcrumb[];
};

const JANK_LOG_COOLDOWN_MS = 60_000;
const jankLastWriteByLabel = new Map<string, number>();
const jankSuppressedByLabel = new Map<string, number>();

/** Test seam: the throttle is module state, suites reset it between blocks. */
export function resetJankRecordingForTest(): void {
  jankLastWriteByLabel.clear();
  jankSuppressedByLabel.clear();
}

/** Test seam: pretend a record was just written for `label` (the real write
 * happens inside recordJankEvent's async path). */
export function markJankRecordedForTest(label: string, now: number): void {
  jankLastWriteByLabel.set(label, now);
}

export function jankRecordingDue(label: string, now: number): boolean {
  const last = jankLastWriteByLabel.get(label) ?? 0;
  return now - last >= JANK_LOG_COOLDOWN_MS;
}

export function buildJankRecord(
  label: string,
  snapshot: PerformanceSnapshot,
  breadcrumbs: Breadcrumb[],
  suppressedSinceLast: number,
  recordedAt: string,
): JankRecord {
  return {
    recordedAt,
    label,
    reason: snapshot.reason,
    ...(suppressedSinceLast > 0 ? { suppressedSinceLast } : {}),
    snapshot,
    breadcrumbs,
  };
}

function shouldPromptForPerformance(now: number, label: string): boolean {
  const hidden = typeof document !== "undefined" && document.visibilityState === "hidden";
  const focused = typeof document === "undefined" || document.hasFocus?.() !== false;
  return shouldPromptForPerformanceLabel(isPerfLabelHandled(label), now - lastPerformancePromptAt, hidden, focused);
}

function promptPerformanceReport(reason: string, currentLagMs = 0): void {
  const now = Date.now();
  const label = performanceLabelForReason(reason);
  // Task 360: 落盘走自己的节流（每 label 60s，被抑制的触发并入下一条的
  // suppressedSinceLast），在弹窗门控之前——弹窗被冷却/隐藏压住时事件照样
  // 有本地档，事后排障不再依赖用户点「发送报告」。
  const recordDue = jankRecordingDue(label, now);
  let suppressed = 0;
  if (recordDue) {
    jankLastWriteByLabel.set(label, now);
    suppressed = jankSuppressedByLabel.get(label) ?? 0;
    jankSuppressedByLabel.set(label, 0);
  } else {
    jankSuppressedByLabel.set(label, (jankSuppressedByLabel.get(label) ?? 0) + 1);
  }
  const promptDue = shouldPromptForPerformance(now, label);
  if (!recordDue && !promptDue) return;
  if (promptDue) {
    lastPerformancePromptAt = now;
    addBreadcrumb("performance", reason);
  }
  const snapshot = performanceSnapshot(reason, currentLagMs);
  // Attribution windows shared by both paths: every recorded long task, plus
  // the lag spike itself for event-loop reports (profiler timestamps share
  // performance.now()'s origin).
  const windows = [...longTasks];
  if (currentLagMs > 0) {
    const nowMs = performance.now();
    windows.push({ startMs: Math.max(0, nowMs - currentLagMs), durationMs: currentLagMs });
  }
  if (!activeProfiler) {
    // Task 692: no self-profiling in this WebView — both paths finish
    // synchronously with empty frames (same face as before the shared
    // collection below).
    if (recordDue) writeJankRecord(label, snapshot, suppressed, now);
    if (promptDue) paintPerformancePrompt(buildPerformancePayload(snapshot), snapshot);
    return;
  }
  // Task 692 (issue #37): the jank sink and the prompt each used to consume
  // the module-level singleton profiler. The sink went first and nulled
  // activeProfiler, so the prompt path saw null and gave up — the local
  // archive kept its sampled frames while the reported payload lost all of
  // them. One trigger now collects once and shares the frames.
  void collectLongTaskFrames(windows)
    .then((frames) => {
      if (frames.length) snapshot.longTaskFrames = frames;
      if (recordDue) writeJankRecord(label, snapshot, suppressed, now);
      if (promptDue) paintPerformancePrompt(buildPerformancePayload(snapshot), snapshot);
    })
    .catch(() => {});
}

// writeJankRecord is the disk sink half of promptPerformanceReport. Kept
// fault-isolated: a failing record must never take the prompt down with it.
function writeJankRecord(label: string, snapshot: PerformanceSnapshot, suppressed: number, nowMs: number): void {
  try {
    const record = buildJankRecord(label, snapshot, snapshotBreadcrumbs(), suppressed, new Date(nowMs).toISOString());
    // Optional binding: older backends drop it silently; diagnostics must
    // never break the trigger path.
    void app.ReportJankRecord?.(JSON.stringify(record))?.catch(() => {});
  } catch {
    // Never let the recorder throw into the trigger path.
  }
}

function maybePromptForHeapPressure(): void {
  const heap = readHeapSnapshot();
  if (!heap?.usagePercent) return;
  if (heap.usedMb >= 512 && heap.usagePercent >= 85) {
    promptPerformanceReport(`js heap ${fmtNumber(heap.usagePercent)}% of limit`);
  }
}

export function installPerformancePressureMonitor() {
  if (performanceMonitorInstalled || typeof window === "undefined" || typeof performance === "undefined") return;
  if (!window.runtime) return;
  performanceMonitorInstalled = true;
  const startedAt = performance.now();
  const graceUntil = startedAt + STARTUP_GRACE_MS;
  const isHidden = () => typeof document !== "undefined" && document.visibilityState === "hidden";
  const isFocused = () => typeof document === "undefined" || document.hasFocus?.() !== false;
  let visibleSince = isHidden() ? Number.POSITIVE_INFINITY : startedAt;
  let focusedSince = isFocused() ? startedAt : Number.POSITIVE_INFINITY;
  let expected = performance.now() + 1000;
  let eventLoopLagPrimed = false;
  // When the view is shown or focused again, overdue timer callbacks can run before
  // the queued visibilitychange/focus task, so visibleSince/focusedSince may still
  // describe the previous settled period at that point. The sampler tracks hidden and
  // unfocused observations itself and restarts both windows on the first settled tick
  // instead of trusting the listener-maintained timestamps.
  let pendingResume = isHidden() || !isFocused();

  const pastGrace = () => performance.now() >= graceUntil;
  const inspectLongTasks = () => {
    if (!pastGrace()) return;
    const summary = longTaskSummary();
    if (!summary) return;
    if (shouldPromptForLongTasks(summary)) {
      promptPerformanceReport(formatLongTaskReason(summary));
    }
  };

  startLongTaskProfiler();

  // Blur/hide park the timestamps at +Infinity so a stale read before the matching
  // resume listener has run can never satisfy the grace windows.
  const resetSamples = () => {
    const now = performance.now();
    longTasks.length = 0;
    lagSamples.length = 0;
    expected = now + 1000;
    eventLoopLagPrimed = false;
    visibleSince = isHidden() ? Number.POSITIVE_INFINITY : now;
    focusedSince = isFocused() ? now : Number.POSITIVE_INFINITY;
    pendingResume = isHidden() || !isFocused();
  };

  if (typeof document !== "undefined") {
    document.addEventListener("visibilitychange", resetSamples);
  }
  window.addEventListener("focus", resetSamples);
  window.addEventListener("blur", resetSamples);

  if (typeof PerformanceObserver !== "undefined") {
    try {
      const observer = new PerformanceObserver((list) => {
        for (const entry of list.getEntries()) {
          if (!shouldRecordLongTaskSample(entry.startTime, entry.duration, graceUntil, isHidden(), visibleSince, isFocused())) continue;
          const attribution = formatLongTaskAttribution(
            entry.name,
            (entry as PerformanceEntry & { attribution?: TaskAttributionLike[] }).attribution,
          );
          longTasks.push({
            startMs: Math.round(entry.startTime),
            durationMs: Math.round(entry.duration),
            ...(attribution ? { attribution } : {}),
          });
        }
        pruneLongTasks();
        inspectLongTasks();
      });
      observer.observe({ entryTypes: ["longtask"] });
    } catch {
      // Some WebViews expose PerformanceObserver without the longtask entry type.
    }
  }

  window.setInterval(() => {
    const now = performance.now();
    if (isHidden() || !isFocused()) {
      pendingResume = true;
    } else if (pendingResume) {
      pendingResume = false;
      visibleSince = now;
      focusedSince = now;
      longTasks.length = 0;
      lagSamples.length = 0;
      expected = now + 1000;
      eventLoopLagPrimed = false;
      return;
    }
    if (!pastGrace()) {
      expected = now + 1000;
      return;
    }
    if (!eventLoopLagPrimed) {
      expected = now + 1000;
      eventLoopLagPrimed = true;
      return;
    }
    const lagMs = Math.max(0, now - expected);
    expected = now + 1000;
    if (!shouldRecordEventLoopLagSample(isHidden(), now - visibleSince, isFocused(), now - focusedSince)) return;
    lagSamples.push(lagMs);
    if (lagSamples.length > MAX_LAG_SAMPLES) lagSamples.shift();
    if (shouldPromptForEventLoopLag(lagSamples, longTaskSummary(now))) {
      promptPerformanceReport(`event loop lag ${fmtNumber(lagMs)}ms`, lagMs);
    }
    maybePromptForHeapPressure();
  }, 1000);
}
