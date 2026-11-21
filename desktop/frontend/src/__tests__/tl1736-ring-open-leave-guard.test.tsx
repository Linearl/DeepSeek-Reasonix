// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1736-ring-open-leave-guard.test.tsx
//
// tl1736 — 圆环 hover 对称防抖（用户 20261120 实测：浮层在指针停靠左/居中
// 位置时出现-消失循环 ~300ms 周期，靠右不闪，间歇性）。
//
// 根因：触发元素 onEnter 200ms 开 / onLeave 120ms 关；浮层（或其数据异步
// 加载后 AnchoredPopover 自动重定位）覆盖指针所在环带 → 浏览器对静止指针
// 补发 mouseleave → 关 → 指针重新 enter → 再开，形成遮挡回路。tl1239③
// 只治「勾选后重定位」的浮层侧 leave，普通打开路径的触发区 onLeave 无豁免。
//
// 修复（用户拍板）：对称防抖——浮层打开后 N ms（OPEN_LEAVE_GUARD_MS=400）
// 内忽略触发区 leave；窗口后真移出照常关（防永久锁死）。不做定位避让。
//
// 验收钉：
//   A1 防抖钉（hover 路径）：open 后窗内补发 leave 不关；
//   A2 防抖钉（click 直开路径）：同上；
//   A3 锁死防钉：窗口过期后补发 leave 照常关（120ms 延迟 + 关闭动画后卸载）；
//   A4 tl1239③ 零回退：勾选豁免语义在改动后原样成立；
//   A5 回归保护：快速划过（enter 未满 200ms 即 leave）浮层不开（防抖只
//      在浮层已开后生效，不改变既有 enter/leave 时序）；
//   A6 窗内忽略后窗外二次 leave 照常关（重定位二次补发场景不断路）。

import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { ContextWindowRing } from "../components/ContextWindowRing";
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
  }
}

function wait(ms = 0): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

class TestResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

let activeRoot: ReturnType<typeof createRoot> | null = null;

function installDom() {
  try {
    activeRoot?.unmount();
  } catch {
    // root 已随旧 DOM 失效——忽略，继续重建环境。
  }
  activeRoot = null;
  // ContextPanel 在 queryCoalesce 白名单里，模块级 inflight 跨块存活会把
  // 上一块的 200ms 共享答案漏进本块——重装 DOM 前先清共享缓存（tl1239 套件
  // 同款）。
  resetQueryCoalescing();
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window;
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
      matches: false,
      media: "",
      onchange: null,
      addEventListener() {},
      removeEventListener() {},
      addListener() {},
      dispatchEvent: () => false,
    }),
  });
  return dom;
}

function baseInfo(): ContextPanelInfo {
  return {
    usedTokens: 10,
    windowTokens: 1_000_000,
    promptTokens: 0,
    completionTokens: 0,
    totalTokens: 0,
    reasoningTokens: 0,
    cacheHitTokens: 0,
    cacheMissTokens: 0,
    sessionCacheHitTokens: 0,
    sessionCacheMissTokens: 0,
    sessionCompletionTokens: 0,
    requestCount: 1,
    elapsedMs: 0,
    sessionCost: 0,
    sessionCurrency: "",
    readFiles: [],
    changedFiles: [],
  };
}

function installContextPanelMock(fn: (tabId: string) => Promise<ContextPanelInfo>) {
  (window as unknown as { go: { main: { App: { ContextPanel: typeof fn } } } }).go = {
    main: { App: { ContextPanel: fn } },
  };
}

async function renderRing(props: Partial<Parameters<typeof ContextWindowRing>[0]> = {}) {
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  activeRoot = root;
  const currentProps: Parameters<typeof ContextWindowRing>[0] = {
    enabled: true,
    tabId: "tab-tl1736",
    context: { used: 10, window: 1_000_000, compactRatio: 0.8 },
    ...props,
  };
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ContextWindowRing {...currentProps} />
      </LocaleProvider>,
    );
    await wait();
  });
  return { root };
}

function trigger(): HTMLButtonElement {
  const el = document.querySelector(".context-ring") as HTMLButtonElement | null;
  if (!el) throw new Error("missing ring trigger");
  return el;
}

