// Run: tsx src/__tests__/tl1444-overlimit-stream-fence.test.ts
//
// tl1444：超限态发消息输出不渲染——工作中但模型输出不显示，关 tab 重开
// （触发 hydrate+自动瘦身）才恢复。日志定案：projection-legacy + strip_images
// + fold 同刻发生（2026-11-06 collab 会话实录：repairlog 三次 strip_images+fold、
// 恢复晋升/代际盖章翻动、runtime 投影读 legacy）。
//
// 断点定位：handleWireEvent 的 epoch/sessionGeneration 两道 fence 对
// turn 内容事件（text/reasoning/tool/turn 生命周期）是纯静默丢弃——
// 469 只给 prompt 卡片接了对账。超限会话恰在 turn 进行中翻动身份
// （投影重建/自动瘦身 → CAS fork/恢复晋升 → 代际盖章），fence 把整轮
// 流式增量吞进无底洞：轮次照跑（runtime 投影是另一条通道）、输出一行
// 不显示，直到关 tab 重开。
//
// 修复：被 fence 丢弃的事件（不分种类）触发同一次去抖权威对账
// （refreshMetaOnlyForTab 重挂 epoch 锚与新代）。丢弃规则逐字不变
// （内容不跨代泄漏）；对账落地前被吞的增量从未推进 projector 游标，
// 对账后下一条放行事件必然暴露 seq 缺口 → 既有 durable replay 回填——
// 流式接续不断，无需重开。
//
// 本件钉三段：
//   ① fence 判定：内容事件丢弃 → reconcile（修复前红/修复后绿）；
//     fence 放行规则逐字不变（469 契约保留）。
//   ② 回填链路：fence 吞掉的增量在对账后经 projector seq-gap 权威重放
//     逐条回填（修复所依赖的既有机制，哨兵钉防回退）。
//   ③ 1347 哨兵扩展：超限态投影重建场景——live turn 流式中途落一页
//     重建投影（history_rebase，diet 折叠后的转录形态），live tail 存活
//     且后续增量继续渲染、轮终 settle 不丢已流文本。

import assert from "node:assert/strict";
import type { AppBindings } from "../lib/bridge";
import type { TurnEventEnvelope, TurnEventReplayView, WireEvent } from "../lib/types";

// ---- ① fence 判定：内容事件丢弃必须触发对账（tl1444 核心，修复前红） ----

const { judgePromptFenceArrival } = await import("../lib/askPanelGate");

type FenceVerdict = ReturnType<typeof judgePromptFenceArrival>;

function expectDrop(verdict: FenceVerdict, reason: "epoch-fence" | "generation-fence", label: string) {
  if (verdict.action !== "drop") {
    throw new Error(`${label}: expected drop, got admit`);
  }
  assert.equal(verdict.reason, reason, `${label} 丢因`);
  assert.equal(verdict.reconcile, true, `${label} 必须触发对账（tl1444：纯丢弃=整轮黑屏）`);
}

// turn 内容事件（text）被代际围栏吞掉 → 丢 + 对账。
expectDrop(
  judgePromptFenceArrival({ promptEvent: false, localGeneration: 2, eventGeneration: 3 }),
  "generation-fence", "text 事件代不一致：丢 + 对账",
);
// 本地 meta 缺代（surface 身份未落地）→ 同样丢 + 对账。
expectDrop(
  judgePromptFenceArrival({ promptEvent: false, eventGeneration: 3 }),
  "generation-fence", "text 事件本地缺代：丢 + 对账",
);
// turn 内容事件被 epoch 围栏吞掉 → 丢 + 对账。
expectDrop(
  judgePromptFenceArrival({ promptEvent: false, acceptedEpoch: "epoch-old", eventEpoch: "epoch-new" }),
  "epoch-fence", "text 事件 epoch 过时：丢 + 对账",
);
// turn 生命周期事件同等对待（turn_done 被吞=永久转不出 running）。
expectDrop(
  judgePromptFenceArrival({ promptEvent: false, localGeneration: 1, eventGeneration: 2 }),
  "generation-fence", "turn 生命周期事件：丢 + 对账",
);

