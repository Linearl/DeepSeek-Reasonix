// Run: tsx src/__tests__/turn-result-panel-gating.test.ts
// 任务522 回归钉：「本轮结果」面板（notice variant "completion"）只允许在
// 轮次结束后出现——进行中不出（completion_summary / verifying / 运行中检查）、
// 自动续轮开新轮时上一轮面板必须摘掉、结束后正常出现、关闭态零行为。

import { applyLabFlags } from "../lib/labFlags";
import { initialState, reducer, type Item, type State } from "../lib/useController";
import { withLiveTurnResult, withRunningChecks } from "../lib/completionResultState";
import type { TurnChanges, WireCompletionReceipt, WireCompletionSummary } from "../lib/types";

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

function completionNotices(s: State): Extract<Item, { kind: "notice" }>[] {
  return s.items.filter((item): item is Extract<Item, { kind: "notice" }> => item.kind === "notice" && item.variant === "completion");
}

const diff: TurnChanges = {
  id: "0:1", turn: 0, coverage: "partial",
  files: [{ path: "tmp/new521_crash.py", kind: "create", added: 259, removed: 0 }],
  added: 259, removed: 0, reasons: ["部分文件未纳入统计"],
};
const receipt: WireCompletionReceipt = { verdict: "partial", diff };
const liveSummary: WireCompletionSummary = {
  preset: "", verdict: "continue", mutations: 3,
  checks_passed: 0, checks_failed: 0, checks_suppressed: 0,
  review: "none", constraint_degraded: false,
};

function submit(s: State, text: string, seq: number): State {
  return reducer(s, { type: "user", text, seq, submissionId: `s${seq}` });
}

console.log("\n任务522：轮次未结束不出「本轮结果」面板");

{
  // 触发条件 A：上一轮面板在自动续轮（无新 user 气泡）开始时必须摘掉。
  let s = submit(initialState, "round one", 0);
  s = reducer(s, { type: "event", e: { kind: "turn_started", turnId: "t1" } });
  s = reducer(s, { type: "event", e: { kind: "completion_summary", turnId: "t1", completion: liveSummary } });
  eq(completionNotices(s).length, 0, "进行中收到 completion_summary 不出面板");
  eq(s.completionSummary?.mutations, 3, "进行中 completion_summary 仍更新状态（dock 入口不受影响）");

  s = reducer(s, { type: "event", e: { kind: "turn_done", turnId: "t1", checkpointTurn: 0, receipt } });
  eq(completionNotices(s).length, 1, "turn_done 后正常出现面板");
  eq(s.completionSummary?.receipt?.diff?.files.length, 1, "turn_done 面板携带 receipt 差异统计");

  // 自动续轮：无新 user 气泡直接开下一轮（readiness 重试 / inbox followup 等）。
  s = reducer(s, { type: "event", e: { kind: "turn_started", turnId: "t2" } });
  eq(completionNotices(s).length, 0, "新轮次开始即摘掉上一轮面板（工作中不与「本轮结果」同屏）");
  eq(s.completionSummary, undefined, "新轮次清空 summary 状态");

  s = reducer(s, { type: "event", e: { kind: "completion_summary", turnId: "t2", completion: liveSummary } });
  eq(completionNotices(s).length, 0, "续轮进行中 completion_summary 仍不出面板");
  s = reducer(s, { type: "event", e: { kind: "turn_phase", turnId: "t2", phase: "verifying" } });
  eq(completionNotices(s).length, 0, "进行中 verifying 阶段不出面板");
  eq(s.completionSummary?.checking, true, "verifying 阶段检查态照常进入状态");
  s = reducer(s, { type: "event", e: { kind: "turn_done", turnId: "t2", checkpointTurn: 1, receipt } });
  eq(completionNotices(s).length, 1, "续轮结束后面板再次出现");
}

{
  // withRunningChecks：进行中不挂面板，且摘掉遗留面板；收尾后不摘。
  const mounted = withLiveTurnResult({ ...initialState, turnActive: false }, { ...liveSummary, receipt });
  eq(completionNotices(mounted).length, 1, "withLiveTurnResult 收尾后（迟到事件）保持 turn_done 挂载语义");
  const tool: Item = { kind: "tool", id: "c1", name: "exec_command", args: '{"command":"go test ./..."}', readOnly: false, status: "running", verifying: true };
  const live = withRunningChecks({ ...mounted, turnActive: true, completionSummary: { ...liveSummary, receipt, checking: true } });
  eq(completionNotices(live).length, 0, "withRunningChecks 进行中摘掉遗留面板");
  eq(live.completionSummary?.checking, false, "withRunningChecks 无运行中工具时检查态归零（状态仍更新）");
  const settled = withRunningChecks({ ...mounted, turnActive: false });
  eq(completionNotices(settled).length, 1, "收尾后迟到的检查事件不摘面板");
  const withTools = withRunningChecks({ ...mounted, turnActive: true, items: [tool] });
  eq(completionNotices(withTools).length, 0, "有运行中检查工具时进行中同样不出面板");
  eq(withTools.completionSummary?.liveChecks?.length, 1, "运行中检查照常进入 liveChecks");
}

{
  // 关闭态零行为：completionSummary lab 关闭时不挂任何面板，状态仍更新。
  applyLabFlags({ completionSummary: false });
  try {
    let s = submit(initialState, "round one", 0);
    s = reducer(s, { type: "event", e: { kind: "turn_started", turnId: "t1" } });
    s = reducer(s, { type: "event", e: { kind: "completion_summary", turnId: "t1", completion: liveSummary } });
    s = reducer(s, { type: "event", e: { kind: "turn_done", turnId: "t1", checkpointTurn: 0, receipt } });
    eq(completionNotices(s).length, 0, "关闭态 turn_done 不出面板");
    eq(s.completionSummary?.receipt?.diff?.files.length, 1, "关闭态状态仍更新（dock/CLI 回执不受影响）");
    s = reducer(s, { type: "event", e: { kind: "turn_started", turnId: "t2" } });
    eq(completionNotices(s).length, 0, "关闭态新轮次无面板可摘、无副作用");
  } finally {
    applyLabFlags({});
  }
}

console.log(`\nturn-result panel gating: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
