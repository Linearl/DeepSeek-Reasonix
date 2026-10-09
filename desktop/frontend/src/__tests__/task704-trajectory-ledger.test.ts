// Run: tsx src/__tests__/task704-trajectory-ledger.test.ts
// 任务 704: unit contract for the trajectory read projections. The ledger is
// a pure projection over Item[] (the transcript read feed — 597 纪律: never
// touch storage), so these tests pin turn grouping, time-anchor inheritance,
// degrade-when-unknown timing, and the timeline fraction model.
import assert from "node:assert";
import process from "node:process";
import { buildTrajectoryLedger, trajectorySummary, type TrajectoryRecord } from "../lib/trajectoryLedger";
import {
  buildTrajectoryTimeline,
  formatTrajectoryClock,
  formatTrajectoryDuration,
  pointerRangeToDomain,
  recordIdsInFocus,
} from "../lib/trajectoryTimeline";
import { historySearchAndAnswer } from "../lib/searchTranscript";
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

console.log("\n任务 704 trajectory ledger + timeline projections");

// ① Turn grouping: every user item opens a turn; steps count assistants.
{
  const items: Item[] = [
    user("u1", "first", { createdAt: 1000, historyTurn: 3 }),
    assistant("a1", { workDurationMs: 500 }),
    tool("t1"),
    assistant("a2"),
    user("u2", "second", { createdAt: 9000 }),
    assistant("a3"),
  ];
  const ledger = buildTrajectoryLedger(items);
  ok(ledger.records.length === 6, "one record per item");
  ok(ledger.turnCount === 2, "two turns from two user items");
  const [u1, a1, t1, a2, u2, a3] = ledger.records as [TrajectoryRecord, TrajectoryRecord, TrajectoryRecord, TrajectoryRecord, TrajectoryRecord, TrajectoryRecord];
  ok(u1.turn === 1 && u1.turnStart && u1.turnLabel === 3, "first turn labeled by historyTurn");
  ok(a1.turn === 1 && a1.step === 1 && a2.turn === 1 && a2.step === 2, "steps count assistants inside a turn");
  ok(u2.turn === 2 && u2.turnStart && u2.turnLabel === undefined, "second turn has no global label");
  ok(a3.turn === 2 && a3.step === 1, "steps reset per turn");
  ok(ledger.firstTurnLabel === 3 && ledger.lastTurnLabel === 3, "turn labels reported");
  const summary = trajectorySummary(ledger);
  ok(summary.turns === 2 && summary.assistants === 3 && summary.tools === 1, "summary counts");
}

// ② Time anchors: own timestamps are known; later records inherit (≈); the
//    pre-anchor head has no anchor at all.
{
  const items: Item[] = [
    tool("t0"), // no startedAt, nothing before it: at === undefined
    user("u1", "hi", { createdAt: 1000 }),
    assistant("a1", { createdAt: 1500, workDurationMs: 400 }),
    tool("t1"), // inherits 1500
  ];
  const ledger = buildTrajectoryLedger(items);
  const [t0, u1, a1, t1] = ledger.records;
  ok(t0.at === undefined && !t0.atKnown, "pre-anchor record has no anchor");
  ok(u1.at === 1000 && u1.atKnown, "user anchor from createdAt");
  ok(a1.at === 1500 && a1.atKnown && a1.durationMs === 400, "assistant anchor + duration");
  ok(t1.at === 1500 && !t1.atKnown, "tool inherits the last anchor");
}

// ③ Failure surfacing: tool error status + warn notices carry failure chips.
{
  const items: Item[] = [
    user("u1", "hi"),
    tool("t1", { status: "error", error: "boom" }),
    { kind: "notice", id: "n1", level: "warn", text: "watch out", code: "ctx_low" },
    { kind: "notice", id: "n2", level: "info", text: "fyi" },
  ];
  const ledger = buildTrajectoryLedger(items);
  const [u1, t1, n1, n2] = ledger.records;
  ok(u1.failed === false, "user not failed");
  ok(t1.failed && t1.errorCode === "tool_error", "errored tool flagged");
  ok(n1.failed && n1.errorCode === "ctx_low", "warn notice flagged with its code");
  ok(!n2.failed, "info notice clean");
  ok(trajectorySummary(ledger).failed === 2, "summary counts failures");
}

// ④ Degrade rules: running records never carry durations; TTFT only when it
//    is a real subset of the work duration (live-only data).
{
  const items: Item[] = [
    user("u1", "hi", { createdAt: 1000 }),
    assistant("a1", { streaming: true, reasoningDurationMs: 300 }),
    tool("t1", { status: "running", durationMs: 5, startedAt: 1100 }),
    assistant("a2", { workDurationMs: 1000, reasoningDurationMs: 1200 }), // ttft >= duration: absent
    assistant("a3", { workDurationMs: 1000, reasoningDurationMs: 250 }), // ttft kept
    assistant("a4", { workDurationMs: 800 }), // no ttft at all (history norm)
  ];
  const ledger = buildTrajectoryLedger(items);
  const [u1, a1, t1, a2, a3, a4] = ledger.records;
  ok(u1.running === false, "settled user not running");
  ok(a1.running && a1.durationMs === undefined && a1.ttftMs === undefined, "streaming assistant: marker only, no invented timing");
  ok(t1.running && t1.durationMs === undefined, "running tool: no duration");
  ok(a2.ttftMs === undefined, "ttft >= duration degraded away");
  ok(a3.ttftMs === 250 && a3.durationMs === 1000, "ttft subset kept");
  ok(a4.ttftMs === undefined && a4.durationMs === 800, "plain history assistant keeps workDuration only");
}

