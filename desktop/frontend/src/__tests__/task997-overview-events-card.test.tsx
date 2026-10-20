// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/task997-overview-events-card.test.tsx
// 任务 997: 概览面板 events 大小卡片化（932 前身样式升级）。
// 验收口径：
//   1. 卡片形态：context-panel__section 圆角卡（与会话指标/用量分析同族
//      class），两行数据（全部对话 + 当前对话），无裸文本残留；
//   2. 两项数据源：全部对话 = PerfTimeSeries 最新点 eventsMb（932 沿用）；
//      当前对话 = TabSessionEventsSize(tabId)（Go 侧单文件 stat）；
//   3. GB 步进：全部对话合计（GB 量级）渲染为一位小数 GB，不再 1423.0 MB；
//   4. 保存换态：refreshKey 递增（turn_done）→ 两数据源一并重取（当前对话
//      数值随保存更新，非仅启动时）；
//   5. 开关沿用 932（labFlags.perfMonitor）：关 = 零 DOM 零桥调用。

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { EventsSizeCard } from "../components/ContextPanel";
import { LocaleProvider } from "../lib/i18n";
import { applyLabFlags } from "../lib/labFlags";

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

console.log("\ntask 997 — overview events size card");

const dom = installDom();
const mainApp: Record<string, (...args: unknown[]) => Promise<unknown>> = {};
(window as unknown as { go: { main: { App: Record<string, unknown> } } }).go = { main: { App: mainApp } };

let perfSeriesCalls = 0;
let totalMb = 1423.4;
mainApp.PerfTimeSeries = async () => {
  perfSeriesCalls += 1;
  return {
    enabled: true,
    available: true,
    intervalSeconds: 5,
    points: [{ ts: "2026-10-19T10:00:00Z", workingSetMb: 100, heapInuseMb: 50, goroutines: 10, handles: 5, cpuPercent: 3, eventsMb: totalMb }],
  };
};
let tabEventsCalls = 0;
let currentBytes = 154_800_000; // ≈147.6 MB
mainApp.TabSessionEventsSize = async () => {
  tabEventsCalls += 1;
  return currentBytes;
};

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("missing root");
const root: Root = createRoot(rootEl);

// --- 1. gate off: zero DOM, zero bridge calls --------------------------------

applyLabFlags({});
await act(async () => {
  root.render(
    <LocaleProvider>
      <EventsSizeCard tabId="tab-997" />
    </LocaleProvider>,
  );
  await wait(20);
});
eq(document.querySelector('[data-testid="context-events-size"]'), null, "gate off renders no card");
eq(perfSeriesCalls, 0, "gate off issues zero PerfTimeSeries calls");
eq(tabEventsCalls, 0, "gate off issues zero TabSessionEventsSize calls");

// --- 2. gate on: card + two rows + both data sources --------------------------

applyLabFlags({ perfMonitor: true });
await act(async () => {
  await wait(30);
});
const card = document.querySelector('[data-testid="context-events-size"]');
ok(card !== null, "gate on renders the card");
eq(card?.className ?? "", "context-panel__section context-panel__events-size-card", "card reuses the section card class family (圆角卡同族)");
const rows = card?.querySelectorAll(".context-panel__events-size-row") ?? [];
eq(rows.length, 2, "card renders exactly two data rows");
eq(rows[0]?.querySelector("span")?.textContent ?? "", "全部对话 events", "row 1 label = 全部对话 events");
eq(rows[0]?.querySelector("strong")?.textContent ?? "", "1.4 GB", "row 1 value = total eventsMb in GB (one decimal)");
eq(rows[1]?.querySelector("span")?.textContent ?? "", "当前对话 events", "row 2 label = 当前对话 events");
eq(rows[1]?.querySelector("strong")?.textContent ?? "", "147.6 MB", "row 2 value = tab log bytes converted to MB");
eq(perfSeriesCalls, 1, "one PerfTimeSeries call per load (no polling)");
eq(tabEventsCalls, 1, "one TabSessionEventsSize call per load (single stat, no walk)");

// --- 3. refreshKey bump: both sources re-fetch (保存换态) ----------------------

totalMb = 1430.0;
currentBytes = 160_000_000; // ≈152.6 MB
await act(async () => {
  root.render(
    <LocaleProvider>
      <EventsSizeCard tabId="tab-997" refreshKey={1} />
    </LocaleProvider>,
  );
  await wait(30);
});
eq(perfSeriesCalls, 2, "refreshKey bump re-fetches the total reading");
eq(tabEventsCalls, 2, "refreshKey bump re-fetches the current-session reading (随保存更新)");
const rowsAfter = document.querySelectorAll('[data-testid="context-events-size"] .context-panel__events-size-row strong');
eq(rowsAfter[0]?.textContent ?? "", "1.4 GB", "total row reflects the fresh sample");
eq(rowsAfter[1]?.textContent ?? "", "152.6 MB", "current row reflects the fresh stat");

// --- 4. tab switch: current-session source re-stats ---------------------------

await act(async () => {
  root.render(
    <LocaleProvider>
      <EventsSizeCard tabId="tab-other" refreshKey={1} />
    </LocaleProvider>,
  );
  await wait(30);
});
eq(tabEventsCalls, 3, "tabId change re-stats the (new) current session");

// --- 5. live flip off: zero DOM again -----------------------------------------

applyLabFlags({});
await act(async () => {
  await wait(20);
});
eq(document.querySelector('[data-testid="context-events-size"]'), null, "live flip off removes the card");

// --- summary ------------------------------------------------------------------

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed > 0 || passed === 0 ? 1 : 0);
