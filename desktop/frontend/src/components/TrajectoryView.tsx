import { useLayoutEffect, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent } from "react";
import { X } from "lucide-react";
import type { Item } from "../lib/useController";
import type { Translator } from "../lib/i18n";
import type { DictKey } from "../lib/i18n";
import { buildTrajectoryLedger, trajectorySummary, type TrajectoryKind, type TrajectoryRecord } from "../lib/trajectoryLedger";
import { buildTrajectoryTimeline, formatTrajectoryClock, formatTrajectoryDuration, pointerRangeToDomain, recordIdsInFocus, type TrajectoryTimelineModel } from "../lib/trajectoryTimeline";
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

function TrajectoryRow({ record, selected, dimmed, onSelect, t }: { record: TrajectoryRecord; selected: boolean; dimmed: boolean; onSelect: (id: string) => void; t: Translator }) {
  const classes = [
    "traj-row",
    `traj-row--${record.kind}`,
    record.turnStart ? "traj-row--turn-start" : "",
    record.nested ? "traj-row--nested" : "",
    record.failed ? "traj-row--failed" : "",
    record.running ? "traj-row--running" : "",
    selected ? "traj-row--selected" : "",
    dimmed ? "traj-row--dimmed" : "",
  ].filter(Boolean).join(" ");
  const turnChip = record.turnStart
    ? <span className="traj-row__turn">T{record.turnLabel ?? record.turn}</span>
    : null;
  const time = record.at != null ? `${record.atKnown ? "" : "≈"}${formatTrajectoryClock(record.at)}` : "";
  return (
    <li
      className={classes}
      data-record-id={record.id}
      role="button"
      tabIndex={0}
      aria-pressed={selected}
      onClick={() => onSelect(record.id)}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          onSelect(record.id);
        }
      }}
    >
      <span className="traj-row__time" title={record.at != null ? formatTrajectoryClock(record.at) : undefined}>{time}</span>
      <span className="traj-row__kind">{turnChip}{t(KIND_LABEL_KEYS[record.kind])}</span>
      {record.kind === "tool" && <span className="traj-row__name">{record.title}</span>}
      <span className="traj-row__preview">{record.preview}</span>
      {record.running && <span className="traj-row__badge traj-row__badge--running">{t("trajectory.running")}</span>}
      {record.failed && <span className="traj-row__badge traj-row__badge--error">{record.errorCode ?? record.status}</span>}
    </li>
  );
}

type InspectorSection = { label: DictKey; text: string };

/** Sections for the inspector, per record kind. Empty sections are dropped. */
function inspectorSections(record: TrajectoryRecord, item: Item | undefined): InspectorSection[] {
  const sections: InspectorSection[] = [];
  const push = (label: DictKey, text: string | undefined): void => {
    const cleaned = (text ?? "").trim();
    if (cleaned !== "") sections.push({ label, text: cleaned });
  };
  switch (record.kind) {
    case "user":
      push("trajectory.inspector.input", item?.kind === "user" ? (item.submitText || item.text) : record.preview);
      break;
    case "assistant":
      push("trajectory.inspector.input", item?.kind === "assistant" ? item.reasoning : undefined);
      push("trajectory.inspector.output", item?.kind === "assistant" ? item.text : record.preview);
      break;
    case "tool":
      push("trajectory.inspector.input", item?.kind === "tool" ? item.args : undefined);
      if (item?.kind === "tool") push("trajectory.inspector.output", item.error || item.output || item.summary);
      break;
    case "compaction":
      push("trajectory.inspector.input", item?.kind === "compaction" ? `${item.trigger} · ${item.messages} msgs` : undefined);
      push("trajectory.inspector.output", item?.kind === "compaction" ? item.summary : record.preview);
      break;
    case "notice":
      push("trajectory.inspector.output", item?.kind === "notice" ? [item.title, item.text, item.detail].filter(Boolean).join("\n") : record.preview);
      break;
    case "phase":
      push("trajectory.inspector.output", item?.kind === "phase" ? item.text : record.preview);
      break;
  }
  return sections;
}

/** Drag threshold (px) separating a click-to-clear from a drag-to-select. */
const DRAG_THRESHOLD_PX = 3;

/** The timeline overview (②): a fixed bar projecting every anchored record's
 * start/duration onto one time domain (DSH 同款). Drag a range to focus the
 * ledger on that period; click to clear. Span titles carry the precise clock
 * + duration; running records draw a start marker only — never a duration. */
