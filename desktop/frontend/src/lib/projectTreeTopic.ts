import { asArray } from "./array";
import { getLocale, type DictKey, type Translator } from "./i18n";
import type { ProjectNode, ProjectTopicStatus } from "./types";

export type ProjectTreeVariant = "classic" | "workbench" | "creation";
export type WorkbenchOrganizeMode = "project" | "recent" | "time";
export type WorkbenchSortMode = "created" | "updated" | "color";

export const WORKBENCH_ORGANIZE_KEY = "projectTree:workbenchOrganize";
// Shared by classic and workbench; key string kept for existing saved choices.
export const WORKBENCH_SORT_KEY = "projectTree:workbenchSort";

export function loadWorkbenchOrganizeMode(): WorkbenchOrganizeMode {
  try {
    const value = localStorage.getItem(WORKBENCH_ORGANIZE_KEY);
    if (value === "recent" || value === "time") return value;
  } catch {
    /* localStorage unavailable */
  }
  return "project";
}

export function loadWorkbenchSortMode(): WorkbenchSortMode {
  try {
    const value = localStorage.getItem(WORKBENCH_SORT_KEY);
    if (value === "created") return "created";
    if (value === "color") return "color";
  } catch {
    /* localStorage unavailable */
  }
  return "updated";
}

export function isRuntimeSessionNode(node: ProjectNode): boolean {
  return node.kind === "session" || node.kind === "global_session";
}

export function isTopicNode(node: ProjectNode): boolean {
  return node.kind === "topic" || node.kind === "global_topic";
}

export function projectTreeRevisionIsFresh(currentRevision: number, incomingRevision: number): boolean {
  return incomingRevision >= currentRevision;
}

export function projectTreeTopicPageIsFresh(
  revisions: Readonly<Record<string, number>>,
  projectKey: string,
  incomingRevision: number,
): boolean {
  return projectTreeRevisionIsFresh(revisions[projectKey] ?? 0, incomingRevision);
}

// 741: only user-visible page loads may flip a folder's pagination indicator
// ("load more" ↔ "indexing history"). project-tree:changed-v2 rides every
// catalog revision bump — it keeps arriving while any session is active — so
// a background first-page reload onto a resident page must leave the
// indicator untouched; only an explicit append (the user's own click) or a
// first load with no resident page (the skeleton) may flip it. A resident
// page record always exists after the folder's first completed load.
export function projectTreePageLoadFlipsIndicator(
  pageState: { loading: boolean } | undefined,
  append: boolean,
): boolean {
  return append || !pageState;
}

// 962：普通浏览不再渲染「加载更多」分页按钮（对齐上游 main-v2 —— 上游浏览态
// 不渲染分页按钮，分页按钮只留给搜索结果）。浏览态会话列表的完整性改由
// loadProjectTopics 沿后端 nextCursor 自动续拉保证：首页返回后只要后端还给出
// 下一页游标就继续拉，直到取全。三条停机条件缺一不可：搜索态仍由用户显式
// 分页（保留的按钮）；空页必须停；游标不前进必须停（防后端游标异常时无限
// 续拉——同一游标反复请求会永远返回同一页）。
//
// 981 二轮（残余放大面 A，候选1 分场景）：962 的沿游标取全只在「用户主动展开 /
// 首次挂载 / 筛选变化」（full=true，默认）时执行；catalog 事件驱动的背景刷新
// 传 full=false —— 只要首页新鲜，恒不续拉。否则一次事件 flush 每个展开文件夹
// 都整轮循环取全（本机 83 会话 / limit 50 → 每文件夹 2 次 ListProjectTopics，
// 2× setTree），962 把单次刷新的成本放大成闪烁主源（调查报告 20261019 §3.1-A）。
// 搜索态/空页/游标不前进三条 962 停机条件原样保留。
export function projectTreeBrowsePageContinues(options: {
  query: string;
  requestCursor: string;
  nextCursor?: string;
  itemCount: number;
  full?: boolean;
}): boolean {
  if (options.full === false) return false;
  if (options.query.trim()) return false;
  if (!options.nextCursor) return false;
  if (options.itemCount <= 0) return false;
  if (options.nextCursor === options.requestCursor) return false;
  return true;
}

