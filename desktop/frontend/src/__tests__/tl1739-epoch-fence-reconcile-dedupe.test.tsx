// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1739-epoch-fence-reconcile-dedupe.test.tsx
//
// tl1739 链路级回归（真实 useController 驱动，469 harness 同族）：
//
// 复现「转录中同一 turn 内容渲染两次」（20261120 实测+截图，desktop.log 实锤
// `turn content dropped by stale fence; reconciling surface identity
// reason=epoch-fence session_changed`）：
//
// 机制链（file:line）：turn 直播中 runtime 重建翻动 epoch → session_changed
// 携新 epoch 到达，被 epoch-fence 丢弃（askPanelGate judgePromptFenceArrival，
// 丢弃规则 non-goal 不动）→ 防抖对账 schedulePromptFenceReconcile 只刷 meta
// （loadMetaForTab 重 arm 锚 + reanchorEpoch）→ reanchorEpoch 删投影器游标而
// 不重播种（turnEventProjection.ts，tl1653x 交付面；tl1653x 设计本意由下一发
// backend_status 快照经 observeRuntime 以 durable floor 重播种，而 fence 对账
// 路径恰好缺这一发）→ 下一条放行直播增量在 acceptLive 暴露「从 0 起」的假缺
// 口 → requestReplay(0) 全量账本重灌 → 重放事件穿过被 controller_rebuilt 归零
// 的 657 水位线，把已上屏内容重新 apply 一遍 → 同一 turn 内容渲染两次。
//
// 修法（本钉验收）：epoch-fence 恢复走 replace 而非 append——对账体先以权威
// 运行时快照重播种（dispatchRuntimeStatusForTab → observeRuntime，floor 语义
// 同 675），再重挂锚；锚翻代时复用 accepted session_changed 同款指纹门
// （sessionChangedReloadNeeded → loadSessionDataForTab(reset)）把表面重建为
// 持久历史，与「事件未被丢弃」的路径语义对称。fence 判定本身与 tab 复用判据
// 均不动（non-goals）。

import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { useController } from "../lib/useController";
import type { AppBindings } from "../lib/bridge";
import type { ContextInfo, EffortInfo, HistoryEntry, HistorySlice, HistorySliceRequest, Meta, TabMeta, TurnEventEnvelope, TurnEventReplayView, WireEvent } from "../lib/types";

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

// ---- 可变舞台：epoch 与账本 ----
// 模拟「runtime 重建翻 epoch」：本地锚停在 epoch-1，新 runtime 是 epoch-2。
// 持久账本按 sessionPath 存续（rebindTurnEvents 重开同一日志，seq 跨重建连排）。
let currentEpoch = "epoch-1";
// head move（session_changed+SessionReset 的本体）会翻会话指纹：翻代后 revision
// 8 / 新 digest——accepted 路径的指纹门据此放行 replace 重载。
let sessionRevision = 7;
let sessionDigest = "digest-before-head-move";
let metaFetches = 0;
const eventHandlers: Array<(e: WireEvent) => void> = [];
const rebuiltHandlers: Array<(tabId?: unknown, runtimeEpoch?: unknown) => void> = [];

const QUESTION = "第四问：这个重复渲染怎么复现？";
const ANSWER = "回答正文：后续写。";
const ANSWER_PREFIX = "回答正文：";
const ANSWER_SUFFIX = "后续写。";

// 持久账本（composer 直发轮：无 user_input 行；seq 跨重建连排，重放页按
// 新 runtime 盖章——rebind 后由当前 sink 盖当前 epoch）。
function durableLedger(): Array<{ seq: number; event: Omit<WireEvent, "seq"> }> {
  return [
    { seq: 1, event: { kind: "turn_started", turnId: "t-1", status: "in_progress", runtimeEpoch: "epoch-1" } },
    { seq: 2, event: { kind: "text", turnId: "t-1", text: ANSWER_PREFIX, runtimeEpoch: "epoch-1" } },
    { seq: 3, event: { kind: "text", turnId: "t-1", text: ANSWER_SUFFIX, runtimeEpoch: "epoch-2" } },
  ];
}

