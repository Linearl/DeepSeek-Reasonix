// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1730-ring-cache-single-source.test.tsx
//
// tl1730 验收钉（方案块1：cacheHit 单源收敛·事件族唯一发射点+双源告警）：
//   ① 前端消费面生产读取点收敛：上下文环「turn hit」行的 cacheHit 对只从
//      事件线（props）读取一处——源码契约 grep 自证（静默兜底摘除、快照对
//      只存在于 debug 对照路径）；
//   ② 显示值与事件值逐字节一致（契约测试）；
//   ③ 分歧注入 → 结构化告警出现（console.warn 双通道形态），显示仍用事件值
//      （不静默取均值、不调和）；
//   ④ 对照判决纯函数 verdict 矩阵（none/absent/agree/diverge）。
//
// 反例（改前）：
//   ① props 缺席 + info 带过期一对 → 静默显示快照率（与实时读数矛盾）；
//   ② props 只报 hit 不报 miss（per-field ??）→ live hit 混 stale miss，
//      同一行率值跨源拼接。

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { ContextWindowRing, reportTurnCacheShadow } from "../components/ContextWindowRing";
import { LocaleProvider } from "../lib/i18n";
import { resetQueryCoalescing } from "../lib/queryCoalesce";
import type { ContextPanelInfo } from "../lib/types";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
    process.exitCode = 1;
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
  Object.defineProperty(globalThis, "navigator", {
    value: { language: "en-US", languages: ["en-US"] },
    configurable: true,
  });
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
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  globalThis.ResizeObserver = TestResizeObserver;
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: () => ({
      matches: true,
      media: "(prefers-reduced-motion: reduce)",
      onchange: null,
      addEventListener() {},
      removeEventListener() {},
      addListener() {},
      removeListener() {},
      dispatchEvent: () => false,
    }),
  });
  return dom;
}

function contextPanelInfo(overrides: Partial<ContextPanelInfo> = {}): ContextPanelInfo {
  return {
    usedTokens: 0,
    windowTokens: 0,
    promptTokens: 0,
    completionTokens: 0,
    totalTokens: 0,
    reasoningTokens: 0,
    cacheHitTokens: 0,
    cacheMissTokens: 0,
    sessionCacheHitTokens: 0,
    sessionCacheMissTokens: 0,
    sessionCompletionTokens: 0,
    requestCount: 0,
    elapsedMs: 0,
    sessionCost: 0,
    sessionCurrency: "",
    readFiles: [],
    changedFiles: [],
    ...overrides,
  };
}

function installContextPanelMock(fn: (tabId: string) => Promise<ContextPanelInfo>) {
  (window as unknown as { go: { main: { App: { ContextPanel: typeof fn } } } }).go = {
    main: {
      App: {
        ContextPanel: fn,
      },
    },
  };
}

async function renderRing(props: Partial<Parameters<typeof ContextWindowRing>[0]> = {}) {
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  let currentProps: Parameters<typeof ContextWindowRing>[0] = {
    enabled: true,
    tabId: "tab-a",
    context: { used: 10, window: 100, compactRatio: 0.8 },
    ...props,
  };
  const paint = async (nextProps: Partial<Parameters<typeof ContextWindowRing>[0]> = {}) => {
    currentProps = { ...currentProps, ...nextProps };
    await act(async () => {
      root.render(
        <LocaleProvider>
          <ContextWindowRing {...currentProps} />
        </LocaleProvider>,
      );
      await wait();
    });
  };
  await paint();
  return { root, rerender: paint };
}

async function openPopover() {
  const trigger = document.querySelector(".context-ring") as HTMLButtonElement;
  await act(async () => {
    trigger.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: null }));
    await wait(220); // hover 打开延迟 200ms。
  });
}

function turnCacheRateRow(): string | null {
  const rows = [...document.querySelectorAll(".context-ring-popover__row")];
  const row = rows.find((candidate) => candidate.querySelector(".context-ring-popover__label")?.textContent === "turn hit");
  return row?.querySelector(".context-ring-popover__value")?.textContent ?? null;
}

