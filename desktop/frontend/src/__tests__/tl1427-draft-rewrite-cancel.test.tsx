// Run: npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1427-draft-rewrite-cancel.test.tsx
//
// tl1427 ①增强中可取消 + 空草稿拦截 + 轨迹通报 acceptance harness:
//  ① 双态:在途按钮从 disabled 变为可点击的取消态(aria/tooltip=取消改写);
//  ② 取消恢复:点取消后回到改写态,草稿不丢(输入框保持原文);
//  ③ 竞态钉:取消后在途结果回来必须整体丢弃——绝不覆盖用户新输入;
//  ④ 快速连点:Enhance→Cancel→Enhance→Cancel 状态始终一致,请求各归各代;
//  ⑤ 轨迹通报:done/cancelled/failed 三种收尾各经 traceDraftRewrite 上报一次,
//     tabId 归属正确(轨迹桥 sink 直捕);
//  ⑥ 空草稿:点击弹 toast 提示(语义对齐 zcode "Type a prompt before
//     enhancing it")、零请求、零弹窗;
//  ⑦ 开关关:按钮不渲染(与引入前逐字节一致,既有族回归主钉在
//     composer-draft-rewrite.test.tsx,此处钉取消态也不存在)。

import { JSDOM } from "jsdom";

import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { Composer } from "../components/Composer";
import { LocaleProvider } from "../lib/i18n";
import { ToastProvider } from "../lib/toast";
import { applyLabFlags } from "../lib/labFlags";
import { setDraftRewriteTraceSink, type DraftRewriteTraceOutcome } from "../lib/draftRewriteTraceBridge";
import { en } from "../locales/en";
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

const TAB_ID = "tab-tl1427";
const CANCEL_LABEL = en["composer.rewriteDraftCancel"];
const REWRITE_LABEL = en["composer.rewriteDraft"];
const EMPTY_TEXT = en["composer.rewriteDraftEmpty"];
const CANCELLED_TOAST = en["composer.rewriteCancelled"];

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
    tabId: TAB_ID,
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

// 手动放行的 deferred:钉在途/取消/迟到结果的时序。
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}

const calls: { method: string; args: unknown[] }[] = [];
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

