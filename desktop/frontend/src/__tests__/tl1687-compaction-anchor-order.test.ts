// tl1687 验收：压缩卡是历史事件锚点——其后的思考/回复渲染在其下方。
//
// 现场（20261118 08:50）：压缩 26s 完成后，新一轮的『1 段思考』块渲染在
// 『上下文已压缩』卡片上面——时间顺序反了。根因：turn_started 预建的空
// assistant 气泡先落 items（游标锚点），随后的 compaction_started 走尾部
// append，卡片排到气泡之后——思考内容（压缩完成后才流出）渲染在卡上方。
// 修复：新卡在「当前回合的 assistant 气泡仍为空」时插到气泡之前；已在流
// 思考的 assistant（内容非空）保持尾部 append（其思考确实先于压缩开始）。
//
// 钉四件事：
// ① 压缩→新思考场景：空气泡时卡片插在 assistant 之前（渲染顺序 = 压缩卡
//    在上、思考在下）；
// ② 思考先于压缩（mid-turn 压力压缩）：assistant 已有 reasoning 内容时
//    卡片仍在尾部（时间序保持：思考 → 压缩卡）；
// ③ 无回合上下文（独立压缩）：卡片尾部 append，逐字节不变；
// ④ 渲染链零回归：items 数组序经 buildTurnModels/partitionTurnItems 保持
//    （压缩卡先于思考 segment），不复活已删的样式A。

import assert from "node:assert/strict";
import { reducer, initialState, type State, type Item } from "../lib/useController";
import { buildTurnModels } from "../lib/transcriptRows";
import type { WireEvent } from "../lib/types";

let passed = 0;
const ok = (cond: boolean, msg: string) => {
  if (!cond) {
    console.error(`FAIL: ${msg}`);
    process.exitCode = 1;
  } else {
    passed += 1;
  }
};

const ev = (e: unknown) => ({ type: "event", e } as unknown as Parameters<typeof reducer>[1]);
const started = (pass?: number): WireEvent =>
  ({ kind: "compaction_started", compaction: { trigger: "auto", pass } } as unknown as WireEvent);

const indices = (s: State) => {
  const compaction = s.items.findIndex((it) => it.kind === "compaction");
  const assistant = s.items.findIndex((it) => it.kind === "assistant");
  return { compaction, assistant };
};

// ── ① 压缩→新思考：空气泡时卡片插在 assistant 之前 ──────────────────────────
{
  process.stdout.write("case 1: empty pre-created bubble — card anchors above it\n");
  let s: State = initialState;
  s = reducer(s, ev({ kind: "turn_started" }));
  const { assistant } = indices(s);
  ok(assistant >= 0, "turn_started pre-creates the assistant bubble");
  s = reducer(s, ev(started(1)));
  const idx = indices(s);
  ok(idx.compaction >= 0, "pending card built");
  ok(idx.compaction < idx.assistant, `card (${idx.compaction}) must render above the empty assistant (${idx.assistant})`);
  const card = s.items[idx.compaction] as Extract<Item, { kind: "compaction" }>;
  ok(card.pending === true, "card stays pending");
  ok(card.pass === 1, "pass number carried");
}

// ── ② 思考先于压缩：已流 reasoning 的 assistant 保持尾部序 ────────────────────
{
  process.stdout.write("case 2: assistant already thinking — card appends below (temporal order kept)\n");
  let s: State = initialState;
  s = reducer(s, ev({ kind: "turn_started" }));
  s = reducer(s, ev({ kind: "reasoning", text: "thinking began before compaction" }));
  s = reducer(s, ev(started(1)));
  const idx = indices(s);
  ok(idx.compaction > idx.assistant, `streaming assistant (${idx.assistant}) keeps the card below (${idx.compaction})`);
}

// ── ③ 无回合上下文：独立压缩尾部 append（零回归） ─────────────────────────────
{
  process.stdout.write("case 3: standalone compaction — tail append unchanged\n");
  const s = reducer(initialState, ev(started(1)));
  const last = s.items[s.items.length - 1] as Extract<Item, { kind: "compaction" }>;
  ok(s.items.length === 1 && last?.kind === "compaction", "no assistant context: card appended at the tail");
}

// ── ③b user 行在前：卡片插在 user 与空气泡之间（回合块内锚点） ────────────────
{
  process.stdout.write("case 3b: user row stays above the anchored card\n");
  let s: State = initialState;
  s = reducer(s, { type: "user", text: "下一个问题", seq: 0, submissionId: "sub1" } as unknown as Parameters<typeof reducer>[1]);
  s = reducer(s, ev({ kind: "turn_started" }));
  s = reducer(s, ev(started(1)));
  const user = s.items.findIndex((it) => it.kind === "user");
  const idx = indices(s);
  ok(user >= 0 && user < idx.compaction && idx.compaction < idx.assistant, `user(${user}) < card(${idx.compaction}) < assistant(${idx.assistant})`);
}

// ── ④ 渲染链：buildTurnModels 保持 items 序（卡先于思考段） ───────────────────
{
  process.stdout.write("case 4: buildTurnModels preserves the anchored order\n");
  const items: Item[] = [
    { kind: "user", id: "u1", text: "q" },
    { kind: "compaction", id: "c1", pending: false, trigger: "auto", messages: 3, summary: "- s", archive: "" },
    { kind: "assistant", id: "a1", text: "answer", reasoning: "thought", streaming: false },
  ];
  const models = buildTurnModels(items);
  const seg0 = models[0]?.segments[0];
  const kinds = (seg0?.processItems ?? []).map((it) => it.kind);
  const compactionAt = kinds.indexOf("compaction");
  const reasoningAt = kinds.findIndex((k) => k === "assistant");
  ok(compactionAt >= 0, "compaction card is process material in the fold");
  ok(reasoningAt < 0 || compactionAt < reasoningAt, `card (${compactionAt}) precedes the reasoning leg (${reasoningAt})`);
}

process.stdout.write(`\ntl1687 compaction anchor order: ${passed} asserts passed\n`);
if (process.exitCode) process.exit(1);
assert.ok(passed >= 8, `expected >=8 asserts, got ${passed}`);
