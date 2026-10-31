// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1224-context-three-section.test.tsx
//
// tl1224 阶段二 — the composer gauge popup's protocol three-section detail
// view. Acceptance (tasklist 1224 阶段二 US-A/US-B):
//   US-A 默认视图零改动: unchecked checkbox renders the EXISTING flat legend
//        (message/images/builtin tools/skills/system prompt/other/MCP rows)
//        and no section structure;
//   US-B 勾选「明细」切三段式: System prompt / Tool list / User messages,
//        sections divided by rules, colored bar, per-sub-item values;
//   checkbox is small, sits above the bar, and its state survives popup
//        close/reopen within the session (per-tab);
//   section math: Σ子项 = 段值 and Σ三段份额 = 100.0;
//   64K warning line: the inject tick renders once the host reports a
//        directory size, and the ring tints past 64KB.

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { ContextWindowRing } from "../components/ContextWindowRing";
import { LocaleProvider } from "../lib/i18n";
import { resetQueryCoalescing } from "../lib/queryCoalesce";
import type { ContextPanelInfo } from "../lib/types";
import { compositionThreeSections } from "../lib/contextGaugePopup";

let passed = 0;
let failed = 0;

// 上一块的 React root：installDom 时先卸载（tl1239 同款隔离）——旧 root 的
// portal/监听器若跨 installDom 存活，pending 渲染会被 React 全局调度器在新
// document 上重新落位（portal 重挂新 body、outside-click 监听重挂新
// document），旧浮层闯入新块的 querySelector 视野并被误判关闭。
let activeRoot: ReturnType<typeof createRoot> | null = null;

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

// tl1239 探针：US-B 块 mock 的调用计数（over 块若再调用它即数据串窗证据）。
let usBMockCalls = 0;

class TestResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

