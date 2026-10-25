// Run: npx tsx src/__tests__/composer-draft-rewrite.test.tsx
//
// 任务 1024 acceptance harness (composer 草稿优化):
//  ① 开关关（默认）：发送按钮左侧不出现改写按钮，composer 工具栏与引入前
//     逐字节一致（铁律 2）；
//  ② 开关开：改写按钮出现在发送按钮左侧，点击读草稿 + 最近上下文
//     调 RewriteDraftForTab，结果回填输入框且**不自动发送**（onSend 零调用）；
//  ③ 空草稿：按钮 disabled，不发请求；
//  ④ 改写返回空串（后端静默降级形态）：输入框保持原样，不弹错、不打断。
//
// 任务 1052 acceptance harness (档位弹窗):
//  ⑤ 点击改写先弹档位弹窗（2KB/5KB/10KB/20KB 四档 + 取消），弹窗出现时
//     **零请求**（不立即改写）；
//  ⑥ 点某档 → 按档调 RewriteDraftForTab（contextBudgetKB 收到所选档），
//     注入上下文 ≤ 所选档字节数，回填不自动发送；
//  ⑦ 取消按钮/遮罩点击 → 关弹窗、零请求；
//  ⑧ 记住上次选择：选档写入 localStorage，重开弹窗默认高亮上次档；
//  ⑨ 空草稿点改写 → 弹窗不出现（与 1024 空态保护一致）、零请求。

import { JSDOM } from "jsdom";

import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { Composer } from "../components/Composer";
import { LocaleProvider } from "../lib/i18n";
import { ToastProvider } from "../lib/toast";
import { applyLabFlags } from "../lib/labFlags";
import type { CollaborationMode, ToolApprovalMode } from "../lib/types";

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

