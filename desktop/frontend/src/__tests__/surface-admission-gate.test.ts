// tl1802: surfaceAdmission 单一准入闸的判定钉。
// 覆盖四组：
//   A. 判定语义等价钉——每个迁移判定与基线散装实现逐字节同判（657 水位线 /
//      K4 revisionBand / K7 inboxItemId / 1739r2 turn-id / 1712 折叠判定）。
//   B. W4×W4 空白格红转绿——无 id 同文气泡折叠（P17 重注入恢复语义不受伤）。
//   C. W5×W5 空白格红转绿——K8 submissionId 幂等（同 submission 双发拒、
//      不同 submission 同文连续打字放行，tl1712 族钉语义保持）。
//   D. 行身份唯一断言门禁（acceptance 3）——装配层行 id 集合派生只允许存在
//      于 surfaceAdmission 单一入口，新增即红。
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { initialState, reducer } from "../lib/useController";
import type { Item, WireEvent } from "../lib/types";
import {
  hasRenderedTurnRows,
  historyRevisionIsOlder,
  isHostAnnouncedTwin,
  judgeEventSeq,
  judgeGuidanceBubble,
  judgeHistoryRevision,
  judgeOptimisticUser,
  judgeStreamSegments,
  pageUserTextSet,
  replayDuplicatesRenderedTurn,
  surfaceRowFacts,
} from "../lib/surfaceAdmission";

function ok(cond: unknown, msg: string): void {
  if (!cond) throw new Error(`FAIL: ${msg}`);
}

function eq(actual: unknown, expected: unknown, msg: string): void {
  if (actual !== expected) throw new Error(`FAIL: ${msg}: got ${String(actual)}, want ${String(expected)}`);
}

const ev = (event: Omit<WireEvent, "seq">, seq?: number): { type: "event"; e: WireEvent } => ({
  type: "event",
  e: { ...event, seq } as WireEvent,
});

const userRows = (s: { items: Item[] }) => s.items.filter((item) => item.kind === "user");
const noticeRows = (s: { items: Item[] }) => s.items.filter((item) => item.kind === "notice");

// ── A1. K3 水位线判定与基线逐字节同判 ─────────────────────────────────────────
{
  let s = reducer(initialState, ev({ kind: "turn_started", turnId: "t1", status: "in_progress" }, 1));
  eq(s.appliedEventSeq, 1, "合法 seq 入列推进水位线（657 基线语义）");
  eq(reducer(s, ev({ kind: "notice", text: "x" }, 1)), s, "同 seq 重放被闸拒收（no-op 引用相等）");
  const seqZero = reducer(s, ev({ kind: "notice", text: "y" }, 0));
  eq(seqZero.items.length, s.items.length + 1, "seq=0 照基线放行（非正 seq 不走水位线裁决）");
  eq(seqZero.appliedEventSeq, 1, "seq=0 不推进水位线（基线语义）");
}

// ── A2. judgeStreamSegments 与基线过滤逐段同判 ────────────────────────────────
{
  let s = reducer(initialState, ev({ kind: "turn_started", turnId: "t2", status: "in_progress" }, 1));
  s = reducer(s, { type: "stream_batch", segments: [{ kind: "text", delta: "A", seq: 2 }] });
  const fresh = judgeStreamSegments(s, [
    { kind: "text", delta: "A", seq: 2 }, // 已折入（拒）
    { kind: "text", delta: "B", seq: 3 }, // 新段（放行）
    { kind: "reasoning", delta: "C" }, // seq-less 旁路（放行）
    { kind: "text", delta: "D", seq: 0 }, // 非正 seq 旁路（放行，基线语义）
  ]);
  eq(fresh.length, 3, "混合批只放行新段/seq-less 段");
  eq(fresh[0]?.delta, "B", "重放段被滤除");
}

// ── A3. K4 revisionBand 判定与基线同判 ────────────────────────────────────────
{
  const s = { ...initialState, historyRevision: 5 };
  ok(!judgeHistoryRevision(s, 4).admit, "旧 revision 拒收");
  ok(judgeHistoryRevision(s, 5).admit, "同 revision 放行");
  ok(judgeHistoryRevision(s, 6).admit, "新 revision 放行");
  ok(judgeHistoryRevision(initialState, 4).admit, "无驻留 revision 时放行");
  ok(historyRevisionIsOlder(5, 4) && !historyRevisionIsOlder(5, 5) && !historyRevisionIsOlder(undefined, 4), "historyRevisionIsOlder 内迁语义不变");
}