// Project shells come from desktop-projects.json and are valid even when the
// disposable catalog still reports revision 0. Catalog revision only gates
// topic pages and non-empty tree refreshes after the first shell is painted.
export function projectTreeShouldApplyShellSnapshot(options: {
  currentRevision: number;
  incomingRevision: number;
  treeEmpty: boolean;
}): boolean {
  if (options.treeEmpty) return true;
  return projectTreeRevisionIsFresh(options.currentRevision, options.incomingRevision);
}

// projectTreeTopicRowIdentity is the identity a folder's topic list dedupes
// on (task 1074). A conversation is ONE logical row keyed by its topicId, not
// by its backing session: the same topic legitimately has several physical
// session files (a collab contact file, a global-workspace copy, a recovery
// replica), and the catalog projects each under the SAME topic id. Keying the
// list on node.key therefore let the same conversation paint as multiple
// rows — the "shadow entries" a single logical session showed three times in
// the Global tree (数据层实测唯一: one catalog_topics row). Topic-less rows
// (runtime-only shells before a topic id lands) fall back to their key.
export function projectTreeTopicRowIdentity(node: ProjectNode): string | null {
  if (!isTopicNode(node) && !isRuntimeSessionNode(node)) return null;
  if (node.topicId) return `${node.kind.startsWith("global") ? "global" : "project"}:${node.topicId}`;
  return node.key;
}

function projectTreeTopicRowKey(node: ProjectNode): string {
  return projectTreeTopicRowIdentity(node) ?? node.key;
}

// projectTreeTopicRowsDeduped collapses same-identity rows within one list,
// keeping the first-seen row. The append path feeds it the incoming list so
// two pages that both carry the same topic (different backing sessions) cannot
// each contribute a shadow row.
function projectTreeTopicRowsDeduped(rows: ProjectNode[]): ProjectNode[] {
  const seen = new Set<string>();
  const out: ProjectNode[] = [];
  for (const row of rows) {
    const rowKey = projectTreeTopicRowKey(row);
    if (seen.has(rowKey)) continue;
    seen.add(rowKey);
    out.push(row);
  }
  return out;
}

export function mergeProjectTopicPage(current: ProjectNode[], incoming: ProjectNode[], append: boolean): ProjectNode[] {
  if (!append) {
    const incomingKeys = new Set(incoming.map(projectTreeTopicRowKey));
    // Project snapshots carry every pinned topic shell, while a lazy first
    // page is bounded. Keep off-page pins so expanding a busy project cannot
    // make its pinned section incomplete again.
    const offPagePins = current.filter((node) => Boolean(node.pinned) && !incomingKeys.has(projectTreeTopicRowKey(node)));
    return [...projectTreeTopicRowsDeduped(incoming), ...offPagePins];
  }
  // Task 1074: the append path merges by logical identity, not node.key — two
  // rows for the same topic (different backing sessions) collapse onto the
  // first-seen row instead of accumulating as shadow entries, both within the
  // incoming list and against the resident rows.
  const next = [...current];
  const positions = new Map(next.map((node, index) => [projectTreeTopicRowKey(node), index]));
  for (const node of projectTreeTopicRowsDeduped(incoming)) {
    const rowKey = projectTreeTopicRowKey(node);
    const index = positions.get(rowKey);
    if (index === undefined) {
      positions.set(rowKey, next.length);
      next.push(node);
    } else {
      next[index] = node;
    }
  }
  return next;
}

// A directory scan commits catalog rows in batches, but an incomplete page is
// not authoritative for replacement, deletion, timestamps, or order. Keep the
// last complete resident rows byte-for-byte and append only newly discovered
// keys until a complete page can replace the canonical first page.
export function mergeIncompleteProjectTopicPage(current: ProjectNode[], incoming: ProjectNode[]): ProjectNode[] {
  const residentKeys = new Set(current.map(projectTreeTopicRowKey));
  const discovered = incoming.filter((node) => !residentKeys.has(projectTreeTopicRowKey(node)));
  return discovered.length === 0 ? current : [...current, ...discovered];
}

