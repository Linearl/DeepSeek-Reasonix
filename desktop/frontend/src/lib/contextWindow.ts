export interface ContextWindowPercentages {
  raw: number;
  display: number;
}

export function contextWindowPercentages(usedTokens: number, windowTokens: number): ContextWindowPercentages {
  if (!Number.isFinite(usedTokens) || !Number.isFinite(windowTokens) || usedTokens <= 0 || windowTokens <= 0) {
    return { raw: 0, display: 0 };
  }
  const rounded = Math.max(0, Math.round((usedTokens / windowTokens) * 100));
  const raw = usedTokens > windowTokens ? Math.max(101, rounded) : rounded;
  return { raw, display: Math.min(100, raw) };
}

export interface CapacityPinLayout {
  /** 已用百分比 pin 的水平位置（clamp 后，6–94% 防出条）。 */
  usagePct: number;
  /** 压缩阈值 pin 的水平位置（clamp 后，与阈值竖线同源语义）。 */
  compactPct: number;
  /**
   * 任务834: 两个 pin 绝对定位在同一行且无防重叠——已用读数走到压缩阈值
   * 附近（±gap 内）时「NN%」与「80%」互相遮挡，而「即将压缩/已到阈值」
   * 恰是用户最需要看清已用百分比的区间。重叠时隐藏阈值 pin 的文字标签：
   * 阈值线仍由进度条上的 compact-marker 竖线表达（不随标签隐藏），foot 行
   * 「距压缩还剩 N」与 aria 摘要也始终可读；已用读数永远不被遮。
   */
  compactLabelVisible: boolean;
}

// 覆盖最窄右 dock（creation 模式 252px，卡片内 meter 条宽约 230px）时两 pin
// 的半宽和（"100%" 与 "80%" 各约 38–44px 宽）≈ 18%；条更宽时该判定只会更
// 保守（多隐藏一个冗余的静态阈值数字），不会让已用读数被遮。
const PIN_GAP_MIN_PCT = 18;

export function capacityPinLayout(rawUsagePct: number, rawCompactPct: number): CapacityPinLayout {
  const usagePct = Math.max(6, Math.min(94, rawUsagePct));
  const compactPct = Math.max(6, Math.min(94, rawCompactPct));
  return {
    usagePct,
    compactPct,
    compactLabelVisible: Math.abs(usagePct - compactPct) >= PIN_GAP_MIN_PCT,
  };
}