// fence 放行规则逐字不变（469 契约：只增不改，跨代内容泄漏防护原样保留）。
assert.equal(judgePromptFenceArrival({ promptEvent: false, localGeneration: 3, eventGeneration: 3 }).action, "admit", "代一致放行");
assert.equal(judgePromptFenceArrival({ promptEvent: false, localGeneration: 3 }).action, "admit", "事件不带代（代 0 omitted）放行");
assert.equal(judgePromptFenceArrival({ promptEvent: false, acceptedEpoch: undefined, eventEpoch: "e" }).action, "admit", "本地无 epoch 锚放行（首次采纳）");
assert.equal(judgePromptFenceArrival({ promptEvent: false, acceptedEpoch: "e", eventEpoch: undefined }).action, "admit", "旧后端无 epoch 放行");
assert.equal(judgePromptFenceArrival({ promptEvent: true, localGeneration: 3, eventGeneration: 3 }).action, "admit", "prompt 事件代一致放行");

// ---- ② 回填链路：fence 吞掉的增量对账后经权威重放逐条回填 ----

const binding: Partial<AppBindings> = {};
Object.defineProperty(globalThis, "window", {
  configurable: true,
  value: { go: { main: { App: binding as AppBindings } } } as Window,
});

const { TurnEventProjector } = await import("../lib/turnEventProjection");

// 现场：游标 3（事件 1-3 已投影）。fence 回收期把 4/5/6 整批吞掉（对账
// 落地前）——被吞事件从未到达 projector，游标停在 3。对账后事件 7 放行：
// 7 > 3+1 暴露缺口 → TurnEventsForTab(tab, 3) 权威重放 → 4/5/6/7 逐条
// 回填 handler，流式接续不断。
{
  const seen: number[] = [];
  binding.TurnEventsForTab = async (_tabId: string, afterSeq: number): Promise<TurnEventReplayView> => {
    const envelopes: TurnEventEnvelope[] = [4, 5, 6, 7].map((seq) => ({
      seq,
      turnId: "t1",
      createdAt: 0,
      event: { kind: seq === 7 ? "turn_done" : "text", text: `回填增量#${seq}` } } as unknown as TurnEventEnvelope),
    );
    return {
      events: envelopes.filter((envelope) => envelope.seq > afterSeq),
      floorSeq: 0,
      latestSeq: 7,
      nextAfterSeq: 7,
      hasMore: false,
      resetRequired: false,
      runtimeEpoch: "epoch-1",
    };
  };
  const projector = new TurnEventProjector();
  const delivered: WireEvent[] = [];
  projector.bind((event) => { delivered.push(event); seen.push(event.seq ?? -1); });
  projector.observeRuntime("tab", "epoch-1", 3, 3, false);
  // fence 回收期：4/5/6 在 handleWireEvent 的 fence 处被丢（不调 acceptLive）。
  // 断言游标确实没被动过——对账后事件 7 必须暴露缺口而不是静默放行。
  assert.equal(projector.acceptLive("tab", { kind: "turn_done", seq: 7, turnId: "t1" } as WireEvent, "epoch-1"), false, "缺口处的放行事件必须进 gap 修复（回填入口）");
  for (let i = 0; i < 100 && seen.length < 4; i += 1) {
    await new Promise((r) => setTimeout(r, 10));
  }
  assert.deepEqual(seen, [4, 5, 6, 7], `被 fence 吞掉的增量必须经权威重放回填，got ${JSON.stringify(seen)}`);
  assert.equal(delivered[0]?.text, "回填增量#4", "回填首条是被吞的第 4 条增量");
}

// 对账后连续增量恢复正常直通（缺口已闭合，流式接续不断；acceptLive 放行
// 后由调用方派发，projector 自身不回调 handler——与 handleWireEvent 同构）。
{
  binding.TurnEventsForTab = async () => {
    throw new Error("must not be called after the gap closed");
  };
  const projector = new TurnEventProjector();
  projector.observeRuntime("tab2", "epoch-1", 0, 0, false);
  assert.equal(projector.acceptLive("tab2", { kind: "text", seq: 1, text: "a" } as WireEvent, "epoch-1"), true);
  assert.equal(projector.acceptLive("tab2", { kind: "text", seq: 2, text: "b" } as WireEvent, "epoch-1"), true);
  assert.equal(projector.acceptLive("tab2", { kind: "text", seq: 3, text: "c" } as WireEvent, "epoch-1"), true);
}

