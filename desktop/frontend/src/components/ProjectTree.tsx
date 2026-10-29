import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import type { CSSProperties, DragEvent as ReactDragEvent, KeyboardEvent as ReactKeyboardEvent, MouseEvent as ReactMouseEvent, ReactNode } from "react";
import { Archive, CheckCheck, ClipboardPen, Pencil, Plus, Folder, FolderPlus, Search, BriefcaseBusiness, Copy, FolderOpen, XCircle, Check, FileJson, FileText, ListCollapse, ListRestart, MessageSquare, Pin, MoreHorizontal, GitBranch, Sparkles, Cloud, SwatchBook, FolderInput, Filter, Server } from "lucide-react";
import { asArray } from "../lib/array";
import { useToast } from "../lib/toast";
import { app } from "../lib/bridge";
import { writeClipboardText } from "../lib/clipboard";
import { OpenerIcon } from "./ExternalOpener";
import { cachedSessionPurpose, loadSessionPurpose, setCachedSessionPurpose, subscribeSessionPurposes } from "../lib/sessionPurpose";
import { onProjectTreeChangedV2 } from "../lib/sessionCatalogBridge";
import { createRepairNoticeGate, sessionCatalogNotice, type SessionCatalogNotice } from "../lib/sessionCatalogPresentation";
import { isRuntimeSessionNode, isTopicNode, loadWorkbenchOrganizeMode, loadWorkbenchSortMode, mergeIncompleteProjectTopicPage, mergeProjectTopicFirstPage, mergeProjectTopicPage, projectTreeDedupedExactTime, projectTreeEventAffectsFolder, projectTreeFolderDisclosure, projectTreePageLoadFlipsIndicator, projectTreeBrowsePageContinues, projectTreeReadActivityKey, projectTreeRevisionIsFresh, projectTreeShellChildren, projectTreeShellSignature, projectTreeShouldApplyShellSnapshot, projectTreeShouldRenderTopicActions, projectTreeShouldSuppressOpenForRename, projectTreeTopicArchiveBlocked, projectTreeTopicHasUnreadActivity, projectTreeTopicHoverCardModel, projectTreeTopicMenuOffersPin, projectTreeTopicMetaLine, projectTreeTopicOpenRequest, projectTreeTopicPageIsFresh, projectTreeTopicPageSignature, projectTreeWithoutTopic, projectTreeWithTopicTitle, topicActivityAt, topicActivityDateLabel, topicActivityLabel, topicIsActive, topicStatus, topicStatusLabel, topicUnknownTimeLabel, WORKBENCH_ORGANIZE_KEY, WORKBENCH_SORT_KEY, type ProjectTreePendingTopicOpen, type ProjectTreeReadActivity, type ProjectTreeTopicHoverCard, type WorkbenchOrganizeMode, type WorkbenchSortMode } from "../lib/projectTreeTopic";
export * from "../lib/projectTreeTopic";
import { arrangeClassicProjectTree, arrangeWorkbenchTree, classicTopicWindow, CLASSIC_TOPIC_PREVIEW_LIMIT, projectTreeBodyState, projectTreeWithoutBuiltinWorkspaceNodes, splitPinnedProjectTree, type PinnedTreeSections } from "../lib/projectTreePresentation";
export * from "../lib/projectTreePresentation";
import type { ExternalOpenersView, ProjectNode, ProjectTreeChangedV2, SessionCatalogStatus } from "../lib/types";
import { topicActivityTime } from "../lib/session";
import { useT, type Translator } from "../lib/i18n";
import { requestTopicVersions } from "../lib/sessionRecoveryVersionHostBridge";
import { PROJECT_COLOR_OPTIONS, projectColorValue, type ProjectColorKey, type ProjectColorOption } from "../lib/projectColors";
import { projectTreeSessionArchiveTargetKey, projectTreeTopicArchiveTargetKey, projectTreeWithoutTopics, isTopicArchiveForceableError, reloadProjectTreeTopics, topicArchiveForceableReason, useProjectTreeArchiveController, type ProjectTreeRefresh, type ProjectTreeRefreshOptions } from "../lib/projectTreeArchive";
import { topicShortcutLabel, type TopicShortcutEntry } from "../lib/topicShortcuts";
import { ContextMenu, contextMenuPointFromEvent, type ContextMenuItem, type ContextMenuPoint } from "./ContextMenu";
import { Tooltip } from "./Tooltip";
import { WorktreeBadge } from "./WorktreeBadge";
import { useProjectCreation } from "./useProjectCreation";
import { useProjectTreeRuntimeProjection } from "../lib/useProjectTreeRuntimeProjection";
import { useProjectTreeFrontendDiagnostics, type ProjectTreeDiagnosticSnapshot } from "../lib/useProjectTreeFrontendDiagnostics";
import { summarizeProjectTreeSessions } from "../lib/projectTreeDiagnostics";
import { GLOBAL_PROJECT_ORDER_KEY, ProjectTreeFolderActivity, ProjectTreeGroupRows, applyProjectOrder, projectTreeProjectRoots, reorderedProjectRoots, useProjectTreeOrganization, type ProjectDropPosition } from "./ProjectTreeOrganization";
import { ProjectTreeSessionArchiveMenu } from "./ProjectTreeSessionArchiveMenu";
import { SessionPurposeEditor } from "./SessionPurposeEditor";
import {
  addProjectGroup,
  clearProjectGroupRoster,
  deleteProjectGroup,
  dropProjectGroupCollapsed,
  groupForProject,
  loadProjectGroupAssign,
  loadProjectGroupCollapsed,
  loadProjectGroups,
  moveProjectToGroup,
  nextProjectGroupID,
  pruneProjectGroupAssign,
  renameProjectGroup,
  reorderProjectGroups,
  persistProjectGroupCollapsed,
  type ProjectGroup,
} from "../lib/projectGroups";
import {
  createProjectGroupViaCatalog,
  defaultProjectGroupCatalogBindings,
  executeProjectGroupDelete,
  proposeProjectGroupDelete,
  pullProjectGroupCatalog,
  renameProjectGroupViaCatalog,
  type ProjectGroupCatalogBindings,
} from "../lib/projectGroupCatalog";
import { NewGroupPanel } from "./NewGroupPanel";
import { useConfirmDialog } from "./ConfirmDialog";
import { loadReadActivityStore, markReadKeysRead, persistReadActivity, readActivityEquals, readActivityKeysInScope, readActivityKeysInSubtree, READ_ACTIVITY_CHANGED_EVENT } from "../lib/readActivity";
import { ProjectTreeHeaderAddControl, ProjectTreeRemoteAction } from "./ProjectTreeAddControls";
import { activeRemoteProjectAncestorKeys, buildRemoteProjectMenuItems, useRemoteRuntimeTree, openRemoteSessionNode, remoteProjectKey, remoteServeBadgeState, renameRemoteProjectTitle, RemoteProjectEmptyState, useRemoteProjectGroups, useRemoteSessionActions } from "./ProjectTreeRemoteGroups";
import type { ProjectTreeProps } from "./ProjectTreeProps";
import { reportFrontendLog } from "../lib/frontendLog";
	import { loadFoldExcessSessions, onFoldExcessSessionsChanged } from "../lib/foldExcessSessions";

function projectNodeKey(node: ProjectNode, depth: number): string {
  return node.key || `${node.kind}-${node.root ?? ""}-${node.topicId ?? ""}-${node.sessionPath ?? ""}-${depth}`;
}

// tl1222: the header's "more" menu is gone — the workbench header collapsed
// onto the classic 3-control form and only the create dropdown remains.
type WorkbenchHeaderMenu = "add" | null;

// Task 827 ④: a dedicated drag MIME type for project-group reorder drags. It
// is what keeps the group drag and the project-row drag from ever interfering
// (acceptance ⑤): each side sets and matches only its own type, and each side
// gates its dragover/drop on its own state (dragGroupId vs dragProjectRoot).
const PROJECT_GROUP_DRAG_TYPE = "application/x-reasonix-project-group-id";
// tl 1199 ②: the project-row drag gains its own dedicated MIME type too. The
// in-app discrimination runs on the dragProjectRoot state (a topic drag never
// sets it), but the state is gone by the time a cross-window drop fires, and
// the plain-text fallback alone would be ambiguous with topic drags (both
// write text/plain). With its own type the group header can accept a project
// drop without ever mistaking a topic id for a project root.
const PROJECT_ROW_DRAG_TYPE = "application/x-reasonix-project-root";

type CollapseSnapshot = {
  expanded: Set<string>;
  manuallyCollapsed: Set<string>;
};

// Task 981: fixed window (not sliding — a sliding debounce starves under the
// continuous event stream a long turn produces) that batches
// project-tree:changed-v2 events before one merged sidebar refresh. 400ms sits
// inside the report's recommended 300–500ms band: long enough to absorb a
// write+reconcile+metadata burst, short enough that a real rename still feels
// immediate.
const PROJECT_TREE_EVENT_BATCH_MS = 400;

// wt-zcode-288：readActivity 的存取与「全部已读」批量标记收口到 lib/readActivity
// （TabBar 等树外组件也要写同一份存档）；这里只保留 baseline 的本地初始化。
const READ_ACTIVITY_BASELINE_KEY = "projectTree:readActivityBaselineAt";

function loadReadActivityBaselineAt(): number {
  try {
    const parsed = Number(localStorage.getItem(READ_ACTIVITY_BASELINE_KEY));
    if (Number.isFinite(parsed) && parsed > 0) return parsed;
    const now = Date.now();
    localStorage.setItem(READ_ACTIVITY_BASELINE_KEY, String(now));
    return now;
  } catch {
    return Date.now();
  }
}

function collapsibleFolderKeys(nodes: ProjectNode[], depth = 0): string[] {
  const keys: string[] = [];
  for (const node of nodes) {
    if (!node) continue;
    const children = asArray(node.children);
    if ((node.kind === "project" || node.kind === "global_folder") && children.length > 0) {
      keys.push(projectNodeKey(node, depth));
    }
    keys.push(...collapsibleFolderKeys(children, depth + 1));
  }
  return keys;
}

export function activeSessionAncestorKeys(
  nodes: ProjectNode[],
  activeScope?: string,
  activeWorkspaceRoot?: string,
  activeTopicId?: string,
  activeSessionPath?: string,
): string[] {
  const walk = (nodeList: ProjectNode[], ancestors: string[]): string[] | null => {
    for (const node of nodeList) {
      if (!node) continue;
      if (topicIsActive(node, activeScope, activeWorkspaceRoot, activeTopicId, activeSessionPath)) return ancestors;
      const children = asArray(node.children);
      if (children.length > 0) {
        const next = walk(children, [...ancestors, projectNodeKey(node, ancestors.length)]);
        if (next) return next;
      }
    }
    return null;
  };
  const found = walk(nodes, []);
  if (found) return found;
  // Shell-only snapshots no longer embed topics. Expand the matching project or
  // Global folder by workspace identity so the first lazy page can load.
  const scope = (activeScope ?? "").trim();
  const root = (activeWorkspaceRoot ?? "").trim();
  for (const node of nodes) {
    if (!node) continue;
    if (scope === "global" && node.kind === "global_folder") {
      return [projectNodeKey(node, 0)];
    }
    if (node.kind === "project" && root && (node.root === root || node.root === activeWorkspaceRoot)) {
      return [projectNodeKey(node, 0)];
    }
    if (!scope && !root && activeTopicId && (node.kind === "project" || node.kind === "global_folder")) {
      // Active topic without resolved scope still needs a folder open path.
      return [projectNodeKey(node, 0)];
    }
  }
  return [];
}

export function defaultExpandedProjectTreeKeys(
  nodes: ProjectNode[],
  activeScope?: string,
  activeWorkspaceRoot?: string,
  activeTopicId?: string,
  activeSessionPath?: string,
): string[] {
  return activeSessionAncestorKeys(nodes, activeScope, activeWorkspaceRoot, activeTopicId, activeSessionPath);
}

// Global rows use the same project tree recipe; the fallback supplies their non-workspace accent.
function projectAccentStyle(color?: string, fallbackValue?: string): CSSProperties | undefined {
  const value = projectColorValue(color) || fallbackValue;
  if (!value) return undefined;
  return { "--project-accent": value } as CSSProperties;
}

function colorMenuLabel(label: string, color?: string, active = false) {
  const value = projectColorValue(color);
  return (
    <span className="project-tree__color-option">
      <span
        className="project-tree__color-swatch"
        style={value ? ({ "--project-accent": value } as CSSProperties) : undefined}
        aria-hidden="true"
      />
      <span>{label}</span>
      {active && <Check className="project-tree__color-check" size={12} />}
    </span>
  );
}

function revealLabelKey(platform: string): "projectTree.revealInFinder" | "projectTree.revealInExplorer" | "projectTree.revealInFileManager" {
  if (platform === "darwin") return "projectTree.revealInFinder";
  if (platform === "windows") return "projectTree.revealInExplorer";
  return "projectTree.revealInFileManager";
}

function projectColorLabel(t: Translator, color?: string): string {
  switch (color) {
    case "red": return t("projectTree.colorRed");
    case "orange": return t("projectTree.colorOrange");
    case "amber": return t("projectTree.colorAmber");
    case "green": return t("projectTree.colorGreen");
    case "teal": return t("projectTree.colorTeal");
    case "blue": return t("projectTree.colorBlue");
    case "purple": return t("projectTree.colorPurple");
    case "pink": return t("projectTree.colorPink");
    default: return t("projectTree.colorDefault");
  }
}

