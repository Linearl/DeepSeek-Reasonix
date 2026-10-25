import { useLayoutEffect, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent } from "react";
import { X } from "lucide-react";
import type { HistoryLoadTrigger, Item } from "../lib/useController";
import type { Translator } from "../lib/i18n";
import type { DictKey } from "../lib/i18n";
import { buildTrajectoryLedger, trajectorySummary, type TrajectoryKind, type TrajectoryRecord } from "../lib/trajectoryLedger";
import { buildTrajectorySequenceTimeline, formatTrajectoryClock, formatTrajectoryDuration, pointerRangeToDomain, recordIdsInFocus, trajectorySpanRect, type TrajectoryTimelineModel } from "../lib/trajectoryTimeline";
import { buildTrajectorySearchIndex, trajectorySearchMatches, type TrajectoryLedgerProjectionOptions } from "../lib/trajectorySearchIndex";

// 任务 704 — 轨迹视图（DSH 同款可观测性视图，A 路线）。
// 任务 1112 — 时间轴三行分色甘特 + 工具栏三开关 + 全文搜索 + 行可读摘要。
// 任务 1131 — 时间轴模式对齐 DSH：默认 = sequence 等宽序位（DSH TrajectoryView
//   .tsx:347 两布尔默认 false → 'sequence'，事件 1/N 等宽满铺）；「时长」开关 =
//   duration 真实耗时占比（序位拼接天然压缩空闲）。渲染百分比统一经
//   trajectorySpanRect 换算（DSH 渲染层同款），最小可见宽由纯 CSS min-width
//   兜底——修复 1112 默认态事件被墙钟空闲稀释成细线（「每个事件都很窄」）与
//   durationOn 态模型单位混用导致的 span 溢出。真实时间墙钟投影（time）不再
//   直接上 UI（lib 保留供后续 actual 模式）。
//
// 纯投影视图：只消费 transcript 读投影（items），不碰存储（597 纪律）。
// 四层 = 工具栏（1112，三开关 + 搜索）+ 时间线总览（三行泳道，1112）+
// 事件台账（行摘要 + 轮次导轨，1112）+ 记录检查器（任务 704）。turn 级
// token 用量/TTFT 不落盘 → 历史会话优雅降级（无用量列、无 TTFT 段）。

export type TrajectoryViewProps = {
  items: Item[];
  running?: boolean;
  hydrating?: boolean;
  /** 任务 704 ⑤: controller history paging — the view window shows the tail;
   * the button chain walks backwards (view window first, backend pages next). */
  hasOlderHistory?: boolean;
  loadingOlderHistory?: boolean;
  olderHistoryExhausted?: boolean;
  onLoadOlderHistory?: (targetTurn?: number, trigger?: HistoryLoadTrigger) => boolean | Promise<boolean>;
  t: Translator;
};

/** Initial rendered window (turns) + per-expand step. DSH keeps a 50-node
 * virtual window; 12 turns bounds the DOM while content-visibility keeps
 * offscreen rows cheap. */
const WINDOW_TURNS = 12;

const KIND_LABEL_KEYS: Record<TrajectoryKind, DictKey> = {
  user: "trajectory.kind.user",
  assistant: "trajectory.kind.assistant",
  tool: "trajectory.kind.tool",
  compaction: "trajectory.kind.compaction",
  notice: "trajectory.kind.notice",
  phase: "trajectory.kind.phase",
};

/** Row shape after 1112 projection: full record, or a collapsed turn/calls
 * summary row that expands on click. */
type TrajectoryRowEntry =
  | { type: "record"; record: TrajectoryRecord }
  | { type: "turn"; turn: number; turnLabel?: number; steps: number; tools: number }
  | { type: "calls"; assistantId: string; calls: number; names: string };

/** 投影（1112）：按工具栏开关 + 搜索/聚焦命中集，把台账记录折叠成行条目。
 * 折叠语义（DSH 同款）：collapsedTurns 时每轮收成一行「N 步 · M 工具调用」；
 * collapsedCalls 时每条助手记录下的连续工具调用收成一行「M 个工具调用」。
 * 顺序：先调用折叠、再整轮折叠（否则轮内首条 user 行会顶开折叠窗口）。 */