// ── A4. 1739r2 turn-id 去重（内迁后 O(1) 查表与基线扫描同判）──────────────────
{
  const items: Item[] = [
    { kind: "user", id: "u0", text: "q" },
    { kind: "assistant", id: "a:turn-9:0", text: "answer", reasoning: "" },
    { kind: "assistant", id: "a:turn-9:1", text: "answer2", reasoning: "" },
    { kind: "assistant", id: "a42", text: "no-turn-id segment", reasoning: "" },
  ];
  ok(hasRenderedTurnRows(items, "turn-9"), "a:<turnId>:* 行在表 = 已渲染");
  ok(!hasRenderedTurnRows(items, "turn-8"), "无该 turn 行 = 未渲染");
  ok(!hasRenderedTurnRows(items, ""), "空 turnId 恒未渲染（基线语义）");
  ok(!hasRenderedTurnRows(items, "42"), "a<seq> 无世代命名不进 Map（基线前缀扫描不含 `a42`）");
  // 与基线扫描实现的等价性对照（前缀扫描为基线事实源）
  const baselineScan = (list: Item[], turnId: string) => list.some((item) => item.id.startsWith(`a:${turnId}:`));
  for (const t of ["turn-9", "turn-8", "", "42"]) eq(hasRenderedTurnRows(items, t), baselineScan(items, t), `等价对照 turn=${t || "<empty>"}`);
  const s1 = { items, activeTurnId: "turn-1" } as Pick<{ items: Item[]; activeTurnId?: string }, "items" | "activeTurnId">;
  ok(replayDuplicatesRenderedTurn(s1, "turn-9"), "非活跃 turn 已渲染 = 重放双写（拒）");
  const s2 = { items, activeTurnId: "turn-9" } as typeof s1;
  ok(!replayDuplicatesRenderedTurn(s2, "turn-9"), "活跃 turn 回填放行（tl1444/1739 主形态）");
  ok(!replayDuplicatesRenderedTurn(undefined, "turn-9"), "无状态放行（基线防御）");
}

// ── A5. K7 inboxItemId 幂等（notice/user 两类分开，与基线扫描同判）────────────
{
  let s = reducer(initialState, ev({ kind: "steer", text: "引导词", itemId: "ib-1" }));
  eq(noticeRows(s).length, 1, "consume-time steer 气泡照常渲染");
  s = reducer(s, ev({ kind: "steer", text: "引导词", itemId: "ib-1" }));
  eq(noticeRows(s).length, 1, "同 itemId steer 重放不双绘（K7，任务258 基线语义）");
  s = reducer(s, ev({ kind: "user_input", text: "同信件", itemId: "ib-1" }));
  eq(userRows(s).length, 1, "notice 与 user 行分集合：同 id 可先后落气泡与用户行（723 配对语义）");
  s = reducer(s, ev({ kind: "user_input", text: "同信件", itemId: "ib-1" }));
  eq(userRows(s).length, 1, "同 itemId user_input 重放不双行（580 幂等）");
}

// ── A6. 1712 折叠判定内迁同判 ────────────────────────────────────────────────
{
  const page: Item[] = [{ kind: "user", id: "h1-0", text: "resume" }];
  const texts = pageUserTextSet(page);
  eq(texts.size, 1, "页面 user 同文集合派生");
  const announce: Item = { kind: "user", id: "u3", text: "resume", hostAnnounced: true };
  const typed: Item = { kind: "user", id: "u4", text: "resume" };
  ok(isHostAnnouncedTwin(announce, texts), "host-announced 同文行 = 折叠");
  ok(!isHostAnnouncedTwin(typed, texts), "composer 行无标记不折叠（打字重复合法）");
  ok(!isHostAnnouncedTwin(announce, pageUserTextSet([])), "页面无同文行不折叠");
}

// ── B. W4×W4 空白格红转绿：无 id 气泡尾邻孪生折叠；P17 恢复语义不受伤 ─────────
{
  // 红转绿：基线（无判定）同文无 id 气泡连发两遍渲染两条；闸后折叠为一条
  // （双发/重注入窗口内气泡必然连续落位=尾邻孪生）。
  let s = reducer(initialState, { type: "guidance_bubble", text: "重启后继续" });
  s = reducer(s, { type: "guidance_bubble", text: "重启后继续" });
  eq(noticeRows(s).length, 1, "无 id 同文气泡尾邻折叠为一条（W4 空白格收口，基线渲染两条）");
  s = reducer(s, { type: "guidance_bubble", text: "另一条引导" });
  eq(noticeRows(s).length, 2, "不同文气泡照常渲染");
  // 尾邻语义：同文气泡被其他行隔开后不再折叠（漏检方向=现状放行，无漏渲染风险）。
  s = reducer(s, { type: "local_notice", level: "info", text: "间隔行" });
  s = reducer(s, { type: "guidance_bubble", text: "重启后继续" });
  eq(noticeRows(s).length, 4, "被隔开的同文气泡放行（尾邻语义：只裁连续双发）");
  // P17 表面重建重注入：replace 擦除气泡后尾行非同文气泡 → 重注入放行（恢复语义）。
  const replaced = reducer(s, { type: "history_replace", items: [], startTurn: 0, totalTurns: 0, hasOlder: false, revision: 9 });
  eq(noticeRows(replaced).length, 0, "replace 擦除气泡（P17 存在的理由，基线语义）");
  const reinjected = reducer(replaced, { type: "guidance_bubble", text: "重启后继续" });
  eq(noticeRows(reinjected).length, 1, "P17 重注入同文气泡放行（表面重建即恢复，不被 W4 门误伤）");
  // 有 id 气泡不受同文折叠影响（K7 幂等独立裁决）
  let s2 = reducer(initialState, { type: "guidance_bubble", text: "带 id 引导", inboxItemId: "ib-9" });
  s2 = reducer(s2, { type: "guidance_bubble", text: "带 id 引导", inboxItemId: "ib-10" });
  eq(noticeRows(s2).length, 2, "同文不同 id 气泡都渲染（id 是不同消息；同文折叠只裁无 id 形态）");
}

