// Run: tsx src/__tests__/live-turn-duplicate-settlement.test.ts
//
// 任务 645: the live render path must converge on the persisted rebuild when a
// settlement event is re-delivered (turn-event ledger replay after a projector
// cursor rewind). Before the guard, the re-applied `message` event settled a
// freshly appended segment — the round's full text + reasoning rendered twice
// with the turn's stats line splitting the answer; reopening the tab (pure
// history rebuild) rendered correctly.

import { initialState, reducer, historyMessagesToItems, findRedeliveredSettlement } from "../lib/useController";
import { buildTurnModels } from "../lib/transcriptRows";
import type { Item, State } from "../lib/useController";
import type { WireEvent } from "../lib/types";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function ev(e: Record<string, unknown>): { type: "event"; e: WireEvent } {
  return { type: "event", e: { turnId: "t1", ...e } as unknown as WireEvent };
}

const R1 = "思考一：用户确认了英文名，我要更新 644。";
const T1 = "英文名定了，写进 644：";
const R2 = "思考二：644 已更新，回复用户。";
const T2 = "`Mail Center` 已写进 644。\n\n那就等开发线回信。\n\n目前悬置：644 待派工。";

function assistantItems(state: State): Array<Extract<Item, { kind: "assistant" }>> {
  return state.items.filter((it): it is Extract<Item, { kind: "assistant" }> => it.kind === "assistant");
}

function driveCanonicalTurn(): State {
  let s = reducer(initialState, ev({ kind: "turn_status", status: "queued" }));
  s = reducer(s, ev({ kind: "turn_started", status: "in_progress" }));
  s = reducer(s, ev({ kind: "stream_attempt", streamAttempt: { id: "s1", action: "begin", attempt: 1 } }));
  s = reducer(s, ev({ kind: "reasoning", reasoning: R1 }));
  s = reducer(s, ev({ kind: "text", text: T1 }));
  s = reducer(s, ev({ kind: "tool_dispatch", tool: { id: "tc1", name: "tl", args: "", readOnly: false, partial: true } }));
  s = reducer(s, ev({ kind: "message", text: T1, reasoning: R1 }));
  s = reducer(s, ev({ kind: "tool_dispatch", tool: { id: "tc1", name: "tl", args: "update 644", readOnly: false } }));
  s = reducer(s, ev({ kind: "tool_result", tool: { id: "tc1", name: "tl", output: "updated: 任务 644" } }));
  s = reducer(s, ev({ kind: "stream_attempt", streamAttempt: { id: "s2", action: "begin", attempt: 2 } }));
  s = reducer(s, ev({ kind: "reasoning", reasoning: R2 }));
  s = reducer(s, ev({ kind: "text", text: T2 }));
  s = reducer(s, ev({ kind: "message", text: T2, reasoning: R2 }));
  return s;
}

function persistedItems(): Item[] {
  return historyMessagesToItems([
    { role: "user", content: "就按你说的办" },
    { role: "assistant", content: T1, reasoning: R1, toolCalls: [{ id: "tc1", name: "tl", arguments: "update 644" }] },
    { role: "tool", toolCallId: "tc1", toolName: "tl", content: "updated: 任务 644" },
    { role: "assistant", content: T2, reasoning: R2 },
  ] as never, "h0-").items;
}

function segmentShape(items: readonly Item[]): string {
  return buildTurnModels(items)
    .flatMap((model) => model.segments.map((seg) => {
      const tools = seg.displayItems.filter((it) => it.kind === "tool").length;
      const thoughts = seg.displayItems.filter((it) => it.kind === "assistant").length;
      const answers = seg.outsideItems.filter((it) => it.kind === "assistant").length;
      return `${tools}t/${thoughts}r/${answers}a`;
    }))
    .join(" | ");
}

console.log("\nlive turn duplicate settlement convergence (任务 645)");

// ── 1. canonical order: live assembly equals the persisted rebuild ──────────
{
  const s = reducer(driveCanonicalTurn(), ev({ kind: "turn_done", status: "completed" }));
  ok(s.items.length === 3, "canonical turn keeps 2 assistant segments + tool (reducer drive has no user bubble)");
  ok(segmentShape(s.items) === segmentShape(persistedItems()), "canonical live layout equals persisted rebuild");
}

// ── 2. ghost reproduction (pre-fix shape): replayed round-1 deltas + message
//       after round 2 settled must NOT append a duplicate bubble ─────────────
{
  let s = driveCanonicalTurn();
  s = reducer(s, ev({ kind: "reasoning", reasoning: R1 }));
  s = reducer(s, ev({ kind: "text", text: T1 }));
  s = reducer(s, ev({ kind: "message", text: T1, reasoning: R1 }));
  s = reducer(s, ev({ kind: "turn_done", status: "completed" }));
  const assistants = assistantItems(s);
  ok(assistants.length === 2, "replayed settlement converges: still two assistant segments");
  ok(assistants.some((it) => it.text === T1 && it.reasoning === R1), "round-1 content present exactly once");
  ok(assistants.some((it) => it.text === T2 && it.reasoning === R2), "round-2 content preserved");
  ok(segmentShape(s.items) === segmentShape(persistedItems()), "replayed live layout equals persisted rebuild");
}

