// Run: tsx src/__tests__/task469-fence-reconcile-harness.test.tsx
//
// 任务469 链路级回归（真实 useController 驱动，覆盖 handleWireEvent fence 段）：
//
// 复现矩阵「ask 前切走切回」的 fence 形态——runtime:rebuilt 丢失时本地 epoch
// 锚过时，新 runtime 的 ask_request 到达 webview 却被 epoch fence 丢弃：
//   修复前：静默丢弃，无打点、无恢复，面板永不挂载；
//   修复后：丢时落 [ask-panel] warn 行 + 调度一次防抖权威对账（刷新该 tab
//   meta → 请求 ReplayPendingPromptsForTab）→ 后端重放的 ask 携新 epoch 到
//   达 → 通过 fence → 面板挂载。普通事件（text）被 fence 丢弃维持纯丢弃，
//   不触发对账（行为与 469 之前一致）。

import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { useController } from "../lib/useController";
import type { AppBindings } from "../lib/bridge";
import type { ContextInfo, EffortInfo, Meta, TabMeta, WireEvent } from "../lib/types";

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
globalThis.Event = dom.window.Event;
globalThis.CustomEvent = dom.window.CustomEvent;
globalThis.localStorage = dom.window.localStorage;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

function flushPromises(): Promise<void> {
  return new Promise((resolvePromise) => setTimeout(resolvePromise, 0));
}

async function waitFor(label: string, predicate: () => boolean) {
  for (let attempt = 0; attempt < 60; attempt += 1) {
    await act(async () => {
      await flushPromises();
    });
    if (predicate()) return;
  }
  throw new Error(`timed out waiting for ${label}`);
}

function metaForTab(): Meta {
  return {
    label: "model",
    ready: true,
    runtime: { phase: "ready", epoch: projectedEpoch },
    eventChannel: "agent:event",
    cwd: "/repo",
    workspaceRoot: "/repo",
    workspaceName: "repo",
    workspacePath: "/repo",
    autoApproveTools: false,
    bypass: false,
    collaborationMode: "normal",
    toolApprovalMode: "ask",
    tokenMode: "full",
    goal: "",
    goalStatus: "stopped",
  };
}

function tabMeta(): TabMeta {
  return {
    id: "tab-a",
    scope: "project",
    workspaceRoot: "/repo",
    workspaceName: "repo",
    workspacePath: "/repo",
    topicId: "topic-a",
    topicTitle: "General",
    sessionPath: "/repo/sessions/tab-a.jsonl",
    label: "model",
    ready: true,
    runtime: { phase: "ready", epoch: projectedEpoch },
    running: false,
    cancellable: false,
    mode: "normal",
    toolApprovalMode: "ask",
    tokenMode: "full",
    active: true,
    cwd: "/repo",
  } as TabMeta;
}

const context: ContextInfo = { used: 0, window: 100, sessionTokens: 0 };
const effortInfo: EffortInfo = { supported: true, current: "auto", default: "auto", levels: ["auto"] };

// 模拟「切走切回丢 runtime:rebuilt」：本地锚停在 epoch-1，新 runtime 是 epoch-2。
let projectedEpoch = "epoch-1";
let metaFetches = 0;
let scopedReplayCalls = 0;
const eventHandlers: Array<(e: WireEvent) => void> = [];

window.runtime = {
  EventsOn: (name: string, cb: (payload: unknown) => void) => {
    if (name === "agent:event") eventHandlers.push(cb as (e: WireEvent) => void);
    return () => {};
  },
  BrowserOpenURL: () => {},
};
window.go = {
  main: {
    App: {
      ListTabs: async () => [tabMeta()],
      MetaForTab: async () => {
        metaFetches += 1;
        return metaForTab();
      },
      ContextUsageForTab: async () => context,
      EffortForTab: async () => effortInfo,
      BalanceForTab: async () => ({ available: false, display: "" }),
      JobsForTab: async () => [],
      CheckpointsForTab: async () => [],
      HistoryForTab: async () => [],
      HistoryPageForTab: async () => ({ messages: [], startTurn: 0, endTurn: 0, totalTurns: 0, hasOlder: false }),
      HistoryCheckpointTurnsForTab: async () => [],
      ReplayPendingPrompts: async () => {},
      ReplayPendingPromptsForTab: async () => {
        scopedReplayCalls += 1;
      },
      SetActiveTab: async () => {},
    } as Partial<AppBindings> as AppBindings,
  },
};

type Controller = ReturnType<typeof useController>;
let controller: Controller | undefined;

function Probe() {
  controller = useController();
  return null;
}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("missing root");
const root = createRoot(rootEl);

await act(async () => {
  root.render(<Probe />);
  await flushPromises();
});
await waitFor("active tab", () => controller?.activeTabId === "tab-a");
await act(async () => {
  await flushPromises();
  await flushPromises();
});
const metaFetchesAtBaseline = metaFetches;
const replayCallsAtBaseline = scopedReplayCalls;
assert.equal(controller?.state.ask, undefined, "初始无 ask 卡片");

// 1) 新 runtime 的 ask 到达（runtime:rebuilt 已丢，本地锚还是 epoch-1）：
//    fence 丢弃，但必须调度对账而非静默吞。
await act(async () => {
  for (const handler of eventHandlers) {
    handler({ kind: "ask_request", tabId: "tab-a", runtimeEpoch: "epoch-2", turnId: "t-1", ask: { id: "a1", question: "继续吗？" } } as WireEvent);
  }
  await flushPromises();
});
assert.equal(controller?.state.ask, undefined, "epoch 过时的 ask 不直接上屏（防跨 runtime 泄漏，规则不变）");

// 2) 对账窗口：meta 刷新到新 epoch → 请求后端权威重放。
await act(async () => {
  projectedEpoch = "epoch-2";
  await new Promise((resolvePromise) => setTimeout(resolvePromise, 550));
  await flushPromises();
  await flushPromises();
});
assert.ok(
  metaFetches > metaFetchesAtBaseline,
  `fence 丢弃必须触发 meta 对账刷新（baseline=${metaFetchesAtBaseline} now=${metaFetches}）`,
);
assert.equal(scopedReplayCalls - replayCallsAtBaseline, 1, "fence 丢弃必须恰好触发一次 ReplayPendingPromptsForTab");

// 3) 后端重放的 ask（携新 epoch）通过 fence → 面板挂载。
await act(async () => {
  for (const handler of eventHandlers) {
    handler({ kind: "ask_request", tabId: "tab-a", runtimeEpoch: "epoch-2", turnId: "t-1", ask: { id: "a1", question: "继续吗？" } } as WireEvent);
  }
  await flushPromises();
});
assert.equal(controller?.state.ask?.id, "a1", "重放的 ask 通过刷新后的 fence，面板挂起");

// 4) 普通事件被 fence 丢弃维持纯丢弃：不再触发对账。
const replayCallsBeforeText = scopedReplayCalls;
await act(async () => {
  for (const handler of eventHandlers) {
    handler({ kind: "text", tabId: "tab-a", runtimeEpoch: "epoch-9", text: "stale" } as WireEvent);
  }
  await new Promise((resolvePromise) => setTimeout(resolvePromise, 450));
  await flushPromises();
});
assert.equal(scopedReplayCalls - replayCallsBeforeText, 0, "text 被 fence 丢弃不触发对账（行为不变）");

await act(async () => {
  root.unmount();
  await flushPromises();
});
process.stdout.write("task469 fence reconcile harness passed\n");
