#!/usr/bin/env node
// tl1417 (issue #82 观测缺口 1): JS Self-Profiling 采样密度实测。
//
// 生产读数: 名义 sampleInterval=10ms, 一次 20145ms 的长任务窗口合计只有 24 个带栈
// 样本 (≈1.2/s, 差两个数量级)。此探针在真实 Chromium 里用与 crash.ts 完全相同的
// Profiler 配置 (10ms / maxBufferSize=6000) 回答四个问题:
//   E1 空闲期是否产生样本 (决定缓冲多久满、rollover 频率)
//   E2 纯 JS 忙循环期间的有效采样间隔 (定时器粒度 / 采样器是否饿死)
//   E3 强制 layout (getBoundingClientRect 循环, 即 #82 的 fullScan 形态) 期间样本是否
//      因叶子帧在 Blink 原生侧而失去 stackId (读取口径 `stackId===undefined → skip`)
//   E4 缓冲在长任务中途满: 采样是否静默停止、samplebufferfull 事件能否在任务结束前派发
//   E5 用生产常量 (10ms×6000) 推算 rollover 周期, 与 E4 合起来解释生产读数
//
// 用法: cd desktop/frontend && PLAYWRIGHT_EXECUTABLE_PATH=<chrome.exe> node bench/profiler-density.mjs
// 输出为 JSON 行, 供报告直接引用。

import http from "node:http";
import path from "node:path";
import { fileURLToPath } from "node:url";

const frontendDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
process.env.PLAYWRIGHT_BROWSERS_PATH = !process.env.PLAYWRIGHT_BROWSERS_PATH || process.env.PLAYWRIGHT_BROWSERS_PATH === ".pw-browsers"
  ? path.join(frontendDir, ".pw-browsers")
  : process.env.PLAYWRIGHT_BROWSERS_PATH;
const { chromium } = await import("playwright");