function metaForTab(): Meta {
  return {
    label: "model",
    ready: true,
    runtime: { phase: "ready", epoch: currentEpoch },
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
    sessionPath: "/repo/sessions/tab-a.jsonl",
    sessionRevision,
    sessionDigest,
  } as Meta;
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
    sessionRevision,
    sessionDigest,
    label: "model",
    ready: true,
    runtime: { phase: "ready", epoch: currentEpoch },
    running: true,
    turnId: "t-1",
    // 权威快照的账本位（连排账本：latest=3 ≥ 已折叠水位——tl1739 后归零判定
    // 依此判「连续账本，保持水位线」）；turn 直播中 floor=turnStartSeq-1=0。
    turnEventSeq: 3,
    turnReplayAfterSeq: 0,
    cancellable: true,
    mode: "normal",
    toolApprovalMode: "ask",
    tokenMode: "full",
    active: true,
    cwd: "/repo",
  } as TabMeta;
}

const context: ContextInfo = { used: 0, window: 100, sessionTokens: 0 };
const effortInfo: EffortInfo = { supported: true, current: "auto", default: "auto", levels: ["auto"] };

// 持久历史页（head 切换后、轮未结算时的会话日志：只有 user 行——流式增量
// 在 turn 账本里，未结算不落会话日志）。
function persistedUserEntry(): HistoryEntry {
  return {
    entryId: `s/tab-a:r${sessionRevision}:m0:o0`,
    turn: 1,
    order: 0,
    message: { role: "user", content: QUESTION },
    refs: [],
  };
}