// 981 二轮（候选1）：事件驱动的背景刷新只取首页，这个合并必须保住已取全文件夹
// 的尾部行——首页只有 limit 行，若按首页整体替换，超过一页的文件夹（本机 83
// 会话 / limit 50 → 2 页）会在每次事件后收缩到 50 行，本身就是一次可见跳变
// （闪烁回归）。合同按「首页是否已覆盖全量」分两支：
//   ·首页无 nextCursor（首页即全量列表）：整体替换，删除/重排照常生效——
//     背景刷新对一页内文件夹的行为与 962 前一致；
//   ·首页仍是窗口（nextCursor 存在）：首页行以新数据替换在前，不在首页的
//     驻留行按原序保留在后；这些行的变更等下一次全量加载（用户展开/重开）
//     收敛，与 741 背景刷新不翻分页指示器的降级口径一致。
export function mergeProjectTopicFirstPage(
  current: ProjectNode[],
  incoming: ProjectNode[],
  options: { nextCursor?: string },
): ProjectNode[] {
  if (!options.nextCursor) return mergeProjectTopicPage(current, incoming, false);
  const dedupedIncoming = projectTreeTopicRowsDeduped(incoming);
  const incomingKeys = new Set(dedupedIncoming.map(projectTreeTopicRowKey));
  const offPagePins = current.filter((node) => Boolean(node.pinned) && !incomingKeys.has(projectTreeTopicRowKey(node)));
  const pinKeys = new Set(offPagePins.map(projectTreeTopicRowKey));
  // Task 1074: the off-first-page resident tail keeps its rows unless their
  // identity is already on the incoming first page — a second physical
  // session for a topic the page just painted must not survive as a shadow.
  const residentTail = current.filter((node) => !incomingKeys.has(projectTreeTopicRowKey(node)) && !pinKeys.has(projectTreeTopicRowKey(node)));
  return [...dedupedIncoming, ...offPagePins, ...residentTail];
}

export function projectTreeTopicPageSignature(
  query: string,
  timeFilter: string,
  sortMode: WorkbenchSortMode,
  limit: number,
): string {
  return [query.trim(), timeFilter, sortMode, String(limit)].join("\u001f");
}

// Topic page loads rewrite children, so a signature keyed only on the project
// shells lets the debounced reload effect observe arrivals without re-arming
// itself on its own writes.
export function projectTreeShellSignature(tree: ProjectNode[]): string {
  return tree.map((node) => node.key).join("\u001f");
}

// After archive, drop that topic immediately so a shell-only refresh cannot
// resurrect it from the previously loaded children.
export function projectTreeWithoutTopic(tree: ProjectNode[], topicId: string): ProjectNode[] {
  const id = topicId.trim();
  if (!id) return tree;
  return projectTreeWithoutTopics(tree, new Set([id]));
}

// Post-commit archive IDs are a client-side tombstone overlay. Apply it to
// every incoming page as well as the resident tree so a pre-commit request
// cannot paint a topic back before the canonical reload acquires its sequence.
export function projectTreeWithoutTopics(tree: ProjectNode[], topicIds: ReadonlySet<string>): ProjectNode[] {
  if (topicIds.size === 0) return tree;
  let changed = false;
  const next: ProjectNode[] = [];
  for (const node of tree) {
    if (node.topicId && topicIds.has(node.topicId) && (isTopicNode(node) || isRuntimeSessionNode(node))) {
      changed = true;
      continue;
    }
    const children = asArray(node.children);
    const filteredChildren = projectTreeWithoutTopics(children, topicIds);
    if (filteredChildren !== children) {
      changed = true;
      next.push({ ...node, children: filteredChildren });
    } else {
      next.push(node);
    }
  }
  return changed ? next : tree;
}

// After a successful rename, paint the new label immediately instead of
// waiting for the catalog event round-trip.
export function projectTreeWithTopicTitle(tree: ProjectNode[], topicId: string, title: string): ProjectNode[] {
  const id = topicId.trim();
  if (!id) return tree;
  let changed = false;
  const next: ProjectNode[] = [];
  for (const node of tree) {
    if (node.topicId === id && (isTopicNode(node) || isRuntimeSessionNode(node))) {
      if (node.label !== title) {
        changed = true;
        next.push({ ...node, label: title });
      } else {
        next.push(node);
      }
      continue;
    }
    const children = asArray(node.children);
    const renamedChildren = projectTreeWithTopicTitle(children, id, title);
    if (renamedChildren !== children) {
      changed = true;
      next.push({ ...node, children: renamedChildren });
    } else {
      next.push(node);
    }
  }
  return changed ? next : tree;
}