function TrajectoryTimelineBar({ model, focus, onFocus, t }: {
  model: TrajectoryTimelineModel;
  focus: TrajectoryTimelineModel["focus"];
  onFocus: (focus: TrajectoryTimelineModel["focus"]) => void;
  t: Translator;
}) {
  const barRef = useRef<HTMLDivElement | null>(null);
  const dragStartRef = useRef<number | null>(null);
  const [dragPreview, setDragPreview] = useState<{ start: number; end: number } | null>(null);

  const fractionAt = (clientX: number): number => {
    const node = barRef.current;
    if (!node) return 0;
    const rect = node.getBoundingClientRect();
    if (rect.width <= 0) return 0;
    return Math.min(1, Math.max(0, (clientX - rect.left) / rect.width));
  };

  const handlePointerDown = (event: ReactPointerEvent<HTMLDivElement>): void => {
    if (event.button !== 0) {
      onFocus(null); // right-click clears (DSH 同款)
      return;
    }
    event.currentTarget.setPointerCapture(event.pointerId);
    dragStartRef.current = event.clientX;
    const fraction = fractionAt(event.clientX);
    setDragPreview({ start: fraction, end: fraction });
  };

  const handlePointerMove = (event: ReactPointerEvent<HTMLDivElement>): void => {
    if (dragStartRef.current == null) return;
    if (Math.abs(event.clientX - dragStartRef.current) < DRAG_THRESHOLD_PX) return;
    const startFraction = fractionAt(dragStartRef.current);
    const fraction = fractionAt(event.clientX);
    setDragPreview({
      start: Math.min(startFraction, fraction),
      end: Math.max(startFraction, fraction),
    });
  };

  const handlePointerUp = (event: ReactPointerEvent<HTMLDivElement>): void => {
    const origin = dragStartRef.current;
    dragStartRef.current = null;
    setDragPreview((current) => {
      const preview = current;
      if (origin == null) return null;
      if (!preview || Math.abs(event.clientX - origin) < DRAG_THRESHOLD_PX) {
        onFocus(null); // plain click clears the focus range
        return null;
      }
      onFocus(pointerRangeToDomain(preview.start, preview.end, model));
      return null;
    });
  };

  const spanTitle = (span: { start: number; width: number; kind: TrajectoryKind; running: boolean; ttftFraction?: number }): string => {
    const at = model.t0 + span.start * (model.t1 - model.t0);
    const parts = [formatTrajectoryClock(Math.round(at))];
    if (span.width > 0) {
      parts.push(formatTrajectoryDuration(Math.round(span.width * (model.t1 - model.t0))));
    }
    if (span.ttftFraction != null) {
      parts.push(`TTFT ${formatTrajectoryDuration(Math.round(span.ttftFraction * span.width * (model.t1 - model.t0)))}`);
    }
    if (span.running) parts.push(t("trajectory.running"));
    return parts.join(" · ");
  };

  const domain = model.t1 - model.t0;
  const focusLeft = focus ? ((focus.start - model.t0) / domain) * 100 : 0;
  const focusWidth = focus ? ((focus.end - focus.start) / domain) * 100 : 0;

  return (
    <div
      ref={barRef}
      className="traj-timeline"
      role="img"
      aria-label={t("trajectory.timeline.label")}
      title={t("trajectory.timeline.hint")}
      onPointerDown={handlePointerDown}
      onPointerMove={handlePointerMove}
      onPointerUp={handlePointerUp}
    >
      {model.spans.map((span) => (
        <span
          key={span.recordId}
          className={`traj-timeline__span traj-timeline__span--${span.kind}${span.running ? " traj-timeline__span--running" : ""}${span.failed ? " traj-timeline__span--failed" : ""}`}
          style={{ left: `${span.start * 100}%`, width: `${Math.max(0.4, span.width * 100)}%` }}
          title={spanTitle(span)}
        >
          {span.ttftFraction != null && (
            <span className="traj-timeline__ttft" style={{ width: `${span.ttftFraction * 100}%` }} />
          )}
        </span>
      ))}
      {model.markers.map((marker) => (
        <span
          key={marker.recordId}
          className={`traj-timeline__marker traj-timeline__marker--${marker.kind}${marker.failed ? " traj-timeline__marker--failed" : ""}`}
          style={{ left: `${marker.start * 100}%` }}
          title={spanTitle(marker)}
        />
      ))}
      {focus && <span className="traj-timeline__focus" style={{ left: `${focusLeft}%`, width: `${Math.max(0.4, focusWidth)}%` }} />}
      {dragPreview && dragPreview.end - dragPreview.start > 0.001 && (
        <span
          className="traj-timeline__drag"
          style={{ left: `${dragPreview.start * 100}%`, width: `${(dragPreview.end - dragPreview.start) * 100}%` }}
        />
      )}
    </div>
  );
}