export function ProjectTree({
  activeScope,
  activeWorkspaceRoot,
  activeTopicId,
  activeSessionPath,
  activeRemote,
  imTopicSources = {},
  variant = "classic",
  onOpenTopic,
  onAddProject,
  onCreateTopic,
  onCreateIsolatedWorktree,
  onRenameTopic,
  onTopicsChanged,
  refreshSignal,
  timeFilter,
  onTimeFilterChange,
  searchExpanded = true,
  searchFocusSignal = 0,
  showShortcutBadges = false,
  shortcutPlatform,
  onVisibleTopicsChange,
  logDirectory = "",
}: ProjectTreeProps) {
  const t = useT();
  const { showToast } = useToast();
  const compactTopics = variant === "workbench";
  const creationTopics = variant === "creation";
  const [tree, setTree] = useState<ProjectNode[]>([]);
  // Task 403 (upstream #10957→#10963): stays false until the first project-tree
  // read settles, so the sidebar paints a loading row instead of the "no
  // projects yet" empty state — which is visually identical to "data lost".
  const [initialTreeReadSettled, setInitialTreeReadSettled] = useState(false);
  const treeRef = useRef<ProjectNode[]>([]);
  const latestRevisionRef = useRef(0);
  const [organizationRevision, setOrganizationRevision] = useState(0);
  const topicRevisionRef = useRef<Record<string, number>>({});
  const topicCompletePageRef = useRef<Record<string, { signature: string; revision: number }>>({});
  const [catalogStatus, setCatalogStatus] = useState<SessionCatalogStatus>({
    state: "opening", revision: 0, indexed: 0, total: 0, repairPending: 0,
    repairActive: 0, repairDeferred: 0, repairBlocked: 0,
  });
  const catalogStatusGenerationRef = useRef(0), rebuildingCatalogRef = useRef(false), catalogRebuildFailedRef = useRef(false);
  const [topicPageState, setTopicPageState] = useState<Record<string, { nextCursor?: string; loading: boolean }>>({});
  const topicPageStateRef = useRef(topicPageState);
  const updateTopicPageState = useCallback((key: string, next: { nextCursor?: string; loading: boolean }) => {
    setTopicPageState((current) => {
      const updated = { ...current, [key]: next };
      topicPageStateRef.current = updated;
      return updated;
    });
  }, []);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [manuallyCollapsed, setManuallyCollapsed] = useState<Set<string>>(new Set());
  const [creatingProject, setCreatingProject] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [editingTopic, setEditingTopic] = useState<string | null>(null);
  const [topicDraft, setTopicDraft] = useState("");
  const [menuNodeKey, setMenuNodeKey] = useState<string | null>(null);
  const [menuProject, setMenuProject] = useState<{ key: string; root: string; path: string; scope: "global" | "project"; label: string } | null>(null);
  const [menuPoint, setMenuPoint] = useState<ContextMenuPoint | null>(null);
  const [editingProject, setEditingProject] = useState<{ key: string; root: string } | null>(null);
  const [projectDraft, setProjectDraft] = useState("");
  const [isolatingProject, setIsolatingProject] = useState<string | null>(null);
  const [worktreeAvailability, setWorktreeAvailability] = useState<Record<string, { available: boolean; reason?: string }>>({});
  const [confirmArchiveTarget, setConfirmArchiveTarget] = useState<string | null>(null);
  // tl 1103: the "Open with ▸" submenu discovers the machine's external openers
  // once per mount (the backend caches discovery for 15s) — the sidebar context
  // menu is built synchronously per row, so the menu reads this snapshot.
  const [externalOpeners, setExternalOpeners] = useState<ExternalOpenersView | null>(null);
  const [confirmRemoveProject, setConfirmRemoveProject] = useState<string | null>(null);
  const [dragProjectRoot, setDragProjectRoot] = useState<string | null>(null);
  const [dropProject, setDropProject] = useState<{ root: string; position: ProjectDropPosition } | null>(null);
  // tl 1199 ②: the project-group header a project drag currently hovers — the
  // "drop into this group" landing highlight (row-level, unlike the reorder
  // bands). Null when no project drag is over a legal target group.
  const [projectDropInto, setProjectDropInto] = useState<string | null>(null);
  const [collapseSnapshot, setCollapseSnapshot] = useState<CollapseSnapshot | null>(null);
  const [platform, setPlatform] = useState("");
  const [workbenchHeaderMenu, setWorkbenchHeaderMenu] = useState<WorkbenchHeaderMenu>(null);
  const [workbenchOrganizeMode, setWorkbenchOrganizeMode] = useState<WorkbenchOrganizeMode>(loadWorkbenchOrganizeMode);
  const [workbenchSortMode, setWorkbenchSortMode] = useState<WorkbenchSortMode>(loadWorkbenchSortMode);
  const workbenchSortModeRef = useRef(workbenchSortMode);
  const [readActivity, setReadActivity] = useState<ProjectTreeReadActivity>(loadReadActivityStore);
  const [readBaselineAt] = useState(loadReadActivityBaselineAt);
  const filterRef = useRef<HTMLDivElement>(null);
  const filterTriggerRef = useRef<HTMLButtonElement>(null);
  const searchInputRef = useRef<HTMLInputElement>(null);
  const topicIndexRef = useRef(0);
  const visibleTopicsCollectorRef = useRef<TopicShortcutEntry[]>([]);
  const [filterMenuOpen, setFilterMenuOpen] = useState(false);
  // Fork (#9221): project colour filter. Only colourable project nodes carry a
  // colour, so the filter prunes the project itself rather than its topics.
  const [colorFilter, setColorFilter] = useState<ProjectColorKey[]>([]);
  const [showAllTopics, setShowAllTopics] = useState<Set<string>>(new Set());
  const [hoverCard, setHoverCard] = useState<{ key: string; card: ProjectTreeTopicHoverCard; left: number; top: number; sessionPath: string } | null>(null);
  const [aiRenamingTopic, setAiRenamingTopic] = useState<string | null>(null);
  // 938：职责缓存变更 → 行 title/「编辑职责」弹窗即时反映（保存成功即 bump）。
  const [, setPurposeRevision] = useState(0);
  useEffect(() => subscribeSessionPurposes(() => setPurposeRevision((n) => n + 1)), []);
  // 938：「编辑职责」弹窗（锚定右键的行元素，AnchoredPopover portal 到 body）。
  const [purposeEditorSessionPath, setPurposeEditorSessionPath] = useState<string | null>(null);
  const purposeEditorAnchorRef = useRef<HTMLElement | null>(null);
  const hoverCardTimerRef = useRef<number | null>(null);
  const creatingRef = useRef(false);
  const closeMenu = useCallback(() => {
    setMenuNodeKey(null);
    setMenuProject(null);
    setMenuPoint(null);
    setConfirmArchiveTarget(null);
    setConfirmRemoveProject(null);
    setWorkbenchHeaderMenu(null);
  }, []);
  const topicLoadSeqRef = useRef<Record<string, number>>({});
  const topicLoadErrorRef = useRef<Record<string, string>>({});
  const refreshRef = useRef<ProjectTreeRefresh>(async () => {});
  const { trashingTopics, trashingSessions, currentArchiveTombstones, trashTopic, trashSession } = useProjectTreeArchiveController({
    treeRef, topicLoadSeqRef, topicPageStateRef, updateTopicPageState, refreshRef,
    optimisticallyRemoveTopic: (topicId) => setTree((current) => projectTreeWithoutTopic(current, topicId)),
    closeMenu, onTopicsChanged, showToast,
  });
  const applyRuntimeProjection = useProjectTreeRuntimeProjection(setTree, currentArchiveTombstones);
  const clickTimerRef = useRef<ProjectTreePendingTopicOpen | null>(null);
  useEffect(() => {
    return () => {
      if (clickTimerRef.current !== null) clearTimeout(clickTimerRef.current.timer);
      if (hoverCardTimerRef.current !== null) window.clearTimeout(hoverCardTimerRef.current);
    };
  }, []);
  const manuallyCollapsedRef = useRef(manuallyCollapsed);

  const cancelHoverCard = useCallback(() => {
    if (hoverCardTimerRef.current !== null) {
      window.clearTimeout(hoverCardTimerRef.current);
      hoverCardTimerRef.current = null;
    }
    setHoverCard((current) => (current === null ? current : null));
  }, []);

  const toggleShowAllTopics = useCallback((key: string) => {
    setShowAllTopics((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }, []);

  const updateManuallyCollapsed = useCallback((updater: (prev: Set<string>) => Set<string>) => {
    setManuallyCollapsed((prev) => {
      const next = updater(prev);
      manuallyCollapsedRef.current = next;
      return next;
    });
  }, []);

  const loadProjectTopicsRef = useRef<(project: ProjectNode, append?: boolean, full?: boolean) => Promise<void>>(async () => {});

  // 981 二轮（候选1 分场景）：full=true（默认）沿游标取全——用户主动展开、首次
  // 挂载、筛选/排序变化、归档后的权威重拉都走这条；full=false 只取首页——catalog
  // 事件驱动的背景刷新只需要首页新鲜（新会话都排在首页），恒不续拉，把一次事件
  // flush 的每文件夹 ListProjectTopics 调用数从 ⌈N/limit⌉ 压回 1（报告 20261019
  // §3.1-A 残余放大链）。
  const loadProjectTopics = useCallback(async (project: ProjectNode, append = false, full = true) => {
    if ((project.kind !== "project" && project.kind !== "global_folder") || project.remote) return;
    const key = project.key;
    const pageState = topicPageStateRef.current[key];
    const cursor = append ? pageState?.nextCursor ?? "" : "";
    if (append && !cursor) return;
    const sortMode = creationTopics ? "updated" : workbenchSortModeRef.current;
    const limit = timeFilter === "10" ? 10 : timeFilter === "20" ? 20 : 50;
    const requestSignature = projectTreeTopicPageSignature(query, timeFilter, sortMode, limit);
    // Last-query-wins: stale completions cannot overwrite a newer first page.
    const seq = (topicLoadSeqRef.current[key] ?? 0) + 1;
    topicLoadSeqRef.current[key] = seq;
    // 741: the flip gate lives in projectTreePageLoadFlipsIndicator — a
    // background first-page reload onto a resident page must not flip the
    // trailing "load more" row to "indexing history" and back on every
    // catalog revision bump.
    if (projectTreePageLoadFlipsIndicator(pageState, append)) {
      updateTopicPageState(key, { ...pageState, loading: true });
    }
    try {
      // 962：普通浏览不再渲染「加载更多」按钮（对齐上游 main-v2 —— 分页按钮
      // 只留给搜索结果）。浏览态列表完整性改由这里沿后端游标自动续拉：首页返回
      // 后只要后端还给出下一页游标就继续拉，直到取全（停机条件见
      // projectTreeBrowsePageContinues：搜索态停 / 空页停 / 游标不前进停）。
      // 981 二轮（候选1）：full=false（事件驱动的背景刷新）不进续拉——恒停在
      // 首页，合并也走保尾部的 mergeProjectTopicFirstPage；full 只改这两处。
      // 搜索态（query 非空）维持单页 + 用户显式翻页，行为零回归。每页之间复
      // 查 seq —— 期间任何刷新拿到新 seq，本循环安静退出，由新请求重新取数。
      let currentCursor = cursor;
      let firstPage = true;
      for (;;) {
        const page = await app.ListProjectTopics({
          scope: project.kind === "global_folder" ? "global" : "project",
          workspaceRoot: project.kind === "global_folder" ? "" : project.root ?? "",
          cursor: currentCursor,
          limit,
          query: query.trim(),
          timeFilter: timeFilter === "10" || timeFilter === "20" || timeFilter === "all" ? "" : timeFilter,
          sortMode,
        });
        if (topicLoadSeqRef.current[key] !== seq) return;
        delete topicLoadErrorRef.current[key];
        if (!projectTreeTopicPageIsFresh(topicRevisionRef.current, key, page.revision)) {
          updateTopicPageState(key, { ...topicPageStateRef.current[key], loading: false });
          return;
        }
        topicRevisionRef.current[key] = Math.max(topicRevisionRef.current[key] ?? 0, page.revision);
        const items = projectTreeWithoutTopics(asArray(page.items), currentArchiveTombstones());
        const completeBaseline = topicCompletePageRef.current[key];
        const preserveCompletePage = page.complete === false && completeBaseline?.signature === requestSignature;
        if (page.complete !== false) {
          topicCompletePageRef.current[key] = { signature: requestSignature, revision: page.revision };
        }
        const pageAppend = append || !firstPage;
        setTree((current) => applyRuntimeProjection(current.map((node) => {
          if (node.key !== key) return node;
          const children = preserveCompletePage
            ? mergeIncompleteProjectTopicPage(asArray(node.children), items)
            : full
              ? mergeProjectTopicPage(asArray(node.children), items, pageAppend)
              // Task 1093: an empty INCOMPLETE first page is a mid-scan
              // snapshot, not a verdict — keep the resident rows so a
              // directory-scan batch cannot collapse a group whose count is
              // unchanged; an EMPTY complete page still replaces (the
              // "folder is empty" case). See mergeProjectTopicFirstPage.
              : mergeProjectTopicFirstPage(asArray(node.children), items, { nextCursor: page.nextCursor, complete: page.complete !== false });
          return children === node.children ? node : { ...node, children };
        })));
        const continues = projectTreeBrowsePageContinues({
          query,
          full,
          requestCursor: currentCursor,
          nextCursor: page.nextCursor,
          itemCount: page.items.length,
        });
        if (!continues) {
          updateTopicPageState(key, preserveCompletePage
            ? { ...topicPageStateRef.current[key], loading: false }
            : { nextCursor: page.nextCursor, loading: false });
          break;
        }
        // continues=true 已蕴含 nextCursor 存在；?? 只为收敛 TS。
        currentCursor = page.nextCursor ?? "";
        firstPage = false;
      }
    } catch (error) {
      if (topicLoadSeqRef.current[key] !== seq) return;
      updateTopicPageState(key, { ...topicPageStateRef.current[key], loading: false });
      const message = error instanceof Error ? error.message : String(error);
      if (topicLoadErrorRef.current[key] !== message) {
        topicLoadErrorRef.current[key] = message;
        showToast(message, "error", { durationMs: 6000 });
      }
    }
  }, [applyRuntimeProjection, creationTopics, currentArchiveTombstones, query, showToast, timeFilter, updateTopicPageState]);
  loadProjectTopicsRef.current = loadProjectTopics;

  const selectWorkbenchSortMode = useCallback((sortMode: WorkbenchSortMode) => {
    if (workbenchSortModeRef.current === sortMode) {
      closeMenu();
      setFilterMenuOpen(false);
      return;
    }
    // Invalidate before scheduling React state so an already-resolved older
    // request cannot write back during the render/effect gap. Its pagination
    // cursor belongs to the old order and must not be reused by the new query.
    workbenchSortModeRef.current = sortMode;
    for (const key in topicLoadSeqRef.current) topicLoadSeqRef.current[key] += 1;
    topicPageStateRef.current = {};
    setTopicPageState({});
    setWorkbenchSortMode(sortMode);
    closeMenu();
    setFilterMenuOpen(false);

    const filtering = query.trim() !== "" || timeFilter !== "all";
    for (const project of treeRef.current) {
      const key = projectNodeKey(project, 0);
      if (filtering || expanded.has(key)) void loadProjectTopics(project);
    }
  }, [closeMenu, expanded, loadProjectTopics, query, timeFilter]);
  // Snapshot carries project shells plus lightweight pinned topic shells.
  // Preserve already loaded pages by project key while reconciling pins, so a
  // metadata refresh does not collapse or blank the sidebar.
  const refresh = useCallback(async (options?: ProjectTreeRefreshOptions) => {
    const reloadRequestedProjects = (projects: ProjectNode[]) => reloadProjectTreeTopics(projects, options, loadProjectTopicsRef.current), catalogStatusGeneration = catalogStatusGenerationRef.current;
    try {
      const snapshot = await app.GetProjectTreeSnapshot();
      const rev = snapshot.revision ?? 0, empty = treeRef.current.length === 0;
      if (!projectTreeShouldApplyShellSnapshot({ currentRevision: latestRevisionRef.current, incomingRevision: rev, treeEmpty: empty })) {
        await reloadRequestedProjects(treeRef.current);
        return;
      }
      if (projectTreeRevisionIsFresh(latestRevisionRef.current, rev)) latestRevisionRef.current = Math.max(latestRevisionRef.current, rev);
      // Task 186: the render-side backstop keeps the host's own workspace from
      // coming back as a project node even if a backend path forgets the whitelist.
      const projects = projectTreeWithoutBuiltinWorkspaceNodes(asArray(snapshot.projects));
      if (!catalogRebuildFailedRef.current && catalogStatusGeneration === catalogStatusGenerationRef.current) setCatalogStatus(snapshot.catalog);
      setTree((current) => applyRuntimeProjection(projects.map((project) => {
        const previous = current.find((node) => node.key === project.key);
        // Topic pages reload asynchronously. Keep the last painted children
        // until their replacement arrives so a mutation cannot blank every
        // expanded folder for the duration of a catalog scan.
        return { ...project, children: projectTreeShellChildren(previous?.children, project.children) };
      })));
      await reloadRequestedProjects(projects);
    } catch {
      // A shell snapshot is metadata-only. If it fails, the resident folder
      // identity can still drive the requested canonical topic reload.
      await reloadRequestedProjects(treeRef.current);
    } finally {
      // Task 403: every exit path settles the first read. A failed snapshot
      // must fall through to the empty state rather than a perpetual loading
      // row (later events still refresh the tree via project-tree:changed-v2).
      setInitialTreeReadSettled(true);
    }
  }, [applyRuntimeProjection]);
  refreshRef.current = refresh;
  const { openRemoteProject, openRemoteWindow, remoteSessions, setRemoteSessions, remoteServers, remoteGroupBusy, remoteGroupError, ensureRemoteGroupSessions, refreshRemoteSessions } = useRemoteProjectGroups(tree, showToast, expanded, query);
  const treeWithRemoteSessions = useRemoteRuntimeTree(tree, remoteSessions, t);
  const remoteSessionActions = useRemoteSessionActions(remoteSessions, refreshRemoteSessions, (error) => showToast(error instanceof Error ? error.message : String(error), "error"));
  const { addingProject, handleAddProject, openBlankProjectFlow, blankProjectFlow, openRemoteConnectFlow, remoteConnectFlow } = useProjectCreation({
    onAddProject,
    onRefresh: refresh,
    showToast,
  });

  const rebuildSessionCatalog = useCallback(async () => {
    if (rebuildingCatalogRef.current || catalogStatus.canRebuild !== true) return;
    rebuildingCatalogRef.current = true; catalogRebuildFailedRef.current = false; catalogStatusGenerationRef.current += 1;
    setCatalogStatus({ ...catalogStatus, state: "rebuilding", canRebuild: false });
    try {
      await app.RebuildSessionCatalog(); catalogStatusGenerationRef.current += 1;
      await refresh();
    } catch {
      catalogRebuildFailedRef.current = true; catalogStatusGenerationRef.current += 1; setCatalogStatus(catalogStatus);
    } finally {
      rebuildingCatalogRef.current = false;
    }
  }, [catalogStatus, refresh]);

  useEffect(() => {
    treeRef.current = tree;
  }, [tree]);

  useEffect(() => {
    manuallyCollapsedRef.current = manuallyCollapsed;
  }, [manuallyCollapsed]);

  const searchVisible = searchExpanded || query.trim().length > 0;

  useEffect(() => {
    if (!searchVisible || searchFocusSignal <= 0) return;
    searchInputRef.current?.focus();
  }, [searchFocusSignal, searchVisible]);

  useEffect(() => {
    void refresh();
  }, [refresh, refreshSignal]);

  // Task 892-B ⑤: the project-group catalog pull is declared further down with
  // the rest of the project-group state; the listener only needs the ref (it
  // fires after mount, when the ref already holds the latest callback).
  const refreshProjectGroupsRef = useRef<() => Promise<void>>(async () => {});
  // Task 981 (sidebar flicker): catalog events ride every revision bump, so a
  // long-running turn streams write/reconcile/metadata events several times a
  // second and each one used to run the full refresh suite (status pull +
  // group roster + a topic-page reload per expanded folder) — the整树重-render
  // the user sees as flicker. Events now batch into a fixed 400ms window: the
  // first event opens the window, every event inside it joins the batch, and
  // one merged flush runs the suite once. A fixed (not sliding) window matters:
  // under a continuous event stream a sliding debounce would starve the flush
  // forever, exactly the load the report measured.
  const pendingTreeEventsRef = useRef<ProjectTreeChangedV2[]>([]);
  const treeEventWindowRef = useRef<number | null>(null);
  const flushTreeEventsRef = useRef<() => void>(() => {});
  flushTreeEventsRef.current = () => {
    const events = pendingTreeEventsRef.current;
    pendingTreeEventsRef.current = [];
    if (events.length === 0) return;
    // Merge the batch: the freshest revision wins, metadata-ness is sticky
    // (any metadata event keeps the roster pull), and the affected-roots union
    // degrades to "everything" the moment any batched event carries empty
    // roots — the same all-folders semantics a single empty-roots event has.
    const event = events.reduce((current, item) => item.revision >= current.revision ? item : current);
    // Any stale member means the stream jumped a revision while batching — the
    // same "refetch the full snapshot instead of dropping the event" contract a
    // single stale event had, evaluated per batched event.
    if (events.some((item) => !projectTreeRevisionIsFresh(latestRevisionRef.current, item.revision))
      || !projectTreeRevisionIsFresh(latestRevisionRef.current, event.revision)) {
      void refresh();
      return;
    }
    latestRevisionRef.current = Math.max(latestRevisionRef.current, event.revision);
    const metadataRevision = events.reduce((current, item) => item.reason === "metadata" ? Math.max(current, item.revision) : current, 0);
    if (metadataRevision > 0) {
      setOrganizationRevision((current) => Math.max(current, metadataRevision));
      // A-slice writes (rename/move/create, incl. project groups) each emit
      // reason=metadata — re-pull the project-group roster so every open
      // window converges on the same catalog (handoff §2.4).
      void refreshProjectGroupsRef.current();
    }
    const catalogStatusGeneration = catalogStatusGenerationRef.current;
    void app.GetSessionCatalogStatus().then((status) => { if (!catalogRebuildFailedRef.current && catalogStatusGeneration === catalogStatusGenerationRef.current) setCatalogStatus(status); }).catch(() => {});
    if (treeRef.current.length === 0) { void refresh(); return; } // race: event before shell
    let affected: string[] = [];
    for (const item of events) {
      const roots = asArray(item.roots);
      if (roots.length === 0) { affected = []; break; } // empty roots = every folder
      affected = [...new Set([...affected, ...roots])];
    }
    for (const project of treeRef.current) {
      const key = projectNodeKey(project, 0);
      if (!expanded.has(key)) continue;
      // 981 二轮（候选1）：事件驱动的背景刷新只取首页（full=false）——首页
      // 足够覆盖新会话插入；取全只留给用户主动展开/首次挂载/筛选变化。
      if (projectTreeEventAffectsFolder(project, affected)) void loadProjectTopics(project, false, false);
    }
  };
  useEffect(() => {
    const unsubscribe = onProjectTreeChangedV2((event) => {
      pendingTreeEventsRef.current.push(event);
      // Fixed window: the first event opens it; later events just join. The flush
      // callback is ref-forwarded so it always sees the latest expanded set and
      // reload closure without re-subscribing per state change.
      if (treeEventWindowRef.current !== null) return;
      treeEventWindowRef.current = window.setTimeout(() => {
        treeEventWindowRef.current = null;
        flushTreeEventsRef.current();
      }, PROJECT_TREE_EVENT_BATCH_MS);
    });
    return () => {
      unsubscribe();
      if (treeEventWindowRef.current !== null) {
        window.clearTimeout(treeEventWindowRef.current);
        treeEventWindowRef.current = null;
      }
      // Unmount must not swallow a batched refresh the tree still owes... but
      // the component is going away, so dropping the pending batch is correct.
      pendingTreeEventsRef.current = [];
    };
  }, []);
  // Debounce query/timeFilter reloads so typing does not stampede the catalog.
  // Dependency is the project-shell signature, not tree: topic page loads
  // rewrite children and would otherwise re-arm this effect in a loop.
  const projectShellSignature = useMemo(() => projectTreeShellSignature(tree), [tree]);
  useEffect(() => {
    const filtering = query.trim() !== "" || timeFilter !== "all";
    const timer = setTimeout(() => {
      for (const project of treeRef.current) {
        const key = projectNodeKey(project, 0);
        if (filtering || expanded.has(key)) void loadProjectTopics(project);
      }
    }, 200);
    return () => clearTimeout(timer);
  }, [expanded, loadProjectTopics, projectShellSignature, query, timeFilter]);

  // Following the active topic is a view concern over the tree already held.
  useEffect(() => {
    const collapsed = manuallyCollapsedRef.current;
    const keys = (activeRemote
      ? activeRemoteProjectAncestorKeys(treeWithRemoteSessions, activeRemote, projectNodeKey)
      : defaultExpandedProjectTreeKeys(treeWithRemoteSessions, activeScope, activeWorkspaceRoot, activeTopicId, activeSessionPath))
      .filter((key) => !collapsed.has(key));
    // Returning prev unchanged keeps a switch that expands nothing new from
    // re-rendering the tree at all.
    setExpanded((prev) => (keys.every((key) => prev.has(key)) ? prev : new Set([...prev, ...keys])));
    // Active remote groups get the same explicit cold start as a click.
    for (const node of treeWithRemoteSessions) {
      if (!node.remote) continue;
      if (!keys.includes(projectNodeKey(node, 0))) continue;
      if (!remoteSessions[remoteProjectKey(node.remote)]?.length) void ensureRemoteGroupSessions(node.remote.hostId, node.remote.workspace);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tree, activeScope, activeWorkspaceRoot, activeTopicId, activeSessionPath, activeRemote]);

  const markNodeRead = useCallback((node: ProjectNode) => {
    const key = projectTreeReadActivityKey(node);
    const activityAt = topicActivityAt(node);
    if (!key || activityAt <= 0) return;
    setReadActivity((prev) => {
      const readAt = Math.max(activityAt, Date.now());
      if ((prev[key] ?? 0) >= readAt) return prev;
      const next = { ...prev, [key]: readAt };
      persistReadActivity(next);
      return next;
    });
  }, []);

  // wt-zcode-288：「全部已读」——把给定 readActivity key 的会话一次标记已读。
  // 三处右键菜单（tab / 项目分组 / 会话行）只负责凑出各自的 key 集合。
  const markAllRead = (keys: string[]) => {
    const next = markReadKeysRead(readActivity, keys);
    if (next === readActivity) return;
    setReadActivity(next);
    persistReadActivity(next);
  };

  // 树外组件（如 TabBar「全部已读」）写档后广播；这里重载内存态让侧栏未读点
  // 立即清零。等值时保持原引用，避免事件回声引发多余渲染。
  useEffect(() => {
    const syncFromStore = () => {
      setReadActivity((prev) => {
        const next = loadReadActivityStore();
        return readActivityEquals(prev, next) ? prev : next;
      });
    };
    window.addEventListener(READ_ACTIVITY_CHANGED_EVENT, syncFromStore);
    return () => window.removeEventListener(READ_ACTIVITY_CHANGED_EVENT, syncFromStore);
  }, []);

  useEffect(() => {
    const markActive = (nodes: ProjectNode[]) => {
      for (const node of nodes) {
        if (topicIsActive(node, activeScope, activeWorkspaceRoot, activeTopicId, activeSessionPath)) markNodeRead(node);
        markActive(asArray(node.children));
      }
    };
    markActive(treeWithRemoteSessions);
  }, [activeScope, activeSessionPath, activeTopicId, activeWorkspaceRoot, markNodeRead, treeWithRemoteSessions]);

  useEffect(() => {
    try {
      localStorage.setItem(WORKBENCH_ORGANIZE_KEY, workbenchOrganizeMode);
    } catch {
      /* ignore */
    }
  }, [workbenchOrganizeMode]);

  useEffect(() => {
    try {
      localStorage.setItem(WORKBENCH_SORT_KEY, workbenchSortMode);
    } catch {
      /* ignore */
    }
  }, [workbenchSortMode]);

  useEffect(() => {
    let cancelled = false;
    void app.Platform().then((value) => {
      if (!cancelled) setPlatform(value);
    }).catch(() => {});
    return () => {
      cancelled = true;
    };
  }, []);

  // tl 1103: discovery for the topic context menu's "Open with ▸" submenu.
  // One pull per mount (backend caches 15s); a failure just leaves the submenu
  // empty — the row menu must not lose its other items over a discovery miss.
  useEffect(() => {
    let cancelled = false;
    void app.ExternalOpeners().then((view) => {
      if (!cancelled) setExternalOpeners(view);
    }).catch(() => {});
    return () => {
      cancelled = true;
    };
  }, []);

  // Close the time-filter menu on outside click or Escape; move focus into the
  // menu on open and back to the trigger on Escape so it is keyboard-operable.
  useEffect(() => {
    if (!filterMenuOpen) return;
    const onMouseDown = (e: MouseEvent) => {
      if (filterRef.current && !filterRef.current.contains(e.target as Node)) setFilterMenuOpen(false);
    };
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setFilterMenuOpen(false);
        filterTriggerRef.current?.focus();
      }
    };
    document.addEventListener("mousedown", onMouseDown);
    document.addEventListener("keydown", onKeyDown);
    const menu = filterRef.current?.querySelector<HTMLElement>(".project-tree__time-filter-menu");
    (menu?.querySelector<HTMLButtonElement>(".project-tree__time-filter-opt--on") ??
      menu?.querySelector<HTMLButtonElement>('[role="menuitem"]'))?.focus();
    return () => {
      document.removeEventListener("mousedown", onMouseDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [filterMenuOpen]);

  const moveMenuFocus = (e: ReactKeyboardEvent<HTMLDivElement>) => {
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp" && e.key !== "Home" && e.key !== "End") return;
    e.preventDefault();
    const items = Array.from(e.currentTarget.querySelectorAll<HTMLButtonElement>('[role="menuitem"]'));
    if (items.length === 0) return;
    const current = items.indexOf(document.activeElement as HTMLButtonElement);
    const next = e.key === "Home" ? 0
      : e.key === "End" ? items.length - 1
      : e.key === "ArrowDown" ? (current + 1 + items.length) % items.length
      : (current - 1 + items.length) % items.length;
    items[next]?.focus();
  };

  const toggleExpand = (key: string, project?: ProjectNode) => {
    const willCollapse = expanded.has(key);
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
    updateManuallyCollapsed((prev) => {
      const next = new Set(prev);
      if (willCollapse) next.add(key);
      else next.delete(key);
      return next;
    });
    if (!willCollapse && project) void loadProjectTopics(project);
  };

  const folderKeys = useMemo(() => collapsibleFolderKeys(tree), [tree]);
  const searchActive = query.trim().length > 0;
  const hasExpandedFolders = !searchActive && folderKeys.some((key) => expanded.has(key));
  const canRestoreCollapsedView = collapseSnapshot !== null;
  const canToggleCollapsedView = !searchActive && folderKeys.length > 0 && (hasExpandedFolders || canRestoreCollapsedView);
  // Project groups (#9222, fork feature): projects are grouped by type and a
  // project belongs to at most one group. Assignments are local-only, so the
  // loaders read straight from localStorage exactly as they always have.
  const [groups, setGroups] = useState<ProjectGroup[]>(loadProjectGroups);
  const [assign, setAssign] = useState<Record<string, string>>(loadProjectGroupAssign);
  const [newGroupOpen, setNewGroupOpen] = useState(false);
  // Task 881 ①: the blank-area right-click menu point. Before this the only
  // "new group" doors were the project-row menu (needs an existing row) and
  // the header controls, so with zero groups and zero projects the whole
  // grouping feature had no visible way in.
  const [blankMenuPoint, setBlankMenuPoint] = useState<ContextMenuPoint | null>(null);
  const [collapsedProjectGroups, setCollapsedProjectGroups] = useState<ReadonlySet<string>>(loadProjectGroupCollapsed);
  // Task 892-B ⑤: roster source. false = localStorage only (pre-892 backend /
  // dev mock); true = the backend catalog (desktop-projects.json projectGroups)
  // is the source of truth and localStorage is only the fallback shadow.
  // Assignments (project→group) stay local — 892-A exposes no setter for
  // desktopProject.ProjectGroup (handoff §一.1 残留).
  const [projectGroupsFromCatalog, setProjectGroupsFromCatalog] = useState(false);
  const projectGroupCatalogRef = useRef<ProjectGroupCatalogBindings>(defaultProjectGroupCatalogBindings());
  const projectGroupPullSeqRef = useRef(0);
  const refreshProjectGroups = useCallback(async () => {
    const seq = ++projectGroupPullSeqRef.current;
    const pull = await pullProjectGroupCatalog(projectGroupCatalogRef.current, loadProjectGroups());
    // null = no binding/read failure (stay local); stale seq = a newer pull wins.
    if (!pull || seq !== projectGroupPullSeqRef.current) return;
    setProjectGroupsFromCatalog(true);
    if (pull.imported) clearProjectGroupRoster();
    setGroups((current) => {
      const same = current.length === pull.roster.length && current.every((group, index) => {
        const next = pull.roster[index];
        return next && group.id === next.id && group.title === next.title && group.parent === next.parent && group.sortOrder === next.sortOrder;
      });
      return same ? current : pull.roster;
    });
    // A group that left the roster (deleted, or cascade-deleted as a child)
    // takes its project assignments with it — projects fall back to ungrouped.
    // 908: an EMPTY roster must never clear assignments (a transient empty pull
    // would wipe them all); that guard + WARN lives in pruneProjectGroupAssign.
    setAssign((current) => pruneProjectGroupAssign(current, new Set(pull.roster.map((group) => group.id))));
  }, []);

  // Catalog adoption on mount (one-time import lives inside the pull).
  useEffect(() => { void refreshProjectGroups(); }, [refreshProjectGroups]);
  // Publish to the v2 metadata listener declared above (same render pass, so
  // a plain ref assignment is enough — every render refreshes the pointer).
  refreshProjectGroupsRef.current = refreshProjectGroups;

  const toggleProjectGroup = useCallback((id: string) => {
    setCollapsedProjectGroups((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      persistProjectGroupCollapsed(next);
      return next;
    });
  }, []);

  const handleMoveToGroup = useCallback((root: string, groupId: string | null) => {
    // The destination is what makes a mis-sorted tree explainable after the fact; the move
    // itself is fork-only organisation with no other trace. (Telemetry moved here from the
    // retired MoveToGroupPanel — tl 1149 replaced that floating panel with the in-menu
    // submenu below, and the log line must survive the panel.)
    reportFrontendLog("project-groups", "project moved to group", `group=${groupId ?? "none"}`);
    setAssign((current) => moveProjectToGroup(current, root, groupId));
  }, []);

  const groupForProjectRoot = useCallback(
    (root?: string): ProjectGroup | null => {
      const id = groupForProject(assign, root);
      return id ? groups.find((group) => group.id === id) ?? null : null;
    },
    [assign, groups],
  );

  // tl 1149: the project context menu's "move to group" entry. It used to open
  // the MoveToGroupPanel floating panel (screen-centred) — replaced by an in-menu
  // submenu so the interaction matches every other row of the same menu (就近弹
  // 出，tl 720 的二级形态). Same options as the panel: ungrouped (only when the
  // project currently sits in a group), every roster group (its own group
  // disabled), and the create-group entry via the shared NewGroupPanel.
  const projectGroupMoveItems = useCallback((root: string): ContextMenuItem[] => {
    const currentGroupId = groupForProjectRoot(root)?.id ?? null;
    return [
      ...(currentGroupId !== null ? [{
        key: "move:ungrouped",
        label: t("projectGroup.ungrouped"),
        onSelect: () => handleMoveToGroup(root, null),
      }] : []),
      ...groups.map((group): ContextMenuItem => ({
        key: `move:${group.id}`,
        label: group.title,
        disabled: currentGroupId === group.id,
        onSelect: () => handleMoveToGroup(root, group.id),
      })),
      {
        key: "move:new-group",
        icon: <FolderPlus size={13} />,
        label: t("projectGroup.createNew"),
        onSelect: () => setNewGroupOpen(true),
      },
    ];
  }, [groups, groupForProjectRoot, handleMoveToGroup, t]);

  // A1: create the group, then put the current project in it. Without the second
  // step a new group would be an empty row with no obvious way to fill it.
  const handleAddGroup = useCallback(
    (title: string) => {
      const trimmed = title.trim();
      if (!trimmed) return;
      setNewGroupOpen(false);
      const root = activeWorkspaceRoot;
      if (projectGroupsFromCatalog) {
        // Task 892-B ⑤: catalog mode — the write goes through the A-slice
        // host (op-log + reason=metadata v2 event re-pulls every window); the
        // assignment below still lands in localStorage (no backend setter).
        const groupID = nextProjectGroupID();
        void (async () => {
          await createProjectGroupViaCatalog(projectGroupCatalogRef.current, groupID, trimmed, null);
          await refreshProjectGroups();
        })();
        if (root) setAssign((current) => moveProjectToGroup(current, root, groupID));
        return;
      }
      const res = addProjectGroup(trimmed, groups);
      setGroups(res.groups);
      if (root) setAssign((current) => moveProjectToGroup(current, root, res.group.id));
    },
    [groups, activeWorkspaceRoot, projectGroupsFromCatalog, refreshProjectGroups],
  );

  // Task 827 ①: per-group-row context menu state. The menu itself renders
  // inside the row (same pattern as the topic rows), so only one group menu can
  // be open at a time and closing is a single setState.
  const [groupMenuId, setGroupMenuId] = useState<string | null>(null);
  // Task 881 ①: right-clicking the tree's blank area offers "new group" — the
  // one entry that never depends on an existing project row or group. Topic
  // and project rows stop propagation inside their own openers, but the group
  // row (task 827 ①) does not, so the handler re-checks the target and stays
  // out of any row that owns its own menu.
  const openBlankMenu = (event: ReactMouseEvent<HTMLElement>) => {
    const target = event.target;
    if (target instanceof Element && target.closest(".project-tree__topic,.project-tree__folder,.project-tree__group-main")) return;
    event.preventDefault();
    closeMenu();
    setBlankMenuPoint(contextMenuPointFromEvent(event));
  };
  const blankMenuItems: ContextMenuItem[] = [
    {
      key: "blank-new-group",
      icon: <FolderInput size={13} />,
      label: t("projectTree.newGroup"),
      onSelect: () => {
        setBlankMenuPoint(null);
        setNewGroupOpen(true);
      },
    },
  ];

  // Task 892-B ⑤: 「新建子分组」inline composer (827-form twin of the
  // session-group composer) — the group id whose child-create input is open.
  const [creatingProjectSubUnder, setCreatingProjectSubUnder] = useState<string | null>(null);
  const [projectSubDraft, setProjectSubDraft] = useState("");
  const commitProjectSubGroup = useCallback((parentID: string) => {
    const trimmed = projectSubDraft.trim();
    setCreatingProjectSubUnder(null);
    if (!trimmed) return;
    if (projectGroupsFromCatalog) {
      void (async () => {
        await createProjectGroupViaCatalog(projectGroupCatalogRef.current, nextProjectGroupID(), trimmed, parentID);
        await refreshProjectGroups();
      })();
      return;
    }
    // Local fallback keeps the same storage spelling the catalog imports
    // (parent field), so a later migration carries the nesting over.
    setGroups((current) => addProjectGroup(trimmed, current, parentID).groups);
  }, [projectSubDraft, projectGroupsFromCatalog, refreshProjectGroups]);
  const [groupMenuPoint, setGroupMenuPoint] = useState<ContextMenuPoint | null>(null);
  // Task 827 ①: inline rename — the row swaps its title for an input, Enter
  // commits, Escape cancels (same UX as the session-group rename in
  // ProjectTreeGroupRows).
  const [renamingGroupId, setRenamingGroupId] = useState<string | null>(null);
  const [groupRenameDraft, setGroupRenameDraft] = useState("");
  // Task 827 ④: group reorder drag. A dedicated drag type keeps this fully
  // separate from the project-row drag (acceptance ⑤): a group drag never sets
  // dragProjectRoot, so project rows ignore it (their onDragOver bails when
  // dragProjectRoot is null), and a project drag never sets dragGroupId, so
  // group rows ignore it.
  const [dragGroupId, setDragGroupId] = useState<string | null>(null);
  const [dropGroup, setDropGroup] = useState<{ id: string; position: ProjectDropPosition } | null>(null);

  const clearGroupDrag = useCallback(() => {
    setDragGroupId(null);
    setDropGroup(null);
  }, []);

  useEffect(() => {
    if (!dragGroupId) return;
    window.addEventListener("dragend", clearGroupDrag);
    window.addEventListener("drop", clearGroupDrag);
    window.addEventListener("blur", clearGroupDrag);
    return () => {
      window.removeEventListener("dragend", clearGroupDrag);
      window.removeEventListener("drop", clearGroupDrag);
      window.removeEventListener("blur", clearGroupDrag);
    };
  }, [clearGroupDrag, dragGroupId]);

  const commitGroupRename = useCallback((id: string) => {
    setRenamingGroupId(null);
    if (projectGroupsFromCatalog) {
      const title = groupRenameDraft.trim();
      if (!title) return; // blank draft keeps the old title (same as local)
      void (async () => {
        await renameProjectGroupViaCatalog(projectGroupCatalogRef.current, id, title);
        await refreshProjectGroups();
      })();
      return;
    }
    setGroups((current) => renameProjectGroup(current, id, groupRenameDraft));
  }, [groupRenameDraft, projectGroupsFromCatalog, refreshProjectGroups]);

  // Task 827 ①: dissolve = remove the group shell only. deleteProjectGroup
  // clears the group's project assignments (persisting both sides), so its
  // projects return to the ungrouped section — the projects themselves are
  // never touched. The confirm dialog guards against a mis-click; the group's
  // persisted collapsed marker is dropped alongside so the set never
  // accumulates stale ids.
  // Task 892-B ⑤: useConfirmDialog powers the proposal-first delete — its
  // message is a ReactNode, so the cascade checkbox rides inside the dialog
  // (same pattern as the session-group flow in ProjectTreeOrganization).
  const projectGroupDeleteDialog = useConfirmDialog();
  const dissolveProjectGroup = useCallback((group: ProjectGroup) => {
    void (async () => {
      setGroupMenuId(null);
      if (projectGroupsFromCatalog) {
        // A-slice 提案制: confirm=false returns the zero-write plan; the
        // dialog offers children-promoted (default) vs cascade (whole
        // subtree). Projects are never deleted — pointing ones ungroup.
        const plan = await proposeProjectGroupDelete(projectGroupCatalogRef.current, group.id);
        if (plan === null) { await refreshProjectGroups(); return; }
        const promoted = plan.promoted ?? [];
        const cascadeRef = { current: false };
        const titles = promoted.map((id) => groups.find((candidate) => candidate.id === id)?.title ?? id);
        const confirmed = await projectGroupDeleteDialog.confirm({
          title: t("projectGroup.delete"),
          message: <>
            <p>{t("projectGroup.deletePlanIntro", { title: group.title })}</p>
            {Array.isArray(plan.unfiledTopics) && plan.unfiledTopics.length > 0 && <p>{t("projectGroup.deletePlanUnfiled", { count: String(plan.unfiledTopics.length) })}</p>}
            {promoted.length > 0 && <>
              <p>{t("projectGroup.deletePlanPromote", { count: String(promoted.length), titles: titles.join(", ") })}</p>
              <label className="reasonix-confirm-dialog__cascade">
                <input
                  type="checkbox"
                  aria-label={t("projectGroup.deletePlanCascade", { count: String(promoted.length) })}
                  onChange={(event) => { cascadeRef.current = event.target.checked; }}
                />
                {t("projectGroup.deletePlanCascade", { count: String(promoted.length) })}
              </label>
            </>}
          </>,
          confirmLabel: t("projectGroup.delete"),
          cancelLabel: t("common.cancel"),
          tone: "danger",
        });
        if (!confirmed) return;
        await executeProjectGroupDelete(projectGroupCatalogRef.current, group.id, cascadeRef.current);
        setCollapsedProjectGroups((current) => dropProjectGroupCollapsed(current, group.id));
        // The re-pull adopts the backend roster and prunes assignments of
        // every id that left it (children included under cascade).
        await refreshProjectGroups();
        return;
      }
      const confirmed = await app.ConfirmAction({
        title: t("projectGroup.delete"),
        message: t("projectGroup.deleteConfirm", { title: group.title }),
        detail: t("projectGroup.deleteDetail"),
        confirmLabel: t("projectGroup.delete"),
        cancelLabel: t("common.cancel"),
        destructive: true,
      });
      if (!confirmed) return;
      const res = deleteProjectGroup(groups, assign, group.id);
      setGroups(res.groups);
      setAssign(res.assign);
      setCollapsedProjectGroups((current) => dropProjectGroupCollapsed(current, group.id));
    })();
  }, [groups, assign, t, projectGroupsFromCatalog, projectGroupDeleteDialog, refreshProjectGroups]);

  const collapseToggleLabel = t(canRestoreCollapsedView ? "projectTree.restoreCollapsedTooltip" : "projectTree.collapseAllTooltip");
  // tl1222: the workbench header shares the classic collapse toggle, so the
  // dedicated workbench labels (collapseAllWorkbench / restoreCollapsedWorkbench)
  // are retired along with their locale keys.

  const toggleCollapsedView = useCallback(() => {
    if (searchActive || folderKeys.length === 0) return;
    if (collapseSnapshot) {
      const currentFolderKeys = new Set(folderKeys);
      setExpanded(() => {
        const next = new Set<string>();
        for (const key of collapseSnapshot.expanded) {
          if (currentFolderKeys.has(key)) next.add(key);
        }
        return next;
      });
      updateManuallyCollapsed(() => {
        const next = new Set<string>();
        for (const key of collapseSnapshot.manuallyCollapsed) {
          if (currentFolderKeys.has(key)) next.add(key);
        }
        return next;
      });
      setCollapseSnapshot(null);
      return;
    }
    if (!hasExpandedFolders) return;
    setCollapseSnapshot({
      expanded: new Set(expanded),
      manuallyCollapsed: new Set(manuallyCollapsed),
    });
    setExpanded((prev) => {
      let changed = false;
      const next = new Set(prev);
      for (const key of folderKeys) {
        if (next.delete(key)) changed = true;
      }
      return changed ? next : prev;
    });
    updateManuallyCollapsed((prev) => {
      let changed = false;
      const next = new Set(prev);
      for (const key of folderKeys) {
        if (!next.has(key)) {
          next.add(key);
          changed = true;
        }
      }
      return changed ? next : prev;
    });
  }, [collapseSnapshot, expanded, folderKeys, hasExpandedFolders, manuallyCollapsed, searchActive, updateManuallyCollapsed]);

  const openWorkbenchHeaderMenu = (
    event: ReactMouseEvent<HTMLElement> | ReactKeyboardEvent<HTMLElement>,
    menu: Exclude<WorkbenchHeaderMenu, null>,
  ) => {
    event.preventDefault();
    event.stopPropagation();
    setMenuNodeKey(null);
    setMenuProject(null);
    setConfirmArchiveTarget(null);
    setConfirmRemoveProject(null);
    setFilterMenuOpen(false);
    setMenuPoint(contextMenuPointFromEvent(event));
    setWorkbenchHeaderMenu((value) => (value === menu ? null : menu));
  };
  const handleCreateTopic = async (scope: string, workspaceRoot: string, key: string) => {
    if (creatingRef.current) return;
    creatingRef.current = true;
    setCreatingProject(key);
    setMenuProject(null);
    setMenuPoint(null);
    setExpanded((prev) => {
      const next = new Set(prev);
      next.add(key);
      return next;
    });
    updateManuallyCollapsed((prev) => {
      if (!prev.has(key)) return prev;
      const next = new Set(prev);
      next.delete(key);
      return next;
    });
    try {
      if (onCreateTopic) {
        await onCreateTopic(scope, workspaceRoot);
        await refresh();
        await onTopicsChanged?.();
        return;
      }
      const targetRoot = scope === "project" ? workspaceRoot : "";
      const topic = await app.CreateTopic(scope, targetRoot, "");
      await refresh();
      await onTopicsChanged?.();
      await onOpenTopic(scope, targetRoot, topic.id);
    } catch {
      /* ignore */
    } finally {
      creatingRef.current = false;
      setCreatingProject(null);
    }
  };

  const handleCreateIsolatedWorktree = async (workspaceRoot: string) => {
    if (!workspaceRoot || isolatingProject) return;
    setIsolatingProject(workspaceRoot);
    closeMenu();
    try {
      await onCreateIsolatedWorktree?.(workspaceRoot);
    } catch (err) {
      showToast(err instanceof Error ? err.message : String(err), "error", { durationMs: 6000 });
    } finally {
      setIsolatingProject(null);
    }
  };
  const trashTopicAny = (topicId: string) => remoteSessionActions.remove(topicId, () => trashTopic(topicId));
  // Deliberately skips the active-work red flag — that is TrashTopicForce's contract —
  // while the file move itself still goes through the removal guard (recycle semantics).
  const trashTopicForceAny = (topicId: string) => remoteSessionActions.remove(topicId, () => app.TrashTopicForce(topicId));
  // tl 1103: the trash menu item's only entry. The ordinary TrashTopic runs
  // first; when it fails with a lease-busy-class error (the exact family
  // TrashTopicForce exists for — the backend marks them) a confirm dialog
  // offers the force path with the failure reason spelled out. Any other
  // failure keeps the plain error toast. Cancelling does nothing.
  const trashTopicForceableAny = (topicId: string) => remoteSessionActions.remove(topicId, () =>
    trashTopic(topicId, async (error) => {
      if (!isTopicArchiveForceableError(error)) return false;
      const confirmed = await app.ConfirmAction({
        title: t("history.moveToTrash"),
        message: t("projectTree.forceArchiveConfirm", { reason: topicArchiveForceableReason(error) }),
        detail: "",
        confirmLabel: t("projectTree.forceArchiveTopic"),
        cancelLabel: t("common.cancel"),
        destructive: true,
      }).catch(() => false);
      if (!confirmed) return true;
      await trashTopicForceAny(topicId);
      return true;
    }));
  const startRenameTopic = (node: ProjectNode, label: string) => {
    setMenuNodeKey(null);
    setMenuProject(null);
    setMenuPoint(null);
    setConfirmArchiveTarget(null);
    setEditingTopic(node.topicId ?? null);
    setTopicDraft(label);
  };

  const startRenameProject = (key: string, root: string, label: string) => {
    setMenuProject(null);
    setMenuNodeKey(null);
    setMenuPoint(null);
    setConfirmRemoveProject(null);
    setEditingProject({ key, root });
    setProjectDraft(label);
  };

  const commitRenameTopic = async (topicId: string) => {
    const title = topicDraft.trim();
    setEditingTopic(null);
    if (!title) return;
    try {
      if (await remoteSessionActions.mutate(topicId, (remote) => app.RenameRemoteProjectSession(remote.hostId, remote.workspace, remote.name, title))) return;
      if (onRenameTopic) await onRenameTopic(topicId, title);
      else await app.RenameTopic(topicId, title);
      // Paint the new label immediately; the catalog event round-trip can lag.
      setTree((current) => applyRuntimeProjection(projectTreeWithTopicTitle(current, topicId, title)));
      await refresh();
      if (!onRenameTopic) await onTopicsChanged?.();
    } catch (err) {
      showToast(err instanceof Error ? err.message : String(err), "error");
    }
  };

  const aiRenameSession = async (topicId: string) => {
    setAiRenamingTopic(topicId);
    try {
      const title = await app.AIRenameSession(topicId);
      await refresh();
      await onTopicsChanged?.();
      if (title) showToast(t("projectTree.aiRenameDone", { title }));
    } catch (err) {
      showToast(err instanceof Error ? err.message : String(err), "error");
    } finally {
      setAiRenamingTopic(null);
    }
  };

  const commitRenameProject = async (root: string) => {
    const title = projectDraft.trim();
    setEditingProject(null);
    if (!title) return;
    try {
      if (!await renameRemoteProjectTitle(root, title)) await app.RenameProject(root, title);
      await refresh();
    } catch (err) {
      showToast(err instanceof Error ? err.message : String(err), "error");
    }
  };

  const setTopicPinned = async (topicId: string, pinned: boolean) => {
    setMenuNodeKey(null);
    setMenuPoint(null);
    try {
      if (await remoteSessionActions.mutate(topicId, (remote) => app.SetRemoteSessionPinned(remote.hostId, remote.workspace, remote.name, pinned))) return;
      await app.SetTopicPinned(topicId, pinned);
      await refresh();
      await onTopicsChanged?.();
    } catch (err) {
      showToast(err instanceof Error ? err.message : String(err), "error");
    }
  };

  const setProjectPinned = async (workspaceRoot: string, pinned: boolean) => {
    if (!workspaceRoot) return;
    try {
      await app.SetProjectPinned(workspaceRoot, pinned);
      setMenuProject(null);
      setMenuPoint(null);
      await refresh();
      await onTopicsChanged?.();
    } catch (err) {
      showToast(err instanceof Error ? err.message : String(err), "error");
    }
  };

  const copyProjectPath = async (path: string) => {
    if (!path) return;
    try {
      await navigator.clipboard?.writeText(path);
    } catch {
      /* ignore */
    }
  };

  const removeProject = async (path: string) => {
    if (!path) return;
    try {
      await app.RemoveWorkspace(path);
      setMenuProject(null);
      setMenuPoint(null);
      setConfirmRemoveProject(null);
      await refresh();
    } catch (err) {
      showToast(err instanceof Error ? err.message : String(err), "error");
    }
  };

  const setProjectColor = async (path: string, color: string) => {
    try {
      await app.SetProjectColor(path, color);
      setMenuProject(null);
      setMenuPoint(null);
      await refresh();
      await onTopicsChanged?.();
    } catch {
      /* ignore */
    }
  };

  // Task 47: the folder move is registry-wide (project key plus every session's
  // recorded workspace), so the picker only supplies the new path and the backend
  // owns the rewrite order. A cancelled picker returns "" and changes nothing.
  const relocateProject = async (path: string) => {
    try {
      const picked = await app.PickProjectFolder(path);
      if (!picked) return;
      await app.RelocateProject(path, picked);
      setMenuProject(null);
      setMenuPoint(null);
      await refresh();
      await onTopicsChanged?.();
    } catch (err) {
      console.error("relocate project failed", err);
    }
  };

  const visibleTree = useMemo(() => {
    const q = query.trim().toLowerCase();
    // Time filter: compute cutoff timestamp.
    const diff = timeFilter === "1h" ? 60 * 60 * 1000
      : timeFilter === "3h" ? 3 * 60 * 60 * 1000
      : timeFilter === "5h" ? 5 * 60 * 60 * 1000
      : timeFilter === "1d" ? 24 * 60 * 60 * 1000
      : 0;
    const nthLatestActivity = (n: number): number | null => {
      const times = new Set<number>();
      const collect = (nodes: ProjectNode[]) => {
        for (const node of nodes) {
          if (node.kind === "topic" || node.kind === "global_topic") times.add(topicActivityTime(node));
          collect(asArray(node.children));
        }
      };
      collect(treeWithRemoteSessions);
      const sorted = [...times].sort((a, b) => b - a);
      return sorted.length === 0 ? null : sorted[Math.min(n, sorted.length) - 1];
    };
    const cutoff: number | null = timeFilter === "all" ? null
      : timeFilter === "10" ? nthLatestActivity(10)
      : timeFilter === "20" ? nthLatestActivity(20)
      : Date.now() - diff;
    const topicMatchesTime = (node: ProjectNode) => {
      if (cutoff === null) return true;
      return topicActivityTime(node) >= cutoff;
    };
    const matchesQuery = (node: ProjectNode) =>
      [node.label, node.root, node.topicId].some((value) => (value ?? "").toLowerCase().includes(q));
    const filterNode = (node: ProjectNode): ProjectNode | null => {
      // For folder nodes: always show when time filter is active (so the tree structure remains navigable).
      const isFolder = node.kind === "project" || node.kind === "global_folder";
      // Color filter applies to colorable project nodes; only the project itself
      // carries a color (its descendants are topics), so filter on projectColor.
      if (colorFilter.length > 0 && node.kind === "project" && !colorFilter.includes((node.projectColor || "") as ProjectColorKey)) {
        return null;
      }
      const children = asArray(node.children)
        .map(filterNode)
        .filter((child): child is ProjectNode => child !== null);
      if (isFolder) {
        if (cutoff !== null && children.length === 0 && !matchesQuery(node) && q === "") return null;
        if (children.length > 0 || matchesQuery(node)) return { ...node, children };
        if (q) return null;
        // With only time filter, show folder if it has any child that matches the time.
        const hasTimeMatch = asArray(node.children).some((c) => topicMatchesTime(c));
        return hasTimeMatch ? { ...node, children: asArray(node.children).filter(topicMatchesTime) } : null;
      }
      if (!q && cutoff === null) return node;
      if (cutoff !== null && !topicMatchesTime(node)) return null;
      if (q && !matchesQuery(node)) return null;
      return node;
    };
    const filtered = treeWithRemoteSessions
      .map(filterNode)
      .filter((node): node is ProjectNode => node !== null);
    if (compactTopics) return arrangeWorkbenchTree(filtered, workbenchOrganizeMode, workbenchSortMode);
    if (creationTopics) return arrangeWorkbenchTree(filtered, "project", "updated");
    return arrangeClassicProjectTree(filtered, workbenchSortMode);
  }, [compactTopics, creationTopics, query, colorFilter, timeFilter, treeWithRemoteSessions, workbenchOrganizeMode, workbenchSortMode]);

  const lastColorFilterLog = useRef("");
  useEffect(() => {
    // The filter is fork-only and silently hides projects, so the visible count is the part
    // worth recording: it distinguishes "filter matched nothing" from "filter matched wrong".
    const signature = colorFilter.length === 0 ? "" : colorFilter.join(",");
    if (signature === lastColorFilterLog.current) return;
    lastColorFilterLog.current = signature;
    reportFrontendLog("project-colour-filter",
      signature === "" ? "colour filter cleared" : "colour filter applied",
      `colours=${signature || "none"} visible=${visibleTree.length}`);
  }, [colorFilter, visibleTree]);

  const pinnedTreeSections = useMemo<PinnedTreeSections>(() => {
    if (creationTopics) return { pinned: [], projects: visibleTree };
    return splitPinnedProjectTree(visibleTree, workbenchSortMode, compactTopics);
  }, [compactTopics, creationTopics, visibleTree, workbenchSortMode]);

  const classicTopics = !compactTopics && !creationTopics;
	const [foldExcessSessions, setFoldExcessSessions] = useState(() => loadFoldExcessSessions());
	useEffect(() => onFoldExcessSessionsChanged(() => setFoldExcessSessions(loadFoldExcessSessions())), []);
	// Task 102: with the preference off, expanded projects list all sessions at startup - no
	// truncation control appears because nothing is truncated.
  const classicTruncationActive = foldExcessSessions && classicTopics && query.trim() === "" && timeFilter === "all";

  const projectLabelByRoot = useMemo(() => {
    const map = new Map<string, string>();
    for (const nodeItem of tree) {
      if (!nodeItem) continue;
      if (nodeItem.kind === "project" && nodeItem.root) map.set(nodeItem.root, nodeItem.label || nodeItem.root);
      if (nodeItem.kind === "global_folder") map.set(GLOBAL_PROJECT_ORDER_KEY, nodeItem.label || "Global");
    }
    return map;
  }, [tree]);

  const scheduleHoverCard = useCallback((element: HTMLElement, rowKey: string, node: ProjectNode) => {
    if (hoverCardTimerRef.current !== null) window.clearTimeout(hoverCardTimerRef.current);
    hoverCardTimerRef.current = window.setTimeout(() => {
      hoverCardTimerRef.current = null;
      if (!element.isConnected) return;
      if (menuNodeKey || menuProject || editingTopic || editingProject || dragProjectRoot) return;
      const rect = element.getBoundingClientRect();
      const globalScope = node.kind === "global_topic" || node.kind === "global_session";
      const projectLabel = globalScope
        ? projectLabelByRoot.get(GLOBAL_PROJECT_ORDER_KEY) ?? "Global"
        : projectLabelByRoot.get(node.root ?? "") ?? "";
      setHoverCard({
        key: rowKey,
        card: projectTreeTopicHoverCardModel(node, t, projectLabel, node.sessionPath ? cachedSessionPurpose(node.sessionPath) : ""),
        left: rect.right + 10,
        top: Math.max(8, Math.min(rect.top, window.innerHeight - 150)),
        sessionPath: node.sessionPath ?? "",
      });
    }, 350);
  }, [menuNodeKey, menuProject, editingTopic, editingProject, dragProjectRoot, projectLabelByRoot, t]);

  // Opening an old session from history can land on a topic hidden behind the
  // classic show-more window; reveal that folder so the active row stays visible.
  useEffect(() => {
    if (!classicTruncationActive) return;
    const revealKeys: string[] = [];
    for (const nodeItem of visibleTree) {
      if (!nodeItem || (nodeItem.kind !== "project" && nodeItem.kind !== "global_folder")) continue;
      const children = asArray(nodeItem.children);
      const activeIndex = children.findIndex((child) =>
        topicIsActive(child, activeScope, activeWorkspaceRoot, activeTopicId, activeSessionPath) ||
        asArray(child.children).some((grand) => topicIsActive(grand, activeScope, activeWorkspaceRoot, activeTopicId, activeSessionPath)));
      if (activeIndex >= CLASSIC_TOPIC_PREVIEW_LIMIT) revealKeys.push(projectNodeKey(nodeItem, 0));
    }
    if (revealKeys.length === 0) return;
    setShowAllTopics((prev) => {
      if (revealKeys.every((key) => prev.has(key))) return prev;
      const next = new Set(prev);
      for (const key of revealKeys) next.add(key);
      return next;
    });
  }, [classicTruncationActive, visibleTree, activeScope, activeWorkspaceRoot, activeTopicId, activeSessionPath]);

  const projectDragEnabled = query.trim() === "";

  const commitProjectReorder = useCallback(async (draggedRoot: string, targetRoot: string, position: ProjectDropPosition) => {
    const nextRoots = reorderedProjectRoots(tree, draggedRoot, targetRoot, position);
    const currentRoots = projectTreeProjectRoots(tree);
    if (nextRoots.join("\n") === currentRoots.join("\n")) return;
    setTree((current) => applyProjectOrder(current, nextRoots));
    try {
      await app.ReorderProjects(nextRoots);
      await refresh();
      await onTopicsChanged?.();
    } catch {
      await refresh();
    }
  }, [onTopicsChanged, refresh, tree]);

  const organization = useProjectTreeOrganization({ tree, refresh, onTopicsChanged, organizationRevision });

  const clearProjectDrag = useCallback(() => {
    setDragProjectRoot(null);
    setDropProject(null);
    // tl 1199 ②: the group-header landing highlight belongs to the same
    // gesture — it must never outlive the drag.
    setProjectDropInto(null);
  }, []);

  useEffect(() => {
    if (!dragProjectRoot) return;
    window.addEventListener("dragend", clearProjectDrag);
    window.addEventListener("drop", clearProjectDrag);
    window.addEventListener("blur", clearProjectDrag);
    return () => {
      window.removeEventListener("dragend", clearProjectDrag);
      window.removeEventListener("drop", clearProjectDrag);
      window.removeEventListener("blur", clearProjectDrag);
    };
  }, [clearProjectDrag, dragProjectRoot]);

  const activeAncestorKeys = useMemo(
    () => activeRemote ? activeRemoteProjectAncestorKeys(treeWithRemoteSessions, activeRemote, projectNodeKey) : activeSessionAncestorKeys(treeWithRemoteSessions, activeScope, activeWorkspaceRoot, activeTopicId, activeSessionPath),
    [activeRemote, activeScope, activeSessionPath, activeTopicId, activeWorkspaceRoot, treeWithRemoteSessions],
  );
  useEffect(() => {
    if (activeAncestorKeys.length === 0) return;
    setExpanded((prev) => {
      let changed = false;
      const next = new Set(prev);
      for (const key of activeAncestorKeys) {
        if (manuallyCollapsed.has(key) || next.has(key)) continue;
        next.add(key);
        changed = true;
      }
      return changed ? next : prev;
    });
  }, [activeAncestorKeys, manuallyCollapsed]);

  const projectTreeDiagnosticSnapshot = useMemo<ProjectTreeDiagnosticSnapshot>(() => {
    const sessionSummary = summarizeProjectTreeSessions({
      tree,
      visibleTree,
      expanded,
      showAllTopics,
      classicTruncationActive,
      queryActive: query.trim().length > 0,
      timeFilterActive: timeFilter !== "all",
      projectNodeKey,
      isActive: (node) => topicIsActive(node, activeScope, activeWorkspaceRoot, activeTopicId, activeSessionPath),
      isUnread: (node) => projectTreeTopicHasUnreadActivity(node, readActivity, activeScope, activeWorkspaceRoot, activeTopicId, activeSessionPath, readBaselineAt),
    });
    return {
      ...sessionSummary,
      directoryState: catalogStatus.state,
      scope: activeScope === "global" ? "global" : activeScope ? "project" : "unknown",
      variant,
      timeFilter,
      queryActive: query.trim().length > 0,
      timeFilterActive: timeFilter !== "all",
      catalogPartial: catalogStatus.state !== "ready"
        || (catalogStatus.repairActive ?? 0) > 0
        || (catalogStatus.unindexedTargetCount ?? 0) > 0
        || Boolean(catalogStatus.lastError),
      catalogRebuilding: catalogStatus.state === "rebuilding",
      catalogRevision: catalogStatus.revision,
      catalogIndexed: catalogStatus.indexed,
      catalogTotal: catalogStatus.total,
      unloadedSessions: Math.max(0, catalogStatus.total - sessionSummary.workspaceSessions),
      repairPending: catalogStatus.repairPending,
      treeRevision: latestRevisionRef.current,
      organizationRevision,
    };
  }, [activeScope, activeSessionPath, activeTopicId, activeWorkspaceRoot, catalogStatus, classicTruncationActive, expanded, organizationRevision, query, readActivity, readBaselineAt, showAllTopics, timeFilter, tree, variant, visibleTree]);

  useProjectTreeFrontendDiagnostics(projectTreeDiagnosticSnapshot);

  const renderNode = (node: ProjectNode | null | undefined, depth: number, section: "pinned" | "projects" = "projects", isVisible = true) => {
    if (!node) return null;
    const key = projectNodeKey(node, depth);
    const children = asArray(node.children);
    const isExpanded = query.trim() ? true : expanded.has(key);
    const hasChildren = children.length > 0;
    // Snapshot rows are shells with no children until the first page is loaded,
    // so every project folder must remain expandable while indexing.
    const folderDisclosure = projectTreeFolderDisclosure(hasChildren, isExpanded, true);

    if (isTopicNode(node) || isRuntimeSessionNode(node)) {
      const isSessionNode = isRuntimeSessionNode(node);
      const openRequest = projectTreeTopicOpenRequest(node);
      const scope = openRequest?.scope ?? "project";
      const scopeClass = scope === "global" ? " project-tree__topic--global" : " project-tree__topic--project";
      const accentStyle = projectAccentStyle(node.projectColor, scope === "global" ? "var(--project-tree-global-accent)" : undefined);
      const active = topicIsActive(node, activeScope, activeWorkspaceRoot, activeTopicId, activeSessionPath);
      const label = (node.label || node.topicId || "Untitled").replace(/^●\s*/, "");
      const activityAt = node.lastActivityAt || node.createdAt || 0;
      // Every variant is a single-line row with the activity time on the right;
      // classic moved there too so turns and the exact date live in the hover
      // preview card and the accessible label instead of a second meta line.
      const sideTimeVisible = true;
      const timeLabel = activityAt ? topicActivityLabel(activityAt, t, true) : topicUnknownTimeLabel(node, t);
      const exactTimeLabel = activityAt ? topicActivityDateLabel(activityAt) : "";
      const metaFull = projectTreeTopicMetaLine(node, t, compactTopics);
      const status = topicStatus(node);
      const statusLabel = topicStatusLabel(node, t);
      const archiveBlocked = projectTreeTopicArchiveBlocked(node);
      const waitingConfirmation = status === "waiting_confirmation";
      // Compact workbench: waiting shows an amber "待确认" pill instead of a
      // spinning orange dot, and that pill replaces the relative time so the
      // paused-for-user state is scannable in the background tab list.
      const showStatusInSide = status === "thinking" || status === "streaming" || status === "waiting_confirmation" || status === "background_job";
      const showWaitingPill = waitingConfirmation || status === "finishing" || status === "cancelling" || status === "unknown";
      const showSideTime = sideTimeVisible && !showWaitingPill;
      const unread = projectTreeTopicHasUnreadActivity(node, readActivity, activeScope, activeWorkspaceRoot, activeTopicId, activeSessionPath, readBaselineAt);
      const topicId = node.topicId ?? "";
      const topicTrashing = trashingTopics.has(topicId);
      const sessionPath = node.sessionPath?.trim() ?? "";
      const sessionTrashing = Boolean(sessionPath) && trashingSessions.has(sessionPath);
      const archiveTargetKey = isSessionNode ? projectTreeSessionArchiveTargetKey(sessionPath) : projectTreeTopicArchiveTargetKey(scope, node.root ?? "", topicId);
      const imSource = scope === "global" && topicId ? imTopicSources[topicId] : undefined;
      const imSourceLabel = imSource?.label || "";
      const imSourceTitle = imSourceLabel ? t("msg.fromIm", { source: imSourceLabel }) : "";
      const imSourcePlatform = (imSource?.platform || "im").replace(/[^a-z0-9_-]/gi, "").toLowerCase() || "im";
      const recoveryLabel = node.recoveryState === "recovery_only"
        ? t("projectTree.recoveryOnly")
        : node.recovered
          ? t("projectTree.recovered")
          : "";
      const title = [node.preview || "", label, recoveryLabel, imSourceTitle, statusLabel, metaFull, projectTreeDedupedExactTime(metaFull, exactTimeLabel)].filter(Boolean).join(" · ");
      // 938：hover 展示该会话登记的职责（sidecar purpose，按需拉取+缓存）。
      // 侧栏标题多为自动命名，purpose 才是真实职责——拼进 title 供原生 tooltip
      // 与 aria-label 展示；purposeRevision 订阅保证拉取到达后即时补上。
      const rowPurpose = node.sessionPath ? cachedSessionPurpose(node.sessionPath) ?? "" : "";
      const titleWithPurpose = rowPurpose ? `${title}\n${rowPurpose}` : title;
      const topicMenuOpen = menuNodeKey === key;
      const pinned = Boolean(node.pinned);
      const pinLabel = t(pinned ? "projectTree.unpinTopic" : "projectTree.pinTopic");
      const openTopicMenu = (event: ReactMouseEvent<HTMLElement> | ReactKeyboardEvent<HTMLElement>) => {
        event.preventDefault();
        event.stopPropagation();
        setMenuProject(null);
        setConfirmRemoveProject(null);
        setMenuPoint(contextMenuPointFromEvent(event));
        setMenuNodeKey(key);
        setConfirmArchiveTarget(null);
        // 938：记住右键的行元素——「编辑职责」弹窗的锚点（菜单关闭后仍指向该行）。
        purposeEditorAnchorRef.current = event.currentTarget as HTMLElement;
      };
      // tl 1103 会话右键菜单（一级 9 项 / 17 功能，二级聚合）——顺序即需求规格表：
      //   置顶 / 重命名 / AI 重命名 / 已读 ── 分组 ▸ ── 复制 ▸ / 打开方式 ▸ /
      //   编辑职责 ── 移到回收站（唯一 danger 项）。
      // 强制归档不再占一级：回收站失败且后端标记 forceable（lease busy 类）时弹
      // 二次确认走 force（trashTopicForceableAny）；其他失败照常报错。远端会话
      // 无本地路径/工作区，复制 ▸ 打开方式 ▸ 两项不渲染。
      const organizationItems = organization.topicMenuItems(node, t);
      const workspaceForOpen = openRequest?.workspaceRoot ?? node.root ?? "";
      const copyToClipboard = (value: string) => {
        void writeClipboardText(value)
          .then(() => showToast(t("msg.copied")))
          .catch(() => {});
      };
      const topicMenuItems: ContextMenuItem[] = [
        ...(projectTreeTopicMenuOffersPin(variant)
          ? [
              {
                key: pinned ? "unpin" : "pin",
                icon: <Pin size={13} />,
                label: pinLabel,
                onSelect: () => void setTopicPinned(topicId, !pinned),
              },
            ]
          : []),
        {
          key: "rename",
          icon: <Pencil size={13} />,
          label: t("projectTree.renameTopic"),
          onSelect: () => startRenameTopic(node, label),
        },
        {
          key: "aiRename",
          icon: <Sparkles size={13} />,
          label: aiRenamingTopic === topicId ? t("projectTree.aiRenamingTopic") : t("projectTree.aiRenameTopic"),
          disabled: aiRenamingTopic !== null || Boolean(node.remoteSession),
          onSelect: () => void aiRenameSession(topicId),
        },
        // wt-zcode-288：会话行右键「已读」（tl 1149：单条会话语境不再叫「全部已读」；
        // 分组右键的同名操作作用于整个分组，保留原名）——当前上下文 = 该会话所属
        // 工作区（project scope 即同一工作区；Global 会话即整个 Global 区）。
        ...(openRequest
          ? [{
              key: "markAllRead",
              icon: <CheckCheck size={13} />,
              label: t("projectTree.markRead"),
              onSelect: () => {
                markAllRead(readActivityKeysInScope(treeWithRemoteSessions, openRequest.scope, openRequest.workspaceRoot));
                closeMenu();
              },
            }]
          : []),
        { type: "separator" as const, key: "ctx-sep-group" },
        // 分组整理（task 720/144）：各分组一行（当前组禁用）+ 分隔线 +
        // 「移出分组」（末尾出口）。tl 1168 返修：入口文案改动作语义「移动到
        // 分组」（与项目行入口共用 projectGroup.moveInto 三语键），ContextMenu
        // 的 submenu 自此是 hover 停留展开的浮动二级面板（点击=触屏、→/←=
        // 键盘），分组叶子不再撑长一级菜单；仍只此一层——嵌套 submenu 照旧
        // disabled；无分组可用时整个入口不渲染。
        ...(organizationItems.length > 0 ? [{
          type: "submenu" as const,
          key: "grouping",
          icon: <FolderInput size={13} />,
          label: t("projectGroup.moveInto"),
          items: organizationItems,
        }] : []),
        { type: "separator" as const, key: "ctx-sep-copy" },
        // tl 1103 新增「复制 ▸」：会话路径 / 日志路径 / 会话 ID，取值口径与
        // topicbar 复制菜单（lib/sessionInfoCopy）一致；嵌套叶子点击后菜单
        // 自动关闭（ContextMenu 契约），无需再调 closeMenu。
        ...(!node.remoteSession ? [{
          type: "submenu" as const,
          key: "copy-info",
          icon: <Copy size={13} />,
          label: t("projectTree.copyInfo"),
          items: [
            {
              key: "copy-session-path",
              icon: <Folder size={13} />,
              label: t("topicBar.copySessionPath"),
              disabled: !sessionPath,
              onSelect: () => copyToClipboard(sessionPath),
            },
            {
              key: "copy-log-path",
              icon: <FileText size={13} />,
              label: t("topicBar.copyLogPath"),
              disabled: !logDirectory,
              onSelect: () => copyToClipboard(logDirectory),
            },
            {
              key: "copy-session-id",
              icon: <FileJson size={13} />,
              label: t("topicBar.copySessionId"),
              disabled: !topicId,
              onSelect: () => copyToClipboard(topicId),
            },
          ] satisfies ContextMenuItem[],
        }] : []),
        // tl 1103 新增「打开方式 ▸」：复用 ExternalOpener 的发现列表（挂载时
        // 拉取一次）；目标路径 = 该会话的工作区，点击经 OpenLocalPathInExternalOpener
        // 调起（既有桥，偏好不回写——与 split-button 主键语义一致）。当前偏好项
        // 打 ✓（无偏好时跟 ExternalOpener 的 selected 口径落到第一项），只展示
        // 不落盘。
        ...(!node.remoteSession && workspaceForOpen && (externalOpeners?.openers.length ?? 0) > 0 ? [{
          type: "submenu" as const,
          key: "open-with",
          icon: <FolderOpen size={13} />,
          label: t("projectTree.openWith"),
          items: (externalOpeners?.openers ?? []).map((opener): ContextMenuItem => ({
            key: `open:${opener.id}`,
            icon: <OpenerIcon opener={opener} />,
            label: opener.name,
            // ExternalOpener 的 selected 口径：偏好项，无偏好落到第一项。
            checked: opener.id === (externalOpeners?.preferred || externalOpeners?.openers[0]?.id),
            onSelect: () => {
              void app.OpenLocalPathInExternalOpener(workspaceForOpen, opener.id).catch((err) => {
                showToast(t("externalOpener.failed", { name: opener.name, error: err instanceof Error ? err.message : String(err) }), "error");
              });
            },
          })),
        }] : []),
        // 938：编辑会话职责（sidecar purpose）——仅本地会话行可编辑（远端会话
        // 无本地 sidecar），弹窗走 SetSessionPurpose 既有写链。
        {
          key: "editPurpose",
          icon: <ClipboardPen size={13} />,
          label: t("projectTree.editPurpose"),
          disabled: !sessionPath || Boolean(node.remoteSession),
          onSelect: () => {
            closeMenu();
            if (!sessionPath) return;
            void loadSessionPurpose(sessionPath);
            setPurposeEditorSessionPath(sessionPath);
          },
        },
        { type: "separator" as const, key: "ctx-sep-trash" },
        {
          key: "trash",
          icon: <Archive className={topicTrashing ? "project-tree__archive-spinner" : undefined} size={13} />,
          label: confirmArchiveTarget === archiveTargetKey ? t("history.confirmMoveToTrash") : t("history.moveToTrash"),
          disabled: archiveBlocked || topicTrashing,
          danger: true,
          onSelect: () => {
            // tl 1103：普通回收站先行；lease busy 类失败由 trashTopicForceableAny
            // 弹二次确认引导到 force——一级不再单列「强制归档」。
            if (confirmArchiveTarget === archiveTargetKey) void trashTopicForceableAny(topicId);
            else setConfirmArchiveTarget(archiveTargetKey);
          },
        },
      ];
      if (!isSessionNode && editingTopic === topicId) {
        return (
          <div
            key={key}
            className={`project-tree__topic project-tree__topic--editing${active ? " project-tree__topic--active" : ""}${imSource ? " project-tree__topic--im-source" : ""}${!classicTopics && metaFull ? " project-tree__topic--has-meta" : ""}`}
            style={{ paddingLeft: 14 + depth * 16 }}
          >
            <input
              autoFocus
              className="project-tree__topic-input"
              value={topicDraft}
              onChange={(event) => setTopicDraft(event.target.value)}
              onFocus={(event) => event.target.select()}
              onKeyDown={(event) => {
                if (event.key === "Enter") void commitRenameTopic(topicId);
                if (event.key === "Escape") setEditingTopic(null);
              }}
              onBlur={() => void commitRenameTopic(topicId)}
            />
          </div>
        );
      }
      const shortcutIndex = showShortcutBadges && isVisible && topicIndexRef.current < 9 ? topicIndexRef.current + 1 : 0;
      if (shortcutIndex > 0) topicIndexRef.current++;
      // Collect visible topics in render order for shortcut navigation
      if (openRequest && isVisible) {
        visibleTopicsCollectorRef.current.push({
          scope: openRequest.scope,
          workspaceRoot: openRequest.workspaceRoot,
          topicId: openRequest.topicId,
          sessionPath: openRequest.sessionPath,
        });
      }
      const topicDrag = organization.topicRow(node, section === "pinned" || isSessionNode || Boolean(query || menuNodeKey || editingTopic || dragProjectRoot || creatingProject));
      const row = (
        <div
          className={`project-tree__topic${scopeClass}${isSessionNode ? " project-tree__topic--session" : ""}${active ? " project-tree__topic--active" : ""}${node.running ? " project-tree__topic--running" : ""}${status ? ` project-tree__topic--status-${status}` : ""}${unread ? " project-tree__topic--unread" : ""}${!isSessionNode && pinned ? " project-tree__topic--pinned" : ""}${topicMenuOpen ? " project-tree__topic--menu-open" : ""}${topicDrag.className}${sideTimeVisible && (timeLabel || showStatusInSide || showWaitingPill) ? " project-tree__topic--with-side" : metaFull ? " project-tree__topic--has-meta" : ""}${imSource ? " project-tree__topic--im-source" : ""}${shortcutIndex > 0 ? " project-tree__topic--show-shortcut" : ""}`}
          style={accentStyle}
          {...topicDrag.props}
          onContextMenu={openTopicMenu}
          onMouseEnter={(event) => {
            // 938：hover 即拉职责（缓存命中零成本）；classic 另起 hover 卡。
            if (node.sessionPath) void loadSessionPurpose(node.sessionPath);
            if (classicTopics) scheduleHoverCard(event.currentTarget, key, node);
          }}
          onMouseLeave={classicTopics ? cancelHoverCard : undefined}
          onMouseDown={classicTopics ? cancelHoverCard : undefined}
        >
          <button
            type="button"
            className="project-tree__topic-main"
            title={classicTopics ? undefined : titleWithPurpose}
            aria-label={classicTopics ? titleWithPurpose : undefined}
            style={{ paddingLeft: 14 + depth * 16 }}
            onClick={() => {
              const remote = node.remoteSession ?? remoteSessionActions.resolve(topicId);
              if (!openRequest && !remote) return;
              const nextClick = { rowKey: key, canRename: !isSessionNode };
              const pending = clickTimerRef.current;
              if (pending !== null) {
                clearTimeout(pending.timer);
                clickTimerRef.current = null;
                if (projectTreeShouldSuppressOpenForRename(pending, nextClick)) return;
              }
              const timer = setTimeout(() => {
                if (clickTimerRef.current?.timer === timer) clickTimerRef.current = null;
                markNodeRead(node);
                if (!openRemoteSessionNode(remote, openRemoteProject) && openRequest) {
                  onOpenTopic(openRequest.scope, openRequest.workspaceRoot, openRequest.topicId, openRequest.sessionPath);
                }
              }, 200);
              clickTimerRef.current = { ...nextClick, timer };
            }}
            onKeyDown={(event) => {
              if (event.key === "ContextMenu" || (event.shiftKey && event.key === "F10")) {
                openTopicMenu(event);
              }
            }}
            onDoubleClick={(event) => {
              if (isSessionNode) return;
              event.stopPropagation();
              if (clickTimerRef.current !== null && clickTimerRef.current.rowKey === key) {
                clearTimeout(clickTimerRef.current.timer);
                clickTimerRef.current = null;
              }
              startRenameTopic(node, label);
            }}
          >
            {/* Task 827 ③: the session row's icon-column placeholder — the
                same fixed width every row reserves, so the title starts one
                indent step right of its container row's title. The status dot
                keeps its established home in the side area; this column only
                holds the grid. Hidden in workbench/creation, which have their
                own fixed paddings. */}
            <span className="project-tree__topic-lead" aria-hidden="true" />
            <span className="project-tree__topic-copy">
              <span className="project-tree__topic-heading">
                <span className="project-tree__topic-label">{label}</span>
                {/* Task 274 ①: model visibility on the sidebar row — provider
                    shown inline, the full modelRef on hover (title/data attr).
                    Absent when the host cannot see the runtime: no guess. */}
                {node.modelRef ? (
                  <span className="project-tree__topic-time" title={node.modelRef} data-model={node.modelRef}>
                    {node.provider || node.modelRef}
                  </span>
                ) : null}
                {recoveryLabel && <span className="project-tree__topic-recovery" title={recoveryLabel}>{recoveryLabel}</span>}
                {imSource && (
                  <span
                    className={`project-tree__topic-im project-tree__topic-im--${imSourcePlatform}`}
                    title={imSourceTitle}
                    aria-label={imSourceTitle}
                  >
                    <MessageSquare size={11} />
                    <span>{imSourceLabel}</span>
                  </span>
                )}
                {!compactTopics && statusLabel && (!classicTopics || status === "paused" || status === "awaiting_delivery" || status === "error") && (
                  <span className={`project-tree__topic-status project-tree__topic-status--${status}`}>{statusLabel}</span>
                )}
              </span>
            </span>
            {sideTimeVisible && (
              <span className={`project-tree__topic-side${!timeLabel && !showStatusInSide && !showWaitingPill ? " project-tree__topic-side--empty" : ""}`}>
                {showWaitingPill && statusLabel ? (
                  <span
                    className="project-tree__topic-waiting-pill"
                    title={statusLabel}
                  >
                    {statusLabel}
                  </span>
                ) : (
                  <>
                    {showStatusInSide && (
                      <span
                        className={`project-tree__topic-state project-tree__topic-state--${status}`}
                        title={statusLabel}
                        aria-hidden="true"
                      />
                    )}
                    {showSideTime && timeLabel && (
                      <span className="project-tree__topic-time" aria-hidden="true">{timeLabel}</span>
                    )}
                  </>
                )}
              </span>
            )}
            {compactTopics && statusLabel && !showWaitingPill && (
              <span className="sr-only">
                {statusLabel}
              </span>
            )}
            {compactTopics && metaFull && (
              <span className="sr-only">
                {metaFull}
              </span>
            )}
          </button>
          {unread && <span className="project-tree__topic-unread-dot" aria-hidden="true" />}
          {projectTreeShouldRenderTopicActions(isSessionNode, variant, unread) && !(node.remoteSession && !node.remoteSession.name) && (
            <span
              className="project-tree__topic-actions"
              aria-label={t("projectTree.topicActions")}
              onMouseEnter={classicTopics ? cancelHoverCard : undefined}
              onFocus={classicTopics ? cancelHoverCard : undefined}
            >
              <Tooltip label={pinLabel} side="top" className="project-tree__topic-action-slot">
                <button
                  className={`project-tree__topic-action${pinned ? " project-tree__topic-action--pinned" : ""}`}
                  type="button"
                  aria-label={pinLabel}
                  aria-pressed={pinned}
                  onClick={(event) => {
                    event.preventDefault();
                    event.stopPropagation();
                    void setTopicPinned(topicId, !pinned);
                  }}
                >
                  <Pin size={15} aria-hidden="true" />
                </button>
              </Tooltip>
              <Tooltip label={t("projectTree.archiveTopic")} side="top" className="project-tree__topic-action-slot">
                <button
                  className={`project-tree__topic-action project-tree__topic-action--archive${topicTrashing ? " project-tree__topic-action--busy" : ""}`}
                  type="button"
                  aria-label={t("projectTree.archiveTopic")}
                  aria-busy={topicTrashing} disabled={archiveBlocked || topicTrashing}
                  onClick={(event) => {
                    event.preventDefault();
                    event.stopPropagation();
                    void trashTopicAny(topicId);
                  }}
                >
                  <Archive className={topicTrashing ? "project-tree__archive-spinner" : undefined} size={15} aria-hidden="true" />
                </button>
              </Tooltip>
            </span>
          )}
          {isSessionNode ? (
            <ProjectTreeSessionArchiveMenu
              open={topicMenuOpen} point={menuPoint} sessionPath={sessionPath} blocked={archiveBlocked || topicTrashing} busy={sessionTrashing} confirmed={confirmArchiveTarget === archiveTargetKey}
              onConfirm={() => setConfirmArchiveTarget(archiveTargetKey)} onTrash={() => { setConfirmArchiveTarget(null); void trashSession(sessionPath); }} onClose={closeMenu} />
          ) : <ContextMenu open={topicMenuOpen} point={menuPoint} items={topicMenuItems} minWidth={178} ariaLabel={t("projectTree.topicActions")} onClose={closeMenu} />}
          {shortcutIndex > 0 && (
            <span className="project-tree__topic-shortcut" aria-hidden="true">
              {topicShortcutLabel(shortcutIndex, shortcutPlatform)}
            </span>
          )}
        </div>
      );
      return (
        <div key={key}>
          {row}
          {hasChildren && (
            <div className={`project-tree__children${isExpanded ? " project-tree__children--expanded" : ""}`}>
              <div className="project-tree__children-inner">
                {children.map((child) => renderNode(child, depth + 1, section, isVisible && isExpanded))}
              </div>
            </div>
          )}
        </div>
      );
    }

    const scope = node.kind === "global_folder" ? "global" : "project";
    const scopeClass = scope === "global" ? " project-tree__folder--global" : " project-tree__folder--project";
    const pinnedClass = node.pinned ? " project-tree__folder--pinned" : "";
    const accentStyle = projectAccentStyle(node.projectColor, scope === "global" ? "var(--project-tree-global-accent)" : undefined);
    const projectRoot = scope === "global" ? "" : node.root ?? "";
    const projectDragKey = scope === "global" ? GLOBAL_PROJECT_ORDER_KEY : projectRoot;
    const projectPath = node.root ?? "";
    const colorTargetRoot = scope === "global" ? "" : projectPath;
    const projectLabel = node.label || (scope === "global" ? "Global" : "Untitled");
    const projectPinned = Boolean(node.pinned);
    const projectActive = node.remote ? Boolean(activeRemote && remoteProjectKey(activeRemote) === remoteProjectKey(node.remote)) : activeScope === scope && (scope === "global" || activeWorkspaceRoot === node.root);
    const projectMenuOpen = menuProject?.key === key;
    const activeTopicInProject = Boolean(activeTopicId) && activeScope === scope && (scope === "global" || activeWorkspaceRoot === projectRoot);
    const sourceProjectNode = tree.find((candidate) => scope === "global"
      ? candidate.kind === "global_folder"
      : candidate.kind === "project" && candidate.root === projectRoot);
    const activeTopicArchiveBlocked = asArray(sourceProjectNode?.children).some((candidate) =>
      isTopicNode(candidate) && candidate.topicId === activeTopicId && projectTreeTopicArchiveBlocked(candidate));
    const draggableProject = section !== "pinned" && projectDragEnabled && depth === 0 && Boolean(projectDragKey) && editingProject?.key !== key;
    const projectDropPosition = dropProject?.root === projectDragKey ? dropProject?.position ?? null : null;
    const handleProjectDragStart = (event: ReactDragEvent<HTMLElement>) => {
      if (!draggableProject) return;
      const target = event.target;
      if (target instanceof Element && target.closest(".project-tree__action-slot,.project-tree__folder-action-slot")) {
        event.preventDefault();
        return;
      }
      event.dataTransfer.effectAllowed = "move";
      // tl 1199 ②: the dedicated type rides alongside text/plain (some drop
      // targets only read the plain-text channel) so the group header can
      // tell a project drag from a topic drag even via the dataTransfer.
      event.dataTransfer.setData(PROJECT_ROW_DRAG_TYPE, projectDragKey);
      event.dataTransfer.setData("text/plain", projectDragKey);
      setDragProjectRoot(projectDragKey);
      setDropProject(null);
      setProjectDropInto(null);
    };
    const handleProjectDragOver = (event: ReactDragEvent<HTMLDivElement>) => {
      if (!draggableProject || !dragProjectRoot || dragProjectRoot === projectDragKey) return;
      event.preventDefault();
      event.dataTransfer.dropEffect = "move";
      const rect = event.currentTarget.getBoundingClientRect();
      const position: ProjectDropPosition = event.clientY < rect.top + rect.height / 2 ? "before" : "after";
      setDropProject((current) => {
        if (current?.root === projectDragKey && current?.position === position) return current;
        return { root: projectDragKey, position };
      });
    };
    const handleProjectDrop = (event: ReactDragEvent<HTMLDivElement>) => {
      if (!draggableProject) return;
      const draggedRoot = dragProjectRoot
        || event.dataTransfer.getData(PROJECT_ROW_DRAG_TYPE)
        || event.dataTransfer.getData("text/plain");
      const position = dropProject?.root === projectDragKey ? dropProject?.position ?? "after" : "after";
      event.preventDefault();
      clearProjectDrag();
      if (!draggedRoot || draggedRoot === projectDragKey) return;
      // tl 1199 ②: a drop on a row of ANOTHER group is a move, not a reorder —
      // it runs the exact chain the row menu's 「移动到分组」 uses
      // (handleMoveToGroup), so the drag can never diverge from the menu (the
      // pre-fix bug: a cross-group drop only rewrote the flat order, the
      // membership never changed, and the row silently stayed in its old
      // group). Same group (or both ungrouped) keeps the flat reorder.
      if (scope === "project") {
        const sourceGroupId = groupForProjectRoot(draggedRoot)?.id ?? null;
        const targetGroupId = groupForProjectRoot(projectRoot)?.id ?? null;
        if (sourceGroupId !== targetGroupId) {
          handleMoveToGroup(draggedRoot, targetGroupId);
          return;
        }
      }
      void commitProjectReorder(draggedRoot, projectDragKey, position);
    };
    const openProjectMenu = (event: ReactMouseEvent<HTMLElement> | ReactKeyboardEvent<HTMLElement>) => {
      event.preventDefault();
      event.stopPropagation();
      setMenuNodeKey(null);
      setConfirmArchiveTarget(null);
      setMenuPoint(contextMenuPointFromEvent(event));
      setMenuProject({ key, root: projectRoot, path: projectPath, scope, label: projectLabel });
      setConfirmRemoveProject(null);
      if (scope === "project" && projectRoot) {
        void app.IsolatedWorktreeAvailability(projectRoot).then((availability) => {
          setWorktreeAvailability((current) => ({
            ...current,
            [projectRoot]: { available: availability.available, reason: availability.reason },
          }));
        }).catch(() => {});
      }
    };
    const isolationAvailability = worktreeAvailability[projectRoot];
    const activeTopicArchiveTarget = activeTopicId ? projectTreeTopicArchiveTargetKey(scope, projectRoot, activeTopicId) : "";
    const isolatedWorkspaceItems: ContextMenuItem[] = scope === "project"
      ? [{
          key: "isolated-delivery-workspace",
          icon: <GitBranch size={13} />,
          label: (
            <span title={isolationAvailability?.reason || t("projectTree.createWorktreeHint")}>
              {isolatingProject === projectRoot ? t("projectTree.creatingWorktree") : t("projectTree.createWorktree")}
            </span>
          ),
          disabled: isolatingProject !== null || isolationAvailability?.available === false,
          onSelect: () => { void handleCreateIsolatedWorktree(projectRoot); },
        }]
      : [];
    const remoteProjectMenuItems = node.remote ? buildRemoteProjectMenuItems({ ref: node.remote, t, closeMenu, openRemoteProject, openRemoteWindow, setRemoteSessions, refresh, showToast }) : [];
    const projectMenuItems: ContextMenuItem[] = [
      {
        key: "new-group",
        icon: <FolderPlus size={13} />,
        label: t("projectTree.newGroup"),
        onSelect: () => organization.createGroup(node, t("projectTree.newGroup")),
      },
      {
        key: "new-session",
        icon: <Plus size={13} />,
        label: t("projectTree.newTopic"),
        onSelect: () => {
          void handleCreateTopic(scope, projectPath, key);
        },
      },
      // wt-zcode-288：项目分组右键「全部已读」——当前上下文 = 该分组子树内全部会话。
      {
        key: "mark-all-read",
        icon: <CheckCheck size={13} />,
        label: t("projectTree.markAllRead"),
        onSelect: () => {
          markAllRead(readActivityKeysInSubtree([node]));
          closeMenu();
        },
      },
      ...isolatedWorkspaceItems,
      {
        key: "rename",
        icon: <Pencil size={13} />,
        label: t("projectTree.renameProject"),
        onSelect: () => startRenameProject(key, projectRoot, projectLabel),
      },
      // tl 1200 ①（用户 20261027 拍板）：「在文件资源管理器中显示」「移动到
      // 分组」上移至颜色区之前——高频路径/入口操作不再沉到菜单尾段。
      { type: "separator" as const, key: "color-separator" },
      {
        key: "reveal",
        icon: <FolderOpen size={13} />,
        label: t(revealLabelKey(platform)),
        disabled: !projectPath,
        onSelect: () => {
          void app.RevealPath(projectPath).catch(() => {});
          closeMenu();
        },
      },
      ...(scope === "project"
        ? [
            {
              // tl 1149: in-menu submenu instead of the screen-centred
              // MoveToGroupPanel — same options, anchored to the row.
              // tl 1200 ①: moved above the color section (user decision).
              type: "submenu" as const,
              key: "move-to-project-group",
              icon: <FolderInput size={13} />,
              label: t("projectGroup.moveInto"),
              items: projectGroupMoveItems(projectPath ?? node.root),
            },
          ]
        : []),
      { type: "separator" as const, key: "color-menu-separator" },
      {
        // tl 1200 ②（用户 20261027 拍板）：9 色平铺收拢为一级「颜色 ▸」+
        // 二级浮动面板。二级交互（hover 停留 180ms 展开/移出关闭/键盘 → 进 ←
        // 出）由 ContextMenu 的 tl 1168 flyout 基建提供，叶子零额外接线；
        // 9 个选项、key 与 setProjectColor 落盘链原样保留（零功能丢失）。
        type: "submenu" as const,
        key: "color",
        icon: <SwatchBook size={13} />,
        label: t("projectTree.color"),
        items: PROJECT_COLOR_OPTIONS.map((option): ContextMenuItem => ({
          key: `color-${option.key || "default"}`,
          label: colorMenuLabel(projectColorLabel(t, option.key), option.key, (node.projectColor || "") === option.key),
          onSelect: () => {
            void setProjectColor(colorTargetRoot, option.key);
          },
        })),
      },
      { type: "separator" as const, key: "path-separator" },
      {
        key: "copy-path",
        icon: <Copy size={13} />,
        label: t("projectTree.copyPath"),
        disabled: !projectPath,
        onSelect: () => {
          void copyProjectPath(projectPath);
          closeMenu();
        },
      },
      ...(scope === "project"
        ? [
            {
              // tl 1200 ①:「移动到分组」已上移至颜色区之前；尾段保留低频
              // 管理项（relocate/versions/remove），序见任务回执清单。
              key: "relocate-project",
              icon: <FolderOpen size={13} />,
              label: t("projectTree.relocateProject"),
              onSelect: () => {
                void relocateProject(projectPath ?? node.root);
                closeMenu();
              },
            },
            {
              key: "topic-versions",
              icon: <GitBranch size={13} />,
              label: t("projectTree.manageVersions"),
              onSelect: () => {
                // Fixed entry point for version management. A transient notification
                // used to be the only way in, so missing it left no route back to the
                // dialog that holds the recovery branches and the merge action.
                requestTopicVersions({
                  scope,
                  workspaceRoot: projectPath ?? node.root,
                  topicId: node.topicId ?? "",
                  path: node.sessionPath,
                });
                closeMenu();
              },
            },
            { type: "separator" as const, key: "remove-separator" },
            {
              key: "remove",
              icon: <XCircle size={13} />,
              label: confirmRemoveProject === key ? t("projectTree.confirmRemoveProject") : t("projectTree.removeProject"),
              danger: true,
              onSelect: () => {
                if (confirmRemoveProject === key) void removeProject(projectPath);
                else setConfirmRemoveProject(key);
              },
            },
          ]
        : []),
    ];
    const workbenchProjectMenuItems: ContextMenuItem[] = [
      ...(scope === "project"
        ? [
            {
              key: projectPinned ? "unpin-project" : "pin-project",
              icon: <Pin size={13} />,
              label: t(projectPinned ? "projectTree.unpinProject" : "projectTree.pinProject"),
              onSelect: () => {
                void setProjectPinned(projectRoot, !projectPinned);
              },
            },
          ]
        : []),
      ...isolatedWorkspaceItems,
      {
        key: "reveal",
        icon: <FolderOpen size={13} />,
        label: t(revealLabelKey(platform)),
        disabled: !projectPath,
        onSelect: () => {
          void app.RevealPath(projectPath).catch(() => {});
          closeMenu();
        },
      },
      {
        key: "rename",
        icon: <Pencil size={13} />,
        label: t("projectTree.renameProjectWorkbench"),
        onSelect: () => startRenameProject(key, projectRoot, projectLabel),
      },
      // wt-zcode-288：workbench 变体的项目分组右键同样提供「全部已读」。
      {
        key: "mark-all-read",
        icon: <CheckCheck size={13} />,
        label: t("projectTree.markAllRead"),
        onSelect: () => {
          markAllRead(readActivityKeysInSubtree([node]));
          closeMenu();
        },
      },
      {
        key: "archive-active-topic",
        icon: <Archive className={activeTopicId && trashingTopics.has(activeTopicId) ? "project-tree__archive-spinner" : undefined} size={13} />,
        label: activeTopicArchiveTarget && confirmArchiveTarget === activeTopicArchiveTarget
          ? t("history.confirmMoveToTrash")
          : t("projectTree.archiveConversation"),
        disabled: !activeTopicInProject || !activeTopicId || activeTopicArchiveBlocked || Boolean(activeTopicId && trashingTopics.has(activeTopicId)),
        danger: true,
        onSelect: () => {
          if (!activeTopicId) return;
          if (confirmArchiveTarget === activeTopicArchiveTarget) void trashTopic(activeTopicId);
          else setConfirmArchiveTarget(activeTopicArchiveTarget);
        },
      },
      ...(scope === "project"
        ? [
            {
              // tl 1149: in-menu submenu instead of the screen-centred
              // MoveToGroupPanel — same options, anchored to the row.
              type: "submenu" as const,
              key: "move-to-project-group",
              icon: <FolderInput size={13} />,
              label: t("projectGroup.moveInto"),
              items: projectGroupMoveItems(projectPath ?? node.root),
            },
            {
              key: "relocate-project",
              icon: <FolderOpen size={13} />,
              label: t("projectTree.relocateProject"),
              onSelect: () => {
                void relocateProject(projectPath ?? node.root);
                closeMenu();
              },
            },
            {
              key: "topic-versions",
              icon: <GitBranch size={13} />,
              label: t("projectTree.manageVersions"),
              onSelect: () => {
                // Fixed entry point for version management. A transient notification
                // used to be the only way in, so missing it left no route back to the
                // dialog that holds the recovery branches and the merge action.
                requestTopicVersions({
                  scope,
                  workspaceRoot: projectPath ?? node.root,
                  topicId: node.topicId ?? "",
                  path: node.sessionPath,
                });
                closeMenu();
              },
            },
            { type: "separator" as const, key: "remove-separator" },
            {
              key: "remove",
              icon: <XCircle size={13} />,
              label: confirmRemoveProject === key ? t("projectTree.confirmRemoveProjectShort") : t("projectTree.removeProjectShort"),
              danger: true,
              onSelect: () => {
                if (confirmRemoveProject === key) void removeProject(projectPath);
                else setConfirmRemoveProject(key);
              },
            },
          ]
        : []),
    ];

    const folderShowAll = showAllTopics.has(key);
    const { visible: windowedChildren, hiddenCount } = classicTruncationActive
      ? classicTopicWindow(children, folderShowAll)
      : { visible: children, hiddenCount: 0 };
    const windowToggleVisible = classicTruncationActive && (hiddenCount > 0 || (folderShowAll && children.length > CLASSIC_TOPIC_PREVIEW_LIMIT));
    const backendPage = topicPageState[key];
    const renderFolderChildren = () => {
      if (!hasChildren) {
        const remoteGroupKey = node.remote ? remoteProjectKey(node.remote) : "";
        const remoteBusy = Boolean(remoteGroupBusy[remoteGroupKey]);
        const remoteError = remoteGroupError[remoteGroupKey] || "";
        if (node.remote) return <RemoteProjectEmptyState
          busy={remoteBusy} error={remoteError} ready={remoteServers[node.remote.hostId]?.[node.remote.workspace]?.state === "ready"}
          isExpanded={isExpanded} depth={depth} classicTopics={classicTopics} t={t} onEnsure={() => ensureRemoteGroupSessions(node.remote!.hostId, node.remote!.workspace)}
        />;
        // While the first topic page is still loading (cold start, catalog
        // reconcile in flight), show a skeleton instead of a blank folder or
        // a premature "no topics" placeholder.
        if (backendPage?.loading) {
          return (
            <div className={`project-tree__children${isExpanded ? " project-tree__children--expanded" : ""}`}>
              <div className="project-tree__children-inner">
                <div className="project-tree__skeleton" style={{ paddingLeft: 14 + (depth + 1) * 16 }} aria-hidden="true">
                  <span className="project-tree__skeleton-bar" />
                  <span className="project-tree__skeleton-bar project-tree__skeleton-bar--short" />
                  <span className="project-tree__skeleton-bar" />
                  <span className="project-tree__skeleton-bar project-tree__skeleton-bar--short" />
                </div>
              </div>
            </div>
          );
        }
        if (!classicTopics) return null;
        return (
          <div className={`project-tree__children${isExpanded ? " project-tree__children--expanded" : ""}`}>
            <div className="project-tree__children-inner">
              <div className="project-tree__topic-placeholder" style={{ paddingLeft: 14 + (depth + 1) * 16 }}>
                {t("projectTree.noTopics")}
              </div>
            </div>
          </div>
        );
      }
      return (
        <div className={`project-tree__children${isExpanded ? " project-tree__children--expanded" : ""}`}>
          <div className="project-tree__children-inner">
            <ProjectTreeGroupRows folder={node} children={windowedChildren} depth={depth + 1} section={section} visible={isVisible && isExpanded} organization={organization} renderNode={renderNode} t={t} onMarkAllRead={(members) => markAllRead(readActivityKeysInSubtree(members))} />
            {windowToggleVisible && (
              <button
                type="button"
                className="project-tree__topic-window-toggle"
                style={{ paddingLeft: 14 + (depth + 1) * 16 }}
                onClick={() => toggleShowAllTopics(key)}
              >
                {hiddenCount > 0 ? t("projectTree.showMoreTopics", { n: hiddenCount }) : t("projectTree.showFewerTopics")}
              </button>
            )}
            {/* 962：分页按钮只留给搜索结果（对齐上游 main-v2 的
                query.trim() && nextCursor 守卫）。普通浏览的完整性由
                loadProjectTopics 沿游标自动续拉保证，不再渲染此按钮。 */}
            {query.trim() && backendPage?.nextCursor && (
              <button
                type="button"
                className="project-tree__topic-window-toggle"
                style={{ paddingLeft: 14 + (depth + 1) * 16 }}
                disabled={backendPage.loading}
                aria-label={`${t("projectTree.loadMore")} · ${projectLabel}`}
                onClick={() => void loadProjectTopics(node, true)}
              >
                {backendPage.loading ? t("projectTree.indexing") : t("projectTree.loadMore")}
              </button>
            )}
          </div>
        </div>
      );
    };

    if (editingProject?.key === key) {
      return (
        <div key={key} className="project-tree__project-wrapper">
          <div
            className={`project-tree__folder project-tree__folder--editing${projectActive ? " project-tree__folder--active" : ""}`}
            style={{ paddingLeft: 8 + depth * 16 }}
          >
            <input
              autoFocus
              className="project-tree__folder-input"
              value={projectDraft}
              onChange={(event) => setProjectDraft(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") void commitRenameProject(projectRoot);
                if (event.key === "Escape") setEditingProject(null);
              }}
              onBlur={() => void commitRenameProject(projectRoot)}
            />
          </div>
          {renderFolderChildren()}
        </div>
      );
    }

    return (
      <div key={key} className="project-tree__project-wrapper">
        <div
          className={`project-tree__folder${scopeClass}${pinnedClass}${draggableProject ? " project-tree__folder--draggable" : ""}${projectActive ? " project-tree__folder--active" : ""}${projectMenuOpen ? " project-tree__folder--menu-open" : ""}${dragProjectRoot === projectDragKey ? " project-tree__folder--dragging" : ""}${projectDropPosition ? ` project-tree__folder--drop-${projectDropPosition}` : ""}`}
          style={accentStyle}
          draggable={draggableProject}
          aria-grabbed={draggableProject ? dragProjectRoot === projectRoot : undefined}
          onDragStart={handleProjectDragStart}
          onDragOver={handleProjectDragOver}
          onDragLeave={(event) => {
            if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDropProject(null);
          }}
          onDrop={handleProjectDrop}
          onDragEnd={clearProjectDrag}
          onContextMenu={openProjectMenu}
        >
          <button
            type="button"
            className="project-tree__folder-main"
            style={{ paddingLeft: 8 + depth * 16 }}
            onClick={() => {
              if (node.remote && !folderDisclosure.canExpand) return void openRemoteProject(node.remote, { focus: true });
              if (folderDisclosure.canExpand) {
                const willExpand = !expanded.has(key);
                toggleExpand(key, node);
                if (node.remote && willExpand) { void ensureRemoteGroupSessions(node.remote.hostId, node.remote.workspace); }
              }
            }}
            onKeyDown={(event) => {
              if (event.key === "ContextMenu" || (event.shiftKey && event.key === "F10")) {
                openProjectMenu(event);
              }
            }}
            aria-expanded={folderDisclosure.ariaExpanded}
          >
            <span className={folderDisclosure.iconStackClassName}>
              {node.remote ? <Cloud size={14} className="project-tree__folder-icon" /> : folderDisclosure.isOpen ? <FolderOpen size={14} className="project-tree__folder-icon" /> : <Folder size={14} className="project-tree__folder-icon" />}
              {/* Task 827 ③: the project color dot lives inside the icon column
                  (bottom-right overlay), so the label starts right after one
                  uniform icon column — the same grid every row follows. */}
              <span className="project-tree__folder-color" aria-hidden="true" />
            </span>
            <span className={`project-tree__folder-label${!hasChildren ? " project-tree__folder-label--empty" : ""}`}>
              {projectLabel}
              {node.isolatedWorktree && <WorktreeBadge size={11} />}
              {node.remote ? <span className={`project-tree__remote-badge project-tree__remote-badge--${remoteServeBadgeState(remoteServers[node.remote.hostId]?.[node.remote.workspace], remoteGroupBusy[remoteProjectKey(node.remote)])}`} aria-hidden="true" /> : null}
            </span>
            <ProjectTreeFolderActivity folder={node} />
          </button>
          {compactTopics && (
            <Tooltip label={t("projectTree.projectActions")} className="project-tree__folder-action-slot">
              <button
                type="button"
                className="project-tree__folder-action project-tree__folder-action--menu"
                aria-label={t("projectTree.projectActions")}
                aria-haspopup="menu"
                aria-expanded={projectMenuOpen}
                onClick={(e) => {
                  openProjectMenu(e);
                }}
              >
                <MoreHorizontal size={16} aria-hidden="true" />
              </button>
            </Tooltip>
          )}
          {!node.remote && <Tooltip label={t("projectTree.newTopicTooltip")} className={compactTopics ? "project-tree__folder-action-slot" : "project-tree__action-slot"}>
            <button
              type="button"
              className={compactTopics
                ? `project-tree__folder-action project-tree__folder-action--create${creatingProject === key ? " project-tree__folder-action--active" : ""}`
                : `project-tree__new-topic${creatingProject === key ? " project-tree__new-topic--active" : ""}`}
              aria-label={t("projectTree.newTopicTooltip")}
              disabled={creatingProject !== null}
              onClick={(e) => {
                e.stopPropagation();
                void handleCreateTopic(scope, projectPath, key);
              }}
            >
              {compactTopics ? <Plus size={15} aria-hidden="true" /> : <Plus size={12} aria-hidden="true" />}
            </button>
          </Tooltip>}
          <ContextMenu
            open={projectMenuOpen}
            point={menuPoint}
            items={node.remote ? remoteProjectMenuItems : compactTopics ? workbenchProjectMenuItems : projectMenuItems}
            minWidth={compactTopics ? 206 : 212}
            ariaLabel={t("projectTree.projectActions")}
            onClose={closeMenu}
          />
        </div>
        {renderFolderChildren()}
      </div>
    );
  };

  // tl1222: the workbench header's "more" menu (archive-all placeholder,
  // organize section, move-section-down placeholder, sort section) is retired —
  // organize moves into the merged filter panel (workbench-only section, see
  // renderClassicFilterControl) and sort already lived there in classic; the
  // two disabled placeholders were dead entries carrying no capability.
  // tl1157: the classic header folds creation down to one dropdown with
  // exactly three top-level entries (user-approved shape A). "New project"
  // expands a second level holding the two creation flows (blank project /
  // existing folder) so neither action is lost; "new project group" opens the
  // shared 827 NewGroupPanel and "remote connection" the shared wizard. The
  // standalone header create-group button is gone — the blank-area right-click
  // (881 ①) and the empty-state link (this task) keep the group entry visible.
  // Since tl1222 the workbench header consumes the same items.
  const classicCreateItems: ContextMenuItem[] = [
    {
      type: "submenu",
      key: "create-project",
      icon: <FolderPlus size={13} />,
      label: t("projectTree.createProject"),
      items: [
        { key: "blank-project", icon: <FolderPlus size={13} />, label: t("projectTree.createBlankProject"), disabled: addingProject, onSelect: () => { closeMenu(); openBlankProjectFlow(); } },
        { key: "use-existing-folder", icon: <FolderOpen size={13} />, label: t("projectTree.useExistingFolder"), disabled: addingProject, onSelect: () => { closeMenu(); void handleAddProject(); } },
      ],
    },
    { key: "new-project-group", icon: <FolderInput size={13} />, label: t("projectGroup.createNew"), onSelect: () => { closeMenu(); setNewGroupOpen(true); } },
    { key: "remote-connection", icon: <Server size={13} />, label: t("projectTree.remoteConnection"), onSelect: () => { closeMenu(); openRemoteConnectFlow(); } },
  ];

  const timeFilterDisplayLabel = timeFilter === "all" ? t("projectTree.timeFilterAll")
    : timeFilter === "10" ? t("projectTree.timeFilter10")
    : timeFilter === "20" ? t("projectTree.timeFilter20")
    : timeFilter === "1h" ? t("projectTree.timeFilter1h")
    : timeFilter === "3h" ? t("projectTree.timeFilter3h")
    : timeFilter === "5h" ? t("projectTree.timeFilter5h")
    : t("projectTree.timeFilter1d");
  const colorFilterActive = colorFilter.length > 0;
  // tl1157: the classic header's two filter buttons (time + colour) fold into
  // one dropdown panel — user-approved shape A (three header buttons total).
  // Every change applies immediately (no apply button): time options close the
  // panel (one-shot choice), colour toggles stay open (multi-select), and the
  // footer "clear filters" resets both at once. The trigger carries a dot
  // badge while any filter is active; the tooltip then spells out the summary.
  // The sort section moves here from the old time menu: arrangeClassicProject-
  // Tree still sorts by workbenchSortMode, so dropping it would have removed
  // the classic layout's only sorting door.
  const classicFilterActive = timeFilter !== "all" || colorFilter.length > 0;
  const classicFilterSummary = [
    timeFilter !== "all" ? timeFilterDisplayLabel : "",
    colorFilter.length > 0 ? t("projectTree.filterColorCount", { count: colorFilter.length }) : "",
  ].filter(Boolean).join(" · ");
  const classicFilterLabel = classicFilterActive ? classicFilterSummary : t("projectTree.filterMenu");
  // tl1222: the merged filter panel is now the single header filter control for
  // BOTH layouts — the workbench header dropped its separate time/colour icon
  // buttons and renders this same control. The `mode` param only gates the
  // workbench-only organize section (classic has no organize modes and its
  // rendered panel is byte-identical to the tl1157 shape).
  const renderClassicFilterControl = (mode: "classic" | "workbench") => (
    <Tooltip
      label={classicFilterLabel}
      className="project-tree__action-slot project-tree__header-action-slot project-tree__header-action-slot--filter"
    >
      <div ref={filterRef} className="project-tree__time-filter">
        <button
          ref={filterTriggerRef}
          type="button"
          className={`project-tree__header-action-btn${classicFilterActive ? " project-tree__header-action-btn--active" : ""}`}
          aria-label={classicFilterLabel}
          aria-haspopup="menu"
          aria-expanded={filterMenuOpen}
          onClick={() => {
            setWorkbenchHeaderMenu(null);
            setMenuPoint(null);
            setFilterMenuOpen(!filterMenuOpen);
          }}
        >
          <Filter size={14} aria-hidden="true" />
          {classicFilterActive && <span className="project-tree__filter-badge" aria-hidden="true" />}
        </button>
        {filterMenuOpen && (
          <div className="project-tree__time-filter-menu project-tree__filter-panel" role="menu" aria-label={t("projectTree.filterMenu")} onKeyDown={moveMenuFocus}>
            <div className="project-tree__time-filter-title">{t("projectTree.timeFilter")}</div>
            <button
              type="button"
              className={`project-tree__time-filter-opt${timeFilter === "all" ? " project-tree__time-filter-opt--on" : ""}`}
              onClick={() => { onTimeFilterChange("all"); setFilterMenuOpen(false); }}
              role="menuitem"
            >
              {t("projectTree.timeFilterAll")}
            </button>
            {(["10", "20", "1h", "3h", "5h", "1d"] as const).map((value) => (
              <button
                type="button"
                key={value}
                className={`project-tree__time-filter-opt${timeFilter === value ? " project-tree__time-filter-opt--on" : ""}`}
                onClick={() => { onTimeFilterChange(value); setFilterMenuOpen(false); }}
                role="menuitem"
              >
                {t(`projectTree.timeFilter${value}`)}
              </button>
            ))}
            <div className="project-tree__time-filter-sep" role="separator" />
            <div className="project-tree__time-filter-title">{t("projectTree.filterByColor")}</div>
            <button
              type="button"
              className={`project-tree__time-filter-opt${!colorFilterActive ? " project-tree__time-filter-opt--on" : ""}`}
              onClick={() => { setColorFilter([]); }}
              role="menuitem"
            >
              {t("projectTree.filterByColorAll")}
            </button>
            {PROJECT_COLOR_OPTIONS
              .filter((option): option is ProjectColorOption & { key: Exclude<ProjectColorKey, ""> } => Boolean(option.key))
              .map((option) => {
                const on = colorFilter.includes(option.key);
                return (
                  <button
                    type="button"
                    key={option.key}
                    className={`project-tree__color-opt${on ? " project-tree__color-opt--on" : ""}`}
                    onClick={() => {
                      // Toggle, and keep the panel open — same contract as the
                      // standalone colour menu this panel absorbs.
                      setColorFilter((current) =>
                        current.includes(option.key)
                          ? current.filter((key) => key !== option.key)
                          : [...current, option.key],
                      );
                    }}
                    role="menuitem"
                  >
                    <span className="project-tree__color-swatch" style={{ background: option.value }} aria-hidden="true" />
                    {t(`projectTree.color.${option.key}`)}
                    {on && <Check className="project-tree__color-check" size={13} aria-hidden="true" />}
                  </button>
                );
              })}
            <div className="project-tree__time-filter-sep" role="separator" />
            <div className="project-tree__time-filter-title">{t("projectTree.sortCriteria")}</div>
            <button
              type="button"
              className={`project-tree__time-filter-opt${workbenchSortMode === "updated" ? " project-tree__time-filter-opt--on" : ""}`}
              onClick={() => selectWorkbenchSortMode("updated")}
              role="menuitem"
            >
              {t("projectTree.sortByUpdatedAt")}
            </button>
            <button
              type="button"
              className={`project-tree__time-filter-opt${workbenchSortMode === "created" ? " project-tree__time-filter-opt--on" : ""}`}
              onClick={() => selectWorkbenchSortMode("created")}
              role="menuitem"
            >
              {t("projectTree.sortByCreatedAt")}
            </button>
            {mode === "workbench" && (
              <>
                {/* tl1222: the workbench organize section moved here from the
                    retired header "more" menu — same panel, same immediate-
                    apply contract. Classic (mode !== "workbench") renders
                    without it, exactly as in tl1157. */}
                <div className="project-tree__time-filter-sep" role="separator" />
                <div className="project-tree__time-filter-title">{t("projectTree.organizeSidebar")}</div>
                <button
                  type="button"
                  className={`project-tree__time-filter-opt${workbenchOrganizeMode === "project" ? " project-tree__time-filter-opt--on" : ""}`}
                  onClick={() => setWorkbenchOrganizeMode("project")}
                  role="menuitem"
                >
                  {t("projectTree.organizeByProject")}
                </button>
                <button
                  type="button"
                  className={`project-tree__time-filter-opt${workbenchOrganizeMode === "recent" ? " project-tree__time-filter-opt--on" : ""}`}
                  onClick={() => setWorkbenchOrganizeMode("recent")}
                  role="menuitem"
                >
                  {t("projectTree.organizeRecentProjects")}
                </button>
                <button
                  type="button"
                  className={`project-tree__time-filter-opt${workbenchOrganizeMode === "time" ? " project-tree__time-filter-opt--on" : ""}`}
                  onClick={() => setWorkbenchOrganizeMode("time")}
                  role="menuitem"
                >
                  {t("projectTree.organizeByTime")}
                </button>
              </>
            )}
            <div className="project-tree__time-filter-sep" role="separator" />
            <button
              type="button"
              className="project-tree__time-filter-opt project-tree__filter-clear"
              disabled={!classicFilterActive}
              onClick={() => {
                onTimeFilterChange("all");
                setColorFilter([]);
                setFilterMenuOpen(false);
              }}
              role="menuitem"
            >
              {t("projectTree.clearTimeFilter")}
            </button>
          </div>
        )}
      </div>
    </Tooltip>
  );


  // tl1222: the workbench header collapses onto the classic 3-control form
  // (user's install-acceptance feedback: "workbench's left project bar should
  // align with classic — its top is still a row of icons"). Both layouts now
  // render the SAME header: merged filter panel / collapse toggle / create
  // dropdown. `mode` only gates the organize section inside the filter panel.
  // Pre-tl1222 workbench header (6 icon buttons: time filter, colour filter,
  // collapse, create-group, more-menu, add-menu) is fully retired — every
  // live entry survives: filters+sort in the panel, organize in the panel
  // (workbench-only), creation via the dropdown, collapse toggle unchanged.
  const renderProjectHeader = (mode: "classic" | "workbench") => (
    <div className="project-tree__header">
      <span className="project-tree__header-title">
        <BriefcaseBusiness className="project-tree__header-icon" size={13} />
        {t("projectTree.workspaceTitle")}
      </span>
      <span className="project-tree__header-actions">
        {renderClassicFilterControl(mode)}
        <Tooltip label={collapseToggleLabel} className="project-tree__action-slot project-tree__header-action-slot project-tree__action-slot--collapse">
          <button
            type="button"
            className={`project-tree__collapse-all${canRestoreCollapsedView ? " project-tree__collapse-all--restore" : ""}`}
            aria-label={collapseToggleLabel}
            aria-pressed={canRestoreCollapsedView}
            disabled={!canToggleCollapsedView}
            onClick={toggleCollapsedView}
          >
            {canRestoreCollapsedView ? <ListRestart size={14} /> : <ListCollapse size={14} />}
          </button>
        </Tooltip>
        <ProjectTreeHeaderAddControl
          open={workbenchHeaderMenu === "add"} point={menuPoint} items={classicCreateItems}
          label={t("projectTree.createMenu")} disabled={addingProject}
          onOpen={(event) => openWorkbenchHeaderMenu(event, "add")} onClose={closeMenu}
        />
      </span>
    </div>
  );

  // Project rows render under their group heading; ungrouped projects follow at
  // the bottom. A group with no members still renders so a freshly created one
  // stays visible while the user fills it.
  //
  // Task 827: the group header row is wired for all four optimizations —
  // ① right-click menu (rename / dissolve with confirm), ③ the unified icon
  // column (folder glyph with the fold chevron overlaid on it), ④ drag to
  // reorder (a dedicated drag type, persisted through reorderProjectGroups).
  // The row paddingLeft is the shared tree grid: 8 + depth * 16 with depth 0,
  // so the group title starts at the same column as a project row title
  // (indent + icon column + gap).
  const renderProjectSection = (depth: number) => {
    // Task 892-B ⑤: arbitrary nesting — top level = no parent, or a parent
    // that left the roster (dangling pointer renders at top level, promote-at-
    // render, same contract as the session tree). Each child renders inside
    // its parent block at level + 1, any depth.
    const renderGroupRow = (group: ProjectGroup, level: number): ReactNode => {
        const members = pinnedTreeSections.projects.filter((node) => groupForProjectRoot(node.root)?.id === group.id);
        const collapsed = collapsedProjectGroups.has(group.id);
        const groupDropPosition = dropGroup?.id === group.id && dragGroupId !== group.id ? dropGroup?.position ?? null : null;
        // tl 1199 ②: a project drag hovering this header paints the row-level
        // "drop into" highlight — but only when the move would actually change
        // something (the dragged project is not already this group's member).
        const projectDropTarget = dragGroupId === null
          && dragProjectRoot !== null
          && dragProjectRoot !== GLOBAL_PROJECT_ORDER_KEY
          && (groupForProjectRoot(dragProjectRoot)?.id ?? null) !== group.id;
        const projectLanding = projectDropTarget && projectDropInto === group.id;
        const groupMenuOpen = groupMenuId === group.id;
        const kidGroups = groups.filter((candidate) => candidate.parent === group.id);
        return (
          <div key={group.id} className="project-tree__group">
            <div
              className={`project-tree__group-main project-tree__pgroup-main${groupDropPosition ? ` project-tree__pgroup-main--drop-${groupDropPosition}` : ""}${projectLanding ? " project-tree__pgroup-main--drop-into" : ""}${dragGroupId === group.id ? " project-tree__pgroup-main--dragging" : ""}${groupMenuOpen ? " project-tree__pgroup-main--menu-open" : ""}`}
              role="button"
              tabIndex={0}
              aria-expanded={!collapsed}
              title={group.title}
              style={{ paddingLeft: 8 + depth * 16 + level * 16 }}
              onClick={() => {
                if (renamingGroupId === group.id) return;
                toggleProjectGroup(group.id);
              }}
              onKeyDown={(event) => {
                if (renamingGroupId === group.id) return;
                if (event.key === "Enter" || event.key === " ") {
                  event.preventDefault();
                  toggleProjectGroup(group.id);
                }
                if (event.key === "ContextMenu" || (event.shiftKey && event.key === "F10")) {
                  setGroupMenuId(group.id);
                  setGroupMenuPoint(contextMenuPointFromEvent(event));
                }
              }}
              onContextMenu={(event) => {
                event.preventDefault();
                setGroupMenuId(group.id);
                setGroupMenuPoint(contextMenuPointFromEvent(event));
              }}
              draggable={projectDragEnabled}
              onDragStart={(event) => {
                if (!projectDragEnabled) return;
                const target = event.target;
                if (target instanceof Element && target.closest(".project-tree__action-slot,.project-tree__folder-action-slot")) {
                  event.preventDefault();
                  return;
                }
                event.dataTransfer.effectAllowed = "move";
                event.dataTransfer.setData(PROJECT_GROUP_DRAG_TYPE, group.id);
                setDragGroupId(group.id);
                setDropGroup(null);
              }}
              onDragOver={(event) => {
                // Only a group drag (its own MIME type) may reorder group rows.
                if (dragGroupId) {
                  if (dragGroupId === group.id) return;
                  event.preventDefault();
                  event.dataTransfer.dropEffect = "move";
                  const rect = event.currentTarget.getBoundingClientRect();
                  const position: ProjectDropPosition = event.clientY < rect.top + rect.height / 2 ? "before" : "after";
                  setDropGroup((current) => {
                    if (current?.id === group.id && current?.position === position) return current;
                    return { id: group.id, position };
                  });
                  return;
                }
                // tl 1199 ②: a project drag may land ON a group header — that
                // moves the project into the group through the exact chain the
                // row menu's 「移动到分组」 uses (handleMoveToGroup). The drag
                // is just another trigger for the same write, never a second
                // mechanism. Same-group hovers stay rejected (the standard
                // not-allowed cursor is the feedback) and the Global pseudo-
                // project never lands on a group.
                if (!dragProjectRoot || dragProjectRoot === GLOBAL_PROJECT_ORDER_KEY) return;
                if ((groupForProjectRoot(dragProjectRoot)?.id ?? null) === group.id) return;
                event.preventDefault();
                event.dataTransfer.dropEffect = "move";
                setProjectDropInto((current) => current === group.id ? current : group.id);
              }}
              onDragLeave={(event) => {
                if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
                  setDropGroup((current) => current?.id === group.id ? null : current);
                  setProjectDropInto((current) => current === group.id ? null : current);
                }
              }}
              onDrop={(event) => {
                // tl 1199 ②: a project drop on the header moves the project
                // into this group via handleMoveToGroup (same as the menu).
                const droppedProjectRoot = dragProjectRoot
                  || event.dataTransfer.getData(PROJECT_ROW_DRAG_TYPE)
                  || "";
                if (!dragGroupId && droppedProjectRoot && droppedProjectRoot !== GLOBAL_PROJECT_ORDER_KEY) {
                  event.preventDefault();
                  clearProjectDrag();
                  if ((groupForProjectRoot(droppedProjectRoot)?.id ?? null) === group.id) {
                    // Defensive: same-group drops never land (dragover rejects
                    // them), so this only fires for a stale-state edge — still
                    // report it instead of failing silently.
                    showToast(t("projectTree.projectAlreadyInGroup", { title: group.title }));
                    return;
                  }
                  handleMoveToGroup(droppedProjectRoot, group.id);
                  return;
                }
                event.preventDefault();
                const draggedId = dragGroupId || event.dataTransfer.getData(PROJECT_GROUP_DRAG_TYPE);
                const position = dropGroup?.id === group.id ? dropGroup?.position ?? "after" : "after";
                clearGroupDrag();
                if (draggedId && draggedId !== group.id) {
                  setGroups((current) => reorderProjectGroups(current, draggedId, group.id, position));
                }
              }}
              onDragEnd={clearGroupDrag}
            >
              {renamingGroupId === group.id ? <input
                autoFocus
                className="project-tree__group-input"
                value={groupRenameDraft}
                onChange={(event) => setGroupRenameDraft(event.target.value)}
                onFocus={(event) => event.target.select()}
                onKeyDown={(event) => {
                  if (event.key === "Enter") commitGroupRename(group.id);
                  if (event.key === "Escape") setRenamingGroupId(null);
                }}
                onBlur={() => commitGroupRename(group.id)}
                onClick={(event) => event.stopPropagation()}
              /> : <>
                {/* Task 827 ③: the group's icon column — one folder glyph with
                    the fold chevron overlaid (collapsed ▸, expanded rotated to
                    ▾). The column is the same width as the project-row folder
                    column, so all titles sit on one vertical grid. */}
                <span className="project-tree__group-fold-stack" aria-hidden="true">
                  <FolderInput className="project-tree__group-icon" size={14} />
                  <span className={`project-tree__group-fold${collapsed ? "" : " project-tree__group-fold--open"}`}>▸</span>
                </span>
                <span className="project-tree__group-name">{group.title}</span>
                <span className="project-tree__group-count">{members.length}</span>
              </>}
            </div>
            {/* Task 892-B ⑤: inline 子分组 composer — one level past this
                group's header, so the to-be-created child appears exactly
                where it will render. */}
            {creatingProjectSubUnder === group.id && <div
              className="project-tree__group-subgroup-create"
              style={{ paddingLeft: 8 + depth * 16 + (level + 1) * 16 }}
            >
              <input
                autoFocus
                className="project-tree__group-input"
                value={projectSubDraft}
                placeholder={t("projectTree.createSubGroupPlaceholder")}
                aria-label={t("projectTree.createSubGroup")}
                onChange={(event) => setProjectSubDraft(event.target.value)}
                onFocus={(event) => event.target.select()}
                onKeyDown={(event) => {
                  if (event.key === "Enter") commitProjectSubGroup(group.id);
                  if (event.key === "Escape") setCreatingProjectSubUnder(null);
                }}
                onBlur={() => commitProjectSubGroup(group.id)}
                onClick={(event) => event.stopPropagation()}
              />
            </div>}
            {groupMenuOpen && <ContextMenu
              open
              point={groupMenuPoint}
              items={[
                {
                  key: "rename",
                  icon: <Pencil size={13} />,
                  label: t("projectGroup.rename"),
                  onSelect: () => {
                    setGroupRenameDraft(group.title);
                    setRenamingGroupId(group.id);
                    setGroupMenuId(null);
                  },
                },
                // Task 892-B ⑤: 新建子分组 (827 form on the project tree) —
                // opens the inline composer below this group's header; Enter
                // files it as this group's child (parent pass-through), Escape
                // /blank blur cancels. Works at every nesting level.
                {
                  key: "create-subgroup",
                  icon: <FolderPlus size={13} />,
                  label: t("projectTree.createSubGroup"),
                  onSelect: () => {
                    setProjectSubDraft("");
                    setCreatingProjectSubUnder(group.id);
                    setGroupMenuId(null);
                    if (collapsed) toggleProjectGroup(group.id);
                  },
                },
                {
                  key: "dissolve",
                  icon: <XCircle size={13} />,
                  label: t("projectGroup.delete"),
                  danger: true,
                  onSelect: () => dissolveProjectGroup(group),
                },
              ]}
              minWidth={178}
              ariaLabel={t("projectGroup.rename")}
              onClose={() => setGroupMenuId(null)}
            />}
            {!collapsed && members.map((node) => renderNode(node, depth, "projects"))}
            {!collapsed && kidGroups.map((kid) => renderGroupRow(kid, level + 1))}
          </div>
        );
    };
    const topGroups = groups.filter((group) => !group.parent || !groups.some((candidate) => candidate.id === group.parent));
    return (
      <div className="project-tree__section project-tree__section--projects">
        {topGroups.map((group) => renderGroupRow(group, 0))}
        {pinnedTreeSections.projects
          .filter((node) => !groupForProjectRoot(node.root))
          .map((node) => renderNode(node, depth, "projects"))}
      </div>
    );
  };

  const renderEmptyState = () => {
    if (query.trim()) return <div className="project-tree__empty">{t("projectTree.emptyNoMatch")}</div>;
    if (timeFilter !== "all") {
      return (
        <div className="project-tree__empty">{t("projectTree.emptyNoTimeFilterMatch")}
          <button
            type="button"
            className="project-tree__empty-primary"
            onClick={() => onTimeFilterChange("all")}
          >
            {t("projectTree.clearTimeFilter")}
          </button>
        </div>
      );
    }
    return (
      <div className="project-tree__empty-state">
        <div className="project-tree__empty project-tree__empty--subtle">{t("projectTree.emptyNoProjects")}</div>
        <button
          type="button"
          className="project-tree__empty-primary"
          onClick={() => void handleAddProject()}
          disabled={addingProject}
        >
          <FolderPlus size={14} />
          <span>{t("projectTree.addProjectTooltip")}</span>
        </button>
        {/* tl1157: visible group entry for the empty tree — the header no
            longer carries a dedicated create-group button, so without this the
            only doors are the header dropdown and the (hidden until used)
            blank-area right-click. Shares the 827 NewGroupPanel; stays out of
            the header icon row. */}
        <button
          type="button"
          className="project-tree__empty-secondary"
          onClick={() => setNewGroupOpen(true)}
        >
          <FolderInput size={14} />
          <span>{t("projectGroup.createNew")}</span>
        </button>
        <ProjectTreeRemoteAction label={t("projectTree.remoteConnection")} disabled={addingProject} onClick={openRemoteConnectFlow} />
      </div>
    );
  };

  const hasTreeRows = pinnedTreeSections.pinned.length > 0 || pinnedTreeSections.projects.length > 0;
  // Task 403 (upstream #10957→#10963): tri-state body. Before the first
  // project-tree read settles, show a "reading folders" row; the "no projects
  // yet" empty state only appears once a settled read came back empty.
  const treeBody = projectTreeBodyState({ initialReadSettled: initialTreeReadSettled, hasRows: hasTreeRows });
  const renderTreeLoadingState = () => (
    <div className="project-tree__empty project-tree__loading" role="status">{t("projectTree.readingFolders")}</div>
  );

  // Report visible topics to parent after render so shortcuts match sidebar order.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => {
    onVisibleTopicsChange?.(visibleTopicsCollectorRef.current);
  });

  // Reset topic index counter and visible topics collector before each render.
  topicIndexRef.current = 0;
  visibleTopicsCollectorRef.current = [];
  const catalogNotice = sessionCatalogNotice(catalogStatus);
  // 703: repair waves claim their rows for a few hundred ms; a status poll
  // landing inside a wave must not paint "正在修复历史记录"/"历史记录稍后重试"
  // for a blink. The gate requires the repair condition to hold 2s before the
  // banner appears; startup-facing notices pass through immediately.
  const repairNoticeGateRef = useRef(createRepairNoticeGate());
  const [visibleCatalogNotice, setVisibleCatalogNotice] = useState<SessionCatalogNotice | null>(() => repairNoticeGateRef.current(catalogNotice));
  useEffect(() => {
    const next = repairNoticeGateRef.current(catalogNotice);
    setVisibleCatalogNotice((current) => (current === next ? current : next));
  }, [catalogNotice]);
  const catalogNoticeText = visibleCatalogNotice === "indexing"
    ? (catalogStatus.total <= 0 ? t("projectTree.indexing")
      : t("projectTree.indexingProgress", { done: catalogStatus.indexed, total: catalogStatus.total }))
    : visibleCatalogNotice === "repair-active"
      // Task 550 ①: the banner only shows when repairActive > 0, so the count
      // reads the same precise field — no legacy repairPending fallback.
      ? t("projectTree.repairActive", { count: catalogStatus.repairActive ?? 0 })
      : visibleCatalogNotice === "repair-deferred"
        ? t("projectTree.repairDeferred")
        : visibleCatalogNotice === "repair-blocked"
          ? t("projectTree.repairBlocked", { count: catalogStatus.repairBlocked ?? 0 })
          : `${t("projectTree.indexing")} — ${t("task.state.failed")}`;

  return (
    <div className="project-tree">
      {searchVisible && (
        <label className="project-tree__search">
          <Search size={14} />
          <input
            ref={searchInputRef}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder={t("projectTree.searchPlaceholder")}
          />
        </label>
      )}
      {visibleCatalogNotice && (
        <div className="project-tree__catalog-progress" role="status">
          <span>{catalogNoticeText}</span>
          {visibleCatalogNotice === "rebuild" && (
            <button type="button" className="project-tree__catalog-rebuild" onClick={() => void rebuildSessionCatalog()}>
              {t("projectTree.rebuildCatalog")}
            </button>
          )}
        </div>
      )}
      {compactTopics ? (
        <>
          {renderProjectHeader("workbench")}
          <div className="project-tree__list project-tree__list--workbench" onContextMenu={openBlankMenu}>
            {treeBody === "loading" ? (
              renderTreeLoadingState()
            ) : treeBody === "empty" ? (
              renderEmptyState()
            ) : (
              <>
                {pinnedTreeSections.pinned.length > 0 && (
                  <div className="project-tree__section project-tree__section--pinned">
                    <div className="project-tree__section-title project-tree__section-title--pinned">
                      <Pin size={14} className="project-tree__section-title-icon" aria-hidden="true" />
                      <span>{t("projectTree.pinnedTitle")}</span>
                    </div>
                    {pinnedTreeSections.pinned.map((node) => renderNode(node, 0, "pinned"))}
                  </div>
                )}
                {renderProjectSection(0)}
              </>
            )}
          </div>
        </>
      ) : (
        <>
          {renderProjectHeader("classic")}
          <div className="project-tree__list" onScroll={cancelHoverCard} onContextMenu={openBlankMenu}>
            {treeBody === "loading" ? (
              renderTreeLoadingState()
            ) : treeBody === "empty" ? (
              renderEmptyState()
            ) : (
              <>
                {pinnedTreeSections.pinned.length > 0 && (
                  <div className="project-tree__section project-tree__section--pinned">
                    <div className="project-tree__section-title">{t("projectTree.pinnedTitle")}</div>
                    {pinnedTreeSections.pinned.map((node) => renderNode(node, 1, "pinned"))}
                  </div>
                )}
                {renderProjectSection(0)}
              </>
            )}
          </div>
        </>
      )}
      {hoverCard && createPortal(
        <div
          className="project-tree__hover-card"
          style={{ left: hoverCard.left, top: hoverCard.top }}
          aria-hidden="true"
        >
          <div className="project-tree__hover-card-title">{hoverCard.card.title}</div>
          {hoverCard.card.statusLabel && (
            <div className="project-tree__hover-card-status">{hoverCard.card.statusLabel}</div>
          )}
          {/* 938：职责行——实时读缓存（拉取晚到也即时补现），pre-wrap 换行不裁剪。 */}
          {(hoverCard.card.purpose || (hoverCard.sessionPath ? cachedSessionPurpose(hoverCard.sessionPath) : "")) && (
            <div className="project-tree__hover-card-purpose">
              {hoverCard.sessionPath ? cachedSessionPurpose(hoverCard.sessionPath) ?? hoverCard.card.purpose : hoverCard.card.purpose}
            </div>
          )}
          <div className="project-tree__hover-card-meta">
            {[hoverCard.card.metaLine, hoverCard.card.exactTime].filter(Boolean).join(" · ")}
          </div>
          {hoverCard.card.projectLabel && (
            <div className="project-tree__hover-card-project">
              <Folder size={12} aria-hidden="true" />
              <span>{hoverCard.card.projectLabel}</span>
            </div>
          )}
        </div>,
        document.body,
      )}
      {blankProjectFlow}
      {remoteConnectFlow}
      {/* 938：「编辑职责」弹窗——锚定右键的那一行，AnchoredPopover portal 到
          body（529 先例防侧栏滚动裁剪）；保存成功写 hover 缓存即时反映。 */}
      <SessionPurposeEditor
        open={purposeEditorSessionPath !== null}
        anchorRef={purposeEditorAnchorRef}
        sessionPath={purposeEditorSessionPath ?? ""}
        initialPurpose={purposeEditorSessionPath ? cachedSessionPurpose(purposeEditorSessionPath) : undefined}
        onClose={() => setPurposeEditorSessionPath(null)}
        onSaved={(purpose) => {
          if (purposeEditorSessionPath) setCachedSessionPurpose(purposeEditorSessionPath, purpose);
          setPurposeEditorSessionPath(null);
        }}
      />
      {/* Task 892-B ⑤: project-group delete proposal dialog (portal targets
          body; the element just has to mount in the component owning the hook). */}
      {projectGroupDeleteDialog.dialog}
      <NewGroupPanel open={newGroupOpen} onClose={() => setNewGroupOpen(false)} onConfirm={handleAddGroup} />
      {/* Task 881 ①: blank-area right-click — the create-group door that works
          with zero projects and zero groups on screen. */}
      <ContextMenu
        open={blankMenuPoint !== null}
        point={blankMenuPoint}
        items={blankMenuItems}
        minWidth={178}
        ariaLabel={t("projectTree.newGroup")}
        onClose={() => setBlankMenuPoint(null)}
      />
      {/* tl 1149: the MoveToGroupPanel floating panel is retired — the project
          context menu's "move to group" row now carries the same options as an
          in-menu submenu (projectGroupMoveItems), anchored to the row. */}
    </div>
  );
}
