import type { TaskSnapshot } from "./types";

// tl1483: task list refresh semantics — stale-while-revalidate with refresh
// reason classification. The task list is a 5s polling surface; every refresh
// path must keep painted rows mounted (no clear→loading→backfill), row order
// must be decoupled from the order the backend happened to return this poll,
// and an idle poll must not churn the painted list at all.

/** Why one task-list fetch was issued. Drives how the page is applied. */
export type TaskListRefreshReason = "mount" | "poll" | "manual" | "action" | "filter";

export type TaskListRow = TaskSnapshot & {
  __projectKey: string;
  __projectLabel: string;
  __catalogKey: string;
};

function recencyMs(value: string | undefined): number {
  if (!value) return 0;
  const parsed = Date.parse(value);
  return Number.isNaN(parsed) ? 0 : parsed;
}

/**
 * Deterministic total order: updated_at desc, then created_at desc, then
 * catalog key ascending. Rows with equal recency keep one stable position no
 * matter which order the backend returned them in (排序稳定钉).
 */
export function taskMonitorRecencyCompare(a: TaskListRow, b: TaskListRow): number {
  const recency = recencyMs(b.updated_at) - recencyMs(a.updated_at);
  if (recency !== 0) return recency;
  const created = recencyMs(b.created_at) - recencyMs(a.created_at);
  if (created !== 0) return created;
  if (a.__catalogKey === b.__catalogKey) return 0;
  return a.__catalogKey < b.__catalogKey ? -1 : 1;
}

export function sortTaskMonitorRows(rows: readonly TaskListRow[]): TaskListRow[] {
  return [...rows].sort(taskMonitorRecencyCompare);
}

/**
 * Full-field row equality. version is compared too: it feeds expectedVersion
 * in stop/requeue requests, so a skipped write must never hide a version bump
 * (that would make the next action request stale).
 */
function rowEquals(current: TaskListRow, next: TaskListRow): boolean {
  return current.task_id === next.task_id
    && current.__catalogKey === next.__catalogKey
    && current.__projectKey === next.__projectKey
    && current.__projectLabel === next.__projectLabel
    && current.session_id === next.session_id
    && current.state === next.state
    && current.runtime_state === next.runtime_state
    && current.version === next.version
    && current.created_at === next.created_at
    && current.updated_at === next.updated_at
    && (current.error_code ?? "") === (next.error_code ?? "")
    && (current.error_summary ?? "") === (next.error_summary ?? "");
}

/** Order-independent row-set equality keyed by the stable catalog key. */
export function taskMonitorRowsEqual(current: readonly TaskListRow[], next: readonly TaskListRow[]): boolean {
  if (current.length !== next.length) return false;
  const nextByKey = new Map(next.map((row) => [row.__catalogKey, row]));
  for (const row of current) {
    const counterpart = nextByKey.get(row.__catalogKey);
    if (!counterpart || !rowEquals(row, counterpart)) return false;
  }
  return true;
}

/**
 * Refresh-reason matrix:
 * - "poll" re-paints only when row content actually changed — an idle poll
 *   keeps the previous array reference, so React bails out and nothing
 *   re-renders;
 * - every other reason ("mount" | "manual" | "action" | "filter") applies the
 *   page eagerly — the caller asked for fresh data on purpose.
 * Both paths apply IN PLACE: rows are keyed by the stable catalog key, so
 * React reconciles instead of remounting, and painted rows survive every
 * refresh (无闪钉). The component never blanks a painted list behind a
 * loading branch — that gate lives in the render, not here.
 */
export function applyTaskMonitorRefresh(
  current: readonly TaskListRow[],
  next: readonly TaskListRow[],
  reason: TaskListRefreshReason,
): TaskListRow[] {
  // Same-reference returns are deliberate: with the identical array React
  // skips the state update entirely, so an idle poll costs zero renders.
  if (reason === "poll" && taskMonitorRowsEqual(current, next)) return current as TaskListRow[];
  return next as TaskListRow[];
}
