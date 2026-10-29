// tl1240: pure logic for the sidebar batch manage mode (multi-select → batch
// archive / batch move-to-group). Kept DOM-free and bridge-free so the
// selection rules and the result summarization are unit-testable without
// mounting the tree; the component wiring lives in ProjectTreeBatchBar.tsx and
// ProjectTree.tsx.
//
// Design (research report tl1240前置, route C): the whole selection goes to the
// backend in ONE bridge call — BatchTrashTopics / BatchMoveToGroup — so the
// global mutation gates are taken once and the batch cost stays ≈ single +
// (N-1)×(teardown+move). This module only shapes the input and interprets the
// per-topic outcomes the backend already isolated.

import type { BatchMoveToGroupResult, BatchTopicArchiveItem, BatchTopicArchiveResult, ProjectNode } from "./types";

/** A topic row eligible for batch operations: local, has a topic id, and is a
 * real topic (project topic or global topic — standalone session rows are
 * archived through DeleteSession, not the topic endpoints). */
export interface BatchTopicEntry {
  topicId: string;
  title: string;
  scope: "global" | "project";
  workspaceRoot: string;
}

export function batchEntryForNode(node: ProjectNode): BatchTopicEntry | null {
  if (!node.topicId) return null;
  if (node.kind !== "topic" && node.kind !== "global_topic") return null;
  if (node.remoteSession) return null;
  return {
    topicId: node.topicId,
    title: node.label,
    scope: node.kind === "global_topic" ? "global" : "project",
    workspaceRoot: node.kind === "global_topic" ? "" : (node.root ?? ""),
  };
}

/** Walk a tree snapshot in display order and collect every batch-eligible
 * topic — the selection universe for "select all visible" and the source of
 * titles for the failure list. */
export function collectBatchTopics(nodes: ProjectNode[], into: BatchTopicEntry[] = []): BatchTopicEntry[] {
  for (const node of nodes) {
    if (!node) continue;
    const entry = batchEntryForNode(node);
    if (entry) into.push(entry);
    if (node.children?.length) collectBatchTopics(node.children, into);
  }
  return into;
}

/** The move action needs one scope+workspaceRoot for the whole batch (the
 * backend endpoint signature is per-scope). Returns the common target when
 * every selected topic shares it, null otherwise — the UI disables move with
 * an explanatory hint instead of silently dropping topics. */
export function singleBatchTarget(entries: BatchTopicEntry[]): { scope: "global" | "project"; workspaceRoot: string } | null {
  if (entries.length === 0) return null;
  const first = entries[0];
  for (const entry of entries) {
    if (entry.scope !== first.scope || entry.workspaceRoot !== first.workspaceRoot) return null;
  }
  return { scope: first.scope, workspaceRoot: first.workspaceRoot };
}

export interface BatchTopicFailure {
  topicId: string;
  /** Display title; falls back to the raw id when the tree no longer knows it. */
  title: string;
  reason: string;
  /** true = the backend marked this failure forceable (active-work gate /
   * session-lease busy): the turn is running or the lease is held elsewhere,
   * so the topic was deliberately skipped — surfaced separately from real
   * failures and never fed to the force path from the batch UI. */
  skipped: boolean;
}

export interface BatchTopicArchiveSummary {
  succeeded: number;
  failed: number;
  /** forceable-class skips (turn running / lease busy). */
  skipped: number;
  total: number;
  failures: BatchTopicFailure[];
}

/** Resolve display titles for the report. Titles come from the selection
 * snapshot taken at action time (the tree may have reloaded by the time the
 * batch lands); unknown ids fall back to the raw id. */
function failureItems(items: BatchTopicArchiveItem[], titles: Map<string, string>): BatchTopicFailure[] {
  return items
    .filter((item) => !item.ok)
    .map((item) => ({
      topicId: item.topicId,
      title: titles.get(item.topicId) ?? item.topicId,
      reason: item.error ?? "",
      skipped: Boolean(item.forceable),
    }));
}

/** Interpret a BatchTrashTopics result for the report strip: counts plus the
 * failure list split into skips (running/blocked) and real failures. */
export function summarizeBatchTopicArchive(result: BatchTopicArchiveResult, titles: Map<string, string>): BatchTopicArchiveSummary {
  const failures = failureItems(result.items ?? [], titles);
  return {
    succeeded: result.succeeded,
    failed: failures.filter((f) => !f.skipped).length,
    skipped: failures.filter((f) => f.skipped).length,
    total: (result.items ?? []).length,
    failures,
  };
}

export interface BatchMoveSummary {
  succeeded: number;
  failed: number;
  total: number;
  failures: BatchTopicFailure[];
}

/** Same shape for the move result — no forceable class there (CAS races are
 * plain failures; the backend retries internally). */
export function summarizeBatchMove(result: BatchMoveToGroupResult, titles: Map<string, string>): BatchMoveSummary {
  const failures = failureItems(result.items ?? [], titles);
  return {
    succeeded: result.succeeded,
    failed: failures.length,
    total: (result.items ?? []).length,
    failures,
  };
}

/** After a successful archive the succeeded topics leave the selection (the
 * failed/skipped stay selected so a corrected retry is one click away). */
export function batchSelectionAfterArchive(previous: Set<string>, result: BatchTopicArchiveResult): Set<string> {
  const next = new Set(previous);
  for (const item of result.items ?? []) {
    if (item.ok) next.delete(item.topicId);
  }
  return next;
}

/** The batch bar's result strip model — one completed operation. */
export interface BatchReportState {
  kind: "archive" | "move";
  done: number;
  total: number;
  skipped: number;
  failures: BatchTopicFailure[];
}
