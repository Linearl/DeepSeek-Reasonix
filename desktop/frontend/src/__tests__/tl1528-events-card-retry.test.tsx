// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1528-events-card-retry.test.tsx
// 任务 tl1528: 概览「全部对话 events」取数失败不再静默吞错。
// 验收口径：
//   1. 失败留痕：PerfTimeSeries / TabSessionEventsSize 每次失败 console.error
//      带 [events-size] 前缀，可从 devtools console 查；
//   2. 退避重试：首败后按 500ms/2s 退避重试（共 3 次尝试），重试成功即恢复
//      正常显示（不再一次失败恒 '-'）；
//   3. 重试耗尽：行内显示「取数失败」提示文案，非裸 '-'；
//   4. available=false（桥调用成功但窗口内无采样点）：行内显示「暂无采样
//      数据」提示文案，且不触发重试（definitive answer 只调一次）。

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

async function waitForCond(cond: () => boolean, timeoutMs = 6000): Promise<boolean> {
  const start = Date.now();
  while (!cond() && Date.now() - start < timeoutMs) {
    await wait(50);
  }
  return cond();
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

console.log("\ntl1528 — overview events card fetch failure logging + retry");

const dom = installDom();
const mainApp: Record<string, (...args: unknown[]) => Promise<unknown>> = {};
(window as unknown as { go: { main: { App: Record<string, unknown> } } }).go = { main: { App: mainApp } };

let perfSeriesCalls = 0;
let perfFailPlan: number[] = []; // 第 N 次调用失败（1-based）
let perfAlwaysFail = false; // 之后的每一次调用都失败（重试耗尽场景）
let perfAvailable = true; // false = 桥调用成功但 available=false（场景 4）
let totalMb = 1423.4;
mainApp.PerfTimeSeries = async () => {
  perfSeriesCalls += 1;
  if (perfAlwaysFail || perfFailPlan.includes(perfSeriesCalls)) throw new Error("bridge down (simulated)");
  if (!perfAvailable) {
    return { enabled: true, available: false, intervalSeconds: 5, points: [] };
  }
  return {
    enabled: true,
    available: true,
    intervalSeconds: 5,
    points: [{ ts: "2026-11-10T10:00:00Z", workingSetMb: 100, heapInuseMb: 50, goroutines: 10, handles: 5, cpuPercent: 3, eventsMb: totalMb }],
  };
};
let tabEventsCalls = 0;
let tabFailPlan: number[] = [];
let currentBytes = 154_800_000; // ≈147.6 MB
mainApp.TabSessionEventsSize = async () => {
  tabEventsCalls += 1;
  if (tabFailPlan.includes(tabEventsCalls)) throw new Error("bridge down (simulated)");
  return currentBytes;
};

// console.error 留痕捕获（tl1528 验收①）
const errorCalls: unknown[][] = [];
const originalConsoleError = console.error;
console.error = (...args: unknown[]) => {
  errorCalls.push(args);
};
const eventsSizeErrors = () => errorCalls.filter((args) => String(args[0]).includes("[events-size]"));

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("missing root");
const root: Root = createRoot(rootEl);

// --- 1. gate off: zero calls ---------------------------------------------------

applyLabFlags({});
await act(async () => {
  root.render(
    <LocaleProvider>
      <EventsSizeCard tabId="tab-1528" />
    </LocaleProvider>,
  );
  await wait(20);
});
eq(perfSeriesCalls, 0, "gate off issues zero PerfTimeSeries calls");

// --- 2. fail once then succeed: logged + retried back to normal display -------
// （判例同款时序：flip 在 act 外、act 内只 wait；断言轮询放后续 act——
//   act 内重复 root.render 同 props 会扰动首败重试的 effect 生命周期。）

perfFailPlan = [1];
tabFailPlan = [1];
applyLabFlags({ perfMonitor: true });
await act(async () => {
  await wait(30);
});
await act(async () => {
  await waitForCond(() => perfSeriesCalls >= 2 && tabEventsCalls >= 2);
});
eq(perfSeriesCalls, 2, "first PerfTimeSeries failure is retried (2nd call succeeds)");
eq(tabEventsCalls, 2, "first TabSessionEventsSize failure is retried (2nd call succeeds)");
ok(eventsSizeErrors().length >= 2, "both first failures logged via console.error with [events-size] prefix");
ok(eventsSizeErrors().some((args) => String(args[0]).includes("PerfTimeSeries")), "PerfTimeSeries failure log names the source");
ok(eventsSizeErrors().some((args) => String(args[0]).includes("TabSessionEventsSize")), "TabSessionEventsSize failure log names the source");
const rowsRecovered = document.querySelectorAll('[data-testid="context-events-size"] .context-panel__events-size-row strong');
eq(rowsRecovered[0]?.textContent ?? "", "1.4 GB", "after retry success the total row shows the real value (不再恒 '-')");
eq(rowsRecovered[1]?.textContent ?? "", "147.6 MB", "after retry success the current row shows the real value");

// --- 3. retries exhausted: 3 attempts, each logged; row shows hint text (非裸 '-') --

perfAlwaysFail = true; // 之后所有调用都失败
// 判例时序（探针实证）：act 的 async 回调内 root.render 的 effect 要等 act
// 退出才 flush——render 与 waitForCond 必须拆两个 act，否则轮询空转 6s。
await act(async () => {
  root.render(
    <LocaleProvider>
      <EventsSizeCard tabId="tab-1528" refreshKey={1} />
    </LocaleProvider>,
  );
  await wait(0);
});
await act(async () => {
  await waitForCond(() => perfSeriesCalls >= 5); // 2(此前) + 1 首试 + 2 重试
});
eq(perfSeriesCalls, 5, "persistent failure stops at 3 attempts (initial + 2 backoff retries), no polling");
const exhaustedLogs = eventsSizeErrors().filter((args) => String(args[0]).includes("failed after"));
ok(exhaustedLogs.length >= 1, "retry exhaustion logged with final 'failed after N attempt(s)' line");
const rowsExhausted = document.querySelectorAll('[data-testid="context-events-size"] .context-panel__events-size-row strong');
eq(rowsExhausted[0]?.textContent ?? "", "取数失败", "exhausted total row shows the error hint text (非裸 '-')");
ok(rowsExhausted[0]?.textContent !== "-", "exhausted total row is not a bare '-'");

console.error = originalConsoleError;

// --- 4. available=false: hint text, no retry (definitive answer) ----------------

perfAlwaysFail = false;
perfAvailable = false;
await act(async () => {
  root.render(
    <LocaleProvider>
      <EventsSizeCard tabId="tab-1528" refreshKey={2} />
    </LocaleProvider>,
  );
  await wait(0);
});
await act(async () => {
  await wait(1200); // 若误重试会在 +500ms 产生第二次调用
});
eq(perfSeriesCalls, 6, "available=false is a definitive answer: exactly one call, no retry");
const rowsUnavailable = document.querySelectorAll('[data-testid="context-events-size"] .context-panel__events-size-row strong');
eq(rowsUnavailable[0]?.textContent ?? "", "暂无采样数据", "available=false total row shows the unavailable hint text (非裸 '-')");
ok(rowsUnavailable[0]?.textContent !== "-", "available=false total row is not a bare '-'");
eq(rowsUnavailable[1]?.textContent ?? "", "147.6 MB", "current row unaffected by the total source's unavailable state");

// --- summary ------------------------------------------------------------------

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed > 0 || passed === 0 ? 1 : 0);
