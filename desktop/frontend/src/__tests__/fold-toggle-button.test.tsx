// Run: tsx src/__tests__/fold-toggle-button.test.tsx
//
// 任务 463 — 「收起全部工作过程」按钮改为折叠/展开双向开关。三段断言：
//   A  纯函数 allWorkProcessesCollapsed：全部关闭才算全折叠；手动混合态
//      （部分展开）必须返回 false——验收⑤「不错位」的推导核心；
//   B  transcript 接线：真实 fold 状态经 store 上报后聚合值翻转
//      （collapse-all → true，expand-all → false，表面注销清理，手动种子态诚实）；
//   C  composer 渲染：折叠态渲染「展开」文案 + 向上图标 + 点击派发
//      reasonix:expand-all-folds；展开态保持原「收起」+ 向下图标；
//      三语键（zh / zh-TW / en）齐备。

import { JSDOM } from "jsdom";
import React from "react";
import { createRoot, type Root } from "react-dom/client";
import type { Item } from "../lib/useController";
import type { ToolApprovalMode } from "../lib/types";
import { createTranscriptHarness } from "./transcript-dom-harness";

let passed = 0;
let failed = 0;

function ok(value: unknown, label: string) {
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

function flushTimers(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

// ─────────────────────────────────────────────────────────────────────────────
// A. 纯函数：手动混合态不算全折叠
// ─────────────────────────────────────────────────────────────────────────────
console.log("\ntask 463 A: allWorkProcessesCollapsed（纯函数）");

{
  const rows = await import("../lib/transcriptRows");
  const experience = "standard" as const;
  // standard 下默认展开的段（运行中）；completed 段默认收起（完成即自动折叠）。
  const openByDefault = { key: "s1", hasOutsideContent: true, hasRunningWork: true, keepReasoningExpanded: false };
  const closedByDefault = { key: "s2", hasOutsideContent: true, hasRunningWork: false, keepReasoningExpanded: false };
  const closed = { open: false, userOverridden: true, running: false };
  const open = { open: true, userOverridden: true, running: false };

  eq(rows.allWorkProcessesCollapsed(new Map(), [openByDefault], experience), false,
    "无折叠条目 + 段默认展开（运行中）→ false");
  eq(rows.allWorkProcessesCollapsed(new Map(), [closedByDefault], experience), true,
    "无折叠条目 + 段默认收起（已完成）→ true（真实全折叠，按钮应显示「展开」）");
  eq(rows.allWorkProcessesCollapsed(new Map([["s1", closed], ["s2", closed]]), [openByDefault, closedByDefault], experience), true,
    "全部条目 open=false → true");
  eq(rows.allWorkProcessesCollapsed(new Map([["s1", closed], ["s2", open]]), [openByDefault, closedByDefault], experience), false,
    "手动混合态（一关一开）→ false（验收⑤不错位的核心推导）");
  eq(rows.allWorkProcessesCollapsed(new Map([["s1", closed]]), [openByDefault], experience), true,
    "唯一段显式关闭（即便其默认展开）→ true（显式条目优先于默认规则）");
  eq(rows.allWorkProcessesCollapsed(new Map([["s2", closed]]), [openByDefault, closedByDefault], experience), false,
    "缺失条目按默认展开规则计入（s1 默认开）→ 不误报全折叠 → false");
  eq(rows.allWorkProcessesCollapsed(new Map([["s1", closed], ["s2", closed]]), [], experience), false,
    "没有可折叠块 → false（无可展开之物，按钮保持收起外观）");
  eq(rows.allWorkProcessesCollapsed(new Map([["s1", closed], ["s2", closed]]), [openByDefault, closedByDefault], "deep" as const), true,
    "deep 层级下条目显式关闭仍算全折叠（deep 不渲染按钮，方向仅存档）");
}

const { act } = await import("react");

// ─────────────────────────────────────────────────────────────────────────────
// B. transcript 接线：真实折叠状态 → store 聚合值
// ─────────────────────────────────────────────────────────────────────────────
console.log("\ntask 463 B: transcript 状态上报接线");

const harness = await createTranscriptHarness({
  storage: { "reasonix-session-experience": "standard" },
});
const foldState = await harness.loadModule<{
  getWorkProcessFoldAggregate: () => { allCollapsed: boolean };
}>("/src/lib/workProcessFoldState.ts");
const foldOverrides = await harness.loadModule<{
  writeTranscriptFoldOverride: (sessionKey: string, segmentKey: string, entry: { open: boolean; userOverridden: boolean; running: boolean }) => void;
  clearTranscriptFoldOverrideSessionForTest: (sessionKey: string) => void;
}>("/src/lib/transcriptFoldOverrides.ts");
const transcriptRows = await harness.loadModule<{
  buildTurnModels: (items: Item[]) => unknown[];
  foldSegmentStates: (models: unknown[], keepReasoningExpanded?: boolean) => Array<{ key: string }>;
}>("/src/lib/transcriptRows.ts");

// 两段工作过程：第一轮已完成（standard 默认收起）、第二轮运行中（默认展开）。
const twoTurnItems: Item[] = [
  { kind: "user", id: "u1", text: "first ask" },
  { kind: "assistant", id: "a1", text: "first answer", reasoning: "first process", workDurationMs: 1_000 },
  { kind: "user", id: "u2", text: "second ask" },
  { kind: "assistant", id: "a2", text: "", reasoning: "second process", streaming: true, workDurationMs: 100 },
];

function segmentKeys(): string[] {
  const models = transcriptRows.buildTurnModels(twoTurnItems);
  return transcriptRows.foldSegmentStates(models, false).map((s) => s.key);
}

function seed(sessionKey: string, key: string, entry: { open: boolean; userOverridden: boolean; running: boolean }) {
  foldOverrides.writeTranscriptFoldOverride(sessionKey, key, entry);
}

try {
  const sessionExperience = await harness.loadModule<{
    hydrateSessionExperience: (v: unknown) => void;
  }>("/src/lib/sessionExperience.ts");
  sessionExperience.hydrateSessionExperience("standard");
  eq(segmentKeys().length, 2, "B 前置: 场景含两个可折叠段");

  // B1: 默认态（完成=关、运行=开）→ 非全折叠 → 按钮应保持「收起」。
  await harness.render(twoTurnItems, { tabId: "fold-b1", running: true });
  eq(foldState.getWorkProcessFoldAggregate().allCollapsed, false,
    "B1: 默认态（混合）→ 聚合 false（按钮显示「收起」）");

  // B2: collapse-all 事件 → 真实全折叠 → 聚合 true（按钮应翻转成「展开」）。
  await act(async () => {
    window.dispatchEvent(new CustomEvent("reasonix:collapse-all-folds"));
  });
  await harness.flush();
  eq(foldState.getWorkProcessFoldAggregate().allCollapsed, true,
    "B2: collapse-all 后 → 聚合 true（按钮显示「展开」）");

  // B2b: 换绑 tabId（等同旧表面卸载）→ 旧上报注销，不得残留「展开」。
  await harness.render(twoTurnItems, { tabId: "fold-b2b", running: true });
  eq(foldState.getWorkProcessFoldAggregate().allCollapsed, false,
    "B2b: 旧表面注销后 → 聚合复位 false");

  // B3: 手动混合态（用户手动展开已完成段 + 手动收起运行段）→ 必须 false。
  // 种子走 transcriptFoldOverrides 的真实存储路径（sessionKey = tab:<tabId>）。
  const keys3 = segmentKeys();
  eq(keys3.length, 2, "B3 前置: 两个可折叠段");
  const sessionKey3 = "tab:fold-b3";
  seed(sessionKey3, keys3[0], { open: true, userOverridden: true, running: false });
  seed(sessionKey3, keys3[1], { open: false, userOverridden: true, running: true });
  await harness.render(twoTurnItems, { tabId: "fold-b3", running: true });
  eq(foldState.getWorkProcessFoldAggregate().allCollapsed, false,
    "B3: 手动部分展开的混合态 → 聚合 false（不错位）");

  // B4: 手动把两段都收起 → 聚合 true（按钮显示「展开」，与真实状态一致）。
  const keys4 = segmentKeys();
  const sessionKey4 = "tab:fold-b4";
  seed(sessionKey4, keys4[0], { open: false, userOverridden: true, running: false });
  seed(sessionKey4, keys4[1], { open: false, userOverridden: true, running: true });
  await harness.render(twoTurnItems, { tabId: "fold-b4", running: true });
  eq(foldState.getWorkProcessFoldAggregate().allCollapsed, true,
    "B4: 手动全部收起 → 聚合 true");

  // B5: expand-all 事件从「全折叠」回到「全展开」→ 聚合 false。
  await act(async () => {
    window.dispatchEvent(new CustomEvent("reasonix:expand-all-folds"));
  });
  await harness.flush();
  eq(foldState.getWorkProcessFoldAggregate().allCollapsed, false,
    "B5: expand-all 后 → 聚合 false（按钮回到「收起」）");
} finally {
  await harness.unmount();
  await harness.close();
}

// ─────────────────────────────────────────────────────────────────────────────
// C. composer 渲染：双向开关的外观与事件
// ─────────────────────────────────────────────────────────────────────────────
console.log("\ntask 463 C: composer 按钮双向渲染");

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
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.Node = dom.window.Node;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.HTMLTextAreaElement = dom.window.HTMLTextAreaElement;
  globalThis.Event = dom.window.Event;
  globalThis.CustomEvent = dom.window.CustomEvent;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.InputEvent = dom.window.InputEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.File = dom.window.File;
  globalThis.FileReader = dom.window.FileReader;
  globalThis.PointerEvent = dom.window.MouseEvent as unknown as typeof PointerEvent;
  globalThis.MutationObserver = dom.window.MutationObserver;
  Object.defineProperty(globalThis, "localStorage", { configurable: true, value: dom.window.localStorage });
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  globalThis.ResizeObserver = TestResizeObserver;
  Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
  Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });
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

