// 任务 704 — 轨迹时间线总览投影（DSH 同款，纯前端）。
//
// 输入是台账记录（trajectoryLedger 的产物），输出是 0..1 归一化时间域上的
// span/marker 模型。只有带已知时间锚的记录上时间线；无时长记录画成 marker
//（DSH 同款：进行中记录只画起始标记、不虚构时长）。assistant span 在有
// TTFT 数据（仅活体态）时分 TTFT/解码两段，历史会话优雅降级为单段。

import type { TrajectoryKind, TrajectoryRecord } from "./trajectoryLedger";

export interface TrajectorySpan {
  recordId: string;
  kind: TrajectoryKind;
  /** 0..1 fraction on the timeline domain. */
  start: number;
  /** 0..1 fraction; 0 for markers. */
  width: number;
  /** TTFT fraction of the span (assistant only, live-only data). */
  ttftFraction?: number;
  running: boolean;
  failed: boolean;
}

export interface TrajectoryTimelineModel {
  /** Epoch-ms domain (t1 > t0). */
  t0: number;
  t1: number;
  /** Records with a known duration, projected as spans. */
  spans: TrajectorySpan[];
  /** Records with a known anchor but no duration, projected as point markers. */
  markers: TrajectorySpan[];
  /** Inclusive epoch-ms range highlighted by a drag-select; null when clear. */
  focus: { start: number; end: number } | null;
}

/** Fraction of the domain kept as padding on each side, so edge spans stay visible. */
const PADDING_FRACTION = 0.02;

/**
 * Project ledger records onto the timeline domain. Returns null when fewer
 * than one anchored record exists (empty/anchor-less history — the overview
 * bar renders its empty state instead of a degenerate axis).
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
  return { t0, t1, spans, markers, focus };
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
