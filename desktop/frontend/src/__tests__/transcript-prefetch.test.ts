// Run: tsx src/__tests__/transcript-prefetch.test.ts
//
// Task 123 prefetch regression tests (2026-09-19 memory/regression follow-up):
// a tab whose page is already resident must NOT be re-fetched — production logs
// showed 100 of the first 114 prefetches re-read a page the store already held,
// each one a redundant backend call plus a full record/projection rebuild that
// landed while the user was switching tabs. The budget guard must also stop a
// speculative read when the store has no headroom, and MRU warming must stay
// bounded.

import { TranscriptStore } from "../lib/transcriptStore";
import { prefetchMruTabs, prefetchTabTranscript, resetPrefetchState } from "../lib/transcriptPrefetch";
import type {
  HistoryContentChunk,
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
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  ok(actual === expected, `${label}${actual === expected ? "" : `: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`}`);
}

function flush() {
  return new Promise((resolve) => setTimeout(resolve, 20));
}

function sampleMessages(turns: number): HistoryMessage[] {
  const messages: HistoryMessage[] = [];
  for (let i = 0; i < turns; i += 1) {
    messages.push({ role: "user", content: `prompt ${i}` });
    messages.push({ role: "assistant", content: `answer ${i}` });
  }
  return messages;
}

class CountingBackend {
  sliceCalls = 0;

  constructor(private readonly messages: HistoryMessage[]) {}

  private slice(req: HistorySliceRequest): HistorySlice {
    const entries: HistoryEntry[] = this.messages.map((message, index) => ({
      entryId: `e${index}`,
      turn: message.role === "user" ? Math.floor(index / 2) + 1 : Math.floor(index / 2) + 1,
      order: index,
      message,
      refs: [],
    }));
    const limit = req.turns ?? entries.length;
    const window = entries.slice(Math.max(0, entries.length - limit * 2));
    return {
      entries: window,
      nextCursor: "",
      hasOlder: false,
      totalTurns: Math.ceil(this.messages.length / 2),
      startTurn: window[0]?.turn ?? 0,
      endTurn: window[window.length - 1]?.turn ?? 0,
      stale: false,
      revision: 1,
      revisionKnown: true,
      digest: "digest-1",
    };
  }

  async HistorySliceForTab(_tabID: string, req: HistorySliceRequest): Promise<HistorySlice> {
    this.sliceCalls += 1;
    return this.slice(req);
  }

  async HistoryContentForTab(): Promise<HistoryContentChunk> {
    return { data: "", done: true, stale: false } as HistoryContentChunk;
  }
}

async function main() {
  process.stdout.write("transcript-prefetch\n");

  // ── resident pages are not re-fetched ─────────────────────────────────────
  resetPrefetchState();
  const backend = new CountingBackend(sampleMessages(20));
  const store = new TranscriptStore(backend, { maxResidentSessions: 8, historyBodyBudgetBytes: 64 << 20, evictCooldownMs: 0 });
  const candidate = { tabId: "tab-a", sessionPath: "C:/sessions/a.jsonl" };

  prefetchTabTranscript(candidate, { store, force: true });
  await flush();
  eq(backend.sliceCalls, 1, "the first prefetch fetches the page once");
  ok(store.isResident(candidate.tabId, candidate.sessionPath), "the page is resident after the first prefetch");

  prefetchTabTranscript(candidate, { store, force: true });
  await flush();
  eq(backend.sliceCalls, 1, "a second prefetch skips the resident page (no backend call)");

  prefetchTabTranscript(candidate, { store, force: true });
  await flush();
  eq(backend.sliceCalls, 1, "repeated prefetches stay free while the page is resident");

  // ── a different tab still warms normally ──────────────────────────────────
  prefetchTabTranscript({ tabId: "tab-b", sessionPath: "C:/sessions/b.jsonl" }, { store, force: true });
  await flush();
  eq(backend.sliceCalls, 2, "a tab that is not resident is still warmed");

  // ── budget guard refuses a speculative read with no headroom ──────────────
  const tightBackend = new CountingBackend(sampleMessages(4));
  const tightStore = new TranscriptStore(tightBackend, {
    maxResidentSessions: 8,
    // Below the prefetch headroom floor (8 MiB), so every speculative read is
    // refused before it touches the backend.
    historyBodyBudgetBytes: 4 << 20,
    evictCooldownMs: 0,
  });
  prefetchTabTranscript({ tabId: "tab-c", sessionPath: "C:/sessions/c.jsonl" }, { store: tightStore, force: true });
  await flush();
  eq(tightBackend.sliceCalls, 0, "prefetch refuses when the body budget has no headroom");

  // ── MRU warming stays bounded per switch ──────────────────────────────────
  resetPrefetchState();
  const mruBackend = new CountingBackend(sampleMessages(12));
  const mruStore = new TranscriptStore(mruBackend, { maxResidentSessions: 8, historyBodyBudgetBytes: 64 << 20, evictCooldownMs: 0 });
  prefetchMruTabs(
    [
      { tabId: "tab-m1", sessionPath: "C:/sessions/m1.jsonl" },
      { tabId: "tab-m2", sessionPath: "C:/sessions/m2.jsonl" },
      { tabId: "tab-m3", sessionPath: "C:/sessions/m3.jsonl" },
    ],
    2,
    { store: mruStore, force: true },
  );
  await flush();
  eq(mruBackend.sliceCalls, 2, "MRU prefetch warms at most two tabs per switch");

  process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
  if (failed > 0) process.exit(1);
}

void main();
