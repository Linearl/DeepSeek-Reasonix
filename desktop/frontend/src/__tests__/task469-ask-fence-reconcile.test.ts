// Run: tsx src/__tests__/task469-ask-fence-reconcile.test.ts
//
// 任务469：ask 弹窗可靠性——webview 内 fence 断点的回归钉。
//
// 断点定位：handleWireEvent 的 runtimeEpoch / sessionGeneration 两道 fence 与
// TurnEventProjector 的 gap 队列都位于 P16 收据行（reducer 内）之前，ask 卡片
// 事件在此被吞时零打点、零恢复——「到了但被吞」与「根本没到」不可分辨，人
// 在场也无法处理。修复后：
//   ① fence 规则逐字保留（judgePromptFenceArrival 只增不改）；
//   ② prompt 类事件（ask/approval/mcp）被 fence 丢弃时标记 reconcile——
//      调用方据此刷新 meta 并请求后端权威重放（自愈）；
//   ③ projector 修复失败/放弃导致 prompt 滞留 gap 队列时经 onPromptsStranded
//      上报，同样走权威重放（重放事件无 seq，绕开卡住的序列）。

import assert from "node:assert/strict";
import type { AppBindings } from "../lib/bridge";
import type { TurnEventReplayView, WireEvent } from "../lib/types";

// ---- ① fence 判定矩阵（judgePromptFenceArrival） ----

const { judgePromptFenceArrival } = await import("../lib/askPanelGate");

type FenceVerdict = ReturnType<typeof judgePromptFenceArrival>;

function expectDrop(verdict: FenceVerdict, reason: "epoch-fence" | "generation-fence", reconcile: boolean, label: string) {
  if (verdict.action !== "drop") {
    throw new Error(`${label}: expected drop, got admit`);
  }
  assert.equal(verdict.reason, reason, `${label} 丢因`);
  assert.equal(verdict.reconcile, reconcile, `${label} 对账标记`);
}

function expectAdmit(verdict: FenceVerdict, label: string) {
  assert.equal(verdict.action, "admit", label);
}

// ask 事件 epoch 过时（本地锚与事件不一致）→ 丢 + 对账。
expectDrop(
  judgePromptFenceArrival({ promptEvent: true, acceptedEpoch: "epoch-old", eventEpoch: "epoch-new" }),
  "epoch-fence", true, "ask 事件 epoch 不一致必须丢",
);

// approval / mcp 同等对待。
expectDrop(
  judgePromptFenceArrival({ promptEvent: true, acceptedEpoch: "a", eventEpoch: "b" }),
  "epoch-fence", true, "approval 事件丢弃同样触发对账",
);

// 普通事件（text）epoch 不一致 → 丢但不对账（行为与 469 之前逐字一致）。
expectDrop(
  judgePromptFenceArrival({ promptEvent: false, acceptedEpoch: "epoch-old", eventEpoch: "epoch-new" }),
  "epoch-fence", false, "普通事件维持纯丢弃，不触发对账",
);

// epoch 一致 / 本地无锚 → 放行。
expectAdmit(judgePromptFenceArrival({ promptEvent: true, acceptedEpoch: "e", eventEpoch: "e" }), "epoch 一致放行");
expectAdmit(judgePromptFenceArrival({ promptEvent: true, acceptedEpoch: undefined, eventEpoch: "e" }), "本地无锚时放行（首次采纳）");
expectAdmit(judgePromptFenceArrival({ promptEvent: true, acceptedEpoch: "e", eventEpoch: undefined }), "旧后端无 epoch 放行");

// sessionGeneration fence：事件带代而本地缺失/不一致 → 丢 + 对账；一致或事件无代 → 放行。
expectDrop(
  judgePromptFenceArrival({ promptEvent: true, eventGeneration: 3 }),
  "generation-fence", true, "本地 meta 缺代时丢",
);
expectDrop(
  judgePromptFenceArrival({ promptEvent: true, localGeneration: 2, eventGeneration: 3 }),
  "generation-fence", true, "代不一致丢弃触发对账",
);
expectAdmit(judgePromptFenceArrival({ promptEvent: true, localGeneration: 3, eventGeneration: 3 }), "代一致放行");
expectAdmit(judgePromptFenceArrival({ promptEvent: true, localGeneration: 3 }), "事件不带代（代 0 omitted）放行");
expectDrop(
  judgePromptFenceArrival({ promptEvent: false, eventGeneration: 3 }),
  "generation-fence", false, "普通事件缺代丢弃不对账（行为不变）",
);

