// 任务1015 会话运行态 SSOT 单源化：唯一的 TurnRuntimeState 五态，由后端
// RuntimeStatus（经 runtime-state 投影 / TabMeta backend_status 事件到达）
// 派生。停止按钮显隐、可复制操作行、send 形态、steer placeholder、排队提示
// 全部经 turnRuntimeSignals 从它派生——前端不得本地推断 turn 结束。
//
// 后端权威源：Controller.RuntimeStatus()（internal/control/controller.go）—
//   Running(=running||finishing) / Finishing / CancelRequested / StopLevel /
//   TurnID / TurnEventSeq。本地事件流（turn_started/turn_done Wire 事件）是
//   后端事件的转发镜像，不是推断；它只参与「运行中」的并集防闪断，永远不
//   能单独宣布结束（结束必须投影与本地事件流一致收敛为 idle）。
//
// 任务796 的 30s 同 turn 宽限拒绝随本件移除：那是「本地 finalize 压过后端
// running」的本地推断路径，正是四信号矛盾的源头之一。fan-out 卡死（finishing
// 泄漏）由后端 idle-turn bridge（90s）自愈投影，前端从此跟随后端。

export type TurnRuntimeStateName = "idle" | "running" | "stopping" | "queued_followup" | "unknown";

export interface TurnRuntimeInput {
  /** 投影 schemaVersion === 1。false = legacy 后端无投影，回落本地事件流。 */
  known: boolean;
  /** 投影 freshness === "synced"。false = 510 unknown（投影可能过期）。 */
  freshnessSynced: boolean;
  /** 后端 RuntimeStatus.Running（running||finishing）的投影镜像。 */
  backendRunning?: boolean;
  /** 本地事件流的 turn 运行中（turn_started/turn_done 事件镜像，非推断）。 */
  localRunning: boolean;
  /** 后端 CancelRequested。 */
  cancelRequested?: boolean;
  /** 后端 461-P7 终止档位（>0 = 终止流程已开）。 */
  stopLevel?: number;
  /** 投递层降级回执：消息在 durable 队列等待下一轮（steer 注入失败降级）。 */
  queuedFollowup: boolean;
}

export interface TurnRuntimeDerivation {
  state: TurnRuntimeStateName;
  /** 已知并集的「turn 在跑」判定（unknown 下仍可用，驱动停止出口）。 */
  runningKnown: boolean;
  /** 迁移留痕用：触发本次状态的事实（后端投影/本地事件/投递回执）。 */
  reason: string;
}

/**
 * 五态派生（纯函数）。优先级：
 *   unknown（投影不可信，510 降级）> stopping（终止流程已开）>
 *   queued_followup（投递回执在队）> running > idle。
 * 「running」判定 = 后端投影 ∪ 本地事件流（known 时）；两者都 idle 才 idle。
 */
export function deriveTurnRuntime(input: TurnRuntimeInput): TurnRuntimeDerivation {
  const backendRunning = input.known ? Boolean(input.backendRunning) : undefined;
  // 并集防闪断：本地 turn_started 已到而投影尚未跟上（400ms 合批/推送延迟）
  // 不得显示 idle；后端投影说 running 时本地 turn_done（fan-out 窗口）不得
  // 压制——前端不推断结束，双源一致 idle 才 idle。
  const runningKnown = backendRunning === undefined ? input.localRunning : backendRunning || input.localRunning;
  if (!input.known || !input.freshnessSynced) {
    return { state: "unknown", runningKnown, reason: input.known ? "projection-unsynced" : "projection-legacy" };
  }
  if (input.cancelRequested || (input.stopLevel ?? 0) > 0) {
    return { state: "stopping", runningKnown: true, reason: input.cancelRequested ? "cancel-requested" : "stop-level" };
  }
  if (input.queuedFollowup) {
    // 降级回执意味着提交时 turn 正在跑（"turn already running" 才降级）；
    // 排队态与 running 可区分（验收3），turn 结束后随新轮自动转 running。
    return { state: "queued_followup", runningKnown: true, reason: "queued-receipt" };
  }
  return runningKnown
    ? { state: "running", runningKnown, reason: backendRunning ? "backend-running" : "local-running" }
    : { state: "idle", runningKnown: false, reason: "all-idle" };
}

export interface TurnRuntimeSignals {
  state: TurnRuntimeStateName;
  /** turn 实际在跑：停止按钮显隐与 send steer 形态的驱动（unknown 出口常驻由它保持）。 */
  turnActive: boolean;
  /** 停止按钮显隐：running/stopping/queued_followup 恒显；unknown 不作为隐藏条件（510）。 */
  stopVisible: boolean;
  /** send 按钮形态 steer + steer placeholder。 */
  steerForm: boolean;
  /** 输入区排队态提示（不混作普通通知）。 */
  queuedNotice: boolean;
  /** 会话静默：可复制/操作行挂载前提。 */
  settled: boolean;
  /** 投影降级（510 run strip 文案「连接中断/状态待同步」）。 */
  degraded: boolean;
}

/** 四信号谓词全部从五态派生——消费端不得再从其它源推断。 */
export function turnRuntimeSignals(derivation: TurnRuntimeDerivation): TurnRuntimeSignals {
  const { state, runningKnown } = derivation;
  const turnActive = state === "running" || state === "stopping" || state === "queued_followup"
    || (state === "unknown" && runningKnown);
  return {
    state,
    turnActive,
    stopVisible: turnActive,
    steerForm: turnActive,
    queuedNotice: state === "queued_followup",
    settled: state === "idle",
    degraded: state === "unknown",
  };
}

// ---- 迁移留痕（验收4：每次迁移记录 from→to 与触发事件，诊断可查）----

export interface TurnRuntimeTransition {
  at: number;
  from: TurnRuntimeStateName;
  to: TurnRuntimeStateName;
  /** deriveTurnRuntime 的 reason（触发事实）。 */
  reason: string;
}

const TRANSITION_LOG_LIMIT = 50;
const transitionLog: TurnRuntimeTransition[] = [];

export function recordTurnRuntimeTransition(transition: TurnRuntimeTransition): void {
  if (transition.from === transition.to) return;
  transitionLog.push(transition);
  if (transitionLog.length > TRANSITION_LOG_LIMIT) transitionLog.splice(0, transitionLog.length - TRANSITION_LOG_LIMIT);
}

export function turnRuntimeTransitionLog(): readonly TurnRuntimeTransition[] {
  return transitionLog;
}

/** 测试隔离用。 */
export function resetTurnRuntimeTransitionLog(): void {
  transitionLog.length = 0;
}
