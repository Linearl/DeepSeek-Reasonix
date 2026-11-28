// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1858-compaction-card-below-actions.test.ts
//
// tl1858 验收（压缩进度卡移到回合尾部/操作按钮行之后）：
//
// 现场（20261128 包121 装机截图）：压缩进行中「正在压缩对话…7秒·估算中…」卡
// 插在回合底部复制/工具按钮行之前，把按钮行压到下方——压缩 UI（tl1701 flush
// 出的 compaction-only 段）的行在 buildTranscriptRowBlocks 里先于 turn-actions
// 行构建。修复：compaction-only 段的行（fold 头 + 展开体）整体挪到
// turn-actions 之后（回合块尾），混合段（mid-turn 压力压缩，压缩与工具同段）
// 保持原位不回退 tl1701 时序保真。
//
// 钉四件事（任务书验收①②③+回归）：
// ① 压缩进行时 turn-actions 行（复制/工具按钮行）在 DOM 序先于压缩段行；
// ② 压缩段行是回合块的最后一批行（回合尾部锚定）；
// ③ 完成/失败态（同段就地翻状态）位置断言与进行态一致；
// ④ 混合段回归：mid-turn 压缩卡仍在 turn-actions 之前（fold 内原位）。
// 附加：fold 关闭时只有头行挪位（体行不构建），仍在按钮行之后。

import assert from "node:assert/strict";
import {
  buildTranscriptRows,
  buildTurnModels,
  EMPTY_FOLDS,
  foldMapWithToggle,
  type FoldMap,
  type TranscriptRow,
} from "../lib/transcriptRows";
import type { Item } from "../lib/useController";

let passed = 0;
const ok = (cond: boolean, msg: string) => {
  if (!cond) {
    console.error(`FAIL: ${msg}`);
    process.exitCode = 1;
  } else {
    passed += 1;
  }
};

type CompactionItem = Extract<Item, { kind: "compaction" }>;

const user: Item = { kind: "user", id: "u1", text: "q" };
const answer: Item = { kind: "assistant", id: "a1", text: "final answer", reasoning: "", streaming: false };
const stats = { kind: "notice", id: "q1", level: "info", variant: "completion", title: "本轮结果", text: "7 turns · 1.2K tokens" } as Extract<Item, { kind: "notice" }>;
const pendingCard: CompactionItem = { kind: "compaction", id: "c1", pending: true, trigger: "auto", messages: 0, summary: "", archive: "" };
const resolvedCard: CompactionItem = { ...pendingCard, pending: false, messages: 5, summary: "- goal: ok" };
const failedCard: CompactionItem = { ...pendingCard, pending: false, failed: true, attempt: 1, reason: "summarizer request failed" };

const turnOf = new Map([["u1", 0]]);
const rowOptions = (folds: FoldMap) => ({
  folds,
  foldPreference: "auto" as const,
  hasOlderHistory: false,
  turnForUser: (item: Extract<Item, { kind: "user" }>) => turnOf.get(item.id),
});

/** 打开压缩卡所在段的 fold（返回新 FoldMap）。 */
function foldsOpeningCompaction(items: Item[]): FoldMap {
  const model = buildTurnModels(items).find((m) => m.user?.id === "u1");
  const segment = model?.segments.find((seg) => seg.processItems.some((it) => it.kind === "compaction"));
  if (!segment) throw new Error("fixture must contain a compaction segment");
  return foldMapWithToggle(EMPTY_FOLDS, segment.key, false);
}

/** 块内行序（拍平后只取本回合行）。 */
function rowOrder(items: Item[], folds: FoldMap): string[] {
  return buildTranscriptRows(buildTurnModels(items), rowOptions(folds)).map((row: TranscriptRow) => row.key);
}

