// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1239-detail-rework.test.tsx
//
// tl1239 — 1224b 细节返修四点（用户 20261029 06:3x 验收反馈）：
//   ① 明细 checkbox 移到面板头部行右端空白处（原「色条上方独立行」不顺手）；
//   ② 分段色条去灰改彩色（灰系无分段区分度；images 段配色规则此前缺失）；
//   ③ 勾选明细时 hover 面板保持打开（勾选改变浮层高度 → 自动重定位把浮层
//      从指针下移开 → 静止指针被补发 mouseleave 而误关），状态保持不变；
//   ④ 「跨会话记忆」行挂内容源解释 tooltip（Go 侧归属链核实＝
//      BackgroundDataBlock：钉住偏好/反馈条目 + 后台记忆事实索引，确为记忆
//      而非「跨会话消息」；internal/agent/context_composition.go ←
//      internal/control/session_context.go ← internal/memory/memory.go）。

import { readFileSync } from "node:fs";
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

function eq(actual: unknown, expected: unknown, label: string) {
  const same = actual === expected;
  if (same) ok(true, label);
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

// 上一块的 React root：installDom 时先卸载——旧 root 的 portal/监听器若跨
// installDom 存活，pending 渲染会被 React 全局调度器在新 document 上重新
// 落位（createPortal 重挂新 body、outside-click 监听重挂新 document），
// 旧浮层闯入新块的 querySelector 视野、旧监听器把新块 trigger 的点击判为
// 外部点击而误关旧浮层（探针实锤：outside-click 的 anchorIsTarget=false）。
let activeRoot: ReturnType<typeof createRoot> | null = null;

function installDom() {
  try {
    activeRoot?.unmount();
  } catch {
    // root 已随旧 DOM 失效——忽略，继续重建环境。
  }
  activeRoot = null;
  // tl1239: ContextPanel 在 queryCoalesce 白名单里，模块级 inflight 跨块存活
  // 会让上一块的 200ms 共享答案漏进本块——重装 DOM 前先清共享缓存。
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
    requestCount: 1,
    elapsedMs: 0,
    sessionCost: 0,
    sessionCurrency: "",
    readFiles: [],
    changedFiles: [],
  };
}