const PAGE = `<!doctype html><html><head><meta charset="utf-8"><title>profiler density</title></head>
<body><div id="grid"></div><script>
// 与 crash.ts 同口径: 落在窗口内且 stackId 已定义的样本才计数。
function inWindowStats(trace, win) {
  const samples = trace.samples || [];
  const inWin = samples.filter((s) => s.timestamp >= win.startMs && s.timestamp <= win.startMs + win.durationMs);
  const withStack = inWin.filter((s) => s.stackId !== undefined);
  const ts = inWin.map((s) => s.timestamp).sort((a, b) => a - b);
  const gaps = [];
  for (let i = 1; i < ts.length; i++) gaps.push(ts[i] - ts[i - 1]);
  gaps.sort((a, b) => a - b);
  const q = (p) => (gaps.length ? gaps[Math.min(gaps.length - 1, Math.floor(p * gaps.length))] : null);
  // 叶子帧名分布 (前 5), 用于看 E3 里叶子是 JS 函数还是原生回调
  const leaf = new Map();
  for (const s of withStack) {
    const st = trace.stacks[s.stackId];
    const fr = trace.frames[st.frameId];
    const name = fr.name || "(anonymous)";
    leaf.set(name, (leaf.get(name) || 0) + 1);
  }
  const top = [...leaf.entries()].sort((a, b) => b[1] - a[1]).slice(0, 5);
  return {
    windowMs: Math.round(win.durationMs),
    total: inWin.length,
    withStack: withStack.length,
    withoutStack: inWin.length - withStack.length,
    perSecondWithStack: win.durationMs ? +(withStack.length / (win.durationMs / 1000)).toFixed(1) : 0,
    gapMedianMs: q(0.5), gapP90Ms: q(0.9), gapMaxMs: gaps.length ? gaps[gaps.length - 1] : null,
    firstMs: ts.length ? +ts[0].toFixed(1) : null, lastMs: ts.length ? +ts[ts.length - 1].toFixed(1) : null,
    topLeaf: top,
  };
}
function busyJs(ms) {
  const t0 = performance.now(); let x = 0;
  while (performance.now() - t0 < ms) { for (let i = 0; i < 2000; i++) x += Math.sqrt(i * 1.0001); }
  return { startMs: t0, durationMs: performance.now() - t0, x };
}
function busyLayout(ms, n) {
  const grid = document.getElementById("grid");
  if (!grid.childElementCount) {
    for (let i = 0; i < n; i++) { const d = document.createElement("div"); d.textContent = "row " + i; d.style.padding = "1px"; grid.appendChild(d); }
  }
  const t0 = performance.now(); let acc = 0; let scans = 0;
  while (performance.now() - t0 < ms) {
    // 写样式使 layout 失效, 再逐块 getBoundingClientRect 强制 layout (= #82 fullScan 形态)
    grid.style.paddingLeft = (scans % 2) + "px";
    for (const el of grid.children) acc += el.getBoundingClientRect().height;
    scans++;
  }
  return { startMs: t0, durationMs: performance.now() - t0, acc, scans };
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
async function withLongTasks(fn) {
  const entries = [];
  const po = new PerformanceObserver((list) => entries.push(...list.getEntries().map((e) => ({ startMs: e.startTime, durationMs: e.duration }))));
  po.observe({ entryTypes: ["longtask"] });
  const r = await fn();
  await sleep(50); // 让 longtask 条目派发
  po.disconnect();
  return { ...r, longTasks: entries };
}
window.__probe = {
  hasProfiler: typeof Profiler === "function",
  async e1Idle(ms) {
    const p = new Profiler({ sampleInterval: 10, maxBufferSize: 6000 });
    const eff = p.sampleInterval;
    const t0 = performance.now();
    await sleep(ms);
    const trace = await p.stop();
    return { effectiveIntervalMs: eff, ...inWindowStats(trace, { startMs: t0, durationMs: ms }), traceSamples: (trace.samples||[]).length };
  },
  async e2BusyJs(ms) {
    const p = new Profiler({ sampleInterval: 10, maxBufferSize: 6000 });
    const r = await withLongTasks(async () => busyJs(ms));
    const trace = await p.stop();
    const win = r.longTasks.find((w) => w.durationMs >= ms * 0.9) || { startMs: r.startMs, durationMs: r.durationMs };
    return { longTasks: r.longTasks.map((w) => ({ startMs: +w.startMs.toFixed(1), durationMs: +w.durationMs.toFixed(1) })), ...inWindowStats(trace, win), traceSamples: (trace.samples||[]).length };
  },
  async e3BusyLayout(ms, n) {
    const p = new Profiler({ sampleInterval: 10, maxBufferSize: 6000 });
    const r = await withLongTasks(async () => busyLayout(ms, n));
    const trace = await p.stop();
    const win = r.longTasks.find((w) => w.durationMs >= ms * 0.9) || { startMs: r.startMs, durationMs: r.durationMs };
    return { scans: r.scans, blocks: n, longTasks: r.longTasks.map((w) => ({ startMs: +w.startMs.toFixed(1), durationMs: +w.durationMs.toFixed(1) })), ...inWindowStats(trace, win), traceSamples: (trace.samples||[]).length };
  },
  async e4BufferFullMidTask(ms, maxBufferSize) {
    const p = new Profiler({ sampleInterval: 10, maxBufferSize });
    let fullAt = null;
    p.addEventListener("samplebufferfull", () => { fullAt = performance.now(); });
    const r = busyJs(ms);
    const taskEnd = performance.now();
    await sleep(30); // 给事件一个宏任务的机会
    const trace = await p.stop();
    const ts = (trace.samples||[]).map((s) => s.timestamp);
    const last = ts.length ? Math.max(...ts) : null;
    return {
      maxBufferSize, taskStartMs: +r.startMs.toFixed(1), taskEndMs: +taskEnd.toFixed(1), taskMs: +r.durationMs.toFixed(1),
      traceSamples: ts.length,
      lastSampleMs: last === null ? null : +last.toFixed(1),
      lastSampleOffsetIntoTaskMs: last === null ? null : +(last - r.startMs).toFixed(1),
      bufferFullEventAtMs: fullAt === null ? null : +fullAt.toFixed(1),
      bufferFullFiredAfterTaskEnd: fullAt !== null && fullAt >= taskEnd,
      samplesLostAfterFullMs: last === null ? null : +(taskEnd - last).toFixed(1),
    };
  },
  // E6: 复现 crash.ts 的生产时序——一个会在长任务中途满的 profiler + samplebufferfull
  // rollover 处理器 (同 startLongTaskProfiler) + PerformanceObserver(longtask) 回调里
  // 像 collectLongTaskFrames 一样 stop 当前 activeProfiler。回答: 主线程解冻后谁先跑,
  // collect 拿到的 trace 里有多少窗口内样本。
  async e6ProductionSequence(ms, maxBufferSize) {
    const log = [];
    let active = null;
    const start = () => {
      const p = new Profiler({ sampleInterval: 10, maxBufferSize });
      p.addEventListener("samplebufferfull", () => {
        log.push({ ev: "samplebufferfull", atMs: +performance.now().toFixed(1), wasActive: active === p });
        if (active !== p) return;
        active = null;
        void p.stop().catch(() => {});
        start();
      });
      active = p;
    };
    start();
    const t0 = performance.now();
    let collected = null;
    const done = new Promise((resolve) => {
      const po = new PerformanceObserver(async (list) => {
        const entries = list.getEntries().map((e) => ({ startMs: e.startTime, durationMs: e.duration }));
        const win = entries.find((w) => w.durationMs >= ms * 0.9);
        if (!win) return;
        log.push({ ev: "po.longtask", atMs: +performance.now().toFixed(1), windowStartMs: +win.startMs.toFixed(1), windowMs: +win.durationMs.toFixed(1) });
        const p = active; active = null;
        const trace = await p.stop();
        collected = inWindowStats(trace, win);
        po.disconnect();
        resolve();
      });
      po.observe({ entryTypes: ["longtask"] });
    });
    busyJs(ms);
    const taskEnd = performance.now();
    await Promise.race([done, sleep(1500)]);
    return { maxBufferSize, taskStartMs: +t0.toFixed(1), taskEndMs: +taskEnd.toFixed(1), order: log, collected };
  },
  // E6b: 同 E6, 但忙循环放进真正的页面宏任务 (setTimeout), 让 longtask 条目稳定派发。
  async e6bProductionSequence(ms, maxBufferSize) {
    const log = [];
    let active = null;
    const start = () => {
      const p = new Profiler({ sampleInterval: 10, maxBufferSize });
      p.addEventListener("samplebufferfull", () => {
        log.push({ ev: "samplebufferfull", atMs: +performance.now().toFixed(1), wasActive: active === p });
        if (active !== p) return;
        active = null;
        void p.stop().catch(() => {});
        start();
      });
      active = p;
    };
    start();
    let collected = null;
    let taskEnd = 0;
    const done = new Promise((resolve) => {
      const po = new PerformanceObserver(async (list) => {
        const entries = list.getEntries().map((e) => ({ startMs: e.startTime, durationMs: e.duration }));
        const win = entries.find((w) => w.durationMs >= ms * 0.9);
        if (!win) return;
        log.push({ ev: "po.longtask", atMs: +performance.now().toFixed(1), windowStartMs: +win.startMs.toFixed(1), windowMs: +win.durationMs.toFixed(1) });
        const p = active; active = null;
        const trace = await p.stop();
        collected = inWindowStats(trace, win);
        po.disconnect();
        resolve();
      });
      po.observe({ entryTypes: ["longtask"] });
    });
    const t0 = await new Promise((r) => setTimeout(() => { const s = performance.now(); busyJs(ms); taskEnd = performance.now(); r(s); }, 0));
    await Promise.race([done, sleep(1500)]);
    return { maxBufferSize, taskStartMs: +t0.toFixed(1), taskEndMs: +taskEnd.toFixed(1), order: log, collected };
  },
};
</script></body></html>`;

