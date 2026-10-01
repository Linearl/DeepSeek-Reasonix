// Run: tsx src/__tests__/task428-ask-panel-gate.test.ts
//
// 任务 428：ask 面板投递门（C1 残留 cancel 不吞注入 / C2 激活清空护栏）。
// 纯逻辑测试——判定函数与 reducer 状态迁移，不依赖 DOM/渲染。

import { judgeAskArrival, decideActivationPrompt } from "../lib/askPanelGate";
import { initialState, reducer } from "../lib/useController";
import type { WireEvent } from "../lib/types";

let passed = 0;
let failed = 0;

function eq(a: unknown, b: unknown, label: string) {
  if (a === b) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(b)}, got ${JSON.stringify(a)}\n`);
    failed += 1;
  }
}

function expect(condition: unknown, label: string) {
  eq(Boolean(condition), true, label);
}

// ---------- C1：judgeAskArrival 纯判定 ----------

const fresh = judgeAskArrival({ cancelRequested: false, turnLive: true }, "ask-1");
eq(fresh.action, "surface", "C1 fresh ask surfaces");
eq(fresh.action === "surface" && fresh.clearCancelResidue, false, "C1 fresh ask keeps cancel flag untouched");

const residue = judgeAskArrival({ cancelRequested: true, turnLive: true }, "ask-1");
eq(residue.action, "surface", "C1 残留 cancel + turn 存活 → 面板仍弹出（428 防御点）");
eq(residue.action === "surface" && residue.clearCancelResidue, true, "C1 残留路径同时清掉残留标志");

const cancelledIdle = judgeAskArrival({ cancelRequested: true, turnLive: false }, "ask-1");
eq(cancelledIdle.action, "drop", "C1 cancel + turn 已落定 → 维持丢弃（取消后的迟到重放不弹僵尸面板）");
eq(cancelledIdle.reason, "cancelled-idle", "C1 丢弃原因可诊断");

const resolved = judgeAskArrival({ cancelRequested: true, turnLive: true, resolvedPromptId: "ask-1" }, "ask-1");
eq(resolved.action, "drop", "C1 已作答墓碑绝对优先——残留 cancel 也不复活已答面板（#6432）");
eq(resolved.reason, "already-resolved", "C1 墓碑丢弃原因可诊断");

const resolvedOtherId = judgeAskArrival({ cancelRequested: false, turnLive: true, resolvedPromptId: "ask-0" }, "ask-1");
eq(resolvedOtherId.action, "surface", "C1 不同 id 不受墓碑影响，正常放行");

const noId = judgeAskArrival({ cancelRequested: false, turnLive: true, resolvedPromptId: "ask-1" }, undefined);
eq(noId.action, "surface", "C1 无 id 事件跳过墓碑比对（维持原行为）");

// ---------- C2：decideActivationPrompt 纯判定 ----------

const taggedAsk = decideActivationPrompt({ backendPendingPrompt: true, ask: { id: "ask-1" }, running: false, turnActive: false });
eq(taggedAsk.preservePrompt, true, "C2 带标签 + 本地 ask → 维持原快路径保留");

const taggedNoPrompt = decideActivationPrompt({ backendPendingPrompt: true, running: false, turnActive: false });
eq(taggedNoPrompt.preservePrompt, false, "C2 带标签但本地无 prompt → 清空（原语义）");

const guardAsk = decideActivationPrompt({ ask: { id: "ask-1" }, running: true, turnActive: false });
eq(guardAsk.preservePrompt, true, "C2 兼容路径 + 存活 ask + turn 存活 → 护栏保留（428 防御点）");
eq(guardAsk.resetPromptAnchor, true, "C2 护栏保留面板但丢弃锚点——激活后重放重新锚定（#6429 保持）");
eq(guardAsk.guardedKind, "ask", "C2 护栏上报 prompt 种类供打点");
eq(guardAsk.guardedPromptId, "ask-1", "C2 护栏上报 prompt id 供打点关联");

const taggedAnchor = decideActivationPrompt({ backendPendingPrompt: true, ask: { id: "ask-1" }, running: true, turnActive: true });
eq(taggedAnchor.resetPromptAnchor, false, "C2 带标签快路径保留原新鲜度边界（原语义）");

const guardApproval = decideActivationPrompt({ approval: { id: "ap-1" }, running: false, turnActive: true });
eq(guardApproval.guardedKind, "approval", "C2 护栏同样覆盖 approval 等待");

const guardMcp = decideActivationPrompt({ mcpInteraction: { id: "mcp-1" }, running: true, turnActive: true });
eq(guardMcp.guardedKind, "mcp", "C2 护栏同样覆盖 mcp 等待");

const staleIdle = decideActivationPrompt({ ask: { id: "ask-1" }, running: false, turnActive: false });
eq(staleIdle.preservePrompt, false, "C2 turn 已落定的缓存残留 → 维持原兼容清空（僵尸面板不复活）");

const nothingLive = decideActivationPrompt({ running: true, turnActive: true });
eq(nothingLive.preservePrompt, false, "C2 无 prompt 时行为不变");

// ---------- C1：reducer 级回归（现场复现路径） ----------

// 现场：turn 运行中（steer 驱动长 turn），cancel 残留，ask 到达。
const runningTurn = reducer(initialState, { type: "event", e: { kind: "turn_started", turnId: "turn-1", status: "in_progress" } as WireEvent });
const cancelledMidTurn = reducer(runningTurn, { type: "cancel_requested" });
eq(cancelledMidTurn.cancelRequested, true, "现场前置：cancel 请求置位残留标志");
eq(cancelledMidTurn.running, true, "现场前置：turn 仍在运行（cancel 未落地）");
const residueAsk = reducer(cancelledMidTurn, {
  type: "event",
  e: { kind: "ask_request", turnId: "turn-1", itemId: "ask-1", ask: { id: "ask-1", questions: [] } } as WireEvent,
});
eq(residueAsk.ask?.id, "ask-1", "C1 回归：残留 cancel 不再吞 ask，面板状态就位");
eq(residueAsk.cancelRequested, false, "C1 回归：残留标志随新 ask 清除");
eq(residueAsk.pendingPrompt, true, "C1 回归：pendingPrompt 打开");
eq(residueAsk.running, true, "C1 回归：turn 运行态保持");
eq(residueAsk.cancellable, true, "C1 回归：面板可取消");

// 旧行为保持：cancel 已落地（turn 结束）后迟到的 ask 重放仍被丢弃。
const settledCancel = reducer(runningTurn, { type: "cancel_requested" });
const cancelledDone = reducer(settledCancel, {
  type: "event",
  e: { kind: "turn_done", turnId: "turn-1", status: "interrupted" } as WireEvent,
});
eq(cancelledDone.cancelRequested, false, "cancel 落地（turn_done）清掉标志");
const lateReplay = reducer(
  { ...cancelledDone, cancelRequested: true },
  { type: "event", e: { kind: "ask_request", turnId: "turn-1", itemId: "ask-1", ask: { id: "ask-1", questions: [] } } as WireEvent },
);
eq(lateReplay.ask, undefined, "C1 回归：turn 已落定后的迟到重放仍丢弃（原语义）");

// 已作答墓碑：ask 提交成功后，同 id 延迟重放不得复活面板。
const waitingAsk = reducer(runningTurn, {
  type: "event",
  e: { kind: "ask_request", turnId: "turn-1", itemId: "ask-1", ask: { id: "ask-1", questions: [] } } as WireEvent,
});
const answered = reducer(waitingAsk, { type: "ask_submit_succeeded", id: "ask-1", epoch: waitingAsk.promptEpoch });
eq(answered.resolvedPromptId, "ask-1", "前置：提交成功写入墓碑");
const replayedAnswered = reducer(answered, {
  type: "event",
  e: { kind: "ask_request", turnId: "turn-1", itemId: "ask-1", ask: { id: "ask-1", questions: [] } } as WireEvent,
});
eq(replayedAnswered.ask, undefined, "C1 回归：已答面板的延迟重放不复活（#6432 保持）");

// ---------- C2：reducer 级回归 ----------

// 现场：面板已弹出（ask 等待中、turn 存活），一次未带标签的激活清空曾把它抹掉。
const activationDuringAsk = reducer(waitingAsk, { type: "backend_activation_start" });
eq(activationDuringAsk.ask?.id, "ask-1", "C2 回归：兼容路径激活不再清空存活 ask");
eq(activationDuringAsk.pendingPrompt, true, "C2 回归：pendingPrompt 保持");
eq(activationDuringAsk.running, true, "C2 回归：运行态保持");
eq(activationDuringAsk.promptArrivedId, undefined, "C2 回归：锚点被丢弃，激活后重放重新锚定（#6429 保持）");
eq(activationDuringAsk.backendActivationPending, true, "C2 回归：激活挂起标志照常置位");
const activationDone = reducer(activationDuringAsk, { type: "backend_activation_done" });
eq(activationDone.backendActivationPending, false, "C2 回归：激活完成标志照常释放");

// #6429 原形：未带标签激活丢锚点（测试锚字段，面板本体由护栏保住）。
const approvalArmed = reducer(initialState, {
  type: "event",
  e: { kind: "approval_request", approval: { id: "plan-1", tool: "exit_plan_mode", subject: "Approve" } } as WireEvent,
});
const approvalActivated = reducer(approvalArmed, { type: "backend_activation_start" });
eq(approvalActivated.promptArrivedId, undefined, "C2 回归：approval 场景激活丢锚点（#6429 断言保持）");
eq(approvalActivated.promptArrivedAt, undefined, "C2 回归：approval 场景激活丢到达时间");
eq(approvalActivated.approval?.id, "plan-1", "C2 回归：approval 场景面板本体保留");

// 带标签快路径行为不变。
const taggedActivation = reducer(waitingAsk, { type: "backend_activation_start", backendPendingPrompt: true });
eq(taggedActivation.ask?.id, "ask-1", "C2 回归：带标签路径保留行为不变");

// 空闲 tab 的陈旧缓存 prompt 仍按原兼容语义清空（构造 idle + ask 状态）。
const idleWithAsk = { ...waitingAsk, running: false, turnActive: false };
const staleWipe = reducer(idleWithAsk, { type: "backend_activation_start" });
eq(staleWipe.ask, undefined, "C2 回归：turn 已落定的缓存 ask 仍被清空（原语义）");
eq(staleWipe.running, false, "C2 回归：清空路径运行态归零不变");

// ---------- 汇总 ----------

process.stdout.write(`\n${passed + failed} assertions, ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
console.log("task 428 ask panel gate tests passed");
