// 任务 704 — 轨迹时间线总览投影（DSH 同款，纯前端）。
// 任务 1112 — 时间轴三行分色甘特 + 四模式投影（sequence/duration/time/actual）。
// 任务 1131 — 模型域统一为原始单位（DSH 同款）+ 渲染层统一换算：模型里的
//   span.start/width 与 t0/t1 一律同域（sequence = 序位数、time/duration = 真实
//   ms），渲染层经 trajectorySpanRect 除以域长折算百分比——1112 的「模型原始
//   单位 × 渲染层百分比直乘」混域（durationOn 时 span 被画成 100% 宽溢出、
//   默认态事件被会话空闲稀释成细线）在架构上消除。
//
// 两种投影入口：
//   buildTrajectoryTimeline(records)   —— 真实时间模式（time/actual）：带时间锚
//     的记录按 epoch-ms 起止投影到原始 ms 域；无时长记录画 marker（进行中只画
//     起始标记，不虚构时长，DSH 同款）；assistant 在有 TTFT（仅活体态）时分
//     TTFT/解码两段。
//   buildTrajectorySequenceTimeline(records) —— 等宽/耗时模式（sequence/duration）：
//     只依赖记录顺序（1111 调研结论：等宽模式不需要时间戳，历史会话可完整呈
//     现）。sequence = 每条记录占 1 等宽单位；duration = 有耗时按真实 ms 占比、
//     无耗时 0 宽（marker 点标注），相邻记录直接拼接 = 空闲被压缩（DSH
//     compressIdle 同款效果）。
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
  /** 起点位置，与模型 t0/t1 同域（sequence = 序位数；time/duration = 真实 ms）。 */
  start: number;
  /** 宽度，与 t0/t1 同域（ms 或序位单位）；0 for markers。 */
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
  /** Boundary position in the model domain (same unit as span.start). */
  start: number;
}

export interface TrajectoryTimelineModel {
  /** Domain start (spans carry the same unit: epoch-ms or sequence ordinals). */
  t0: number;
  /** Domain end (t1 > t0). */
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

/**
 * 真实时间投影（time/actual 模式）：把带时间锚的记录投影到 epoch-ms 域。
 * 1131：spans/markers/turnBoundaries 与 t0/t1 同用原始 ms（DSH timeline.ts 同款
 * 模型域），百分比折算统一交给渲染层 trajectorySpanRect。
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
      spans.push({
        ...common,
        start: record.at,
        width: record.durationMs,
        ttftFraction: record.ttftMs != null && record.ttftMs > 0
          ? Math.min(1, record.ttftMs / record.durationMs)
          : undefined,
      });
    } else {
      markers.push({ ...common, start: record.at, width: 0 });
    }
  }
  return { t0, t1, spans, markers, turnBoundaries: turnBoundaries(records, (record) => record.at ?? 0), focus };
}

/**
 * 等宽/耗时投影（sequence/duration 模式）：只依赖记录顺序，不需要时间锚。
 * t0/t1 = 0..总单位数（sequence 总单位 = 记录数；duration = 真实 ms 合计），
 * 与 span.start/width 同域——渲染层经 trajectorySpanRect 折算百分比。
 */
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
    return 0; // 1131：无耗时记录 0 宽（渲染为 marker 点），不再赋 1 假单位混入 ms 域（DSH duration 同款不留位）
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
  // 域 = 0..总单位数（sequence 序位 / duration ms），span/边界同域返回；
  // unitScale 保留为「1 序位单位折算的归一化宽度」读数（渲染不再依赖它做
  // 最小宽兜底——1131 起最小可见宽由纯 CSS min-width 承担，DSH 同款）。
  return {
    t0: 0,
    t1: total,
    unitScale: 1 / total,
    spans,
    markers,
    turnBoundaries: turnBoundaries(records, (record) => boundaryAt.get(record.id) ?? 0),
    focus,
  };
}

/**
 * 1131：把模型域里的 span 矩形折算为时间轴容器百分比（渲染层唯一换算口，
 * DSH TrajectoryTimeline.tsx:685-687 (span.start-model.start)/fullDuration 同款）。
 * 调用方保证 span 与 model.t0/t1 同域（两个投影构建器均满足）。
 */
export function trajectorySpanRect(model: TrajectoryTimelineModel, span: { start: number; width: number }): { leftPct: number; widthPct: number } {
  const domain = Math.max(1e-9, model.t1 - model.t0);
  return {
    leftPct: ((span.start - model.t0) / domain) * 100,
    widthPct: (span.width / domain) * 100,
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

/** Ledger record ids whose projected span overlaps the focus range (1131：
 * 按模型域区间交叠判断，DSH trajectoryTimelineFocusIndexes 同款——域单位无关，
 * 修复 sequence 序位域拖选与 epoch-ms 时间锚的混域错配；未进投影的记录永不聚焦）。 */
export function recordIdsInFocus(model: TrajectoryTimelineModel, focus: { start: number; end: number }): Set<string> {
  const ids = new Set<string>();
  const hit = (start: number, width: number): boolean => start <= focus.end && start + width >= focus.start;
  for (const span of model.spans) if (hit(span.start, span.width)) ids.add(span.recordId);
  for (const marker of model.markers) if (hit(marker.start, marker.width)) ids.add(marker.recordId);
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
