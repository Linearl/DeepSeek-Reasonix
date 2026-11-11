import type { DictKey } from "../locales/en";
import type { Item } from "./useController";

export interface ContextWindowStatus {
  tone: "good" | "notice" | "warn";
  key: DictKey;
}

// 任务1147：压缩运行态的窗口区覆盖。用户实测（2026-10-24）：分块压缩全程
// 容量卡钉着「已超过窗口上限」红条+反复弹「卡住了请停止」，读作失败——
// 实际压缩在推进并最终成功。以最新一张压缩卡的阶段覆盖容量卡状态行：
// pending=压缩中（中性，替代红条），failed=失败（独立可辨识文案）；
// 完成卡不覆盖（正常显示压缩后读数）。
// tl1559：running 相携带实时读数源（pending 卡上 reducer 维护的
// CompactionProgress 值 + startedAt 内存戳），结构上兼容
// CompactionLiveSource——容量卡直接把整个 override 喂给共享
// compactionLiveReadout，与折叠头/压缩卡同一数据面同一格式缝。
export interface CompactionWindowOverride {
  phase: "running" | "failed";
  done?: number;
  total?: number;
  /** tl1559: running 相恒真—— CompactionLiveSource 的 pending 腿。 */
  pending?: boolean;
  startedAt?: number;
  tokens?: number;
  tokensPerSec?: number;
}

export function compactionWindowOverride(items: Item[] | undefined): CompactionWindowOverride | null {
  for (let i = (items?.length ?? 0) - 1; i >= 0; i--) {
    const item = items![i]!;
    if (item.kind !== "compaction") continue;
    // 任务867：新一轮 CompactionStarted 把失败卡翻回 pending，同一张卡
    // pending/failed 互斥；倒序首张即最新阶段。
    if (item.pending) {
      return { phase: "running", pending: true, done: item.done, total: item.total, startedAt: item.startedAt, tokens: item.tokens, tokensPerSec: item.tokensPerSec };
    }
    if (item.failed) return { phase: "failed" };
    return null;
  }
  return null;
}

export function formatCacheHitRate(hitTokens: number, missTokens: number): string {
  const denom = hitTokens + missTokens;
  if (denom <= 0) return "-";
  return `${((hitTokens / denom) * 100).toFixed(2)}%`;
}

export function contextWindowStatus(rawUsagePct: number, compactPct: number): ContextWindowStatus {
  if (rawUsagePct > 100) return { tone: "warn", key: "context.windowStatusOverLimit" };
  const usagePct = Math.min(100, Math.max(0, rawUsagePct));
  if (usagePct >= 90) return { tone: "warn", key: "context.windowStatusNearLimit" };
  if (compactPct > 0 && usagePct >= compactPct) return { tone: "warn", key: "context.windowStatusPastCompact" };
  if (compactPct > 0 && usagePct >= Math.max(0, compactPct - 10)) return { tone: "notice", key: "context.windowStatusWatch" };
  return { tone: "good", key: "context.windowStatusHealthy" };
}

/** 任务1025（显示层收口）：读数来源标记。admission.observedPrompt > 0 说明
 *  provider 上次响应已实测校准（「实测」）；= 0 说明是新会话首轮 / 切 provider
 *  首轮，读数是估算（「估算」）。 */
export type ContextReadoutSource = "measured" | "estimated";

export function contextReadoutSource(observedPrompt: number | undefined): ContextReadoutSource {
  return (observedPrompt ?? 0) > 0 ? "measured" : "estimated";
}

/** 读数来源的 i18n key，供窗口环/分段条共用同一套文案。 */
export function contextReadoutSourceKey(source: ContextReadoutSource): DictKey {
  return source === "measured" ? "context.readoutMeasured" : "context.readoutEstimated";
}
