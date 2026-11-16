// Run: tsx src/__tests__/tl1636-turn-result-single-surface.test.ts
//
// tl1636 验收：「本轮变动/本轮结果」渲染面统一——reducer 层回归钉。
//   根因链：同一回合的文件变动明细原本有两个渲染面——① turn-actions 行
//   「本轮文件」chip 直出条（Message.tsx TurnActions，不依赖 receipt 无条件
//   渲染）与 ② completion notice 折叠卡（NoticeCard，挂载判定 turn_done
//   receipt/completionSummary）。任务522 的新轮启动又无条件清空全部
//   completion 卡：用户发出下一条消息 → 上一轮卡就地消失（只剩直出条=样式A
//   观感）→ 切 tab 触发 missed-turn 全量重建 → history turn_result 无差别
//   重建卡（样式B）——「同一轮次初始样式A、切回样式B」漂移的主通道。
//   修复：①直出条退役（DOM 面在 tl1137 测试钉）；②522 收窄——只移除位于
//   最后一个 user 分界之后的卡（会当运行中轮 footer 的 auto-continue 场景），
//   有新 user 气泡时卡归上一轮块尾保留。live 与 hydration（历史 turn_result
//   按轮重建）由此共用同一判定：卡跟着自己的轮走。
//   522 原语义回归（进行中不出/终局挂载/auto-continue 摘除/关闭态零行为）
//   由 turn-result-panel-gating.test.ts 继续钉，本文件不重复。

import { initialState, reducer, type Item, type State } from "../lib/useController";
import type { TurnChanges, WireCompletionReceipt } from "../lib/types";

let passed = 0;
let failed = 0;

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}

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

function completionNotices(s: State): Extract<Item, { kind: "notice" }>[] {
  return s.items.filter((item): item is Extract<Item, { kind: "notice" }> => item.kind === "notice" && item.variant === "completion");
}

function submit(s: State, text: string, seq: number): State {
  return reducer(s, { type: "user", text, seq, submissionId: `s${seq}` });
}

const diff: TurnChanges = {
  id: "0:1", turn: 0, coverage: "complete",
  files: [{ path: "src/a.go", kind: "modify", added: 10, removed: 2 }],
  added: 10, removed: 2, reasons: [],
};
const receipt: WireCompletionReceipt = { verdict: "complete", diff };

console.log("\ntl1636：completion 卡随 user 分界保留（522 收窄）");

{
  // 场景 A（修复主行为）：轮1 settle 挂卡 → 用户发新消息（有 user 气泡分界）
  // → 新轮 turn_started —— 上一轮卡保留（归上一轮块尾，不再当运行中轮
  // footer，因为新 user item 已把它压进上一轮块）。
  let s = submit(initialState, "round one", 0);
  s = reducer(s, { type: "event", e: { kind: "turn_started", turnId: "t1" } });
  s = reducer(s, { type: "event", e: { kind: "turn_done", turnId: "t1", checkpointTurn: 0, receipt } });
  eq(completionNotices(s).length, 1, "轮1 settle 后挂卡");

  s = submit(s, "round two", 2);
  s = reducer(s, { type: "event", e: { kind: "turn_started", turnId: "t2" } });
  eq(completionNotices(s).length, 1, "tl1636：新 user 气泡分界——上一轮卡保留（切 tab 不再是唯一能看到卡的路径）");
  const notice = completionNotices(s)[0];
  const lastUser = s.items.reduce((acc, it, index) => (it.kind === "user" ? index : acc), -1);
  ok(s.items.indexOf(notice) < lastUser, "tl1636：保留的卡位于新 user 分界之前（上一轮块内，非运行中轮 footer）");
}

{
  // 场景 B（522 语义保持）：auto-continue（无新 user 气泡）——卡仍移除，
  // 防「卡当运行中轮 footer」（readiness 重试 / inbox followup 现场）。
  let s = submit(initialState, "round one", 0);
  s = reducer(s, { type: "event", e: { kind: "turn_started", turnId: "t1" } });
  s = reducer(s, { type: "event", e: { kind: "turn_done", turnId: "t1", checkpointTurn: 0, receipt } });
  eq(completionNotices(s).length, 1, "轮1 settle 后挂卡");

  s = reducer(s, { type: "event", e: { kind: "turn_started", turnId: "t2" } });
  eq(completionNotices(s).length, 0, "tl1636：auto-continue（无 user 分界）仍移除卡——522 footer 防御语义保持");
}

{
  // 场景 C（多轮并存）：轮1 卡保留 → 轮2 settle 挂自己的卡 —— 两轮各归各，
  // 与 hydration（history turn_result 按轮重建）判定一致。
  let s = submit(initialState, "round one", 0);
  s = reducer(s, { type: "event", e: { kind: "turn_started", turnId: "t1" } });
  s = reducer(s, { type: "event", e: { kind: "turn_done", turnId: "t1", checkpointTurn: 0, receipt } });

  s = submit(s, "round two", 2);
  s = reducer(s, { type: "event", e: { kind: "turn_started", turnId: "t2" } });
  s = reducer(s, { type: "event", e: { kind: "turn_done", turnId: "t2", checkpointTurn: 1, receipt } });
  eq(completionNotices(s).length, 2, "tl1636：两轮各挂各的卡（live 与 hydration turn_result 重建同构）");
}

console.log(`\ntl1636 turn-result single surface: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
