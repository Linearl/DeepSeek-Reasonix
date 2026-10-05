// TabBar renders the browser-like workspace tab strip. Each tab represents one
// open project/global topic, so switching tabs switches the active conversation.
import { crossGroupDropIntent, isSplitViewEnabled, onSplitViewEnabledChange } from "../lib/splitView";
import { Fragment, useEffect, useMemo, useRef, useState } from "react";
import type { CSSProperties, DragEvent, KeyboardEvent as ReactKeyboardEvent, MouseEvent as ReactMouseEvent } from "react";
import { CheckCheck, FileText, Plus, Search, X } from "lucide-react";
import { normalizeCollaborationMode, normalizeMode, normalizeToolApprovalMode, type Mode, type TabMeta } from "../lib/types";
import { projectColorValue } from "../lib/projectColors";
import { prefetchTabTranscript } from "../lib/transcriptPrefetch";
import { useT } from "../lib/i18n";
import { Tooltip } from "./Tooltip";
import { ContextMenu, contextMenuPointFromEvent, type ContextMenuItem, type ContextMenuPoint } from "./ContextMenu";
import { WorktreeBadge } from "./WorktreeBadge";
import { selectCloseInactiveIds, selectCloseOtherIds, selectCloseRightIds } from "../lib/tabClosePolicy";
// wt-zcode-288：标签页右键「全部已读」——读档/写档与未读判定统一走 readActivity 存档。
import { markTabsAllRead } from "../lib/readActivity";
// 任务 506：标签栏自适应压缩开关（experimental_tab_compress，默认关）。
import { labFlagEnabled, onLabFlagsChange } from "../lib/labFlags";

interface TabBarProps {
  tabs: TabMeta[];
  activeTabId?: string;
  onTabChange: (tabId: string) => void;
  onTabClose: (tabId: string) => void;
  onTabsClose: (tabIds: string[], nextActiveTabId?: string) => void;
  onTabStopAndClose?: (tabId: string) => void;
  onTabsReorder: (tabIds: string[]) => void;
  onNewTab: () => void;
  onOpenPalette?: () => void;
  commandCompact?: boolean;
  revealActiveSignal?: number;
  /** Tab currently shown in the secondary pane, when a split is open (task 70). */
  splitTabId?: string | null;
  /** Toggle the split for this tab; the secondary pane holds one other tab. */
  onToggleSplit?: (tabId: string) => void;
}

type DropSide = "before" | "after";

/** Task 123 (S5): hover intent is a 150 ms debounce — long enough that dragging
 * the pointer across the strip does not start speculative reads, short enough
 * that a real click still lands on a warm tab. */
const HOVER_PREFETCH_DEBOUNCE_MS = 150;

/**
 * 任务 506 降宽档位（量级判断，实施前已报备）：
 * - 176px 固定宽 × 8 个 ≈ 1.44k px，恰好放满 1536 逻辑 px 的最大化窗口
 *   （1920@125% 常见开发环境），所以从第 9 个开始降；
 * - 每档容纳 4 个（9/13/17/21 等差），避免频繁跳档重排；148px 即既有
 *   窄窗口（≤980px）档宽，保持同一把尺子；
 * - 下限 84px：状态点(7px) + 左右内边距压缩后仍剩 ~46px 标签文案
 *   （约 3~4 个汉字），再低就只剩色点无法辨认——那之后是搜索/图墙（505）
 *   的保底范围；
 * - 17 个起隐藏 plan/goal/auto/yolo 文本徽章（宽度过小徽章挤掉标题），
 *   模式信息始终保留在 hover title 里。
 * 宽度以 inline `--tabbar-tab-width` 注入 `.tabbar` 根节点：行内样式压过
 * 所有样式面（darwin/native-tabs/theme-style 共 5 处消费块），开关关闭时
 * 不注入 = 逐像素等价旧行为。
 */
export const TAB_COMPRESS_TIERS = [
  { minTabs: 9, widthPx: 148 },
  { minTabs: 13, widthPx: 122 },
  { minTabs: 17, widthPx: 100 },
  { minTabs: 21, widthPx: 84 },
] as const;

/** 给定标签数返回档位：0=不压缩，1..4=TAB_COMPRESS_TIERS 下标+1。 */
export function tabCompressTier(tabCount: number): number {
  let tier = 0;
  for (let index = 0; index < TAB_COMPRESS_TIERS.length; index += 1) {
    if (tabCount >= TAB_COMPRESS_TIERS[index].minTabs) tier = index + 1;
  }
  return tier;
}