const server = http.createServer((req, res) => {
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
try {
  const page = await browser.newPage();
  await page.goto(`http://127.0.0.1:${port}/`, { waitUntil: "load" });
  const version = browser.version();
  const hasProfiler = await page.evaluate(() => window.__probe.hasProfiler);
  out("env", { chromium: version, hasProfiler, platform: process.platform });
  if (!hasProfiler) throw new Error("Profiler API unavailable (Document-Policy header not honoured?)");

  out("E1.idle", await page.evaluate((ms) => window.__probe.e1Idle(ms), 2000));
  out("E2.busyJs", await page.evaluate((ms) => window.__probe.e2BusyJs(ms), 3000));
  out("E3.busyLayout", await page.evaluate(([ms, n]) => window.__probe.e3BusyLayout(ms, n), [3000, 1500]));
  out("E4.bufferFullMidTask", await page.evaluate(([ms, cap]) => window.__probe.e4BufferFullMidTask(ms, cap), [3000, 40]));
  out("E6.productionSequence", await page.evaluate(([ms, cap]) => window.__probe.e6ProductionSequence(ms, cap), [4000, 60]));
  out("E6b.productionSequenceMacrotask", await page.evaluate(([ms, cap]) => window.__probe.e6bProductionSequence(ms, cap), [4000, 60]));
  // E5: 生产常量推算 (不实跑 60s): 空闲也采样 ⇒ 缓冲寿命 = 6000 × 有效间隔, 与负载无关。
  const e1 = await page.evaluate((ms) => window.__probe.e1Idle(ms), 1000);
  const effective = e1.effectiveIntervalMs || 10;
  out("E5.productionRollover", {
    sampleIntervalMs: 10, effectiveIntervalMs: effective, maxBufferSize: 6000,
    bufferLifetimeS: +((6000 * effective) / 1000).toFixed(1),
    note: "rollover 丢弃旧 trace; 缓冲若在长任务中途满, 采样静默停止且新 profiler 要等任务结束后的宏任务才能启动",
  });
} finally {
  await browser.close();
  server.close();
}
