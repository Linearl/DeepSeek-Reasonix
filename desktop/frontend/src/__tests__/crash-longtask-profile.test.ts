// Run: tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/crash-longtask-profile.test.ts
//
// tl1417 (issue #82 observation gap 1): long-task frame attribution.
//   (a) the longest window gets its own bucket, the merged list keeps its meaning;
//   (b) sampling density is reported honestly (measured cadence, coverage of the
//       max task, buffer truncation visible as sampledToMs << durationMs);
//   (c) the sampler rolls over on an idle timer and keeps the previous trace, so a
//       report after a rollover still attributes tasks from before it — the
//       buffer-full rollover (bench/profiler-density.mjs E6b) used to hand the
//       report an empty profiler;
//   (d) end-to-end: fake Profiler + fake PerformanceObserver through
//       installPerformancePressureMonitor to the jank record JSON.

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import {
  aggregateLongTaskProfile,
  aggregateLongTaskProfiles,
  formatPerformanceContext,
  installPerformancePressureMonitor,
  type PerformanceSnapshot,
  type ProfilerTrace,
} from "../lib/crash";

let passed = 0;
let failed = 0;

function eq(a: unknown, b: unknown, label: string) {
  if (JSON.stringify(a) === JSON.stringify(b)) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(b)}, got ${JSON.stringify(a)}\n`);
    failed += 1;
  }
}
function ok(value: boolean, label: string) {
  eq(value, true, label);
}

// Build a trace with samples at a fixed cadence inside [fromMs, toMs) all landing
// on `frame` (leaf), plus optional stackless idle samples.
function denseTrace(
  frames: { name: string; fromMs: number; toMs: number; gapMs?: number; line?: number }[],
  idle: { fromMs: number; toMs: number; gapMs?: number }[] = [],
  resource = "wails://wails/assets/index.js",
): ProfilerTrace {
  const trace: ProfilerTrace = { resources: [resource], frames: [], stacks: [], samples: [] };
  frames.forEach((f, i) => {
    trace.frames!.push({ name: f.name, resourceId: 0, line: f.line ?? i + 1 });
    trace.stacks!.push({ frameId: i });
    for (let t = f.fromMs; t < f.toMs; t += f.gapMs ?? 30) trace.samples!.push({ timestamp: t, stackId: i });
  });
  for (const w of idle) for (let t = w.fromMs; t < w.toMs; t += w.gapMs ?? 30) trace.samples!.push({ timestamp: t });
  trace.samples!.sort((a, b) => a.timestamp - b.timestamp);
  return trace;
}

console.log("\ncrash long-task profile (tl1417 gap 1)");

// ── (a) bucketing: multi-window + max task ────────────────────────────────
{
  // Three 100ms click tasks on frame "dispatch", then a 16587ms stall whose
  // samples stop 300ms in (the buffer filled there — the #82 shape).
  const windows = [
    { startMs: 1_000, durationMs: 100 },
    { startMs: 1_300, durationMs: 100 },
    { startMs: 1_600, durationMs: 100 },
    { startMs: 5_000, durationMs: 16_587 },
  ];
  const trace = denseTrace(
    [
      { name: "dispatch", fromMs: 1_000, toMs: 1_700 },
      { name: "fullScan", fromMs: 5_000, toMs: 5_300 },
    ],
    [{ fromMs: 2_000, toMs: 4_000 }],
  );
  const legacy = aggregateLongTaskProfile(trace, windows);
  const summary = aggregateLongTaskProfiles([trace], windows);
  eq(summary.frames, legacy, "merged frames equal the pre-tl1417 aggregate (old field semantics unchanged)");
  eq(summary.frames.map((f) => f.label.split(" ")[0]), ["dispatch", "fullScan"], "merged list blends every window (dispatch dominates by sample count)");
  const max = summary.maxTask!;
  eq([max.startMs, max.durationMs], [5_000, 16_587], "max bucket picks the longest window");
  eq(max.frames.map((f) => f.label.split(" ")[0]), ["fullScan"], "max bucket holds only the stall's own frames");
  eq(max.samples, 10, "max bucket sample count is the stall's own");
  eq([max.sampledFromMs, max.sampledToMs], [0, 270], "coverage offsets expose the truncation: sampled 0..0.27s of 16.6s");
  eq(summary.sampling.traces, 1, "sampling reports the trace count");
  eq(summary.sampling.samples, trace.samples!.length, "sampling counts every sample, idle ones included");
  eq(summary.sampling.inWindow, 10 + 4 * 3, "sampling counts stack samples inside any window");
  eq(summary.sampling.medianGapMs, 30, "sampling reports the measured cadence, not the nominal 10ms");
  eq([summary.sampling.firstMs, summary.sampling.lastMs], [1_000, 5_270], "sampling reports the trace span");
  eq(summary.sampling.nominalIntervalMs, 10, "nominal interval is still named so the reader can compare");
}

// ── (c) two traces (previous period + current) merge across frame tables ───
{
  const windows = [
    { startMs: 1_000, durationMs: 500 },
    { startMs: 60_000, durationMs: 2_000 },
  ];
  // Independent frame tables: frame 0 is "alpha" in trace A but "beta" in trace B;
  // alpha sits at the same source line in both so its label is identical.
  const previous = denseTrace([{ name: "alpha", fromMs: 1_000, toMs: 1_500, line: 7 }]);
  const current = denseTrace([{ name: "beta", fromMs: 60_000, toMs: 62_000 }, { name: "alpha", fromMs: 61_000, toMs: 61_300, line: 7 }]);
  const summary = aggregateLongTaskProfiles([previous, current], windows);
  const byName = Object.fromEntries(summary.frames.map((f) => [f.label.split(" ")[0], f.samples]));
  eq(byName.alpha, Math.ceil(500 / 30) + 10, "same label from two traces is summed (frame ids are per-trace)");
  eq(summary.maxTask!.frames.map((f) => f.label.split(" ")[0]), ["beta", "alpha"], "max bucket also merges across traces");
  eq(summary.sampling.traces, 2, "two traces reported");
  eq(summary.sampling.firstMs, 1_000, "span starts at the previous trace");
}

// ── (b) no windows / empty trace still describe sampling ───────────────────
{
  const summary = aggregateLongTaskProfiles([denseTrace([{ name: "x", fromMs: 0, toMs: 300 }])], []);
  eq(summary.frames, [], "no windows → no frames (unchanged)");
  eq(summary.maxTask, undefined, "no windows → no max bucket");
  eq(summary.sampling.samples, 10, "sampling still reported without windows");
  const empty = aggregateLongTaskProfiles([{}], [{ startMs: 0, durationMs: 100 }]);
  eq(empty.maxTask?.samples, 0, "max bucket with zero samples says so");
  eq(empty.maxTask?.sampledFromMs, undefined, "no coverage offsets without samples");
}

// ── formatting: new sections additive, old section unchanged ───────────────
{
  const base: PerformanceSnapshot = {
    reason: "long task 7 in 60s, max 16587ms, total 20145ms",
    uptimeMs: 3_734_400,
    visibility: "visible",
    focused: true,
    online: true,
    hardwareConcurrency: 16,
    longTasks: { count: 7, totalMs: 20_145, maxMs: 16_587, recent: [{ startMs: 3_713_500, durationMs: 16_587 }] },
    longTaskFrames: [{ label: "processDispatchQueue (vendor-react:1:214677)", samples: 13 }],
  };
  const legacyText = formatPerformanceContext(base);
  ok(legacyText.includes("long task top frames (sampled):\n  13x processDispatchQueue (vendor-react:1:214677)"), "old frames section renders exactly as before");
  ok(!legacyText.includes("max long task") && !legacyText.includes("profiler sampling"), "new sections absent when the fields are absent");

  const text = formatPerformanceContext({
    ...base,
    longTaskMaxFrames: {
      startMs: 3_713_500,
      durationMs: 16_587,
      samples: 10,
      sampledFromMs: 0,
      sampledToMs: 310,
      frames: [{ label: "fullScan (index.js:131)", samples: 10 }],
    },
    profilerSampling: { nominalIntervalMs: 10, medianGapMs: 30.6, traces: 2, samples: 4_210, firstMs: 3_650_100, lastMs: 3_734_400, inWindow: 24 },
  });
  ok(text.includes("max long task 16587ms @ 3713.5s top frames (sampled 0.0s..0.3s of 16.6s, 10 samples):\n  10x fullScan (index.js:131)"), "max-task section names the window and its sampled slice");
  ok(text.includes("profiler sampling: nominal 10ms, measured ~31ms between samples, 2 traces, 4210 samples covering 3650.1s..3734.4s, 24 in long-task windows"), "sampling line states the measured cadence and coverage");
  const noCover = formatPerformanceContext({
    ...base,
    longTaskMaxFrames: { startMs: 3_713_500, durationMs: 16_587, samples: 0, frames: [] },
  });
  ok(noCover.includes("max long task 16587ms @ 3713.5s top frames (no samples inside it):"), "zero-sample max task is stated, not hidden");
}

// ── source pins: timer rollover + kept trace + single collect site ─────────
{
  const testDir = dirname(fileURLToPath(import.meta.url));
  const src = readFileSync(resolve(testDir, "../lib/crash.ts"), "utf8");
  ok(src.includes("window.setInterval(rolloverLongTaskProfiler, PROFILER_ROLLOVER_MS)"), "rollover runs on an idle timer, not only on samplebufferfull");
  ok(src.includes("const PROFILER_ROLLOVER_MS = 45_000"), "rollover period 45s < 96s buffer capacity at the 16ms API interval");
  ok(src.includes("if (profiler) previousTrace = profiler.stop().catch(() => null);"), "rollover keeps the stopped trace instead of dropping it");
  ok(src.includes("const [current, prior] = await Promise.all([profiler.stop(), previous ?? Promise.resolve(null)]);"), "collect merges the previous period's trace with the current one");
  ok(src.split("collectLongTaskFrames(").length - 1 === 2, "still one frame collection per trigger (issue #37 contention pin)");
  ok(src.includes("if (profile.frames.length) snapshot.longTaskFrames = profile.frames;"), "merged frames still land in longTaskFrames (consumers unchanged)");
}

// ── (d) end-to-end through installPerformancePressureMonitor ───────────────
{
  type Listener = () => void;
  class FakeProfiler {
    static instances: FakeProfiler[] = [];
    trace: ProfilerTrace = {};
    stopped = false;
    listeners = new Map<string, Listener>();
    holdStop = false;
    private resolveStop?: (trace: ProfilerTrace) => void;
    constructor(public opts: { sampleInterval: number; maxBufferSize: number }) {
      FakeProfiler.instances.push(this);
    }
    addEventListener(type: string, l: Listener) {
      this.listeners.set(type, l);
    }
    stop() {
      this.stopped = true;
      if (!this.holdStop) return Promise.resolve(this.trace);
      return new Promise<ProfilerTrace>((resolve) => { this.resolveStop = resolve; });
    }
    releaseStop() {
      this.resolveStop?.(this.trace);
    }
  }
  const intervals: { cb: () => void; ms: number }[] = [];
  const observers: { cb: (list: { getEntries: () => unknown[] }) => void }[] = [];
  const records: string[] = [];
  let now = 0;
  const g = globalThis as any;
  const prev = { window: g.window, document: g.document, performance: g.performance, PerformanceObserver: g.PerformanceObserver, Profiler: g.Profiler, localStorage: g.localStorage };
  g.performance = { now: () => now };
  g.Profiler = FakeProfiler;
  g.window = {
    runtime: {},
    // Jank sink captured here; the prompt face is kept out by marking the label
    // as already reported (persisted set, same build "dev").
    go: { main: { App: { ReportJankRecord: (json: string) => { records.push(json); return Promise.resolve(); } } } },
    location: { protocol: "app:", host: "test", pathname: "/", hash: "" },
    addEventListener: () => {},
    setInterval: (cb: () => void, ms: number) => { intervals.push({ cb, ms }); return intervals.length; },
  };
  g.localStorage = {
    getItem: (key: string) => (key === "reasonix:perf-reported" ? JSON.stringify({ build: "dev", labels: ["performance.longtask", "performance.lag"] }) : null),
    setItem: () => {},
  };
  g.document = {
    visibilityState: "visible",
    hasFocus: () => true,
    addEventListener: () => {},
    documentElement: { dataset: {} },
    getElementById: () => null,
  };
  g.PerformanceObserver = class {
    cb: (list: { getEntries: () => unknown[] }) => void;
    constructor(cb: (list: { getEntries: () => unknown[] }) => void) { this.cb = cb; observers.push(this); }
    observe() {}
  };

  installPerformancePressureMonitor();
  eq(FakeProfiler.instances.length, 1, "e2e: profiler started at install");
  eq(FakeProfiler.instances[0].opts, { sampleInterval: 10, maxBufferSize: 6000 }, "e2e: production profiler options unchanged");
  const rollover = intervals.find((i) => i.ms === 45_000);
  ok(Boolean(rollover), "e2e: 45s rollover interval registered");
  ok(intervals.some((i) => i.ms === 1000), "e2e: the 1s lag sampler interval is still registered");

  // Period 1: three click tasks at 20.0s..20.7s on "dispatch".
  FakeProfiler.instances[0].trace = denseTrace([{ name: "dispatch", fromMs: 20_000, toMs: 20_700 }]);
  // The 1s lag sampler primes on its first settled tick (expected := 41.0s).
  const lagTick = intervals.find((i) => i.ms === 1000)!.cb;
  now = 40_000;
  lagTick();
  now = 44_000;
  rollover!.cb();
  eq(FakeProfiler.instances.length, 2, "e2e: rollover started a second profiler");
  ok(FakeProfiler.instances[0].stopped, "e2e: rollover stopped the first profiler (trace kept, not dropped)");

  // Period 2: the stall at 50.0s; its samples stop 0.3s in (buffer full there).
  FakeProfiler.instances[1].trace = denseTrace([{ name: "fullScan", fromMs: 50_000, toMs: 50_300 }], [{ fromMs: 45_000, toMs: 49_000 }]);
  FakeProfiler.instances[1].holdStop = true; // collect in flight — the #37 contention window
  now = 70_000;
  const entries = [
    { startTime: 20_000, duration: 100, name: "self" },
    { startTime: 20_300, duration: 100, name: "self" },
    { startTime: 20_600, duration: 100, name: "self" },
    { startTime: 50_000, duration: 16_587, name: "self" },
  ];
  observers[0].cb({ getEntries: () => entries });
  // The overdue 1s tick fires while trigger 1's stop() is still pending: after
  // every big stall this is exactly the order (lag tick next to the PO
  // callback). The lag trigger must share the traces instead of reporting empty.
  now = 72_300;
  lagTick(); // 72.3s − 41.0s expected = 31.3s lag, corroborated by the stall → lag trigger
  FakeProfiler.instances[1].releaseStop();
  // collect() awaits two stop() promises then writes the records.
  for (let i = 0; i < 10 && records.length < 2; i += 1) await new Promise((r) => setTimeout(r, 0));
  eq(records.length, 2, "e2e: both triggers wrote a record (longtask first, lag shared)");
  const longtaskRecord = JSON.parse(records[0] ?? "{}") as { snapshot: PerformanceSnapshot; label: string };
  const lagRecord = JSON.parse(records[1] ?? "{}") as { snapshot: PerformanceSnapshot; label: string };
  eq(longtaskRecord.label, "performance.longtask", "e2e: record 0 is the longtask trigger");
  const snap = longtaskRecord.snapshot;
  eq(snap.longTaskFrames?.map((f) => f.label.split(" ")[0]), ["dispatch", "fullScan"], "e2e: merged frames span both periods (previous trace survived the rollover)");
  eq(snap.longTaskMaxFrames?.frames.map((f) => f.label.split(" ")[0]), ["fullScan"], "e2e: max bucket isolates the stall");
  eq([snap.longTaskMaxFrames?.startMs, snap.longTaskMaxFrames?.durationMs, snap.longTaskMaxFrames?.sampledToMs], [50_000, 16_587, 270], "e2e: max bucket shows the stall was only sampled 0.27s in");
  eq(snap.profilerSampling?.traces, 2, "e2e: two traces merged");
  eq(snap.profilerSampling?.medianGapMs, 30, "e2e: measured cadence reported");
  eq(FakeProfiler.instances.length, 3, "e2e: collect restarted the profiler once (the shared trigger did not start another)");
  ok(FakeProfiler.instances[1].stopped, "e2e: collect stopped the active profiler");
  // The lag trigger that arrived mid-collect: same traces, its own windows.
  eq(lagRecord.label, "performance.lag", "e2e: record 1 is the lag trigger");
  ok(lagRecord.snapshot.reason.startsWith("event loop lag 31300ms"), `e2e: lag reason carries the measured lag (${lagRecord.snapshot.reason})`);
  eq(lagRecord.snapshot.longTaskMaxFrames?.frames.map((f) => f.label.split(" ")[0]), ["fullScan"], "e2e: lag record shares the traces — max bucket present instead of empty");
  eq(lagRecord.snapshot.profilerSampling?.traces, 2, "e2e: lag record reports the shared traces");
  eq(lagRecord.snapshot.longTaskFrames?.map((f) => f.label.split(" ")[0]), ["dispatch", "fullScan"], "e2e: lag record merged frames too (its windows include the long tasks)");

  g.window = prev.window; g.document = prev.document; g.performance = prev.performance;
  g.PerformanceObserver = prev.PerformanceObserver; g.Profiler = prev.Profiler; g.localStorage = prev.localStorage;
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exitCode = 1;
