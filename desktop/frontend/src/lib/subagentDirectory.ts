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
      ref: item.subagentProgress?.ref || item.subagentOutcome?.[0] || undefined,
    };
    (entry.running ? running : ended).push(entry);
  }
  running.sort(byStartedDesc);
  ended.sort(byStartedDesc);
  return { running, ended };
}