// hover 打开浮层：mouseover（无 relatedTarget）触发 React onMouseEnter →
// 200ms enter 延迟后 open（此时对称护栏已在 timer 回调里武装）。
async function openViaHover() {
  await act(async () => {
    trigger().dispatchEvent(new window.MouseEvent("mouseover", { bubbles: true, cancelable: true }));
    await wait(260); // 越过 200ms enter 延迟 + 渲染帧
  });
}

async function openViaClick() {
  await act(async () => {
    trigger().click();
    await wait();
    await wait();
  });
}

// 触发区 leave 模拟：mouseout 不带 relatedTarget 即视为离开整条链
// （tl1168/tl1239 套件同款 hover 模式）。
async function leaveTrigger() {
  await act(async () => {
    trigger().dispatchEvent(new window.MouseEvent("mouseout", { bubbles: true, cancelable: true }));
    await wait();
  });
}

function popoverOpen(): boolean {
  const el = document.querySelector(".anchored-popover") as HTMLElement | null;
  return el != null && el.getAttribute("data-state") === "open";
}

async function closeAnimationSettled(): Promise<void> {
  // 分段 act 轮询等待卸载：触发区关闭链路有两个先后 timer（120ms onLeave
  // 延迟 → setOpen(false) → 140ms AnchoredPopover 关闭动画 → 卸载），act
  // 单段长 await 挂起期间 fire 的第二次 setState 不会被 flush（实测卡在
  // closing 态）——短段 act 让每步 setState 各自 flush；产品代码真实时序
  // ~280ms 完成卸载（探针实测），600ms 轮询上限留足余量。
  for (let i = 0; i < 10; i += 1) {
    await act(async () => {
      await wait(60);
    });
    if (document.querySelector(".context-ring-popover__inner") == null) return;
  }
}

// 护栏判定只与时刻差有关——窗内断言段冻结 Date.now 屏蔽 harness 耗时
// （act 开销可能让 400ms 窗「合法过期」，tl1239③ 套件同款）。
function freezeNow(): () => void {
  const realNow = Date.now;
  const frozenAt = realNow();
  (Date as unknown as { now: () => number }).now = () => frozenAt;
  return () => {
    (Date as unknown as { now: () => number }).now = realNow;
  };
}

const here = dirname(fileURLToPath(import.meta.url));

console.log("\ntl1736 — ring open-leave guard (symmetric debounce)");

// ── A5: 快速划过不开（防抖不改变未开语义） ─────────────────────────────────
{
  installDom();
  installContextPanelMock(async () => baseInfo());
  await renderRing({ tabId: "tab-tl1736-glance" });
  await act(async () => {
    trigger().dispatchEvent(new window.MouseEvent("mouseover", { bubbles: true, cancelable: true }));
    await wait(50); // 未满 200ms enter 延迟
    trigger().dispatchEvent(new window.MouseEvent("mouseout", { bubbles: true, cancelable: true }));
    await wait(260); // 越过 enter 延迟——enterTimer 已被 leave 清掉
  });
  ok(!popoverOpen(), "A5 快速划过（enter<200ms 即 leave）浮层不开");
}

// ── A1: 防抖钉（hover 路径）——open 后窗内补发 leave 不关 ────────────────────
{
  installDom();
  installContextPanelMock(async () => baseInfo());
  await renderRing({ tabId: "tab-tl1736-hover" });
  await openViaHover();
  ok(popoverOpen(), "A1 前置：hover 260ms 后浮层已开");
  const unfreeze = freezeNow();
  try {
    // 同一冻结时刻内模拟「浮层渲染压住指针 → 浏览器补发 mouseleave」。
    await leaveTrigger();
  } finally {
    unfreeze();
  }
  ok(popoverOpen(), "A1 open 后护栏窗内补发 leave 不关浮层（遮挡回路被堵）");
}

// ── A2: 防抖钉（click 直开路径） ────────────────────────────────────────────
{
  installDom();
  installContextPanelMock(async () => baseInfo());
  await renderRing({ tabId: "tab-tl1736-click" });
  await openViaClick();
  ok(popoverOpen(), "A2 前置：click 直开后浮层已开");
  const unfreeze = freezeNow();
  try {
    await leaveTrigger();
  } finally {
    unfreeze();
  }
  ok(popoverOpen(), "A2 click 直开路径窗内补发 leave 同样被护栏忽略");
}