function installBridgeApp(methods: Record<string, unknown>) {
  (window as unknown as { go: { main: { App: Record<string, unknown> } } }).go = {
    main: {
      App: {
        Commands: async () => [],
        Models: async () => [],
        ModelsForTab: async () => [],
        ...methods,
      },
    },
  };
}

installDom();
installBridgeApp({});

const { Composer } = await import("../components/Composer");
const { LocaleProvider } = await import("../lib/i18n");
const { ToastProvider } = await import("../lib/toast");
const { publishWorkProcessFoldState, clearWorkProcessFoldState, resetWorkProcessFoldStateForTest } = await import("../lib/workProcessFoldState");
const zh = (await import("../locales/zh")).zh;
const zhTw = (await import("../locales/zh-TW")).zhTW;
const en = (await import("../locales/en")).en;

try {
  // 三语键齐备（验收④）。
  eq(zh["composer.expandAll"], "展开全部工作过程", "zh: composer.expandAll = 展开全部工作过程");
  eq(zhTw["composer.expandAll"], "全部展開工作過程", "zh-TW: composer.expandAll = 全部展開工作過程");
  eq(en["composer.expandAll"], "Expand all work processes", "en: composer.expandAll = Expand all work processes");

  const rootEl = document.getElementById("root")!;
  let root: Root | null = createRoot(rootEl);
  const baseProps = {
    running: false,
    collaborationMode: "normal",
    toolApprovalMode: "ask" as ToolApprovalMode,
    goal: "",
    cwd: "/repo",
    modelLabel: "DeepSeek-R1",
    imageInputEnabled: true,
    tabId: "single-surface-tab",
    sessionKey: "session:project:/repo:topic-a:session-a",
    onSend: () => {},
    onCancel: async () => ({ discardedItemIds: [] }),
    onCycleMode: () => {},
    onSetMode: () => {},
    onSetCollaborationMode: () => {},
    onSetToolApprovalMode: () => {},
    onToggleYoloApprovalMode: () => {},
    onClearGoal: () => {},
    onSwitchModel: () => {},
    onSetEffort: () => {},
    ready: true,
  };
  const paint = async () => {
    await act(async () => {
      root!.render(
        React.createElement(
          LocaleProvider,
          null,
          React.createElement(
            ToastProvider,
            null,
            React.createElement("div", { className: "chat-pane" },
              React.createElement(Composer, baseProps)),
          ),
        ),
      );
      await flushTimers();
    });
  };
  // 重挂载：C1 的点击会触发 1.6s 防抖 gray-out（组件态），重挂载是绕开它的
  // 干净路径，同时覆盖「composer 后于上报挂载也能读到正确方向」的时序。
  const remount = async () => {
    await act(async () => { root?.unmount(); });
    root = createRoot(rootEl);
    await paint();
  };

  const button = (): HTMLButtonElement => {
    const node = document.querySelector<HTMLButtonElement>(".composer-meta__collapse-all");
    if (!node) throw new Error("collapse-all button did not render");
    return node;
  };

  resetWorkProcessFoldStateForTest();
  await paint();

  // C1: 展开态（默认）——保持原「收起」外观与事件（验收③）。
  publishWorkProcessFoldState("single-surface-tab", { hasFoldables: true, allCollapsed: false });
  await act(async () => { await flushTimers(); });
  eq(button().dataset.foldState, "expanded", "C1: data-fold-state=expanded");
  ok(button().querySelector("svg[class*=\"chevrons-down\"]"), "C1: 向下图标（chevrons-down）");
  ok(!button().querySelector("svg[class*=\"chevrons-up\"]"), "C1: 无向上图标");
  eq(button().getAttribute("aria-label"), en["composer.collapseAll"], "C1: aria-label=收起文案（en）");

  const dispatched: string[] = [];
  const spy = (event: Event) => dispatched.push(event.type);
  window.addEventListener("reasonix:collapse-all-folds", spy);
  window.addEventListener("reasonix:expand-all-folds", spy);
  await act(async () => { button().click(); await flushTimers(); });
  ok(dispatched.includes("reasonix:collapse-all-folds"), "C1: 点击派发 collapse-all 事件");
  ok(!dispatched.includes("reasonix:expand-all-folds"), "C1: 展开态不派发 expand 事件");

  // C2: 折叠态（transcript 上报全折叠）——「展开」文案 + 向上图标（验收①②）。
  await remount();
  publishWorkProcessFoldState("single-surface-tab", { hasFoldables: true, allCollapsed: true });
  await act(async () => { await flushTimers(); });
  eq(button().dataset.foldState, "collapsed", "C2: data-fold-state=collapsed");
  ok(button().querySelector("svg[class*=\"chevrons-up\"]"), "C2: 图标反转为向上（chevrons-up）");
  ok(!button().querySelector("svg[class*=\"chevrons-down\"]"), "C2: 无向下图标");
  eq(button().getAttribute("aria-label"), en["composer.expandAll"], "C2: aria-label=展开文案（en）");
  eq(button().getAttribute("title"), en["composer.expandAll"], "C2: title=展开文案");

  const dispatched2: string[] = [];
  const spy2 = (event: Event) => dispatched2.push(event.type);
  window.addEventListener("reasonix:collapse-all-folds", spy2);
  window.addEventListener("reasonix:expand-all-folds", spy2);
  await act(async () => { button().click(); await flushTimers(); });
  ok(dispatched2.includes("reasonix:expand-all-folds"), "C2: 折叠态点击派发 expand-all 事件");
  ok(!dispatched2.includes("reasonix:collapse-all-folds"), "C2: 折叠态不派发 collapse 事件");

  // C3: 上报注销（transcript 卸载）→ 回到「收起」外观，不残留「展开」。
  clearWorkProcessFoldState("single-surface-tab");
  await act(async () => { await flushTimers(); });
  eq(button().dataset.foldState, "expanded", "C3: 上报注销后回到 expanded（不残留折叠态）");

  await act(async () => { root?.unmount(); });
  root = null;
} finally {
  resetWorkProcessFoldStateForTest();
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
process.exit(0);