// ── ①② 压缩进行中：按钮行先于压缩段行，压缩段行殿后 ──────────────────────────
{
  process.stdout.write("case 1+2: pending card — turn-actions precede the compaction segment rows; segment rows close the turn\n");
  const items: Item[] = [user, answer, stats, pendingCard];
  const order = rowOrder(items, foldsOpeningCompaction(items));
  const ta = order.indexOf("ta:u1");
  const card = order.indexOf("c:c1");
  const header = order.findIndex((key) => key.startsWith("ph:"));
  ok(ta >= 0, "turn-actions row mounted");
  ok(card >= 0, "pending compaction card row built (fold open)");
  ok(header >= 0, "compaction segment header row present");
  ok(ta < header && ta < card, `turn-actions (${ta}) must precede the compaction header (${header}) and card (${card})`);
  ok(card === order.length - 1, `compaction card (${card}) must be the last row of the turn block (len ${order.length})`);
  ok(order.indexOf("a:a1") < ta, "answer stays above the actions row");
  ok(order.indexOf("n:q1") < ta, "completion stats stay above the actions row");
}

// ── ③a 完成态：同位置（按钮行之后、块尾） ────────────────────────────────────
{
  process.stdout.write("case 3a: resolved card keeps the anchored position\n");
  const items: Item[] = [user, answer, stats, resolvedCard];
  const order = rowOrder(items, foldsOpeningCompaction(items));
  const ta = order.indexOf("ta:u1");
  const card = order.indexOf("c:c1");
  ok(ta >= 0 && card > ta, `resolved card (${card}) stays below the actions row (${ta})`);
  ok(card === order.length - 1, "resolved card closes the turn block");
}

// ── ③b 失败态：同位置 ────────────────────────────────────────────────────────
{
  process.stdout.write("case 3b: failed card keeps the anchored position\n");
  const items: Item[] = [user, answer, stats, failedCard];
  const order = rowOrder(items, foldsOpeningCompaction(items));
  const ta = order.indexOf("ta:u1");
  const card = order.indexOf("c:c1");
  ok(ta >= 0 && card > ta, `failed card (${card}) stays below the actions row (${ta})`);
  ok(card === order.length - 1, "failed card closes the turn block");
}

// ── ③c fold 关闭：只有头行挪位，仍在按钮行之后 ───────────────────────────────
{
  process.stdout.write("case 3c: closed fold — header alone moves below the actions row\n");
  const items: Item[] = [user, answer, stats, pendingCard];
  const order = rowOrder(items, EMPTY_FOLDS);
  const ta = order.indexOf("ta:u1");
  const header = order.findIndex((key) => key.startsWith("ph:"));
  ok(ta >= 0 && header > ta, `closed-fold header (${header}) stays below the actions row (${ta})`);
  ok(!order.includes("c:c1"), "closed fold builds no compaction body row");
  ok(header === order.length - 1, "header closes the turn block");
}

// ── ④ 混合段回归：mid-turn 压缩卡保持在 turn-actions 之前（fold 内原位） ──────
{
  process.stdout.write("case 4: mixed segment (tool + mid-turn compaction) keeps the in-fold position above turn-actions\n");
  const tool: Item = { kind: "tool", id: "t1", name: "read_file", args: "{}", readOnly: true, status: "done" };
  const items: Item[] = [user, tool, pendingCard, answer, stats];
  const order = rowOrder(items, foldsOpeningCompaction(items));
  const ta = order.indexOf("ta:u1");
  const card = order.indexOf("c:c1");
  ok(ta >= 0 && card >= 0, "both rows present");
  ok(card < ta, `mid-turn card (${card}) must stay above the actions row (${ta}) — tl1701 chronology preserved`);
  ok(order.indexOf("t:t1") < card, "tool card precedes the compaction card inside the fold");
}

// ── ⑤ prelude/活跃回合（无 turn-actions 行）：压缩段行落块尾，不前插 ──────────
{
  process.stdout.write("case 5: active turn without turn-actions — compaction rows still close the block\n");
  const active: Item[] = [user, answer, stats, { ...pendingCard }];
  const folds = foldsOpeningCompaction(active);
  const rows = buildTranscriptRows(buildTurnModels(active, undefined, true), rowOptions(folds));
  const order = rows.map((row) => row.key);
  ok(!order.includes("ta:u1"), "active turn mounts no turn-actions row");
  ok(order.indexOf("c:c1") === order.length - 1, "compaction card closes the block without the actions row");
}

process.stdout.write(`\ntl1858 compaction card below actions: ${passed} asserts passed\n`);
if (process.exitCode) process.exit(1);
assert.ok(passed >= 12, `expected >=12 asserts, got ${passed}`);