function tabDisplayTitle(tab: TabMeta): string {
  if (tab.tabType === "file" || tab.scope === "file") return tab.topicTitle?.trim() || tab.filePath?.split("/").filter(Boolean).pop() || "File";
  const title = tab.topicTitle?.trim();
  if (tab.scope === "global") return title || "Global";
  return title || "Untitled";
}

function tabFullTitle(tab: TabMeta): string {
  if (tab.tabType === "file" || tab.scope === "file") return tab.filePath || tabDisplayTitle(tab);
  if (tab.scope === "global") {
    const title = tabDisplayTitle(tab);
    const workspaceName = tab.workspaceName?.trim() || "Global";
    return title === workspaceName ? workspaceName : `${workspaceName} / ${title}`;
  }
  const workspaceName = tab.workspaceName?.trim() || "Project";
  return `${workspaceName} / ${tabDisplayTitle(tab)}`;
}

function tabMode(tab: TabMeta): Mode {
  return normalizeMode(tab.mode);
}

function projectAccentStyle(color?: string): CSSProperties | undefined {
  const value = projectColorValue(color);
  if (!value) return undefined;
  return { "--project-accent": value } as CSSProperties;
}

export function TabBar({ tabs, activeTabId, onTabChange, onTabClose, onTabsClose, onTabStopAndClose, onTabsReorder, onNewTab, onOpenPalette, commandCompact = false, revealActiveSignal = 0, splitTabId = null, onToggleSplit }: TabBarProps) {
  // Task 70-1: the split is an experiment - with the switch off the menu below is
  // exactly the pre-split list.
  const [splitViewEnabled, setSplitViewEnabled] = useState(isSplitViewEnabled());
  useEffect(() => onSplitViewEnabledChange(setSplitViewEnabled), []);
  // 任务 506：压缩开关走 lab 模块门（设置保存即重放快照，无需重启），
  // 关闭时 tier 恒为 0，不注入任何行内样式。
  const [tabCompressEnabled, setTabCompressEnabled] = useState(labFlagEnabled("tabCompress"));
  useEffect(() => onLabFlagsChange(() => setTabCompressEnabled(labFlagEnabled("tabCompress"))), []);
  const t = useT();
  const [draggingTabId, setDraggingTabId] = useState<string | null>(null);
  const [dropTarget, setDropTarget] = useState<{ id: string; side: DropSide } | null>(null);
  const [menuTabId, setMenuTabId] = useState<string | null>(null);
  const [menuPoint, setMenuPoint] = useState<ContextMenuPoint | null>(null);
  const suppressClickRef = useRef(false);
  const tabRefs = useRef(new Map<string, HTMLButtonElement>());
  const hoverPrefetchTimer = useRef<number | null>(null);
  const backendActiveTabId = tabs.find((tab) => tab.active)?.id;
  const activeTabIdExists = Boolean(activeTabId && tabs.some((tab) => tab.id === activeTabId));
  const resolvedActiveTabId = activeTabIdExists ? activeTabId : backendActiveTabId;
  const tabOrderKey = tabs.map((tab) => tab.id).join("\u0000");

  useEffect(() => {
    if (!resolvedActiveTabId) return;
    const frame = window.requestAnimationFrame(() => {
      tabRefs.current.get(resolvedActiveTabId)?.scrollIntoView({
        block: "nearest",
        inline: "nearest",
      });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [backendActiveTabId, resolvedActiveTabId, revealActiveSignal, tabOrderKey]);

  useEffect(() => () => cancelHoverPrefetch(), []);

  const handleClose = (tabId: string) => {
    onTabClose(tabId);
  };

  const clearDragState = () => {
    setDraggingTabId(null);
    setDropTarget(null);
  };

  const dropSideForEvent = (event: DragEvent<HTMLButtonElement>): DropSide => {
    const rect = event.currentTarget.getBoundingClientRect();
    return event.clientX > rect.left + rect.width / 2 ? "after" : "before";
  };

  const reorderTabIds = (draggedId: string, targetId: string, side: DropSide): string[] => {
    const ids = tabs.map((tab) => tab.id);
    const from = ids.indexOf(draggedId);
    const target = ids.indexOf(targetId);
    if (from < 0 || target < 0 || draggedId === targetId) return ids;
    const next = ids.filter((id) => id !== draggedId);
    const targetAfterRemoval = next.indexOf(targetId);
    const insertAt = side === "after" ? targetAfterRemoval + 1 : targetAfterRemoval;
    next.splice(insertAt, 0, draggedId);
    return next;
  };

  const handleDragStart = (event: DragEvent<HTMLButtonElement>, tabId: string) => {
    setDraggingTabId(tabId);
    setDropTarget(null);
    event.dataTransfer.effectAllowed = "move";
    event.dataTransfer.setData("text/plain", tabId);
  };

  const handleDragOver = (event: DragEvent<HTMLButtonElement>, tabId: string) => {
    if (!draggingTabId || draggingTabId === tabId) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "move";
    setDropTarget({ id: tabId, side: dropSideForEvent(event) });
  };

  const handleDrop = (event: DragEvent<HTMLButtonElement>, tabId: string) => {
    event.preventDefault();
    const draggedId = draggingTabId || event.dataTransfer.getData("text/plain");
    const side = dropTarget?.id === tabId ? dropTarget.side : dropSideForEvent(event);
    clearDragState();
    if (!draggedId || draggedId === tabId) return;
    // Task 70 二期: with a split open a drop that lands on the OTHER group's
    // tab moves the tab across instead of reordering inside one group. Both
    // directions ride the same toggle the context menu uses: replacing the
    // secondary, or clearing it (which closes the split).
    const cross = crossGroupDropIntent(draggedId, tabId, splitTabId);
    if (cross && onToggleSplit) {
      suppressClickRef.current = true;
      onToggleSplit(draggedId);
      return;
    }
    const next = reorderTabIds(draggedId, tabId, side);
    if (next.join("\u0000") !== tabs.map((tab) => tab.id).join("\u0000")) {
      suppressClickRef.current = true;
      onTabsReorder(next);
    }
  };

  const cancelHoverPrefetch = () => {
    if (hoverPrefetchTimer.current === null) return;
    window.clearTimeout(hoverPrefetchTimer.current);
    hoverPrefetchTimer.current = null;
  };

  const scheduleHoverPrefetch = (tab: TabMeta) => {
    cancelHoverPrefetch();
    if (tab.id === resolvedActiveTabId) return;
    const sessionPath = (tab.sessionPath ?? "").trim();
    if (!sessionPath) return;
    hoverPrefetchTimer.current = window.setTimeout(() => {
      hoverPrefetchTimer.current = null;
      prefetchTabTranscript({
        tabId: tab.id,
        sessionPath,
        revision: tab.sessionRevision,
        digest: tab.sessionDigest,
      });
    }, HOVER_PREFETCH_DEBOUNCE_MS);
  };

  const handleTabClick = (tabId: string) => {
    if (suppressClickRef.current) {
      suppressClickRef.current = false;
      return;
    }
    onTabChange(tabId);
  };

  const handleTabAuxClick = (event: ReactMouseEvent<HTMLButtonElement>, tabId: string) => {
    // DOM MouseEvent.button: 1 is the auxiliary button, normally the middle/wheel button.
    if (event.button !== 1) return;
    event.preventDefault();
    event.stopPropagation();
    handleClose(tabId);
  };

  const openTabMenu = (event: ReactMouseEvent<HTMLButtonElement> | ReactKeyboardEvent<HTMLButtonElement>, tabId: string) => {
    event.preventDefault();
    event.stopPropagation();
    setMenuTabId(tabId);
    setMenuPoint(contextMenuPointFromEvent(event));
  };

  const closeTabMenu = () => {
    setMenuTabId(null);
    setMenuPoint(null);
  };

  const closeTabsFromMenu = (tabIds: string[], nextActiveTabId?: string) => {
    closeTabMenu();
    onTabsClose(tabIds, nextActiveTabId);
  };

  const menuTabIndex = menuTabId ? tabs.findIndex((tab) => tab.id === menuTabId) : -1;
  const tabMenuItems: ContextMenuItem[] = menuTabId && menuTabIndex >= 0
    ? [
        ...(splitViewEnabled
          ? [{
              key: "split-view",
              label: splitTabId && splitTabId === menuTabId ? t("tabBar.closeSplitView") : t("tabBar.splitView"),
              disabled: tabs.length <= 1,
              onSelect: () => onToggleSplit?.(menuTabId),
            }]
          : []),
        {
          key: "mark-all-read",
          icon: <CheckCheck size={13} />,
          // wt-zcode-288：当前上下文 = 打开的全部标签页对应的会话。
          label: t("projectTree.markAllRead"),
          onSelect: () => markTabsAllRead(tabs),
        },
        {
          key: "close-current",
          label: t("tabBar.closeTab"),
          disabled: tabs.length <= 1,
          onSelect: () => closeTabsFromMenu([menuTabId]),
        },
        ...(onTabStopAndClose
          ? [{
              key: "stop-and-close",
              label: t("tabBar.stopAndCloseTab"),
              disabled: tabs.length <= 1,
              onSelect: () => {
                const target = menuTabId;
                closeTabMenu();
                if (target) onTabStopAndClose(target);
              },
            }]
          : []),
        {
          key: "close-other",
          label: t("tabBar.closeOtherTabs"),
          disabled: tabs.length <= 1,
          onSelect: () => {
            const target = selectCloseOtherIds(tabs, menuTabId);
            closeTabsFromMenu(target.ids, target.nextActiveTabId);
          },
        },
        {
          // 任务 368：关闭非活跃标签页——保留的是「当前活跃标签」而非右击的
          // 那个（对齐 Chrome「关闭其他标签页」语义，锚点=活跃而非锚点=右击）。
          // 无副作用：批量关闭走 keep_running（任务 223），会话零删除，无确认弹窗。
          key: "close-inactive",
          label: t("tabBar.closeInactiveTabs"),
          disabled: tabs.length <= 1,
          onSelect: () => {
            const target = selectCloseInactiveIds(tabs, resolvedActiveTabId);
            closeTabsFromMenu(target.ids, target.nextActiveTabId);
          },
        },
        {
          key: "close-right",
          label: t("tabBar.closeTabsToRight"),
          disabled: menuTabIndex >= tabs.length - 1,
          onSelect: () => {
            const target = selectCloseRightIds(tabs, menuTabIndex, menuTabId, resolvedActiveTabId);
            closeTabsFromMenu(target.ids, target.nextActiveTabId);
          },
        },
      ]
    : [];

  // Task 70-4: with a split open the strip reads as two groups - the primary pane's
  // tabs first, then a divider, then the tab shown in the secondary pane.
  const orderedTabs = useMemo(() => {
    if (!splitTabId) return tabs;
    const secondary = tabs.find((tab) => tab.id === splitTabId);
    if (!secondary) return tabs;
    return [...tabs.filter((tab) => tab.id !== splitTabId), secondary];
  }, [tabs, splitTabId]);

  // 任务 506：开关开启且标签数过档时，把档位宽度写进行内 `--tabbar-tab-width`；
  // tier 0（含开关关闭）不注入任何样式 = 与旧渲染逐像素等价。
  const compressTier = tabCompressEnabled ? tabCompressTier(orderedTabs.length) : 0;
  const compressStyle = compressTier > 0
    ? ({ "--tabbar-tab-width": `${TAB_COMPRESS_TIERS[compressTier - 1].widthPx}px` } as CSSProperties)
    : undefined;
  // 17 个起（tier 3+）文本徽章不再渲染：宽度不足时徽章会挤掉标题，
  // 模式信息由 hover title（stateTitle）完整承接。
  const badgesVisible = compressTier > 0 && compressTier < 3;

  return (
    <div
      className="tabbar"
      style={compressStyle}
      data-tab-compress={tabCompressEnabled ? "on" : undefined}
      data-tab-tier={compressTier > 0 ? compressTier : undefined}
    >
      <div className="tabbar__tabs">
        {orderedTabs.map((tab) => {
          const displayTitle = tabDisplayTitle(tab);
          const fullTitle = tabFullTitle(tab);
          const mode = tabMode(tab);
          const collaborationMode = normalizeCollaborationMode(tab.collaborationMode, tab.goal, mode);
          const planMode = collaborationMode === "plan";
          const goalMode = collaborationMode === "goal";
          const toolApprovalMode = normalizeToolApprovalMode(tab.toolApprovalMode, mode);
          const stateTitle = [
            tab.running ? "Running" : "",
            planMode ? "Plan" : "",
            goalMode ? "Goal" : "",
            toolApprovalMode === "auto" ? "Auto approve" : "",
            toolApprovalMode === "yolo" ? "YOLO approval" : "",
          ].filter(Boolean).join(" · ");
          const annotatedTitle = stateTitle ? `${stateTitle} · ${fullTitle}` : fullTitle;
          return (
            <Fragment key={tab.id}>
              {splitTabId === tab.id && <span className="tabbar__split-divider" aria-hidden="true" />}
            <button
              ref={(node) => {
                if (node) {
                  tabRefs.current.set(tab.id, node);
                } else {
                  tabRefs.current.delete(tab.id);
                }
              }}
              draggable
              className={[
                "tabbar__tab",
                tab.id === resolvedActiveTabId ? "tabbar__tab--active" : "",
                tab.running ? "tabbar__tab--running" : "",
                toolApprovalMode === "yolo" ? "tabbar__tab--yolo" : "",
                draggingTabId === tab.id ? "tabbar__tab--dragging" : "",
                dropTarget?.id === tab.id ? `tabbar__tab--drop-${dropTarget.side}` : "",
              ].filter(Boolean).join(" ")}
              title={annotatedTitle}
              aria-label={annotatedTitle}
              style={projectAccentStyle(tab.projectColor)}
              onClick={() => handleTabClick(tab.id)}
              onAuxClick={(event) => handleTabAuxClick(event, tab.id)}
              onMouseEnter={() => scheduleHoverPrefetch(tab)}
              onMouseLeave={cancelHoverPrefetch}
              onMouseDown={(event) => {
                // Prevent the browser/webview middle-click auto-scroll before auxclick fires.
                if (event.button === 1) event.preventDefault();
              }}
              onContextMenu={(event) => openTabMenu(event, tab.id)}
              onKeyDown={(event) => {
                if (event.key === "ContextMenu" || (event.shiftKey && event.key === "F10")) {
                  openTabMenu(event, tab.id);
                }
              }}
              onDragStart={(event) => handleDragStart(event, tab.id)}
              onDragOver={(event) => handleDragOver(event, tab.id)}
              onDrop={(event) => handleDrop(event, tab.id)}
              onDragEnd={clearDragState}
            >
              {tab.tabType === "file" || tab.scope === "file" ? (
                <FileText size={12} className="tabbar__file-icon" />
              ) : (
                <span
                  className={[
                    "tabbar__status",
                    tab.running ? "tabbar__status--running" : "",
                  ].filter(Boolean).join(" ")}
                />
              )}
              <span className="tabbar__tab-label">{displayTitle}</span>
              {tab.isolatedWorktree && <WorktreeBadge size={11} />}
              {badgesVisible && planMode && <span className="tabbar__mode-badge tabbar__mode-badge--plan">plan</span>}
              {badgesVisible && goalMode && <span className="tabbar__mode-badge tabbar__mode-badge--plan">goal</span>}
              {badgesVisible && toolApprovalMode === "auto" && <span className="tabbar__mode-badge tabbar__mode-badge--plan">auto</span>}
              {badgesVisible && toolApprovalMode === "yolo" && <span className="tabbar__mode-badge tabbar__mode-badge--yolo">yolo</span>}
              <span
                className="tabbar__tab-close"
                onClick={(e) => {
                  e.stopPropagation();
                  handleClose(tab.id);
                }}
              >
                <X size={10} />
              </span>
            </button>
            </Fragment>
          );
        })}
      </div>
      <Tooltip label={t("tabBar.newSession")}>
        <button className="tabbar__new" type="button" aria-label={t("tabBar.newSession")} onClick={onNewTab}>
          <Plus size={13} />
        </button>
      </Tooltip>
      {onOpenPalette && <span className="tabbar__spacer" aria-hidden="true" />}
      {onOpenPalette && (
        <button
          className={["tabbar__command", commandCompact ? "tabbar__command--compact" : ""].filter(Boolean).join(" ")}
          type="button"
          onClick={onOpenPalette}
          aria-label={t("palette.placeholder")}
          title={t("palette.placeholder")}
        >
          <Search size={commandCompact ? 16 : 13} className="tabbar__command-icon" />
          {!commandCompact && (
            <>
              <span className="tabbar__command-text tabbar__command-text--full">{t("tabBar.commandSearch")}</span>
              <span className="tabbar__command-text tabbar__command-text--compact">{t("tabBar.commandSearchCompact")}</span>
              <kbd className="tabbar__command-kbd">⌘K</kbd>
            </>
          )}
        </button>
      )}
      <ContextMenu
        open={Boolean(menuTabId)}
        point={menuPoint}
        items={tabMenuItems}
        minWidth={170}
        ariaLabel={t("tabBar.tabActions")}
        onClose={closeTabMenu}
      />
    </div>
  );
}
