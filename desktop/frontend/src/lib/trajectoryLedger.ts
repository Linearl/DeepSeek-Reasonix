// 任务 704 — 轨迹视图读投影（DSH 同款，A 路线）。
//
// 轨迹 = 共享会话事件流之上的纯投影视图（597 调研唯一纪律）：本模块只消费
// controller/transcript 的读投影（Item[]），不直读存储、不改快照。v3 数据里
// turn 边界/工具链/compaction 都是一等条目；turn 级 token usage/TTFT 不落盘
// ——历史会话优雅降级（无用量列、无 TTFT 段），不做任何虚构。
//
// 时间锚语义：记录自身带时间（user/assistant createdAt、live tool startedAt）
// 时 atKnown=true；否则继承序列中最近一条带时间记录的时间（atKnown=false，
// 台账显示「≈」）。首个带时间记录之前的记录无锚（at=undefined），时间线不画。

import type { Item } from "./useController";

export type TrajectoryKind = "user" | "assistant" | "tool" | "compaction" | "notice" | "phase";

export interface TrajectoryRecord {
  /** Source item id (stable within the loaded window). */
  id: string;
  /** Position in the source items array. */
  index: number;
  kind: TrajectoryKind;
  /** 1-based turn ordinal within the loaded window (records before the first user item sit in turn 0). */
  turn: number;
  /** Global turn number when the user item carries historyTurn; undefined otherwise. */
  turnLabel?: number;
  /** True on the user record that opens a turn. */
  turnStart: boolean;
  /** Assistant-step ordinal inside the turn (0 = before the turn's first assistant record). */
  step: number;
  /** Epoch-ms time anchor (own or inherited); undefined before the first anchored record. */
  at?: number;
  /** False when `at` was inherited from a preceding record rather than measured. */
  atKnown: boolean;
  /** Known duration (assistant workDurationMs, live tool durationMs); absent = never invent one. */
  durationMs?: number;
  /** Assistant time-to-first-token (live-only reasoningDurationMs); history degrades to absent. */
  ttftMs?: number;
  failed: boolean;
  /** Notice code / tool error marker for failed records. */
  errorCode?: string;
  /** One-line label (tool name, compaction trigger, record kind). */
  title: string;
  /** Short collapsed preview of the payload. */
  preview: string;
  /** Tool nested under a subagent `task` call. */
  nested: boolean;
  /** Tool status / notice level, for row chips. */
  status?: string;
  /** Streaming assistant or running tool — draw a start marker, never a duration. */
  running: boolean;
}

export interface TrajectoryLedger {
  records: TrajectoryRecord[];
  /** Turn count in the loaded window (turn 0 for pre-first-user records not counted). */
  turnCount: number;
  /** First/last global turn labels when known. */
  firstTurnLabel?: number;
  lastTurnLabel?: number;
}

const PREVIEW_MAX = 160;

function preview(text: string | undefined, max = PREVIEW_MAX): string {
  const cleaned = (text ?? "").replace(/\s+/g, " ").trim();
  return cleaned.length <= max ? cleaned : cleaned.slice(0, max - 1) + "…";
}

function assistantPreview(text: string, reasoning: string): string {
  return preview(text) || preview(reasoning, 80);
}

/**
 * Project transcript items into the trajectory ledger. Pure: same items in,
 * same ledger out. Streaming/running records carry no duration (DSH rule:
 * never invent one).
 */
export function buildTrajectoryLedger(items: Item[]): TrajectoryLedger {
  const records: TrajectoryRecord[] = [];
  let turn = 0;
  let step = 0;
  let lastAt: number | undefined;
  let firstTurnLabel: number | undefined;
  let lastTurnLabel: number | undefined;

  for (let index = 0; index < items.length; index += 1) {
    const item = items[index];
    const base = { id: item.id, index, atKnown: false, failed: false, nested: false, running: false, preview: "", turnStart: false };
    switch (item.kind) {
      case "user": {
        turn += 1;
        step = 0;
        const turnLabel = item.historyTurn;
        if (turnLabel != null) {
          firstTurnLabel ??= turnLabel;
          lastTurnLabel = turnLabel;
        }
        const at = item.createdAt;
        if (at != null) lastAt = at;
        records.push({
          ...base,
          kind: "user",
          turn,
          step: 0,
          turnLabel,
          turnStart: true,
          at,
          atKnown: at != null,
          title: "user",
          preview: preview(item.submitText || item.text, 120),
          failed: item.failed === true,
        });
        break;
      }
      case "assistant": {
        step += 1;
        const running = item.streaming === true;
        const at = (item as { createdAt?: number }).createdAt;
        if (at != null) lastAt = at;
        const durationMs = running ? undefined : item.workDurationMs;
        const ttftMs = item.reasoningDurationMs;
        records.push({
          ...base,
          kind: "assistant",
          turn,
          step,
          at,
          atKnown: at != null,
          durationMs,
          ttftMs: !running && ttftMs && durationMs && ttftMs < durationMs ? ttftMs : undefined,
          title: "assistant",
          preview: assistantPreview(item.text, item.reasoning),
          running,
        });
        break;
      }
      case "tool": {
        const failed = item.status === "error";
        const running = item.status === "running";
        if (item.startedAt != null) lastAt = item.startedAt;
        const at = item.startedAt ?? lastAt;
        records.push({
          ...base,
          kind: "tool",
          turn,
          step,
          at,
          atKnown: item.startedAt != null,
          durationMs: running ? undefined : item.durationMs,
          title: item.name,
          preview: preview(item.summary || item.subject || item.args, 120),
          failed,
          errorCode: failed ? "tool_error" : undefined,
          nested: item.parentId != null,
          status: item.status,
          running,
        });
        break;
      }
      case "compaction": {
        records.push({
          ...base,
          kind: "compaction",
          turn,
          step,
          at: lastAt,
          title: "compaction",
          preview: preview(item.summary, 120),
          status: item.pending ? "running" : "done",
          running: item.pending === true,
        });
        break;
      }
      case "notice": {
        records.push({
          ...base,
          kind: "notice",
          turn,
          step,
          at: lastAt,
          title: item.code || `notice_${item.level}`,
          preview: preview(item.detail || item.text, 120),
          failed: item.level === "warn",
          errorCode: item.level === "warn" ? item.code ?? "warn" : undefined,
          status: item.level,
        });
        break;
      }
      case "phase": {
        records.push({ ...base, kind: "phase", turn, step, at: lastAt, title: "phase", preview: preview(item.text, 120) });
        break;
      }
    }
  }

  return { records, turnCount: turn, firstTurnLabel, lastTurnLabel };
}

/** Assistant/tool/compaction counts — the ledger header summary line. */
export function trajectorySummary(ledger: TrajectoryLedger): { turns: number; assistants: number; tools: number; failed: number } {
  let assistants = 0;
  let tools = 0;
  let failed = 0;
  for (const record of ledger.records) {
    if (record.kind === "assistant") assistants += 1;
    if (record.kind === "tool") tools += 1;
    if (record.failed) failed += 1;
  }
  return { turns: ledger.turnCount, assistants, tools, failed };
}