// ── A3: 锁死防钉——窗口过期后 leave 照常关（防永久锁死） ─────────────────────
{
  installDom();
  installContextPanelMock(async () => baseInfo());
  await renderRing({ tabId: "tab-tl1736-lock" });
  await openViaHover();
  ok(popoverOpen(), "A3 前置：浮层已开（护栏已武装）");
  await act(async () => {
    await wait(480); // 真实越过 400ms 护栏窗（arm 时刻 + 260ms 已过 ~220ms）
  });
  await leaveTrigger(); // 此时补发 leave 不再受护栏豁免
  await closeAnimationSettled();
  ok(!popoverOpen(), "A3 护栏窗后补发 leave 照常关闭（120ms 延迟生效）");
  ok(
    document.querySelector(".context-ring-popover__inner") == null,
    "A3 关闭动画后面板卸载",
  );
}

// ── A6: 窗内忽略后窗外二次 leave 照常关（重定位二次补发不断路） ───────────────
{
  installDom();
  installContextPanelMock(async () => baseInfo());
  await renderRing({ tabId: "tab-tl1736-second" });
  await openViaHover();
  const unfreeze = freezeNow();
  try {
    await leaveTrigger(); // 窗内第一次补发——忽略
  } finally {
    unfreeze();
  }
  ok(popoverOpen(), "A6 前置：窗内第一次补发 leave 被忽略");
  await act(async () => {
    await wait(480); // 护栏窗过期
  });
  await leaveTrigger(); // 第二次补发（数据回来重定位再压指针 / 真移出）
  await closeAnimationSettled();
  ok(!popoverOpen(), "A6 窗后二次 leave 照常关闭（不会因一次豁免而续命）");
}

// ── A4: tl1239③ 零回退——勾选豁免语义原样成立 ───────────────────────────────
{
  installDom();
  installContextPanelMock(async () => ({
    ...baseInfo(),
    usedTokens: 711200,
    composition: {
      systemPromptTokens: 10700,
      builtinToolTokens: 11600,
      skillTokens: 0,
      mcpToolTokens: 2600,
      messageTokens: 337700,
      imageTokens: 11000,
      totalTokens: 711200,
      standingMemoryTokens: 261000,
      catalogTokens: 61000,
      runtimeTokens: 12000,
      recallTokens: 3600,
      directoryBytes: 50_000,
    },
  }));
  await renderRing({ tabId: "tab-tl1739-regress" });
  await openViaClick();
  const toggleLabel = document.querySelector(".context-composition__detail-toggle") as HTMLLabelElement | null;
  if (!toggleLabel) throw new Error("missing detail toggle label");
  const toggleInput = document.querySelector('[data-testid="composition-detail-toggle"]') as HTMLInputElement;
  const inner = document.querySelector(".context-ring-popover__inner") as HTMLElement;
  if (!inner) throw new Error("missing popover inner");
  // tl1239③ 手势序原样重放：mousedown 武装浮层侧护栏 → 勾选 → 浮层侧
  // mouseover/mouseout（勾选改高度 → 重定位补发）。
  const unfreeze = freezeNow();
  try {
    await act(async () => {
      toggleLabel.dispatchEvent(new MouseEvent("mousedown", { bubbles: true }));
      toggleInput.click();
      inner.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, cancelable: true }));
      inner.dispatchEvent(new MouseEvent("mouseout", { bubbles: true, cancelable: true }));
      await wait();
    });
  } finally {
    unfreeze();
  }
  ok(toggleInput.checked === true, "A4 前置：勾选已生效（明细视图切换）");
  ok(
    document.querySelector('[data-testid="composition-sections"]') != null,
    "A4 前置：三段式明细视图已切换",
  );
  ok(popoverOpen(), "A4 tl1239③：勾选后浮层侧补发 mouseleave 不关面板（零回退）");
  // 浮层侧豁免窗后真移出照常关（tl1239③ 既有语义第二半）。
  await act(async () => {
    await wait(450);
  });
  await act(async () => {
    inner.dispatchEvent(new window.MouseEvent("mouseout", { bubbles: true, cancelable: true }));
    await wait();
  });
  await closeAnimationSettled();
  ok(!popoverOpen(), "A4 tl1239③：浮层侧豁免窗后真移出照常关闭（零回退）");
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
