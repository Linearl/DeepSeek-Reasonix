import type { DictKey } from "../locales/en";

export interface ContextWindowStatus {
  tone: "good" | "notice" | "warn";
  key: DictKey;
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