/** The record inspector (③): a local side panel over the ledger. Timing rows
 * degrade honestly — absent duration/TTFT/token usage render as the shared
 * "not recorded" note instead of invented numbers. */
function TrajectoryInspector({ record, item, onClose, t }: { record: TrajectoryRecord; item: Item | undefined; onClose: () => void; t: Translator }) {
  const sections = inspectorSections(record, item);
  const timingLines: string[] = [];
  if (record.at != null) {
    timingLines.push(`${record.atKnown ? "" : "≈"}${formatTrajectoryClock(record.at)}`);
  }
  if (record.durationMs != null) timingLines.push(formatTrajectoryDuration(record.durationMs));
  if (record.ttftMs != null) timingLines.push(`TTFT ${formatTrajectoryDuration(record.ttftMs)}`);
  return (
    <aside className="traj-inspector" role="dialog" aria-label={t(KIND_LABEL_KEYS[record.kind])}>
      <div className="traj-inspector__head">
        <span className="traj-inspector__title">{t(KIND_LABEL_KEYS[record.kind])}{record.kind === "tool" ? ` · ${record.title}` : ""}</span>
        <button type="button" className="traj-inspector__close" aria-label={t("trajectory.inspector.close")} onClick={onClose}>
          <X size={14} />
        </button>
      </div>
      <div className="traj-inspector__meta">
        <span>{t("trajectory.inspector.turn")} T{record.turnLabel ?? record.turn}</span>
        {record.step > 0 && <span>{t("trajectory.inspector.step")} {record.step}</span>}
        {record.status && <span>{t("trajectory.inspector.status")} {record.status}</span>}
        {timingLines.length > 0 && <span>{t("trajectory.inspector.timing")} {timingLines.join(" · ")}</span>}
      </div>
      {(record.kind === "assistant" || record.kind === "tool") && (
        <p className="traj-inspector__usage">{t("trajectory.inspector.usageNA")}</p>
      )}
      {sections.map((section) => (
        <section key={section.label} className="traj-inspector__section">
          <h3 className="traj-inspector__label">{t(section.label)}</h3>
          <pre className="traj-inspector__body">{section.text}</pre>
        </section>
      ))}
    </aside>
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
  const timeline = useMemo(() => buildTrajectoryTimeline(ledger.records), [ledger.records]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [focus, setFocus] = useState<TrajectoryTimelineModel["focus"]>(null);
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

  const selectedItem = selectedId != null ? items.find((item) => item.id === selectedId) : undefined;
  const selectedRecord = selectedId != null ? ledger.records.find((record) => record.id === selectedId) : undefined;
  // 拖选聚焦（DSH 同款）：区间外的台账行降透明；无锚记录永不落在区间内。
  const focusIds = useMemo(
    () => (focus && ledger ? recordIdsInFocus(ledger.records, focus) : null),
    [focus, ledger],
  );

  const body = hydrating
    ? <div className="traj__placeholder">{t("common.loading")}</div>
    : ledger.records.length === 0
      ? <div className="traj__empty">{t("trajectory.empty")}</div>
      : (
        <ol className="traj__ledger">
          {ledger.records.map((record) => (
            <TrajectoryRow key={record.id} record={record} selected={record.id === selectedId}
              dimmed={focusIds != null && !focusIds.has(record.id)}
              onSelect={setSelectedId} t={t} />
          ))}
        </ol>
      );

  return (
    <div className={`traj${selectedRecord ? " traj--inspecting" : ""}`} role="region" aria-label={t("trajectory.label")}>
      <div className="traj__head">
        <span className="traj__head-title">{t("trajectory.label")}</span>
        <span className="traj__head-meta">
          {t("trajectory.summary", { turns: summary.turns, assistants: summary.assistants, tools: summary.tools, failed: summary.failed })}
        </span>
        {running && <span className="traj__live-dot" aria-hidden="true" title={t("trajectory.running")} />}
      </div>
      {timeline && <TrajectoryTimelineBar model={timeline} focus={focus} onFocus={setFocus} t={t} />}
      <div className="traj__scroll" ref={scrollRef} onScroll={handleScroll}>
        {body}
      </div>
      {selectedRecord && <TrajectoryInspector record={selectedRecord} item={selectedItem} onClose={() => setSelectedId(null)} t={t} />}
    </div>
  );
}
