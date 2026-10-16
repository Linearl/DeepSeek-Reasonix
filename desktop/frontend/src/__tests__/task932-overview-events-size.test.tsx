// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/task932-overview-events-size.test.tsx
// 任务 932: 概览面板（ContextPanel）最下方的 events 文件大小行。
// 验收口径：
//   1. labFlags.perfMonitor（监控卡 perf monitor 行）关闭 → 概览 DOM 零变化
//      （无 context-events-size 节点）且零桥调用（铁律 2）；
//   2. 开启 → 行出现，数值来自 PerfTimeSeries 投影的最新点 eventsMb，
//      人性化 KB/MB 渲染；
//   3. 三语 locale 齐全；
//   4. formatEventsSize 纯逻辑（KB/MB 分界、非法值兜底）。

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { ContextPanel, EventsSizeRow, formatEventsSize } from "../components/ContextPanel";
import { LocaleProvider } from "../lib/i18n";
import { applyLabFlags } from "../lib/labFlags";
import { readFileSync } from "node:fs";
import { join } from "node:path";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

function wait(ms = 0): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

class TestResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

function installDom() {
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  globalThis.Node = dom.window.Node;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.Event = dom.window.Event;
  globalThis.ResizeObserver = TestResizeObserver;
  return dom;
}

// --- 1. pure formatter -------------------------------------------------------

console.log("\ntask 932 — formatEventsSize");

eq(formatEventsSize(1477.6), "1477.6 MB", "large value renders one-decimal MB");
eq(formatEventsSize(1), "1.0 MB", "exactly 1 MB stays in MB");
eq(formatEventsSize(0.5), "512 KB", "sub-MB renders in KB");
eq(formatEventsSize(0.001), "1 KB", "tiny value floors to whole KB");
eq(formatEventsSize(0), "-", "zero renders dash");
eq(formatEventsSize(-5), "-", "negative renders dash");
eq(formatEventsSize(Number.NaN), "-", "NaN renders dash");

// --- 2. locale keys ----------------------------------------------------------

console.log("\ntask 932 — locale keys");

const localeDir = join(import.meta.dirname, "..", "locales");
for (const name of ["zh", "zh-TW", "en"]) {
  const source = readFileSync(join(localeDir, `${name}.ts`), "utf-8");
  ok(source.includes('"context.eventsSizeLabel"'), `${name} carries context.eventsSizeLabel`);
}

// --- 3. rendered row ---------------------------------------------------------

console.log("\ntask 932 — events size row");

const dom = installDom();
const mainApp: Record<string, (...args: unknown[]) => Promise<unknown>> = {
  ContextPanel: async () => ({
    usedTokens: 0, windowTokens: 0, promptTokens: 0, completionTokens: 0, totalTokens: 0,
    reasoningTokens: 0, cacheHitTokens: 0, cacheMissTokens: 0, sessionCacheHitTokens: 0,
    sessionCacheMissTokens: 0, sessionCompletionTokens: 0, requestCount: 0, elapsedMs: 0,
    sessionCost: 0, readFiles: [], changedFiles: [],
  }),
};
(window as unknown as { go: { main: { App: Record<string, unknown> } } }).go = { main: { App: mainApp } };

let perfSeriesCalls = 0;
mainApp.PerfTimeSeries = async () => {
  perfSeriesCalls += 1;
  return {
    enabled: true,
    available: true,
    intervalSeconds: 5,
    points: [
      { ts: "2026-10-17T10:00:00Z", workingSetMb: 100, heapInuseMb: 50, goroutines: 10, handles: 5, cpuPercent: 3, eventsMb: 1477.6 },
      { ts: "2026-10-17T10:01:00Z", workingSetMb: 101, heapInuseMb: 51, goroutines: 11, handles: 5, cpuPercent: 4, eventsMb: 1480.2 },
    ],
  };
};

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("missing root");
const root: Root = createRoot(rootEl);

// Case 1: gate OFF — no DOM node, zero bridge calls (零变化验收).
applyLabFlags({});
await act(async () => {
  root.render(
    <LocaleProvider>
      <EventsSizeRow />
    </LocaleProvider>,
  );
  await wait(20);
});
eq(document.querySelector('[data-testid="context-events-size"]'), null, "gate off renders no events-size node");
eq(perfSeriesCalls, 0, "gate off issues zero PerfTimeSeries calls");

// Case 2: gate ON — row appears, reads the LAST point's eventsMb, KB/MB format.
applyLabFlags({ perfMonitor: true });
await act(async () => {
  await wait(30);
});
const row = document.querySelector('[data-testid="context-events-size"]');
ok(row !== null, "gate on renders the events-size node");
eq(row?.textContent ?? "", "events 文件大小1480.2 MB", "row shows label + latest-point value in MB");
eq(perfSeriesCalls, 1, "gate on issues exactly one PerfTimeSeries call (no polling)");

// Case 3: live flip OFF — the row disappears again (settings-save apply).
applyLabFlags({});
await act(async () => {
  await wait(20);
});
eq(document.querySelector('[data-testid="context-events-size"]'), null, "live flip off removes the row again");

// Case 4: empty reading (sampler on but no points yet) — dash placeholder.
applyLabFlags({ perfMonitor: true });
mainApp.PerfTimeSeries = async () => {
  perfSeriesCalls += 1;
  return { enabled: true, available: false, intervalSeconds: 5, points: [] };
};
await act(async () => {
  root.render(
    <LocaleProvider>
      <EventsSizeRow />
    </LocaleProvider>,
  );
  await wait(30);
});
const emptyRow = document.querySelector('[data-testid="context-events-size"]');
ok(emptyRow !== null, "empty reading still renders the row");
ok((emptyRow?.textContent ?? "").endsWith("-"), "empty reading shows dash placeholder");

// Case 5: integration — full ContextPanel with gate off stays unchanged
// (snapshot equivalence: no events-size node anywhere in the panel).
applyLabFlags({});
perfSeriesCalls = 0;
await act(async () => {
  root.render(
    <LocaleProvider>
      <ContextPanel tabId="tab-932" context={{ used: 10, window: 100 }} />
    </LocaleProvider>,
  );
  await wait(30);
});
eq(document.querySelector('[data-testid="context-events-size"]'), null, "full ContextPanel with gate off has no events-size node");
eq(perfSeriesCalls, 0, "full ContextPanel with gate off issues zero PerfTimeSeries calls");
ok((document.querySelector(".context-panel")?.textContent ?? "").length > 0, "the rest of the overview still renders");

// --- summary -----------------------------------------------------------------

console.log(`\n${passed} passed, ${failed} failed`);
// Full-ContextPanel cases arm PlanUsageCard polling timers inside jsdom; exit
// explicitly so they cannot hold the event loop open after the summary.
process.exit(failed > 0 || passed === 0 ? 1 : 0);