export function projectTrajectoryRows(records: TrajectoryRecord[], options: TrajectoryLedgerProjectionOptions): TrajectoryRowEntry[] {
  const kept = (record: TrajectoryRecord): boolean =>
    (options.searchMatchIds == null || options.searchMatchIds.has(record.id)) &&
    (options.focusIds == null || options.focusIds.has(record.id));
  // Pass 1: record rows (+ collapsed tool-call summary rows).
  const pass1: TrajectoryRowEntry[] = [];
  let index = 0;
  while (index < records.length) {
    const record = records[index];
    if (!kept(record)) { index += 1; continue; }
    if (options.collapsedCalls && record.kind === "assistant") {
      let end = index + 1;
      while (end < records.length && records[end].kind === "tool" && kept(records[end])) end += 1;
      if (end > index + 1) {
        pass1.push({
          type: "calls",
          assistantId: record.id,
          calls: end - index - 1,
          names: records.slice(index + 1, end).map((tool) => tool.title).join(" · "),
        });
        index = end;
        continue;
      }
    }
    pass1.push({ type: "record", record });
    index += 1;
  }
  if (!options.collapsedTurns) return pass1;
  // Pass 2: fold each turn's non-user records into one summary row; the turn's
  // leading user record (the turn label carrier) stays visible.
  const entries: TrajectoryRowEntry[] = [];
  let cursor = 0;
  while (cursor < pass1.length) {
    const entry = pass1[cursor];
    if (entry.type === "record" && entry.record.turnStart) {
      const turn = entry.record.turn;
      let end = cursor + 1;
      while (end < pass1.length) {
        const candidate = pass1[end];
        if (candidate.type !== "record" || candidate.record.turn !== turn) break;
        end += 1;
      }
      let steps = 0;
      let tools = 0;
      for (const folded of pass1.slice(cursor + 1, end)) {
        if (folded.type === "record") {
          if (folded.record.kind === "assistant") steps += 1;
          if (folded.record.kind === "tool") tools += 1;
        } else if (folded.type === "calls") {
          tools += folded.calls;
        }
      }
      entries.push(entry, { type: "turn", turn, turnLabel: entry.record.turnLabel, steps, tools });
      cursor = end;
      continue;
    }
    entries.push(entry);
    cursor += 1;
  }
  return entries;
}