function tl1224Composition() {
  return {
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
    tabId: "tab-tl1239",
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

async function openPopover() {
  const trigger = document.querySelector(".context-ring") as HTMLButtonElement | null;
  if (!trigger) throw new Error("missing ring trigger");
  await act(async () => {
    trigger.click();
    await wait();
    await wait();
  });
  return trigger;
}

// 静止指针被重定位补发 mouseleave 的模拟：React 的 onMouseLeave 由原生
// mouseout（冒泡）推出（tl1168 套件同款 hover 模式：mouseout 不带
// relatedTarget 即视为离开整条链）。
async function leavePopoverInner() {
  const inner = document.querySelector(".context-ring-popover__inner");
  if (!inner) throw new Error("missing popover inner");
  await act(async () => {
    inner.dispatchEvent(new window.MouseEvent("mouseout", { bubbles: true, cancelable: true }));
    await wait();
  });
}

function popoverOpen(): boolean {
  const el = document.querySelector(".anchored-popover") as HTMLElement | null;
  return el != null && el.getAttribute("data-state") === "open";
}

function toggleInput(): HTMLInputElement {
  const toggle = document.querySelector('[data-testid="composition-detail-toggle"]') as HTMLInputElement | null;
  if (!toggle) throw new Error("missing detail toggle");
  return toggle;
}

// 勾选交互沿真实事件序：mousedown（武装 tl1239③ 护栏）→ click/change。
async function clickToggle() {
  const label = document.querySelector(".context-composition__detail-toggle") as HTMLLabelElement;
  if (!label) throw new Error("missing detail toggle label");
  await act(async () => {
    label.dispatchEvent(new MouseEvent("mousedown", { bubbles: true }));
    await wait();
    toggleInput().click();
    await wait();
  });
}

const here = dirname(fileURLToPath(import.meta.url));
const cssText = readFileSync(join(here, "..", "styles.css"), "utf8");

/** 提取一条 CSS 规则体（selector 首现到下一个 `}`）。 */
function cssRule(selector: string): string {
  const start = cssText.indexOf(selector);
  if (start < 0) return "";
  const end = cssText.indexOf("}", start);
  return cssText.slice(start, end < 0 ? cssText.length : end);
}

console.log("\ntl1239 — 1224b detail rework (four acceptance points)");

// ── ②: 分段色条彩色（静态 CSS 断言——颜色不上屏，文本级钉死回归） ──────────
{
  ok(
    cssRule(".context-composition__seg--images").includes("var(--info)"),
    "② images 段配色规则存在且为彩色（此前整段透明：任务1040 起无规则）",
  );
  ok(
    cssRule(".context-composition__dot--images").includes("var(--info)"),
    "② images legend 点与段条同值",
  );
  ok(
    cssRule(".context-composition__seg--systemPrompt").includes("var(--info)") &&
      !cssRule(".context-composition__seg--systemPrompt").includes("var(--fg-faint) 75%"),
    "② systemPrompt 段去纯灰（原 fg-faint 75%）改弱蓝",
  );
  ok(
    cssRule(".context-composition__seg--builtinTools").includes("var(--ok)") &&
      !cssRule(".context-composition__seg--builtinTools").includes("var(--accent) 55%"),
    "② builtinTools 段去主题色混灰改绿",
  );
  ok(
    cssRule(".context-composition__seg--mcpTools").includes("var(--warn) 55%") &&
      !cssRule(".context-composition__seg--mcpTools").includes("var(--accent) 40%"),
    "② mcpTools 段改弱琥珀（工具族与 builtinTools 明暗成对）",
  );
  ok(
    cssRule(".context-composition__seg--message").includes("var(--accent)") &&
      cssRule(".context-composition__seg--skills").includes("var(--warn)"),
    "② 既有锚点不动：message 仍主题色、skills 仍琥珀",
  );
  ok(
    cssRule(".context-composition__seg--section-userMessages").includes("background: var(--ok);") &&
      !cssRule(".context-composition__seg--section-userMessages").includes("65%"),
    "② 明细三段条 userMessages 段改全饱和绿（原 65% 混灰）",
  );
  ok(
    cssRule(".context-composition__dot--item-runtime").includes("var(--info)") &&
      !cssRule(".context-composition__dot--item-runtime").includes("var(--fg-faint) 75%"),
    "② runtime 子项点去纯灰改弱蓝（彩色子项口径）",
  );
}

// ── ④: 三语 tooltip key 齐备（静态文本断言，防死键/漏译） ────────────────────
{
  const read = (name: string) => readFileSync(join(here, "..", "locales", name), "utf8");
  ok(read("en.ts").includes('"context.subitem.standingMemoryTitle"'), "④ en 含 standingMemoryTitle key");
  ok(read("zh.ts").includes('"context.subitem.standingMemoryTitle"'), "④ zh 含 standingMemoryTitle key");
  ok(read("zh-TW.ts").includes('"context.subitem.standingMemoryTitle"'), "④ zh-TW 含 standingMemoryTitle key");
  ok(
    read("zh.ts").includes("记忆索引而非历史消息记录"),
    "④ zh 文案点明「记忆索引而非消息记录」（用户质疑回应）",
  );
  ok(
    read("zh-TW.ts").includes('"context.subitem.standingMemory": "跨會話記憶"'),
    "④ zh-TW standingMemory 标签为繁体（tl1224b 简体漏网纠正，335:1 惯例）",
  );
}

// ── ①+③+④: 组件行为 ─────────────────────────────────────────────────────────
{
  installDom();
  installContextPanelMock(async (tabId: string) => ({
    ...baseInfo(),
    usedTokens: 711200,
    windowTokens: 1_000_000,
    composition: tabId === "tab-tl1239" ? tl1224Composition() : undefined,
  }));

  // 无构成数据：头部行不出现明细开关（与明细视图同进退）。
  await renderRing({ tabId: "tab-tl1239-empty" });
  installContextPanelMock(async () => ({ ...baseInfo(), usedTokens: 10, windowTokens: 1_000_000 }));
  await act(async () => {
    (document.querySelector(".context-ring") as HTMLButtonElement).click();
    await wait();
    await wait();
    await wait();
  });
  ok(
    document.querySelector(".context-composition__detail-toggle") == null,
    "① 无构成数据时头部行无明细开关（整块同进退）",
  );

  // 有构成数据：开关在头部行内、右对齐 modifier、不在 composition 块内。
  installDom();
  installContextPanelMock(async () => ({
    ...baseInfo(),
    usedTokens: 711200,
    windowTokens: 1_000_000,
    composition: tl1224Composition(),
  }));
  await renderRing();
  await openPopover();
  const toggle = toggleInput();
  const toggleLabel = toggle.closest("label") as HTMLLabelElement;
  ok(
    toggleLabel.querySelector(".context-ring-popover__header") == null &&
      toggleLabel.closest(".context-ring-popover__header") != null,
    "① 明细开关位于面板头部行内（348.6k/1M 估算 35% 那一行）",
  );
  ok(
    toggle.closest(".context-composition") == null,
    "① 明细开关不再位于色条所在 composition 块内（独立行移除）",
  );
  ok(
    toggleLabel.className.includes("context-composition__detail-toggle--header"),
    "① 开关挂头部行右对齐 modifier",
  );
  ok(
    cssRule(".context-composition__detail-toggle--header").includes("margin: 0 0 0 auto"),
    "① 右对齐实现为 margin-left:auto（头部行右端空白处）",
  );
  eq(toggle.checked, false, "① 默认未勾选（US-A 平铺视图照常）");
  ok(document.querySelector(".context-composition__legend") != null, "①② US-A 默认平铺视图结构不变");

  // ③: 勾选（先 mousedown 武装护栏）后，静止指针被补发的 mouseleave 不关面板。
  // 武装与补发在同一 act 内完成——真实浏览器里重定位补发紧跟点击发生，同一
  // 帧内；测试环境 act 开销可能让 400ms 护栏「合法过期」，故段内冻结
  // Date.now 屏蔽 harness 耗时（护栏判定只与时刻差有关）。
  const toggleLabelEl = document.querySelector(".context-composition__detail-toggle") as HTMLLabelElement;
  const realNow = Date.now;
  let frozenAt = 0;
  await act(async () => {
    frozenAt = realNow();
    (Date as unknown as { now: () => number }).now = () => frozenAt;
    try {
      // 同一同步块内完成整个手势（真实浏览器同一帧）：mousedown 武装 → 勾选
      // → 重定位补发 mouseover/mouseout。
      toggleLabelEl.dispatchEvent(new MouseEvent("mousedown", { bubbles: true }));
      toggleInput().click();
      const innerEl = document.querySelector(".context-ring-popover__inner");
      innerEl!.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, cancelable: true }));
      innerEl!.dispatchEvent(new MouseEvent("mouseout", { bubbles: true, cancelable: true }));
      await wait();
    } finally {
      (Date as unknown as { now: () => number }).now = realNow;
    }
  });
  ok(document.querySelector('[data-testid="composition-sections"]') != null, "③ 勾选后切三段式明细视图");
  ok(popoverOpen(), "③ 勾选后被补发的 mouseleave 不关闭面板（护栏生效）");
  ok(
    (toggleInput() as HTMLInputElement).checked === true,
    "③ 护栏生效期间勾选状态保持",
  );
  // 护栏窗口（400ms）过后真移出 → 照常关闭。
  await act(async () => {
    await wait(450);
  });
  await leavePopoverInner();
  await act(async () => {
    await wait(250); // AnchoredPopover 关闭动画 140ms 后卸载
  });
  ok(!popoverOpen(), "③ 护栏窗外真移出触发关闭");
  ok(
    document.querySelector(".context-ring-popover__inner") == null,
    "③ 关闭动画后面板卸载",
  );

  // ④: 跨会话记忆行挂解释 tooltip，其余子项行不挂（换新 tabId：detailView
  // 按 tabId 共享，同 tabId 会带着上一块的勾选状态进场）。
  installDom();
  installContextPanelMock(async () => ({
    ...baseInfo(),
    usedTokens: 711200,
    windowTokens: 1_000_000,
    composition: tl1224Composition(),
  }));
  await renderRing({ tabId: "tab-tl1239-tip" });
  await openPopover();
  await clickToggle();
  const itemRows = document.querySelectorAll(".context-composition__section-item");
  ok(itemRows.length === 9, "④ 明细九子项行渲染（tooltip 断言的载体）");
  let memoryTitle = "";
  let historyTitle: string | null = "";
  let titledCount = 0;
  itemRows.forEach((row) => {
    const label = row.querySelector(".context-composition__label") as HTMLElement;
    const dot = row.querySelector("[class*='context-composition__dot--item-']") as HTMLElement;
    if (dot.className.includes("dot--item-standingMemory")) memoryTitle = label.getAttribute("title") ?? "";
    if (dot.className.includes("dot--item-history")) historyTitle = label.getAttribute("title");
    if (label.getAttribute("title")) titledCount += 1;
  });
  ok(memoryTitle.length > 0, "④ 跨会话记忆行挂解释 tooltip");
  ok(
    memoryTitle.includes("记忆索引") || memoryTitle.includes("memory index"),
    "④ tooltip 点明内容源＝记忆索引（非消息记录）",
  );
  eq(titledCount, 1, "④ 仅跨会话记忆一行挂 tooltip（最小改动）");
  eq(historyTitle, null, "④ 历史消息行无 tooltip");
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