// ── 3. pure settlement re-delivery (no replayed deltas) is a no-op ──────────
{
  const before = driveCanonicalTurn();
  const after = reducer(before, ev({ kind: "message", text: T1, reasoning: R1 }));
  ok(after.items.length === before.items.length, "pure re-delivery appends nothing");
  ok(after.currentAssistant === undefined, "pure re-delivery leaves no live segment bound");
}

// ── 4. a genuinely new round still appends after the guard ──────────────────
{
  let s = driveCanonicalTurn();
  const R3 = "思考三：追加说明。";
  const T3 = "补充：派工顺序等回板后定。";
  s = reducer(s, ev({ kind: "stream_attempt", streamAttempt: { id: "s3", action: "begin", attempt: 3 } }));
  s = reducer(s, ev({ kind: "reasoning", reasoning: R3 }));
  s = reducer(s, ev({ kind: "text", text: T3 }));
  s = reducer(s, ev({ kind: "message", text: T3, reasoning: R3 }));
  ok(assistantItems(s).length === 3, "new sampling round still allocates its segment");
}

// ── 5. identical content in an EARLIER turn never fires the guard ───────────
{
  const earlier: Item[] = [
    { kind: "user", id: "u0", text: "第一问" },
    { kind: "assistant", id: "a-old", text: T1, reasoning: R1, streaming: false, wasStreamed: true },
    { kind: "user", id: "u1", text: "第二问" },
  ];
  const match = findRedeliveredSettlement(earlier, T1, R1, undefined);
  ok(match === undefined, "settlement scan is bounded to the active turn");
}

// ── 6. streaming segments are never duplicate candidates ────────────────────
{
  const items: Item[] = [
    { kind: "user", id: "u1", text: "问" },
    { kind: "assistant", id: "a-live", text: T1, reasoning: R1, streaming: true, wasStreamed: true },
  ];
  ok(findRedeliveredSettlement(items, T1, R1, undefined) === undefined, "streaming segment is not a re-delivery candidate");
}

// ── 7. replayed round-1 settlement while round 2 is mid-stream converges ────
{
  let s = reducer(initialState, ev({ kind: "turn_status", status: "queued" }));
  s = reducer(s, ev({ kind: "turn_started", status: "in_progress" }));
  s = reducer(s, ev({ kind: "stream_attempt", streamAttempt: { id: "s1", action: "begin", attempt: 1 } }));
  s = reducer(s, ev({ kind: "reasoning", reasoning: R1 }));
  s = reducer(s, ev({ kind: "text", text: T1 }));
  s = reducer(s, ev({ kind: "tool_dispatch", tool: { id: "tc1", name: "tl", args: "", readOnly: false, partial: true } }));
  s = reducer(s, ev({ kind: "message", text: T1, reasoning: R1 }));
  s = reducer(s, ev({ kind: "tool_dispatch", tool: { id: "tc1", name: "tl", args: "update 644", readOnly: false } }));
  s = reducer(s, ev({ kind: "tool_result", tool: { id: "tc1", name: "tl", output: "updated: 任务 644" } }));
  s = reducer(s, ev({ kind: "stream_attempt", streamAttempt: { id: "s2", action: "begin", attempt: 2 } }));
  s = reducer(s, ev({ kind: "reasoning", reasoning: R2 }));
  s = reducer(s, ev({ kind: "text", text: T2 }));
  // replay strikes mid-round-2: round-1 deltas + settlement re-apply, then the
  // real round-2 settlement must still land on exactly one final segment.
  s = reducer(s, ev({ kind: "reasoning", reasoning: R1 }));
  s = reducer(s, ev({ kind: "text", text: T1 }));
  s = reducer(s, ev({ kind: "message", text: T1, reasoning: R1 }));
  s = reducer(s, ev({ kind: "message", text: T2, reasoning: R2 }));
  s = reducer(s, ev({ kind: "turn_done", status: "completed" }));
  const assistants = assistantItems(s);
  ok(assistants.length === 2, "mid-stream replay converges on two assistant segments");
  ok(assistants.some((it) => it.text === T2 && it.reasoning === R2), "round-2 settlement survives the mid-stream replay");
  ok(segmentShape(s.items) === segmentShape(persistedItems()), "mid-stream replay layout equals persisted rebuild");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
