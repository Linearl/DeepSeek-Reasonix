/**
 * Task 428 — ask 面板投递门（纯判定）。
 *
 * ask 选择面板在两条路径上会被静默吞掉（现场：折叠条到了、面板从未弹出，
 * agent 与用户双向干等整个 ask 超时）：
 *
 *   C1 — `ask_request` 事件进入 reducer 时，只要 `cancelRequested` 残留为
 *   true 就整体丢弃。turn 被 Stop 后经 steer/恢复继续跑时，该标志可能残留
 *   整个 turn 生命周期，后续每一次 ask（含重放）都被吞。
 *
 *   C2 — `backend_activation_start` 兼容路径（未带 `backendPendingPrompt`
 *   标签，如 restoreNavigationSource）无条件清空本地还活着的 prompt 等待。
 *
 * 两个判定都抽成纯函数：吞没决策可脱离 DOM 单测，reducer 分支与诊断日志
 * 读同一份结论，永不漂移。
 *
 * 判据与 406（工具恢复围栏）联动：前端打点 feature=ask-panel、Go 侧
 * [ask-panel] emit 行、interrupted-turn-recovery 记录，三方按 prompt id +
 * turn id 关联，可从日志直接复原「发出→前端处理→围栏收尾」全链。
 */

/** C1 判定输入：reducer 进入 ask_request 分支时的状态切片。 */
export interface AskArrivalView {
  cancelRequested: boolean;
  /** 本地仍认为 turn 存活（running 或 turnActive）。 */
  turnLive: boolean;
  /** 已本地作答/提交成功的 prompt id（#6432 墓碑）。 */
  resolvedPromptId?: string;
}

export type AskArrivalVerdict =
  | { action: "surface"; clearCancelResidue: false; reason: "fresh" }
  | { action: "surface"; clearCancelResidue: true; reason: "cancel-residue" }
  | { action: "drop"; reason: "already-resolved" }
  | { action: "drop"; reason: "cancelled-idle" };

/**
 * 判定一条到来的 ask 是否允许打开面板。
 *
 * 顺序即语义：
 * 1. 已作答墓碑（resolvedPromptId）绝对优先——延迟重放不得复活已答过的
 *    面板（#6432 round 2），即使 cancel 残留也一样丢弃；
 * 2. cancel 残留 + turn 仍存活 → 新 ask 本身就是「运行时活着且在等用户」
 *    的正面证据（真正被取消的 turn 不会再提出新问题），视为残留并清掉，
 *    面板照常弹出——这是 428 的防御修复点；
 * 3. cancel + turn 已落定 → 维持旧行为丢弃（取消落地后的迟到重放，弹出
 *    只会制造僵尸面板）；
 * 4. 其余一律放行。
 */
export function judgeAskArrival(state: AskArrivalView, askId?: string): AskArrivalVerdict {
  if (askId !== undefined && state.resolvedPromptId !== undefined && askId === state.resolvedPromptId) {
    return { action: "drop", reason: "already-resolved" };
  }
  if (state.cancelRequested) {
    if (state.turnLive) return { action: "surface", clearCancelResidue: true, reason: "cancel-residue" };
    return { action: "drop", reason: "cancelled-idle" };
  }
  return { action: "surface", clearCancelResidue: false, reason: "fresh" };
}

/** C2 判定输入：backend_activation_start 分支的状态切片（结构性最小视图）。 */
export interface ActivationPromptView {
  /** 后端标签：激活元数据确认该会话确有挂起 prompt。 */
  backendPendingPrompt?: boolean;
  approval?: { id?: string };
  ask?: { id?: string };
  mcpInteraction?: { id?: string };
  running: boolean;
  turnActive: boolean;
}

export interface ActivationPromptDecision {
  preservePrompt: boolean;
  /**
   * 护栏保留面板时仍丢弃新鲜度锚点（promptArrivedId/At）：激活后的重放要
   * 相对本次激活重新锚定（#6429 tab-switch 语义）。面板留下，锚点重置。
   */
  resetPromptAnchor: boolean;
  /** 护栏生效时给出被保住的 prompt 种类（旧代码在这里会清空它）。 */
  guardedKind?: "ask" | "approval" | "mcp";
  guardedPromptId?: string;
}

/**
 * 判定 backend_activation_start 是否保留本地 prompt 等待。
 *
 * 带标签的快路径维持原语义（后端确认挂起 + 本地确有 approval/ask 才保留，
 * 且保留原新鲜度边界）。护栏只补兼容路径的缺口：本地还持有 prompt 等待、
 * 且 turn 仍存活时保留面板——清空一个活着的等待就是 428 的 C2 吞面板；
 * turn 已落定的缓存残留仍按原兼容语义清掉（陈旧面板不复活）。
 */
export function decideActivationPrompt(state: ActivationPromptView): ActivationPromptDecision {
  const tagged = Boolean(state.backendPendingPrompt && (state.approval || state.ask));
  if (tagged) return { preservePrompt: true, resetPromptAnchor: false };
  const live = state.ask
    ? ({ kind: "ask" as const, id: state.ask.id })
    : state.approval
      ? ({ kind: "approval" as const, id: state.approval.id })
      : state.mcpInteraction
        ? ({ kind: "mcp" as const, id: state.mcpInteraction.id })
        : undefined;
  if (live && (state.running || state.turnActive)) {
    return { preservePrompt: true, resetPromptAnchor: true, guardedKind: live.kind, guardedPromptId: live.id };
  }
  return { preservePrompt: false, resetPromptAnchor: false };
}

