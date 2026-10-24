// 任务 704 — 轨迹时间线总览投影（DSH 同款，纯前端）。
// 任务 1112 — 时间轴三行分色甘特 + 四模式投影（sequence/duration/time/actual）。
//
// 两种投影入口：
//   buildTrajectoryTimeline(records)   —— 真实时间模式（time/actual）：带时间锚
//     的记录按 epoch-ms 起止投影到 0..1 域；无时长记录画 marker（进行中只画
//     起始标记，不虚构时长，DSH 同款）；assistant 在有 TTFT（仅活体态）时分
//     TTFT/解码两段。
//   buildTrajectorySequenceTimeline(records) —— 等宽/耗时模式（sequence/duration）：
//     只依赖记录顺序（1111 调研结论：等宽模式不需要时间戳，历史会话可完整呈
//     现）。sequence = 每条记录占 1 等宽单位；duration = 有耗时按耗时比例、无
//     耗时记 1 最小单位，空闲间隙被压缩（DSH compressIdle 同款效果）。
//
// 两种投影输出统一形状：{ spans, markers, turnBoundaries, focus }——三行泳道
// 归一（laneOf）+ 轮次边界 + 筛选/hover 联动所需字段，供台账与搜索共用一份
// 模型（DSH「一份投影三处消费」）。

import type { TrajectoryKind, TrajectoryRecord } from "./trajectoryLedger";

/** Timeline lanes (1111 调研 §1.1): 0 输入 / 1 模型 / 2 工具。 */
export type TrajectoryLane = 0 | 1 | 2;

/** record.kind → lane（compaction/notice/phase 归灰系统行，随其时间锚所在序位取整）。 */
export function laneOf(kind: TrajectoryKind): TrajectoryLane | null {
  switch (kind) {
    case "user": return 0;
    case "assistant": return 1;
    case "tool": return 2;
    default: return null;
  }
}

export interface TrajectorySpan {
  recordId: string;
  kind: TrajectoryKind;
  /** 0..1 fraction on the timeline domain. */
  start: number;
  /** 0..1 fraction; 0 for markers. */
  width: number;
  /** TTFT fraction of the span (assistant only, live-only data). */
  ttftFraction?: number;
  /** 泳道行（0 输入/1 模型/2 工具）；null = 系统类记录不进三行（仅作轮次边界锚）。 */
  lane: TrajectoryLane | null;
  running: boolean;
  failed: boolean;
}

export interface TrajectoryTurnBoundary {
  /** Window-local turn ordinal (0 = records before the first user item). */
  turn: number;
  /** Global turn number when the user item carries historyTurn. */
  turnLabel?: number;
  /** 0..1 boundary position. */
  start: number;
}

export interface TrajectoryTimelineModel {
  /** Epoch-ms domain (t1 > t0). */
  t0: number;
  /** Epoch-ms domain. */
  t1: number;
  /** 序位模式下 1 序位单位折算的归一化宽度；时间模式为 undefined（UI 用像素下限）。 */
  unitScale?: number;
  /** Records with a known duration, projected as spans. */
  spans: TrajectorySpan[];
  /** Records with a known anchor but no duration, projected as point markers. */
  markers: TrajectorySpan[];
  /** 轮次起点位置（时间轴竖线 + 「第 N 轮」标签共用）。 */
  turnBoundaries: TrajectoryTurnBoundary[];
  /** Inclusive epoch-ms range highlighted by a drag-select; null when clear. */
  focus: { start: number; end: number } | null;
}

/** Fraction of the domain kept as padding on each side, so edge spans stay visible. */
const PADDING_FRACTION = 0.02;

/**
 * 真实时间投影（time/actual 模式）：把带时间锚的记录投影到 epoch-ms 域。
 * Returns null when fewer than one anchored record exists (empty/anchor-less
 * history — the overview bar renders its empty state instead of a degenerate axis).
 */
export function buildTrajectoryTimeline(records: TrajectoryRecord[], focus: TrajectoryTimelineModel["focus"] = null): TrajectoryTimelineModel | null {
  let t0 = Infinity;
  let t1 = -Infinity;
  for (const record of records) {
    if (record.at == null) continue;
    t0 = Math.min(t0, record.at);
    t1 = Math.max(t1, record.at + (record.durationMs ?? 0));
  }
  if (!Number.isFinite(t0) || !Number.isFinite(t1)) return null;
  if (t1 <= t0) t1 = t0 + 1; // single instant: keep a valid non-zero domain
  const domain = t1 - t0;
  const pad = domain * PADDING_FRACTION;
  const lo = t0 - pad;
  const hi = t1 + pad;
  const span = hi - lo;
  const project = (at: number): number => (at - lo) / span;

  const spans: TrajectorySpan[] = [];
  const markers: TrajectorySpan[] = [];
  for (const record of records) {
    if (record.at == null) continue;
    const common = {
      recordId: record.id,
      kind: record.kind,
      lane: laneOf(record.kind),
      running: record.running,
      failed: record.failed,
    };
    if (record.durationMs != null && record.durationMs > 0 && !record.running) {
      const start = project(record.at);
      const width = Math.max(0, project(record.at + record.durationMs) - start);
      const ttft = record.ttftMs != null && record.ttftMs > 0
        ? Math.min(1, record.ttftMs / record.durationMs)
        : undefined;
      spans.push({ ...common, start, width, ttftFraction: ttft });
    } else {
      markers.push({ ...common, start: project(record.at), width: 0 });
    }
  }
  return { t0, t1, spans, markers, turnBoundaries: turnBoundaries(records, (record) => project(record.at ?? 0)), focus };
}