// ── C. W5×W5 空白格红转绿：K8 submissionId 幂等 ──────────────────────────────
{
  // 红转绿：同一 submission 双发（双击/通道重放）基线渲染两条同文行；闸后一条。
  let s = reducer(initialState, { type: "user", seq: 0, text: "继续", submissionId: "sub-dup" });
  s = reducer(s, { type: "user", seq: 1, text: "继续", submissionId: "sub-dup" });
  eq(userRows(s).length, 1, "同 submissionId 双发折叠为一条（W5 空白格收口，基线两条）");
  // tl1712 族钉语义：不同 submissionId 的同文连续打字合法（两行都渲染）。
  let s2 = reducer(initialState, { type: "user", seq: 0, text: "ok", submissionId: "sub1" });
  s2 = reducer(s2, { type: "user", seq: 1, text: "ok", submissionId: "sub2" });
  eq(userRows(s2).length, 2, "不同 submission 同文连续打字两行都渲染（1712 钉 580 case 5）");
  // 失败重发链不受影响：失败行 submissionId 已清（reduceSubmitFailure），新发必得新 id。
  ok(judgeOptimisticUser({ ...initialState, items: [{ kind: "user", id: "u0", text: "x" } as Item] }, undefined).admit, "无 submissionId 直通（防御）");
}

// ── D. 行身份唯一断言门禁（acceptance 3）────────────────────────────────────
{
  const libDir = join(import.meta.dirname, "..", "lib");
  const gate = "surfaceAdmission.ts";
  // 装配层文件清单（写入者侧：reducer/装配/行装配/提交失败/行模型）。
  const assemblyFiles = ["useController.ts", "hydrateHistoryApply.ts", "assistantItems.ts", "turnSubmissionFailure.ts", "transcriptRows.ts", "streamDeltaBatch.ts", "turnEventProjection.ts"];
  // 门禁 1：turn 渲染行身份解析（`a:` 前缀 → turnId）只允许在闸内实现。
  for (const file of assemblyFiles) {
    const src = readFileSync(join(libDir, file), "utf8");
    ok(!/startsWith\(`a:`\)|startsWith\("a:"\)/.test(src), `${file} 不得本地解析 turn 渲染行身份（唯一入口 ${gate}）`);
  }
  // 门禁 2：行 id 集合派生函数（stableIdSet 形态）在装配层只能以「读闸消费者」
  // 形态存在——本地定义即红（历史失败形态：第二套 stableIdSet 克隆）。
  for (const file of assemblyFiles) {
    if (file === gate) continue;
    const src = readFileSync(join(libDir, file), "utf8");
    ok(!/function\s+\w*[sS]tableIdSet\w*\s*\(/.test(src), `${file} 不得本地定义 stableIdSet 形态函数（消费者改 import ${gate}）`);
  }
  // 门禁 3：消费者确从闸 import（retainedLiveTail 消费者形态验证）。
  const hydrate = readFileSync(join(libDir, "hydrateHistoryApply.ts"), "utf8");
  ok(/import\s*\{\s*stableItemIdSet\s*\}\s*from\s*"\.\/surfaceAdmission"/.test(hydrate), "hydrateHistoryApply 以消费者形态 import 闸的行 id 集合访问器");
  // 门禁 4：闸导出 renderedItemIds 派生（SSOT 访问器在位）。
  const gateSrc = readFileSync(join(libDir, gate), "utf8");
  ok(/renderedItemIds: ReadonlySet<string>/.test(gateSrc) && /export function surfaceRowFacts/.test(gateSrc), "闸在位：renderedItemIds SSOT 访问器唯一实现于 surfaceAdmission");
}

// ── facts 派生纯度：同 items 引用幂等、变更即新派生 ───────────────────────────
{
  const items: Item[] = [{ kind: "user", id: "u1", text: "a" }];
  const f1 = surfaceRowFacts(items);
  const f2 = surfaceRowFacts(items);
  ok(f1 === f2, "同 items 引用 memo 命中（O(1) 形态）");
  const items2 = [...items, { kind: "user", id: "u2", text: "b" } as Item];
  const f3 = surfaceRowFacts(items2);
  ok(f1 !== f3, "items 变更即新派生（不与旧表面串味）");
  eq(f3.renderedItemIds.size, 2, "renderedItemIds 覆盖全部行 id");
}

process.stdout.write("surface admission gate tests passed\n");
