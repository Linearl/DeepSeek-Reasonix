// tl1347 二轮: an interrupted turn's already-generated assistant text must
// stay visible in the live transcript projection together with the
// interrupted notice — the user-facing shape was "sent a message, got no
// reply at all" when the turn died mid-stream. Pins the live (reducer) half;
// the reload (history projection) half is pinned in desktop Go tests.
//
// Run: tsx src/__tests__/tl1347-interrupted-partial-text-live.test.ts

import assert from "node:assert/strict";

import { initialState, reducer } from "../lib/useController";

type S = ReturnType<typeof reducer>;

function liveTurn(): S {
  return reducer(initialState, { type: "event", e: { kind: "turn_started", turnId: "t1", status: "in_progress" } });
}

// Interrupted after partial text streamed: text stays + interrupted notice.
{
  let s = liveTurn();
  s = reducer(s, { type: "event", e: { kind: "text", text: "中断前已生成的部分回答" } });
  s = reducer(s, { type: "event", e: { kind: "turn_done", turnId: "t1", status: "interrupted" } });
  const assistant = s.items.filter((it) => it.kind === "assistant");
  const text = assistant.map((it) => (it as { text?: string }).text ?? "").join("");
  assert.ok(text.includes("中断前已生成的部分回答"), `partial text must stay visible after an interrupted turn_done, got items=${JSON.stringify(s.items.map((it) => ({ kind: it.kind, text: (it as { text?: string }).text })))}`);
  assert.ok(s.items.some((it) => it.kind === "notice"), "interrupted guidance notice must be present");
}

// Same shape via the stream_batch path (the real streaming transport).
{
  let s = liveTurn();
  s = reducer(s, { type: "stream_batch", segments: [{ kind: "text", delta: "经 stream_batch 到达的部分文本" }] });
  s = reducer(s, { type: "event", e: { kind: "turn_done", turnId: "t1", status: "interrupted" } });
  const text = s.items.filter((it) => it.kind === "assistant").map((it) => (it as { text?: string }).text ?? "").join("");
  assert.ok(text.includes("经 stream_batch 到达的部分文本"), `stream_batch text must survive an interrupted turn_done, got ${JSON.stringify(text)}`);
}

// Interrupted before any output: no assistant rows, but the guidance notice
// still explains the stop (no silent black hole).
{
  const s = reducer(liveTurn(), { type: "event", e: { kind: "turn_done", turnId: "t1", status: "interrupted" } });
  assert.ok(!s.items.some((it) => it.kind === "assistant"), "no empty assistant shell after a zero-output interruption");
  assert.ok(s.items.some((it) => it.kind === "notice"), "interrupted guidance still present with zero output");
}

// Reasoning-only interruption: reasoning text stays visible too.
{
  let s = liveTurn();
  s = reducer(s, { type: "event", e: { kind: "reasoning", reasoning: "中断前的推理片段" } });
  s = reducer(s, { type: "event", e: { kind: "turn_done", turnId: "t1", status: "interrupted" } });
  const reasoning = s.items.filter((it) => it.kind === "assistant").map((it) => (it as { reasoning?: string }).reasoning ?? "").join("");
  assert.ok(reasoning.includes("中断前的推理片段"), `partial reasoning must stay visible, got ${JSON.stringify(reasoning)}`);
}

// message settlement after streamed deltas (the stream's closing envelope):
// the settled text stays through an interrupted turn_done, and an empty
// closing envelope must not delete a bubble that carries streamed text.
{
  let s = liveTurn();
  s = reducer(s, { type: "event", e: { kind: "text", text: "流式阶段的部分文本" } });
  s = reducer(s, { type: "event", e: { kind: "message" } });
  const settledText = s.items.filter((it) => it.kind === "assistant").map((it) => (it as { text?: string }).text ?? "").join("");
  assert.ok(settledText.includes("流式阶段的部分文本"), `message settlement must keep streamed text, got ${JSON.stringify(settledText)}`);
  s = reducer(s, { type: "event", e: { kind: "turn_done", turnId: "t1", status: "interrupted" } });
  const finalText = s.items.filter((it) => it.kind === "assistant").map((it) => (it as { text?: string }).text ?? "").join("");
  assert.ok(finalText.includes("流式阶段的部分文本"), `settled text must survive an interrupted turn_done, got ${JSON.stringify(finalText)}`);
}

process.stdout.write("tl1347-interrupted-partial-text-live: all assertions passed\n");
