// Run: tsx src/__tests__/subagent-progress.test.ts
//
// Verifies the reserved ToolProgress channels (reasonix.subagent.*) update
// only the target card's in-memory subagentProgress: never tool.output, never
// the settled parent assistant segment, never history data. Also locks the background keep-
// running rule, the group-card settle rule, preview caps, and the isolation
// of concurrent children.

import { historyMessagesToItems, initialState, reducer, SUBAGENT_PROGRESS_STATUS, SUBAGENT_PROGRESS_REASONING, SUBAGENT_PROGRESS_TEXT, SUBAGENT_PROGRESS_NOTICE } from "../lib/useController";
import type { HistoryMessage, WireTool } from "../lib/types";
import type { Item } from "../lib/useController";

type TestState = typeof initialState;
type ToolItem = Extract<Item, { kind: "tool" }>;

let passed = 0;
let failed = 0;

function eq<T>(a: T, b: T, label: string) {
  if (a === b) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(b)}, got ${JSON.stringify(a)}\n`);
    failed += 1;
  }
}

function ok(cond: boolean, label: string) {
  if (cond) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function toolItems(s: TestState): ToolItem[] {
  return s.items.filter((it): it is ToolItem => it.kind === "tool");
}

function toolById(s: TestState, id: string): ToolItem {
  const it = toolItems(s).find((t) => t.id === id);
  if (!it) throw new Error(`tool ${id} missing`);
  return it;
}

function dispatch(s: TestState, tool: WireTool): TestState {
  return reducer(s, { type: "event", e: { kind: "tool_dispatch", tool } });
}

function result(s: TestState, tool: WireTool): TestState {
  return reducer(s, { type: "event", e: { kind: "tool_result", tool } });
}

function progress(s: TestState, tool: WireTool): TestState {
  return reducer(s, { type: "event", e: { kind: "tool_progress", tool } });
}

function text(s: TestState, t: string): TestState {
  return reducer(s, { type: "event", e: { kind: "text", text: t } });
}

function reasoning(s: TestState, t: string): TestState {
  return reducer(s, { type: "event", e: { kind: "reasoning", reasoning: t } });
}

function progressTool(id: string, name: string, output: string, extra: Partial<WireTool> = {}): WireTool {
  return { id, name, output, readOnly: false, ...extra };
}

console.log("\nsubagent progress reducer");

// --- 1. Reserved channels update only the target card's preview ------------

{
  let s = initialState;
  s = text(s, "parent answer part 1");
  s = reasoning(s, "parent thinking");
  s = dispatch(s, { id: "task-1", name: "task", args: "{}", readOnly: true });
  const parentBefore = JSON.stringify(s.items.find((item) => item.kind === "assistant"));

  s = progress(s, progressTool("task-1", SUBAGENT_PROGRESS_STATUS, "running"));
  s = progress(s, progressTool("task-1", SUBAGENT_PROGRESS_REASONING, "child thinks"));
  s = progress(s, progressTool("task-1", SUBAGENT_PROGRESS_TEXT, "child answer preview"));
  s = progress(s, progressTool("task-1", SUBAGENT_PROGRESS_NOTICE, "heads up"));
  s = progress(s, progressTool("task-1", SUBAGENT_PROGRESS_STATUS, "completed", { durationMs: 1234 }));

  const card = toolById(s, "task-1");
  ok(card.subagentProgress?.phase === "completed", "terminal status phase applied");
  eq(card.subagentProgress?.reasoning, "child thinks", "reasoning preview routed to subagentProgress");
  eq(card.subagentProgress?.text, "child answer preview", "text preview routed to subagentProgress");
  eq(card.subagentProgress?.notice, "heads up", "notice preview routed to subagentProgress");
  eq(card.subagentProgress?.durationMs, 1234, "terminal duration captured");
  eq(card.status, "done", "completed terminal maps to done status");
  eq(card.output, undefined, "preview never writes tool.output");
  const parent = s.items.find((item) => item.kind === "assistant");
  eq(JSON.stringify(parent), parentBefore, "settled parent assistant bytes unchanged by child previews");
  eq(parent?.kind === "assistant" ? parent.text : undefined, "parent answer part 1", "parent text unchanged by child previews");
  eq(parent?.kind === "assistant" ? parent.reasoning : undefined, "parent thinking", "parent reasoning unchanged by child previews");
}

// --- 2. Unknown status phases and unknown names are ignored ---------------

{
  let s = initialState;
  s = dispatch(s, { id: "task-1", name: "task", args: "{}", readOnly: true });
  const before = JSON.stringify(toolById(s, "task-1"));
  s = progress(s, progressTool("task-1", SUBAGENT_PROGRESS_STATUS, "not-a-phase"));
  s = progress(s, progressTool("task-1", "reasonix.subagent.bogus", "x"));
  eq(JSON.stringify(toolById(s, "task-1")), before, "unknown phase and unknown channel ignored");
}

// --- 3. Concurrent children stay isolated per card ID ----------------------

{
  let s = initialState;
  s = dispatch(s, { id: "p-1", name: "parallel_tasks", args: "{}", readOnly: true });
  s = dispatch(s, { id: "p-1/sub-1", name: "task", args: "{}", readOnly: true, parentId: "p-1" });
  s = dispatch(s, { id: "p-1/sub-2", name: "task", args: "{}", readOnly: true, parentId: "p-1" });

  s = progress(s, progressTool("p-1/sub-1", SUBAGENT_PROGRESS_REASONING, "AAAA"));
  s = progress(s, progressTool("p-1/sub-2", SUBAGENT_PROGRESS_REASONING, "BBBB"));
  s = progress(s, progressTool("p-1/sub-1", SUBAGENT_PROGRESS_TEXT, "1111"));
  s = progress(s, progressTool("p-1/sub-2", SUBAGENT_PROGRESS_TEXT, "2222"));

  eq(toolById(s, "p-1/sub-1").subagentProgress?.reasoning, "AAAA", "child 1 reasoning isolated");
  eq(toolById(s, "p-1/sub-1").subagentProgress?.text, "1111", "child 1 text isolated");
  eq(toolById(s, "p-1/sub-2").subagentProgress?.reasoning, "BBBB", "child 2 reasoning isolated");
  eq(toolById(s, "p-1/sub-2").subagentProgress?.text, "2222", "child 2 text isolated");
  ok(!toolById(s, "p-1/sub-1").output && !toolById(s, "p-1/sub-2").output, "no child preview leaks into outputs");
}

// --- 4. Background call keeps running until its terminal progress ----------

{
  let s = initialState;
  // A real background dispatch carries run_in_background in the call args
  // (the engine's isBackgroundTaskCall reads the same flag); its result is a
  // job id, not the child's answer.
  s = dispatch(s, { id: "bg-1", name: "task", args: '{"prompt":"x","run_in_background":true}', readOnly: true });
  s = result(s, { id: "bg-1", name: "task", readOnly: true, output: "Started background task \"bg\" (job-1)." });
  eq(toolById(s, "bg-1").status, "running", "job id result keeps the card running");
  s = progress(s, progressTool("bg-1", SUBAGENT_PROGRESS_STATUS, "queued"));
  s = progress(s, progressTool("bg-1", SUBAGENT_PROGRESS_STATUS, "running"));
  s = progress(s, progressTool("bg-1", SUBAGENT_PROGRESS_STATUS, "completed", { durationMs: 42 }));
  eq(toolById(s, "bg-1").status, "done", "terminal progress settles the background card");
  eq(toolById(s, "bg-1").subagentProgress?.phase, "completed", "background card phase completed");
  eq(toolById(s, "bg-1").output, "Started background task \"bg\" (job-1).", "job id output retained");
}

// --- 5. Cancelled / failed terminals use stopped / error semantics ---------

{
  let s = initialState;
  s = dispatch(s, { id: "c-1", name: "task", args: "{}", readOnly: true });
  s = progress(s, progressTool("c-1", SUBAGENT_PROGRESS_STATUS, "cancelled"));
  s = result(s, { id: "c-1", name: "task", readOnly: true, err: "cancelled: context canceled" });
  eq(toolById(s, "c-1").status, "stopped", "cancelled terminal wins over an error result");

  s = dispatch(s, { id: "f-1", name: "task", args: "{}", readOnly: true });
  s = progress(s, progressTool("f-1", SUBAGENT_PROGRESS_STATUS, "failed"));
  s = result(s, { id: "f-1", name: "task", readOnly: true, err: "provider exploded" });
  eq(toolById(s, "f-1").status, "error", "failed terminal maps to error status");
}

// --- 6. Group card settles only from its own lifecycle terminal ------------

{
  let s = initialState;
  s = dispatch(s, { id: "fl-1", name: "fleet", args: '{"run_in_background":true}', readOnly: true });
  s = dispatch(s, { id: "fl-1/fleet-1", name: "task", args: "{}", readOnly: true, parentId: "fl-1" });
  s = dispatch(s, { id: "fl-1/fleet-2", name: "task", args: "{}", readOnly: true, parentId: "fl-1" });

  // Background fleet: the result (job id) arrives while children still run.
  s = result(s, { id: "fl-1", name: "fleet", readOnly: true, output: "Started background fleet (job-2)." });
  eq(toolById(s, "fl-1").status, "running", "fleet card stays running after the job-id result");

  s = progress(s, progressTool("fl-1/fleet-1", SUBAGENT_PROGRESS_STATUS, "completed"));
  s = progress(s, progressTool("fl-1/fleet-2", SUBAGENT_PROGRESS_STATUS, "completed"));
  eq(toolById(s, "fl-1").status, "running", "all children terminal alone must not settle the fleet");

  // The group settles from its own lifecycle terminal event.
  s = progress(s, progressTool("fl-1", SUBAGENT_PROGRESS_STATUS, "completed", { durationMs: 9000 }));
  eq(toolById(s, "fl-1").status, "done", "group completed terminal settles the fleet");
  eq(toolById(s, "fl-1").subagentProgress?.phase, "completed", "settled fleet phase completed");
}

// --- 6b. Job-id first + fast child must not settle the group ----------------

{
  let s = initialState;
  s = dispatch(s, { id: "fl-0", name: "fleet", args: '{"run_in_background":true}', readOnly: true });
  // Background order: job-id result, then child-1 dispatches and finishes
  // while later children have not dispatched yet.
  s = result(s, { id: "fl-0", name: "fleet", readOnly: true, output: "Started background fleet (job-3)." });
  eq(toolById(s, "fl-0").status, "running", "fleet stays running after the job-id result");

  s = dispatch(s, { id: "fl-0/fleet-1", name: "task", args: "{}", readOnly: true, parentId: "fl-0" });
  s = progress(s, progressTool("fl-0/fleet-1", SUBAGENT_PROGRESS_STATUS, "completed"));
  eq(toolById(s, "fl-0").status, "running", "a fast first child must not settle the fleet");

  // A later child appears and runs while the group is still live.
  s = dispatch(s, { id: "fl-0/fleet-2", name: "task", args: "{}", readOnly: true, parentId: "fl-0" });
  s = progress(s, progressTool("fl-0/fleet-2", SUBAGENT_PROGRESS_STATUS, "reasoning"));
  eq(toolById(s, "fl-0").status, "running", "fleet keeps running while a later child works");

  s = progress(s, progressTool("fl-0/fleet-2", SUBAGENT_PROGRESS_STATUS, "completed"));
  s = progress(s, progressTool("fl-0", SUBAGENT_PROGRESS_STATUS, "completed"));
  eq(toolById(s, "fl-0").status, "done", "group terminal settles the fleet after all children");
}

// --- 6c. Zero-child cancellation and group failure --------------------------

{
  // A background fleet cancelled before any child dispatched still receives
  // its explicit cancelled terminal from the backend.
  let s = initialState;
  s = dispatch(s, { id: "zc-1", name: "fleet", args: '{"run_in_background":true}', readOnly: true });
  s = progress(s, progressTool("zc-1", SUBAGENT_PROGRESS_STATUS, "cancelled"));
  s = result(s, { id: "zc-1", name: "fleet", readOnly: true, err: "cancelled: context canceled" });
  eq(toolById(s, "zc-1").status, "stopped", "zero-child cancelled fleet shows stopped");

  // A group failed terminal maps to error regardless of children.
  s = dispatch(s, { id: "gf-1", name: "parallel_tasks", args: "{}", readOnly: true });
  s = dispatch(s, { id: "gf-1/sub-1", name: "task", args: "{}", readOnly: true, parentId: "gf-1" });
  s = progress(s, progressTool("gf-1/sub-1", SUBAGENT_PROGRESS_STATUS, "failed"));
  s = progress(s, progressTool("gf-1", SUBAGENT_PROGRESS_STATUS, "failed"));
  s = result(s, { id: "gf-1", name: "parallel_tasks", readOnly: true, output: "Completed 1 parallel tasks..." });
  eq(toolById(s, "gf-1").status, "error", "group failed terminal maps to error");
}

// --- 7. Preview caps keep recent tails -------------------------------------

{
  let s = initialState;
  s = dispatch(s, { id: "cap-1", name: "task", args: "{}", readOnly: true });
  const big = "x".repeat(12_000);
  const tail = "TAIL";
  s = progress(s, progressTool("cap-1", SUBAGENT_PROGRESS_REASONING, big + tail));
  s = progress(s, progressTool("cap-1", SUBAGENT_PROGRESS_TEXT, big + tail));
  s = progress(s, progressTool("cap-1", SUBAGENT_PROGRESS_NOTICE, big + tail));

  const sp = toolById(s, "cap-1").subagentProgress!;
  eq(sp.reasoning.length, 8_192, "reasoning preview capped at 8 KiB");
  ok(sp.reasoning.endsWith(tail), "reasoning keeps the recent tail");
  eq(sp.text.length, 8_192, "text preview capped at 8 KiB");
  eq(sp.notice.length, 2_048, "notice preview capped at 2 KiB");
  eq(sp.truncated, false, "frontend cap alone does not mark truncated (backend sends the flag)");
  s = progress(s, progressTool("cap-1", SUBAGENT_PROGRESS_TEXT, "more", { truncated: true }));
  eq(toolById(s, "cap-1").subagentProgress?.truncated, true, "backend truncated flag honored");
}

// --- 8. Terminal keeps the preview; history hydration never restores it ----

{
  let s = initialState;
  s = dispatch(s, { id: "h-1", name: "task", args: "{}", readOnly: true });
  s = progress(s, progressTool("h-1", SUBAGENT_PROGRESS_REASONING, "kept after terminal"));
  s = progress(s, progressTool("h-1", SUBAGENT_PROGRESS_STATUS, "completed"));
  eq(toolById(s, "h-1").subagentProgress?.reasoning, "kept after terminal", "preview retained after terminal");

  const hydrated = historyMessagesToItems([
    { role: "user", content: "go" },
    { role: "assistant", content: "", toolCalls: [{ id: "h-1", name: "task", arguments: "{}" }] },
    { role: "tool", toolCallId: "h-1", toolName: "task", content: "done" },
  ] as HistoryMessage[], "h").items.filter((it) => it.kind === "tool");
  ok(hydrated.every((it) => !it.subagentProgress), "history hydration never restores transient progress");
}

// --- 9. Nested real tool activity touches the parent card ------------------

{
  let s = initialState;
  s = dispatch(s, { id: "t-1", name: "task", args: "{}", readOnly: true });
  const before = toolById(s, "t-1").subagentProgress!.lastActivityAt;
  s = dispatch(s, { id: "t-1/bash_1", name: "bash", args: "ls", readOnly: false, parentId: "t-1" });
  const after = toolById(s, "t-1").subagentProgress!;
  eq(after.phase, "tool", "nested dispatch flips parent phase to tool");
  ok(after.lastActivityAt >= before, "nested dispatch refreshes parent recent activity");
}

// --- 10. Ordinary tool progress behavior is unchanged ----------------------

{
  let s = initialState;
  s = dispatch(s, { id: "b-1", name: "bash", args: "ls", readOnly: false });
  s = progress(s, progressTool("b-1", "bash", "file1\n"));
  s = progress(s, progressTool("b-1", "bash", "file2\n"));
  eq(toolById(s, "b-1").output, "file1\nfile2\n", "ordinary progress still appends to tool.output");
  eq(toolById(s, "b-1").subagentProgress, undefined, "ordinary tools never gain a progress preview");

  // Archiving on result still applies to sub-agent cards without dropping the
  // preview (the tracker always emits its terminal before the result).
  s = dispatch(s, { id: "arc-1", name: "task", args: "{}", readOnly: true });
  s = progress(s, progressTool("arc-1", SUBAGENT_PROGRESS_STATUS, "completed"));
  s = result(s, { id: "arc-1", name: "task", readOnly: true, output: "done" });
  const archived = toolById(s, "arc-1");
  eq(archived.status, "done", "archived sub-agent card settles");
  ok(archived.subagentProgress !== undefined, "subagentProgress survives result archiving");
}

// --- 11. Terminal outcome metadata survives output archiving ---------------

{
  let s = initialState;
  s = dispatch(s, { id: "outcome-1", name: "task", args: "{}", readOnly: true });
  s = progress(s, progressTool("outcome-1", SUBAGENT_PROGRESS_STATUS, "partial"));
  s = result(s, {
    id: "outcome-1",
    name: "task",
    readOnly: true,
    output: "Subagent reference: sa_child\nSubagent outcome: status=partial retryable=true error_code=completion_uncertain",
    subagentRef: "sa_child",
    subagentStatus: "partial",
    subagentErrorCode: "completion_uncertain",
    subagentRetryable: true,
  });
  const archived = toolById(s, "outcome-1");
  eq(JSON.stringify(archived.subagentOutcome), JSON.stringify(["sa_child", "partial", "completion_uncertain", true]), "terminal outcome is normalized once at the result boundary");
  eq(archived.output, undefined, "outcome metadata survives without retaining archived tool output");
}

// --- 12. 任务 533: a foreground result settles the card even when the -------
// terminal status progress event was lost or reordered (the completed
// children stuck in "运行中" screenshot). The directory projection must move
// them to the ended section at the completion moment.

{
  let s = initialState;
  s = dispatch(s, { id: "g-1", name: "parallel_tasks", args: "{}", readOnly: true });
  for (let i = 1; i <= 3; i += 1) {
    s = dispatch(s, { id: `g-1/sub-${i}`, name: "task", args: `{"prompt":"t${i}"}`, readOnly: true, parentId: "g-1" });
    s = progress(s, progressTool(`g-1/sub-${i}`, SUBAGENT_PROGRESS_STATUS, "running"));
  }
  // Every terminal status progress event is lost; only the results arrive.
  for (let i = 1; i <= 3; i += 1) {
    s = result(s, { id: `g-1/sub-${i}`, name: "task", readOnly: true, output: `answer ${i}`, parentId: "g-1" });
    eq(toolById(s, `g-1/sub-${i}`).status, "done", `lost terminal: child ${i} settles done on its result`);
  }
  // The group's own result settles it too (parallel_tasks has no background form).
  s = result(s, { id: "g-1", name: "parallel_tasks", readOnly: true, output: "Completed 3 parallel tasks:" });
  eq(toolById(s, "g-1").status, "done", "lost terminal: foreground group settles on its result");

  // Dock projection (lib/subagentDirectory): nothing left running, the batch
  // is listed in the ended section.
  const { buildSubagentDirectory } = await import("../lib/subagentDirectory");
  const dir = buildSubagentDirectory(s.items);
  eq(dir.running.length, 0, "lost terminal: directory running section empty");
  eq(dir.ended.length, 4, "lost terminal: directory ended section lists the batch");

  // A failed foreground child still shows error semantics without a terminal.
  s = dispatch(s, { id: "g-2/sub-1", name: "task", args: "{}", readOnly: true, parentId: "g-2" });
  s = result(s, { id: "g-2/sub-1", name: "task", readOnly: true, err: "child exploded", parentId: "g-2" });
  eq(toolById(s, "g-2/sub-1").status, "error", "lost terminal: failed child settles error on its result");

  // An intact cancelled terminal keeps its stopped semantics (unchanged rule).
  s = dispatch(s, { id: "g-2/sub-2", name: "task", args: "{}", readOnly: true, parentId: "g-2" });
  s = progress(s, progressTool("g-2/sub-2", SUBAGENT_PROGRESS_STATUS, "cancelled"));
  s = result(s, { id: "g-2/sub-2", name: "task", readOnly: true, err: "cancelled: context canceled", parentId: "g-2" });
  eq(toolById(s, "g-2/sub-2").status, "stopped", "intact cancelled terminal keeps stopped semantics");

  // Background dispatches keep the legacy rule: a job-id result never settles.
  s = dispatch(s, { id: "g-3", name: "fleet", args: '{"run_in_background":true}', readOnly: true });
  s = result(s, { id: "g-3", name: "fleet", readOnly: true, output: "Started background fleet (job-9)." });
  eq(toolById(s, "g-3").status, "running", "background fleet result keeps the card running");
  s = progress(s, progressTool("g-3", SUBAGENT_PROGRESS_STATUS, "completed", { durationMs: 5 }));
  eq(toolById(s, "g-3").status, "done", "background fleet settles only on its terminal");
}

console.log(`\nsubagent progress: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
