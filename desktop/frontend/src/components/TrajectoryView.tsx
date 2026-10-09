import { useLayoutEffect, useMemo, useRef } from "react";
import type { Item } from "../lib/useController";
import type { Translator } from "../lib/i18n";
import type { DictKey } from "../lib/i18n";
import { buildTrajectoryLedger, trajectorySummary, type TrajectoryKind, type TrajectoryRecord } from "../lib/trajectoryLedger";
import { formatTrajectoryClock } from "../lib/trajectoryTimeline";
import "./TrajectoryView.css";

// 任务 704 — 轨迹视图（DSH 同款可观测性视图，A 路线）。
//
// 纯投影视图：只消费 transcript 读投影（items），不碰存储（597 纪律）。
// MVP 三层 = 事件台账（本文件）+ 时间线总览（TrajectoryTimeline，slice 5）
// + 记录检查器（TrajectoryInspector，slice 4）。turn 级 token 用量/TTFT 不
// 落盘 → 历史会话优雅降级（无用量列、无 TTFT 段，见 trajectoryLedger）。

export type TrajectoryViewProps = {
  items: Item[];
  running?: boolean;
  hydrating?: boolean;
  t: Translator;
};

const KIND_LABEL_KEYS: Record<TrajectoryKind, DictKey> = {
  user: "trajectory.kind.user",
  assistant: "trajectory.kind.assistant",
  tool: "trajectory.kind.tool",
  compaction: "trajectory.kind.compaction",
  notice: "trajectory.kind.notice",
  phase: "trajectory.kind.phase",
};

function TrajectoryRow({ record, t }: { record: TrajectoryRecord; t: Translator }) {
  const classes = [
    "traj-row",
    `traj-row--${record.kind}`,
    record.turnStart ? "traj-row--turn-start" : "",
    record.nested ? "traj-row--nested" : "",
    record.failed ? "traj-row--failed" : "",
    record.running ? "traj-row--running" : "",
  ].filter(Boolean).join(" ");
  const turnChip = record.turnStart
    ? <span className="traj-row__turn">T{record.turnLabel ?? record.turn}</span>
    : null;
  const time = record.at != null ? `${record.atKnown ? "" : "≈"}${formatTrajectoryClock(record.at)}` : "";
  return (
    <li className={classes} data-record-id={record.id}>
      <span className="traj-row__time" title={record.at != null ? formatTrajectoryClock(record.at) : undefined}>{time}</span>
      <span className="traj-row__kind">{turnChip}{t(KIND_LABEL_KEYS[record.kind])}</span>
      {record.kind === "tool" && <span className="traj-row__name">{record.title}</span>}
      <span className="traj-row__preview">{record.preview}</span>
      {record.running && <span className="traj-row__badge traj-row__badge--running">{t("trajectory.running")}</span>}
      {record.failed && <span className="traj-row__badge traj-row__badge--error">{record.errorCode ?? record.status}</span>}
    </li>
  );
}

/**
 * The trajectory surface: a turn-aware event ledger over the session's read
 * projection. Tail-anchored on mount (DSH: open at the latest record); the
 * reader stays pinned only while at the bottom, so scrolling up is stable.
 */
export function TrajectoryView({ items, running, hydrating, t }: TrajectoryViewProps) {
  const ledger = useMemo(() => buildTrajectoryLedger(items), [items]);
  const summary = useMemo(() => trajectorySummary(ledger), [ledger]);
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const pinnedRef = useRef(true);

  // Tail anchor: re-pin to the newest record while the reader sits at the
  // bottom (mount, new records, streaming growth). Scrolling up unpins.
  useLayoutEffect(() => {
    const node = scrollRef.current;
    if (node && pinnedRef.current) node.scrollTop = node.scrollHeight;
  }, [ledger, running]);

  const handleScroll = (): void => {
    const node = scrollRef.current;
    if (!node) return;
    pinnedRef.current = node.scrollHeight - node.scrollTop - node.clientHeight < 48;
  };

  const body = hydrating
    ? <div className="traj__placeholder">{t("common.loading")}</div>
    : ledger.records.length === 0
      ? <div className="traj__empty">{t("trajectory.empty")}</div>
      : (
        <ol className="traj__ledger">
          {ledger.records.map((record) => <TrajectoryRow key={record.id} record={record} t={t} />)}
        </ol>
      );

  return (
    <div className="traj" role="region" aria-label={t("trajectory.label")}>
      <div className="traj__head">
        <span className="traj__head-title">{t("trajectory.label")}</span>
        <span className="traj__head-meta">
          {t("trajectory.summary", { turns: summary.turns, assistants: summary.assistants, tools: summary.tools, failed: summary.failed })}
        </span>
        {running && <span className="traj__live-dot" aria-hidden="true" title={t("trajectory.running")} />}
      </div>
      <div className="traj__scroll" ref={scrollRef} onScroll={handleScroll}>
        {/* slice 5: the timeline overview bar mounts here (sticky header). */}
        {body}
      </div>
    </div>
  );
}
