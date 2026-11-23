// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1739r2-epoch-fence-replay-turn-dedupe.test.tsx
//
// tl1739r2 链路级回归（真实 useController 驱动，tl1739 原钉同族 harness）：
//
// 复发形态（20261120 切2tab恢复 / 20261121 重启注入双绘 / 20261123 回合结束切tab
// 恢复，三例同族）：回合已结算上屏后，epoch-fence 翻代 + 账本重启判定归零水位
// 线（tl1739 四件修的 ledgerRestarted 分支），对账体经 observeRuntime 以重启账
// 本 floor 重播种 → requestReplay 重灌重启后的账本——重放事件穿过归零的水位线
// 把已结算 turn 重新 apply：replayed turn_started 在表面再造一个同 id 空气泡
// （a:<turnId>:0 第二行），replayed text 重新流满——同一 turn 渲染两次。
//
// 协调线定性（10:1x from-main）：数据层无重复（重启账本每事件一条、重放逐条
// 忠实投递）、display 层双写（渲染入口缺 turn 级去重）。修法=渲染入口按 turn
// id 去重：reconcile 重放通道（isProjectingReplay）上，非表面活跃 turn 且表面
// 已有该 turn 渲染行（a:<turnId>:* 前缀，assistantItems.ensureAssistant 的 id
// 方案）的事件直接丢弃——重放不重复 append。活跃 turn 的回填（fence 吞增量后
// 的 seq-gap 权威重放，tl1444/tl1739 主形态）不受影响：activeTurnId 相等即放
// 行，seq 水位线照常裁决。fence 判定本身与 tab 复用判据不动（non-goals）。
//
// 红转绿：修前本钉红（answerFullOccurrences=2、a:t-1:0 行 id 重复两条）；
// 修后绿（恰渲染一次）。

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

// ---- 可变舞台：epoch 与重启后的账本 ----
// 会话指纹（revision/digest）全程不变：sessionChangedReloadNeeded 指纹门不放行
// reload——表面保持原样（回合已结算上屏），正是三例复发的表面状态。切 tab 的
// 自愈路径（reset 重建，history replace 天然去重）不在本钉范围。
let currentEpoch = "epoch-1";
let snapshotEpoch = "epoch-1";
let snapshotSeq = 4;
let snapshotRunning = true;
let snapshotTurnId: string | undefined = "t-1";
let snapshotReplayAfter = 0;
// 账本读取相位：pre=挂载窗口（空页，挂载期不重灌）；restarted=翻代后的重启账本
// （seq 从 1 重启，内容为同 turn 的 turn_started+全文——resume/重启注入形态）。
let ledgerPhase: "pre" | "restarted" = "pre";
let replayRequests = 0;
let restartedPourSeen = false;
let metaFetches = 0;
const eventHandlers: Array<(e: WireEvent) => void> = [];
const rebuiltHandlers: Array<(tabId?: unknown, runtimeEpoch?: unknown) => void> = [];

const QUESTION = "第五问：回合结束后的重复渲染怎么复现？";
const ANSWER = "回答正文：完整回答。";
const ANSWER_PREFIX = "回答正文：";
const ANSWER_SUFFIX = "完整回答。";
const SESSION_REVISION = 7;
const SESSION_DIGEST = "digest-static";

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
    sessionRevision: SESSION_REVISION,
    sessionDigest: SESSION_DIGEST,
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
    sessionRevision: SESSION_REVISION,
    sessionDigest: SESSION_DIGEST,
    label: "model",
    ready: true,
    runtime: { phase: "ready", epoch: snapshotEpoch },
    running: snapshotRunning,
    turnId: snapshotTurnId,
    turnEventSeq: snapshotSeq,
    turnReplayAfterSeq: snapshotReplayAfter,
    cancellable: snapshotRunning,
    mode: "normal",
    toolApprovalMode: "ask",
    tokenMode: "full",
    active: true,
    cwd: "/repo",
  } as TabMeta;
}

const context: ContextInfo = { used: 0, window: 100, sessionTokens: 0 };
const effortInfo: EffortInfo = { supported: true, current: "auto", default: "auto", levels: ["auto"] };

