// Run: tsx src/__tests__/task536-ask-panel-close.test.ts
//
// 任务536：ask 被取消/超时后面板未关闭——前端半边的回归钉。
//
// 现场：后端弃置 prompt（取消/超时/autopilot 拒绝）后前端面板开着，用户连填
// 3 次全被 "prompt is not pending" 拒回。修复契约：
//   ① 后端 prompt_closed 信号到达时，匹配 id 的面板必关 + 墓碑（延迟重放不
//      复活），非匹配 id 是无操作；
//   ② 竞态兜底：提交撞上已关闭的 prompt（"prompt is not pending" /
//      "already resolved"）按面板过期处理，不再向用户弹原始报错——判定
//      judgePromptGoneError 与 UI 后果分离（reducer 与 submit 路径读同一份）。

import assert from "node:assert/strict";
import { judgePromptGoneError } from "../lib/askPanelGate";

const { initialState, reducer } = await import("../lib/useController");
type State = import("../lib/useController").State;

function apply(state: State, e: any): State {
  return reducer(state, { type: "event", e });
}

function openAsk(): State {
  // turn 排队 → ask_request 弹面板：这是后端弃置前的真实事件序。
  let s = apply(initialState, { kind: "turn_status", turnId: "turn-1", status: "queued" });
  s = apply(s, { kind: "ask_request", turnId: "turn-1", itemId: "ask-1", ask: { id: "ask-1", questions: [{ id: "q1", prompt: "table or bullets?" }] } });
  assert.equal(s.ask?.id, "ask-1", "前置：ask 面板已打开");
  assert.equal(s.pendingPrompt, true, "前置：面板处于 pending 等待");
  return s;
}

// ---- ① prompt_closed 面板必关 ----

// 超时路径：面板开着收到 prompt_closed → 面板关、墓碑落、pendingPrompt 清。
{
  const s = openAsk();
  const closed = apply(s, { kind: "prompt_closed", turnId: "turn-1", itemId: "ask-1", promptId: "ask-1", promptKind: "ask" });
  assert.equal(closed.ask, undefined, "超时/取消后 prompt_closed 必须关掉 ask 面板");
  assert.equal(closed.pendingPrompt, false, "面板关闭后 pendingPrompt 必须清零");
  assert.equal(closed.resolvedPromptId, "ask-1", "关闭必须留墓碑，延迟重放不得复活面板");
}

// 取消路径同信号（同一事件种类，独立钉一次防将来分叉）。
{
  const s = openAsk();
  const closed = apply(s, { kind: "prompt_closed", itemId: "ask-1", promptKind: "ask" });
  assert.equal(closed.ask, undefined, "取消路径的 prompt_closed 同样必关面板");
}

// 墓碑生效：同 id 的 ask_request 延迟重放不得复活面板（#6432 同型约束）。
{
  const s = openAsk();
  const closed = apply(s, { kind: "prompt_closed", itemId: "ask-1", promptKind: "ask" });
  const replayed = apply(closed, { kind: "ask_request", turnId: "turn-1", itemId: "ask-1", ask: { id: "ask-1", questions: [] } });
  assert.equal(replayed.ask, undefined, "墓碑后的同 id 重放必须被吞（judgeAskArrival already-resolved）");
}

// 非匹配 id 是无操作：approval 卡不得被 ask 的关闭信号误伤。
{
  let s = apply(initialState, { kind: "turn_status", turnId: "turn-1", status: "queued" });
  s = apply(s, { kind: "approval_request", turnId: "turn-1", itemId: "ap-1", approval: { id: "ap-1", tool: "bash", subject: "ls" } });
  const untouched = apply(s, { kind: "prompt_closed", itemId: "ask-9", promptKind: "ask" });
  assert.equal(untouched.approval?.id, "ap-1", "非匹配 id 的 prompt_closed 不得误伤其它面板");
  assert.equal(untouched.pendingPrompt, true, "无关面板的 pending 等待保持不变");
}

// 未知 id（面板已关/后端从未弹卡）也是无操作，不产生墓碑副作用。
{
  const before = openAsk();
  const after = apply(before, { kind: "prompt_closed", itemId: "other-id", promptKind: "ask" });
  assert.equal(after.ask?.id, "ask-1", "未知 id 不得关掉活着的面板");
  assert.equal(after.resolvedPromptId, undefined, "未知 id 不得落墓碑");
}

// approval 面板同样接受 prompt_closed（同一弃置收口覆盖三种卡）。
{
  let s = apply(initialState, { kind: "turn_status", turnId: "turn-1", status: "queued" });
  s = apply(s, { kind: "approval_request", turnId: "turn-1", itemId: "ap-1", approval: { id: "ap-1", tool: "bash", subject: "rm -rf tmp" } });
  const closed = apply(s, { kind: "prompt_closed", itemId: "ap-1", promptKind: "approval" });
  assert.equal(closed.approval, undefined, "approval 卡的 prompt_closed 同样必关");
  assert.equal(closed.resolvedPromptId, "ap-1", "approval 关闭同样落墓碑");
}

// ---- ② 竞态兜底：提交撞上已关闭 prompt → 面板过期，不再弹原始报错 ----

// 判定矩阵：两类弃置报错命中；普通失败（网络/磁盘）不命中，维持原 warn 提示。
assert.equal(judgePromptGoneError("prompt is not pending"), true, "not pending 报错必须识别为面板已关");
assert.equal(judgePromptGoneError("prompt is not pending: ask 3"), true, "带上下文的 not pending 同样命中");
assert.equal(judgePromptGoneError("prompt is already resolved"), true, "already resolved（双击竞态）同样命中");
assert.equal(judgePromptGoneError("Prompt Is Not Pending"), true, "大小写不敏感（后端错误包装不保证大小写）");
assert.equal(judgePromptGoneError("connection refused"), false, "普通失败维持原有用户提示路径");
assert.equal(judgePromptGoneError("prompt belongs to a stale turn"), false, "stale 类错误由既有 isStalePromptError 处理，不重复归类");

// 竞态现场的 reducer 语义：面板开着时同 id 的 expire_prompt 关面板（与 submit
// catch 分发的动作一致），之后同 id 提交重放被墓碑吞掉。
{
  const s = openAsk();
  const action = { type: "expire_prompt" as const, id: "ask-1", epoch: s.promptEpoch, kind: "ask" as const };
  const expired = reducer(s, action);
  assert.equal(expired.ask, undefined, "提交撞上死 prompt 时 expire_prompt 必须关面板");
  assert.equal(expired.resolvedPromptId, "ask-1", "expire_prompt 落墓碑");
  const replayed = apply(expired, { kind: "ask_request", turnId: "turn-1", itemId: "ask-1", ask: { id: "ask-1", questions: [] } });
  assert.equal(replayed.ask, undefined, "墓碑吞掉对账重放，面板不再复活");
}

console.log("task536 ask panel close tests passed");
