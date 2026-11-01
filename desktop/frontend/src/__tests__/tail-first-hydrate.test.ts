// Run: npx tsx src/__tests__/tail-first-hydrate.test.ts
//
// tl1332 (tl1083 §5.1 最小档): resident 核鲜失败的大会话切 tab 降级
// 「尾部首绘 + 后台补史」，替换全量重载。验收锚 = tl1083 §6.4/§6.5：
//   ① 大会话（messages>N 或 bytes>M）首拉页深降到 12-turn 尾窗页，
//      hydrate_done 先行，整页由后台分片补史（每片独立小任务）；
//   ② 小会话两条腿都不中 → 路径零改动（快路径命中不变）；
//   ③ 补史片序保证 prepend 后前缀即整页原序（无乱序穿插）；
//   ④ 补史期间占用 loadOlder 并发闸，且孤儿退出必清闸。

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import {
  HISTORY_FILL_CHUNK_ITEMS,
  HISTORY_TAIL_FIRST_TURNS,
  TAIL_FIRST_PAGE_MIN_BYTES,
  TAIL_FIRST_PAGE_MIN_RECORDS,
  fillChunkStartTurn,
  planHistoryFillChunks,
  prependRestAfterMerge,
  residentPageHeavy,
} from "../lib/hydrateHistoryApply";

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

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const controller = readFileSync(join(root, "lib/useController.ts"), "utf8");

console.log("\ntail-first hydrate degradation (tl1332 / tl1083 §5.1 最小档)");

// ── 1. 门槛：仅大会话生效，小会话/无驻留页不进降级 ────────────────────────────
{
  ok(!residentPageHeavy(undefined), "no resident page (LRU evicted, cold open) never degrades");
  ok(!residentPageHeavy({ records: 0, bodyBytes: 0 }), "an empty resident page is not heavy");
  ok(!residentPageHeavy({ records: TAIL_FIRST_PAGE_MIN_RECORDS - 1, bodyBytes: 0 }), "records just under N stays on the legacy path");
  ok(residentPageHeavy({ records: TAIL_FIRST_PAGE_MIN_RECORDS, bodyBytes: 0 }), "records at N degrades (messages>N leg)");
  ok(!residentPageHeavy({ records: 0, bodyBytes: TAIL_FIRST_PAGE_MIN_BYTES - 1 }), "bytes just under M stays on the legacy path");
  ok(residentPageHeavy({ records: 0, bodyBytes: TAIL_FIRST_PAGE_MIN_BYTES }), "bytes at M degrades (bytes>M leg)");
  // 健康小会话量级：几十条消息、百 KiB 体量——双腿都远低于门槛。
  ok(!residentPageHeavy({ records: 80, bodyBytes: 200_000 }), "a healthy small session keeps the byte-identical legacy path");
}

// ── 2. 补史分片：连续覆盖、余量片、空页安全 ─────────────────────────────────
{
  ok(planHistoryFillChunks(0, HISTORY_FILL_CHUNK_ITEMS).length === 0, "an empty page plans no chunks");
  ok(planHistoryFillChunks(-1, 600).length === 0, "a negative length plans no chunks");
  ok(planHistoryFillChunks(5, 0).length === 0, "a non-positive chunk size plans no chunks");
  const exact = planHistoryFillChunks(1200, 600);
  ok(exact.length === 2 && exact[0][0] === 0 && exact[0][1] === 600 && exact[1][0] === 600 && exact[1][1] === 1200, "an exact multiple splits into contiguous ranges");
  const rest = planHistoryFillChunks(1450, 600);
  ok(rest.length === 3 && rest[2][0] === 1200 && rest[2][1] === 1450, "a remainder chunk is allowed to be short");
  const cover = planHistoryFillChunks(1450, 600);
  ok(cover[0][0] === 0 && cover.every(([, to], i) => i === cover.length - 1 || to === cover[i + 1][0]), "chunks tile [0, length) with no gap or overlap");
}

// ── 3. 分片 startTurn 锚 ────────────────────────────────────────────────────
{
  const items = [{ historyTurn: 4 }, {}, { historyTurn: 5 }, {}, {}];
  ok(fillChunkStartTurn(items, 0, 5, 1) === 4, "the first anchored row in a chunk is its start turn");
  ok(fillChunkStartTurn([{}, {}, { historyTurn: 7 }], 0, 2, 3) === 3, "an unanchored chunk falls back to the page start");
  ok(fillChunkStartTurn([], 0, 3, 9) === 9, "an empty range is safe and falls back");
}