window.runtime = {
  EventsOn: (name: string, cb: (payload: unknown) => void) => {
    if (name === "agent:event") eventHandlers.push(cb as (e: WireEvent) => void);
    if (name === "runtime:rebuilt") rebuiltHandlers.push(cb as (tabId?: unknown, runtimeEpoch?: unknown) => void);
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
      HistoryForTab: async () => [persistedUserEntry().message],
      HistoryPageForTab: async () => ({
        messages: [persistedUserEntry().message],
        startTurn: 1,
        endTurn: 1,
        totalTurns: 1,
        hasOlder: false,
      }),
      HistorySliceForTab: async (_tabId: string, _req: HistorySliceRequest): Promise<HistorySlice> => ({
        entries: [persistedUserEntry()],
        nextCursor: "",
        hasOlder: false,
        totalTurns: 1,
        startTurn: 1,
        endTurn: 1,
        stale: false,
        revision: sessionRevision,
        revisionKnown: true,
        digest: sessionDigest,
      }),
      HistoryCheckpointTurnsForTab: async () => [],
      TurnEventsForTab: async (_tabId: string, afterSeq: number): Promise<TurnEventReplayView> => {
        const events: TurnEventEnvelope[] = durableLedger()
          .filter((line) => line.seq > afterSeq)
          .map((line) => ({
            turnId: "t-1",
            seq: line.seq,
            status: "in_progress",
            runtimeEpoch: "epoch-2",
            event: { ...line.event, seq: line.seq } as WireEvent,
          }));
        return {
          events,
          floorSeq: 0,
          latestSeq: 3,
          nextAfterSeq: 3,
          hasMore: false,
          resetRequired: false,
          runtimeEpoch: "epoch-2",
        };
      },
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

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("missing root");
const root = createRoot(rootEl);

await act(async () => {
  root.render(<Probe />);
  await flushPromises();
});
await waitFor("active tab", () => controller?.activeTabId === "tab-a");
// 挂载 hydrate 异步落地后再 paint，排除 history apply 与直播 paint 的竞态。
await act(async () => {
  for (let attempt = 0; attempt < 10; attempt += 1) {
    await new Promise((resolvePromise) => setTimeout(resolvePromise, 20));
  }
});
const metaFetchesAtBaseline = metaFetches;
assert.ok(controller, "controller mounted");

function feed(e: WireEvent) {
  for (const handler of eventHandlers) handler(e);
}

// 答案文本在表面上的出现次数：跨 assistant 行与 live 气泡统计完整答案与
// 前缀片段（「内容渲染两次」的直接读数）。
function answerFullOccurrences(): number {
  const state = controller?.state;
  if (!state) return 0;
  let count = 0;
  for (const item of state.items) {
    if (item.kind !== "assistant") continue;
    count += ((item as { text?: string }).text ?? "").split(ANSWER).length - 1;
  }
  count += (state.live?.text ?? "").split(ANSWER).length - 1;
  return count;
}

// ---- 1) epoch-1 直播 paint：流式回答（未结算，转录中；user 行由挂载 hydrate
// 的持久页带来——composer 轮的 user 行不进 turn 账本，与生产一致）----
await act(async () => {
  feed({ kind: "turn_started", tabId: "tab-a", seq: 1, turnId: "t-1", status: "in_progress", runtimeEpoch: "epoch-1" } as WireEvent);
  feed({ kind: "text", tabId: "tab-a", seq: 2, turnId: "t-1", text: ANSWER_PREFIX, runtimeEpoch: "epoch-1" } as WireEvent);
  // rAF 批（stream_batch）按帧刷，给真实帧时间。
  for (let attempt = 0; attempt < 5; attempt += 1) {
    await new Promise((resolvePromise) => setTimeout(resolvePromise, 20));
  }
});
assert.equal(
  controller?.state.items.some((item) => item.kind === "user" && (item as { text?: string }).text === QUESTION),
  true,
  "baseline：user 行已上屏",
);
assert.equal(controller?.state.live?.text, ANSWER_PREFIX, "baseline：流式前缀已上屏（转录中）");

// ---- 2) runtime 重建通告（无 epoch 变体：锚不动，657 水位线归零）----
await act(async () => {
  for (const handler of rebuiltHandlers) handler("tab-a", undefined);
  await flushPromises();
});

// ---- 3) session_changed 携新 epoch → epoch-fence 丢弃（事故日志行）----
await act(async () => {
  feed({ kind: "session_changed", tabId: "tab-a", sessionReset: true, sessionPath: "/repo/sessions/tab-a.jsonl", runtimeEpoch: "epoch-2" } as WireEvent);
  await flushPromises();
});
assert.equal(controller?.state.live?.text, ANSWER_PREFIX, "fence 丢弃：stale 事件不直接上屏（丢弃规则不变）");

// ---- 4) 防抖对账窗口：meta 刷新重 arm 锚（epoch-2）+ reanchorEpoch 删游标 ----
currentEpoch = "epoch-2";
sessionRevision = 8;
sessionDigest = "digest-after-head-move";
await act(async () => {
  await new Promise((resolvePromise) => setTimeout(resolvePromise, 550));
  await flushPromises();
  await flushPromises();
});
assert.ok(metaFetches > metaFetchesAtBaseline, "fence 丢弃触发 meta 对账（469 语义保持）");

// ---- 5) 下一条直播增量放行 → 假缺口 → 账本重灌 ----
await act(async () => {
  feed({ kind: "text", tabId: "tab-a", seq: 3, turnId: "t-1", text: ANSWER_SUFFIX, runtimeEpoch: "epoch-2" } as WireEvent);
  await flushPromises();
  await flushPromises();
  await flushPromises();
  await flushPromises();
});

// 修复前：reanchorEpoch 删游标不播种 → requestReplay(0) 全量重灌，重放事件
// 穿过归零水位线把已上屏前缀重新 apply——流式文本翻倍（事故形态）。
// 修复后：对账观察到锚翻代，走 accepted session_changed 同款指纹门 replace，
// 表面重建为持久历史，被吞增量由 floor 有界的权威重放回填——恰渲染一次。
assert.equal(answerFullOccurrences(), 1, "转录中同一 turn 内容恰渲染一次（tl1739 验收：无重复行）");
const liveText = controller?.state.live?.text ?? "";
assert.equal(liveText.includes(`${ANSWER_PREFIX}${ANSWER_PREFIX}`), false, `流式文本不得翻倍，got ${JSON.stringify(liveText)}`);
const userRowCount = controller?.state.items.filter((item) => item.kind === "user" && (item as { text?: string }).text === QUESTION).length ?? 0;
assert.equal(userRowCount, 1, "user 行恰一条（「四问消息出现两条」形态回归钉）");
const assistantRowsWithAnswer = controller?.state.items.filter((item) => item.kind === "assistant" && ((item as { text?: string }).text ?? "").includes(ANSWER)).length ?? 0;
assert.ok(assistantRowsWithAnswer <= 1, `已结算 assistant 行至多一条含完整答案，got ${assistantRowsWithAnswer}`);

await act(async () => {
  root.unmount();
  await flushPromises();
});
process.stdout.write("tl1739 epoch-fence reconcile dedupe passed\n");
// 运行中轮的巡检定时器与 JSDOM rAF 会挂住事件循环，断言全绿后显式退出。
process.exit(0);