function flushTimers(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
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
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.Node = dom.window.Node;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.HTMLTextAreaElement = dom.window.HTMLTextAreaElement;
  globalThis.Event = dom.window.Event;
  globalThis.CustomEvent = dom.window.CustomEvent;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.InputEvent = dom.window.InputEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.PointerEvent = dom.window.MouseEvent as unknown as typeof PointerEvent;
  globalThis.MutationObserver = dom.window.MutationObserver;
  globalThis.File = dom.window.File;
  globalThis.FileReader = dom.window.FileReader;
  globalThis.localStorage = dom.window.localStorage;
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

type ComposerProps = Partial<Parameters<typeof Composer>[0]>;

async function renderComposer(props: ComposerProps = {}) {
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  let currentProps: Parameters<typeof Composer>[0] = {
    running: false,
    collaborationMode: "normal" as CollaborationMode,
    toolApprovalMode: "ask" as ToolApprovalMode,
    goal: "",
    cwd: "/repo",
    modelLabel: "mimo-v2.6",
    onSend: () => {},
    onCancel: () => undefined,
    onCycleMode: () => {},
    onSetMode: () => {},
    onSetCollaborationMode: () => {},
    onSetToolApprovalMode: () => {},
    onToggleYoloApprovalMode: () => {},
    onClearGoal: () => {},
    onSwitchModel: () => {},
    onSetEffort: () => {},
    ready: true,
    ...props,
  };
  const paint = async (nextProps: ComposerProps = {}) => {
    currentProps = { ...currentProps, ...nextProps };
    await act(async () => {
      root.render(
        <LocaleProvider>
          <ToastProvider>
            <Composer {...currentProps} />
          </ToastProvider>
        </LocaleProvider>,
      );
      await flushTimers();
    });
  };
  await paint();
  return { root };
}

// 桥接桩：app 是 bridge.ts 的 Proxy——realApp()（window.go?.main?.App）优先，
// 没有才落 makeMockApp()；Proxy 在 target 缺属性时回落 mock，所以把两条
// 目标方法直接挂到 app 上，其余方法照旧走 mock，不用知道 Composer 还读了
// 什么。桩跨用例共享（bridge.ts 的 app 只有一份），每用例重设一遍即可。
const calls: { method: string; args: unknown[] }[] = [];
let rewriteResult = "改写后的结构化草稿";
let historyPage = {
  messages: [
    { role: "user", content: "CI 又红了" },
    { role: "assistant", content: "我看下日志" },
  ],
  startTurn: 0,
  endTurn: 2,
  totalTurns: 2,
  hasOlder: false,
};
function stubBridge() {
  // bridge.ts 的 app Proxy 在 realApp()（window.go?.main?.App）存在时**只**读
  // 那个对象、不再回落 makeMockApp()——所以桩必须挂到 window.go.main.App 上，
  // 并把 Composer 会读的只读能力一并桩上（composer-goal-toggle 同款写法）。
  const w = window as unknown as { go?: { main?: { App?: unknown } } };
  w.go = {
    main: {
      App: {
        Commands: async () => [],
        Models: async () => [],
        ModelsForTab: async () => [],
        SlashArgs: async () => ({ items: [], from: 0 }),
        RewriteDraftForTab: async (...args: unknown[]) => {
          calls.push({ method: "RewriteDraftForTab", args });
          return rewriteResult;
        },
        HistoryPageForTab: async (...args: unknown[]) => {
          calls.push({ method: "HistoryPageForTab", args });
          return historyPage;
        },
      },
    },
  };
}

function composerValue(): string {
  const ta = document.querySelector<HTMLTextAreaElement>("textarea");
  return ta?.value ?? "";
}

// textPasteEvent seeds composer text through its own paste handler — the same
// pattern composer-context-menu-clipboard / composer-session-draft use —
// because the controlled textarea's state is not reachable through synthetic
// input events here.
function textPasteEvent(text: string): Event {
  const event = new window.Event("paste", { bubbles: true, cancelable: true });
  Object.defineProperty(event, "clipboardData", {
    configurable: true,
    value: {
      files: [],
      items: [],
      types: ["text/plain"],
      getData: (kind: string) => (kind === "text" || kind === "text/plain" ? text : ""),
    },
  });
  return event;
}

// Buttons are wrapped in Tooltip (which portals a wrapper element), so query
// the labelled control itself rather than assuming the button is a direct
// toolbar child.
function rewriteButton(): HTMLButtonElement | null {
  return document.querySelector<HTMLButtonElement>("button.composer__btn--rewrite");
}

// 档位弹窗（任务 1052）：portal 到 body 的 dialog；四档按钮带 aria-pressed，
// 取消按钮在 __actions 区。
function budgetDialog(): HTMLElement | null {
  return document.querySelector<HTMLElement>("div.lab-pick-dialog[role=\"dialog\"]");
}

function budgetTierButtons(): HTMLButtonElement[] {
  return [...document.querySelectorAll<HTMLButtonElement>("div.lab-pick-dialog[role=\"dialog\"] button[aria-pressed]")];
}

function budgetCancelButton(): HTMLButtonElement | null {
  return document.querySelector<HTMLButtonElement>("div.lab-pick-dialog[role=\"dialog\"] .lab-bulk-dialog__actions .btn");
}

function sendButton(): HTMLButtonElement | null {
  return document.querySelector<HTMLButtonElement>("button.composer__btn--send");
}

// clickControl dispatches a real user click the way jsdom/react-dom expect it
// (bubbling MouseEvent with the click detail), instead of HTMLElement.click()
// which the toolbar's synthetic-event handling can miss under act.
function clickControl(node: HTMLElement | null) {
  if (!node) return;
  node.dispatchEvent(new window.MouseEvent("click", { bubbles: true, cancelable: true, detail: 1 }));
}

// ── ① 开关关：按钮不出现，工具栏与引入前一致 ──────────────────────────
{
  const dom = installDom();
  applyLabFlags({ draftRewrite: false });
  stubBridge();
  const { root } = await renderComposer();
  ok(rewriteButton() === null, "flag off: the rewrite button does not render");
  ok(sendButton() !== null, "flag off: the send button is still there");
  await act(async () => root.unmount());
  dom.window.close();
}

// ── ②⑤⑥⑦⑧⑨ 开关开：按钮在发送左侧；点击先弹档位弹窗，选档才改写 ────
{
  const dom = installDom();
  applyLabFlags({ draftRewrite: true });
  stubBridge();
  calls.length = 0;
  localStorage.clear();
  let sent = 0;
  const { root } = await renderComposer({ onSend: () => { sent += 1; } });
  const btn = rewriteButton();
  ok(btn !== null, "flag on: the rewrite button renders left of send");
  if (btn) {
    const send = sendButton();
    ok(send !== null, "send button is present in the same run");
    if (send) {
      // Document order is decisive: the rewrite button is rendered before the
      // send button, so it reads as LEFT of send in the toolbar.
      const positions = [...document.querySelectorAll("button.composer__btn--rewrite, button.composer__btn--send")];
      ok(positions.indexOf(btn) < positions.indexOf(send),
        "the rewrite button sits LEFT of the send button");
    }
    // ⑨ 空草稿：点击不弹窗、不发请求（改写入口的空态保护）。
    calls.length = 0;
    await act(async () => { clickControl(rewriteButton()); await flushTimers(); });
    await act(async () => { await flushTimers(); });
    eq(calls.filter((c) => c.method === "RewriteDraftForTab").length, 0,
      "empty draft: clicking the rewrite button sends nothing");
    eq(budgetDialog(), null, "empty draft: no budget dialog opens");

    // 填入草稿（走 composer 自己的 paste 通道——与 context-menu-clipboard /
    // session-draft 测试同一路径；insertRequest 那条路在本 jsdom 布景下只改
    // textarea 显示、不进 React state，点改写按钮读到的仍是空草稿）。
    await act(async () => {
      const node = document.querySelector<HTMLTextAreaElement>("textarea");
      if (!node) throw new Error("composer textarea did not render");
      node.focus();
      node.setSelectionRange(0, node.value.length);
      node.dispatchEvent(textPasteEvent("帮我看看那个 CI 报错"));
      await flushTimers();
    });
    eq(composerValue(), "帮我看看那个 CI 报错", "the draft lands in the composer via its paste handler");

    // ⑤ 点击改写 → 弹窗出现（四档+取消），此刻零请求（不立即改写）。
    await act(async () => { clickControl(rewriteButton()); await flushTimers(); });
    await act(async () => { await flushTimers(); });
    ok(budgetDialog() !== null, "clicking rewrite opens the budget dialog (no rewrite yet)");
    eq(calls.filter((c) => c.method === "RewriteDraftForTab").length, 0,
      "the dialog opening sends nothing");
    const tiers = budgetTierButtons();
    eq(tiers.length, 4, "the dialog offers exactly four budget tiers");
    eq(tiers.map((b) => b.textContent?.trim()).join(","), "2 KB,5 KB,10 KB,20 KB",
      "the four tiers read 2KB/5KB/10KB/20KB");
    ok(budgetCancelButton() !== null, "the dialog has a cancel button");
    eq(tiers.filter((b) => b.getAttribute("aria-pressed") === "true").length, 1,
      "exactly one tier is highlighted by default");
    eq(tiers.find((b) => b.getAttribute("aria-pressed") === "true")?.textContent?.trim(), "10 KB",
      "the default highlighted tier is 10KB (first use, matching the 1024 behavior)");

    // ⑦ 取消 → 关弹窗、零请求。
    clickControl(budgetCancelButton());
    await act(async () => { await flushTimers(); });
    eq(budgetDialog(), null, "cancel closes the budget dialog");
    eq(calls.filter((c) => c.method === "RewriteDraftForTab").length, 0,
      "cancel sends nothing");

    // ⑥ 重新打开，点 2KB 档 → 按档发起改写。
    await act(async () => { clickControl(rewriteButton()); await flushTimers(); });
    await act(async () => { await flushTimers(); });
    ok(budgetDialog() !== null, "the dialog reopens on the next click");
    const twoKB = budgetTierButtons().find((b) => b.textContent?.trim() === "2 KB") ?? null;
    clickControl(twoKB);
    await act(async () => { await flushTimers(); });
    await act(async () => { await flushTimers(); });

    eq(budgetDialog(), null, "choosing a tier closes the dialog");
    const rewriteCalls = calls.filter((c) => c.method === "RewriteDraftForTab");
    eq(rewriteCalls.length, 1, "choosing a tier sends exactly one RewriteDraftForTab request");
    const args = rewriteCalls[0]?.args as unknown[] | undefined;
    ok(Array.isArray(args) && args[1] === "帮我看看那个 CI 报错", "the request carries the draft verbatim");
    ok(Array.isArray(args) && typeof args[2] === "string" && (args[2] as string).includes("CI 又红了"),
      "the request carries the recent conversation context");
    ok(Array.isArray(args) && args[3] === 2,
      "the request carries the chosen tier as contextBudgetKB (2)");
    ok(Array.isArray(args) && typeof args[2] === "string" && (args[2] as string).length <= 2 * 1024,
      "the injected context is trimmed to the chosen 2KB tier");
    eq(composerValue(), rewriteResult, "the rewritten draft fills the input box");
    eq(sent, 0, "the rewrite NEVER auto-sends (onSend stays at zero calls)");

    // ⑧ 记住上次选择：上一步选了 2KB，重开弹窗默认高亮 2KB（刷新后同 key）。
    ok(localStorage.getItem("composer:draftRewriteBudgetKB") === "2",
      "the chosen tier is persisted to localStorage");
    await act(async () => { clickControl(rewriteButton()); await flushTimers(); });
    await act(async () => { await flushTimers(); });
    eq(budgetTierButtons().find((b) => b.getAttribute("aria-pressed") === "true")?.textContent?.trim(), "2 KB",
      "the next open defaults to the remembered tier");
    // 收尾：Esc 关弹窗，不留下挂起的 dialog 影响卸载。
    document.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await act(async () => { await flushTimers(); });
  }
  await act(async () => root.unmount());
  dom.window.close();
}

// ── ④ 改写返回空串：静默降级，输入框保持原样 ──────────────────────────
{
  const dom = installDom();
  applyLabFlags({ draftRewrite: true });
  stubBridge();
  rewriteResult = "";
  localStorage.clear();
  const { root } = await renderComposer();
  await act(async () => {
    const node = document.querySelector<HTMLTextAreaElement>("textarea");
    if (!node) throw new Error("composer textarea did not render");
    node.focus();
    node.setSelectionRange(0, node.value.length);
    node.dispatchEvent(textPasteEvent("原始草稿"));
    await flushTimers();
  });
  eq(composerValue(), "原始草稿", "the draft lands in the composer via its paste handler");
  calls.length = 0;
  await act(async () => { clickControl(rewriteButton()); await flushTimers(); });
  await act(async () => { await flushTimers(); });
  // 1052：先弹档位弹窗，选档（默认 10KB）后才改写。
  ok(budgetDialog() !== null, "the budget dialog opens before rewriting");
  const defaultTier = budgetTierButtons().find((b) => b.getAttribute("aria-pressed") === "true") ?? null;
  clickControl(defaultTier);
  await act(async () => { await flushTimers(); });
  await act(async () => { await flushTimers(); });
  const rewriteCalls = calls.filter((c) => c.method === "RewriteDraftForTab");
  eq(rewriteCalls.length, 1, "the chosen tier sends exactly one rewrite request");
  ok(Array.isArray(rewriteCalls[0]?.args) && (rewriteCalls[0]?.args as unknown[])[3] === 10,
    "the default tier carries contextBudgetKB=10");
  eq(composerValue(), "原始草稿", "an empty rewrite degrades silently: the draft stays untouched");
  rewriteResult = "改写后的结构化草稿";
  await act(async () => root.unmount());
  dom.window.close();
}

// ── tl 1067 ⑤ 开关切换后按钮即时正确（无二连击）+ 双态回原状 ──────────
// 根因形态：设置面板保存后 onChanged 链路喂进来的 settings 若缺
// experimentalDraftRewrite（启动快照 DesktopStartupSettingsView 少该字段时
// 即如此），applyDesktopPreferences 用 `?? false` 把已开的 flag 覆盖成
// false——开关显示「开」但按钮消失，再点一次开关才恢复（用户实测的「二连击」）。
// 本用例直接驱动同一条 onLabFlagsChange 事件链（applyLabFlags 广播）钉双态：
// 关→按钮逐字节消失、开→按钮即时出现，且切换无需重新挂载 Composer。
{
  const dom = installDom();
  applyLabFlags({ draftRewrite: false });
  stubBridge();
  const { root } = await renderComposer();
  ok(rewriteButton() === null, "1067 pre: flag off renders no rewrite button");
  await act(async () => {
    applyLabFlags({ draftRewrite: true });
    await flushTimers();
  });
  ok(rewriteButton() !== null, "1067: flipping the lab flag ON shows the button immediately (no re-mount, no second click)");
  await act(async () => {
    applyLabFlags({ draftRewrite: false });
    await flushTimers();
  });
  ok(rewriteButton() === null, "1067: flipping the lab flag OFF removes the button again (byte-for-byte back to off state)");
  await act(async () => {
    applyLabFlags({ draftRewrite: true });
    await flushTimers();
  });
  ok(rewriteButton() !== null, "1067: flipping back ON restores the button on the first flip (single-toggle contract)");
  await act(async () => root.unmount());
  dom.window.close();
}

// ── tl 1115 运行态按钮次序：改写在左、停止紧邻发送键（位置互换） ──────────
// 用户要求输入栏运行态下「停止」（红色方块）与「草稿改写」位置互换——互换后
// 工具栏次序为 … → 改写 → 停止 → 发送。document order 即视觉从左到右。
{
  const dom = installDom();
  applyLabFlags({ draftRewrite: true });
  stubBridge();
  const { root } = await renderComposer({ running: true });
  const rewrite = rewriteButton();
  const stop = document.querySelector<HTMLButtonElement>("button.composer__btn--stop");
  const send = sendButton();
  ok(rewrite !== null, "tl1115 running: the rewrite button renders");
  ok(stop !== null, "tl1115 running: the stop button renders");
  if (rewrite && stop && send) {
    const positions = [...document.querySelectorAll("button.composer__btn--rewrite, button.composer__btn--stop, button.composer__btn--send")];
    ok(positions.indexOf(rewrite) < positions.indexOf(stop),
      "tl1115 running: the rewrite button sits LEFT of the stop button (swapped from stop-then-rewrite)");
    ok(positions.indexOf(stop) < positions.indexOf(send),
      "tl1115 running: the stop button sits LEFT of the send button (adjacent to send)");
  }
  await act(async () => root.unmount());
  dom.window.close();
}

// ── tl 1115 空闲态：无停止键、改写键照常渲染（两态验收之 idle 侧） ────────
{
  const dom = installDom();
  applyLabFlags({ draftRewrite: true });
  stubBridge();
  const { root } = await renderComposer({ running: false });
  ok(document.querySelector("button.composer__btn--stop") === null, "tl1115 idle: no stop button renders");
  ok(rewriteButton() !== null, "tl1115 idle: the rewrite button still renders");
  await act(async () => root.unmount());
  dom.window.close();
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
