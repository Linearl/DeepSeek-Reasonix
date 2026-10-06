// Run: tsx src/__tests__/composer-stop-unknown.test.tsx
//
// 任务510（用户反馈：运行中无终止按钮）：runtime 投影处于 unknown 降级态
// （远端 freshness 抖动 / 全局同步失败）时，停止按钮曾被整体隐藏 +
// handleCancel 硬早退——turn 在远端继续跑而用户无从停止。钉住修复契约：
// ① unknown 态停止入口仍可见可用（aria 明示状态未知），点击确实发出取消；
// ② 全局同步失败（store.fail）不再波及本 tab 的停止入口（b 面隔离）；
// ③ 快照明确不可取消 / finishing 的防误操作语义不回归。

import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { Composer } from "../components/Composer";
import { LocaleProvider } from "../lib/i18n";
import { en } from "../locales/en";
import { runtimeStateStore, type RuntimeProjection, type RuntimeState } from "../lib/runtimeStateStore";
import { ToastProvider } from "../lib/toast";
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
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.MutationObserver = dom.window.MutationObserver;
  globalThis.File = dom.window.File;
  globalThis.localStorage = dom.window.localStorage;
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

const stopState = (over: Partial<RuntimeState> = {}): RuntimeState => ({
  schemaVersion: 1, runtimeEpoch: "e510", revision: 1, phase: "executing", running: true,
  turnId: "turn-510", turnStatus: "in_progress", turnEventSeq: 1, pendingPrompt: false,
  cancelRequested: false, cancellable: true, backgroundJobs: 0, activity: "thinking", ...over,
});
const projection = (freshness: "synced" | "unknown", over: Partial<RuntimeState> = {}): RuntimeProjection => ({
  epoch: "app-510", revision: 2, topics: [],
  sessions: [{ tabId: "t510-stop", scope: "project", workspaceRoot: "/fixture", topicId: "topic", sessionPath: "/fixture/s.jsonl",
    sessionGeneration: 1, open: true, remote: false, freshness, state: stopState(over) }],
});

async function renderComposer(props: Partial<Parameters<typeof Composer>[0]> = {}) {
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  const calls = { cancel: 0 };
  let currentProps: Parameters<typeof Composer>[0] = {
    running: false,
    collaborationMode: "normal" as CollaborationMode,
    toolApprovalMode: "ask" as ToolApprovalMode,
    goal: "",
    cwd: "/repo",
    modelLabel: "DeepSeek-R1",
    onSend: () => {},
    onCancel: () => {
      calls.cancel += 1;
      return undefined;
    },
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
  const paint = async (nextProps: Partial<Parameters<typeof Composer>[0]> = {}) => {
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
  return { root, calls, rerender: paint };
}

const stopButton = () => document.querySelector<HTMLButtonElement>(".composer__btn--stop");

console.log("\ncomposer stop entry under unknown runtime state (task 510)");

// ① synced 基线：运行中可见可点（防回归锚）。
{
  const dom = installDom();
  runtimeStateStore.commit(projection("synced"));
  const { root, calls } = await renderComposer({ running: true, tabId: "t510-stop" });
  ok(stopButton() !== null, "synced running composer renders the stop button");
  eq(stopButton()?.disabled, false, "synced stop button is enabled");
  await act(async () => {
    stopButton()?.click();
    await flushTimers();
  });
  eq(calls.cancel, 1, "synced stop click fires onCancel");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

// ② unknown 降级态：按钮仍在、aria 明示状态未知、点击发出取消（修复本体）。
{
  const dom = installDom();
  runtimeStateStore.commit(projection("unknown"));
  const { root, calls } = await renderComposer({ running: true, tabId: "t510-stop" });
  ok(stopButton() !== null, "unknown state keeps the stop button visible");
  eq(stopButton()?.disabled, false, "unknown stop button is not disabled by a stale projection");
  eq(stopButton()?.getAttribute("aria-label"), en["composer.stopUnknownState"], "unknown stop aria-label discloses the degraded state");
  await act(async () => {
    stopButton()?.click();
    await flushTimers();
  });
  eq(calls.cancel, 1, "unknown stop click still fires onCancel (no hard early-return)");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

// ③ unknown + 过期快照 cancellable=false：过期数据不锁死出口。
{
  const dom = installDom();
  runtimeStateStore.commit(projection("unknown", { cancellable: false }));
  const { root, calls } = await renderComposer({ running: true, tabId: "t510-stop" });
  eq(stopButton()?.disabled, false, "stale cancellable=false does not disable the stop exit while unknown");
  await act(async () => {
    stopButton()?.click();
    await flushTimers();
  });
  eq(calls.cancel, 1, "stale cancellable=false still lets the stop request through");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

// ④ synced + 明确不可取消：防误操作语义不回归。
{
  const dom = installDom();
  runtimeStateStore.commit(projection("synced", { cancellable: false }));
  const { root, calls } = await renderComposer({ running: true, tabId: "t510-stop" });
  ok(stopButton() !== null, "fresh uncancellable state still renders the stop button");
  eq(stopButton()?.disabled, true, "fresh cancellable=false disables the button as before");
  await act(async () => {
    stopButton()?.click();
    await flushTimers();
  });
  eq(calls.cancel, 0, "fresh uncancellable click does not fire onCancel");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

// ⑤ finishing：不出按钮（P7 阶梯语义未动）。
{
  const dom = installDom();
  runtimeStateStore.commit(projection("synced", { phase: "finishing" }));
  const { root } = await renderComposer({ running: true, tabId: "t510-stop" });
  eq(stopButton(), null, "finishing composer renders no stop button");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

// ⑥ 全局 failed 不波及本 tab（b 面隔离，UI 级）：任一其他 tab 的同步异常曾经让
// 全部 composer 一起丢按钮；现在全局失败只留在 store.getFailed 供树图降级。
{
  const dom = installDom();
  runtimeStateStore.commit(projection("synced"));
  runtimeStateStore.fail();
  const { root, calls } = await renderComposer({ running: true, tabId: "t510-stop" });
  ok(stopButton() !== null, "global sync failure no longer hides this tab's stop button");
  eq(stopButton()?.disabled, false, "global sync failure leaves the stop exit enabled");
  await act(async () => {
    stopButton()?.click();
    await flushTimers();
  });
  eq(calls.cancel, 1, "global sync failure still lets the stop request through");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

process.stdout.write(`\ncomposer stop unknown: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
