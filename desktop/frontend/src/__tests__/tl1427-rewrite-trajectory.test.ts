// Run: npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1427-rewrite-trajectory.test.ts
//
// tl1427 ②轨迹可见 acceptance harness(纯投影层,无 DOM):
//  ① reducer("draft_rewrite_trace") 落一条 trajectoryOnly notice:
//     code="promptEnhance"(对齐 zcode modelTrajectory.source.promptEnhance)、
//     level=info、带 createdAt,不打断 running/turnActive;
//  ② 记录不含草稿正文与改写结果——文案只有 outcome 与耗时;
//  ③ buildTrajectoryLedger 消费该 item:轨迹台账出现 title=promptEnhance 的
//     notice 记录(preview=文案),且记录不含草稿正文;
//  ④ buildTurnModels(聊天行投影唯一 items 入口)跳过 trajectoryOnly notice:
//     聊天流零渲染——「不进 transcript」的展示侧纪律;
//  ⑤ cancelled / failed outcome 各落对应文案键;
//  ⑥ 普通 notice(无 trajectoryOnly)行为不变(投影回归钉)。

import { en } from "../locales/en";
import { reducer, initialState } from "../lib/useController";
import type { Item } from "../lib/useController";
import { buildTrajectoryLedger } from "../lib/trajectoryLedger";
import { buildTurnModels } from "../lib/transcriptRows";

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
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

const DRAFT = "帮我改写这段需求描述,把它弄清楚";
const DONE_TEXT = en["composer.rewriteTrace.done"].replace("{n}", "1.2");

function traceItem(id: string, text: string): Item {
  return { kind: "notice", id, level: "info", code: "promptEnhance", trajectoryOnly: true, text, createdAt: 42 };
}

// ── ① reducer 落轨迹 notice:code/trajectoryOnly/createdAt/不打断 running ──
{
  const running: typeof initialState = { ...initialState, running: true, turnActive: true };
  const next = reducer(running, { type: "draft_rewrite_trace", id: "drt-1", outcome: "done", elapsedMs: 1200, createdAt: 42 });
  const last = next.items[next.items.length - 1];
  ok(last?.kind === "notice", "reducer appends a notice item for draft_rewrite_trace");
  if (last?.kind === "notice") {
    eq(last.code, "promptEnhance", "the trace notice carries code=promptEnhance (zcode modelTrajectory.source.promptEnhance alignment)");
    eq(last.trajectoryOnly, true, "the trace notice is trajectoryOnly");
    eq(last.createdAt, 42, "the trace notice carries createdAt");
    eq(last.text, DONE_TEXT, "done text fills the elapsed seconds");
  }
  eq(next.running, true, "the trace never interrupts running");
  eq(next.turnActive, true, "the trace never interrupts turnActive");
  eq(next.seq, running.seq + 1, "the trace bumps seq");
}

// ── ②⑤ 文案不含草稿正文;cancelled/failed 各走各的键 ──────────────────
{
  const next = reducer(initialState, { type: "draft_rewrite_trace", id: "drt-2", outcome: "cancelled", elapsedMs: 300, createdAt: 43 });
  const last = next.items[next.items.length - 1];
  ok(last?.kind === "notice" && last.text === en["composer.rewriteTrace.cancelled"],
    "cancelled text uses the dedicated locale key");
  ok(last?.kind === "notice" && !last.text.includes(DRAFT), "no draft body leaks into the cancelled trace text");
  const next2 = reducer(initialState, { type: "draft_rewrite_trace", id: "drt-3", outcome: "failed", elapsedMs: 50, createdAt: 44 });
  const last2 = next2.items[next2.items.length - 1];
  ok(last2?.kind === "notice" && last2.text === en["composer.rewriteTrace.failed"],
    "failed text uses the dedicated locale key (silent degrade)");
}

// ── ③ 轨迹台账消费:notice 记录 title=promptEnhance,不含草稿正文 ────────
{
  const items: Item[] = [
    { kind: "user", id: "u1", text: DRAFT },
    traceItem("drt-1", DONE_TEXT),
    { kind: "assistant", id: "a1", text: "改好了", reasoning: "", streaming: false },
  ];
  const ledger = buildTrajectoryLedger(items);
  const traceRecords = ledger.records.filter((r) => r.kind === "notice" && r.title === "promptEnhance");
  eq(traceRecords.length, 1, "the trajectory ledger shows exactly one promptEnhance record");
  const record = traceRecords[0];
  ok(record != null, "the promptEnhance record exists");
  if (record) {
    eq(record.preview, DONE_TEXT, "the record preview is the localized outcome line");
    ok(!record.preview.includes(DRAFT) && !record.preview.includes("改好了"),
      "the record carries no draft body and no rewrite result");
    eq(record.turn, 1, "the record lands in the turn it happened in");
    // 轨迹投影的时间锚语义（704 既定）：notice 继承最近一条带时间记录；本用例
    // 的 user 无 createdAt → 无锚（at=undefined），createdAt 留作记录数据。
    eq(record.at, undefined, "the record follows the existing notice time-anchor semantics (inherit, never invent)");
  }
}

// ── ④ 聊天行投影跳过 trajectoryOnly notice(不进 transcript 展示侧) ─────
{
  const items: Item[] = [
    { kind: "user", id: "u1", text: DRAFT },
    traceItem("drt-1", DONE_TEXT),
    { kind: "assistant", id: "a1", text: "改好了", reasoning: "", streaming: false },
  ];
  const models = buildTurnModels(items);
  let leaked = 0;
  let turns = 0;
  for (const model of models) {
    if (model.user) turns += 1;
    for (const it of model.turnItems) {
      if (it.kind === "notice" && it.trajectoryOnly) leaked += 1;
    }
    for (const segment of model.segments) {
      for (const it of segment.processItems) {
        if (it.kind === "notice" && it.trajectoryOnly) leaked += 1;
      }
      for (const it of segment.outsideItems) {
        if (it.kind === "notice" && it.trajectoryOnly) leaked += 1;
      }
    }
  }
  eq(turns, 1, "the user turn still partitions normally around the trace");
  eq(leaked, 0, "buildTurnModels (the chat row projection entry) drops the trajectoryOnly notice entirely");
}

// ── ⑥ 普通 notice 投影行为不变(回归钉) ─────────────────────────────
{
  const items: Item[] = [
    { kind: "user", id: "u1", text: DRAFT },
    { kind: "notice", id: "n1", level: "warn", text: "普通告警" },
  ];
  const models = buildTurnModels(items);
  let normalNotice = 0;
  for (const model of models) {
    for (const segment of model.segments) {
      for (const it of segment.processItems) {
        if (it.kind === "notice" && it.id === "n1") normalNotice += 1;
      }
      for (const it of segment.outsideItems) {
        if (it.kind === "notice" && it.id === "n1") normalNotice += 1;
      }
    }
  }
  eq(normalNotice, 1, "a plain notice (no trajectoryOnly) still renders in the chat projection (warn lands in outsideItems)");
  const ledger = buildTrajectoryLedger(items);
  ok(ledger.records.some((r) => r.kind === "notice" && r.title === "notice_warn"),
    "a plain notice still shows in the trajectory ledger");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
