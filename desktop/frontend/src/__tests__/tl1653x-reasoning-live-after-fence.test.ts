// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1653x-reasoning-live-after-fence.test.ts
//
// tl1653x 形态②钉：「工作中/等待模型响应」期间思考块不出现，切 tab 才出。
//
// 根因链（file:line）：controller 重建（resume/Diet 改写/effort switch）翻动
// runtime epoch → 流式 reasoning/text 事件被 wire epoch-fence 丢弃
//（useController handleWireEvent → judgePromptFenceArrival → drop）→ fence 对账
// （schedulePromptFenceReconcile → refreshMetaOnlyForTab → loadMetaForTab）只重
// arm fence 锚（runtimeEpochByTabRef），投影器 epochByTab 仍停旧代 → 后续事件
// 过 fence 进 acceptLive 暴露 seq gap → replayGap 在 epoch 校验处早退
//（requestedEpoch=新 vs currentEpoch=旧）→ 被丢增量永不回填，思考块直到切 tab
// （observeRuntime 重置游标 + hydrate 重读 history）才出现。
//
// 修法：TurnEventProjector.reanchorEpoch（observeRuntime epoch 翻动分支同语义），
// loadMetaForTab 重 arm fence 锚同拍调用——两处 epoch 权威不再分叉。
//
// 本钉在 projector 级复现修复前后两条路：
//   ① 修前形态（用同代 reanchor 缺失模拟）：投影器停在旧代时，回填页带着新代
//      runtimeEpoch → epoch-mismatch 早退，reasoning 增量不落 handler；
//   ② 修后形态：reanchorEpoch(new) 后同一条回填链放行，reasoning/text 增量
//      实时落 handler（无需切 tab）。

import assert from "node:assert/strict";
import type { AppBindings } from "../lib/bridge";
import type { TurnEventReplayView, WireEvent } from "../lib/types";

type ReplaySink = (view: TurnEventReplayView) => void;
let respondReplay!: ReplaySink;
let replayCalls = 0;
let lastReplayCall: { afterSeq: number }[] = [];

const binding: Partial<AppBindings> = {
  TurnEventsForTab: (_tabId: string, afterSeq: number) => {
    replayCalls += 1;
    lastReplayCall.push({ afterSeq });
    return new Promise<TurnEventReplayView>((resolve) => { respondReplay = (view) => resolve(view); });
  },
};
Object.defineProperty(globalThis, "window", {
  configurable: true,
  value: { go: { main: { App: binding as AppBindings } } } as Window,
});

const { TurnEventProjector } = await import("../lib/turnEventProjection");

function drain(times = 40): Promise<void> {
  let chain = Promise.resolve();
  for (let i = 0; i < times; i += 1) chain = chain.then(() => Promise.resolve());
  return chain;
}

// 场景 ①（修前形态回归钉，投影器未 reanchor）：回填页带着新代 epoch → 早退。
{
  const projected: WireEvent[] = [];
  const projector = new TurnEventProjector();
  projector.bind((event) => projected.push(event));
  // 前端已锚旧代（启动 hydrate 时刻的 runtime）
  projector.observeRuntime("tab-a", "epoch-old", 10, 10, false);
  // controller 重建后新代事件 seq=13 到达（seq 11/12 的增量被 fence 丢过）
  const accepted = projector.acceptLive("tab-a", { kind: "reasoning", seq: 13, runtimeEpoch: "epoch-old", text: "think" }, "epoch-old");
  assert.equal(accepted, false, "a gap event is queued, not applied");
  await drain();
  assert.ok(replayCalls >= 1, "gap repair requested");
  // 回填页返回**新代**数据（后端 controller 已重建）；投影器还停在旧代
  respondReplay({
    events: [
      { turnId: "t1", seq: 11, status: "in_progress", event: { kind: "reasoning", turnId: "t1", text: "think-11", runtimeEpoch: "epoch-new" } },
      { turnId: "t1", seq: 12, status: "in_progress", event: { kind: "reasoning", turnId: "t1", text: "think-12", runtimeEpoch: "epoch-new" } },
    ],
    floorSeq: 11,
    latestSeq: 12,
    nextAfterSeq: 12,
    hasMore: false,
    resetRequired: false,
    runtimeEpoch: "epoch-new",
  });
  await drain();
  assert.equal(projected.length, 0, "PRE-FIX SHAPE: a repair whose replay epoch differs from the projector's stale epoch early-returns; the dropped reasoning deltas stay unrendered");
  projector.release("tab-a");
}