export function projectTreeFolderKeyForTopic(tree: ProjectNode[], topicId: string): string {
  const id = topicId.trim();
  if (!id) return "";
  for (const node of tree) {
    if (node.kind !== "project" && node.kind !== "global_folder") continue;
    if (asArray(node.children).some((child) => child.topicId === id)) return node.key;
  }
  return "";
}

export function projectTreeFolderKeyForSession(tree: ProjectNode[], sessionPath: string): string {
  const path = sessionPath.trim();
  if (!path) return "";
  const containsSession = (nodes: ProjectNode[]): boolean => nodes.some((node) =>
    (isRuntimeSessionNode(node) && node.sessionPath?.trim() === path)
    || containsSession(asArray(node.children)),
  );
  for (const node of tree) {
    if (node.kind !== "project" && node.kind !== "global_folder") continue;
    if (containsSession(asArray(node.children))) return node.key;
  }
  return "";
}

export function invalidateProjectTreeTopicLoads(sequences: Record<string, number>, keys: Iterable<string>): void {
  for (const key of keys) sequences[key] = (sequences[key] ?? 0) + 1;
}

export function projectTreeShellChildren(
  previous: ProjectNode[] | undefined,
  pinnedShells: ProjectNode[] | undefined = [],
): ProjectNode[] {
  const shells = asArray(pinnedShells).filter((node) => isTopicNode(node) && Boolean(node.pinned));
  if (!previous || previous.length === 0) return shells;

  const shellByKey = new Map(shells.map((node) => [node.key, node]));
  const next = asArray(previous).map((node) => {
    if (!isTopicNode(node)) return node;
    const shell = shellByKey.get(node.key);
    if (!shell) return node.pinned ? { ...node, pinned: false } : node;
    shellByKey.delete(node.key);
    // Preserve the resident row's sortOrder: shells are metadata-only and a
    // zero-valued sortOrder would flatten manual ordering back to activity
    // sort (dragged order snapping back after refresh).
    return { ...node, ...shell, sortOrder: node.sortOrder ?? shell.sortOrder, children: node.children ?? shell.children };
  });
  return [...next, ...shellByKey.values()];
}

export function projectTreeEventAffectsFolder(project: ProjectNode, roots: string[]): boolean {
  if (roots.length === 0) return true;
  const root = project.kind === "global_folder" ? "" : project.root ?? "";
  return roots.includes(root);
}

export type ProjectTreeTopicOpenRequest = {
  scope: "global" | "project";
  workspaceRoot: string;
  topicId: string;
  sessionPath?: string;
};

export function projectTreeTopicOpenRequest(node: ProjectNode): ProjectTreeTopicOpenRequest | null {
  if (!isTopicNode(node) && !isRuntimeSessionNode(node)) return null;
  const scope = node.kind === "global_topic" || node.kind === "global_session" ? "global" : "project";
  return {
    scope,
    workspaceRoot: scope === "global" ? "" : node.root ?? "",
    topicId: node.topicId ?? "",
    sessionPath: node.sessionPath,
  };
}

export type ProjectTreeTopicClickTarget = {
  rowKey: string;
  canRename: boolean;
};

export type ProjectTreePendingTopicOpen = ProjectTreeTopicClickTarget & {
  timer: ReturnType<typeof setTimeout>;
};

export function projectTreeShouldSuppressOpenForRename(
  pending: ProjectTreeTopicClickTarget | null,
  next: ProjectTreeTopicClickTarget,
): boolean {
  return Boolean(pending && pending.rowKey === next.rowKey && pending.canRename && next.canRename);
}

export type ProjectTreeFolderDisclosure = {
  canExpand: boolean;
  isOpen: boolean;
  ariaExpanded?: boolean;
  iconStackClassName: string;
};