function stubBridgeWith(deferral: { current: Promise<string> } | null) {
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
          return deferral ? deferral.current : "改写后的结构化草稿";
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

async function pasteDraft(text: string) {
  await act(async () => {
    const node = document.querySelector<HTMLTextAreaElement>("textarea");
    if (!node) throw new Error("composer textarea did not render");
    node.focus();
    node.setSelectionRange(0, node.value.length);
    node.dispatchEvent(textPasteEvent(text));
    await flushTimers();
  });
}

function rewriteButton(): HTMLButtonElement | null {
  return document.querySelector<HTMLButtonElement>("button.composer__btn--rewrite");
}

function budgetDialog(): HTMLElement | null {
  return document.querySelector<HTMLElement>("div.lab-pick-dialog[role=\"dialog\"]");
}

function budgetTierButtons(): HTMLButtonElement[] {
  return [...document.querySelectorAll<HTMLButtonElement>("div.lab-pick-dialog[role=\"dialog\"] button[aria-pressed]")];
}

function clickControl(node: HTMLElement | null) {
  if (!node) return;
  node.dispatchEvent(new window.MouseEvent("click", { bubbles: true, cancelable: true, detail: 1 }));
}

function toastText(): string {
  return [...document.querySelectorAll<HTMLElement>(".toast__text")].map((n) => n.textContent ?? "").join("|");
}

// 选默认档（当前高亮档）发起改写。
async function chooseDefaultTier() {
  ok(budgetDialog() !== null, "pre: the budget dialog is open");
  const tier = budgetTierButtons().find((b) => b.getAttribute("aria-pressed") === "true") ?? null;
  clickControl(tier);
  await act(async () => { await flushTimers(); });
}

type Trace = { tabId: string; outcome: DraftRewriteTraceOutcome; elapsedMs: number };

// ── ①②③ 双态、取消恢复、迟到结果丢弃（竞态钉） ────────────────────────
{
  const dom = installDom();
  applyLabFlags({ draftRewrite: true });
  const gate = deferred<string>();
  const deferral = { current: gate.promise };
  stubBridgeWith(deferral);
  calls.length = 0;
  localStorage.clear();
  const traces: Trace[] = [];
  setDraftRewriteTraceSink((tabId, outcome) => { traces.push({ tabId, outcome, elapsedMs: 0 }); });
  const { root } = await renderComposer();
  await pasteDraft("帮我看看那个 CI 报错");
  await act(async () => { clickControl(rewriteButton()); await flushTimers(); });
  await chooseDefaultTier();

  // ① 在途=可点击的取消态。
  const inFlight = rewriteButton();
  ok(inFlight !== null, "the rewrite button renders during the in-flight request");
  eq(inFlight?.disabled, false, "tl1427: the button is CLICKABLE while a rewrite is in flight (no longer disabled)");
  eq(inFlight?.getAttribute("aria-label"), CANCEL_LABEL, "in flight: aria-label switches to the cancel label");
  eq(inFlight?.className.includes("composer__btn--rewrite-cancel"), true, "in flight: the cancel variant class is applied");

  // ② 取消 → 恢复改写态,草稿不丢。
  await act(async () => { clickControl(inFlight); await flushTimers(); });
  const afterCancel = rewriteButton();
  eq(afterCancel?.getAttribute("aria-label"), REWRITE_LABEL, "after cancel: aria-label is back to the rewrite label");
  eq(afterCancel?.className.includes("composer__btn--rewrite-cancel"), false, "after cancel: the cancel variant class is gone");
  eq(composerValue(), "帮我看看那个 CI 报错", "after cancel: the draft is NOT lost");

  // ③ 竞态钉:迟到结果必须被整体丢弃。
  await act(async () => { gate.resolve("改写后的结构化草稿"); await flushTimers(); });
  await act(async () => { await flushTimers(); });
  eq(composerValue(), "帮我看看那个 CI 报错", "RACE PIN: a late result after cancel is discarded verbatim (the new input survives)");

  // ⑤ 轨迹:cancelled 一次,且迟到的那次不补 done。
  eq(traces.filter((x) => x.outcome === "cancelled").length, 1, "exactly one cancelled trace is reported");
  eq(traces.filter((x) => x.outcome === "done").length, 0, "the discarded late result does NOT report a done trace");
  eq(traces[0]?.tabId, TAB_ID, "the trace carries the owning tabId");

  await act(async () => root.unmount());
  setDraftRewriteTraceSink(undefined);
  dom.window.close();
}

// ── ④ 快速连点:状态一致、请求按代际各归各 ─────────────────────────────
{
  const dom = installDom();
  applyLabFlags({ draftRewrite: true });
  const gate1 = deferred<string>();
  const gate2 = deferred<string>();
  const gates = [gate1, gate2];
  stubBridgeWith(null);
  // 每次调用切换 gate:第一次请求等 gate1、第二次等 gate2。
  const w = window as unknown as { go?: { main?: { App?: unknown } } };
  let rewriteCalls = 0;
  (w.go!.main!.App as Record<string, unknown>).RewriteDraftForTab = async (...args: unknown[]) => {
    calls.push({ method: "RewriteDraftForTab", args });
    const gate = gates[Math.min(rewriteCalls, gates.length - 1)];
    rewriteCalls += 1;
    return gate.promise;
  };
  calls.length = 0;
  localStorage.clear();
  const { root } = await renderComposer();
  await pasteDraft("第二轮草稿");

  // 第一轮:发起 → 在途。
  await act(async () => { clickControl(rewriteButton()); await flushTimers(); });
  await chooseDefaultTier();
  ok(rewriteButton()?.getAttribute("aria-label") === CANCEL_LABEL, "round 1: in-flight shows the cancel label");

  // 取消 → 立即再发起(第二轮在途)。
  await act(async () => { clickControl(rewriteButton()); await flushTimers(); });
  ok(rewriteButton()?.getAttribute("aria-label") === REWRITE_LABEL, "between rounds: back to the rewrite label");
  await act(async () => { clickControl(rewriteButton()); await flushTimers(); });
  await chooseDefaultTier();
  ok(rewriteButton()?.getAttribute("aria-label") === CANCEL_LABEL, "round 2: in-flight again shows the cancel label");
  await act(async () => { clickControl(rewriteButton()); await flushTimers(); });
  ok(rewriteButton()?.getAttribute("aria-label") === REWRITE_LABEL, "after second cancel: the rewrite label is back");

  // 两个旧结果先后迟到:都不覆盖输入。
  await act(async () => { gate1.resolve("旧结果一"); await flushTimers(); });
  await act(async () => { gate2.resolve("旧结果二"); await flushTimers(); });
  eq(composerValue(), "第二轮草稿", "rapid toggles: BOTH stale results are discarded, the draft survives");
  eq(rewriteCalls, 2, "rapid toggles: exactly two rewrite requests were sent (one per Enhance press)");

  await act(async () => root.unmount());
  dom.window.close();
}

// ── ⑤ done/failed 轨迹通报 ────────────────────────────────────────────
{
  const dom = installDom();
  applyLabFlags({ draftRewrite: true });
  const gate = deferred<string>();
  const deferral = { current: gate.promise };
  stubBridgeWith(deferral);
  calls.length = 0;
  localStorage.clear();
  const traces: Trace[] = [];
  setDraftRewriteTraceSink((tabId, outcome) => { traces.push({ tabId, outcome, elapsedMs: 0 }); });
  const { root } = await renderComposer();
  await pasteDraft("成功路径草稿");
  await act(async () => { clickControl(rewriteButton()); await flushTimers(); });
  await chooseDefaultTier();
  await act(async () => { gate.resolve("改写成功的结果"); await flushTimers(); });
  await act(async () => { await flushTimers(); });
  eq(composerValue(), "改写成功的结果", "the successful rewrite still fills the input");
  eq(traces.map((x) => x.outcome).join(","), "done", "exactly one done trace is reported");
  eq(traces[0]?.tabId, TAB_ID, "the done trace carries the owning tabId");
  eq(rewriteButton()?.getAttribute("aria-label"), REWRITE_LABEL, "after completion the button is back to the rewrite label");

  // failed:reject 的请求 → failed 轨迹 + 草稿保持。
  const failGate = deferred<string>();
  (window as unknown as { go?: { main?: { App?: unknown } } }).go!.main!.App!.RewriteDraftForTab = async (...args: unknown[]) => {
    calls.push({ method: "RewriteDraftForTab", args });
    return failGate.promise;
  };
  await act(async () => { clickControl(rewriteButton()); await flushTimers(); });
  await chooseDefaultTier();
  await act(async () => { failGate.reject(new Error("provider down")); await flushTimers(); });
  await act(async () => { await flushTimers(); });
  eq(composerValue(), "改写成功的结果", "a failed rewrite degrades silently (draft keeps the filled text)");
  eq(traces.map((x) => x.outcome).join(","), "done,failed", "the failed rewrite reports exactly one failed trace");
  eq(rewriteButton()?.getAttribute("aria-label"), REWRITE_LABEL, "after failure the button is back to the rewrite label");

  await act(async () => root.unmount());
  setDraftRewriteTraceSink(undefined);
  dom.window.close();
}

// ── ⑥ 空草稿:toast 提示、零请求、零弹窗 ──────────────────────────────
{
  const dom = installDom();
  applyLabFlags({ draftRewrite: true });
  stubBridgeWith(null);
  calls.length = 0;
  localStorage.clear();
  const { root } = await renderComposer();
  await act(async () => { clickControl(rewriteButton()); await flushTimers(); });
  await act(async () => { await flushTimers(); });
  eq(budgetDialog(), null, "empty draft: no budget dialog opens");
  eq(calls.filter((c) => c.method === "RewriteDraftForTab").length, 0, "empty draft: no rewrite request");
  ok(toastText().includes(EMPTY_TEXT), "empty draft: a toast explains what is missing (zcode 'Type a prompt before enhancing it' alignment)");
  await act(async () => root.unmount());
  dom.window.close();
}

// ── ⑦ 开关关:取消态也不存在 ───────────────────────────────────────────
{
  const dom = installDom();
  applyLabFlags({ draftRewrite: false });
  stubBridgeWith(null);
  const { root } = await renderComposer();
  ok(rewriteButton() === null, "flag off: neither the rewrite nor a cancel state renders");
  await act(async () => root.unmount());
  dom.window.close();
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