function installDom() {
  try {
    activeRoot?.unmount();
  } catch {
    // root 已随旧 DOM 失效——忽略，继续重建环境。
  }
  activeRoot = null;
  // tl1239: ContextPanel 在 queryCoalesce 白名单里，模块级 inflight 跨块存活
  // ——前一块的 200ms 共享答案会漏进下一块（load 相关抖动的根因：主线程被
  // 渲染阻塞时 forget 定时器迟到，下一块的点击命中上一块的缓存答案，新 mock
  // 根本不被调用）。每块重装 DOM 前先清共享缓存。
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

// The composition shape from the tl1224 field observation: a 261K-class
// standing memory index riding the session-context envelope, a real catalog,
// and conversation history — the blind spot the flat view could not show.
function tl1224Composition() {
  return {
    systemPromptTokens: 10700,
    builtinToolTokens: 11600,
    skillTokens: 0,
    mcpToolTokens: 2600,
    messageTokens: 337700, // 历史消息（信封与召回已拆出）
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
    tabId: "tab-tl1224",
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

// tl1239: the two 64K assertions read ring/tick state that lands only after
// the ContextPanel mock resolves; the fixed two-tick wait raced under machine
// load (baseline f55602dd8: 4/6 runs failed the same two assertions). Poll
// with a bounded budget instead of fixed ticks — same assertions, stable wait.
async function waitForCondition(check: () => boolean, label: string): Promise<void> {
  await act(async () => {
    for (let i = 0; i < 250 && !check(); i += 1) await wait(4);
  });
  if (!check()) {
    const ring = document.querySelector(".context-ring");
    const tick = document.querySelector(".context-ring-popover__mark--inject");
    throw new Error(
      `timeout waiting for: ${label}; ring="${(ring as HTMLElement | null)?.className ?? "none"}" ` +
        `composition=${document.querySelector(".context-composition") != null} ` +
        `sections=${document.querySelector('[data-testid="composition-sections"]') != null} ` +
        `tickTitle="${tick?.getAttribute("title") ?? "none"}" usBMockCalls=${usBMockCalls}`,
    );
  }
}

console.log("\ntl1224 phase 2 — protocol three-section detail view");

// ── pure model: section math ───────────────────────────────────────────────
{
  eq(compositionThreeSections(null).length, 0, "no composition means no sections");
  const sections = compositionThreeSections(tl1224Composition());
  eq(sections.length, 3, "the full composition renders exactly three protocol sections");
  eq(sections[0].key, "systemPrompt", "section 1 is System prompt");
  eq(sections[1].key, "tools", "section 2 is Tool list");
  eq(sections[2].key, "userMessages", "section 3 is User messages");

  const sys = sections[0];
  eq(sys.items.length, 4, "System prompt carries 基础/跨会话记忆/能力目录/运行时快照 four sub-items");
  eq(
    sys.tokens,
    sys.items.reduce((sum, item) => sum + item.tokens, 0),
    "System prompt section total = Σ子项",
  );
  eq(sys.tokens, 10700 + 261000 + 61000 + 12000, "System prompt section carries the standing-prefix sum (incl. the 261K memory index)");

  const tools = sections[1];
  eq(tools.items.map((item) => item.key).join(","), "builtinTools,mcpTools", "Tool list sub-items = 内置工具 + MCP 服务器");
  eq(tools.tokens, 11600 + 2600, "Tool list total = builtin + MCP");

  const msgs = sections[2];
  eq(
    msgs.items.map((item) => item.key).join(","),
    "history,images,recall",
    "User messages sub-items = 历史 + 图片 + 每轮召回（零技能正文行隐藏）",
  );
  eq(msgs.tokens, 337700 + 11000 + 3600, "User messages total = 历史 + 图片 + 召回");

  const sectionSum = sections.reduce((sum, section) => sum + section.tokens, 0);
  eq(sectionSum, tl1224Composition().totalTokens, "Σ三段 = totalTokens（协议口径不变量）");
  const shareSum = Math.round(sections.reduce((sum, section) => sum + section.share, 0) * 10) / 10;
  eq(shareSum, 100, "三段份额合计 100.0");

  // 旧宿主（无 tl1224 细分字段）不产生幽灵段。
  const legacy = compositionThreeSections({
    systemPromptTokens: 3000, builtinToolTokens: 1500, skillTokens: 0,
    mcpToolTokens: 700, messageTokens: 4300, totalTokens: 9500,
  });
  eq(legacy.length, 3, "a pre-1224 host still renders three sections (history absorbs everything)");
  const legacySys = legacy.find((section) => section.key === "systemPrompt")!;
  eq(legacySys.items.map((item) => item.key).join(","), "systemBase", "a pre-1224 host shows only the base-prompt sub-item under System prompt");
}

// ── component: US-A 默认视图零改动 + US-B 切换 + 会话内保持 ─────────────────
{
  installDom();
  const composition = tl1224Composition();
  installContextPanelMock(async (tabId: string) => {
    usBMockCalls += 1;
    return {
      ...baseInfo(),
      usedTokens: 711200,
      windowTokens: 1_000_000,
      composition: tabId === "tab-tl1224" ? composition : undefined,
    };
  });

  await renderRing();
  await openPopover();

  // US-A: 默认（未勾选）= 现状平铺视图。
  const toggle = document.querySelector('[data-testid="composition-detail-toggle"]') as HTMLInputElement;
  ok(toggle != null, "the small detail checkbox renders above the composition bar");
  eq(toggle.checked, false, "default state is unchecked");
  ok(document.querySelector(".context-composition__legend") != null, "US-A 默认视图：平铺 legend 照常渲染");
  ok(document.querySelector('[data-testid="composition-sections"]') == null, "US-A 默认视图：无三段结构（零改动）");
  const flatRows = document.querySelectorAll(".context-composition__legend-item").length;
  eq(flatRows, 6, "US-A 默认视图：平铺行数与既有口径一致（含图片/MCP 行）");

  // US-B: 勾选后切三段式。
  await act(async () => {
    toggle.click();
    await wait();
  });
  ok(document.querySelector(".context-composition__legend") == null, "US-B：平铺 legend 退场");
  const sectionsEl = document.querySelector('[data-testid="composition-sections"]');
  ok(sectionsEl != null, "US-B：三段结构上屏");
  const sectionHeads = document.querySelectorAll(".context-composition__section-head").length;
  eq(sectionHeads, 3, "US-B：三段头部（横线分割的分组）");
  const itemRows = document.querySelectorAll(".context-composition__section-item").length;
  eq(itemRows, 9, "US-B：九个子项行（4+2+3，各自数值）");
  const sectionBars = document.querySelectorAll(".context-composition__seg--section-systemPrompt").length;
  eq(sectionBars, 1, "US-B：彩色三段条含 System prompt 段");

  // 会话内保持：关掉浮层再打开，勾选状态不丢。
  await act(async () => {
    document.body.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await wait();
  });
  await openPopover();
  const toggleReopened = document.querySelector('[data-testid="composition-detail-toggle"]') as HTMLInputElement;
  eq(toggleReopened.checked, true, "勾选状态浮层重开后保持（会话内）");
  ok(document.querySelector('[data-testid="composition-sections"]') != null, "重开后仍是三段视图");

  // 切回默认：再点一次 checkbox。
  await act(async () => {
    toggleReopened.click();
    await wait();
  });
  ok(document.querySelector(".context-composition__legend") != null, "再次点击切回平铺默认视图");
}

// ── 64K 警告线视觉态 ──────────────────────────────────────────────────────
{
  installDom();
  const over = tl1224Composition();
  over.directoryBytes = 70_000; // > 64KB warn line
  let servedOverCalls = 0;
  installContextPanelMock(async () => {
    servedOverCalls += 1;
    return {
      ...baseInfo(),
      usedTokens: 711200,
      windowTokens: 1_000_000,
      composition: over,
    };
  });

  await renderRing();
  await openPopover(); // ContextPanel 异步读数先落，再断言圆环视觉态
  await waitForCondition(
    () => (document.querySelector(".context-ring") as HTMLElement | null)?.className.includes("context-ring--overinject") === true,
    `ring overinject tint (overMockCalls=${servedOverCalls})`,
  );
  const ring = document.querySelector(".context-ring") as HTMLElement;
  ok(ring.className.includes("context-ring--overinject"), "超 64KB 警告线：圆环着警告色（超线视觉态）");
  const injectTick = document.querySelector(".context-ring-popover__mark--inject");
  ok(injectTick != null, "64K 刻度线上屏（目录已渲染即显示）");
  ok(
    (injectTick as HTMLElement).className.includes("context-ring-popover__mark--inject-over"),
    "超线时刻度转警告色",
  );

  // 未超线：圆环不着警告色，刻度保留。
  installDom();
  const under = tl1224Composition(); // 50KB < 64KB
  installContextPanelMock(async () => ({
    ...baseInfo(),
    usedTokens: 711200,
    windowTokens: 1_000_000,
    composition: under,
  }));
  await renderRing();
  const ringUnder = document.querySelector(".context-ring") as HTMLElement;
  ok(!ringUnder.className.includes("context-ring--overinject"), "线内目录块不触发超线视觉态");
  await openPopover();
  await waitForCondition(
    () => document.querySelector(".context-ring-popover__mark--inject") != null,
    "inject tick",
  );
  ok(document.querySelector(".context-ring-popover__mark--inject") != null, "线内时刻度仍显示（被动可见）");
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
