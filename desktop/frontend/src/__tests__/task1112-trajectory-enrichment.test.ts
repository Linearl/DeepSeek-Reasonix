// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/task1112-trajectory-enrichment.test.ts
// 任务 1112: trajectory view enrichment (three-lane gantt, filters, readable
// summaries, search). The timeline projection must map every kind to its lane
// (user→input/assistant→model/tool→tool, everything else degrades to a
// marker-only system record), and the sequence timeline must build fully from
// record order alone (the 1111 finding: equal-width mode needs no time
// anchors, so history sessions render completely). The search index must
// AND-match across turn numbers/kind labels/title/preview, and the row
// projection must collapse turns/calls into summary rows while search-match
// filtering drives both dimming and collapse-guard behavior.
import assert from "node:assert";
import process from "node:process";
import { buildTrajectoryLedger, type TrajectoryRecord } from "../lib/trajectoryLedger";
import { buildTrajectorySequenceTimeline, buildTrajectoryTimeline, laneOf } from "../lib/trajectoryTimeline";
import { buildTrajectorySearchIndex, trajectorySearchMatches } from "../lib/trajectorySearchIndex";
import { projectTrajectoryRows } from "../components/trajectoryViewBody";
import type { Item } from "../lib/useController";

let passed = 0;
let failed = 0;
function ok(cond: boolean, label: string) {
  if (cond) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; process.exitCode = 1; }
}

function user(id: string, text: string, extra: Partial<Extract<Item, { kind: "user" }>> = {}): Item {
  return { kind: "user", id, text, ...extra };
}
function assistant(id: string, extra: Partial<Extract<Item, { kind: "assistant" }>> = {}): Item {
  return { kind: "assistant", id, text: extra.text ?? `answer-${id}`, reasoning: "", streaming: false, ...extra };
}
function tool(id: string, extra: Partial<Extract<Item, { kind: "tool" }>> = {}): Item {
  return { kind: "tool", id, name: extra.name ?? "bash", args: "", readOnly: false, status: "done", ...extra };
}

console.log("\n任务 1112 trajectory enrichment: lanes, filters, summaries, search");

// ① Lane mapping: user→0 输入, assistant→1 模型, tool→2 工具, system kinds null.
{
  ok(laneOf("user") === 0 && laneOf("assistant") === 1 && laneOf("tool") === 2, "user/assistant/tool map to lanes 0/1/2");
  ok(laneOf("compaction") === null && laneOf("notice") === null && laneOf("phase") === null, "system kinds stay out of the three lanes");
}

// ② Three-lane gantt on a real-time domain: spans carry lane + turn boundaries.
{
  const items: Item[] = [
    user("u1", "hi", { createdAt: 1000 }),
    assistant("a1", { createdAt: 1500, workDurationMs: 400 }),
    tool("t1", { startedAt: 2000, durationMs: 100 }),
    user("u2", "again", { createdAt: 3000 }),
    assistant("a2", { createdAt: 3500, workDurationMs: 200 }),
  ];
  const ledger = buildTrajectoryLedger(items);
  const model = buildTrajectoryTimeline(ledger.records)!;
  ok(model.spans.length === 3 && model.markers.length === 2, "duration records are spans; the duration-less users are markers");
  ok([...model.spans, ...model.markers].every((span) => span.lane !== null), "every projected record carries a lane");
  const spans = new Map(model.spans.map((span) => [span.recordId, span]));
  const markers = new Map(model.markers.map((marker) => [marker.recordId, marker]));
  ok(markers.get("u1")?.lane === 0 && spans.get("a1")?.lane === 1 && spans.get("t1")?.lane === 2,
    "user→lane 0, assistant→lane 1, tool→lane 2 (three-lane coloring)");
  ok(model.turnBoundaries.length === 2, "two turn boundaries projected");
  ok(model.turnBoundaries[0].turn === 1 && model.turnBoundaries[0].turnLabel === undefined, "turn 1 boundary");
  ok(model.turnBoundaries[1].turn === 2, "turn 2 boundary");
}

// ③ Sequence timeline: builds fully from record order with no time anchors
//    (the 1111 finding — history sessions render completely).
{
  const items: Item[] = [
    user("u1", "hi"), // no createdAt
    assistant("a1"), // no createdAt/workDurationMs
    tool("t1"), // no startedAt
  ];
  const ledger = buildTrajectoryLedger(items);
  ok(buildTrajectoryTimeline(ledger.records) === null, "real-time projection is null without anchors");
  const seq = buildTrajectorySequenceTimeline(ledger.records, "sequence");
  ok(seq.spans.length === 3, "sequence projection carries every record (no anchors needed)");
  ok(seq.spans[0].start === 0 && seq.spans[1].start === 1 && seq.spans[2].start === 2, "sequence spans are equal-width sequential units");
  ok(seq.spans[0].width === 1 && seq.spans[2].width === 1, "sequence spans are 1 unit wide");
  ok(seq.turnBoundaries.length === 1 && seq.turnBoundaries[0].start === 0, "sequence turn boundary at the first record");
}

