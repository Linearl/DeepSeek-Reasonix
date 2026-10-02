// 任务 448（384 收尾）：更早历史请求的组件层策略，四个入口共用一份。
//
// 为什么收成一个模块：滚动请求、自动填充、"加载更早"按钮、问题跳转原本各自
// 抄了一份闸，于是各自漂移——三处仍在 `running` 时拒绝（controller 层的这条
// 拒绝已由任务 384 移除），两处在 `olderHistoryError` 置位时拒绝（zcode 的
// 教训：只读可重发的查询失败是"留给下次触发重试"，不是把 UI 锁死）。一份谓词、
// 测一次，是防止四份拷贝再次分叉的最小结构（任务 255 两层永久不一致是同族病）。
//
// 只有两个状态有资格闸请求，且与 controller 层口径一致：更早页是否还存在
// （hasOlder）、是否已有一页在途（loading）。`running` 与 `olderHistoryError`
// 是展示态，不是策略：会话跑着也允许向上补一页（头部前插与尾部追加天然不相交，
// 碰撞交给闸下的 revision/digest 指纹与去重），失败也不改判下次。

export type OlderHistoryRequestState = {
  /**
   * controller 层的唯一硬闸（任务 384 A′）。调用方看不见它时省略（问题跳转），
   * 此时按"假定还有更早页"处理——真没有也由 controller 自己的闸回答。
   */
  hasOlderHistory?: boolean;
  loadingOlderHistory: boolean;
};

/**
 * 能否发起一次"加载更早"。
 *
 * 注意它**不**接受 `running` / `olderHistoryError`：这两项若重新进来就是把
 * 任务 384 与 zcode 借鉴项（B2/B3）一起退回，`task448` 测试会当场红。
 */
export function canRequestOlderHistory(state: OlderHistoryRequestState): boolean {
  if (state.loadingOlderHistory) return false;
  return state.hasOlderHistory !== false;
}

/**
 * 预取半径（借 zcode timelineScrollAnchor：`max(64, 2 × 视口高)`）：读者还差
 * 两个视口到顶就把更早一页拉起来，抵达时已在本地。64px 保底，兼作视口测高为
 * 0（卸载/测高期间）时的退化值——退化到旧行为而不是不触发。
 */
export function olderHistoryTriggerPx(viewportHeight: number): number {
  if (!Number.isFinite(viewportHeight) || viewportHeight <= 0) return 64;
  return Math.max(64, viewportHeight * 2);
}

// ── 任务 448 收尾（445 调研 §1.5-2）：闸拒绝留痕 ─────────────────────────
//
// 445 取证时发现组件层拒绝是静默的：desktop.log 里 `history.older-request`
// 完全缺席，只能反推"请求被组件层吞了"，说不出是哪一道、什么原因。控制器层
// 早有同域日志（`history fetch skipped` / `older skipped reason=loading-in-progress`），
// 组件层补齐同一种可取证性，装机复现时才有据可查。
//
// explain 与 canRequest 是同一份判定的两种读法：一个给原因，一个给布尔。
// 两者各有一半测试锁着（task448 套件互为对照），谁漂移都会当场红。

export type OlderHistoryGateReason = "loading-in-progress" | "no-older-page";

export type OlderHistoryGateDecision = {
  allowed: boolean;
  reason?: OlderHistoryGateReason;
};

/** 与 `canRequestOlderHistory` 同一份两态判定，但回答"为什么拒"。 */
export function explainOlderHistoryGate(state: OlderHistoryRequestState): OlderHistoryGateDecision {
  if (state.loadingOlderHistory) return { allowed: false, reason: "loading-in-progress" };
  if (state.hasOlderHistory === false) return { allowed: false, reason: "no-older-page" };
  return { allowed: true };
}

/**
 * 转换式留痕发射器：同一原因**连续**拒绝只记首条，翻回允许后重置。
 *
 * frontendLog 的纪律是"记转换，不记每帧"——滚动路径在 loading 在途时会被
 * 连续拒绝，逐次上报会把 4MB 滚动日志刷穿；而"拒绝 → 允许 → 又拒绝"是
 * 新发生的行为，值得再记一条。`report` 由调用方注入（通常包一层
 * `reportFrontendLog("history-paging", ...)`），判定模块因此保持零依赖。
 */
export function createOlderHistoryGateLogger(
  report: (message: string, detail: string) => void,
): (decision: OlderHistoryGateDecision, trigger: string) => void {
  let last: OlderHistoryGateReason | "allowed" | undefined;
  return (decision, trigger) => {
    const key = decision.allowed ? "allowed" : decision.reason;
    if (key === last) return;
    last = key;
    if (decision.allowed) return;
    report("older request blocked at component gate", `trigger=${trigger} reason=${decision.reason ?? "unknown"}`);
  };
}