// ── ⓪ 源码契约：生产读取点收敛（grep 自证+下限断言）──
{
  const testDir = dirname(fileURLToPath(import.meta.url));
  const ringSource = readFileSync(resolve(testDir, "../components/ContextWindowRing.tsx"), "utf8");
  // 唯一生产读取点：显示对只读事件线一次。
  assert.match(ringSource, /const turnCacheHit = cacheHitTokens \?\? 0;/, "the display hit reads the live event prop at exactly one point");
  assert.match(ringSource, /const turnCacheMiss = cacheMissTokens \?\? 0;/, "the display miss reads the live event prop at exactly one point");
  assert.doesNotMatch(ringSource, /cacheHitTokens \?\? info/, "the silent per-field fallback is gone");
  assert.doesNotMatch(ringSource, /cacheMissTokens \?\? info/, "the silent per-field fallback is gone (miss)");
  // 快照对只活在 debug 对照路径（info 读取点各恰好一次）。
  eq((ringSource.match(/info\?\.cacheHitTokens/g) ?? []).length, 1, "the snapshot hit appears exactly once (the debug shadow path)");
  eq((ringSource.match(/info\?\.cacheMissTokens/g) ?? []).length, 1, "the snapshot miss appears exactly once (the debug shadow path)");
  assert.match(ringSource, /reportTurnCacheShadow\(/, "the popover compares the shadow pair through the verdict helper");
  ok(true, "source contract: one production read point, shadow demoted to the debug compare path");
}

// ── 对照判决 verdict 矩阵（纯函数）──
{
  const debugCalls: unknown[][] = [];
  const warnCalls: unknown[][] = [];
  const realDebug = console.debug;
  const realWarn = console.warn;
  console.debug = (...args: unknown[]) => { debugCalls.push(args); };
  console.warn = (...args: unknown[]) => { warnCalls.push(args); };
  try {
    eq(reportTurnCacheShadow({ hit: 800, miss: 200 }, null), "none", "no shadow pair → no comparison");
    eq(reportTurnCacheShadow({ hit: 0, miss: 0 }, { hit: 300, miss: 700 }), "absent", "unreported authority (first-turn bridge window) stays unalarmed");
    eq(reportTurnCacheShadow({ hit: 800, miss: 200 }, { hit: 800, miss: 200 }), "agree", "matching clocks log the debug compare line");
    eq(reportTurnCacheShadow({ hit: 800, miss: 200 }, { hit: 100, miss: 900 }), "diverge", "clock divergence flips the structured warning");
    ok(debugCalls.length === 1, "exactly one debug compare line for the agree case");
    ok(warnCalls.length === 1, "exactly one structured warning for the diverge case");
    const warnPayload = JSON.stringify(warnCalls[0] ?? []);
    ok(warnPayload.includes("authority") && warnPayload.includes("shadow"), "the warning carries both structured pairs");
    ok(warnPayload.includes("displaying the event pair"), "the warning states the display keeps the event pair (no averaging)");
  } finally {
    console.debug = realDebug;
    console.warn = realWarn;
  }
}

console.log("\nring turn cache single source (tl1730)");

// ── ① 显示值与事件值逐字节一致；info 过期对不渗入 ──
{
  resetQueryCoalescing();
  const dom = installDom();
  installContextPanelMock(async () => contextPanelInfo({ cacheHitTokens: 100, cacheMissTokens: 900 }));
  const handle = await renderRing({ cacheHitTokens: 800, cacheMissTokens: 200 });
  await openPopover();
  eq(turnCacheRateRow(), "80.00%", "the displayed rate is byte-identical to the event pair (800/200)");
  await act(async () => { handle.root.unmount(); });
  dom.window.close();
}

// ── ② props 缺席：info 过期对不再静默兜底 → "-"（诚实缺席）──
{
  resetQueryCoalescing();
  const dom = installDom();
  installContextPanelMock(async () => contextPanelInfo({ cacheHitTokens: 300, cacheMissTokens: 700 }));
  const handle = await renderRing({});
  await openPopover();
  eq(turnCacheRateRow(), "-", "no live pair renders '-' instead of the stale snapshot rate");
  await act(async () => { handle.root.unmount(); });
  dom.window.close();
}

// ── ③ 反例锚（改前拼接 bug）：props 只报 hit → 不得混入 info 的 miss ──
{
  resetQueryCoalescing();
  const dom = installDom();
  installContextPanelMock(async () => contextPanelInfo({ cacheHitTokens: 100, cacheMissTokens: 900 }));
  // live hit 800 + live miss 缺失：改后率值 = 800/800 = 100%；
  // 改前（per-field ??）会拼成 800/(800+900) ≈ 47.06%。
  const handle = await renderRing({ cacheHitTokens: 800 });
  await openPopover();
  eq(turnCacheRateRow(), "100.00%", "a partial live pair never mixes with snapshot values inside one rate");
  await act(async () => { handle.root.unmount(); });
  dom.window.close();
}

// ── ④ 分歧注入 → 结构化告警出现，显示仍用事件值（方案验收③）──
{
  resetQueryCoalescing();
  const dom = installDom();
  installContextPanelMock(async () => contextPanelInfo({ cacheHitTokens: 100, cacheMissTokens: 900 }));
  const warnCalls: string[] = [];
  const debugCalls: string[] = [];
  const realWarn = console.warn;
  const realDebug = console.debug;
  console.warn = (...args: unknown[]) => { warnCalls.push(args.map(String).join(" ")); };
  console.debug = (...args: unknown[]) => { debugCalls.push(args.map(String).join(" ")); };
  try {
    const handle = await renderRing({ cacheHitTokens: 800, cacheMissTokens: 200 });
    await openPopover();
    eq(turnCacheRateRow(), "80.00%", "under divergence the display still shows the event pair");
    ok(warnCalls.some((line) => line.includes("authority/shadow mismatch")), "the structured divergence warning fired");
    await act(async () => { handle.root.unmount(); });
  } finally {
    console.warn = realWarn;
    console.debug = realDebug;
  }
  dom.window.close();
}

// ── ⑤ 两钟一致 → 只出 debug 对照行，无告警 ──
{
  resetQueryCoalescing();
  const dom = installDom();
  installContextPanelMock(async () => contextPanelInfo({ cacheHitTokens: 800, cacheMissTokens: 200 }));
  const warnCalls: string[] = [];
  const realWarn = console.warn;
  console.warn = (...args: unknown[]) => { warnCalls.push(args.map(String).join(" ")); };
  try {
    const handle = await renderRing({ cacheHitTokens: 800, cacheMissTokens: 200 });
    await openPopover();
    eq(turnCacheRateRow(), "80.00%", "agreement keeps the event-pair rate on screen");
    eq(warnCalls.length, 0, "no divergence warning when the clocks agree");
    await act(async () => { handle.root.unmount(); });
  } finally {
    console.warn = realWarn;
  }
  dom.window.close();
}

// ── ⑥ props 对零值（provider 未报缓存）照常 "-"，语义不变 ──
{
  resetQueryCoalescing();
  const dom = installDom();
  installContextPanelMock(async () => contextPanelInfo());
  const handle = await renderRing({ cacheHitTokens: 0, cacheMissTokens: 0 });
  await openPopover();
  eq(turnCacheRateRow(), "-", "an explicit zero pair keeps the not-reported '-' face");
  await act(async () => { handle.root.unmount(); });
  dom.window.close();
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
