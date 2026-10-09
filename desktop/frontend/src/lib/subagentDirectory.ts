// Task 495: in-memory subagent directory for the right-dock "子代理" tab.
//
// The session transcript already carries every subagent card (live progress
// preview on newly dispatched tools, name+status on cards hydrated from
// history — the in-memory progress preview itself is never persisted). This
// module projects those tool items into the zcode-style two-section shape the
// dock panel renders: a running section and an ended section, newest first.
// Pure and synchronous on purpose: the data source is the transcript the
// window already holds, so no backend paging endpoint is needed — the
// 20-per-page rule (task 495 ④) applies to what the panel reveals, not to
// what has to be fetched.

import { parseSubagentOutcomeText } from "./subagentOutcome";
import type { SubagentArtifactView } from "./types";
import { isTerminalSubagentPhase, type Item, type SubagentPhase, type ToolStatus } from "./useController";

export type SubagentDirectoryItem = Extract<Item, { kind: "tool" }>;

// Superset of the transcript's subagent card names: the progress-card tools
// (useController's private SUBAGENT_PROGRESS_TOOLS) plus the agent-shaped
// tools ToolCard presents with subagent chrome (ToolCard's SUBAGENT_TOOLS).
const SUBAGENT_DIRECTORY_TOOLS: readonly string[] = [
  "task", "read_only_task", "parallel_tasks", "fleet",
  "run_skill", "explore", "research", "review", "security_review",
];

export function isSubagentToolName(name: string | undefined): boolean {
  return !!name && SUBAGENT_DIRECTORY_TOOLS.includes(name);
}

/** Task 495 ④: the ended list starts at 20 rows; "show more" reveals +20. */
export const SUBAGENT_DIRECTORY_PAGE_SIZE = 20;

export type SubagentDirectoryEntry = {
  item: SubagentDirectoryItem;
  running: boolean;
  phase?: SubagentPhase;
  status: ToolStatus;
  /** 任务440: the child's persisted transcript ref, when one is known —
   *  running entries latch it from the progress preview stream, ended entries
   *  from the result's subagent outcome. The dock live view reads the
   *  transcript through it; undefined (ephemeral run, ref never seen) keeps
   *  the entry preview-only. */
  ref?: string;
};

export type SubagentDirectory = {
  running: SubagentDirectoryEntry[];
  ended: SubagentDirectoryEntry[];
};

/** Terminal phase wins over a stale running status (useController reconciles
 *  the two, but the directory must not depend on event ordering). */
function entryRunning(item: SubagentDirectoryItem): boolean {
  if (item.subagentProgress && isTerminalSubagentPhase(item.subagentProgress.phase)) return false;
  return item.status === "running";
}

const byStartedDesc = (a: SubagentDirectoryEntry, b: SubagentDirectoryEntry): number =>
  (b.item.startedAt ?? 0) - (a.item.startedAt ?? 0);

// 任务495 剩余项: a hydrated (post-restart) card carries no live ref fields —
// but the persisted tool RESULT text still holds the "Subagent reference:"
// header (lib/subagentOutcome), so the ref is recoverable client-side. This
// keeps hydrated entries dedupe-able against the persisted directory and lets
// the 507 plan-A detail read their transcript through the 440 bridge.
function entryRef(item: SubagentDirectoryItem): string | undefined {
  return item.subagentProgress?.ref || item.subagentOutcome?.[0] || parseSubagentOutcomeText(item.output)?.[0] || undefined;
}

export function buildSubagentDirectory(items: readonly Item[] | undefined): SubagentDirectory {
  const running: SubagentDirectoryEntry[] = [];
  const ended: SubagentDirectoryEntry[] = [];
  for (const item of items ?? []) {
    if (item.kind !== "tool") continue;
    if (!item.subagentProgress && !isSubagentToolName(item.name)) continue;
    const entry: SubagentDirectoryEntry = {
      item,
      running: entryRunning(item),
      phase: item.subagentProgress?.phase,
      status: item.status,
      ref: entryRef(item),
    };
    (entry.running ? running : ended).push(entry);
  }
  running.sort(byStartedDesc);
  ended.sort(byStartedDesc);
  return { running, ended };
}

// 任务495 剩余项: a persisted-only ended entry. The synthesized tool item is a
// view-model — it never enters the transcript — carrying exactly the fields
// the dock rows/detail already read (subject → title, summary → outcome,
// startedAt/durationMs from the sidecar timestamps). The "persisted:" id
// prefix namespaces it away from real card ids.
function persistedDirectoryEntry(view: SubagentArtifactView): SubagentDirectoryEntry {
  const status: ToolStatus = view.status === "failed" ? "error" : view.status === "interrupted" ? "stopped" : "done";
  const item: SubagentDirectoryItem = {
    kind: "tool",
    id: `persisted:${view.ref}`,
    name: view.name || view.kind || "task",
    args: "",
    readOnly: true,
    status,
    startedAt: view.createdAt || undefined,
    durationMs: view.updatedAt && view.createdAt ? Math.max(0, view.updatedAt - view.createdAt) : undefined,
    subject: view.name || undefined,
    summary: view.outcome || undefined,
  };
  return { item, running: false, status, ref: view.ref };
}

/**
 * 任务495 剩余项 (已结束列表在右栏的可见性): fold the persisted sidecar
 * directory (desktop ListSubagentsByParent — survives restart, compaction and
 * archived outputs) into the transcript-projected ended section. Transcript
 * entries win: they carry the richer in-memory state, so a view whose ref is
 * already represented is dropped; a view with no transcript entry (compacted
 * card, archived output) is appended as a persisted-only row. Still-running
 * records never enter the ended list — the running section owns live state.
 * Pure: returns a new directory, input untouched.
 */
export function mergeEndedSubagentRecords(directory: SubagentDirectory, views: readonly SubagentArtifactView[] | undefined): SubagentDirectory {
  const knownRefs = new Set(directory.ended.map((entry) => entry.ref).filter(Boolean) as string[]);
  const persisted = (views ?? [])
    .filter((view) => view.status !== "running" && !knownRefs.has(view.ref))
    .map(persistedDirectoryEntry);
  if (persisted.length === 0) return directory;
  const ended = [...directory.ended, ...persisted].sort(byStartedDesc);
  return { running: directory.running, ended };
}

/** 任务495 剩余项 (用法引导): the dock tab label surfaces the ended count so
 *  ended content is visible at the entry before the tab is opened. */
export function subagentsTabLabel(base: string, endedCount: number): string {
  return endedCount > 0 ? `${base} · ${endedCount}` : base;
}
