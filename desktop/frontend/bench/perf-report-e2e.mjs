#!/usr/bin/env node
// tl1417 (issue #82) 验收: 真实 Chromium 里跑真实 crash.ts 管线 (bridge Proxy +
// breadcrumbs 活动通道 + Profiler + PerformanceObserver(longtask) +
// promptPerformanceReport), 复现「活动→安静→长任务」时序, 读两条出口的真实数据:
//   本地出口 = ReportJankRecord 收到的 JSON (结构化 snapshot)
//   上传/弹窗出口 = formatPerformanceContext 文本 (弹窗 <pre> 与上传 message 同源)
// 断言: max 任务单独分桶且帧是长任务自己的; 采样密度被诚实标注; 触发前上下文列出
// bridge 调用/流式事件/交互, 并点名长任务开始前最后一个事件。
//
// 用法: cd desktop/frontend && PLAYWRIGHT_EXECUTABLE_PATH=<chrome.exe> node bench/perf-report-e2e.mjs

import http from "node:http";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const frontendDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
process.env.PLAYWRIGHT_BROWSERS_PATH = !process.env.PLAYWRIGHT_BROWSERS_PATH || process.env.PLAYWRIGHT_BROWSERS_PATH === ".pw-browsers"
  ? path.join(frontendDir, ".pw-browsers")
  : process.env.PLAYWRIGHT_BROWSERS_PATH;
const { chromium } = await import("playwright");
const { build } = await import("vite");

// ── 1. 把真实管线打成一个浏览器 ESM 包 ──────────────────────────────────────
const outDir = mkdtempSync(path.join(os.tmpdir(), "tl1417-perf-e2e-"));
const srcUrl = (p) => p.split(path.sep).join("/");
const entry = path.join(outDir, "entry.ts");
writeFileSync(entry, `
import { installBreadcrumbConsoleHook, snapshotActivity } from "${srcUrl(path.join(frontendDir, "src/lib/breadcrumbs.ts"))}";
import { installPerformancePressureMonitor, formatPerformanceContext } from "${srcUrl(path.join(frontendDir, "src/lib/crash.ts"))}";
import { app } from "${srcUrl(path.join(frontendDir, "src/lib/bridge.ts"))}";
installBreadcrumbConsoleHook();
installPerformancePressureMonitor();
(window as any).__perf = { app, snapshotActivity, formatPerformanceContext, installedAt: performance.now() };
`);
await build({
  configFile: false,
  root: frontendDir,
  logLevel: "silent",
  define: { __BUILD_COMMIT__: JSON.stringify("tl1417-e2e"), __BUILD_CHANNEL__: JSON.stringify("bench") },
  build: {
    outDir,
    emptyOutDir: false,
    minify: false,
    sourcemap: false,
    lib: { entry, formats: ["es"], fileName: () => "bundle.js" },
    // A transitive dependency of bridge.ts reads process.env at module scope;
    // the app build gets that from Vite's full config, the lib build needs a shim.
    rollupOptions: { output: { inlineDynamicImports: true, banner: "var process = { env: { NODE_ENV: 'production' } };" } },
  },
});
const bundle = readFileSync(path.join(outDir, "bundle.js"), "utf8");