// epoch 与 generation 同时违规 → epoch 优先（与 handleWireEvent 判定顺序一致）。
expectDrop(
  judgePromptFenceArrival({ promptEvent: true, acceptedEpoch: "a", eventEpoch: "b", eventGeneration: 9 }),
  "epoch-fence", true, "双违规时 epoch 先判",
);

// ---- ② projector：卡住的 prompt 滞留 gap 队列必须上报（自愈入口） ----

const binding: Partial<AppBindings> = {};
Object.defineProperty(globalThis, "window", {
  configurable: true,
  value: { go: { main: { App: binding as AppBindings } } } as Window,
});

const { TurnEventProjector } = await import("../lib/turnEventProjection");

function makeAskEvent(seq: number, epoch?: string): WireEvent {
  return { kind: "ask_request", seq, turnId: "t1", runtimeEpoch: epoch, ask: { id: "a1" } } as WireEvent;
}

// 场景 A：TurnEventsForTab 永久失败 → 修复失败终态 → prompt 滞留必须上报。
{
  binding.TurnEventsForTab = async () => {
    throw new Error("backend unavailable");
  };
  const projector = new TurnEventProjector();
  const stranded: string[] = [];
  projector.onPromptsStranded((tabId) => stranded.push(tabId));
  projector.observeRuntime("tab", "epoch-1", 0, 0, false);
  assert.equal(projector.acceptLive("tab", makeAskEvent(2, "epoch-1"), "epoch-1"), false, "gap 中的 ask 先入队");
  for (let i = 0; i < 40 && stranded.length === 0; i += 1) {
    await new Promise((r) => setTimeout(r, 10));
  }
  assert.deepEqual(stranded, ["tab"], "修复失败的 ask 必须经 stranded 上报（权威重放入口）");
}

// 场景 B：重放返回的 runtimeEpoch 与当前不一致（修复放弃路径）→ 同样上报。
{
  const staleReplay: TurnEventReplayView = {
    events: [],
    floorSeq: 0,
    latestSeq: 0,
    nextAfterSeq: 0,
    hasMore: false,
    resetRequired: false,
    transcriptRevision: 1,
    transcriptDigest: "d",
    runtimeEpoch: "epoch-stale",
  };
  binding.TurnEventsForTab = async () => staleReplay;
  const projector = new TurnEventProjector();
  projector.observeRuntime("tab", "epoch-2", 0, 0, false);
  const stranded: string[] = [];
  projector.onPromptsStranded((tabId) => stranded.push(tabId));
  assert.equal(projector.acceptLive("tab", makeAskEvent(5, "epoch-2"), "epoch-2"), false, "epoch 倒挂下的 ask 入 gap 队列");
  for (let i = 0; i < 40 && stranded.length === 0; i += 1) {
    await new Promise((r) => setTimeout(r, 10));
  }
  assert.deepEqual(stranded, ["tab"], "epoch 不匹配放弃修复时 ask 同样上报");
}

// 场景 C：gap 队列只有普通事件 → 不得触发（重放只为 prompt 服务）。
{
  binding.TurnEventsForTab = async () => {
    throw new Error("backend unavailable");
  };
  const projector = new TurnEventProjector();
  projector.observeRuntime("tab", "epoch-3", 0, 0, false);
  const stranded: string[] = [];
  projector.onPromptsStranded((tabId) => stranded.push(tabId));
  assert.equal(projector.acceptLive("tab", { kind: "text", seq: 2, text: "x" } as WireEvent, "epoch-3"), false);
  await new Promise((r) => setTimeout(r, 120));
  assert.deepEqual(stranded, [], "普通事件滞留不触发 prompt 重放");
}

// 场景 D：无 gap 正常连发 → 不触发。
{
  binding.TurnEventsForTab = async () => {
    throw new Error("must not be called");
  };
  const projector = new TurnEventProjector();
  projector.observeRuntime("tab", "epoch-4", 0, 0, false);
  const stranded: string[] = [];
  projector.onPromptsStranded((tabId) => stranded.push(tabId));
  assert.equal(projector.acceptLive("tab", makeAskEvent(1, "epoch-4")), true, "无 gap 的 ask 直接放行（弹窗正常路径）");
  assert.equal(projector.acceptLive("tab", makeAskEvent(2, "epoch-4")), true, "连续 seq 的第二条放行");
  await new Promise((r) => setTimeout(r, 80));
  assert.deepEqual(stranded, [], "正常路径不触发重放");
}

process.stdout.write("task469 ask fence reconcile tests passed\n");