// allowEmptyExpand lets classic folders open without children so the expanded
// state can host the "no sessions" placeholder row; other variants keep the
// original contract where empty folders are inert.
export function projectTreeFolderDisclosure(hasChildren: boolean, isExpanded: boolean, allowEmptyExpand = false): ProjectTreeFolderDisclosure {
  const canExpand = hasChildren || allowEmptyExpand;
  const isOpen = canExpand && isExpanded;
  return {
    canExpand,
    isOpen,
    ariaExpanded: canExpand ? isExpanded : undefined,
    iconStackClassName: `project-tree__icon-stack${canExpand ? " project-tree__icon-stack--expandable" : ""}`,
  };
}

function topicMatchesActiveIdentity(node: ProjectNode, activeScope?: string, activeWorkspaceRoot?: string, activeTopicId?: string): boolean {
  if (!node.topicId || !activeTopicId) return false;
  const scope = node.kind === "global_topic" || node.kind === "global_session" ? "global" : "project";
  if (scope === "global") return activeScope === "global" && activeTopicId === node.topicId;
  return activeScope === "project" && activeTopicId === node.topicId && activeWorkspaceRoot === node.root;
}

export function topicIsActive(node: ProjectNode, activeScope?: string, activeWorkspaceRoot?: string, activeTopicId?: string, activeSessionPath?: string): boolean {
  if (isRuntimeSessionNode(node)) {
    return Boolean(node.sessionPath && activeSessionPath && activeSessionPath === node.sessionPath);
  }
  if (!isTopicNode(node)) return false;
  if (activeSessionPath && asArray(node.children).some(isRuntimeSessionNode)) return false;
  // Remote filesystem paths are not globally unique: two hosts can expose the
  // same absolute session path. Their synthesized rows already carry a
  // host-qualified topicId, so never let the generic path fallback mark a row
  // from another host active.
  if (node.remoteSession) return topicMatchesActiveIdentity(node, activeScope, activeWorkspaceRoot, activeTopicId);
  if (topicMatchesActiveIdentity(node, activeScope, activeWorkspaceRoot, activeTopicId)) return true;
  return Boolean(node.sessionPath && activeSessionPath && activeSessionPath === node.sessionPath);
}

export function projectTreeTopicMetaLine(node: ProjectNode, t: Translator, compact = false): string {
  const parts: string[] = [];
  const turns = node.turns ?? 0;
  if (node.turnsState === "unknown") parts.push(t("history.indexing"));
  else if (turns > 0) parts.push(t(turns === 1 ? "history.turnOne" : "history.turnOther", { n: turns }));
  const activityAt = node.lastActivityAt || node.createdAt || 0;
  if (activityAt) parts.push(topicActivityLabel(activityAt, t, compact));
  if (parts.length === 0) parts.push(t("projectTree.previously"));
  return parts.join(" · ");
}

// Model for the classic hover preview card: the row keeps a time-only meta
// line, so the card carries the full title, turns, exact date, and project.
export type ProjectTreeTopicHoverCard = {
  title: string;
  statusLabel: string;
  metaLine: string;
  exactTime: string;
  projectLabel: string;
  // 938：会话登记的职责（sidecar purpose，hover 时按需拉取；空=未登记，不渲染行）。
  purpose: string;
};

// Activity labels older than a week are already the calendar date (always the
// meta line's last part), so callers pairing the two keep a single copy.
export function projectTreeDedupedExactTime(metaLine: string, exactTime: string): string {
  return exactTime && metaLine.endsWith(exactTime) ? "" : exactTime;
}

export function projectTreeTopicHoverCardModel(node: ProjectNode, t: Translator, projectLabel: string, purpose = ""): ProjectTreeTopicHoverCard {
  const activityAt = node.lastActivityAt || node.createdAt || 0;
  const metaLine = projectTreeTopicMetaLine(node, t);
  const exactTime = activityAt ? topicActivityDateLabel(activityAt) : "";
  return {
    title: (node.preview || node.label || node.topicId || "Untitled").replace(/^●\s*/, ""),
    statusLabel: topicStatusLabel(node, t),
    metaLine,
    exactTime: projectTreeDedupedExactTime(metaLine, exactTime),
    projectLabel,
    purpose: purpose.trim(),
  };
}

export function topicUnknownTimeLabel(node: ProjectNode, t: Translator): string {
  return topicActivityAt(node) ? "" : t("projectTree.previously");
}

