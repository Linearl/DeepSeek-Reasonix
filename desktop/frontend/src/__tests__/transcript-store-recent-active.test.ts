// Run: npx tsx src/__tests__/transcript-store-recent-active.test.ts
//
// Task 190-M1: recent-active eviction exemption. Task 196 traced "switch back
// to a large session, full reload" to the history body budget breaching on
// that session alone — once its cooldown lapsed, the budget loop evicted it
// no matter how recently the user had switched away. A tab that left the
// active slot inside `recentActiveExemptMs` is now exempt outright; the
// window closing is what restores normal eviction, so budgets still converge.

import assert from "node:assert/strict";
import { TranscriptStore } from "../lib/transcriptStore";
import type {
  HistoryEntry,
  HistoryMessage,
  HistorySlice,
  HistorySliceRequest,
} from "../lib/types";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

// ── minimal one-page backend ────────────────────────────────────────────────

class FakeBackend {
  sliceCalls: HistorySliceRequest[] = [];
  /** Requests from this tab get the single-point-breach transcript; others are small. */
  constructor(private readonly bigTab: string) {}

  private messagesFor(tabId: string): HistoryMessage[] {
    return tabId === this.bigTab ? BIG_MESSAGES : SMALL_MESSAGES;
  }

  async HistorySliceForTab(tabID: string, req: HistorySliceRequest): Promise<HistorySlice> {
    this.sliceCalls.push(req);
    const messages = this.messagesFor(tabID);
    let turn = 0;
    const turns: number[] = [];
    for (const message of messages) {
      if (message.role === "user") turn += 1;
      turns.push(turn);
    }
    const entries: HistoryEntry[] = messages.map((message, index) => ({
      entryId: `s1:r0:m${index}:o0`,
      turn: turns[index],
      order: index,
      message,
      refs: [],
    }));
    return {
      entries,
      nextCursor: "",
      hasOlder: false,
      totalTurns: turn,
      startTurn: turns.length > 0 ? Math.min(...turns) : 0,
      endTurn: turns.length > 0 ? Math.max(...turns) : 0,
      stale: false,
      revision: 1,
      revisionKnown: true,
      digest: "digest-1",
    };
  }

  async HistoryContentForTab(): Promise<never> {
    throw new Error("not expected in this suite");
  }
}

// One huge user message: its body alone breaches the store's byte budget —
// the single-point breach from task 196.
const BIG = "x".repeat(3000);
const BIG_MESSAGES: HistoryMessage[] = [{ role: "user", content: BIG }];
const SMALL_MESSAGES: HistoryMessage[] = [{ role: "user", content: "hi" }];
const FULL_PAGE = { turns: 9999 };

async function loadBigThenSwitchAway(exemptMs: number) {
  const backend = new FakeBackend("tab-1");
  const store = new TranscriptStore(backend, {
    historyBodyBudgetBytes: 4096,
    maxResidentSessions: 10,
    evictCooldownMs: 0, // isolate the exemption: no cooldown shielding here
    recentActiveExemptMs: exemptMs,
  });
  // The user is viewing tab-1 while it loads (active pin), as the real UI does.
  store.noteActiveTab("tab-1");
  await store.loadLatest("tab-1", "/s/1.jsonl", FULL_PAGE);
  ok(store.isResident("tab-1", "/s/1.jsonl"), "large session loads resident");
  ok(store.totalBodyBytes() > 4096, "large session alone breaches the byte budget (task 196 precondition)");
  // The user switches away to another tab: tab-1 leaves the active slot now.
  store.noteActiveTab("tab-2", "tab-1");
  return { backend, store };
}

// ── 1. exemption holds: switching back does not pay a full reload ───────────
{
  const { backend, store } = await loadBigThenSwitchAway(30 * 60_000);
  // Load other tabs: budget enforcement runs, but tab-1 is inside the window.
  await store.loadLatest("tab-2", "/s/2.jsonl", FULL_PAGE);
  await store.loadLatest("tab-3", "/s/3.jsonl", FULL_PAGE);
  ok(store.isResident("tab-1", "/s/1.jsonl"), "recently-active large session survives budget enforcement");
  ok(store.sessionStats("tab-1", "/s/1.jsonl")?.recentlyActive === true, "sessionStats exposes the recent-active flag");
  // Switch back: the projection is still resident, no fresh slice fetch happens.
  const slicesBefore = backend.sliceCalls.length;
  const generationBefore = store.sessionStats("tab-1", "/s/1.jsonl")?.generation;
  store.noteActiveTab("tab-1", "tab-2");
  const projection = store.peek("tab-1", "/s/1.jsonl");
  ok(projection !== undefined, "switching back peeks the resident projection (no hydrate veto, no full reload)");
  ok(backend.sliceCalls.length === slicesBefore, "switching back issues no extra slice request");
  ok(store.sessionStats("tab-1", "/s/1.jsonl")?.generation === generationBefore, "generation unchanged: no eviction happened");
}

// ── 2. control: with the exemption off, the breach evicts the large session ─
{
  const { backend, store } = await loadBigThenSwitchAway(0);
  await store.loadLatest("tab-2", "/s/2.jsonl", FULL_PAGE);
  ok(!store.isResident("tab-1", "/s/1.jsonl"), "control (exempt off): the large session is evicted once its cooldown lapses");
  ok(backend.sliceCalls.length >= 2, "control: reload on switch-back would re-fetch from the backend");
}

// ── 3. the window closes: eviction resumes and the budget converges ────────
{
  const { store } = await loadBigThenSwitchAway(5); // 5ms window
  await sleep(20); // age past the window
  await store.loadLatest("tab-2", "/s/2.jsonl", FULL_PAGE);
  ok(!store.isResident("tab-1", "/s/1.jsonl"), "past the window the large session evicts as usual");
  ok(store.totalBodyBytes() <= 4096, "the byte budget converges once the window lapses");
}

// ── 4. never-active tabs are not exempt (regular eviction path intact) ─────
{
  const backend = new FakeBackend("tab-1");
  const store = new TranscriptStore(backend, {
    historyBodyBudgetBytes: 4096,
    maxResidentSessions: 2,
    evictCooldownMs: 0,
    recentActiveExemptMs: 30 * 60_000,
  });
  await store.loadLatest("tab-1", "/s/1.jsonl", FULL_PAGE);
  await store.loadLatest("tab-2", "/s/2.jsonl", FULL_PAGE);
  await store.loadLatest("tab-3", "/s/3.jsonl", FULL_PAGE);
  ok(!store.isResident("tab-1", "/s/1.jsonl"), "a tab that was never active is evicted by the count cap as before");
  ok(store.residentSessionCount() <= 2, "resident count cap holds with the exemption enabled");
}

// ── 5. pinned semantics unchanged: active tab never depends on the window ──
{
  const backend = new FakeBackend("tab-1");
  const store = new TranscriptStore(backend, {
    historyBodyBudgetBytes: 4096,
    maxResidentSessions: 10,
    evictCooldownMs: 0,
    recentActiveExemptMs: 0,
  });
  // Active pin first (as the real UI does): still protected while active.
  store.noteActiveTab("tab-1");
  await store.loadLatest("tab-1", "/s/1.jsonl", FULL_PAGE);
  await store.loadLatest("tab-2", "/s/2.jsonl", FULL_PAGE);
  ok(store.isResident("tab-1", "/s/1.jsonl"), "the active tab stays pinned out of eviction with the exemption off");
}

process.stdout.write(`\n${passed} checks passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