function TrajectoryRow({ entry, selected, dimmed, onSelect, t }: { entry: TrajectoryRowEntry; selected: boolean; dimmed: boolean; onSelect: (id: string) => void; t: Translator }) {
  if (entry.type === "turn") {
    return (
      <li className={`traj-row traj-row--turn${dimmed ? " traj-row--dimmed" : ""}`}>
        <span className="traj-row__time" />
        <span className="traj-row__kind">
          <span className="traj-row__turn">T{entry.turnLabel ?? entry.turn}</span>
          <span className="traj-row__tag traj-row__tag--user">{t("trajectory.kind.turn")}</span>
        </span>
        <span className="traj-row__preview traj-row__placeholder">{t("trajectory.turn.collapsed", { steps: entry.steps, tools: entry.tools })}</span>
      </li>
    );
  }
  if (entry.type === "calls") {
    return (
      <li className={`traj-row traj-row--calls${dimmed ? " traj-row--dimmed" : ""}`}>
        <span className="traj-row__time" />
        <span className="traj-row__kind">
          <span className="traj-row__tag traj-row__tag--tool">{t("trajectory.kind.calls")}</span>
        </span>
        <span className="traj-row__preview traj-row__placeholder">{t("trajectory.calls.collapsed", { count: entry.calls })}{entry.names ? ` · ${entry.names}` : ""}</span>
      </li>
    );
  }
  const record = entry.record;
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
      <span className="traj-row__kind">
        {record.turnStart && <span className="traj-row__turn">T{record.turnLabel ?? record.turn}</span>}
        <span className={`traj-row__tag traj-row__tag--${record.kind}`}>{t(KIND_LABEL_KEYS[record.kind])}</span>
      </span>
      {record.kind === "tool" && <span className="traj-row__name">{record.title}</span>}
      <span className="traj-row__preview">{record.preview}</span>
      {record.durationMs != null && record.durationMs > 0 && (
        <span className="traj-row__duration" title={formatTrajectoryDuration(record.durationMs)}>{formatTrajectoryDuration(record.durationMs)}</span>
      )}
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

/** 三行泳道标签（1111 调研 §1.1：左 44px、10px 右对齐）。 */
const LANE_LABEL_KEYS: DictKey[] = ["trajectory.lane.input", "trajectory.lane.model", "trajectory.lane.tool"];

/** The timeline overview (②): a three-lane gantt (input/model/tool) projecting
 * every record onto one domain (DSH 同款，任务 1112 三行分色；1131 模式对齐：
 * sequence 等宽序位为默认、duration 真实耗时为开关态). Both modes need record
 * order only, so history sessions render fully without persisted time anchors.
 * Drag a range to focus the ledger; click to clear. Hover tracks a crosshair
 * line; spans carry precise titles; running records draw a start marker.
 * Span rectangles are normalized here once via trajectorySpanRect (DSH 渲染层
 * 同款换算); minimum visible width is pure CSS (min-width), never JS. */
function TrajectoryTimelineBar({ model, mode, focus, onFocus, onSpanClick, searchMatchIds, t }: {
  model: TrajectoryTimelineModel;
  mode: "sequence" | "duration";
  focus: TrajectoryTimelineModel["focus"];
  onFocus: (focus: TrajectoryTimelineModel["focus"]) => void;
  onSpanClick: (recordId: string) => void;
  searchMatchIds: Set<string> | null;
  t: Translator;
}) {
  const barRef = useRef<HTMLDivElement | null>(null);
  const dragStartRef = useRef<number | null>(null);
  const [dragPreview, setDragPreview] = useState<{ start: number; end: number } | null>(null);
  const [hover, setHover] = useState<number | null>(null);

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
    setHover(fractionAt(event.clientX));
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

  const domain = model.t1 - model.t0;
  // 1131：模型域（序位/ms）→ 容器百分比的唯一换算口；DSH 渲染层同款。
  const leftPct = (value: number): number => ((value - model.t0) / Math.max(1e-9, domain)) * 100;
  const spanTitle = (span: { recordId: string; start: number; width: number; kind: TrajectoryKind; running: boolean; ttftFraction?: number }): string => {
    const parts: string[] = [];
    if (mode === "duration" && span.width > 0) {
      // duration 模型：span.width 即真实毫秒（1131 起无耗时记录 0 宽走 marker）。
      parts.push(formatTrajectoryDuration(span.width));
      if (span.ttftFraction != null) {
        parts.push(`TTFT ${formatTrajectoryDuration(span.ttftFraction * span.width)}`);
      }
    }
    if (span.running) parts.push(t("trajectory.running"));
    return parts.length > 0 ? parts.join(" · ") : t(KIND_LABEL_KEYS[span.kind]);
  };

  const focusLeft = focus ? leftPct(focus.start) : 0;
  const focusWidth = focus ? ((focus.end - focus.start) / Math.max(1e-9, domain)) * 100 : 0;

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
      onPointerLeave={() => setHover(null)}
    >
      {LANE_LABEL_KEYS.map((key, lane) => (
        <span key={key} className="traj-timeline__lane-label" style={{ top: `${lane * 14}px` }}>{t(key)}</span>
      ))}
      <div className="traj-timeline__plot">
        {model.spans.map((span) => {
          const rect = trajectorySpanRect(model, span);
          return (
            <span
              key={span.recordId}
              className={`traj-timeline__span traj-timeline__span--${span.kind}${span.running ? " traj-timeline__span--running" : ""}${span.failed ? " traj-timeline__span--failed" : ""}${searchMatchIds != null && !searchMatchIds.has(span.recordId) ? " traj-timeline__span--search-dim" : ""}`}
              style={{ left: `${rect.leftPct}%`, width: `max(2px, calc(${rect.widthPct}% - 2px))`, top: span.lane != null ? `${span.lane * 14}px` : "42px" }}
              title={spanTitle(span)}
              onClick={(event) => { event.stopPropagation(); onSpanClick(span.recordId); }}
            >
              {span.ttftFraction != null && (
                <span className="traj-timeline__ttft" style={{ width: `${span.ttftFraction * 100}%` }} />
              )}
            </span>
          );
        })}
        {model.markers.map((marker) => (
          <span
            key={marker.recordId}
            className={`traj-timeline__marker traj-timeline__marker--${marker.kind}${marker.failed ? " traj-timeline__marker--failed" : ""}${searchMatchIds != null && !searchMatchIds.has(marker.recordId) ? " traj-timeline__marker--search-dim" : ""}`}
            style={{ left: `${leftPct(marker.start)}%`, top: marker.lane != null ? `${marker.lane * 14}px` : "42px" }}
            title={spanTitle(marker)}
            onClick={(event) => { event.stopPropagation(); onSpanClick(marker.recordId); }}
          />
        ))}
        {model.turnBoundaries.map((boundary) => (
          <span key={`turn-${boundary.turn}`} className="traj-timeline__turn-line" style={{ left: `${leftPct(boundary.start)}%` }} title={t("trajectory.turn.label", { turn: boundary.turnLabel ?? boundary.turn })} />
        ))}
        {focus && <span className="traj-timeline__focus" style={{ left: `${focusLeft}%`, width: `${focusWidth}%` }} />}
        {dragPreview && dragPreview.end - dragPreview.start > 0.001 && (
          <span
            className="traj-timeline__drag"
            style={{ left: `${dragPreview.start * 100}%`, width: `${(dragPreview.end - dragPreview.start) * 100}%` }}
          />
        )}
        {hover != null && dragPreview == null && (
          <span className="traj-timeline__crosshair" style={{ left: `${hover * 100}%` }} />
        )}
      </div>
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
export function TrajectoryView({ items, running, hydrating, hasOlderHistory, loadingOlderHistory, olderHistoryExhausted, onLoadOlderHistory, t }: TrajectoryViewProps) {
  const ledger = useMemo(() => buildTrajectoryLedger(items), [items]);
  const summary = useMemo(() => trajectorySummary(ledger), [ledger]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [focus, setFocus] = useState<TrajectoryTimelineModel["focus"]>(null);
  const [windowTurns, setWindowTurns] = useState(WINDOW_TURNS);
  const [query, setQuery] = useState("");
  const [durationOn, setDurationOn] = useState(false);
  const [turnsCollapsed, setTurnsCollapsed] = useState(false);
  const [callsCollapsed, setCallsCollapsed] = useState(false);
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const pinnedRef = useRef(true);
  const anchorHeightRef = useRef<number | null>(null);

  // ⑤ 长历史窗口化: render only the tail window (last WINDOW_TURNS turns).
  // Records arrive in turn order, so the cut is a plain turn threshold.
  const windowedRecords = useMemo(() => {
    if (windowTurns >= ledger.turnCount) return ledger.records;
    const minTurn = ledger.turnCount - windowTurns + 1;
    return ledger.records.filter((record) => record.turn >= minTurn);
  }, [ledger, windowTurns]);
  const hiddenCount = ledger.records.length - windowedRecords.length;
  // 1131 时间轴投影（DSH 模式对齐）：默认 = sequence 等宽序位（每个事件 1/N
  // 等宽满铺，DSH TrajectoryView.tsx:347 两布尔默认 false → 'sequence' 同款）；
  // 「时长」开关 = duration 真实耗时占比（序位拼接天然压缩空闲，DSH
  // 'duration' 同款）。1112 默认的真实时间墙钟投影会把 span 稀释成细线（用户
  // 反馈「每个事件都很窄」的根因），已退出 UI（lib 保留）。两模式都只依赖
  // 记录顺序，历史无锚会话完整呈现（1111 结论）。
  const timeline = useMemo(() => buildTrajectorySequenceTimeline(windowedRecords, "sequence"), [windowedRecords]);
  const durationTimeline = useMemo(() => buildTrajectorySequenceTimeline(windowedRecords, "duration"), [windowedRecords]);
  const timelineModel = useMemo(() => (durationOn ? durationTimeline : timeline), [durationOn, timeline, durationTimeline]);

  // 1112 搜索索引：窗口内记录级倒排（纯内存拼接，React memo 缓存）。
  const searchIndex = useMemo(() => buildTrajectorySearchIndex(windowedRecords), [windowedRecords]);
  const searchMatchIds = useMemo(() => trajectorySearchMatches(searchIndex, query), [searchIndex, query]);
  // 拖选聚焦（DSH 同款）：区间外的台账行降透明；无锚记录永不落在区间内。
  const focusIds = useMemo(
    () => (focus && timelineModel ? recordIdsInFocus(timelineModel, focus) : null),
    [focus, timelineModel],
  );
  const rows = useMemo(
    () => projectTrajectoryRows(windowedRecords, { equalWidth: true, collapsedTurns: turnsCollapsed, collapsedCalls: callsCollapsed, searchMatchIds, focusIds }),
    [windowedRecords, turnsCollapsed, callsCollapsed, searchMatchIds, focusIds],
  );

  // Tail anchor: re-pin to the newest record while the reader sits at the
  // bottom (mount, new records, streaming growth). Scrolling up unpins. When
  // a window expand prepends content, restore the reader's position instead
  // (anchor to the previous scrollHeight).
  useLayoutEffect(() => {
    const node = scrollRef.current;
    if (!node) return;
    if (anchorHeightRef.current != null) {
      node.scrollTop += node.scrollHeight - anchorHeightRef.current;
      anchorHeightRef.current = null;
    } else if (pinnedRef.current) {
      node.scrollTop = node.scrollHeight;
    }
  }, [ledger, windowTurns, running]);

  const handleScroll = (): void => {
    const node = scrollRef.current;
    if (!node) return;
    pinnedRef.current = node.scrollHeight - node.scrollTop - node.clientHeight < 48;
  };

  const expandWindow = (): void => {
    anchorHeightRef.current = scrollRef.current?.scrollHeight ?? null;
    setWindowTurns((turns) => turns + WINDOW_TURNS);
  };

  const loadOlderFromBackend = (): void => {
    if (!hasOlderHistory || loadingOlderHistory) return;
    // Keep the current window covering the whole feed so freshly paged-in
    // turns render immediately instead of hiding behind the tail window.
    anchorHeightRef.current = scrollRef.current?.scrollHeight ?? null;
    setWindowTurns((turns) => turns + WINDOW_TURNS);
    void onLoadOlderHistory?.(undefined, "viewport-user");
  };

  const selectedItem = selectedId != null ? items.find((item) => item.id === selectedId) : undefined;
  const selectedRecord = selectedId != null ? ledger.records.find((record) => record.id === selectedId) : undefined;

  const body = hydrating
    ? <div className="traj__placeholder">{t("common.loading")}</div>
    : ledger.records.length === 0
      ? <div className="traj__empty">{t("trajectory.empty")}</div>
      : (
        <ol className="traj__ledger">
          {hiddenCount > 0 && (
            <li className="traj__load-older-row">
              <button type="button" className="traj__load-older" onClick={expandWindow}>
                {t("trajectory.loadOlder.window", { count: hiddenCount })}
              </button>
            </li>
          )}
          {hiddenCount === 0 && hasOlderHistory && !olderHistoryExhausted && (
            <li className="traj__load-older-row">
              <button type="button" className="traj__load-older" disabled={loadingOlderHistory} onClick={loadOlderFromBackend}>
                {loadingOlderHistory ? t("common.loading") : t("trajectory.loadOlder.history")}
              </button>
            </li>
          )}
          {rows.map((entry) => {
            if (entry.type === "turn" || entry.type === "calls") {
              return <TrajectoryRow key={`${entry.type}-${entry.type === "turn" ? entry.turn : entry.assistantId}`} entry={entry} selected={false} dimmed={false} onSelect={setSelectedId} t={t} />;
            }
            return (
              <TrajectoryRow key={entry.record.id} entry={entry} selected={entry.record.id === selectedId}
                dimmed={(focusIds != null && !focusIds.has(entry.record.id)) || (searchMatchIds != null && !searchMatchIds.has(entry.record.id))}
                onSelect={setSelectedId} t={t} />
            );
          })}
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
      <div className="traj__toolbar">
        <button type="button" className="traj__toggle" aria-pressed={durationOn} onClick={() => { setDurationOn((on) => !on); setFocus(null); }}>
          <span className="traj__toggle-icon" aria-hidden="true">⏱</span>
          {t("trajectory.toolbar.duration")}
        </button>
        <button type="button" className="traj__toggle" aria-pressed={turnsCollapsed} onClick={() => setTurnsCollapsed((on) => !on)}>
          {turnsCollapsed ? "⊟" : "⊞"} {t("trajectory.toolbar.turns")}
        </button>
        <button type="button" className="traj__toggle" aria-pressed={callsCollapsed} onClick={() => setCallsCollapsed((on) => !on)}>
          {callsCollapsed ? "⊟" : "⊞"} {t("trajectory.toolbar.calls")}
        </button>
        <input
          type="search"
          className="traj__search"
          value={query}
          placeholder={t("trajectory.search.placeholder")}
          aria-label={t("trajectory.search.label")}
          onChange={(event) => setQuery(event.target.value)}
        />
      </div>
      {timelineModel && <TrajectoryTimelineBar model={timelineModel} mode={durationOn ? "duration" : "sequence"} focus={focus} onFocus={setFocus} onSpanClick={setSelectedId} searchMatchIds={searchMatchIds} t={t} />}
      <div className="traj__scroll" ref={scrollRef} onScroll={handleScroll}>
        {body}
      </div>
      {selectedRecord && <TrajectoryInspector record={selectedRecord} item={selectedItem} onClose={() => setSelectedId(null)} t={t} />}
    </div>
  );
}
