// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1848-ask-live-arrival.test.tsx
//
// tl1848 存在性钉（P1 致命：ask 弹窗不弹，另一台机器包113 前台 autopilot 实测）：
//
// 读码收窄修正：任务书预判的「活动 ask 过桥通道缺失」不成立——Go 侧
// eventwire.ToWire 把 AskRequest 映射为 wire "ask_request"（wire.go:653），
// tabEventSink.Emit 对每个事件走 emitRuntimeEvent("agent:event", …)
// （tabs.go:1595），前端 reducer 有 case "ask_request"（useController.ts:2634）。
// 真断点必是 handleWireEvent 关卡链的条件性吞点。本钉把「前台活流 ask →
// state.ask 填充（AskCard 渲染前提）」的存在性锁死：
//   A  基线活流形态（带 seq、无 epoch/generation 锚）→ 面板挂起；
//   B  跨会话身份一致形态（事件与本地 meta 同 sessionGeneration）→ 面板挂起。
// 任一场景红 = 活流主链存在条件性吞点，即 tl1848 现场的断点实证。

import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { useController } from "../lib/useController";
import type { AppBindings } from "../lib/bridge";
import type { ContextInfo, EffortInfo, Meta, TabMeta, WireEvent } from "../lib/types";

const dom = new JSDOM("<!doctype html><html><body><div id=\"root-a\"></div><div id=\"root-b\"></div><div id=\"root-c\"></div></body></html>", {
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

// 场景 B 的跨会话身份：本地 meta 与事件携带同一 sessionGeneration。
// 0 = 场景 A：双方都不带（新鲜 tab 的最常见活流形态）。
let localSessionGeneration: number | undefined = undefined;

function metaForTab(): Meta {
  const meta: Meta = {
    label: "model",
    ready: true,
    runtime: { phase: "ready", epoch: "" },
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
  if (localSessionGeneration !== undefined) {
    (meta as { sessionGeneration?: number }).sessionGeneration = localSessionGeneration;
  }
  return meta;
}

function tabMeta(): TabMeta {
  const meta = {
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
    runtime: { phase: "ready", epoch: "" },
    running: false,
    cancellable: false,
    mode: "normal",
    toolApprovalMode: "ask",
    tokenMode: "full",
    active: true,
    cwd: "/repo",
  } as TabMeta;
  if (localSessionGeneration !== undefined) {
    meta.sessionGeneration = localSessionGeneration;
  }
  return meta;
}

const context: ContextInfo = { used: 0, window: 100, sessionTokens: 0 };
const effortInfo: EffortInfo = { supported: true, current: "auto", default: "auto", levels: ["auto"] };

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
      MetaForTab: async () => metaForTab(),
      ContextUsageForTab: async () => context,
      EffortForTab: async () => effortInfo,
      BalanceForTab: async () => ({ available: false, display: "" }),
      JobsForTab: async () => [],
      CheckpointsForTab: async () => [],
      HistoryForTab: async () => [],
      HistoryPageForTab: async () => ({ messages: [], startTurn: 0, endTurn: 0, totalTurns: 0, hasOlder: false }),
      HistoryCheckpointTurnsForTab: async () => [],
      ReplayPendingPrompts: async () => {},
      ReplayPendingPromptsForTab: async () => {},
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

const rootElA = document.getElementById("root-a");
const rootElB = document.getElementById("root-b");
const rootElC = document.getElementById("root-c");
if (!rootElA || !rootElB || !rootElC) throw new Error("missing roots");

async function mountAndSettle(rootEl: HTMLElement): Promise<{ unmount: () => Promise<void> }> {
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
  return {
    unmount: async () => {
      await act(async () => {
        root.unmount();
        await flushPromises();
      });
    },
  };
}

function emitLive(e: WireEvent): void {
  for (const handler of eventHandlers) handler(e);
}

// 场景 A：基线活流——前台 tab 正常收 ask（带账本 seq、无身份锚），面板必须挂起。
const surfaceA = await mountAndSettle(rootElA);
assert.equal(controller?.state.ask, undefined, "A: 初始无 ask 卡片");
await act(async () => {
  emitLive({
    kind: "ask_request",
    tabId: "tab-a",
    turnId: "t-live-1",
    seq: 1,
    emittedAt: Date.now(),
    ask: { id: "a-live-1", questions: [{ id: "q1", prompt: "继续吗？", options: [{ label: "继续" }] }] },
  } as WireEvent);
  await flushPromises();
});
assert.equal(
  controller?.state.ask?.id,
  "a-live-1",
  "A: 前台活流 ask 必须填充 state.ask（AskCard 渲染前提；红=活流主链有条件性吞点）",
);
assert.equal(controller?.state.pendingPrompt, true, "A: 活流 ask 进入 prompt 等待态");

await surfaceA.unmount();

// 场景 B：跨会话身份一致——事件与本地 meta 携带同一 sessionGeneration，
// fence/generation 关卡必须放行（跨会话通信翻动 generation 但身份对齐时不吞）。
localSessionGeneration = 7;
eventHandlers.length = 0;
const surfaceB = await mountAndSettle(rootElB);
assert.equal(controller?.state.ask, undefined, "B: 初始无 ask 卡片");
await act(async () => {
  emitLive({
    kind: "ask_request",
    tabId: "tab-a",
    turnId: "t-live-2",
    seq: 1,
    sessionGeneration: 7,
    emittedAt: Date.now(),
    ask: { id: "a-live-2", questions: [{ id: "q1", prompt: "授权执行？", options: [{ label: "允许" }] }] },
  } as WireEvent);
  await flushPromises();
});
assert.equal(
  controller?.state.ask?.id,
  "a-live-2",
  "B: sessionGeneration 一致的活流 ask 必须填充 state.ask（身份对齐不吞；红=身份关卡误伤活流）",
);

await surfaceB.unmount();

// 场景 C：活流 seq 缺口 → 账本权威重放恢复——活流 ask 因缺口进 gap queue
// 后，repair 从 TurnEventsForTab 回补缺口事件，gap queue 的活流 ask 在
// repair 终端按序 drain，state.ask 必须最终填充（面板不因缺口丢失）。
eventHandlers.length = 0;
localSessionGeneration = undefined;
let turnEventsCalls = 0;
window.go.main.App.TurnEventsForTab = async (_tabID: string, afterSeq: number) => {
  turnEventsCalls += 1;
  if (afterSeq !== 1) {
    return { events: [], floorSeq: 0, latestSeq: 1, nextAfterSeq: afterSeq, hasMore: false, resetRequired: false };
  }
  return {
    events: [
      { turnId: "t-live-3", seq: 2, status: "in_progress", event: { kind: "text", text: "准备工作…" } },
    ],
    floorSeq: 0,
    latestSeq: 2,
    nextAfterSeq: 2,
    hasMore: false,
    resetRequired: false,
  };
};
const surfaceC = await mountAndSettle(rootElC);
assert.equal(controller?.state.ask, undefined, "C: 初始无 ask 卡片");
await act(async () => {
  // 先放行 seq=1 建立游标，再让 ask 携 seq=3 制造缺口（2 缺失 → gap queue）。
  // 修复后 repair 终端 drain 在同一微任务窗口内完成回补，中间态（gap queue
  // 悬挂）不再可观测——这正是修复语义本身。
  emitLive({ kind: "turn_started", tabId: "tab-a", turnId: "t-live-3", seq: 1 } as WireEvent);
  emitLive({
    kind: "ask_request",
    tabId: "tab-a",
    turnId: "t-live-3",
    seq: 3,
    emittedAt: Date.now(),
    ask: { id: "a-live-3", questions: [{ id: "q1", prompt: "缺口恢复后弹出？", options: [{ label: "是" }] }] },
  } as WireEvent);
  await flushPromises();
});
await waitFor("gap repair drain", () => controller?.state.ask?.id === "a-live-3");
assert.equal(
  controller?.state.ask?.id,
  "a-live-3",
  "C: seq 缺口恢复后活流 ask 必须经 gap queue drain 填充 state.ask（权威重放兜底不丢面板）",
);
assert.ok(turnEventsCalls >= 1, "C: 缺口必须触发 TurnEventsForTab 账本回补");

await surfaceC.unmount();
delete (window.go.main.App as Partial<AppBindings>).TurnEventsForTab;
process.stdout.write("tl1848 ask live arrival passed\n");
// 三场景 harness 的 hydrate retry timer 在 unmount 后仍持有事件循环句柄
// （与 useController 的启动轮询面相关，非本钉关注面）；断言已全部通过，
// 显式退出避免进程悬挂。
process.exit(0);
