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
import type { ForegroundSubagentView, JobView, SubagentArtifactView } from "./types";
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

/** tl1435: how often the mounted panel re-pulls the persisted directory while
 *  nothing else triggers it. The transcript-count trigger cannot see a
 *  workflow-dispatched child (no session transcript card → no ended-count
 *  move), so this interval is what makes its running row appear — and its
 *  move to the ended section happen — live. Same cadence the task monitor
 *  panel polls its store (TaskMonitorPanel POLL_INTERVAL_MS). */
export const SUBAGENTS_PERSISTED_POLL_MS = 5000;

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
  /** tl1361: the transcript call id under which the child's live tool events
   *  are re-parented (nested_sink.go: Tool.ParentID = <dispatch call id>).
   *  The detail view's running tool stream looks the events up by it.
   *  Undefined means "the item id is the lookup key" — the transcript
   *  projection's case, where item.id IS the dispatch call id — and for a
   *  registry row whose source carries no call id (bash jobs) the lookup
   *  finds nothing and the detail stays preview-only. tl1387 二轮: task/fleet
   *  job rows stamp it too (JobView.parentToolCallId), so a background child
   *  whose transcript card is missing still gets the live work-process
   *  stream. */
  callId?: string;
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

// tl1361: the child's own live tool-call stream. The backend re-parents a
// running sub-agent's real tool events under the dispatching task call's id
// (internal/agent/nested_sink.go: Tool.ParentID = <task call id>, call ids
// namespaced as "<parent id>/<child id>"), so the transcript the window
// already holds carries the child's work process — the dock detail can render
// it without any backend change. Pure projection in transcript order; the
// running entry's own card never matches (its parentId points at the parent
// turn's assistant item, not at itself).
export function subagentChildTools(items: readonly Item[] | undefined, callId: string): SubagentDirectoryItem[] {
  if (!callId) return [];
  const out: SubagentDirectoryItem[] = [];
  for (const item of items ?? []) {
    if (item.kind !== "tool" || item.parentId !== callId) continue;
    out.push(item);
  }
  return out;
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
// tl1433: the prefix is a named constant — sidecarOnlyRunningEntries keys on
// it, so the projection and the predicate must share one source of truth.
const PERSISTED_ENTRY_ID_PREFIX = "persisted:";

function persistedDirectoryEntry(view: SubagentArtifactView): SubagentDirectoryEntry {
  const status: ToolStatus = view.status === "failed" ? "error" : view.status === "interrupted" ? "stopped" : "done";
  const item: SubagentDirectoryItem = {
    kind: "tool",
    id: `${PERSISTED_ENTRY_ID_PREFIX}${view.ref}`,
    // tl1209: description-first — the persisted sidecar's Name is the
    // profile/skill name ("ask", "task"), which makes every row read the
    // same; the dispatch description is what tells them apart. A record
    // without one keeps the old fallback chain (name → kind → "task"), so
    // nothing ever renders bare.
    name: view.description || view.name || view.kind || "task",
    args: "",
    readOnly: true,
    status,
    startedAt: view.createdAt || undefined,
    durationMs: view.updatedAt && view.createdAt ? Math.max(0, view.updatedAt - view.createdAt) : undefined,
    subject: view.description || view.name || undefined,
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

// tl1435: a persisted-only RUNNING entry. Same view-model convention as
// persistedDirectoryEntry (never enters the transcript; the "persisted:" id
// prefix keeps the row's React key stable when the same ref later flips to a
// terminal status and moves to the ended section), but projected as a running
// row: the sidecar meta now lands on disk at dispatch (agent.PrepareFresh
// saveMeta, PrepareContinue precedent), so the persisted directory carries
// live state too — and it is the ONLY source that sees a child whose dispatch
// never produces a session-transcript card (workflow node execution:
// internal/workflow runner.go calls TaskTool.Execute outside any turn, so
// neither the transcript projection nor the turn-scoped foreground registry
// ever sees it while running).
function persistedRunningDirectoryEntry(view: SubagentArtifactView): SubagentDirectoryEntry {
  const item: SubagentDirectoryItem = {
    kind: "tool",
    id: `${PERSISTED_ENTRY_ID_PREFIX}${view.ref}`,
    // tl1209 semantics, same as the ended twin: the dispatch description
    // distinguishes same-profile children; the profile name is the fallback.
    name: view.description || view.name || view.kind || "task",
    args: "",
    readOnly: true,
    status: "running",
    startedAt: view.createdAt || undefined,
    subject: view.description || view.name || undefined,
  };
  return { item, running: true, status: "running", ref: view.ref };
}

/** tl1435 (运行中区收工作流子代理): fold RUNNING persisted sidecar views into
 *  the running section — the two sections of the panel now draw from the same
 *  ListSubagentsByParent source the ended section already reads (557-style
 *  single-source convergence), so a workflow-dispatched child is visible
 *  while it runs and moves to the ended section when its meta flips terminal.
 *  Transcript entries win on ref collisions (a regular task child has both a
 *  card and a running meta; the card carries the richer live state), so the
 *  merge changes nothing for children the transcript already represents.
 *  Pure: returns the input directory when there is nothing to add. */
export function mergePersistedRunningSubagents(directory: SubagentDirectory, views: readonly SubagentArtifactView[] | undefined): SubagentDirectory {
  const runningViews = (views ?? []).filter((view) => view.status === "running" && view.ref);
  if (runningViews.length === 0) return directory;
  const known = new Set<string>();
  for (const entry of directory.running) {
    if (entry.ref) known.add(entry.ref);
    known.add(entry.item.id);
  }
  const extra = runningViews
    .filter((view) => !known.has(view.ref))
    .map(persistedRunningDirectoryEntry);
  if (extra.length === 0) return directory;
  return { running: [...extra, ...directory.running], ended: directory.ended };
}

/** 任务495 剩余项 (用法引导): the dock tab label surfaces the ended count so
 *  ended content is visible at the entry before the tab is opened. */
export function subagentsTabLabel(base: string, endedCount: number): string {
  return endedCount > 0 ? `${base} · ${endedCount}` : base;
}

// tl1387: a second, backend-owned running source for the dock panel (the
// zcode form: the directory's running list must never depend on one data
// path). The transcript projection above is the rich source (progress
// preview, nested tool stream, terminate) — but it only knows about a child
// while the session transcript holds its dispatch card, and that card is a
// live-reducer artifact: a gap repair stall, a tail-first hydration window or
// a speculative-rollback can leave the panel blind while the child is
// demonstrably running (the capsule's job/foreground registries see it —
// tl1387 第一层/第二层两层都断在「列表只读转录」这一单源上). The registries
// the capsule already merges (mergeCapsuleWork: controller jobs + the
// process-local foreground registry from emitSubagentLifecycle telemetry)
// are the same breadth the 557/440 contract gives the capsule — projected
// here as preview-only running rows.

/** RegistryRunningSource is what App already holds for the ACTIVE tab: the
 *  controller's background sub-agent jobs plus the foreground registry rows
 *  bound to the same tab. bash jobs are not sub-agents and never enter. */
export interface RegistryRunningSource {
  jobs?: readonly JobView[];
  foreground?: readonly ForegroundSubagentView[];
}

// tl1433: named so the job-twin guard (sidecarOnlyRunningEntries) keys on the
// same string the projection below writes.
const REGISTRY_JOB_ID_PREFIX = "registry:job:";

/** One registry-projected running entry. `ref` is stamped only for
 *  foreground rows (the child's real transcript ref — the same string
 *  CancelSubagentForTab and ReadSubagentSession take); background job rows
 *  carry the job id as the item id only, so they render without a terminate
 *  button or a transcript read instead of aiming those bridges at a job id
 *  that is not a ref. tl1361: a foreground row whose lifecycle telemetry
 *  carried the dispatch call id (ForegroundSubagentView.parentToolCallId,
 *  task 667) stamps it as `callId`, so the detail view's running tool stream
 *  can look the child's re-parented tool events up in the transcript even
 *  while the card itself is missing (the stall this registry row exists
 *  for). tl1387 二轮: task/fleet job rows stamp the same dispatch call id
 *  (JobView.parentToolCallId, jobs.View.ParentToolCallID) — the backend now
 *  carries it, so a background child whose card is missing gets the live
 *  work-process stream too; bash jobs never do. */
export function registryRunningEntries(source: RegistryRunningSource | undefined, tabId: string): SubagentDirectoryEntry[] {
  const out: SubagentDirectoryEntry[] = [];
  if (!source) return out;
  const seen = new Set<string>();
  const push = (id: string, label: string, startedAt: number | undefined, ref: string | undefined, callId?: string) => {
    if (!id || seen.has(id)) return;
    seen.add(id);
    const item: SubagentDirectoryItem = {
      kind: "tool",
      id,
      // tl1209 semantics: the dispatch description tells same-profile
      // children apart; the profile name is the fallback.
      name: label || "task",
      args: "",
      readOnly: true,
      status: "running",
      startedAt: startedAt || undefined,
      subject: label || undefined,
    };
    out.push({ item, running: true, status: "running", ...(ref ? { ref } : {}), ...(callId ? { callId } : {}) });
  };
  for (const job of source.jobs ?? []) {
    if (!job || job.kind === "bash") continue;
    push(`${REGISTRY_JOB_ID_PREFIX}${job.id}`, job.label, job.startedAt, undefined, job.parentToolCallId || undefined);
  }
  for (const entry of source.foreground ?? []) {
    if (!entry) continue;
    // Only the active tab's rows belong in this session's directory; foreign
    // tabs carry their origin in the capsule instead (task 557 contract).
    if (tabId && entry.tabId && entry.tabId !== tabId) continue;
    push(`registry:fg:${entry.ref}`, entry.description || entry.name || entry.ref, entry.startedAt, entry.ref, entry.parentToolCallId || undefined);
  }
  return out;
}

/** tl1387: fold registry-projected running rows into the transcript
 *  projection. Transcript entries win on ref collisions (they carry the
 *  richer live state); a registry row whose ref (or id, for ref-less job
 *  rows) is not represented is appended. tl1361: a registry row's `callId`
 *  (the dispatch call id) also dedupes against a transcript card's item id —
 *  a card that lost its progress-preview ref (hydration window) still means
 *  "this child is represented", so the registry twin must not double the
 *  row. Pure: returns the input directory when there is nothing to add. */
export function mergeRegistryRunningSubagents(directory: SubagentDirectory, registry: readonly SubagentDirectoryEntry[] | undefined): SubagentDirectory {
  if (!registry || registry.length === 0) return directory;
  const known = new Set<string>();
  for (const entry of directory.running) {
    if (entry.ref) known.add(entry.ref);
    known.add(entry.item.id);
  }
  const extra = registry.filter((entry) => {
    if (entry.ref && known.has(entry.ref)) return false;
    if (entry.callId && known.has(entry.callId)) return false;
    return !known.has(entry.item.id);
  });
  if (extra.length === 0) return directory;
  return { running: [...extra, ...directory.running], ended: directory.ended };
}

// tl1433 四缺口③：「仅 sidecar 可见」的运行中条目——工作流派发的子代理就是
// 这一类（tl1435：不进会话转录、不进轮次登记表，只有 sidecar running meta）。
// 判定 = 由 sidecar 投影（persisted: 前缀）+ 不是会话自己派发的孪生：
//   - 前台登记表里有同 ref 的（转录卡片缺窗的前台子代理）不算；
//   - 后台 job 行不带 ref，按「同一次派发同时落 job 与 sidecar meta」认孪生：
//     有运行中的非 bash job 起点与该条目派发时刻相差在窗口内，就当它是那个
//     job 的子代理（宁可平铺，不错挂到工作流下）。

/** job 与其 sidecar meta 同次派发落地的时刻差上限（jobs.Manager 起 job →
 *  goroutine 内 PrepareFresh 落 meta，实测毫秒级；取 3s 容余，误差方向只会
 *  让工作流子代理留在顶层，不会错挂）。 */
export const JOB_TWIN_WINDOW_MS = 3000;

/** 派发时刻是否贴着某个运行中后台 job 的起点（= 该 job 的孪生记录）。 */
export function hasJobTwin(createdAt: number | undefined, jobStarts: readonly number[]): boolean {
  if (!createdAt) return false;
  return jobStarts.some((start) => Boolean(start) && Math.abs(start - createdAt) <= JOB_TWIN_WINDOW_MS);
}

export function sidecarOnlyRunningEntries(
  running: readonly SubagentDirectoryEntry[],
  registry: readonly SubagentDirectoryEntry[] | undefined,
): SubagentDirectoryEntry[] {
  const registryRefs = new Set<string>();
  const jobStarts: number[] = [];
  for (const entry of registry ?? []) {
    if (entry.ref) registryRefs.add(entry.ref);
    if (entry.item.id.startsWith(REGISTRY_JOB_ID_PREFIX) && entry.item.startedAt) jobStarts.push(entry.item.startedAt);
  }
  return running.filter((entry) =>
    entry.running
    && entry.item.id.startsWith(PERSISTED_ENTRY_ID_PREFIX)
    && !(entry.ref && registryRefs.has(entry.ref))
    && !hasJobTwin(entry.item.startedAt, jobStarts));
}