const topicStatusLabels: Record<ProjectTopicStatus, DictKey> = {
  thinking: "projectTree.status.thinking",
  finishing: "runtime.finishing",
  unknown: "runtime.unknown",
  cancelling: "status.jobStopping",
  streaming: "projectTree.status.streaming",
  waiting_confirmation: "projectTree.status.waitingConfirmation",
  background_job: "projectTree.status.backgroundJob",
  paused: "projectTree.status.paused",
  awaiting_delivery: "projectTree.status.awaitingDelivery",
  error: "projectTree.status.error",
  diverged_recovery: "projectTree.status.divergedRecovery",
};

export function normalizeTopicStatus(status?: string): ProjectTopicStatus | "" {
  if (status === "finishing" || status === "cancelling" || status === "unknown") return status;
  if (!status) return "";
  if (status === "thinking" || status === "streaming" || status === "waiting_confirmation" || status === "background_job" || status === "paused" || status === "awaiting_delivery" || status === "error" || status === "diverged_recovery") {
    return status;
  }
  return "";
}

export function topicStatus(node: ProjectNode): ProjectTopicStatus | "" {
  // Ordinary list never surfaces recovery-branch status. Active runtime states
  // only: thinking/streaming/waiting/etc. History owns other saved versions.
  const live = node.running ? "streaming" : "";
  const stored = normalizeTopicStatus(node.status);
  if (stored && stored !== "diverged_recovery") return stored;
  return live;
}

// Task 312: the sidebar status dot the member rows render (and therefore the
// exact set a collapsed group header aggregates). Kept as one predicate so the
// row condition (ProjectTree's showStatusInSide) and the group aggregation can
// never drift apart.
export function projectTreeStatusShowsDot(status: ProjectTopicStatus | ""): boolean {
  return status === "thinking" || status === "streaming" || status === "waiting_confirmation" || status === "background_job";
}

// Task 312: the dot a collapsed group header shows — the first member whose
// live status (topicStatus folds node.running into "streaming") qualifies, so
// every group aggregates independently and an all-idle group returns "".
export function projectTreeGroupDotStatus(members: ProjectNode[]): ProjectTopicStatus | "" {
  for (const member of members) {
    const status = topicStatus(member);
    if (projectTreeStatusShowsDot(status)) return status;
  }
  return "";
}

// Task 313: how many members the collapsed-group dot stands for. Same predicate
// (projectTreeStatusShowsDot) and same topicStatus pipeline as the dot, so the
// count and the dot live and die together — zero new push wiring.
export function projectTreeGroupActiveCount(members: ProjectNode[]): number {
  let count = 0;
  for (const member of members) {
    if (projectTreeStatusShowsDot(topicStatus(member))) count += 1;
  }
  return count;
}

export function projectTreeTopicArchiveBlocked(node: ProjectNode): boolean {
  if (node.status === "finishing" || node.status === "cancelling" || node.status === "unknown") return true;
  if (asArray(node.children).some(projectTreeTopicArchiveBlocked)) return true;
  const status = normalizeTopicStatus(node.status);
  if (status === "thinking" || status === "streaming" || status === "waiting_confirmation" || status === "background_job") return true;
  if (status === "paused" || status === "awaiting_delivery" || status === "error" || status === "diverged_recovery") return false;
  return Boolean(node.running);
}

export function topicStatusLabel(node: ProjectNode, t: Translator): string {
  const status = topicStatus(node);
  return status ? t(topicStatusLabels[status]) : "";
}

export function topicActivityAt(node: ProjectNode): number {
  return node.lastActivityAt || node.createdAt || 0;
}

export function projectTreeReadActivityKey(node: ProjectNode): string | null {
  const request = projectTreeTopicOpenRequest(node);
  if (!request?.topicId) return null;
  return [request.scope, request.workspaceRoot, request.topicId].join("\u001f");
}

export type ProjectTreeReadActivity = Record<string, number>;

