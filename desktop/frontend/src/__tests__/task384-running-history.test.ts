// Task 384 acceptance (running-state history unlock — A′+C, visibility +
// gate rework only; the task-243-adjacent paging protections are untouched):
//  A′  the `state.running` refusal gates are REMOVED — a live turn no longer
//      silently blocks history paging (head-prepend vs tail-append are
//      naturally disjoint; generation checks below the gate own collisions);
//  C①  the 15s stuck-loading self-heal runs BEFORE every gate, so a stuck
//      "loading" is resettable under any condition (0831 loading-leak lesson);
//  C②  every skip path is logged (zero-silent): the loading-in-progress skip
//      now reports, the exhausted path already reports (task 255);
//  243-lineage protections intact: revision/digest fingerprints, request seq,
//  double dedupe (316), identity-retry (101), exhausted single source (255).
//
// Run: npx tsx src/__tests__/task384-running-history.test.ts

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { duplicateLiveItemIds, retainedLiveTail } from "../lib/hydrateHistoryApply";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const read = (rel: string) => readFileSync(fileURLToPath(new URL(rel, import.meta.url)), "utf8");
const controller = read("../lib/useController.ts");

console.log("\ntask 384 running-state history unlock");

// Slice out the loadOlderHistory function body for gate-level assertions.
{
  const fnIdx = controller.indexOf("async function loadOlder(");
  ok(fnIdx > 0, "loadOlderHistory function located");
  const body = controller.slice(fnIdx, fnIdx + 6000);

  // A′: no running refusal CONDITION remains in the function (the rationale
  // comment may still mention the old gate — match the code shape only).
  ok(!/\|\| state\.running/.test(body) && !/state\.running\) return false/.test(body) && !/state\?\.running\)/.test(body), "running refusal gates removed from loadOlder (A′)");
  ok(body.includes("if (!state?.historyHasOlder) return false;"), "historyHasOlder remains the single hard gate");

  // C①: self-heal precedes the historyHasOlder gate.
  const healIdx = body.indexOf("historyOlderLoading) {");
  const gateIdx = body.indexOf("if (!state?.historyHasOlder) return false;");
  ok(healIdx > 0 && gateIdx > healIdx, "self-heal runs BEFORE the historyHasOlder gate (C①)");

  // C②: the loading-in-progress skip logs (zero-silent).
  ok(body.includes('reason=loading-in-progress'), "loading-in-progress skip logs (C② zero-silent)");

  // A′ evidence comment keeps the rationale next to the gate (maintainability
  // face; the dispatch-side log on refusal sites is unchanged).
  ok(body.includes("Task 384 (A′"), "gate rework carries the task-384 rationale");
}

// ③ collision protection lineage intact — the source still wires every
//    protection AFTER the gate (fingerprints, seq, dedupe, identity-retry).
{
  const fnIdx = controller.indexOf("async function loadOlder(");
  const body = controller.slice(fnIdx, fnIdx + 9000);
  const protections: Array<[string, string]> = [
    ["historyOlderSeq.current.get(targetTabId) !== requestSeq", "request-seq staleness discard"],
    ["fingerprintMatches(sessionRevision, currentRevision)", "revision fingerprint"],
    ["digestMatches(sessionDigest, currentDigest)", "digest fingerprint"],
    ["loadOlder(targetTabId, targetTurn, trigger, true)", "identity-retry (task 101)"],
    ['"history_older_exhausted"', "exhausted single source (task 255)"],
  ];
  for (const [needle, label] of protections) {
    const idx = body.indexOf(needle);
    ok(idx > 0 && idx > body.indexOf("if (!state?.historyHasOlder) return false;"), `${label} wired after the (single) gate`);
  }
  // Double dedupe (task 316) lives in the history_prepend REDUCER (it runs on
  // every prepend regardless of the gate) — assert its wiring file-level:
  ok(controller.includes("duplicateLiveItemIds") && controller.includes('case "history_prepend"'), "double-dedupe union wired in the prepend reducer (runs during running too)");
}

// ② prepend-during-stream anchoring (data face): prepending an older page
//    above a live tail keeps the live rows intact and drops duplicates —
//    the 316 union already proven; re-assert the interleave shape here with
//    a live turn mid-stream (assistant streaming row) present.
{
  type Row = { kind: string; id: string; text?: string };
  const page: Row[] = [
    { kind: "user", id: "old0", text: "earlier question" },
    { kind: "assistant", id: "old1", text: "earlier answer" },
  ];
  const live: Row[] = [
    { kind: "user", id: "e9", text: "current question" },
    { kind: "assistant", id: "live-turn", text: "streaming now" },
  ];
  const dupOld = duplicateLiveItemIds(page, live);
  ok(dupOld.length === 0, "disjoint page/live produce no duplicate removals");
  const merged = retainedLiveTail(live, page);
  ok(merged.every((r) => r.id !== "old0" && r.id !== "old1"), "prepended page rows do not clobber the live turn tail");
  ok(merged.some((r) => r.id === "live-turn"), "live streaming row survives the prepend interleave");
}

// C① regression guard: the self-heal dispatch text and its 15s threshold are
// unchanged (task 101 P0 semantics preserved, only its POSITION moved).
ok(controller.includes('"stale loading gate reset"') && controller.includes("15_000"), "self-heal dispatch + 15s threshold unchanged");

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