/**
 * 任务469 fence 判定输入：handleWireEvent 在 P16 收据行（reducer 内）之前
 * 有两道会静默丢事件的 fence——runtimeEpoch（本地锚过时，如 runtime:rebuilt
 * 走 App 级后备队列与 sink 队列顺序倒挂）与 sessionGeneration（meta 尚未跟
 * 上会话轮换后的新代）。ask/approval/mcp 卡片事件被这两道 fence 吞掉时：
 * 「到了但被吞」与「根本没到」在 desktop.log 不可分辨，且不触发任何恢复——
 * 人在场也无法处理，只能关闭重开。
 *
 * 判定抽成纯函数：丢弃决策、诊断打点与对账触发读同一份结论，永不漂移。
 */
export interface PromptFenceView {
  /** 是否 prompt 卡片类事件（ask_request / approval_request / mcp_interaction）。 */
  promptEvent: boolean;
  /** 本地已采纳的 runtime epoch（runtimeEpochByTabRef）。 */
  acceptedEpoch?: string;
  /** 事件自带的 runtime epoch。 */
  eventEpoch?: string;
  /** 本地 meta 的会话代（statesRef meta.sessionGeneration）。 */
  localGeneration?: number;
  /** 事件自带的会话代（wire sessionGeneration）。 */
  eventGeneration?: number;
}

export type PromptFenceVerdict =
  | { action: "admit" }
  | { action: "drop"; reason: "epoch-fence" | "generation-fence"; reconcile: boolean };

/**
 * 判定一条到来事件是否允许通过 fence 进入 projector/reducer。
 *
 * 语义分层：
 * - fence 拒绝规则与既有行为逐字等价（epoch 双方非空且不同 → 丢；事件带
 *   会话代而本地 meta 缺失/无代/代不同 → 丢）——本判定不放宽任何一道门，
 *   跨会话/跨 runtime 的内容泄漏防护原样保留；
 * - 唯一的差别在「丢」的后果：prompt 卡片类事件被丢时 reconcile=true——
 *   调用方据此走权威对账（刷新 meta + 后端重放）。重放只重发当前
 *   controller 真实挂起的 prompt（无僵尸风险），且重放事件不带 seq，
 *   可绕开卡住的 projector 序列；普通事件维持纯丢弃（行为不变）。
 */
export function judgePromptFenceArrival(view: PromptFenceView): PromptFenceVerdict {
  if (view.eventEpoch && view.acceptedEpoch && view.acceptedEpoch !== view.eventEpoch) {
    return { action: "drop", reason: "epoch-fence", reconcile: view.promptEvent };
  }
  if (
    view.eventGeneration !== undefined &&
    (view.localGeneration === undefined || view.localGeneration !== view.eventGeneration)
  ) {
    return { action: "drop", reason: "generation-fence", reconcile: view.promptEvent };
  }
  return { action: "admit" };
}

/**
 * 任务536 提交报错判定（纯判定）：后端已弃置的 prompt（取消/超时/autopilot
 * 拒绝/controller 重建）会把残留面板的提交拒回 `prompt is not pending`
 * （或双击后的 `prompt is already resolved`）。这两类报错对用户的正确呈现是
 * 「面板关掉」，不是一段英文报错——用户被坑 3 次的现场就是面板开着、报错
 * 弹了 3 次。判定与 UI 后果分离：submit 路径读同一份结论，永不漂移。
 */
export function judgePromptGoneError(message: string): boolean {
  return /not pending|already resolved/i.test(message);
}

/**
 * 任务461-P16 收据打点（纯判定）：每一条到达前端的 ask 都落一行收据，与
 * 后端 `[ask-panel] ask request emitted`（controller.go）按 prompt id +
 * turn id 对表，量化 emit→前端收到 的投递延迟——「弹窗延迟大」「完全不弹」
 * 「用户消息不渲染」（P17）共用这一条后端→webview 通道，收据行把断点二分
 * 为「事件没到前端」与「到了但没呈现」。抽成纯函数与 428 的两个判定同住，
 * reducer 调用与单测读同一份结论。
 *
 * emittedAt 来自 wire 的 emittedAt（后端序列化时刻，unix ms）；缺省（旧后
 * 端或重放前的历史事件）时延迟留空，收据行仍然落——「收到」本身就是断点
 * 证据。
 */
export function describeAskReceipt(
  emittedAt: number | undefined,
  askId: string | undefined,
  turnId: string | undefined,
  nowMs: number,
): { latencyMs?: number; detail: string } {
  const latencyMs = emittedAt && emittedAt > 0 ? Math.max(0, nowMs - emittedAt) : undefined;
  const parts = [`ask=${askId ?? "-"}`, `turn=${turnId ?? "-"}`];
  if (latencyMs !== undefined) parts.push(`delivery_ms=${latencyMs}`);
  else parts.push("delivery_ms=absent");
  return { latencyMs, detail: parts.join(" ") };
}