function persistedUserEntry(): HistoryEntry {
  return {
    entryId: `s/tab-a:r${SESSION_REVISION}:m0:o0`,
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
        revision: SESSION_REVISION,
        revisionKnown: true,
        digest: SESSION_DIGEST,
      }),
      HistoryCheckpointTurnsForTab: async () => [],
      TurnEventsForTab: async (_tabId: string, afterSeq: number): Promise<TurnEventReplayView> => {
        replayRequests += 1;
        if (ledgerPhase === "pre") {
          // 挂载窗口：重启前的账本读取返回空页（latest=0）——挂载期不重灌，
          // 表面内容由直播 paint 建立。
          return { events: [], floorSeq: 0, latestSeq: 0, nextAfterSeq: 0, hasMore: false, resetRequired: false, runtimeEpoch: currentEpoch };
        }
        restartedPourSeen = true;
        // 重启后的账本：seq 从 1 重启（新会话头/runtime 重启形态），重放页盖当
        // 前 epoch（rebind 后由当前 sink 盖章）。
        const events: TurnEventEnvelope[] = [];
        if (afterSeq < 1) {
          events.push({
            turnId: "t-1",
            seq: 1,
            status: "in_progress",
            runtimeEpoch: currentEpoch,
            event: { kind: "turn_started", turnId: "t-1", seq: 1, status: "in_progress", runtimeEpoch: currentEpoch } as WireEvent,
          });
        }
        if (afterSeq < 2) {
          events.push({
            turnId: "t-1",
            seq: 2,
            status: "in_progress",
            runtimeEpoch: currentEpoch,
            event: { kind: "text", turnId: "t-1", seq: 2, text: ANSWER, runtimeEpoch: currentEpoch } as WireEvent,
          });
        }
        return { events, floorSeq: 1, latestSeq: 2, nextAfterSeq: 2, hasMore: false, resetRequired: false, runtimeEpoch: currentEpoch };
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

// 答案文本在表面上的出现次数：跨 assistant 行与 live 气泡统计（「内容渲染两
// 次」的直接读数，tl1739 原钉同款）。
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

// 某一 assistant 行 id 在表面上的条数（display 双写的行级 forensic：重放再造
// 的气泡与已结算行同 id `a:<turnId>:0`）。
function assistantRowCount(id: string): number {
  const state = controller?.state;
  if (!state) return 0;
  return state.items.filter((item) => item.kind === "assistant" && item.id === id).length;
}

// ---- 1) epoch-1 直播 paint → 回合结算：完整回答上屏（恰一次）----
await act(async () => {
  feed({ kind: "turn_started", tabId: "tab-a", seq: 1, turnId: "t-1", status: "in_progress", runtimeEpoch: "epoch-1" } as WireEvent);
  feed({ kind: "text", tabId: "tab-a", seq: 2, turnId: "t-1", text: ANSWER_PREFIX, runtimeEpoch: "epoch-1" } as WireEvent);
  feed({ kind: "text", tabId: "tab-a", seq: 3, turnId: "t-1", text: ANSWER_SUFFIX, runtimeEpoch: "epoch-1" } as WireEvent);
  // rAF 批（stream_batch）按帧刷，给真实帧时间。
  for (let attempt = 0; attempt < 5; attempt += 1) {
    await new Promise((resolvePromise) => setTimeout(resolvePromise, 20));
  }
});
await act(async () => {
  feed({ kind: "turn_done", tabId: "tab-a", seq: 4, turnId: "t-1", status: "completed", runtimeEpoch: "epoch-1" } as WireEvent);
  await flushPromises();
  await flushPromises();
});
// turn_done 的 ancillary 刷新（meta/context/checkpoints）落地后再进 fence 窗口，
// baseline 只统计窗口内的 meta fetch。
await act(async () => {
  for (let attempt = 0; attempt < 10; attempt += 1) {
    await new Promise((resolvePromise) => setTimeout(resolvePromise, 20));
  }
});
assert.equal(answerFullOccurrences(), 1, "baseline：回合结算后完整回答恰渲染一次");
assert.equal(controller?.state.live, undefined, "baseline：回合结束 live 清空");
assert.equal(assistantRowCount("a:t-1:0"), 1, "baseline：结算行 a:t-1:0 恰一条");
assert.equal(controller?.state.activeTurnId, undefined, "baseline：回合结束 activeTurnId 清空");

// ---- 2) runtime 重建通告 + session_changed 携新 epoch → epoch-fence 丢弃 ----
// 同拍喂入（flush 前）：重建的 meta 刷新是异步链，锚此刻仍是 epoch-1，fence 必
// 然判定丢弃并调度防抖对账（事故日志行 reason=epoch-fence session_changed）。
await act(async () => {
  for (const handler of rebuiltHandlers) handler("tab-a", undefined);
  feed({ kind: "session_changed", tabId: "tab-a", sessionReset: true, sessionPath: "/repo/sessions/tab-a.jsonl", runtimeEpoch: "epoch-2" } as WireEvent);
  await flushPromises();
});
assert.equal(answerFullOccurrences(), 1, "fence 丢弃：stale 事件不直接上屏（丢弃规则不变）");

// ---- 3) 翻代窗口：快照换 epoch+账本重启（seq 4→2 倒退）、指纹不变（无 reload）----
currentEpoch = "epoch-2";
snapshotEpoch = "epoch-2";
snapshotSeq = 2;
snapshotRunning = false;
snapshotTurnId = undefined;
snapshotReplayAfter = 0;
ledgerPhase = "restarted";
await act(async () => {
  await new Promise((resolvePromise) => setTimeout(resolvePromise, 550));
  await flushPromises();
  await flushPromises();
});
assert.ok(metaFetches > metaFetchesAtBaseline, "fence 丢弃触发 meta 对账（469 语义保持）");
await waitFor("restarted ledger replay pour", () => restartedPourSeen);
await act(async () => {
  await flushPromises();
  await flushPromises();
  await flushPromises();
});

// ---- 4) 判定：重放重灌不得把已结算 turn 再渲染一遍 ----
// 修前（红）：replayed turn_started 在表面再造 a:t-1:0 空气泡、replayed text
// 重新流满——answerFullOccurrences=2、行 id 重复两条（display 层双写）。
// 修后（绿）：渲染入口按 turn id 去重，重放不重复 append——恰渲染一次。
assert.ok(replayRequests >= 2, "对账体重灌已触达（重启账本重放已运行，排除未触发假绿）");
assert.equal(controller?.state.historyRevision, SESSION_REVISION, "指纹门不放行 reload（表面保持原样，复发形态成立前提）");
assert.equal(answerFullOccurrences(), 1, `同一 turn 内容恰渲染一次（tl1739r2 验收：无重复行）`);
assert.equal(assistantRowCount("a:t-1:0"), 1, `结算行 a:t-1:0 恰一条（display 双写行级 forensic）`);
const userRowCount = controller?.state.items.filter((item) => item.kind === "user" && (item as { text?: string }).text === QUESTION).length ?? 0;
assert.equal(userRowCount, 1, "user 行恰一条");

await act(async () => {
  root.unmount();
  await flushPromises();
});
process.stdout.write("tl1739r2 epoch-fence replay turn dedupe passed\n");
// 运行中轮的巡检定时器与 JSDOM rAF 会挂住事件循环，断言全绿后显式退出。
process.exit(0);