// ── 4. 片序行为验证：新→旧倒序 prepend，前缀终态=整页原序 ─────────────────────
{
  // 模拟 reducer 的 prepend 合并（prependRestAfterMerge 与 history_prepend
  // 的 drop-by-id 语义同源）：尾窗页已画 2 行，整页 6 行分 3 片倒序补史。
  const fullPage = ["p0", "p1", "p2", "p3", "p4", "p5"].map((id) => ({ id }));
  const tailPainted = [{ id: "p4" }, { id: "p5" }];
  const chunks = planHistoryFillChunks(fullPage.length, 2);
  let surface: Array<{ id: string }> = [...tailPainted];
  for (let i = chunks.length - 1; i >= 0; i -= 1) {
    const [from, to] = chunks[i];
    const chunk = fullPage.slice(from, to);
    // history_prepend 语义：页行在前，既有行剔除与页行同 id 者后拼接。
    surface = [...chunk, ...prependRestAfterMerge(surface, chunk, [])];
  }
  ok(surface.length === fullPage.length, "the filled prefix carries every page row exactly once");
  ok(surface.every((item, i) => item.id === fullPage[i].id), "reverse-order chunk prepends rebuild the page in its original order (no inversion)");
}

// ── 5. 源码合同：两阶段接线 ─────────────────────────────────────────────────
ok(
  /turns: tailFirstFill \? HISTORY_TAIL_FIRST_TURNS : HISTORY_PAGE_TURNS/.test(controller),
  "the first fetch depth is the 12-turn tail window only for degraded large sessions",
);
ok(
  /tailFirstFill = residentPageHeavy\(getTranscriptStore\(\)\.sessionStats\(tabId, sessionPath\)\)/.test(controller),
  "the gate reads the resident page's records/bodyBytes (messages>N or bytes>M)",
);
ok(
  /turns: HISTORY_PAGE_TURNS,\s*\n\s*preferResident: false,\s*\n\s*expectedRevision: fillExpectedRevision,/.test(controller),
  "the background fill re-fetches the full-depth page with the fresh-meta binding",
);
ok(
  /type: "history_prepend",\s*\n\s*items: pageItems\.slice\(from, to\),/.test(controller),
  "fill chunks apply through history_prepend (bounded dispatches, prefix-only)",
);
ok(
  /reportStageTiming\(tabId, `\$\{reason\}:fill:apply`/.test(controller) && /reportStageTiming\(tabId, `\$\{reason\}:fill:total`/.test(controller),
  "per-chunk and total fill stages are timed (the §6.4 frontend-monitor evidence)",
);
ok(
  /dispatchTo\(tabId, \{ type: "history_older_start" \}\);/.test(controller),
  "the fill arms the loadOlder concurrency gate (no interleaved user paging)",
);
ok(
  /finally \{\s*\n\s*clearOlderGateIfOrphaned\(\);/.test(controller),
  "the fill's finally backstop clears the gate on orphaned exits",
);
ok(
  /!skipHistory && !tailFirstFill &&\s*\n\s*!foregroundTurnActive\(\) && !historyFingerprintMatchesMeta/.test(controller),
  "the background reconcile yields to the fill (one writer per switch)",
);
ok(
  /await Promise\.all\(\[backgroundReconcile, backgroundHistoryFill\]\);/.test(controller),
  "the in-flight hydrate promise joins the fill",
);
ok(
  /\? \(tailFirstFill \? "resident-stale-tail-first" : "resident-stale"\)/.test(controller),
  "the degraded decision is separately named in the hydrate decision log",
);
ok(
  !/LoadSession\(/.test(readFileSync(join(root, "lib/hydrateHistoryApply.ts"), "utf8")),
  "the degradation introduces no full-session decode path (frontend only)",
);
ok(
  HISTORY_TAIL_FIRST_TURNS > 0 && HISTORY_TAIL_FIRST_TURNS < 60,
  "the tail first-paint window is a strict subset of the full page depth",
);

assert.ok(true);
console.log(`\n${passed} passed, ${failed} failed`);