// ---- ③ 1347 哨兵扩展：超限态投影重建场景（rebase 中断流接续钉） ----

const { initialState, reducer } = await import("../lib/useController");

type S = ReturnType<typeof reducer>;

function liveTurnOnHistory(): S {
  // 超限会话形态：已加载一页重建投影历史（diet 折叠占位 + 新用户消息）。
  let s = reducer(initialState, {
    type: "history_replace",
    items: [
      { kind: "notice", id: "h-fold", level: "info", text: "[本地折叠] 已省略 N 条早期消息" },
      { kind: "user", id: "h-u1", text: "超限态下的新消息", createdAt: 1 },
    ],
    startTurn: 1,
    totalTurns: 2,
    hasOlder: false,
    revision: 100,
  });
  return reducer(s, { type: "event", e: { kind: "turn_started", turnId: "t1", status: "in_progress" } });
}

function streamedText(s: S): string {
  // 流式阶段的增量累积在 s.live（渲染层把 live 合并进流式气泡）；item.text
  // 在 settle 时才提交。可见性断言读两处并集。
  const settled = s.items.filter((it) => it.kind === "assistant").map((it) => (it as { text?: string }).text ?? "").join("");
  return `${settled}${s.live?.text ?? ""}`;
}

// 投影重建页面在流式中途落地（rebase）：live tail 存活，已流文本不丢。
{
  let s = liveTurnOnHistory();
  s = reducer(s, { type: "stream_batch", segments: [{ kind: "text", delta: "rebase 前已流出的部分" }] });
  assert.ok(streamedText(s).includes("rebase 前已流出的部分"), "rebase 前已流文本可见");
  // diet 折叠后的新投影页：折叠占位 + 同一条用户消息，不含 live 气泡。
  s = reducer(s, {
    type: "history_rebase",
    items: [
      { kind: "notice", id: "h-fold", level: "info", text: "[本地折叠] 已省略 N 条早期消息" },
      { kind: "user", id: "h-u1", text: "超限态下的新消息", createdAt: 1 },
    ],
    startTurn: 1,
    totalTurns: 2,
    hasOlder: false,
    revision: 101,
  });
  assert.ok(streamedText(s).includes("rebase 前已流出的部分"), `投影重建页面落地后已流文本必须存活，got items=${JSON.stringify(s.items.map((it) => ({ kind: it.kind, id: (it as { id?: string }).id })))}`);
  // 流式接续：rebase 后的增量继续渲染（流式不断）。
  s = reducer(s, { type: "stream_batch", segments: [{ kind: "text", delta: "；rebase 后接续的增量" }] });
  const text = streamedText(s);
  assert.ok(text.includes("rebase 前已流出的部分") && text.includes("；rebase 后接续的增量"), `rebase 后增量必须接续渲染，got ${JSON.stringify(text)}`);
  // 轮终 settle 不丢已流文本（1347 哨兵本体在重建场景的延伸）。
  s = reducer(s, { type: "event", e: { kind: "turn_done", turnId: "t1", status: "completed" } });
  assert.ok(streamedText(s).includes("；rebase 后接续的增量"), "turn_done 后已流文本不丢");
}

// 多段 assistant（工具轮次分段）在 rebase 后同样存活。
{
  let s = liveTurnOnHistory();
  s = reducer(s, { type: "stream_batch", segments: [{ kind: "text", delta: "第一段" }] });
  s = reducer(s, { type: "event", e: { kind: "message" } });
  s = reducer(s, { type: "stream_batch", segments: [{ kind: "text", delta: "第二段" }] });
  s = reducer(s, {
    type: "history_rebase",
    items: [
      { kind: "notice", id: "h-fold", level: "info", text: "[本地折叠] 已省略 N 条早期消息" },
      { kind: "user", id: "h-u1", text: "超限态下的新消息", createdAt: 1 },
    ],
    startTurn: 1,
    totalTurns: 2,
    hasOlder: false,
    revision: 102,
  });
  const text = streamedText(s);
  assert.ok(text.includes("第一段") && text.includes("第二段"), `多段流文本 rebase 后必须整组存活，got ${JSON.stringify(text)}`);
}

process.stdout.write("tl1444-overlimit-stream-fence: all assertions passed\n");