// ── 2. 带 Document-Policy 的静态服务 ─────────────────────────────────────────
// 注意: 忙循环与对照 profiler 必须是同源 <script> 下发的代码。Chromium 的
// ProfilerTraceBuilder 会丢弃没有同源脚本 URL 的帧 (CDP addInitScript / evaluate
// 注入的函数), 整栈被丢的样本就成了无栈样本——第一版 bench 把 __busy 放在
// addInitScript 里, 对照 profiler 实测长任务期间 35 样本/s 但带栈 0。
const PAGE = `<!doctype html><html><head><meta charset="utf-8"><title>tl1417 perf e2e</title></head>
<body><div id="grid"></div>
<script>
window.__busy = function __busy(ms) {
  const grid = document.getElementById("grid");
  if (grid && !grid.childElementCount) for (let i = 0; i < 800; i++) { const d = document.createElement("div"); d.textContent = "row " + i; grid.appendChild(d); }
  const t0 = performance.now(); let acc = 0; let n = 0;
  while (performance.now() - t0 < ms) {
    if (grid) { grid.style.paddingLeft = (n % 2) + "px"; for (const el of grid.children) acc += el.getBoundingClientRect().height; }
    for (let i = 0; i < 20000; i++) acc += Math.sqrt(i);
    n++;
  }
  return acc;
};
// Control profiler: same page, full-scenario coverage, histogram per second.
window.__control = typeof Profiler === "function" ? new Profiler({ sampleInterval: 10, maxBufferSize: 6000 }) : null;
window.__controlHistogram = async (windows) => {
  if (!window.__control) return null;
  const trace = await window.__control.stop();
  const rows = [];
  for (let s = 0; s * 1000 < performance.now(); s += 1) {
    const from = s * 1000;
    const inSec = (trace.samples || []).filter((x) => x.timestamp >= from && x.timestamp < from + 1000);
    rows.push({ s, samples: inSec.length, withStack: inSec.filter((x) => x.stackId !== undefined).length });
  }
  const inWin = (trace.samples || []).filter((x) => x.stackId !== undefined && windows.some((w) => x.timestamp >= w.startMs && x.timestamp <= w.startMs + w.durationMs));
  return { rows: rows.filter((r) => r.samples > 0), traceSamples: (trace.samples || []).length, inWindowStacked: inWin.length };
};
</script>
<script type="module" src="/bundle.js"></script></body></html>`;
const server = http.createServer((req, res) => {
  if (req.url === "/bundle.js") {
    res.writeHead(200, { "Content-Type": "text/javascript; charset=utf-8", "Cache-Control": "no-store" });
    res.end(bundle);
    return;
  }
  res.writeHead(200, { "Content-Type": "text/html; charset=utf-8", "Document-Policy": "js-profiling", "Cache-Control": "no-store" });
  res.end(PAGE);
});
await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
const port = server.address().port;

const browser = await chromium.launch({
  headless: true,
  ...(process.env.PLAYWRIGHT_EXECUTABLE_PATH ? { executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH } : {}),
});
const out = (name, data) => process.stdout.write(`${JSON.stringify({ probe: name, ...data })}\n`);
let failures = 0;
const assert = (cond, msg) => {
  process.stdout.write(`  ${cond ? "PASS" : "FAIL"} ${msg}\n`);
  if (!cond) failures += 1;
};

