// 任务657: reducer 级 seq 幂等防护的断言钉。任务 645 定过 ghost 段的形：
// turn 事件账本在投影器游标回退后（checkpoint reset 修复）重灌已结算事件，
// reducer 无 seq 防护时同 seq 重放会重复入列（幽灵 assistant 段、live 内容
// 翻倍）。这里按 seq 维度逐条钉死：同 seq 重放一律 no-op（引用相等），
// 新 seq / seq-less 旁路照常入列，reset 点归零、history_rebase 保留。
import { initialState, reducer } from "../lib/useController";
import type { WireEvent } from "../lib/types";

function equal(actual: unknown, expected: unknown, message: string) {
  if (actual !== expected) throw new Error(`${message}: got ${String(actual)}, want ${String(expected)}`);
}

function deepEqual(actual: unknown, expected: unknown, message: string) {
  if (JSON.stringify(actual) !== JSON.stringify(expected)) {
    throw new Error(`${message}:\n  actual   ${JSON.stringify(actual)}\n  expected ${JSON.stringify(expected)}`);
  }
}

const ev = (seq: number, event: Omit<WireEvent, "seq">): { type: "event"; e: WireEvent } => ({ type: "event", e: { ...event, seq } });
const assistantCount = (s: { items: { kind: string }[] }) => s.items.filter((it) => it.kind === "assistant").length;

// ---- 常规序：水位线随每个合法 seq 推进 ----
let s = reducer(initialState, ev(1, { kind: "turn_started", turnId: "turn-1", status: "in_progress" }));
equal(s.appliedEventSeq, 1, "turn_started 入列后水位线推进到 1");
equal(s.currentAssistant, "a:turn-1:0", "首轮 assistant 段照常预创建");

s = reducer(s, { type: "stream_batch", segments: [{ kind: "text", delta: "A", seq: 2 }] });
equal(s.appliedEventSeq, 2, "流内 delta 入列后水位线推进到 2");
equal(s.live?.text, "A", "首个 delta 照常上屏");

// 混合批：重放段（seq 2）+ 新段（seq 3）同帧到达——只应用新段
s = reducer(s, { type: "stream_batch", segments: [{ kind: "text", delta: "A", seq: 2 }, { kind: "text", delta: "B", seq: 3 }] });
equal(s.live?.text, "AB", "混合批丢弃重放段、应用新段（不出现 AAB/ABB）");
equal(s.appliedEventSeq, 3, "混合批后水位线推进到新段 seq");

// 结算（seq 4）
s = reducer(s, ev(4, { kind: "message", turnId: "turn-1", text: "AB", reasoning: "" }));
equal(assistantCount(s), 1, "一轮只结算出一个 assistant 段");
equal(s.appliedEventSeq, 4, "结算后水位线推进到 4");
const settledItems = s.items;

// ---- 纯重放：同 seq 逐类回灌，全部 no-op（引用相等）----
const replayStart = reducer(s, ev(1, { kind: "turn_started", turnId: "turn-1", status: "in_progress" }));
equal(replayStart, s, "同 seq turn_started 重放为 no-op（不重建 assistant 段）");
const replayDelta = reducer(s, { type: "stream_batch", segments: [{ kind: "text", delta: "A", seq: 2 }] });
equal(replayDelta, s, "同 seq 纯重放批为 no-op（live 内容不翻倍）");
const replaySettle = reducer(s, ev(4, { kind: "message", turnId: "turn-1", text: "AB", reasoning: "" }));
equal(replaySettle, s, "同 seq message 重放为 no-op——645 幽灵段按 seq 钉死");
deepEqual(replaySettle.items, settledItems, "重放后 items 逐字节不变");
equal(assistantCount(replaySettle), 1, "重放后仍只有一个 assistant 段");

// 乱序旧 seq（水位线 4 之后来 seq 2）同样丢弃
const staleOlder = reducer(s, ev(2, { kind: "turn_started", turnId: "turn-1", status: "in_progress" }));
equal(staleOlder, s, "低于水位线的乱序旧事件为 no-op");

// ---- seq-less 事件旁路不受水位线拦截 ----
const withNotice = reducer(s, { type: "event", e: { kind: "notice", text: "bypass" } });
equal(withNotice.items.length, s.items.length + 1, "seq-less 事件照常入列");
equal(withNotice.appliedEventSeq, s.appliedEventSeq, "seq-less 事件不推进水位线");

// ---- 新一轮：更高 seq 照常入列 ----
s = reducer(s, ev(10, { kind: "turn_started", turnId: "turn-2", status: "in_progress" }));
equal(s.appliedEventSeq, 10, "新轮更高 seq 照常入列");
s = reducer(s, { type: "stream_batch", segments: [{ kind: "reasoning", delta: "想", seq: 11 }] });
equal(s.live?.reasoning, "想", "新轮 delta 照常上屏");
s = reducer(s, ev(12, { kind: "message", turnId: "turn-2", text: "C", reasoning: "想" }));
equal(assistantCount(s), 2, "两轮各结算一个 assistant 段");
equal(s.appliedEventSeq, 12, "水位线推进到 12");

// ---- history_rebase 保留水位线：rebase 保留 live tail，重放是重复不是重建 ----
s = reducer(initialState, ev(1, { kind: "turn_started", turnId: "turn-3", status: "in_progress" }));
s = reducer(s, { type: "stream_batch", segments: [{ kind: "reasoning", delta: "想", seq: 2 }] });
const rebased = reducer(s, {
  type: "history_rebase",
  items: [{ kind: "user", id: "h1", text: "问" }],
  startTurn: 1,
  totalTurns: 1,
  hasOlder: false,
});
equal(rebased.appliedEventSeq, 2, "history_rebase 保留水位线（live tail 被保留，重放即重复）");
const rebaseReplay = reducer(rebased, { type: "stream_batch", segments: [{ kind: "reasoning", delta: "想", seq: 2 }] });
equal(rebaseReplay, rebased, "rebase 后同 seq 重放为 no-op");
equal(rebaseReplay.live?.reasoning, "想", "rebase 后重放不把 live 内容再灌一遍（不想出『想想』）");

// ---- reset 点归零 ----
const rebuilt = reducer(s, { type: "controller_rebuilt" });
equal(rebuilt.appliedEventSeq, 0, "controller_rebuilt 归零水位线（新 epoch seq 从 1 重启）");
const freshEpoch = reducer(rebuilt, ev(1, { kind: "turn_started", turnId: "turn-new", status: "in_progress" }));
equal(freshEpoch.appliedEventSeq, 1, "新 epoch 的 seq=1 不被旧水位线吞掉");
equal(freshEpoch.running, true, "新 epoch 事件照常驱动渲染状态");

const resetState = reducer(s, { type: "reset" });
equal(resetState.appliedEventSeq, 0, "reset（切换会话）归零水位线");

console.log("turn event seq idempotency tests passed");
