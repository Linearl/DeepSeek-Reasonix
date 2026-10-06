// Run: npx tsx src/__tests__/task523-append-only-paging.test.ts
// 任务 523（P1 用户直报）：活跃会话滚动加载历史必失败（history identity changed）。
//
// 根因链（三层同族判据都用「整体身份完全匹配」，在持续增长的会话上必然失效）：
//   后端  cursor 绑定整会话 revision+digest，任何保存（含 append-only）都使
//         cursor stale → 前端被迫整页 reload（修：cursor v2 携带 rewrite epoch，
//         append-only 增长不再 stale —— Go 侧 TestHistorySliceCursor* 已钉）；
//   store sameFingerprint 严格相等，revision 增长即把 prepend 降级为 reload
//         （修：同 epoch 且 revision 单调前进 = 前缀稳定，允许 prepend）；
//   controller 请求时捕获的 meta revision/digest 与响应时完全匹配才放行，
//         活跃会话几乎永不成立（修：revision 单调容忍 + sessionGeneration
//         相等才放行，rebase/换会话仍然拒绝）。
//
// 本文件钉 store 层验收 ③「尾部增长不拒 / 前缀改写才拒」：
//   1. 追加增长（同 epoch，revision 前进）→ prepend，不降级 reload；
//   2. 改写（epoch 前进）→ reload，绝不 prepend 旧前缀；
//   3. stale 标记 → reload（原有保护不删）。

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
    digest: "d7",
    epoch: 2,
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

const TAB = "tab-523";
const SESSION = "/tmp/session-523.jsonl";

console.log("\ntask 523 append-only paging (store layer)");

// 1. 尾部增长不拒：loadLatest 之后会话追加保存（revision 7→9，epoch 不变 2），
//    旧 cursor 继续翻页必须 prepend —— 修复前 sameFingerprint 严格相等把它
//    降级为 reload（活跃会话里 26 次 identity-changed 的 store 侧根因）。
{
  const { backend } = backendOf([
    slice({ entries: [entry("b", 2), entry("a", 1)], nextCursor: "c1", hasOlder: true, totalTurns: 4, revision: 7, epoch: 2 }),
    slice({ entries: [entry("a0", 0)], nextCursor: "", hasOlder: false, totalTurns: 4, revision: 9, epoch: 2, digest: "d9" }),
  ]);
  const store = new TranscriptStore(backend);
  await store.loadLatest(TAB, SESSION);
  const page = await store.loadOlder(TAB, SESSION);
  eq(page?.kind, "prepend", "append-only growth (same epoch, revision advanced) prepends instead of reloading");
  eq(page?.hasOlder, false, "page still reports the store's authoritative hasOlder");
}

// 2. 前缀改写才拒：epoch 前进（compaction/rewind 落盘）→ 旧 cursor 必须降级
//    reload，绝不把改写前的前缀 prepend 进当前会话（验收 ② 不删正确性保护）。
{
  const { backend } = backendOf([
    slice({ entries: [entry("b", 2), entry("a", 1)], nextCursor: "c1", hasOlder: true, totalTurns: 4, revision: 7, epoch: 2 }),
    slice({ entries: [entry("compact", 1)], nextCursor: "", hasOlder: false, totalTurns: 2, revision: 8, epoch: 3, digest: "d8-rewritten" }),
  ]);
  const store = new TranscriptStore(backend);
  await store.loadLatest(TAB, SESSION);
  const page = await store.loadOlder(TAB, SESSION);
  eq(page?.kind, "reload", "a rewrite (epoch advanced) downgrades to a fresh latest-page reload, never a prepend");
}

// 3. stale 标记 → reload：后端显式 stale 的原有保护保持不变。
{
  const { backend } = backendOf([
    slice({ entries: [entry("b", 2), entry("a", 1)], nextCursor: "c1", hasOlder: true, totalTurns: 4 }),
    slice({ stale: true, entries: [], revision: 8, epoch: 3, digest: "d8" }),
    slice({ entries: [entry("fresh", 1)], revision: 8, epoch: 3, digest: "d8" }),
  ]);
  const store = new TranscriptStore(backend);
  await store.loadLatest(TAB, SESSION);
  const page = await store.loadOlder(TAB, SESSION);
  eq(page?.kind, "reload", "an explicit stale marker still reloads the latest page");
}

// 4. revision 回退（同一 epoch 下不可能，防御性）：不做宽容，走严格路径降级。
{
  const { backend } = backendOf([
    slice({ entries: [entry("b", 2), entry("a", 1)], nextCursor: "c1", hasOlder: true, totalTurns: 4, revision: 7, epoch: 2 }),
    slice({ entries: [entry("x", 0)], nextCursor: "", hasOlder: false, revision: 5, epoch: 2, digest: "d5" }),
  ]);
  const store = new TranscriptStore(backend);
  await store.loadLatest(TAB, SESSION);
  const page = await store.loadOlder(TAB, SESSION);
  eq(page?.kind, "reload", "a revision regression under the same epoch is not tolerated (strict fallback)");
}

// 5. Controller 契约（源码级钉）：identity 判据换维度 —— revision 单调容忍 +
//    sessionGeneration 相等 + 文案可理解（验收 ②③④）。
{
  const { readFileSync } = await import("node:fs");
  const { join, dirname } = await import("node:path");
  const root = join(dirname(new URL(import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, "$1")), "..");
  const controller = readFileSync(join(root, "lib", "useController.ts"), "utf8");
  eq(/revisionCompat|revisionGrewOrEqual/.test(controller), true, "controller: revision comparison tolerates monotonic growth");
  eq(/current\.meta\.sessionGeneration === sessionGeneration|current\.meta\?\.sessionGeneration !== sessionGeneration/.test(controller),
    true, "controller: a changed session generation still rejects the page");
  eq(controller.includes("digestMatches(sessionDigest, currentDigest)"), false,
    "controller: the whole-session digest equality gate is gone (it failed on every append)");
  eq(controller.includes('error: "会话内容已变化，请重试"'), true,
    "controller: the rejection surfaces an understandable message, not internal jargon");
}

console.log(`\ntask 523 append-only paging: ${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