// ④ Duration mode: real durations take proportional width, missing durations
//    are 0-width point markers (1131: no fake 1-unit filler in the ms domain),
//    no idle gaps (compressIdle semantics).
{
  const items: Item[] = [
    user("u1", "hi", { createdAt: 1000 }),
    assistant("a1", { createdAt: 1500, workDurationMs: 1000 }),
    tool("t1", { startedAt: 9000, durationMs: 500 }), // 7500ms idle gap before it
    tool("t2"), // no duration at all
  ];
  const ledger = buildTrajectoryLedger(items);
  const dur = buildTrajectorySequenceTimeline(ledger.records, "duration");
  const spans = new Map(dur.spans.map((span) => [span.recordId, span]));
  const markers = new Map(dur.markers.map((marker) => [marker.recordId, marker]));
  ok(dur.spans.length === 2 && markers.size === 2, "duration spans (a1/t1) + markers for the duration-less u1/t2");
  const total = dur.t1 - dur.t0;
  ok(total === 1500, "duration domain = raw ms total only (1000 + 500; no per-marker filler unit)");
  ok(dur.unitScale != null && Math.abs(dur.unitScale - 1 / total) < 1e-12, "unitScale = 1 序位 / 归一化域总长");
  ok(spans.get("a1")!.width === 1000 && spans.get("t1")!.width === 500, "span widths are the raw ms proportions");
  ok(markers.has("t2") && markers.get("t2")!.width === 0, "no-duration record is a 0-width point marker, not a span");
  ok(Math.abs(spans.get("t1")!.start - (spans.get("a1")!.start + spans.get("a1")!.width)) < 1e-9,
    "idle time is compressed out (no gap between records)");
}

// ⑤ Search index: AND matching over turn numbers/kind labels/title/preview.
{
  const items: Item[] = [
    user("u1", "fix the parser bug", { createdAt: 1000 }),
    assistant("a1", { text: "I will inspect the parser module" }),
    tool("t1", { name: "grep", args: "", summary: "found 3 matches in parser.ts" }),
    user("u2", "thanks", { createdAt: 2000 }),
    assistant("a2", { text: "done" }),
  ];
  const ledger = buildTrajectoryLedger(items);
  const index = buildTrajectorySearchIndex(ledger.records);
  ok(index.length === 5, "index covers every record");
  const parser = trajectorySearchMatches(index, "parser");
  ok(parser != null && parser.size === 3 && parser.has("u1") && parser.has("a1") && parser.has("t1"),
    "single term hits the records carrying it (user preview + assistant text + tool summary)");
  const both = trajectorySearchMatches(index, "parser grep");
  ok(both != null && both.size === 1 && both.has("t1"), "AND matching: two terms narrow to one record");
  const none = trajectorySearchMatches(index, "parser zzz");
  ok(none != null && none.size === 0, "no match yields an empty set (not null)");
  ok(trajectorySearchMatches(index, "   ") === null, "blank query = no filtering");
  const turnScoped = trajectorySearchMatches(index, "turn2");
  ok(turnScoped != null && turnScoped.has("u2") && turnScoped.has("a2"), "turn numbers are searchable");
}

// ⑥ Row projection: turns/calls collapse into summary rows; search-match
//    filtering drives both dimming and the collapse guard.
{
  const items: Item[] = [
    user("u1", "hi", { createdAt: 1000, historyTurn: 7 }),
    assistant("a1"),
    tool("t1", { name: "grep" }),
    tool("t2", { name: "edit" }),
    assistant("a2"),
    user("u2", "again", { createdAt: 2000 }),
    assistant("a3"),
  ];
  const ledger = buildTrajectoryLedger(items);
  const rows = projectTrajectoryRows(ledger.records, {
    equalWidth: true, collapsedTurns: true, collapsedCalls: false, searchMatchIds: null, focusIds: null,
  });
  ok(rows.length === 4, "collapsedTurns keeps each turn's user row + one summary row");
  ok(rows[0].type === "record" && rows[1].type === "turn" && rows[2].type === "record" && rows[3].type === "turn",
    "turn summary rows sit right after their turn's user row");
  if (rows[1].type === "turn") {
    ok(rows[1].turn === 1 && rows[1].turnLabel === 7 && rows[1].steps === 2 && rows[1].tools === 2,
      "turn summary counts steps + tool calls and carries the historyTurn label");
  }
  const callRows = projectTrajectoryRows(ledger.records, {
    equalWidth: true, collapsedTurns: false, collapsedCalls: true, searchMatchIds: null, focusIds: null,
  });
  const callsRow = callRows.find((row) => row.type === "calls");
  ok(callRows.length === 5 && callsRow != null, "collapsedCalls folds a1's trailing tool calls into one row");
  if (callsRow?.type === "calls") {
    ok(callsRow.calls === 2 && callsRow.names === "grep · edit", "calls summary carries count + tool names");
  }
  const onlyT1 = projectTrajectoryRows(ledger.records, {
    equalWidth: true, collapsedTurns: true, collapsedCalls: true,
    searchMatchIds: new Set(["t1"]), focusIds: null,
  });
  ok(onlyT1.length === 1 && onlyT1[0].type === "record", "search match guards the collapse: the hit row renders alone, unfolded");
}

// ⑦ Failed records keep the red override on every lane (error覆盖一切).
{
  const items: Item[] = [
    user("u1", "hi", { createdAt: 1000 }),
    tool("t1", { status: "error", startedAt: 1500, durationMs: 50 }),
  ];
  const ledger = buildTrajectoryLedger(items);
  const model = buildTrajectoryTimeline(ledger.records)!;
  const span = model.spans.find((s) => s.recordId === "t1")!;
  ok(span.failed === true && span.lane === 2, "failed tool keeps lane 2 + failed flag (CSS paints it red)");
}

console.log(`\n${passed} passed, ${failed} failed`);
if (!failed) process.stdout.write("task 1112 trajectory enrichment contract: OK\n");
