/**
 * 任务874: transcript 空白哨兵的状态机（纯函数，便于单测）。
 *
 * 2026-10-13 19:10 实录：大会话 reset 重建后 items 已在 state，视口可见块却
 * 为 0（渲染层丢失），且 kernel 的 blank-viewport anomaly 只重观察一次、不落
 * desktop.log，空白一直持续到用户重开窗口。本状态机把「内容行已挂载但连续
 * 多次检查视口内可见块为 0」判为渲染丢失：先升级为需上报（落日志），再升级
 * 为需恢复（重提交几何 + 强制回底）；恢复次数有上限，用尽后报告一次放弃，
 * 绝不无限循环重滚。
 *
 * 单帧空白是正常形态（虚拟器预测量、布局中被裁剪），因此要求连续多次命中才
 * 判丢失；`clientHeight <= 0` 的隐藏分栏 / 驻留离屏面不算空白。
 */

export const TRANSCRIPT_BLANK_CHECK_INTERVAL_MS = 400;
export const TRANSCRIPT_BLANK_CHECKS_BEFORE_RECOVERY = 3;
export const TRANSCRIPT_BLANK_RECOVERY_LIMIT = 3;

export type BlankSentinelGeometry = {
  scrollTop: number;
  scrollHeight: number;
  clientHeight: number;
  visibleBlockCount: number;
};

export type BlankSentinelState = {
  surface: string;
  blankChecks: number;
  recoveries: number;
  exhaustedReported: boolean;
};

export type BlankSentinelVerdict = {
  /** 本次检查命中空白（可见块为 0 且存在内容行）。 */
  blank: boolean;
  /** 空白已持续到阈值：需要上报并执行一次恢复。 */
  shouldRecover: boolean;
  /** 恢复次数已用尽且尚未报告过放弃：需要补一条升级日志。 */
  shouldReportExhausted: boolean;
  state: BlankSentinelState;
};

export function blankSentinelState(surface: string): BlankSentinelState {
  return { surface, blankChecks: 0, recoveries: 0, exhaustedReported: false };
}

/** surface 变化即重置：新表面的一切空白计数从零开始。 */
export function rollBlankSentinelSurface(state: BlankSentinelState, surfaceKey: string): BlankSentinelState {
  if (state.surface === surfaceKey) return state;
  return blankSentinelState(surfaceKey);
}

export function assessTranscriptBlank(
  previous: BlankSentinelState,
  geometry: BlankSentinelGeometry,
  contentRows: number,
): BlankSentinelVerdict {
  if (geometry.clientHeight <= 0 || contentRows <= 0 || geometry.visibleBlockCount > 0) {
    return { blank: false, shouldRecover: false, shouldReportExhausted: false, state: { ...previous, blankChecks: 0 } };
  }
  const blankChecks = previous.blankChecks + 1;
  if (blankChecks < TRANSCRIPT_BLANK_CHECKS_BEFORE_RECOVERY) {
    return { blank: true, shouldRecover: false, shouldReportExhausted: false, state: { ...previous, blankChecks } };
  }
  if (previous.recoveries >= TRANSCRIPT_BLANK_RECOVERY_LIMIT) {
    return {
      blank: true,
      shouldRecover: false,
      shouldReportExhausted: !previous.exhaustedReported,
      state: { ...previous, blankChecks, exhaustedReported: true },
    };
  }
  return {
    blank: true,
    shouldRecover: true,
    shouldReportExhausted: false,
    state: { ...previous, blankChecks: 0, recoveries: previous.recoveries + 1 },
  };
}