export function projectTreeTopicHasUnreadActivity(
  node: ProjectNode,
  readActivity: ProjectTreeReadActivity,
  activeScope?: string,
  activeWorkspaceRoot?: string,
  activeTopicId?: string,
  activeSessionPath?: string,
  baselineAt = 0,
): boolean {
  if (!isTopicNode(node) && !isRuntimeSessionNode(node)) return false;
  if (topicIsActive(node, activeScope, activeWorkspaceRoot, activeTopicId, activeSessionPath)) return false;
  if (topicMatchesActiveIdentity(node, activeScope, activeWorkspaceRoot, activeTopicId)) return false;
  if (topicStatus(node) !== "") return false;
  const key = projectTreeReadActivityKey(node);
  const activityAt = topicActivityAt(node);
  if (!key || activityAt <= 0) return false;
  return Math.max(readActivity[key] ?? 0, baselineAt) < activityAt;
}

export function projectTreeShouldRenderTopicActions(isSessionNode: boolean, variant: ProjectTreeVariant, unread: boolean): boolean {
  return !isSessionNode && variant !== "creation" && !unread;
}

// Pinning reorders the classic/workbench trees shared with creation mode, so
// the creation context menu keeps its original rename/trash-only entries.
export function projectTreeTopicMenuOffersPin(variant: ProjectTreeVariant): boolean {
  return variant !== "creation";
}

export function topicActivityLabel(ms: number, t: Translator, compact = false): string {
  if (ms <= 0) return "";
  const delta = Date.now() - ms;
  const locale = getLocale();
  const minute = 60_000;
  const hour = 60 * minute;
  const day = 24 * hour;
  const month = 30 * day;
  const year = 365 * day;
  if (delta < minute) return t("projectTree.justNow");
  if (!compact) {
    const rtfLocale = locale === "zh" ? "zh-CN" : locale === "zh-TW" ? "zh-TW" : "en";
    const rtf = new Intl.RelativeTimeFormat(rtfLocale, { numeric: "auto" });
    if (delta < hour) return rtf.format(-Math.max(1, Math.round(delta / minute)), "minute");
    if (delta < day) return rtf.format(-Math.round(delta / hour), "hour");
    if (delta < 7 * day) return rtf.format(-Math.round(delta / day), "day");
    return topicActivityDateLabel(ms);
  }
  if (delta < hour) {
    const value = Math.max(1, Math.round(delta / minute));
    return locale === "zh" || locale === "zh-TW" ? `${value} 分钟` : `${value}m`;
  }
  if (delta < day) {
    const value = Math.round(delta / hour);
    return locale === "zh" || locale === "zh-TW" ? `${value} 小时` : `${value}h`;
  }
  if (delta < 7 * day) {
    const value = Math.round(delta / day);
    return locale === "zh" || locale === "zh-TW" ? `${value} 天` : `${value}d`;
  }
  if (delta < month) {
    const value = Math.round(delta / day);
    return locale === "zh" || locale === "zh-TW" ? `${value} 天` : `${value}d`;
  }
  if (delta < year) {
    const value = Math.max(1, Math.round(delta / month));
    return locale === "zh" || locale === "zh-TW" ? `${value} 个月` : `${value}mo`;
  }
  const value = Math.max(1, Math.round(delta / year));
  return locale === "zh" || locale === "zh-TW" ? `${value} 年` : `${value}y`;
}

export function topicActivityDateLabel(ms: number): string {
  if (ms <= 0) return "";
  const locale = getLocale();
  const dateLocale = locale === "zh" ? "zh-CN" : locale === "zh-TW" ? "zh-TW" : "en";
  return new Date(ms).toLocaleDateString(dateLocale);
}

// projectTreeTopicRecoveryCopyCount: placeholder for the (unimplemented)
// recovery-copy consolidation counter. The consolidate menu action calls the
// backend directly; this count was referenced by UI code that shipped before
// the helper existed (fork 1.34-era residue). Returns 0 until the feature
// lands; see fork task 11 follow-ups.
// projectTreeTopicRecoveryCopyCount is the folded recovery-copy badge count for
// a topic row. Runtime session rows and non-positive/missing counts render no
// badge; the copies themselves stay folded behind the canonical row (#8525).
export function projectTreeTopicRecoveryCopyCount(node: ProjectNode): number {
	if (!isTopicNode(node)) return 0;
	const count = node.recoveryCopyCount ?? 0;
	return count > 0 ? Math.floor(count) : 0;
}
