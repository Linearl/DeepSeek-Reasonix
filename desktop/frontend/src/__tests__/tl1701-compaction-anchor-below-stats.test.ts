// tl1701 验收：压缩进度卡锚在「本轮结果」统计块下方（回合结束面）。
//
// 现场（20261119）：『正在压缩对话…·11秒·估算中』插在本轮结果统计块上方，
// 而压缩实际在回合结束后才触发（tl1669 同步语义）——统计先产出、压缩后跑，
// 渲染顺序却把卡排到了统计上方。根因：partitionTurnItems 把 completion
// notice 无条件提升（hoist）到本回合最后一个 segment 的 outsideItems 尾部
// ——排在压缩卡 fold 之后。修复：当 items 序里 completion notice 先于
// compaction 卡时（统计先产出=回合后压缩的真实时序），在卡入段前把暂存的
// 统计 flush 进自己的 segment 边界——统计渲染在卡上方；items 序里卡先于
// 统计（mid-turn 压力压缩）时保持 hoist——统计确实晚于该 pass，两种时序
// 各自保真。
//
// 钉四件事：
// ① 统计→卡 items 序（回合后压缩主形态）：统计 segment 在卡 segment 之前；
// ② 卡→统计 items 序（mid-turn 压缩）：卡仍在统计之前（时序不回退）；
// ③ 无统计的独立压缩：segment 结构零变化；
// ④ 答案行位置不漂移（outsideItems 既有语义：答案→统计→卡）。

import assert from "node:assert/strict";
import { partitionTurnItems, type Item } from "../lib/transcriptRows";

let passed = 0;
const ok = (cond: boolean, msg: string) => {
  if (!cond) {
    console.error(`FAIL: ${msg}`);
    process.exitCode = 1;
  } else {
    passed += 1;
  }
};

type NoticeItem = Extract<Item, { kind: "notice" }>;
type CompactionItem = Extract<Item, { kind: "compaction" }>;

const answer: Item = { kind: "assistant", id: "a1", text: "final answer", reasoning: "", streaming: false };
const stats: NoticeItem = { kind: "notice", id: "q1", level: "info", variant: "completion", title: "本轮结果", text: "7 turns · 1.2K tokens" };
const card: CompactionItem = { kind: "compaction", id: "c1", pending: true, trigger: "auto", messages: 0, summary: "", archive: "" };

/** 拍平渲染序：segment 顺序内 [processItems..., outsideItems...]。 */
function renderOrder(items: Item[]): string[] {
  const out: string[] = [];
  for (const seg of partitionTurnItems(items)) {
    for (const it of seg.processItems) out.push(it.id);
    for (const it of seg.outsideItems) out.push(it.id);
  }
  return out;
}

// ── ① 统计→卡（回合后压缩主形态）：统计渲染在卡上方 ──────────────────────────
{
  process.stdout.write("case 1: stats precede the card — stats render above (post-turn anchor)\n");
  const order = renderOrder([{ kind: "user", id: "u1", text: "q" }, answer, stats, card]);
  const statsAt = order.indexOf("q1");
  const cardAt = order.indexOf("c1");
  const answerAt = order.indexOf("a1");
  ok(statsAt >= 0 && cardAt > statsAt, `stats (${statsAt}) must render above the card (${cardAt})`);
  ok(answerAt < statsAt, `answer (${answerAt}) stays above the stats (${statsAt})`);
}

// ── ② 卡→统计（mid-turn 压缩）：卡保持在统计上方（时序不回退） ────────────────
{
  process.stdout.write("case 2: card precedes the stats (mid-turn pass) — order unchanged\n");
  const order = renderOrder([{ kind: "user", id: "u1", text: "q" }, answer, card, stats]);
  ok(order.indexOf("c1") < order.indexOf("q1"), `mid-turn card (${order.indexOf("c1")}) stays above the later stats (${order.indexOf("q1")})`);
}

// ── ③ 无统计的独立压缩：零回归 ────────────────────────────────────────────────
{
  process.stdout.write("case 3: no stats — card partition unchanged\n");
  const segs = partitionTurnItems([card]);
  ok(segs.length === 1 && segs[0].processItems.length === 1 && segs[0].outsideItems.length === 0, "lone card stays a single process segment");
}

// ── ④ 完成时就地转终局卡（同一 segment 位置，渲染序同 ①） ─────────────────────
{
  process.stdout.write("case 4: resolved card keeps the anchored position below the stats\n");
  const resolved: CompactionItem = { ...card, pending: false, messages: 5, summary: "- s" };
  const order = renderOrder([{ kind: "user", id: "u1", text: "q" }, answer, stats, resolved]);
  ok(order.indexOf("q1") < order.indexOf("c1"), "resolved card also anchors below the stats");
}

process.stdout.write(`\ntl1701 compaction anchor below stats: ${passed} asserts passed\n`);
if (process.exitCode) process.exit(1);
assert.ok(passed >= 5, `expected >=5 asserts, got ${passed}`);