// ⑤ Nested subtool records flagged; compaction records inherit anchors.
{
  const items: Item[] = [
    user("u1", "hi", { createdAt: 1000 }),
    assistant("a1", { createdAt: 1100 }),
    tool("task1", { name: "task" }),
    tool("sub1", { parentId: "task1", name: "bash" }),
    { kind: "compaction", id: "c1", pending: false, trigger: "auto", messages: 12, summary: "folded", archive: "…" },
  ];
  const ledger = buildTrajectoryLedger(items);
  const [, , task1, sub1, c1] = ledger.records;
  ok(!task1.nested && sub1.nested, "subtool nesting flagged");
  ok(c1.kind === "compaction" && c1.at === 1100 && !c1.atKnown, "compaction inherits anchor");
}

// ⑥ Timeline: fractions on a padded domain; zero-duration records become
//    markers; ttftFraction projects the live-only split.
{
  const ledger = buildTrajectoryLedger([
    user("u1", "hi", { createdAt: 1000 }),
    assistant("a1", { createdAt: 2000, workDurationMs: 1000, reasoningDurationMs: 250 }),
    tool("t1", { startedAt: 4000 }), // anchored, no duration → marker
    user("u2", "again"), // no anchor at all → never on the timeline
  ]);
  const model = buildTrajectoryTimeline(ledger.records);
  ok(model !== null, "timeline builds for anchored history");
  if (model) {
    // The t1 marker (startedAt 4000, no duration) extends the domain: the
    // overview must include anchored markers, not just duration spans.
    ok(model.t0 === 1000 && model.t1 === 4000, "domain covers first anchor to last anchored record");
    const span = model.spans.find((s) => s.recordId === "a1");
    ok(!!span, "assistant projected as span");
    if (span) {
      ok(span.start > 0 && span.start < 0.6, "span starts inside the padded domain");
      ok(Math.abs((span.width + span.start) - span.start - span.width) < 1e-9, "span width finite");
      ok(span.width > 0.3 && span.width < 0.6, "span covers half the domain within padding");
      ok(Math.abs(span.ttftFraction! - 0.25) < 1e-9, "ttft fraction = 250/1000");
    }
    const marker = model.markers.find((m) => m.recordId === "t1");
    ok(!!marker && marker.width === 0 && marker.start > 0.9, "anchor-less-duration record is a late marker");
    ok(!model.spans.some((s) => s.recordId === "u2") && !model.markers.some((m) => m.recordId === "u2"),
      "unanchored record stays off the timeline");
  }
}

// ⑦ Degenerate domains: single instant stays valid; anchor-less history → null.
{
  const single = buildTrajectoryTimeline(buildTrajectoryLedger([user("u1", "hi", { createdAt: 500 })]).records);
  ok(single !== null && single.t1 > single.t0 && single.spans.length === 0 && single.markers.length === 1,
    "single instant: valid domain, one marker");
  ok(buildTrajectoryTimeline(buildTrajectoryLedger([tool("t0"), user("u1", "hi")]).records) === null,
    "no anchors: timeline is null (empty-state view)");
}

// ⑧ Focus helpers: drag range normalization + membership.
{
  const ledger = buildTrajectoryLedger([
    user("u1", "hi", { createdAt: 1000 }),
    assistant("a1", { createdAt: 2000, workDurationMs: 500 }),
    tool("t1", { startedAt: 2600, durationMs: 100 }),
  ]);
  const model = buildTrajectoryTimeline(ledger.records)!;
  // Domain 1000..2700 (t1 ends at 2700). Fractions 0.3/0.95 map to ~1510/2615,
  // which covers a1 (2000) and t1 (2600) but not u1 (1000).
  const range = pointerRangeToDomain(0.95, 0.3, model);
  ok(range.start < range.end, "pointer range normalized start<=end");
  const ids = recordIdsInFocus(ledger.records, range);
  ok(ids.has("a1") && ids.has("t1") && !ids.has("u1"), "focus covers anchored records inside the range only");
}

// ⑨ Hydrate path: assistant items pick up wire createdAt, and stay
//    byte-identical when the field is absent (conditional spread).
{
  const withTime = historySearchAndAnswer("x1", { content: "answer", workDurationMs: 10, createdAt: 1234 });
  const item = withTime.find((it) => it.kind === "assistant") as Extract<Item, { kind: "assistant" }>;
  ok(item.createdAt === 1234, "hydrated assistant carries createdAt");
  const withoutTime = historySearchAndAnswer("x2", { content: "answer" });
  const bare = withoutTime.find((it) => it.kind === "assistant") as Extract<Item, { kind: "assistant" }>;
  ok(!("createdAt" in bare), "absent wire createdAt keeps the pre-704 item shape");
}

// ⑩ Hover formatters.
{
  ok(formatTrajectoryDuration(450) === "450ms", "sub-second ms formatting");
  ok(formatTrajectoryDuration(2500) === "2.50s", "second formatting");
  ok(formatTrajectoryDuration(65_000) === "1m05s", "minute formatting");
  ok(formatTrajectoryDuration(Number.NaN) === "—", "invalid duration degrades");
  ok(formatTrajectoryClock(new Date(2026, 0, 1, 14, 2, 11).getTime()) === "14:02:11", "clock formatting");
}

console.log(`\n${passed} passed, ${failed} failed`);
if (!failed) process.stdout.write("task 704 trajectory ledger contract: OK\n");