/** 等宽/耗时投影（sequence/duration 模式）：只依赖记录顺序，不需要时间锚。
 * t0/t1 在 sequence 模式恒为 0/1（归一化序位域）；duration 模式 t0/t1 取总
 * 单位数，供 hover 读数换算（无时间锚时读数退化为序位，不虚构钟面时间）。 */
export function buildTrajectorySequenceTimeline(
  records: TrajectoryRecord[],
  mode: "sequence" | "duration",
  focus: TrajectoryTimelineModel["focus"] = null,
): TrajectoryTimelineModel {
  const hasDuration = (record: TrajectoryRecord): boolean =>
    record.durationMs != null && record.durationMs > 0 && !record.running;
  const widthOf = (record: TrajectoryRecord): number => {
    if (mode === "sequence") return 1;
    if (hasDuration(record)) return record.durationMs ?? 1;
    return 1; // 无耗时记录占 1 最小单位（DSH duration 模式同款 min-unit）
  };
  let cursor = 0;
  const spans: TrajectorySpan[] = [];
  const markers: TrajectorySpan[] = [];
  const boundaryAt = new Map<string, number>();
  for (const record of records) {
    const common = {
      recordId: record.id,
      kind: record.kind,
      lane: laneOf(record.kind),
      running: record.running,
      failed: record.failed,
    };
    boundaryAt.set(record.id, cursor);
    const rawWidth = widthOf(record);
    const asSpan = mode === "sequence" || hasDuration(record);
    if (asSpan) {
      spans.push({
        ...common,
        start: cursor,
        width: rawWidth,
        ttftFraction: record.ttftMs != null && record.durationMs != null && record.durationMs > 0
          ? Math.min(1, record.ttftMs / record.durationMs)
          : undefined,
      });
    } else {
      markers.push({ ...common, start: cursor, width: 0 });
    }
    cursor += rawWidth;
  }
  const total = Math.max(cursor, 1);
  // 序位域：t0/t1 = 0..总单位数（duration 模式总单位是 ms，供 hover 读数换算）；
  // unitScale = 1 序位单位折算的归一化宽度（UI 做最小像素宽度换算用）。
  return {
    t0: 0,
    t1: total,
    unitScale: 1 / total,
    spans,
    markers,
    turnBoundaries: turnBoundaries(records, (record) => (boundaryAt.get(record.id) ?? 0) / total),
    focus,
  };
}

/** 轮次起点（turnStart 记录）的 0..1 位置集合——时间轴竖线与标签共用。 */
function turnBoundaries(records: TrajectoryRecord[], project: (record: TrajectoryRecord) => number): TrajectoryTurnBoundary[] {
  const boundaries: TrajectoryTurnBoundary[] = [];
  for (const record of records) {
    if (!record.turnStart) continue;
    boundaries.push({ turn: record.turn, turnLabel: record.turnLabel, start: Math.min(1, Math.max(0, project(record))) });
  }
  return boundaries;
}

/** Epoch-ms range a pointer selection covers, normalized start <= end. */
export function pointerRangeToDomain(a: number, b: number, model: TrajectoryTimelineModel): { start: number; end: number } {
  const toDomain = (fraction: number): number => {
    const span = model.t1 - model.t0;
    return Math.round(model.t0 + Math.min(1, Math.max(0, fraction)) * span);
  };
  const start = toDomain(a);
  const end = toDomain(b);
  return start <= end ? { start, end } : { start: end, end: start };
}

/** Ledger record ids inside the focus range (anchored records only; anchor-less records never focus). */
export function recordIdsInFocus(records: TrajectoryRecord[], focus: { start: number; end: number }): Set<string> {
  const ids = new Set<string>();
  for (const record of records) {
    if (record.at == null) continue;
    if (record.at >= focus.start && record.at <= focus.end) ids.add(record.id);
  }
  return ids;
}

/** "m42.3s" style compact duration for hover readouts. */
export function formatTrajectoryDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "—";
  if (ms < 1000) return `${Math.round(ms)}ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(ms < 10_000 ? 2 : 1)}s`;
  const minutes = Math.floor(ms / 60_000);
  const seconds = (ms % 60_000) / 1000;
  return `${minutes}m${seconds.toFixed(0).padStart(2, "0")}s`;
}

/** "14:02:11" style local clock for hover readouts. */
export function formatTrajectoryClock(at: number): string {
  const date = new Date(at);
  const pad = (n: number): string => String(n).padStart(2, "0");
  return `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}
