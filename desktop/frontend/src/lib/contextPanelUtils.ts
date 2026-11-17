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
// （tl1559 r2：第一轮曾在此挂实时读数源字段喂容量卡胶囊，用户裁决摘除
// 该胶囊——与左侧转录区压缩行重复；读数源字段随之回收，override 回到
// 纯状态覆盖。实时读数只剩折叠头条与压缩卡两处，见 compactionLive.ts。）
export interface CompactionWindowOverride {
  phase: "running" | "failed";
  done?: number;
  total?: number;
}

export function compactionWindowOverride(items: Item[] | undefined): CompactionWindowOverride | null {
  for (let i = (items?.length ?? 0) - 1; i >= 0; i--) {
    const item = items![i]!;
    if (item.kind !== "compaction") continue;
    // 任务867：新一轮 CompactionStarted 把失败卡翻回 pending，同一张卡
    // pending/failed 互斥；倒序首张即最新阶段。
    if (item.pending) return { phase: "running", done: item.done, total: item.total };
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

/** 投影失效标注的点亮判据（任务549 原门 + tl1653 第二形态）。
 *
 * 投影失效（projectionValid === false）时容量读数有两种失真形态：
 *   形态一（任务549，20261024 现场）：视图退化为服务全量 canonical，读数
 *     巨大且超触发线——usedTokens > triggerTokens；
 *   形态二（tl1653，20261116 21:24 现场）：视图退化为尾部梯队（任务719），
 *     读数远小于真实占用——95 轮会话读数 2%，canonical 480K，原门永不
 *     命中，面板零降级标注，纯误导。
 * canonicalTokens 随 maintenance 一路上报：投影失效且 canonical 超过显示
 * 读数，即证明读数失真（显示的是降级视图而非真实占用），标注点亮并由
 * 标题给出 canonical 实数。投影有效时两形态都不成立，标注保持安静。 */
export function projectionStaleAnnotation(
  projectionValid: boolean | undefined,
  usedTokens: number,
  triggerTokens: number,
  canonicalTokens: number,
): boolean {
  if (projectionValid !== false) return false;
  if (triggerTokens > 0 && usedTokens > triggerTokens) return true;
  return canonicalTokens > 0 && canonicalTokens > usedTokens;
}

/** tl1656：估算偏差>30% 的切口径门。投影失效（projectionStale 已点亮）且
 * canonical 超过显示读数 30% 以上时，读数主体切换到 canonical——降级视图
 * （尾部梯队/残值投影）的读数只反映「下一请求发送什么」，而压缩判据在降级
 * 态按 canonical 物理占用触发（context_manager.go maintenanceOccupancyEstimate），
 * 主读数不切就出现「23%·距压缩 47 万」与「按 94 万触发压缩」同屏的误导
 * （20261117 09:40 现场：估算 229.9K，canonical 941485，4 倍差）。
 *
 * 投影有效时绝不切换：健康压缩会话 used<canonical 是压缩的目的本身，
 * 不是失真（tl1653 形态一门的前提同源）。canonical 缺数（旧侧报文）不动。 */
export const CONTEXT_ESTIMATE_DEVIATION_RATIO = 1.3;

/** tl1670：面板读数与「全量记录（canonical）」背离的两种形态——两态同一把尺。
 *
 *  - degradedCanonical（降级态）：投影失效，canonical 就是下一请求要发的物理
 *    占用（尾部梯队/残值投影只量到视图那一层）→ 主读数切 canonical（tl1656
 *    已交付面，本件不改其语义）。
 *  - historyVolume（常态态）：投影有效，canonical 是**整段历史体积**——压缩
 *    只写投影、历史永不缩减（现场 20261117 18:14:57 折叠后 source 771812 →
 *    视图 261814；18:30:14 下一轮压缩 src=1050159 仍是整段历史），所以
 *    canonical ≫ 视图读数是**折叠的成果不是失真**。此时主读数必须保持视图
 *    口径（切过去等于把整段历史谎报成窗口占用，会把健康会话误报成超窗），
 *    但面板必须把两个数都摆出来——用户拿面板读数与日志里的 compaction
 *    src= 对账时，需要知道那不是同一个尺（透明原则）。 */
export type ContextCaliberNote = "none" | "degradedCanonical" | "historyVolume";

export interface ContextDisplayCaliber {
  /** 容量卡主读数应显示的 token 数。 */
  tokens: number;
  /** true = 主读数已切换到 canonical（降级视图读数偏低）。 */
  canonicalOverride: boolean;
  /** 面板上要明示的口径标注（none = 无背离，两数同尺）。 */
  note: ContextCaliberNote;
}

export function contextDisplayCaliber(
  projectionStale: boolean,
  usedTokens: number,
  canonicalTokens: number,
): ContextDisplayCaliber {
  if (canonicalTokens <= 0) return { tokens: usedTokens, canonicalOverride: false, note: "none" };
  if (canonicalTokens <= usedTokens * CONTEXT_ESTIMATE_DEVIATION_RATIO) {
    return { tokens: usedTokens, canonicalOverride: false, note: "none" };
  }
  if (projectionStale) return { tokens: canonicalTokens, canonicalOverride: true, note: "degradedCanonical" };
  return { tokens: usedTokens, canonicalOverride: false, note: "historyVolume" };
}