try {
  const page = await browser.newPage({ viewport: { width: 1000, height: 700 } });
  const pageErrors = [];
  page.on("pageerror", (e) => { pageErrors.push(String(e)); process.stderr.write(`PAGEERROR ${String(e)}\n`); });
  page.on("console", (m) => { if (m.type() === "error" || m.type() === "warning") process.stderr.write(`CONSOLE.${m.type()} ${m.text()}\n`); });
  // Wails runtime + Go binding stubs, installed before any page script: the
  // monitor requires window.runtime, the bridge Proxy routes to window.go, and
  // ReportJankRecord is the local outlet we capture.
  await page.addInitScript(() => {
    const listeners = {};
    window.__jank = [];
    window.__emit = (name, payload) => (listeners[name] || []).forEach((cb) => cb(payload));
    window.runtime = {
      EventsOn: (name, cb) => { (listeners[name] ||= []).push(cb); return () => {}; },
      BrowserOpenURL: () => {},
    };
    window.go = { main: { App: {
      ReportJankRecord: (json) => { window.__jank.push(json); return Promise.resolve(); },
      GetStatus: () => Promise.resolve({ ok: true, secret: "SHOULD-NEVER-APPEAR" }),
      LoadHistoryPage: (sessionId, cursor) => Promise.resolve({ sessionId, cursor, body: "SECRET-HISTORY-BODY" }),
    } } };
  });
  await page.goto(`http://127.0.0.1:${port}/`, { waitUntil: "load" });
  await page.waitForFunction(() => Boolean(window.__perf), null, { timeout: 15_000, polling: 100 });
  out("env", { chromium: browser.version(), hasProfiler: await page.evaluate(() => typeof Profiler === "function") });

  // ── 3. 活动期: bridge 调用 / 流式事件 / 交互 (带参数与内容, 验证它们进不了报告) ──
  await page.evaluate(async () => {
    await window.__perf.app.GetStatus();
    await window.__perf.app.LoadHistoryPage("C:\\Users\\alice\\project", 42);
    for (let i = 0; i < 400; i++) window.__emit("agent:event", { kind: "assistant_delta", text: "SECRET-DELTA-" + i });
    window.__emit("tab:meta", { label: "SECRET-TAB-LABEL" });
    window.__emit("agent:event", { kind: "turn_done", text: "SECRET-END" });
  });
  await page.mouse.click(400, 300);
  await page.keyboard.press("a");
  const activityBefore = await page.evaluate(() => window.__perf.snapshotActivity());
  out("activity.afterWarmup", { entries: activityBefore.map((e) => `${e.kind} ${e.name} x${e.n}`) });

  // ── 4. 安静期直到启动宽限 (15s) 过去, 然后: 一个流式事件 → 其后的宏任务是 2.5s 长任务 ──
  await page.waitForFunction(() => performance.now() > 15_600, null, { timeout: 30_000, polling: 250 });
  const stall = await page.evaluate(() => new Promise((resolve) => {
    window.__emit("agent:event", { kind: "session_changed", sessionReset: true });
    setTimeout(() => {
      const t0 = performance.now();
      window.__busy(2500);
      resolve({ startMs: +t0.toFixed(1), durationMs: +(performance.now() - t0).toFixed(1) });
    }, 0);
  }));
  out("stall", stall);

  // ── 5. 等本地出口 (ReportJankRecord) 收到记录; 读弹窗 <pre> (= 上传 message 的 context 段) ──
  await page.waitForFunction(() => window.__jank.length > 0, null, { timeout: 15_000 });
  // Let every trigger settle (the overdue lag tick fires next to the PO callback), then dump all.
  await page.waitForTimeout(1500);
  const allRecords = await page.evaluate(() => window.__jank.map((json) => {
    const r = JSON.parse(json);
    return {
      reason: r.reason,
      label: r.label,
      hasMaxTask: Boolean(r.snapshot.longTaskMaxFrames),
      maxTaskSamples: r.snapshot.longTaskMaxFrames?.samples,
      sampling: r.snapshot.profilerSampling,
    };
  }));
  const records = await page.evaluate(() => window.__jank.slice());
  const longtaskIdx = records.findIndex((json) => JSON.parse(json).label === "performance.longtask");
  const record = JSON.parse(records[longtaskIdx]);
  const lagRecord = JSON.parse(records.find((json) => JSON.parse(json).label === "performance.lag"));
  const promptText = await page.evaluate(() => document.querySelector("pre.performance-report__body")?.textContent ?? "");
  const snap = record.snapshot;
  const control = await page.evaluate((w) => window.__controlHistogram([w]), { startMs: stall.startMs, durationMs: stall.durationMs });
  out("allRecords", allRecords);
  out("controlHistogram", control);

  out("readings", {
    reason: snap.reason,
    longTasks: snap.longTasks,
    longTaskFrames: snap.longTaskFrames,
    longTaskMaxFrames: snap.longTaskMaxFrames,
    profilerSampling: snap.profilerSampling,
    recentActivity: snap.recentActivity?.map((e) => `@${(e.t / 1000).toFixed(1)}s${e.n > 1 ? `..${(e.last / 1000).toFixed(1)}s` : ""} ${e.kind} ${e.name}${e.n > 1 ? ` x${e.n}` : ""}`),
    breadcrumbs: record.breadcrumbs.map((b) => `[${b.cat}] ${b.msg}`),
    pageErrors,
  });
  process.stdout.write("\n--- sample payload: performance context (prompt body = upload message section) ---\n" + promptText + "\n--- end ---\n\n");

  // ── 6. 断言 ─────────────────────────────────────────────────────────────────
  const max = snap.longTaskMaxFrames;
  assert(Boolean(max) && Math.abs(max.durationMs - stall.durationMs) < 150, `max bucket is the 2.5s stall (durationMs=${max?.durationMs})`);
  assert(max.frames.length > 0 && max.frames[0].label.startsWith("__busy"), `max bucket top frame is the stall's own function (${max.frames[0]?.label})`);
  assert(max.samples >= 40, `stall sampled densely: ${max.samples} samples in ${max.durationMs}ms (≈${(max.samples / (max.durationMs / 1000)).toFixed(0)}/s)`);
  assert(max.sampledToMs >= max.durationMs - 120, `coverage reaches the end of the stall (sampledToMs=${max.sampledToMs} of ${max.durationMs}) — no buffer truncation`);
  const s = snap.profilerSampling;
  assert(s && s.nominalIntervalMs === 10 && s.medianGapMs >= 12 && s.medianGapMs <= 60, `sampling line is honest: nominal 10ms, measured ~${s?.medianGapMs}ms`);
  assert(s.inWindow === max.samples, `only one window → inWindow equals the stall's samples (${s.inWindow})`);
  assert(JSON.stringify(snap.longTaskFrames) === JSON.stringify(max.frames), "single-window case: merged list equals the max bucket (old field intact)");
  const names = (snap.recentActivity ?? []).map((e) => `${e.kind} ${e.name}`);
  assert(names.includes("bridge GetStatus") && names.includes("bridge.done GetStatus"), "activity: bridge call + resolution recorded by name");
  assert(names.includes("bridge LoadHistoryPage"), "activity: hot-path bridge method recorded (not in the curated crumb allowlist)");
  const delta = (snap.recentActivity ?? []).find((e) => e.kind === "stream" && e.name === "assistant_delta");
  assert(Boolean(delta) && delta.n === 400, `activity: 400-delta stream coalesced into one entry (n=${delta?.n})`);
  assert(names.includes("stream tab:meta") && names.includes("stream turn_done"), "activity: other channels / kinds recorded by name");
  assert(names.includes("ui pointerdown") && names.includes("ui keydown"), "activity: interactions recorded by type");
  assert(names[names.length - 1] === "stream session_changed" || names.includes("stream session_changed"), "activity: the event right before the stall is present");
  assert(/last activity before max long task @ \d+\.\ds: @ \d+\.\ds stream session_changed \(0\.0s earlier\)/.test(promptText), "trigger-source line names session_changed 0.0s before the stall");
  assert(Boolean(lagRecord.snapshot.longTaskMaxFrames) && Boolean(lagRecord.snapshot.profilerSampling), "the lag trigger that lost the profiler race still carries frames + sampling (contention fix)");
  assert(lagRecord.snapshot.recentActivity?.length > 0, "the lag record carries the activity context too");
  const everything = JSON.stringify(allRecords) + JSON.stringify(record) + promptText;
  for (const leak of ["SECRET", "alice", "C:\\\\Users", "SHOULD-NEVER-APPEAR"]) {
    assert(!everything.includes(leak), `privacy: no "${leak}" in the record or the prompt text`);
  }
  assert(promptText.includes("max long task") && promptText.includes("profiler sampling:") && promptText.includes("recent activity (names only, coalesced; @ uptime):"), "prompt/upload text carries all three new sections");
  const crumbNames = record.breadcrumbs.map((b) => b.cat);
  assert(!crumbNames.some((c) => c === "bridge" && record.breadcrumbs.find((b) => b.cat === c && /LoadHistoryPage|GetStatus/.test(b.msg))), "curated crumbs unchanged: hot-path methods still not in the 30-slot ring");
  assert(pageErrors.length === 0, `no page errors (${pageErrors.join("; ")})`);
} finally {
  await browser.close();
  server.close();
}
process.stdout.write(`\n${failures === 0 ? "ALL PASS" : `${failures} FAILED`}\n`);
if (failures > 0) process.exitCode = 1;