// 场景 ②（修后形态）：reanchorEpoch 与 fence 锚同拍后，同一条回填链放行。
{
  const projected: WireEvent[] = [];
  const projector = new TurnEventProjector();
  projector.bind((event) => projected.push(event));
  projector.observeRuntime("tab-b", "epoch-old", 10, 10, false);
  const accepted = projector.acceptLive("tab-b", { kind: "reasoning", seq: 13, runtimeEpoch: "epoch-old", text: "think" }, "epoch-old");
  assert.equal(accepted, false, "gap queued as before");
  await drain();
  // fence 对账拿到新 meta：loadMetaForTab 重 arm 锚并 reanchor（修复点）
  projector.reanchorEpoch("tab-b", "epoch-new");
  // reanchor 作废了旧 repair（generation++），旧回填响应返回时必须被丢弃
  respondReplay({
    events: [],
    floorSeq: 1,
    latestSeq: 1,
    nextAfterSeq: 1,
    hasMore: false,
    resetRequired: false,
    runtimeEpoch: "epoch-old",
  });
  await drain();
  assert.equal(projected.length, 0, "the voided old-epoch repair must not project");
  // 真实时序的下一次 backend_status dispatch：observeRuntime 以新代播种游标
  //（replayAfter=10 = 新 controller 报告的 durable floor）
  projector.observeRuntime("tab-b", "epoch-new", 14, 10, true);
  // 下一条放行事件（fence 锚与投影器同代）暴露缺口 → 新 repair 以新代跑
  const accepted2 = projector.acceptLive("tab-b", { kind: "reasoning", seq: 14, runtimeEpoch: "epoch-new", text: "think" }, "epoch-new");
  assert.equal(accepted2, false, "gap re-exposed after reanchor");
  await drain();
  // 新代回填页（含被 fence 丢掉的思考增量）→ 必须实时投影（不等切 tab）
  respondReplay({
    events: [
      { turnId: "t1", seq: 11, status: "in_progress", event: { kind: "reasoning", turnId: "t1", text: "think-11", runtimeEpoch: "epoch-new" } },
      { turnId: "t1", seq: 12, status: "in_progress", event: { kind: "reasoning", turnId: "t1", text: "think-12", runtimeEpoch: "epoch-new" } },
      { turnId: "t1", seq: 13, status: "in_progress", event: { kind: "reasoning", turnId: "t1", text: "think-13", runtimeEpoch: "epoch-new" } },
    ],
    floorSeq: 11,
    latestSeq: 13,
    nextAfterSeq: 13,
    hasMore: false,
    resetRequired: false,
    runtimeEpoch: "epoch-new",
  });
  await drain();
  const seqs = projected.map((event) => event.seq);
  assert.deepEqual(seqs, [11, 12, 13, 14], "POST-FIX SHAPE: reanchored projector fills the fence-dropped reasoning deltas live (11-13 replayed, 14 queued live tail)");
  assert.equal(projected[0].text, "think-11", "the reasoning content dropped by the fence is restored");
  projector.release("tab-b");
}

// 场景 ③：reanchorEpoch 边界——同代 no-op、空 epoch no-op、清理语义。
{
  const projected: WireEvent[] = [];
  const projector = new TurnEventProjector();
  projector.bind((event) => projected.push(event));
  projector.observeRuntime("tab-c", "epoch-a", 5, 5, false);
  const generationBefore = (projector as unknown as { generationByTab: Map<string, number> }).generationByTab.get("tab-c");
  // 同代 reanchor：no-op（不清游标，不打断正常流）
  projector.reanchorEpoch("tab-c", "epoch-a");
  const generationAfter = (projector as unknown as { generationByTab: Map<string, number> }).generationByTab.get("tab-c");
  assert.equal(generationAfter, generationBefore, "same-epoch reanchor is a no-op (generation unchanged)");
  // 空 epoch：no-op
  projector.reanchorEpoch("tab-c", undefined);
  assert.equal((projector as unknown as { generationByTab: Map<string, number> }).generationByTab.get("tab-c"), generationAfter, "undefined epoch reanchor is a no-op");
  // 翻代 reanchor：游标作废（与 observeRuntime epoch 分支同语义）
  projector.reanchorEpoch("tab-c", "epoch-b");
  const epochByTab = (projector as unknown as { epochByTab: Map<string, string> }).epochByTab.get("tab-c");
  assert.equal(epochByTab, "epoch-b", "epoch flip reanchors the projector epoch");
  assert.equal(
    (projector as unknown as { sequenceByTab: Map<string, number> }).sequenceByTab.has("tab-c"),
    false,
    "epoch flip voids the old epoch's cursor (same teardown as observeRuntime)",
  );
  // 翻代后旧代事件被拒（stale seq 不能污染新游标）
  assert.equal(projector.acceptLive("tab-c", { kind: "text", seq: 1, runtimeEpoch: "epoch-b" }, "epoch-b"), true, "fresh cursor accepts the new epoch's first seq");
  projector.release("tab-c");
}

console.log("tl1653x reasoning-live-after-fence tests passed");
