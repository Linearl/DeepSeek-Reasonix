// 任务 1112 — 轨迹台账全文搜索索引（DSH trajectory-search-index 同款思路，纯前端）。
//
// 按记录稳定 ID 建记录级倒排：索引文本 = 轮次号/类型标签/标题/摘要（1111
// 调研 §2.1：索引含轮次号/分组/类型/摘要——原文/思考/工具参数结果走检查器，
// 本索引覆盖台账可见字段）。查询按空白切词、全部词 AND 命中；命中集驱动
// 台账过滤 + 时间轴透明度联动（未命中降透明，DSH 同款）。
//
// 索引随投影重建（React memo 缓存），不引入 3s 节流：记录数在百级、重建
// 是纯内存拼接，成本可忽略（DSH 的节流面向千级虚拟窗口）。

import type { TrajectoryKind, TrajectoryRecord } from "./trajectoryLedger";

const KIND_LABEL: Record<TrajectoryKind, string> = {
  user: "用户 user",
  assistant: "助手 assistant",
  tool: "工具 tool",
  compaction: "压缩 compaction",
  notice: "通知 notice",
  phase: "阶段 phase",
};

export interface TrajectorySearchEntry {
  recordId: string;
  /** Lowercased haystack: turn labels + kind labels + title + preview. */
  text: string;
}

/** 记录集 → 稳定 ID 倒排（顺序与 records 一致）。 */
export function buildTrajectorySearchIndex(records: TrajectoryRecord[]): TrajectorySearchEntry[] {
  return records.map((record) => {
    const turnText = `t${record.turn} turn${record.turn}${record.turnLabel != null ? ` t${record.turnLabel} turn${record.turnLabel}` : ""}`;
    return {
      recordId: record.id,
      text: `${turnText} ${KIND_LABEL[record.kind]} ${record.title} ${record.preview}`.toLowerCase(),
    };
  });
}

/** 空白切词 + 全词 AND 命中集；空查询 = 全命中（null 表示不过滤）。 */
export function trajectorySearchMatches(index: TrajectorySearchEntry[], query: string): Set<string> | null {
  const terms = query.toLowerCase().split(/\s+/).filter((term) => term !== "");
  if (terms.length === 0) return null;
  const ids = new Set<string>();
  for (const entry of index) {
    if (terms.every((term) => entry.text.includes(term))) ids.add(entry.recordId);
  }
  return ids;
}

/** 1112 台账投影选项：时长（等宽/真实耗时）+ 轮次/调用一键折叠 + 搜索 + 拖选聚焦。 */
export interface TrajectoryLedgerProjectionOptions {
  /** true = 等宽（sequence，每条记录一格）；false = duration（有耗时按真实比例）。 */
  equalWidth: boolean;
  /** 一键收起/展开全部轮次（收起后每轮折叠成一行摘要）。 */
  collapsedTurns: boolean;
  /** 一键收起/展开全部助手记录下方的工具调用（折叠为「M 个工具调用」摘要行）。 */
  collapsedCalls: boolean;
  /** 搜索命中集（null = 不过滤）。 */
  searchMatchIds: Set<string> | null;
  /** 拖选聚焦区间（null = 不过滤）。 */
  focusIds: Set<string> | null;
}
