// Run: npx tsx src/__tests__/history-older-exhausted.test.ts
// Task 255: the "load earlier" millis-retry loop. The UI flag (historyHasOlder,
// set by the backend page call) and the store flag (session.hasOlder, set by
// slice calls) came from two different backend objects; when they disagreed,
// loadOlder's early-out returned undefined, the controller read that as
// "history page unavailable", and every retry re-entered the same early-out.
//
// Fix under test: the store early-out now returns kind "exhausted" carrying the
// authoritative projection, and the controller maps it to
// history_older_exhausted so the two layers resync instead of looping.

import assert from "node:assert/strict";
import { TranscriptStore, type TranscriptBackend } from "../lib/transcriptStore";
import type { HistoryEntry, HistorySlice } from "../lib/types";

let passed = 0;
let failed = 0;
function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
    failed += 1;
  }
}

function entry(id: string, turn: number): HistoryEntry {
  return {
    entryId: id,
    turn,
    order: turn,
    message: { role: "user", content: `m-${id}` } as unknown as HistoryEntry["message"],
    refs: [],
  };
}

function slice(overrides: Partial<HistorySlice> = {}): HistorySlice {
  return {
    entries: [],
    nextCursor: "",
    hasOlder: false,
    totalTurns: 1,
    startTurn: 1,
    endTurn: 1,
    stale: false,
    revision: 7,
    revisionKnown: true,
    digest: "d",
    ...overrides,
  };
}

/** Backend that serves the given slices in call order (last one repeats). */
function backendOf(pages: HistorySlice[]): { backend: TranscriptBackend; calls: () => number } {
  let call = 0;
  const backend = {
    HistorySliceForTab: async () => {
      const s = pages[Math.min(call, pages.length - 1)];
      call += 1;
      return s;
    },
    HistoryContentForTab: async () => {
      throw new Error("content refs are not exercised in this test");
    },
  } as unknown as TranscriptBackend;
  return { backend, calls: () => call };
}

const TAB = "tab-255";
const SESSION = "/tmp/session-255.jsonl";

console.log("\nhistory older exhausted (task 255)");

// 1. Prime already says hasOlder=false: loadOlder must answer "exhausted"
//    (before the fix this was undefined → the controller reported a failure).
{
  const { backend } = backendOf([slice({ entries: [entry("a", 1)], hasOlder: false })]);
  const store = new TranscriptStore(backend);
  const primed = await store.loadLatest(TAB, SESSION);
  eq(primed?.hasOlder, false, "prime: store hasOlder=false from the page slice");
  const result = await store.loadOlder(TAB, SESSION);
  eq(result?.kind, "exhausted", "exhausted store state returns kind=exhausted, not undefined");
  eq(result?.hasOlder, false, "exhausted result carries the authoritative flag");
}

// 2. Paging to the very end, then one more: the second call is the exact
//    dead-loop shape (UI may still hold hasOlder=true from its own layer).
{
  const { backend } = backendOf([
    slice({ entries: [entry("b", 2), entry("a", 1)], nextCursor: "c1", hasOlder: true, totalTurns: 3 }),
    slice({ entries: [entry("a0", 0)], nextCursor: "", hasOlder: false, totalTurns: 3 }),
  ]);
  const store = new TranscriptStore(backend);
  await store.loadLatest(TAB, SESSION);
  const page = await store.loadOlder(TAB, SESSION);
  eq(page?.kind, "prepend", "paging: first older page prepends");
  eq(page?.hasOlder, false, "paging: store now knows history ended");
  const again = await store.loadOlder(TAB, SESSION);
  eq(again?.kind, "exhausted", "paging: retrying past the end returns exhausted (was undefined)");
}

// 3. olderInFlight stays a scheduling early-out (undefined): the controller's
//    in-flight map normally prevents this, and it must not read as "exhausted"
//    while a real page is being fetched.
{
  let release!: (slice: HistorySlice) => void;
  const gated = new Promise<HistorySlice>((resolve) => { release = resolve; });
  let call = 0;
  const backend = {
    HistorySliceForTab: async () => {
      call += 1;
      if (call === 1) return slice({ entries: [entry("b", 2)], nextCursor: "c1", hasOlder: true });
      return gated;
    },
    HistoryContentForTab: async () => {
      throw new Error("not expected");
    },
  } as unknown as TranscriptBackend;
  const store = new TranscriptStore(backend);
  await store.loadLatest(TAB, SESSION);
  const first = store.loadOlder(TAB, SESSION);
  const second = await store.loadOlder(TAB, SESSION);
  eq(second, undefined, "in-flight concurrency stays a scheduling early-out (undefined)");
  release(slice({ entries: [entry("a", 1)], nextCursor: "", hasOlder: false }));
  eq((await first)?.kind, "prepend", "gated page completes after release");
}

// 4. Controller contract (source-level pin): the exhausted kind maps to
//    history_older_exhausted, which is what resyncs the UI layer.
{
  const { readFileSync } = await import("node:fs");
  const { join, dirname } = await import("node:path");
  const root = join(dirname(new URL(import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, "$1")), "..");
  const controller = readFileSync(join(root, "lib", "useController.ts"), "utf8");
  eq(controller.includes('result?.kind === "exhausted"'), true, "controller: exhausted kind is consumed");
  eq(/result\?\.kind === "exhausted"[\s\S]{0,400}?type: "history_older_exhausted"/.test(controller), true,
    "controller: exhausted dispatches history_older_exhausted (UI flag resyncs to false)");
}

console.log(`\nhistory older exhausted: ${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
